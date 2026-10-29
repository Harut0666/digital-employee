// 左栏会话分组：待我处理 / 进行中 / 已交付，带状态色点。

import type { Role, TaskState, TaskSummary } from "@de/core/contract";

export type SessionDot = "warning" | "processing" | "success" | "default" | "error";

export interface SessionItem extends TaskSummary {
  dot: SessionDot;
}
export interface SessionGroup {
  key: "mine" | "active" | "done";
  label: string;
  dot: SessionDot;
  items: SessionItem[];
}

/** 每个角色“待我处理”对应的暂停状态。 */
const MINE: Record<string, TaskState[]> = {
  initiator: ["draft", "awaiting_initiator"],
  reviewer: ["awaiting_review"],
  operator: ["awaiting_operator"],
};

const DONE: TaskState[] = ["delivered", "terminated"];

function dotOf(state: TaskState, mine: boolean): SessionDot {
  if (state === "delivered") return "success";
  if (state === "terminated") return "default";
  return mine ? "warning" : "processing";
}

export function groupSessions(tasks: readonly TaskSummary[], role: Role): SessionGroup[] {
  const groups: SessionGroup[] = [
    { key: "mine", label: "待我处理", dot: "warning", items: [] },
    { key: "active", label: "进行中", dot: "processing", items: [] },
    { key: "done", label: "已交付", dot: "success", items: [] },
  ];
  for (const t of tasks) {
    const isMine = (MINE[role] ?? []).includes(t.state);
    const target = DONE.includes(t.state) ? groups[2]! : isMine ? groups[0]! : groups[1]!;
    target.items.push({ ...t, dot: dotOf(t.state, isMine) });
  }
  for (const g of groups) g.items.sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt));
  return groups;
}
