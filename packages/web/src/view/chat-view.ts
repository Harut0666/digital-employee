// 会话 → 对话流。纯函数，不依赖 React 与网络。
// 消息和卡片都来自后端的会话接口，这里只决定显示成什么、哪张卡片还能操作。
// 任务卡片上的按钮由后端在读取时配上（只配给当前暂停的卡片），前端不推算。

import type { DeliverableRef, QcCheckResult, TaskEvent, TaskState } from "@de/core/contract";
import { deriveStepRows, type StepRow, type StepRowDef } from "./step-rows";
import type { AgentToolView, PauseView, SessionAttachment, SessionCard, SessionDetail, SessionEmployee, TaskAction, UiRole } from "../api/types";
import { modelNotices, selectedModelNotice } from "./session-model";
import { derivePermissionCard, fileChangeOf, permissionModeLabel, traceOf, type FileChangeView, type PermissionCardView, type PermissionTrace } from "./permission-mode";
import { latestTaskId } from "./task-runs";
import { deriveTodoCard, latestTodoId, type TodoCardView } from "./todo";
import { deriveQuestionCard, type QuestionCardView } from "./question";

interface ItemBase {
  /** React key，取自消息 ID */
  key: string;
  ts: string;
}

export type ChatItem =
  | (ItemBase & { kind: "user"; text: string; role?: UiRole })
  | (ItemBase & { kind: "agent"; text: string })
  // 模型切换说明（T39）不用单独的 kind：是程序说明，按 kind: "notice" 显示
  | (ItemBase & { kind: "notice"; text: string })
  /** 旧会话里已发出的“确认员工”卡（T49 之前）：只读显示，不再有按钮 */
  | (ItemBase & {
      kind: "employee_confirm";
      proposed?: string;
      source: NonNullable<SessionCard["source"]>;
      reason?: string;
      candidates: SessionEmployee[];
      active: false;
    })
  /** 表单卡：submitted 是任务里的表单；superseded 是旧会话里已发出的草稿表单（T49 之前，只读） */
  | (ItemBase & {
      kind: "form";
      employee: string;
      draft: Record<string, unknown>;
      attachments: SessionAttachment[];
      mode: "superseded" | "submitted";
    })
  /** 员工表单（T49）：run_employee 弹出的结构化表单，人核对、补充、确认开工或“不是这个员工” */
  | (ItemBase & { kind: "employee_form"; card: SessionCard; writable: boolean })
  | (ItemBase & { kind: "pause"; pause?: PauseView; actions: TaskAction[]; active: boolean })
  | (ItemBase & { kind: "qc"; passed: boolean; checks: QcCheckResult[] })
  /** 送审提示：审核在右栏进行，对话里只说一句 */
  | (ItemBase & { kind: "review_notice"; pause?: PauseView; active: boolean })
  /** 使用目录文件：前台 Agent 提议把人工作区目录里的文件当材料，人确认后才复制进会话 */
  | (ItemBase & { kind: "use_file"; path: string; name: string; size?: number; reason?: string; confirmed: boolean; actions: TaskAction[]; active: boolean })
  /** Agent 在对话里起草的退回意见，审核人确认后才退回；只带退回这一个操作 */
  | (ItemBase & { kind: "return_confirm"; actions: TaskAction[]; active: boolean; comment: string; sections?: string[] })
  /** 交付物；会话里有多个任务时带着任务号和第几次运行（下载取这个任务的交付物） */
  | (ItemBase & { kind: "delivered"; deliverables: DeliverableRef[]; taskId?: string; run?: number })
  /** 早先任务的步骤行（会话里先后运行过多个任务）：留在这个任务开工的位置，只读 */
  | (ItemBase & { kind: "past_steps"; taskId: string; run: number; rows: StepRow[] })
  /** 权限确认（T48）：Agent 想做的动作和三个答复按钮；答复后只剩一行结果 */
  | (ItemBase & { kind: "permission" } & PermissionCardView)
  /** Agent 写的文件：一行说明，点开看改动 */
  | (ItemBase & { kind: "file_change" } & FileChangeView)
  /** 待办清单（todo_write）：一张紧凑的卡，最新的一张展开，旧的收起 */
  | (ItemBase & { kind: "todo" } & TodoCardView)
  /** 提问（ask_user_question）：Agent 向发起人提的结构化问题 */
  | (ItemBase & { kind: "question"; card: SessionCard; view: QuestionCardView })
  /** 自动放行、被拒绝的留痕 */
  | (ItemBase & { kind: "trace" } & PermissionTrace)
  /** 正在说的一轮里已经调用的工具（可折叠成一行） */
  | (ItemBase & { kind: "tools"; turn: number; items: AgentToolView[] })
  /** 正在说的一轮里已经说出的部分（还没写成会话里的消息） */
  | (ItemBase & { kind: "streaming"; turn: number; text: string })
  | (ItemBase & {
      kind: "filing";
      taskId: string;
      title: string;
      description: string;
      draftFailed: boolean;
      filed?: string;
      /** 还没入库：可以改描述并确认 */
      active: boolean;
      /** 能请模型起草描述（只读会话不能写会话日志） */
      canDraft: boolean;
    });

