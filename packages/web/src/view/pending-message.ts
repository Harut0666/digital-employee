// 乐观的“待确认用户消息”。纯函数，不依赖 React 与网络。
//
// 发一句话时，服务端先把它写进会话日志再返回受理；但界面读会话的时机不一定在写入之后
// （新对话刚建好就切过去读一次、一轮结束才重读），所以这句话要在界面上先显示出来：
// 发送时登记一条待确认的话，正式消息进了日志就让位，发送失败就移除。
// 服务端受理结果（TurnAccepted）只带轮次、不带消息 id，所以按“文字 + 角色 + 条数”去重：
// 登记时记下日志里已有几条同文字同角色的人的话（基线），日志里比基线多出一条才算确认，
// 早先说过同样的话、连发两句同样的话都不会误判；不依赖浏览器与服务端的时钟。

import type { SessionDetail, SessionSummary, UiRole } from "../api/types";

/** 一条还没在会话日志里看到的人的话 */
export interface PendingMessage {
  /** 本地编号（不是服务端的消息 id） */
  id: string;
  sessionId: string;
  /** 已去掉首尾空白，和服务端落盘的一致 */
  text: string;
  role: UiRole;
  /** 发送时间（ISO），对话里显示的时间 */
  ts: string;
  sentAtMs: number;
  /** 登记时日志里已有的同文字同角色的人的话，加上更早还没确认的同样的待确认条 */
  baseline: number;
}

/** 等不到正式消息的待确认条最多挂这么久，之后丢弃（避免界面上一直多一条） */
export const PENDING_TTL_MS = 2 * 60 * 1000;

/** 日志里同文字同角色的人的话有几条 */
function loggedCount(detail: SessionDetail | undefined, text: string, role: UiRole): number {
  return detail?.messages.filter((m) => m.kind === "user" && (m.role ?? "initiator") === role && (m.text ?? "").trim() === text).length ?? 0;
}

export function createPending(input: {
  id: string;
  sessionId: string;
  text: string;
  role: UiRole;
  nowMs: number;
  /** 界面上当前已读到的这个会话；新对话刚建好、还没读到时不给 */
  detail?: SessionDetail;
  /** 这个会话里已有的待确认条（连发时算进基线） */
  existing?: readonly PendingMessage[];
}): PendingMessage {
  const text = input.text.trim();
  const logged = loggedCount(input.detail?.id === input.sessionId ? input.detail : undefined, text, input.role);
  const waiting = (input.existing ?? []).filter((p) => p.sessionId === input.sessionId && p.role === input.role && p.text === text).length;
  return { id: input.id, sessionId: input.sessionId, text, role: input.role, ts: new Date(input.nowMs).toISOString(), sentAtMs: input.nowMs, baseline: logged + waiting };
}

/** 读到一次会话之后：正式消息已经进了日志的待确认条让位，过期的丢弃，其余保留。只处理这个会话的。 */
export function settlePending(pending: readonly PendingMessage[], detail: SessionDetail, nowMs: number): PendingMessage[] {
  return pending.filter((p) => {
    if (p.sessionId !== detail.id) return true;
    if (nowMs - p.sentAtMs > PENDING_TTL_MS) return false;
    return loggedCount(detail, p.text, p.role) <= p.baseline;
  });
}

export function addPending(pending: readonly PendingMessage[], item: PendingMessage): PendingMessage[] {
  return [...pending, item];
}

export function removePending(pending: readonly PendingMessage[], id: string): PendingMessage[] {
  return pending.filter((p) => p.id !== id);
}

/** 某个会话的待确认条（登记时是追加的，所以就是发送先后） */
export function pendingOf(pending: readonly PendingMessage[], sessionId: string | undefined): PendingMessage[] {
  return pending.filter((p) => p.sessionId === sessionId);
}

/** 刚建好的会话摘要补上空消息：第一次读取回来之前就能先把对话页画出来 */
export function seedDetail(created: SessionSummary): SessionDetail {
  return { ...created, messages: [] };
}
