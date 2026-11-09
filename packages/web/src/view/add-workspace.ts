// “添加工作区”弹窗：目录输入框旁的“浏览…”和提交。纯函数。

import type { PickFolderResult } from "../api/workspaces";
import { baseName } from "./workspace-picker";

export interface WorkspaceForm {
  name: string;
  path: string;
}

/** 选中目录后：路径填进输入框；名称为空时用目录名补全（已写的名称不覆盖）。 */
export function applyPickedFolder(form: WorkspaceForm, picked: string): WorkspaceForm {
  const path = picked.trim();
  return { name: form.name.trim() ? form.name : baseName(path), path };
}

export type PickFolderOutcome = { kind: "picked"; path: string } | { kind: "cancelled" } | { kind: "error"; message: string };

/** 选目录接口的结果 → 界面要做的事：选中填路径，取消什么都不做，失败提示原因。 */
export function pickFolderOutcome(res: PickFolderResult): PickFolderOutcome {
  if (!res.ok) return { kind: "error", message: res.message };
  if ("cancelled" in res) return { kind: "cancelled" };
  const path = res.path.trim();
  return path ? { kind: "picked", path } : { kind: "cancelled" };
}

/**
 * 提交的内容；名称和目录都没写时不能提交。
 * 只写目录用目录名当名称；只写名称则不带目录（应用目录下新建）。
 */
export function workspaceSubmission(form: WorkspaceForm): { name: string; path: string | undefined } | undefined {
  const path = form.path.trim();
  const name = form.name.trim() || (path ? baseName(path) : "");
  if (!name) return undefined;
  return { name, path: path || undefined };
}
