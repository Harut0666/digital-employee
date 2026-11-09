// 侧栏“工作区 | 记录”两个 tab 的分组与排序（Spec 0006“侧栏界面”）。
//
// 状态点由后端算（session.summary.statusDot），这里只把它换成颜色，不推导：
// 等你处理 → 运行中 → 已完成未读 → 空闲，优先级由后端定，前端按原样显示。
// 分组也只照后端给的 workspaceId / group，前端不自己判断会话归哪个工作区、任务归哪组。

import type { RecordGroup, RecordItem, SessionSummary, WorkspaceInfo } from "../api/types";
import type { WorkspaceList } from "../api/workspaces";

/** 工作区列表 → 全部会话：各工作区自带的加上末尾的未归属。 */
export function flattenSessions(list: WorkspaceList | undefined): SessionSummary[] {
  if (!list) return [];
  const known = new Set(list.workspaces.map((w) => w.id));
  const owned = list.workspaces.flatMap((w) => w.sessions ?? []);
  return [...owned, ...(list.unassigned ?? []).filter((s) => !known.has(s.workspaceId))];
}

export interface DotView {
  dot: "warning" | "processing" | "success" | "default";
  /** 归档的会话灰掉 */
  archived: boolean;
}

const DOT_COLOR: Record<string, DotView["dot"]> = {
  waiting: "warning",
  running: "processing",
  done: "success",
  idle: "default",
};

/** 状态点：后端的值 → 颜色；归档的会话灰掉。 */
export function dotView(item: { statusDot?: string; archived?: boolean }): DotView {
  return { dot: DOT_COLOR[item.statusDot ?? "idle"] ?? "default", archived: item.archived === true };
}

/** 有任务交付或终止之后还没打开（“已完成未读”）；归档的不算。 */
export function unreadOf(item: { unread?: boolean; archived?: boolean; statusDot?: string }): boolean {
  return item.unread === true && item.archived !== true && item.statusDot === "done";
}

export interface WorkspaceGroup {
  /** 工作区 ID；未归属为 "ungrouped" */
  key: string;
  label: string;
  /** 未归属分组没有工作区 */
  workspace?: WorkspaceInfo;
  /** 组里的会话总数（折叠时也照常显示） */
  count: number;
  expanded: boolean;
  /** 当前打开的会话在这个组里（组标题描一下） */
  hasCurrent: boolean;
  /** 折叠时为空 */
  sessions: SessionSummary[];
}

export interface WorkspaceTreeOptions {
  /** 显示已归档的会话 */
  includeArchived: boolean;
  /** 当前打开的会话 */
  currentId?: string;
}

const UNGROUPED = "ungrouped";

const newestFirst = (a: { updatedAt: string }, b: { updatedAt: string }) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt);

/**
 * 工作区 tab：每个工作区一组，组内按更新时间倒序；所属工作区已取消登记的会话放在最后的“未归属”。
 * @param workspaces 后端登记的工作区（顺序即显示顺序）
 * @param sessions 后端给的全部会话摘要
 * @param collapsed 收起的工作区 ID（没收起过的默认展开）
 */
export function groupSessionsByWorkspace(
  workspaces: readonly WorkspaceInfo[],
  sessions: readonly SessionSummary[],
  collapsed: readonly string[],
  options: WorkspaceTreeOptions,
): WorkspaceGroup[] {
  const visible = sessions.filter((s) => options.includeArchived || !s.archived);
  const known = new Set(workspaces.map((w) => w.id));
  const folded = new Set(collapsed);
  const group = (key: string, label: string, workspace: WorkspaceInfo | undefined, members: readonly SessionSummary[]): WorkspaceGroup => {
    const sorted = [...members].sort(newestFirst);
    const hasCurrent = options.currentId !== undefined && sorted.some((s) => s.id === options.currentId);
    const expanded = !folded.has(key);
    return { key, label, workspace, count: sorted.length, expanded, hasCurrent, sessions: expanded ? sorted : [] };
  };
  const groups = workspaces.map((workspace) =>
    group(
      workspace.id,
      workspace.name,
      workspace,
      visible.filter((s) => s.workspaceId === workspace.id),
    ),
  );
  const unassigned = visible.filter((s) => !known.has(s.workspaceId));
  if (unassigned.length > 0 || groups.length === 0) groups.push(group(UNGROUPED, "未归属", undefined, unassigned));
  return groups;
}

export interface RecordGroupView {
  key: RecordGroup;
  label: string;
  items: RecordItem[];
}

/** 记录 tab 的分组顺序与名字（与后端的一致）。 */
export const RECORD_GROUP_ORDER: { key: RecordGroup; label: string }[] = [
  { key: "running", label: "进行中" },
  { key: "supplement", label: "待补充" },
  { key: "review", label: "待审核" },
  { key: "returned", label: "已退回" },
  { key: "delivered", label: "已交付" },
  { key: "terminated", label: "已终止" },
];

/** 记录 tab：按后端给的 group 分组，组的顺序固定（空的组也保留），组内按后端给的顺序（新的在前）。 */
export function groupRecords(records: readonly RecordItem[]): RecordGroupView[] {
  return RECORD_GROUP_ORDER.map(({ key, label }) => ({
    key,
    label,
    items: records.filter((r) => r.group === key),
  }));
}

/** 记录里的“第几次运行”：同一个会话先后运行了多个任务时每行标出；只运行过一次的不标。 */
export function runLabel(item: Pick<RecordItem, "sessionId" | "runNumber">, records: readonly Pick<RecordItem, "sessionId">[]): string {
  if (!item.sessionId || !item.runNumber) return "";
  const sameSession = records.filter((record) => record.sessionId === item.sessionId).length;
  return sameSession > 1 || item.runNumber > 1 ? `第 ${item.runNumber} 次运行` : "";
}

/** 记录里的一条：副标题（员工 · 工作区）。 */
export function recordSubtitle(item: Pick<RecordItem, "employee" | "workspaceName">): string {
  return [item.employee, item.workspaceName].filter((part) => part !== "").join(" · ");
}