/** 会话绑定的任务现状（取自任务快照），用来在交付后给出交付物 */
export interface ChatTask {
  state: TaskState;
  deliverables: DeliverableRef[];
}

/** 早先的一个任务（会话里先后运行过多个任务）：对话里它的步骤行和交付物按各自的时间点显示 */
export interface PastRun {
  taskId: string;
  /** 第几次运行（从 1 开始） */
  run: number;
  state: TaskState;
  deliverables: DeliverableRef[];
  events: readonly TaskEvent[];
  steps: readonly StepRowDef[];
}

/** 正在说的一轮（来自会话事件流）：说完之后它的内容会变成会话里的消息 */
export interface LiveTurn {
  turn: number;
  text: string;
  tools: AgentToolView[];
  /** 这一轮已经开始 */
  running?: boolean;
}

function noticeItem(notice: { key: string; ts: string; text: string }): ChatItem {
  return { key: notice.key, ts: notice.ts, kind: "notice", text: notice.text };
}

/** 一条说明按时间插进前面：同一时间点的插在已有消息之后。 */
function insertByTime(items: ChatItem[], item: ChatItem): void {
  const at = Date.parse(item.ts);
  let start = 0;
  let end = items.length;
  while (start < end) {
    const middle = (start + end) >> 1;
    if (Date.parse(items[middle]?.ts ?? "") <= at) start = middle + 1;
    else end = middle;
  }
  items.splice(start, 0, item);
}

export function deriveChat(
  session: SessionDetail,
  task?: ChatTask,
  events: readonly TaskEvent[] = [],
  live?: LiveTurn,
  past: readonly PastRun[] = [],
  role: UiRole = "initiator",
): ChatItem[] {
  const items = deriveMessages(session, task, events, past, role);
  if (!live || (!live.text && live.tools.length === 0)) return items;
  // 还没写成消息的这一轮排在最后一条之后
  const ts = session.updatedAt;
  if (live.tools.length > 0) {
    // 每次调用的工具序号一变就换一个 key：折叠状态只对这一批调用有效
    items.push({ key: `tools-${live.turn}-${live.tools.length}`, ts, kind: "tools", turn: live.turn, items: live.tools });
  }
  if (live.text) items.push({ key: `streaming-${live.turn}-${live.text.length}`, ts, kind: "streaming", turn: live.turn, text: live.text });
  return items;
}

