// 侧栏“工作区”栏的视图选项与搜索。纯函数，不依赖 React 与网络。
//
// 视图选项（分组方式、排序、筛选会话）存在 localStorage，键 de.workspace.view.v1；
// 读写都包 try/catch，读不到或读出来不合法时用默认值。搜索关键字不持久化。
// 搜索只做本地过滤（会话标题或所属工作区名称，不区分大小写的子串），没有服务端内容搜索接口。

import type { SessionSummary, WorkspaceInfo } from "../api/types";

export type SessionGrouping = "workspace" | "flat";
export type SessionSort = "recent" | "name";
/** 隐藏已归档（默认）| 全部 | 仅已归档 */
export type SessionFilter = "active" | "all" | "archived";

export interface WorkspaceViewOptions {
  group: SessionGrouping;
  sort: SessionSort;
  filter: SessionFilter;
}

export const VIEW_STORAGE_KEY = "de.workspace.view.v1";

export const DEFAULT_VIEW_OPTIONS: WorkspaceViewOptions = { group: "workspace", sort: "recent", filter: "active" };

/** 视图选项菜单里每一组的选项和说法（菜单按它画，不在组件里写死） */
export const VIEW_OPTION_GROUPS: {
  key: keyof WorkspaceViewOptions;
  label: string;
  options: { value: string; label: string }[];
}[] = [
  {
    key: "group",
    label: "分组方式",
    options: [
      { value: "workspace", label: "按工作区" },
      { value: "flat", label: "单列表" },
    ],
  },
  {
    key: "sort",
    label: "排序",
    options: [
      { value: "recent", label: "最近更新" },
      { value: "name", label: "名称" },
    ],
  },
  {
    key: "filter",
    label: "筛选会话",
    options: [
      { value: "active", label: "隐藏已归档" },
      { value: "all", label: "全部" },
      { value: "archived", label: "仅已归档" },
    ],
  },
];

const pick = <T extends string>(value: unknown, allowed: readonly T[], fallback: T): T => (allowed.includes(value as T) ? (value as T) : fallback);

/** 读出来的文本 → 视图选项；缺的、不合法的字段各自回到默认值。 */
export function parseViewOptions(raw: string | null | undefined): WorkspaceViewOptions {
  if (!raw) return { ...DEFAULT_VIEW_OPTIONS };
  let data: unknown;
  try {
    data = JSON.parse(raw);
  } catch {
    return { ...DEFAULT_VIEW_OPTIONS };
  }
  const record = data && typeof data === "object" ? (data as Record<string, unknown>) : {};
  return {
    group: pick(record.group, ["workspace", "flat"], DEFAULT_VIEW_OPTIONS.group),
    sort: pick(record.sort, ["recent", "name"], DEFAULT_VIEW_OPTIONS.sort),
    filter: pick(record.filter, ["active", "all", "archived"], DEFAULT_VIEW_OPTIONS.filter),
  };
}

export function serializeViewOptions(options: WorkspaceViewOptions): string {
  return JSON.stringify({ group: options.group, sort: options.sort, filter: options.filter });
}

type StorageLike = Pick<Storage, "getItem" | "setItem">;

function defaultStorage(): StorageLike | undefined {
  try {
    return globalThis.localStorage;
  } catch {
    return undefined;
  }
}

/** 从 localStorage 读；读不到（隐私窗口、被禁用）就用默认值。 */
export function loadViewOptions(storage: StorageLike | undefined = defaultStorage()): WorkspaceViewOptions {
  try {
    return parseViewOptions(storage?.getItem(VIEW_STORAGE_KEY));
  } catch {
    return { ...DEFAULT_VIEW_OPTIONS };
  }
}

/** 写进 localStorage；写不进去时静默（只是下次不记得）。 */
export function saveViewOptions(options: WorkspaceViewOptions, storage: StorageLike | undefined = defaultStorage()): void {
  try {
    storage?.setItem(VIEW_STORAGE_KEY, serializeViewOptions(options));
  } catch {
    // 存不了就算了
  }
}

/** 会话所属工作区的名字：已登记的取登记名，否则取会话自带的名字，都没有写“未归属”。 */
export function workspaceNameOf(session: Pick<SessionSummary, "workspaceId" | "workspaceName">, workspaces: readonly Pick<WorkspaceInfo, "id" | "name">[]): string {
  return workspaces.find((w) => w.id === session.workspaceId)?.name || session.workspaceName || "未归属";
}

export interface SearchHit {
  session: SessionSummary;
  workspaceName: string;
}

const newestFirst = (a: SessionSummary, b: SessionSummary) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt);

/** 搜索：会话标题或所属工作区名称含关键字（不区分大小写），最近更新的在前；关键字为空不搜。 */
export function searchSessions(sessions: readonly SessionSummary[], workspaces: readonly Pick<WorkspaceInfo, "id" | "name">[], query: string): SearchHit[] {
  const needle = query.trim().toLowerCase();
  if (!needle) return [];
  return [...sessions]
    .sort(newestFirst)
    .map((session) => ({ session, workspaceName: workspaceNameOf(session, workspaces) }))
    .filter((hit) => hit.session.title.toLowerCase().includes(needle) || hit.workspaceName.toLowerCase().includes(needle));
}
