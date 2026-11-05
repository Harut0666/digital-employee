// 权限与审批的纯函数（接缝 D）：预设、审批策略、固定提示与二次确认文案。
//
// 诚实边界（Spec 0004「审批的诚实边界」）：本应用是本机单进程，没有系统级文件沙箱；
// 三档预设只是设置文档里的值，用于审批确认与审计留痕，不改变操作系统文件权限，
// 工具白名单当前也没有接入执行层。界面文案必须与这一点保持一致，不能声称有沙箱隔离。
import type { ApprovalPolicy, DefaultRole, SandboxMode } from "@de/core/contract";
import { ROLE_LABELS } from "../../view/permissions";

export interface SandboxPreset {
  mode: SandboxMode;
  label: string;
  description: string;
}

/** 三档预设（顺序与设置页按钮一致）。 */
export const SANDBOX_PRESETS: readonly SandboxPreset[] = [
  {
    mode: "read-only",
    label: "只读",
    description: "声明本次工作按只读边界处理（记录与审批提示）。当前版本不限制工具读文件的能力，也不改变文件权限。",
  },
  {
    mode: "workspace-write",
    label: "工作区可写",
    description: "默认档：声明任务只在工作区内产生交付物、附件与配置备份。它同样只写设置值，不改变操作系统权限。",
  },
  {
    mode: "danger-full-access",
    label: "完全访问",
    description: "声明允许完全访问；审批是否跳过由「审批策略」决定。本机模式仍没有系统级沙箱，误操作会直接落到本机文件上。",
  },
];

export const APPROVAL_OPTIONS: readonly { value: ApprovalPolicy; label: string; description: string }[] = [
  { value: "ask", label: "每次询问（默认）", description: "危险操作前先弹确认框，确认后执行并写审计。" },
  { value: "never", label: "从不询问", description: "危险操作不再弹确认框，直接执行；服务端仍会写审计。建议只在演示时短暂使用。" },
];

export const DEFAULT_ROLE_OPTIONS = (Object.keys(ROLE_LABELS) as DefaultRole[]).map((role) => ({
  value: role,
  label: ROLE_LABELS[role],
}));

/** 固定提示（T23 要求原文）；与下面的细节一起，避免对外声称有沙箱隔离。 */
export const SECURITY_NOTE = "本机模式：不提供系统级文件沙箱；预设影响审批确认与审计留痕。";

/** 诚实说明：预设只是设置文档里的值，不改真实文件权限，也不做网络控制。 */
export const SECURITY_DETAIL =
  "本机模式不改变操作系统文件权限，也不做网络控制。当前版本三档预设只写入设置文档，用于审批确认与审计留痕；工具白名单与系统级沙箱尚未接入执行层。";

export function presetOf(mode: SandboxMode): SandboxPreset {
  return SANDBOX_PRESETS.find((preset) => preset.mode === mode) ?? SANDBOX_PRESETS[1]!;
}

export function isDangerousSandboxMode(mode: SandboxMode): boolean {
  return mode === "danger-full-access";
}

export function isDangerousApprovalPolicy(policy: ApprovalPolicy): boolean {
  return policy === "never";
}

export function approvalLabel(policy: ApprovalPolicy): string {
  return APPROVAL_OPTIONS.find((option) => option.value === policy)?.label ?? policy;
}

/** 切预设的确认正文：写清会发生什么、不会发生什么。 */
export function sandboxConfirmContent(mode: SandboxMode): string {
  if (isDangerousSandboxMode(mode)) {
    return (
      "切到「完全访问」后：声明允许完全访问，审批是否跳过由「审批策略」决定；本机模式没有系统级沙箱，" +
      "也不会改变真实文件权限，但误操作会直接作用在本机文件上。每次切换都会写审计。仍要切换？"
    );
  }
  return `切换到「${presetOf(mode).label}」：只写入设置文档，用于审批确认与审计留痕；不改变真实文件权限。仍要切换？`;
}

/** 切审批策略的确认正文。 */
export function approvalConfirmContent(policy: ApprovalPolicy): string {
  if (isDangerousApprovalPolicy(policy)) {
    return (
      "切到「从不询问」后：危险操作不再弹确认框，但每次仍会写审计；本机模式没有系统级沙箱，" +
      "误操作会直接生效。演示结束后建议切回「每次询问」。仍要切换？"
    );
  }
  return "切回「每次询问」：危险操作会先弹确认框，确认后才执行并写审计。仍要切换？";
}
