// 侧栏“工作区”tab 的纯函数视图（Spec 0006）：分组标题、可折叠、目录缺失的提示。
// 工作区只是目录，所有工作区同时可用；这里只负责把列表整理成界面要的形状，不涉及切换。

import type { WorkspaceItem, WorkspaceList } from "../api/workspaces";

export interface WorkspaceGroupView {
  id: string;
  name: string;
  path: string;
  /** 目录缺失：登记保留，会话仍可查看 */
  missing: boolean;
  hint: string;
}

export interface WorkspacesView {
  groups: WorkspaceGroupView[];
  /** 单目录模式：不能新建、重命名或取消登记 */
  canManage: boolean;
  hint?: string;
  empty: boolean;
}

/** 目录名：取路径最后一段 */
export const baseName = (dir: string) => dir.split(/[\\/]/).filter(Boolean).at(-1) ?? dir;

export function workspaceGroups(list: WorkspaceList | undefined): WorkspacesView {
  const items: WorkspaceItem[] = list?.workspaces ?? [];
  return {
    groups: items.map((w) => ({
      id: w.id,
      name: w.name,
      path: w.path,
      missing: w.missing === true,
      hint: w.missing === true ? w.missingText ?? "目录缺失" : w.path,
    })),
    canManage: list !== undefined && !list.readOnly,
    hint: list?.readOnly ? list.reason : undefined,
    empty: list !== undefined && items.length === 0,
  };
}
