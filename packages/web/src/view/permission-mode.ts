// 权限模式（T48）的界面派生。纯函数，不依赖 React 与网络。
// 模式和权限确认卡的内容都来自后端的会话接口，这里只决定显示成什么；按钮取自卡片上的 actions。

import type { PermissionDecision, PermissionMode, SessionCard, SessionMessage, UiRole } from "../api/types";

export interface PermissionModeInfo {
  value: PermissionMode;
  label: string;
  /** 下拉里的一句简短说明 */
  hint: string;
}

/** 五个模式，从严到松；说明和 Spec 0007 的矩阵一致 */
export const PERMISSION_MODES: readonly PermissionModeInfo[] = [
  { value: "plan", label: "计划", hint: "只读：Agent 只看不动，给你一个方案" },
  { value: "ask", label: "询问", hint: "写文件、运行员工前先问你" },
  { value: "accept_edits", label: "接受编辑", hint: "在工作区里写、改文件不用问" },
  { value: "auto", label: "自动", hint: "员工表单完整有效时直接开工" },
  { value: "yolo", label: "放开", hint: "连目录文件的确认也免掉" },
];

const DEFAULT_MODE: PermissionMode = "ask";

function infoOf(mode: string | undefined): PermissionModeInfo {
  return PERMISSION_MODES.find((m) => m.value === mode) ?? PERMISSION_MODES.find((m) => m.value === DEFAULT_MODE)!;
}

/** 下拉上显示的模式名；旧数据没有模式时按询问算 */
export function permissionModeLabel(mode: string | undefined): string {
  return infoOf(mode).label;
}

export interface PermissionMenuItem extends PermissionModeInfo {
  selected: boolean;
}

/** 下拉里的五项，当前模式标出来 */
export function permissionMenuItems(current: string | undefined): PermissionMenuItem[] {
  const picked = infoOf(current).value;
  return PERMISSION_MODES.map((m) => ({ ...m, selected: m.value === picked }));
}

/** 当前模式的说明（下拉按钮的提示文字） */
export function permissionHint(mode: string | undefined): string {
  return infoOf(mode).hint;
}

// ---- 权限确认卡 ----

export type PermissionState = "pending" | "allowed" | "always" | "denied" | "cancelled";

export interface PermissionButton {
  decision: PermissionDecision;
  label: string;
  style: "primary" | "default" | "danger";
}

export interface PermissionCardView {
  requestId: string;
  action: string;
  actionLabel: string;
  object: string;
  detail?: string;
  state: PermissionState;
  stateText: string;
  /** 现在能不能答复：还在等、是发起人、会话可写 */
  canAnswer: boolean;
  buttons: PermissionButton[];
}

function stateOf(card: SessionCard): PermissionState {
  if (card.pending) return "pending";
  switch (card.decision) {
    case "allow_once":
      return "allowed";
    case "always_allow":
      return "always";
    case "deny":
      return "denied";
    default:
      return "cancelled";
  }
}

const STATE_TEXT: Record<Exclude<PermissionState, "pending">, string> = {
  allowed: "已允许一次",
  always: "已选择本会话总是允许这一类",
  denied: "已拒绝",
  cancelled: "已取消（这一轮被停止）",
};

/** 权限确认卡 → 显示用的内容。发起人才有按钮；别的角色和只读会话只看得到在等谁。 */
export function derivePermissionCard(card: SessionCard, opts: { writable: boolean; role: UiRole }): PermissionCardView {
  const state = stateOf(card);
  const canAnswer = state === "pending" && opts.writable && opts.role === "initiator";
  const buttons: PermissionButton[] = canAnswer
    ? (card.choices ?? []).map((c) => ({ decision: c.decision, label: c.label, style: c.style }))
    : [];
  return {
    requestId: card.requestId ?? "",
    action: card.action ?? "",
    actionLabel: card.actionLabel ?? "",
    object: card.object ?? card.path ?? "",
    detail: card.detail || undefined,
    state,
    stateText: state === "pending" ? (canAnswer ? "等你答复" : "等待发起人答复") : STATE_TEXT[state],
    canAnswer,
    buttons,
  };
}

