// 会话 → 对话流。纯函数，不依赖 React 与网络。
// 消息和卡片都来自后端的会话接口，这里只决定显示成什么、哪张卡片还能操作。
// 任务卡片上的按钮由后端在读取时配上（只配给当前暂停的卡片），前端不推算。

import type { DeliverableRef, QcCheckResult, TaskEvent, TaskState } from "@de/core/contract";
import { deriveStepRows, type StepRow, type StepRowDef } from "./step-rows";
import type { AgentToolView, PauseView, SessionAttachment, SessionCard, SessionDetail, SessionEmployee, TaskAction, UiRole } from "../api/types";
import { derivePermissionCard, fileChangeOf, traceOf, type FileChangeView, type PermissionCardView, type PermissionTrace } from "./permission-mode";
import { latestTaskId } from "./task-runs";
import { deriveTodoCard, latestTodoId, type TodoCardView } from "./todo";
import { deriveQuestionCard, type QuestionCardView } from "./question";
import type { PendingMessage } from "./pending-message";

interface ItemBase {
  /** React key，取自消息 ID */
  key: string;
  ts: string;
}

export type ChatItem =
  | (ItemBase & { kind: "user"; text: string; role?: UiRole; pending?: boolean })
  | (ItemBase & { kind: "agent"; text: string; turn?: number })
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
  /** 员工表单（T49）：run_employee 弹出的结构化表单，人核对、补充、确认开工或“取消” */
  | (ItemBase & { kind: "employee_form"; card: SessionCard; writable: boolean })
  | (ItemBase & { kind: "pause"; pause?: PauseView; actions: TaskAction[]; active: boolean })
  /** 使用目录文件：前台 Agent 提议把人工作区目录里的文件当材料，人确认后才复制进会话 */
  | (ItemBase & { kind: "use_file"; path: string; name: string; size?: number; reason?: string; confirmed: boolean; actions: TaskAction[]; active: boolean })
  /** Agent 在对话里起草的退回意见，审核人确认后才退回；只带退回这一个操作 */
  | (ItemBase & { kind: "return_confirm"; actions: TaskAction[]; active: boolean; comment: string; sections?: string[] })
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
  | (ItemBase & { kind: "tools"; turn: number; items: AgentToolView[]; running: boolean; startedAt?: number; endedAt?: number })
  /** 正在说的一轮里已经说出的部分（还没写成会话里的消息） */
  | (ItemBase & { kind: "streaming"; turn: number; text: string; running: boolean });

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
  /** 这一轮开始、结束的时间（毫秒时间戳，前端收到事件的时刻），用来显示“用时” */
  startedAt?: number;
  endedAt?: number;
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

/** 后端在交付时写进会话的说明（internal/session/cards.go） */
const DELIVERED_NOTICE = "任务已交付。";

/** 写文件的工具：会话里已经有对应的“改动”行，不再在工具行里重复 */
const FILE_CHANGE_TOOLS = new Set(["write_file", "edit_file"]);

/**
 * 流式内容和正式消息是同一个节点：这一轮的正式回复一进会话（按轮次认；旧消息没有轮次时按文字对得上认），
 * 流式的临时文字就让位，对话里只留正式消息；说话中、或出错没有正式回复时才显示流式文字。
 */
function liveSettled(session: SessionDetail, live: LiveTurn): boolean {
  const finals = session.messages.filter((m) => m.kind === "assistant");
  if (finals.some((m) => m.turn === live.turn)) return true;
  if (live.running) return false;
  const spoken = live.text.trim();
  const last = finals.at(-1)?.text?.trim() ?? "";
  // 没有轮次的旧消息：说完之后最后一条回复与流式文字互相包含，就是同一条
  return spoken !== "" && last !== "" && !finals.some((m) => m.turn !== undefined) && (last.includes(spoken) || spoken.includes(last));
}

