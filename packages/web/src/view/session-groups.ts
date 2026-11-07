// 左栏会话分组：需要我处理 / 运行中 / 已结束，另有按角色列出的全部待办。
// 有任务的会话，“等谁处理”完全读摘要的 waitingFor，前端不维护状态和角色的映射。
// 还没提交的会话只有发起人能推进（确认员工、提交都只接受发起人），所以算在等发起人。

import type { SessionSummary, UiRole } from "../api/types";
import { ROLE_LABELS } from "./permissions";

export type SessionDot = "warning" | "processing" | "success" | "default" | "error";

export interface SessionItem extends SessionSummary {
  dot: SessionDot;
  /** 标题下的一行说明 */
  sub: string;
}
export interface SessionGroup {
  key: "mine" | "active" | "done" | UiRole;
  label: string;
  dot: SessionDot;
  items: SessionItem[];
}

const DRAFT_TEXT: Partial<Record<SessionSummary["status"], string>> = {
  reception: "待确认员工",
  drafting: "待提交",
};

/** 会话在等哪些角色 */
export function waitingOf(s: SessionSummary): UiRole[] {
  return DRAFT_TEXT[s.status] && !s.readOnly ? ["initiator"] : s.waitingFor;
}

const statusOf = (s: SessionSummary) => DRAFT_TEXT[s.status] ?? s.statusText ?? "";

function dotOf(s: SessionSummary, mine: boolean): SessionDot {
  if (s.status === "closed") return s.taskState === "terminated" ? "default" : "success";
  return mine ? "warning" : "processing";
}

const newestFirst = (a: SessionSummary, b: SessionSummary) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt);

export function groupSessions(sessions: readonly SessionSummary[], role: UiRole): SessionGroup[] {
  const groups: SessionGroup[] = [
    { key: "mine", label: "需要我处理", dot: "warning", items: [] },
    { key: "active", label: "运行中", dot: "processing", items: [] },
    { key: "done", label: "已结束", dot: "success", items: [] },
  ];
  for (const s of sessions) {
    const waiting = waitingOf(s);
    const isMine = waiting.includes(role);
    const target = s.status === "closed" ? groups[2]! : isMine ? groups[0]! : groups[1]!;
    const others = !isMine && waiting.length > 0 ? ` · 等${waiting.map((r) => ROLE_LABELS[r]).join("、")}` : "";
    target.items.push({ ...s, dot: dotOf(s, isMine), sub: `${statusOf(s)}${others}` });
  }
  for (const g of groups) g.items.sort(newestFirst);
  return groups;
}

/** 全部待办：每个角色一组。一个会话等多个角色时，在每个角色下都出现。 */
export function groupTodos(sessions: readonly SessionSummary[]): SessionGroup[] {
  return (Object.keys(ROLE_LABELS) as UiRole[]).map((role) => ({
    key: role,
    label: ROLE_LABELS[role],
    dot: "warning",
    items: sessions
      .filter((s) => waitingOf(s).includes(role))
      .map((s) => ({ ...s, dot: "warning" as const, sub: statusOf(s) }))
      .sort(newestFirst),
  }));
}
