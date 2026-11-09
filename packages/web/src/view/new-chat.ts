// 侧栏顶部“+ 新对话”的走向（全局入口，永远可见）。纯函数。
//
// - 只有一个可用的工作区：直接在它下面开；
// - 有多个：让人选在哪个工作区，默认当前选中会话所在的工作区；
// - 一个工作区都没有（只剩“未归属”）或目录都缺失：先引导添加工作区。
// “可用”指目录没缺失；缺失的工作区登记还在、会话仍可查看，但不能在它下面开新对话。

import type { SessionSummary } from "../api/types";
import type { WorkspaceItem, WorkspaceList } from "../api/workspaces";

export interface NewChatOption {
  id: string;
  name: string;
  path: string;
  /** 目录缺失：置灰，不能选 */
  disabled: boolean;
}

export type NewChatEntry =
  | {
      /** 直接开；workspaceId 为空表示还没读到列表，交给服务端取唯一的工作区 */
      kind: "direct";
      workspaceId: string | undefined;
    }
  | { kind: "choose"; options: NewChatOption[]; defaultId: string }
  | {
      kind: "add-workspace";
      /** 没有任何登记的工作区 | 登记的工作区目录都缺失了 */
      reason: "empty" | "missing";
      /** 单目录模式（环境变量指定了目录）不能添加 */
      canAdd: boolean;
      hint?: string;
    };

type ListLike = Pick<WorkspaceList, "readOnly" | "workspaces"> & { reason?: string };

const usable = (w: WorkspaceItem) => w.missing !== true;

/** 点“+ 新对话”后怎么走。currentWorkspaceId 是当前选中会话所在的工作区（用作默认）。 */
export function newChatEntry(list: ListLike | undefined, currentWorkspaceId?: string): NewChatEntry {
  if (!list) return { kind: "direct", workspaceId: undefined };
  const eligible = list.workspaces.filter(usable);
  if (eligible.length === 0) {
    const entry: NewChatEntry = { kind: "add-workspace", reason: list.workspaces.length === 0 ? "empty" : "missing", canAdd: !list.readOnly };
    return list.readOnly && list.reason ? { ...entry, hint: list.reason } : entry;
  }
  const only = eligible.length === 1 ? eligible[0] : undefined;
  if (only) return { kind: "direct", workspaceId: only.id };
  const preferred = eligible.find((w) => w.id === currentWorkspaceId) ?? eligible[0]!;
  return {
    kind: "choose",
    defaultId: preferred.id,
    options: list.workspaces.map((w) => ({ id: w.id, name: w.name, path: w.path, disabled: !usable(w) })),
  };
}

/** 新对话开在哪个工作区：指定的可用就用它；否则按入口的默认；没有可用的交给服务端。 */
export function startWorkspaceId(list: ListLike | undefined, preferredId?: string): string | undefined {
  if (list && preferredId && list.workspaces.some((w) => w.id === preferredId && usable(w))) return preferredId;
  const entry = newChatEntry(list, preferredId);
  if (entry.kind === "direct") return entry.workspaceId;
  if (entry.kind === "choose") return entry.defaultId;
  return undefined;
}

/** 新对话页上显示的目标工作区：开在哪、能换成哪些，或者被拦住（先添加工作区）。 */
export interface NewChatTarget {
  blocked?: Extract<NewChatEntry, { kind: "add-workspace" }>;
  current?: { id: string; name: string };
  /** 多个工作区时可更换的选项（含置灰的）；只有一个时为空 */
  choices: NewChatOption[];
}

export function newChatTarget(list: ListLike | undefined, preferredId?: string): NewChatTarget {
  const entry = newChatEntry(list, preferredId);
  if (entry.kind === "add-workspace") return { blocked: entry, choices: [] };
  const id = startWorkspaceId(list, preferredId);
  const workspace = id ? list?.workspaces.find((w) => w.id === id) : undefined;
  return {
    current: workspace ? { id: workspace.id, name: workspace.name } : undefined,
    choices: entry.kind === "choose" ? entry.options : [],
  };
}

/** 当前选中的会话（或新对话页带着的工作区）属于哪个工作区。 */
export function currentWorkspaceOf(
  list: WorkspaceList | undefined,
  selection: { kind: "new"; workspaceId?: string } | { kind: "session"; id: string } | { kind: "page"; page: string },
): string | undefined {
  if (selection.kind === "new") return selection.workspaceId;
  if (selection.kind !== "session" || !list) return undefined;
  const sessions: SessionSummary[] = [...list.workspaces.flatMap((w) => w.sessions ?? []), ...(list.unassigned ?? [])];
  return sessions.find((s) => s.id === selection.id)?.workspaceId;
}
