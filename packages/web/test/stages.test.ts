import { describe, expect, test } from "bun:test";
import { deriveStages } from "../src/view/stages";
import { seqOf, type EventInput } from "./helpers";

const STEPS = [
  { id: "admission", label: "准入校验", kind: "compute" as const, activity: "正在检查材料是否齐全" },
  { id: "load", label: "读取数据", kind: "compute" as const, activity: "正在整理销售数据" },
  { id: "write", label: "撰写周报叙述", kind: "judge" as const, activity: "正在撰写周报" },
  { id: "qc", label: "质检", kind: "qc" as const, activity: "正在质检" },
];

const statuses = (line: ReturnType<typeof deriveStages>) => line.stages.map((s) => `${s.label}:${s.status}`);

const submitted: EventInput[] = [
  { type: "state_changed", from: "draft", to: "admission", trigger: "submit", actor: "initiator" },
  { type: "step_started", stepId: "admission", attempt: 1, reworkRound: 0 },
  { type: "step_completed", stepId: "admission", reworkRound: 0, outputPath: "a.json", outputSha256: "x" },
  { type: "state_changed", from: "admission", to: "running", trigger: "admission_passed", actor: "system" },
];

describe("deriveStages：发起、准入、执行、质检、审核、交付", () => {
  test("草稿停在发起", () => {
    const line = deriveStages([], STEPS, "draft");
    expect(statuses(line)).toEqual(["发起:current", "准入:pending", "执行:pending", "质检:pending", "审核:pending", "交付:pending"]);
  });

  test("执行中显示正在运行的步骤的业务话，不显示工具名", () => {
    const events = seqOf([...submitted, { type: "step_started", stepId: "write", attempt: 1, reworkRound: 0 }]);
    const line = deriveStages(events, STEPS, "running");
    expect(statuses(line)).toEqual(["发起:done", "准入:done", "执行:current", "质检:pending", "审核:pending", "交付:pending"]);
    expect(line.activity).toBe("正在撰写周报");
  });

  test("步骤没写业务话时退回步骤名", () => {
    const events = seqOf([...submitted, { type: "step_started", stepId: "write", attempt: 1, reworkRound: 0 }]);
    const bare = STEPS.map(({ activity: _, ...step }) => step);
    expect(deriveStages(events, bare, "running").activity).toBe("撰写周报叙述");
  });

  test("准入时暂停：停在准入，显示暂停标题", () => {
    const events = seqOf([
      { type: "state_changed", from: "draft", to: "admission", trigger: "submit", actor: "initiator" },
      {
        type: "state_changed",
        from: "admission",
        to: "awaiting_initiator",
        trigger: "admission_failed",
        actor: "system",
        pause: { code: "minutes_missing", message: "缺纪要", options: [] },
      },
    ]);
    const line = deriveStages(events, STEPS, "awaiting_initiator", "缺少本周纪要");
    expect(statuses(line)).toEqual(["发起:done", "准入:paused", "执行:pending", "质检:pending", "审核:pending", "交付:pending"]);
    expect(line.activity).toBe("缺少本周纪要");
  });

  test("执行中暂停停在执行；终止停在终止前的阶段", () => {
    const paused = seqOf([
      ...submitted,
      { type: "state_changed", from: "running", to: "awaiting_operator", trigger: "tool_failed", actor: "system", pause: { code: "tool_failed", message: "失败", options: [] } },
    ]);
    expect(statuses(deriveStages(paused, STEPS, "awaiting_operator"))[2]).toBe("执行:paused");

    const stopped = seqOf([...submitted, { type: "state_changed", from: "running", to: "terminated", trigger: "terminate", actor: "initiator" }]);
    const line = deriveStages(stopped, STEPS, "terminated");
    expect(statuses(line)).toEqual(["发起:done", "准入:done", "执行:stopped", "质检:pending", "审核:pending", "交付:pending"]);
    expect(line.activity).toBe("已终止");
  });

  test("待审核停在审核；交付后全部完成", () => {
    const review = deriveStages(seqOf(submitted), STEPS, "awaiting_review", "待审核");
    expect(statuses(review)).toEqual(["发起:done", "准入:done", "执行:done", "质检:done", "审核:current", "交付:pending"]);
    expect(review.activity).toBe("待审核");
    const delivered = deriveStages(seqOf(submitted), STEPS, "delivered");
    expect(delivered.stages.every((s) => s.status === "done")).toBe(true);
    expect(delivered.activity).toBe("已交付");
  });

  test("退回重做时回到执行，审核重新等待", () => {
    const events = seqOf([
      ...submitted,
      { type: "state_changed", from: "awaiting_review", to: "running", trigger: "return", actor: "reviewer" },
      { type: "step_started", stepId: "write", attempt: 2, reworkRound: 1 },
    ]);
    const line = deriveStages(events, STEPS, "running");
    expect(statuses(line).slice(2)).toEqual(["执行:current", "质检:pending", "审核:pending", "交付:pending"]);
    expect(line.activity).toBe("正在撰写周报");
  });
});
