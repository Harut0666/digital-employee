// 交付区（右栏“交付物”下面）要显示的东西：入库确认、早先任务的交付物。纯函数，不依赖 React 与网络。
// 这些原来是对话里的卡片；现在对话里只留员工的工具卡片，交付之后的事都放在右栏。

import type { DeliverableRef } from "@de/core/contract";
import type { SessionDetail } from "../api/types";
import type { PastRun } from "./chat-view";

/** 入库确认：交付后由程序发出，模型起草描述，人确认后才放进知识库 */
export interface FilingView {
  taskId: string;
  title: string;
  description: string;
  draftFailed: boolean;
  /** 已入库的知识库条目 ID */
  filed?: string;
  /** 还没入库：可以改描述并确认 */
  active: boolean;
  /** 能请模型起草描述（只读会话不能写会话日志） */
  canDraft: boolean;
}

/** 某个任务最近一张入库确认卡片 */
export function filingOf(session: SessionDetail, taskId: string | undefined): FilingView | undefined {
  if (!taskId) return undefined;
  const cards = session.messages.filter((m) => m.kind === "card" && m.card?.type === "knowledge_filing" && (m.card.taskId ?? session.taskId) === taskId);
  const card = cards.at(-1)?.card;
  if (!card) return undefined;
  return {
    taskId,
    title: card.title ?? "",
    description: card.description ?? "",
    draftFailed: card.draftFailed ?? false,
    filed: card.filed,
    active: !card.filed,
    canDraft: !session.readOnly,
  };
}

/** 会话里早先交付的任务：交付物和它的入库确认（当前任务的交付物在右栏主体里） */
export interface EarlierDelivery {
  taskId: string;
  run: number;
  deliverables: DeliverableRef[];
  filing?: FilingView;
}

export function earlierDeliveries(session: SessionDetail, past: readonly PastRun[]): EarlierDelivery[] {
  return past
    .filter((run) => run.state === "delivered" && run.deliverables.length > 0)
    .map((run) => ({ taskId: run.taskId, run: run.run, deliverables: run.deliverables, filing: filingOf(session, run.taskId) }));
}
