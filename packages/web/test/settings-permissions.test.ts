// 权限与审批的纯函数（接缝 D）：三档预设、审批策略、危险确认文案与固定提示。
// 这些文案要能直接给评委看：既不能夸大（本机模式没有系统级沙箱），也要说清确认后的后果。
import { describe, expect, test } from "bun:test";
import { ROLE_LABELS } from "../src/view/permissions";
import {
  APPROVAL_OPTIONS,
  DEFAULT_ROLE_OPTIONS,
  SANDBOX_PRESETS,
  SECURITY_DETAIL,
  SECURITY_NOTE,
  approvalConfirmContent,
  approvalLabel,
  isDangerousApprovalPolicy,
  isDangerousSandboxMode,
  presetOf,
  sandboxConfirmContent,
} from "../src/pages/settings/permissions";

describe("权限预设与审批纯函数", () => {
  test("三档预设顺序与默认值：只读 / 工作区可写 / 完全访问", () => {
    expect(SANDBOX_PRESETS.map((p) => p.mode)).toEqual(["read-only", "workspace-write", "danger-full-access"]);
    expect(SANDBOX_PRESETS.map((p) => p.label)).toEqual(["只读", "工作区可写", "完全访问"]);
    expect(presetOf("workspace-write").label).toBe("工作区可写");
    for (const preset of SANDBOX_PRESETS) expect(preset.description.length).toBeGreaterThan(0);
  });

  test("审批策略：每次询问 / 从不，默认每次询问", () => {
    expect(APPROVAL_OPTIONS.map((o) => o.value)).toEqual(["ask", "never"]);
    expect(APPROVAL_OPTIONS[0]!.label).toContain("询问");
    expect(approvalLabel("ask")).toContain("每次");
    expect(approvalLabel("never")).toContain("从不");
  });

  test("默认角色选项与标签：发起人 / 审核人 / 操作员", () => {
    expect(DEFAULT_ROLE_OPTIONS.map((o) => o.value)).toEqual(["initiator", "reviewer", "operator"]);
    expect(ROLE_LABELS.initiator).toBe("发起人");
    expect(ROLE_LABELS.reviewer).toBe("审核人");
    expect(ROLE_LABELS.operator).toBe("操作员");
  });

  test("危险判定：完全访问与从不询问需要二次确认，其余不需要", () => {
    expect(isDangerousSandboxMode("danger-full-access")).toBe(true);
    expect(isDangerousSandboxMode("read-only")).toBe(false);
    expect(isDangerousSandboxMode("workspace-write")).toBe(false);
    expect(isDangerousApprovalPolicy("never")).toBe(true);
    expect(isDangerousApprovalPolicy("ask")).toBe(false);
  });

  test("固定提示必须诚实：本机模式、无系统级文件沙箱、不改真实文件权限", () => {
    expect(SECURITY_NOTE).toContain("本机模式");
    expect(SECURITY_NOTE).toContain("不提供系统级文件沙箱");
    expect(SECURITY_NOTE).toContain("审批确认");
    expect(SECURITY_NOTE).toContain("审计留痕");
    expect(SECURITY_DETAIL).toContain("不改变");
    expect(SECURITY_DETAIL).toContain("文件权限");
    expect(SECURITY_DETAIL).toContain("工具白名单");
  });

  test("确认文案写清后果：完全访问 / 从不询问各自的代价", () => {
    const sandbox = sandboxConfirmContent("danger-full-access");
    expect(sandbox).toContain("完全访问");
    expect(sandbox).toContain("系统级沙箱");
    expect(sandbox).toContain("审计");
    expect(approvalConfirmContent("never")).toContain("不再");
    expect(approvalConfirmContent("never")).toContain("审计");
    expect(approvalConfirmContent("ask")).toContain("每次");
  });
});
