// 左上角的工作区选择：从工作区列表推出显示名称、菜单项和能不能切换、新建。

import type { SwitchWorkspaceResult, WorkspaceItem, WorkspaceList } from "../api/workspaces";

export interface WorkspacePickerView {
  title: string;
  tooltip: string;
  items: (WorkspaceItem & { current: boolean })[];
  canSwitch: boolean;
  canCreate: boolean;
  /** 只读时的原因 */
  hint?: string;
}

/** 工作区名：取路径最后一段 */
const baseName = (dir: string) => dir.split(/[\\/]/).filter(Boolean).at(-1) ?? dir;

/** fallbackDir：列表还没读到时显示的工作区目录 */
export function workspacePicker(list: WorkspaceList | undefined, fallbackDir?: string): WorkspacePickerView {
  const current = list?.workspaces.find((w) => w.id === list.current);
  const path = current?.path ?? fallbackDir;
  const editable = list !== undefined && !list.readOnly;
  return {
    title: current?.name ?? (fallbackDir ? baseName(fallbackDir) : "工作区"),
    tooltip: path ? `当前工作区：${path}` : "当前工作区",
    items: (list?.workspaces ?? []).map((w) => ({ ...w, current: w.id === list?.current })),
    canSwitch: editable,
    canCreate: editable,
    hint: list?.readOnly ? list.reason : undefined,
  };
}

export type SwitchOutcome = { kind: "reload" } | { kind: "none" } | { kind: "error"; message: string };

/** 切换生效后整页重新加载：设置、员工、任务、会话都换成新工作区的。 */
export function switchOutcome(result: SwitchWorkspaceResult): SwitchOutcome {
  if (!result.ok) return { kind: "error", message: result.message };
  return result.reload ? { kind: "reload" } : { kind: "none" };
}
