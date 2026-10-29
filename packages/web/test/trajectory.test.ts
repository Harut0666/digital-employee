import { describe, expect, test } from "bun:test";
import { deriveTrajectory } from "../src/view/trajectory";
import { STEP_DEFS, seqOf, type EventInput } from "./helpers";

const submit: EventInput[] = [
  { type: "state_changed", from: "draft", to: "admission", trigger: "submit", actor: "initiator" },
  { type: "state_changed", from: "admission", to: "running", trigger: "admission_passed", actor: "system" },
];

const step = (id: string, reworkRound = 0): EventInput[] => [
  { type: "step_started", stepId: id, attempt: 1, reworkRound },
  { type: "step_completed", stepId: id, reworkRound, outputPath: `${id}.json`, outputSha256: "x" },
];

describe("deriveTrajectory：步骤为骨架、事件为叶子", () => {
  const events = seqOf([
    ...submit,
    ...step("admission"),
    { type: "step_started", stepId: "load", attempt: 1, reworkRound: 0 },
    { type: "tool_called", stepId: "load", callId: "c1", tool: "load_sales", args: {}, attempt: 1 },
    { type: "tool_result", callId: "c1", ok: false, error: "读取超时" },
    { type: "tool_called", stepId: "load", callId: "c2", tool: "load_sales", args: {}, attempt: 2 },
    { type: "tool_result", callId: "c2", ok: true },
    { type: "step_completed", stepId: "load", reworkRound: 0, outputPath: "load.json", outputSha256: "x" },
    { type: "step_started", stepId: "match", attempt: 1, reworkRound: 0 },
    { type: "model_called", stepId: "match", turn: 1, response: {}, durationMs: 1200, usage: { input: 100, output: 20 } },
    { type: "step_completed", stepId: "match", reworkRound: 0, outputPath: "match.json", outputSha256: "x" },
    { type: "step_started", stepId: "qc", attempt: 1, reworkRound: 0 },
    {
      type: "qc_result",
      attempt: 1,
      passed: false,
      checks: [
        { id: "traceability", label: "数据溯源", passed: false, issues: ["数字 12.5 找不到来源"], fixStep: "write" },
        { id: "structure", label: "结构完整", passed: true, issues: [] },
      ],
    },
  ]);
  const view = deriveTrajectory(events, STEP_DEFS);

  test("首轮按定义列出全部步骤，状态与耗时来自步骤事件", () => {
    expect(view.rounds.map((r) => r.round)).toEqual([0]);
    const steps = view.rounds[0]!.items.filter((i) => i.kind === "step");
    expect(steps.map((s) => [s.label, s.status])).toEqual([
      ["准入校验", "done"],
      ["读取数据", "done"],
      ["匹配原因", "done"],
      ["撰写周报", "pending"],
      ["自动质检", "running"],
    ]);
    expect(steps[0]!.meta?.stepKind).toBe("compute");
    expect(steps[0]!.meta?.durationMs).toBe(1000);
  });

  test("工具调用挂在所属步骤下：失败、重试与错误都在叶子上", () => {
    const load = view.rounds[0]!.items.find((i) => i.label === "读取数据")!;
    expect(load.children).toHaveLength(2);
    expect(load.children![0]).toMatchObject({ kind: "tool", label: "load_sales", status: "failed", error: "读取超时", meta: { attempt: 1, durationMs: 1000 } });
    expect(load.children![1]).toMatchObject({ kind: "tool", status: "done", meta: { attempt: 2 } });
  });

  test("模型轮次挂在判断步骤下，带时长与 token", () => {
    const match = view.rounds[0]!.items.find((i) => i.label === "匹配原因")!;
    expect(match.children).toHaveLength(1);
    expect(match.children![0]).toMatchObject({ kind: "model", label: "模型第 1 轮", meta: { durationMs: 1200, usage: { input: 100, output: 20 } } });
  });

  test("质检不通过列为过程行，未通过项作为子项", () => {
    const qc = view.rounds[0]!.items.find((i) => i.kind === "qc")!;
    expect(qc).toMatchObject({ label: "质检第 1 次：未通过", status: "failed", meta: { passed: false } });
    expect(qc.children!.map((c) => [c.label, c.detail])).toEqual([["数据溯源", "数字 12.5 找不到来源"]]);
  });

  test("摘要：完成数、工具/模型调用与失败数", () => {
    expect(view.summary).toEqual({ steps: 5, done: 3, failed: 0, toolCalls: 2, toolFailures: 1, modelCalls: 1 });
    expect(view.active).toBe(true);
    expect(view.activeStepId).toBe("qc");
    expect(view.lastSeq).toBe(events.length);
  });
});

