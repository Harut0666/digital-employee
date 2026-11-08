// 会话 → 对话流。纯函数，不依赖 React 与网络。
// 消息和卡片都来自后端的会话接口，这里只决定显示成什么、哪张卡片还能操作。
// 任务卡片上的按钮由后端在读取时配上（只配给当前暂停的卡片），前端不推算。

import type { DeliverableRef, QcCheckResult, TaskState } from "@de/core/contract";
import type { AgentToolView, PauseView, SessionAttachment, SessionCard, SessionDetail, SessionEmployee, TaskAction, UiRole } from "../api/types";

interface ItemBase {
  /** React key，取自消息 ID */
  key: string;
  ts: string;
}

export type ChatItem =
  | (ItemBase & { kind: "user"; text: string; role?: UiRole })
  | (ItemBase & { kind: "agent"; text: string })
  | (ItemBase & { kind: "notice"; text: string })
  | (ItemBase & {
      kind: "employee_confirm";
      proposed?: string;
      source: NonNullable<SessionCard["source"]>;
      reason?: string;
      candidates: SessionEmployee[];
      active: boolean;
    })
  | (ItemBase & {
      kind: "form";
      employee: string;
      draft: Record<string, unknown>;
      attachments: SessionAttachment[];
      /** editable：员工已接手、还没提交的最新一张；superseded：后面又发了新表单或已提交；submitted：任务里的表单 */
      mode: "editable" | "superseded" | "submitted";
    })
  | (ItemBase & { kind: "pause"; pause?: PauseView; actions: TaskAction[]; active: boolean })
  | (ItemBase & { kind: "qc"; passed: boolean; checks: QcCheckResult[] })
  /** 送审提示：审核在右栏进行，对话里只说一句 */
  | (ItemBase & { kind: "review_notice"; pause?: PauseView; active: boolean })
  /** Agent 在对话里起草的退回意见，审核人确认后才退回；只带退回这一个操作 */
  | (ItemBase & { kind: "return_confirm"; actions: TaskAction[]; active: boolean; comment: string; sections?: string[] })
  | (ItemBase & { kind: "delivered"; deliverables: DeliverableRef[] })
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

/** 正在说的一轮（来自会话事件流）：说完之后它的内容会变成会话里的消息 */
export interface LiveTurn {
  turn: number;
  text: string;
  tools: AgentToolView[];
  /** 这一轮已经开始 */
  running?: boolean;
}

export function deriveChat(session: SessionDetail, task?: ChatTask, live?: LiveTurn): ChatItem[] {
  const items = deriveMessages(session, task);
  if (!live || (!live.text && live.tools.length === 0)) return items;
  // 还没写成消息的这一轮排在最后一条之后
  const ts = session.updatedAt;
  let key = 0;
  if (live.tools.length > 0) {
    // 每次调用的工具序号一变就换一个 key：折叠状态只对这一批调用有效
    items.push({ key: `tools-${live.turn}-${live.tools.length}`, ts, kind: "tools", turn: live.turn, items: live.tools });
  }
  if (live.text) items.push({ key: `streaming-${live.turn}-${key++}`, ts, kind: "streaming", turn: live.turn, text: live.text });
  return items;
}

function deriveMessages(session: SessionDetail, task?: ChatTask): ChatItem[] {
  const items: ChatItem[] = [];
  const lastIndex = (match: (card: SessionCard) => boolean) => {
    for (let i = session.messages.length - 1; i >= 0; i--) {
      const card = session.messages[i]?.card;
      if (card && match(card)) return i;
    }
    return -1;
  };
  const lastConfirm = lastIndex((c) => c.type === "employee_confirm");
  const lastDraftForm = lastIndex((c) => c.type === "form" && !c.submitted);
  const writable = !session.readOnly;
  // 入库确认卡片排在交付物之后：先能下载，再决定要不要入库
  const filings: ChatItem[] = [];

  session.messages.forEach((m, index) => {
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
        else if (m.card) items.push(cardItem(base, m.card, index));
        break;
      default:
        // 工具留痕和模型失败只进会话日志，不进对话
        break;
    }
  });
  if (task?.state === "delivered" && task.deliverables.length > 0) {
    const ts = session.messages.at(-1)?.ts ?? session.updatedAt;
    items.push({ key: "delivered", ts, kind: "delivered", deliverables: task.deliverables });
  }
  items.push(...filings);
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

  function cardItem(base: ItemBase, card: SessionCard, index: number): ChatItem {
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
          active: writable && session.status === "reception" && index === lastConfirm,
        };
      case "form": {
        const editable = !card.submitted && writable && session.status === "drafting" && index === lastDraftForm;
        return {
          ...base,
          kind: "form",
          employee: card.employee ?? session.employee ?? "",
          // 可编辑的卡片显示会话里最新的草稿和材料：卡片发出后人可能又上传了文件
          draft: (editable ? session.draft : card.draft) ?? {},
          attachments: (editable ? session.attachments : card.attachments) ?? [],
          mode: card.submitted ? "submitted" : editable ? "editable" : "superseded",
        };
      }
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
    }
  }
}