function deriveMessages(session: SessionDetail, task: ChatTask | undefined, events: readonly TaskEvent[], past: readonly PastRun[], role: UiRole): ChatItem[] {
  const items: ChatItem[] = [];
  const writable = !session.readOnly;
  // 入库确认卡片排在交付物之后：先能下载，再决定要不要入库
  const filings: ChatItem[] = [];

  session.messages.forEach((m) => {
    const base = { key: m.id, ts: m.ts };
    switch (m.kind) {
      case "user":
        items.push({ ...base, kind: "user", text: m.text ?? "", role: m.role });
        break;
      case "assistant":
        items.push({ ...base, kind: "agent", text: m.text ?? "" });
        break;
      case "notice":
        items.push({ ...base, kind: "notice", text: m.text ?? "" });
        break;
      case "card":
        if (m.card?.type === "knowledge_filing") filings.push(filingItem(base, m.card));
        else if (m.card) items.push(cardItem(base, m.card));
        break;
      case "todo_written":
        items.push({ ...base, kind: "todo", ...deriveTodoCard(m, m.id === latestTodoId(session.messages)) });
        break;
      case "permission_mode_set":
        items.push({ ...base, kind: "notice", text: `权限模式切换为「${permissionModeLabel(m.mode)}」` });
        break;
      case "tool_called": {
        // 写文件的工具行：点开看改了什么；别的工具留痕只进会话日志
        const change = fileChangeOf(m);
        if (change) items.push({ ...base, kind: "file_change", ...change });
        break;
      }
      case "permission_allowed":
      case "tool_denied": {
        const trace = traceOf(m);
        if (trace) items.push({ ...base, kind: "trace", ...trace });
        break;
      }
      default:
        // 工具留痕和模型失败只进会话日志，不进对话
        break;
    }
  });
  if (task?.state === "delivered" && task.deliverables.length > 0) {
    const ts = session.messages.at(-1)?.ts ?? session.updatedAt;
    items.push({ key: "delivered", ts, kind: "delivered", deliverables: task.deliverables });
  }
  // 早先的任务：步骤行紧跟在它开工的位置，交付物在它交付的时间点
  for (const run of past) {
    const rows = deriveStepRows(run.events, run.steps, { fallbackModel: undefined });
    const first = run.events[0];
    if (first && rows.some((row) => row.status !== "pending")) {
      insertByTime(items, { key: `steps-${run.taskId}`, ts: first.ts, kind: "past_steps", taskId: run.taskId, run: run.run, rows });
    }
    const deliveredEvent = run.events.find((e) => e.type === "state_changed" && e.to === "delivered");
    if (run.state === "delivered" && run.deliverables.length > 0 && deliveredEvent) {
      insertByTime(items, { key: `delivered-${run.taskId}`, ts: deliveredEvent.ts, kind: "delivered", deliverables: run.deliverables, taskId: run.taskId, run: run.run });
    }
  }
  // 入库确认卡片：当前任务的排在最后；早先任务的紧跟在它交付之后
  const latest = latestTaskId(session);
  for (const filing of filings) {
    if (filing.kind === "filing" && filing.taskId !== latest && past.some((run) => run.taskId === filing.taskId)) insertByTime(items, filing);
    else items.push(filing);
  }

  // 模型切换说明（T39）：会话里选的模型、任务运行中的切换，按时间插进对话
  const selected = selectedModelNotice(session);
  if (selected) items.push(noticeItem(selected));
  for (const notice of modelNotices(events)) insertByTime(items, noticeItem(notice));
  return items;

  function filingItem(base: ItemBase, card: SessionCard): ChatItem {
    return {
      ...base,
      kind: "filing",
      taskId: card.taskId ?? session.taskId ?? "",
      title: card.title ?? "",
      description: card.description ?? "",
      draftFailed: card.draftFailed ?? false,
      filed: card.filed,
      active: !card.filed,
      canDraft: writable,
    };
  }

  function cardItem(base: ItemBase, card: SessionCard): ChatItem {
    const actions = card.actions ?? [];
    switch (card.type) {
      case "employee_confirm":
        return {
          ...base,
          kind: "employee_confirm",
          proposed: card.proposed,
          source: card.source ?? "manual",
          reason: card.reason,
          candidates: card.candidates ?? [],
          active: false,
        };
      case "form":
        return {
          ...base,
          kind: "form",
          employee: card.employee ?? session.employee ?? "",
          draft: card.draft ?? {},
          attachments: card.attachments ?? [],
          mode: card.submitted ? "submitted" : "superseded",
        };
      case "employee_form":
        return { ...base, kind: "employee_form", card, writable };
      case "pause":
        return { ...base, kind: "pause", pause: card.pause, actions, active: actions.length > 0 };
      case "qc":
        return { ...base, kind: "qc", passed: card.passed ?? false, checks: card.checks ?? [] };
      case "review": {
        if (!card.comment) return { ...base, kind: "review_notice", pause: card.pause, active: actions.length > 0 };
        const giveBack = actions.filter((a) => a.command === "return");
        return { ...base, kind: "return_confirm", actions: giveBack, active: giveBack.length > 0, comment: card.comment, sections: card.sections };
      }
      case "knowledge_filing":
        return filingItem(base, card);
      case "permission":
        return { ...base, kind: "permission", ...derivePermissionCard(card, { writable, role }) };
      case "question":
        return { ...base, kind: "question", card, view: deriveQuestionCard(card, { writable, role }) };
      case "folder_file":
        return {
          ...base,
          kind: "use_file",
          path: card.path ?? "",
          name: card.name ?? card.path?.split("/").at(-1) ?? "",
          size: card.size,
          reason: card.reason,
          confirmed: card.confirmed ?? false,
          actions,
          // 按钮由后端按“文件还在、人还没确认”配好；任务进行中（status 为 task）不能再加材料
          active: writable && session.status !== "task" && !card.confirmed && actions.length > 0,
        };
    }
  }
}
