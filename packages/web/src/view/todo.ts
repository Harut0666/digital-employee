// 待办清单（todo_write）的界面派生。纯函数，不依赖 React 与网络。
// 清单整表替换：每次 todo_write 在会话里留下一条 todo_written。对话里每次更新显示一张紧凑的卡，
// 最新的一张展开并实时更新，更早的收起成一行。

import type { SessionMessage, TodoItem, TodoStatus } from "../api/types";

export interface TodoCounts {
  pending: number;
  inProgress: number;
  completed: number;
}

export interface TodoRowView {
  content: string;
  status: TodoStatus;
  /** 图标：勾选 / 进行中 / 待做 */
  icon: "check" | "progress" | "pending";
}

export interface TodoCardView {
  rows: TodoRowView[];
  counts: TodoCounts;
  /** 一行摘要，如“3 项待办，已完成 1 项” */
  summary: string;
  /** 最新的一张：展开并实时更新；更早的收起 */
  latest: boolean;
}

const ICONS: Record<TodoStatus, TodoRowView["icon"]> = { completed: "check", in_progress: "progress", pending: "pending" };

const STATUSES: readonly string[] = ["pending", "in_progress", "completed"];

/** 后端给的清单里不认识的项丢掉，不让界面崩 */
export function cleanTodos(raw: readonly TodoItem[] | undefined): TodoItem[] {
  return (raw ?? []).filter((t) => typeof t?.content === "string" && t.content.trim() !== "" && STATUSES.includes(t.status));
}

export function countTodos(todos: readonly TodoItem[]): TodoCounts {
  const counts: TodoCounts = { pending: 0, inProgress: 0, completed: 0 };
  for (const todo of todos) {
    if (todo.status === "completed") counts.completed++;
    else if (todo.status === "in_progress") counts.inProgress++;
    else counts.pending++;
  }
  return counts;
}

export function todoSummary(todos: readonly TodoItem[]): string {
  if (todos.length === 0) return "待办清单已清空";
  const counts = countTodos(todos);
  const active = todos.find((t) => t.status === "in_progress");
  const head = `${todos.length} 项待办，已完成 ${counts.completed} 项`;
  return active ? `${head}，正在做：${active.content}` : head;
}

/** 一条 todo_written → 待办清单卡；latest 由调用方按“是不是会话里最后一条”给 */
export function deriveTodoCard(message: Pick<SessionMessage, "todos">, latest: boolean): TodoCardView {
  const todos = cleanTodos(message.todos);
  return {
    rows: todos.map((t) => ({ content: t.content, status: t.status, icon: ICONS[t.status] })),
    counts: countTodos(todos),
    summary: todoSummary(todos),
    latest,
  };
}

/** 会话里最后一条 todo_written 的 id；没有时为空 */
export function latestTodoId(messages: readonly SessionMessage[]): string | undefined {
  for (let i = messages.length - 1; i >= 0; i--) {
    if (messages[i]?.kind === "todo_written") return messages[i]!.id;
  }
  return undefined;
}