// ---- 写文件的工具行 ----

export interface FileChangeView {
  tool: "write_file" | "edit_file";
  ok: boolean;
  path: string;
  action?: "created" | "modified";
  bytes?: number;
  /** 一行说明：新建 / 修改了哪个文件、多少字节；失败时写明原因 */
  summary: string;
  /** 点开看的内容：write_file 是写进去的文本，edit_file 是差异摘要 */
  body?: string;
  bodyKind?: "content" | "diff";
}

const WRITE_TOOLS = ["write_file", "edit_file"] as const;
const isWriteTool = (tool: string | undefined): tool is (typeof WRITE_TOOLS)[number] => WRITE_TOOLS.includes(tool as never);
/** 点开的内容最多显示多少字，免得把对话撑得太长 */
const BODY_LIMIT = 4000;

const record = (value: unknown): Record<string, unknown> | undefined =>
  typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Record<string, unknown>) : undefined;

const clip = (text: string) => (text.length > BODY_LIMIT ? `${text.slice(0, BODY_LIMIT)}\n…（后面还有 ${text.length - BODY_LIMIT} 个字）` : text);

/** 一条工具留痕 → 对话里的“写文件”工具行；不是写文件、或被停止没有执行时返回 undefined */
export function fileChangeOf(message: SessionMessage): FileChangeView | undefined {
  if (message.kind !== "tool_called" || !isWriteTool(message.tool)) return undefined;
  const tool = message.tool;
  const args = record(message.args) ?? {};
  const result = record(message.result);
  const requested = typeof args.path === "string" ? args.path : "";
  if (result && typeof result.error === "string") {
    const why = typeof result.message === "string" ? result.message : (message.reason ?? "");
    return { tool, ok: false, path: requested, summary: `没能${tool === "write_file" ? "写" : "改"} ${requested}：${why}` };
  }
  if (!result || typeof result.path !== "string") {
    // 被停止（结果是一句“已取消”）或别的失败原因：有原因就写出来，被停止的不显示这一行
    if (message.reason) return { tool, ok: false, path: requested, summary: `没能${tool === "write_file" ? "写" : "改"} ${requested}：${message.reason}` };
    return undefined;
  }
  const action = result.action === "created" ? "created" : "modified";
  const bytes = typeof result.bytes === "number" ? result.bytes : undefined;
  const view: FileChangeView = {
    tool,
    ok: true,
    path: result.path,
    action,
    bytes,
    summary: `${action === "created" ? "新建" : "修改"} ${result.path}${bytes === undefined ? "" : ` · ${bytes} 字节`}`,
  };
  if (tool === "edit_file" && typeof result.diff === "string") {
    view.body = clip(result.diff);
    view.bodyKind = "diff";
  } else if (tool === "write_file" && typeof args.content === "string") {
    view.body = clip(args.content);
    view.bodyKind = "content";
  }
  return view;
}

// ---- 权限留痕 ----

export interface PermissionTrace {
  tone: "allowed" | "denied";
  text: string;
}

/** 受权限关口管着的工具：它们的拒绝才算权限留痕（越权调用别的工具只进日志） */
const GATED_TOOLS = new Set<string>([...WRITE_TOOLS, "run_employee", "use_folder_file"]);

/** 自动放行、被边界或模式拒绝 → 对话里的一行；用户拒绝已体现在确认卡上，不重复 */
export function traceOf(message: SessionMessage): PermissionTrace | undefined {
  const target = (() => {
    const path = record(message.args)?.path;
    return `${message.tool ?? ""}${typeof path === "string" ? ` ${path}` : ""}`;
  })();
  if (message.kind === "permission_allowed") return { tone: "allowed", text: `已放行 ${target}：${message.reason ?? ""}` };
  if (message.kind === "tool_denied" && GATED_TOOLS.has(message.tool ?? "") && !(message.reason ?? "").startsWith("用户拒绝")) {
    return { tone: "denied", text: `已拒绝 ${target}：${message.reason ?? ""}` };
  }
  return undefined;
}
