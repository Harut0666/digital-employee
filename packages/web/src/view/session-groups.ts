// 左栏会话分组：需要我处理 / 运行中 / 已结束，另有按角色列出的全部待办。
// “等谁处理”完全读摘要的 waitingFor，前端不维护状态和角色的映射。

import { TERMINAL_STATES, type TaskState } from "@de/core/contract";
import type { TaskSummary, UiRole } from "../api/types";
import { ROLE_LABELS } from "./permissions";

export type SessionDot = "warning" | "processing" | "success" | "default" | "error";

export interface SessionItem extends TaskSummary {
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

function dotOf(state: TaskState, mine: boolean): SessionDot {
  if (state === "delivered") return "success";
  if (state === "terminated") return "default";
  return mine ? "warning" : "processing";
}

const newestFirst = (a: TaskSummary, b: TaskSummary) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt);

export function groupSessions(tasks: readonly TaskSummary[], role: UiRole): SessionGroup[] {
  const groups: SessionGroup[] = [
    { key: "mine", label: "需要我处理", dot: "warning", items: [] },
    { key: "active", label: "运行中", dot: "processing", items: [] },
    { key: "done", label: "已结束", dot: "success", items: [] },
  ];
  for (const t of tasks) {
    const isMine = t.waitingFor.includes(role);
    const target = TERMINAL_STATES.includes(t.state) ? groups[2]! : isMine ? groups[0]! : groups[1]!;
    const waiting = !isMine && t.waitingFor.length > 0 ? ` · 等${t.waitingFor.map((r) => ROLE_LABELS[r]).join("、")}` : "";
    target.items.push({ ...t, dot: dotOf(t.state, isMine), sub: `${t.statusText}${waiting}` });
  }
  for (const g of groups) g.items.sort(newestFirst);
  return groups;
}

/** 全部待办：每个角色一组。一个任务等多个角色时，在每个角色下都出现。 */
export function groupTodos(tasks: readonly TaskSummary[]): SessionGroup[] {
  return (Object.keys(ROLE_LABELS) as UiRole[]).map((role) => ({
    key: role,
    label: ROLE_LABELS[role],
    dot: "warning",
    items: tasks
      .filter((t) => t.waitingFor.includes(role))
      .map((t) => ({ ...t, dot: "warning" as const, sub: t.statusText }))
      .sort(newestFirst),
  }));
}