export function deriveChat(
  session: SessionDetail,
  task?: ChatTask,
  events: readonly TaskEvent[] = [],
  live?: LiveTurn,
  past: readonly PastRun[] = [],
  role: UiRole = "initiator",
  /** 刚发出、还没在会话日志里看到的人的话（乐观显示）：排在已有消息之后、这一轮的过程行和流式文字之前 */
  pending: readonly PendingMessage[] = [],
): ChatItem[] {
  // 这一轮有事件流里的工具调用时，日志里同一轮的调用记录让位给它（它更新鲜，还带用时）
  const liveTurn = live && live.tools.length > 0 ? live.turn : undefined;
  const firstOfTurn = new Map<number, ChatItem>();
  const items = deriveMessages(session, task, events, past, role, liveTurn, firstOfTurn);
  for (const p of pending) items.push({ key: `pending-${p.id}`, ts: p.ts, kind: "user", text: p.text, role: p.role, pending: true });
  // 同一轮的正式回复和流式内容共用一个 key（同一个节点），文字增长不换 key
  const turnKey = (turn: number) => `turn-${turn}`;
  const claimed = new Set<number>();
  for (const item of items) {
    if (item.kind !== "agent" || item.turn === undefined || claimed.has(item.turn)) continue;
    claimed.add(item.turn);
    item.key = turnKey(item.turn);
  }
  if (!live || (!live.text && live.tools.length === 0)) return items;
  const settled = liveSettled(session, live);
  // 写文件已经有“改动”行了：工具行里不重复
  const written = new Set(session.messages.filter((m) => m.kind === "tool_called" && m.turn === live.turn && FILE_CHANGE_TOOLS.has(m.tool ?? "")).map((m) => m.tool));
  const tools = live.tools.filter((t) => !(t.ended && written.has(t.tool)));
  const ts = session.updatedAt;
  const running = live.running ?? false;
  if (tools.length > 0) {
    // 每次调用的工具序号一变就换一个 key：折叠状态只对这一批调用有效
    const row: ChatItem = { key: `tools-${live.turn}`, ts, kind: "tools", turn: live.turn, items: tools, running, startedAt: live.startedAt, endedAt: live.endedAt };
    // 工具调用先于它引出的一切：这一轮在会话日志里已经写下的第一项（回复、员工表单等卡片、改动行……）之前；
    // 日志里还没有这一轮的内容（调用还在进行）才排到最后
    const first = firstOfTurn.get(live.turn);
    const at = first ? items.indexOf(first) : -1;
    if (at >= 0) items.splice(at, 0, row);
    else items.push(row);
  }
  if (live.text && !settled) items.push({ key: turnKey(live.turn), ts, kind: "streaming", turn: live.turn, text: live.text, running });
  return items;
}
function deriveMessages(
  session: SessionDetail,
  task: ChatTask | undefined,
  events: readonly TaskEvent[],
  past: readonly PastRun[],
  role: UiRole,
  /** 这一轮的工具调用由事件流里那份显示，日志里的记录不再重复生成 */
  liveTurn: number | undefined,
  /** 输出：每一轮在对话里产生的第一项（事件流里的工具行排在它之前） */
  firstOfTurn: Map<number, ChatItem>,
): ChatItem[] {
  const items: ChatItem[] = [];
  const writable = !session.readOnly;

  session.messages.forEach((m) => {
    const base = { key: m.id, ts: m.ts };
    const before = items.length;
    switch (m.kind) {
      case "user":
        items.push({ ...base, kind: "user", text: m.text ?? "", role: m.role });
        break;
      case "assistant":
        items.push({ ...base, kind: "agent", text: m.text ?? "", turn: m.turn });
        break;
      case "notice":
        // “任务已交付。”不进对话：交付状态在标题栏的状态标签上，交付物和入库在右栏
        if (m.text === DELIVERED_NOTICE) break;
        items.push({ ...base, kind: "notice", text: m.text ?? "" });
        break;
      case "card": {
        // 对话里只留员工的工具卡片（员工表单、使用目录文件等）和需要人答复的确认；质检、送审提示、交付物、入库确认都在右栏
        const item = m.card ? cardItem(base, m.card) : undefined;
        if (item) items.push(item);
        break;
      }
      case "todo_written":
        items.push({ ...base, kind: "todo", ...deriveTodoCard(m, m.id === latestTodoId(session.messages)) });
        break;
      // 权限模式切换：输入框左下角的下拉已显示当前模式，轨迹里留痕，对话里不再插说明
      case "tool_called": {
        // 写文件的工具行：点开看改了什么
        const change = fileChangeOf(m);
        if (change) {
          items.push({ ...base, kind: "file_change", ...change });
          break;
        }
        // 别的工具调用：刷新、切走再切回来后没有事件流，按日志在原位置补一行（连着的合成一行）。
        // 没有轮次的是旧格式留痕，仍只留在日志里；这一轮事件流里有的话由那份显示
        if (m.turn === undefined || m.turn === liveTurn) break;
        const call: AgentToolView = { id: m.id, tool: m.tool ?? "", args: m.args, ended: true, result: m.result, reason: m.reason };
        const last = items.at(-1);
        if (last?.kind === "tools" && last.key.startsWith("tools-log-") && last.turn === (m.turn ?? 0)) last.items.push(call);
        else items.push({ ...base, key: `tools-log-${m.id}`, kind: "tools", turn: m.turn ?? 0, items: [call], running: false });
        break;
      }
      case "permission_allowed":
      case "tool_denied": {
        const trace = traceOf(m);
        if (trace) items.push({ ...base, kind: "trace", ...trace });
        break;
      }
      default:
        // 模型失败只进会话日志，不进对话
        break;
    }
    // 这一轮在对话里产生的第一项，记下来给事件流里的工具行定位
    const added = items[before];
    if (m.turn !== undefined && added && !firstOfTurn.has(m.turn)) firstOfTurn.set(m.turn, added);
  });
  // 早先的任务：步骤行紧跟在它开工的位置（它们的交付物在右栏“早先的交付”里）
  for (const run of past) {
    const rows = deriveStepRows(run.events, run.steps, { fallbackModel: undefined });
    const first = run.events[0];
    if (first && rows.some((row) => row.status !== "pending")) {
      insertByTime(items, { key: `steps-${run.taskId}`, ts: first.ts, kind: "past_steps", taskId: run.taskId, run: run.run, rows });
    }
  }

  return items;

  function cardItem(base: ItemBase, card: SessionCard): ChatItem | undefined {
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
      // 质检结果在右栏的检验记录里；送审只是一句提示，审核本身在右栏；入库确认在右栏交付物下面
      case "qc":
      case "knowledge_filing":
        return undefined;
      case "review": {
        if (!card.comment) return undefined;
        const giveBack = actions.filter((a) => a.command === "return");
        return { ...base, kind: "return_confirm", actions: giveBack, active: giveBack.length > 0, comment: card.comment, sections: card.sections };
      }
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
