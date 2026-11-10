// 权限模式的界面派生（T48，ADR-0007 增补二改成三档）。纯函数，不依赖 React 与网络。
// 模式和权限确认卡的内容都来自后端的会话接口，这里只决定显示成什么；按钮取自卡片上的 actions。

import type { PermissionDecision, PermissionMode, SessionCard, SessionMessage, UiRole } from "../api/types";

export interface PermissionModeInfo {
  value: PermissionMode;
  label: string;
  /** 下拉里的一句简短说明 */
  hint: string;
  /** 显示色调：safe 偏安全、default 常规、warn 提醒（完全权限；不是报错色，别吓人） */
  tone: "safe" | "default" | "warn";
}

/** 三个模式，从严到松（也是菜单顺序）；说明和 ADR-0007 增补二的矩阵一致 */
export const PERMISSION_MODES: readonly PermissionModeInfo[] = [
  { value: "read_only", label: "仅可查看", tone: "safe", hint: "只读文件、做计划、回答问题，不写文件、不运行员工和命令" },
  { value: "workspace", label: "仅限工作区", tone: "default", hint: "可在工作区目录内读写文件，运行员工、使用目录文件、运行命令前先问你" },
  { value: "full", label: "完全权限", tone: "warn", hint: "不再逐项询问，写文件、运行员工、用目录文件、运行命令都直接执行——命令仍可能触达工作区之外，请谨慎" },
];

const DEFAULT_MODE: PermissionMode = "workspace";

/** 旧版本五档的值 → 新值（后端已经把旧数据读作新值，这里只是防备更早缓存下来的数据） */
const LEGACY_MODES: Readonly<Record<string, PermissionMode>> = {
  plan: "read_only",
  ask: "workspace",
  accept_edits: "workspace",
  auto: "workspace",
  yolo: "full",
};

/** 任何取值 → 三个新值之一：没有值（新会话、旧数据）按默认的仅限工作区，旧五档的值映射过来，不认得的按最严格的仅可查看 */
export function normalizePermissionMode(mode: string | undefined): PermissionMode {
  if (mode === undefined || mode === "") return DEFAULT_MODE;
  if (PERMISSION_MODES.some((m) => m.value === mode)) return mode as PermissionMode;
  return LEGACY_MODES[mode] ?? "read_only";
}

function infoOf(mode: string | undefined): PermissionModeInfo {
  const value = normalizePermissionMode(mode);
  return PERMISSION_MODES.find((m) => m.value === value)!;
}

/** 下拉上显示的模式名；旧数据没有模式时按仅限工作区算 */
export function permissionModeLabel(mode: string | undefined): string {
  return infoOf(mode).label;
}

/** 当前模式的色调（完全权限用提醒色） */
export function permissionTone(mode: string | undefined): PermissionModeInfo["tone"] {
  return infoOf(mode).tone;
}

export interface PermissionMenuItem extends PermissionModeInfo {
  selected: boolean;
}

/** 下拉里的三项，当前模式标出来 */
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
  /** shell 确认卡（权限确认卡的变体）：原样显示将要运行的命令、工作目录和醒目的风险提示（尽力而为，不是安全边界） */
  shell?: { command: string; workdir: string; warnings: string[] };
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
    // shell 的对象就是命令块，不再重复一句“运行命令”
    object: card.action === "run_command" ? "" : (card.object ?? card.path ?? ""),
    detail: card.detail || undefined,
    state,
    stateText: state === "pending" ? (canAnswer ? "等你答复" : "等待发起人答复") : STATE_TEXT[state],
    canAnswer,
    buttons,
    ...(card.action === "run_command" || card.tool === "shell"
      ? { shell: { command: card.command ?? "", workdir: card.workdir ?? "", warnings: card.warnings ?? [] } }
      : {}),
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
const GATED_TOOLS = new Set<string>([...WRITE_TOOLS, "run_employee", "use_folder_file", "shell"]);

/** 自动放行、被边界或模式拒绝 → 对话里的一行；发起人拒绝已体现在确认卡上，不重复 */
export function traceOf(message: SessionMessage): PermissionTrace | undefined {
  const target = (() => {
    const args = record(message.args);
    // shell：留痕里写出完整命令（长的只显示前面一段）
    if (message.tool === "shell" && typeof args?.command === "string") {
      const command = args.command.length > 200 ? `${args.command.slice(0, 200)}…` : args.command;
      return `shell ${command}`;
    }
    const path = args?.path;
    return `${message.tool ?? ""}${typeof path === "string" ? ` ${path}` : ""}`;
  })();
  if (message.kind === "permission_allowed") return { tone: "allowed", text: `已放行 ${target}：${message.reason ?? ""}` };
  if (message.kind === "tool_denied" && GATED_TOOLS.has(message.tool ?? "") && message.decision !== "deny") {
    return { tone: "denied", text: `已拒绝 ${target}：${message.reason ?? ""}` };
  }
  return undefined;
}