describe("deriveTrajectory：重做与暂停", () => {
  const round0: EventInput[] = [
    ...submit,
    ...step("admission"),
    ...step("write"),
    { type: "state_changed", from: "running", to: "qc", trigger: "steps_completed", actor: "system" },
    { type: "qc_result", attempt: 1, passed: true, checks: [{ id: "structure", label: "结构完整", passed: true, issues: [] }] },
    {
      type: "state_changed",
      from: "qc",
      to: "awaiting_review",
      trigger: "qc_passed",
      actor: "system",
      pause: { code: "awaiting_review", message: "已通过质检，等待审核人决定。", options: ["approve", "return"] },
    },
  ];

  test("退回后新开一轮，只列实际重跑的步骤，退回意见作为过程行", () => {
    const view = deriveTrajectory(
      seqOf([
        ...round0,
        { type: "review_decision", decision: "return", comment: "华南异常原因不够具体" },
        { type: "state_changed", from: "awaiting_review", to: "running", trigger: "return", actor: "reviewer" },
        { type: "step_started", stepId: "write", attempt: 1, reworkRound: 1 },
        { type: "step_completed", stepId: "write", reworkRound: 1, outputPath: "w1.json", outputSha256: "x" },
      ]),
      STEP_DEFS,
    );
    expect(view.rounds.map((r) => r.round)).toEqual([0, 1]);
    const second = view.rounds[1]!;
    expect(second.items.map((i) => [i.kind, i.label, i.status])).toEqual([
      ["state", "审核退回，开始重做", "info"],
      ["step", "撰写周报", "done"],
    ]);
    expect(second.items[0]!.detail).toContain("按退回意见重做");
    // 摘要按最新一轮算：这一轮只重跑了 write
    expect(view.summary.done).toBe(1);
  });

  test("暂停与失败：状态行带暂停说明，失败步骤计入摘要，active=false", () => {
    const view = deriveTrajectory(
      seqOf([
        ...submit,
        { type: "step_started", stepId: "load", attempt: 1, reworkRound: 0 },
        { type: "tool_called", stepId: "load", callId: "c1", tool: "load_sales", args: {}, attempt: 1 },
        { type: "tool_result", callId: "c1", ok: false, error: "超时" },
        { type: "step_failed", stepId: "load", error: "读取数据工具超时，已重试 3 次" },
        {
          type: "state_changed",
          from: "running",
          to: "awaiting_operator",
          trigger: "tool_failed",
          actor: "system",
          pause: { code: "tool_failed", message: "读取数据工具超时，已重试 3 次", options: ["retry", "terminate"] },
        },
      ]),
      STEP_DEFS,
    );
    expect(view.active).toBe(false);
    expect(view.summary.failed).toBe(1);
    const last = view.rounds[0]!.items.at(-1)!;
    expect(last).toMatchObject({ kind: "state", label: "任务暂停，等待处理", detail: "读取数据工具超时，已重试 3 次" });
    expect(view.rounds[0]!.items.find((i) => i.label === "读取数据")).toMatchObject({ status: "failed", error: "读取数据工具超时，已重试 3 次" });
  });

  test("没有事件时给出空轨迹但保留步骤总数", () => {
    const view = deriveTrajectory([], STEP_DEFS);
    expect(view.rounds).toEqual([]);
    expect(view.summary.steps).toBe(5);
    expect(view.active).toBe(false);
    expect(view.lastSeq).toBe(0);
  });
});
