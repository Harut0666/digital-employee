import { describe, expect, test } from "bun:test";
import type { PauseReason } from "@de/core/contract";
import { deriveSessionView } from "../src/view/session-view";
import { STEP_DEFS, seqOf, type EventInput } from "./helpers";

const created: EventInput = {
  type: "task_created",
  employee: "weekly-report",
  configHash: "h",
  form: { period: { year: 2026, week: 44 } },
  attachments: [],
};

describe("deriveSessionView: 工具故障暂停", () => {
  const events = seqOf([
    created,
    { type: "state_changed", from: "draft", to: "admission", trigger: "submit", actor: "initiator" },
    { type: "state_changed", from: "admission", to: "running", trigger: "admission_passed", actor: "system" },
    { type: "step_started", stepId: "admission", attempt: 1, reworkRound: 0 },
    { type: "step_completed", stepId: "admission", reworkRound: 0, outputPath: "a.json", outputSha256: "x" },
    { type: "step_started", stepId: "load", attempt: 1, reworkRound: 0 },
    { type: "tool_called", stepId: "load", callId: "c1", tool: "load_sales", args: {}, attempt: 1 },
    { type: "tool_result", callId: "c1", ok: false, error: "超时" },
    { type: "step_failed", stepId: "load", error: "读取数据工具超时" },
    {
      type: "state_changed",
      from: "running",
      to: "awaiting_operator",
      trigger: "tool_failed",
      actor: "system",
      pause: {
        code: "tool_failed",
        message: "读取数据工具超时，已重试 3 次，已完成 1/5 步",
        options: ["retry", "terminate"],
      },
    },
  ]);

  const view = deriveSessionView(events, STEP_DEFS);

  test("当前状态取自最后一次 state_changed", () => {
    expect(view.state).toBe("awaiting_operator");
  });

  test("步骤状态：完成、失败、未开始", () => {
    const byId = Object.fromEntries(view.steps.map((s) => [s.id, s.status]));
    expect(byId).toEqual({ admission: "done", load: "failed", match: "pending", write: "pending", qc: "pending" });
    expect(view.steps.find((s) => s.id === "load")?.error).toBe("读取数据工具超时");
  });

  test("对话流依次是：用户提交、系统开始执行、暂停卡（执行过程在轨迹里）", () => {
    expect(view.cards.map((c) => c.kind)).toEqual(["user", "agent", "pause"]);
  });

  test("暂停卡带原因和命令选项，且处于待处理", () => {
    const pause = view.cards.at(-1);
    expect(pause).toMatchObject({
      kind: "pause",
      active: true,
      state: "awaiting_operator",
      pause: { code: "tool_failed", options: ["retry", "terminate"] },
    });
    expect(view.pause?.code).toBe("tool_failed");
  });

  test("统计：工具调用与失败次数", () => {
    expect(view.counts).toEqual({ modelCalls: 0, toolCalls: 1, toolFailures: 1 });
  });

  test("用时只计自动执行阶段：从提交到进入暂停", () => {
    expect(view.elapsedMs).toBe(8000);
  });
});

describe("deriveSessionView: 暂停被处理后", () => {
  const events = seqOf([
    created,
    { type: "state_changed", from: "draft", to: "admission", trigger: "submit", actor: "initiator" },
    {
      type: "state_changed",
      from: "admission",
      to: "awaiting_initiator",
      trigger: "admission_failed",
      actor: "system",
      pause: { code: "minutes_missing", message: "缺少第 44 周周会纪要", options: ["supplement", "continue_without_minutes", "terminate"] },
    },
    { type: "state_changed", from: "awaiting_initiator", to: "admission", trigger: "continue_without_minutes", actor: "initiator" },
    { type: "state_changed", from: "admission", to: "running", trigger: "admission_passed", actor: "system" },
  ]);
  const view = deriveSessionView(events, STEP_DEFS);

  test("旧暂停卡变为已处理，视图上不再有暂停", () => {
    const pauseCard = view.cards.find((c) => c.kind === "pause");
    expect(pauseCard).toMatchObject({ kind: "pause", active: false });
    expect(view.pause).toBeUndefined();
    expect(view.state).toBe("running");
  });

  test("发起人的选择以用户消息出现在流里", () => {
    const users = view.cards.filter((c) => c.kind === "user");
    expect(users.map((c) => (c as { text: string }).text)).toEqual(["已提交表单，请开始处理", "无纪要继续"]);
  });
});

const reviewPause: PauseReason = { code: "awaiting_review", message: "质检通过，请审核", options: ["approve", "return"] };
const qcChecks = [
  { id: "structure", label: "结构完整", passed: true, issues: [] },
  { id: "traceability", label: "数据溯源", passed: true, issues: [] },
];

const toReview: EventInput[] = [
  created,
  { type: "state_changed", from: "draft", to: "admission", trigger: "submit", actor: "initiator" },
  { type: "state_changed", from: "admission", to: "running", trigger: "admission_passed", actor: "system" },
  { type: "step_started", stepId: "write", attempt: 1, reworkRound: 0 },
  { type: "model_called", stepId: "write", turn: 1, response: {}, durationMs: 800 },
  { type: "step_completed", stepId: "write", reworkRound: 0, outputPath: "w.json", outputSha256: "x" },
  { type: "state_changed", from: "running", to: "qc", trigger: "steps_completed", actor: "system" },
  { type: "qc_result", attempt: 1, passed: true, checks: qcChecks },
  { type: "deliverable_created", kind: "docx", path: "report.docx", sha256: "a" },
  { type: "deliverable_created", kind: "qc_record", path: "qc.md", sha256: "b" },
  { type: "state_changed", from: "qc", to: "awaiting_review", trigger: "qc_passed", actor: "system", pause: reviewPause },
];

