import { describe, expect, test } from "bun:test";
import type { TaskAction } from "../src/api/types";
import { actionPlan, needsModelSettings } from "../src/view/permissions";

const retry: TaskAction = { command: "retry", label: "重试", roles: ["initiator", "operator"], style: "primary" };
const terminate: TaskAction = { command: "terminate", label: "终止", roles: ["operator"], style: "danger" };
const toolFailed = [retry, terminate];
const waiting = ["initiator", "operator"] as const;

describe("actionPlan", () => {
  test("按钮只取快照里属于当前角色的操作，顺序不变", () => {
    expect(actionPlan(toolFailed, [...waiting], "operator").mine).toEqual([retry, terminate]);
    expect(actionPlan(toolFailed, [...waiting], "initiator").mine).toEqual([retry]);
    expect(actionPlan(toolFailed, [...waiting], "reviewer").mine).toEqual([]);
  });

  test("当前角色没有操作时，说明在等谁，并能切到每个被等的角色", () => {
    expect(actionPlan(toolFailed, [...waiting], "reviewer").handoff).toEqual({
      text: "等待发起人、操作员处理",
      switchTo: ["initiator", "operator"],
    });
  });

  test("当前角色只有部分操作时，说明其余操作归谁", () => {
    expect(actionPlan(toolFailed, [...waiting], "initiator").handoff).toEqual({
      text: "「终止」由操作员处理",
      switchTo: ["operator"],
    });
  });

  test("多个操作归同一角色时合并成一句", () => {
    const review: TaskAction[] = [
      { command: "approve", label: "通过", roles: ["reviewer"], style: "primary" },
      { command: "terminate", label: "终止", roles: ["operator"], style: "danger" },
      { command: "return", label: "退回", roles: ["operator"], style: "default" },
    ];
    expect(actionPlan(review, ["reviewer", "operator"], "reviewer").handoff).toEqual({
      text: "「终止」「退回」由操作员处理",
      switchTo: ["operator"],
    });
  });

  test("当前角色能做全部操作，或任务不等任何人时，不提示切换", () => {
    expect(actionPlan(toolFailed, [...waiting], "operator").handoff).toBeUndefined();
    expect(actionPlan([], [], "reviewer").handoff).toBeUndefined();
  });
});

describe("needsModelSettings", () => {
  test("模型不可用时要给设置入口", () => {
    expect(needsModelSettings({ code: "model_unavailable" })).toBe(true);
  });

  test("旧工作区里，模型调用失败记在工具故障下，同样要给设置入口", () => {
    expect(needsModelSettings({ code: "tool_failed", detail: { failure: "model_error" } })).toBe(true);
  });

  test("普通工具故障和其他暂停不给", () => {
    expect(needsModelSettings({ code: "tool_failed", detail: { tool: "parse_minutes" } })).toBe(false);
    expect(needsModelSettings({ code: "minutes_missing" })).toBe(false);
  });
});