describe("deriveSessionView: 待审核", () => {
  const view = deriveSessionView(seqOf([...toReview]), STEP_DEFS);

  test("出现质检卡和审核卡，审核卡待处理", () => {
    expect(view.cards.map((c) => c.kind)).toEqual(["user", "agent", "qc", "review"]);
    expect(view.cards.at(-1)).toMatchObject({ kind: "review", active: true });
  });

  test("质检卡带全部检查项", () => {
    expect(view.cards.find((c) => c.kind === "qc")).toMatchObject({ kind: "qc", passed: true, attempt: 1, checks: qcChecks });
    expect(view.qc?.checks).toHaveLength(2);
  });

  test("交付物列表在审核前就可预览", () => {
    expect(view.deliverables.map((d) => d.kind)).toEqual(["docx", "qc_record"]);
    expect(view.counts.modelCalls).toBe(1);
  });

  test("审核通过后卡片记录决定，随后出现已交付卡", () => {
    const v = deriveSessionView(
      seqOf([
        ...toReview,
        { type: "review_decision", decision: "approve" },
        { type: "state_changed", from: "awaiting_review", to: "delivered", trigger: "approve", actor: "reviewer" },
      ]),
      STEP_DEFS,
    );
    const review = v.cards.find((c) => c.kind === "review");
    expect(review).toMatchObject({ active: false, decision: { decision: "approve" } });
    expect(v.cards.at(-1)).toMatchObject({ kind: "delivered" });
    expect(v.state).toBe("delivered");
  });

  test("退回：保留意见和章节，步骤取最新一轮", () => {
    const v = deriveSessionView(
      seqOf([
        ...toReview,
        { type: "review_decision", decision: "return", comment: "华南异常原因不够具体", sections: ["异常与原因"] },
        { type: "state_changed", from: "awaiting_review", to: "running", trigger: "return", actor: "reviewer" },
        { type: "step_started", stepId: "write", attempt: 1, reworkRound: 1 },
      ]),
      STEP_DEFS,
    );
    const review = v.cards.find((c) => c.kind === "review");
    expect(review).toMatchObject({
      active: false,
      decision: { decision: "return", comment: "华南异常原因不够具体", sections: ["异常与原因"] },
    });
    // 右栏步骤取最新一轮：只有被重跑的步骤
    expect(v.steps.map((s) => [s.id, s.status])).toEqual([["write", "running"]]);
  });
});

describe("deriveSessionView: 质检不通过与非法操作", () => {
  const view = deriveSessionView(
    seqOf([
      created,
      { type: "state_changed", from: "draft", to: "admission", trigger: "submit", actor: "initiator" },
      { type: "qc_result", attempt: 1, passed: false, checks: [{ id: "traceability", label: "数据溯源", passed: false, issues: ["数字 12.5 找不到来源"], fixStep: "write" }] },
      { type: "transition_rejected", from: "awaiting_review", trigger: "approve", actor: "initiator", reason: "发起人不能审核" },
      { type: "state_changed", from: "awaiting_initiator", to: "terminated", trigger: "terminate", actor: "initiator" },
    ]),
    STEP_DEFS,
  );

  test("被拒绝的流转以提示卡留痕，终止以提示卡出现", () => {
    const notices = view.cards.filter((c) => c.kind === "notice");
    expect(notices.map((c) => (c as { text: string }).text)).toEqual(["操作被拒绝：发起人不能审核", "任务已终止"]);
  });

  test("未通过的质检卡保留问题清单", () => {
    expect(view.cards.find((c) => c.kind === "qc")).toMatchObject({ passed: false });
    expect(view.qc?.checks[0]?.issues).toEqual(["数字 12.5 找不到来源"]);
  });
});

describe("deriveSessionView: 重做后的交付物", () => {
  test("同类交付物以最新一份为准", () => {
    const v = deriveSessionView(
      seqOf([
        created,
        { type: "deliverable_created", kind: "docx", path: "v1.docx", sha256: "1" },
        { type: "deliverable_created", kind: "docx", path: "v2.docx", sha256: "2" },
      ]),
      STEP_DEFS,
    );
    expect(v.deliverables).toEqual([{ kind: "docx", path: "v2.docx", sha256: "2" }]);
  });
});


describe("deriveSessionView: 用时不含暂停等待", () => {
  test("暂停期间的时间不计入", () => {
    const events = seqOf([
      created,
      { type: "state_changed", from: "draft", to: "admission", trigger: "submit", actor: "initiator" },
      { type: "state_changed", from: "admission", to: "running", trigger: "admission_passed", actor: "system" },
      { type: "state_changed", from: "running", to: "awaiting_operator", trigger: "tool_failed", actor: "system", pause: { code: "tool_failed", message: "x", options: ["retry"] } },
      { type: "state_changed", from: "awaiting_operator", to: "running", trigger: "retry", actor: "operator" },
      { type: "state_changed", from: "running", to: "qc", trigger: "steps_completed", actor: "system" },
    ]).map((e, i) => (i >= 4 ? { ...e, ts: new Date(Date.parse(e.ts) + 3_600_000).toISOString() } : e));
    // 提交(1s) → 暂停(3s)：2s；一小时后重试 → 质检：1s
    expect(deriveSessionView(events, STEP_DEFS).elapsedMs).toBe(3000);
  });
});
