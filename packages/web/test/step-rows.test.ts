import { describe, expect, test } from "bun:test";
import { deriveStepRows } from "../src/view/step-rows";
import { seqOf, type EventInput } from "./helpers";

// 步骤定义带业务说法（工作流配置里的 activity）：对话里的步骤行说人话。
const DEFS = [
  { id: "admission", label: "准入校验", kind: "compute" as const, activity: "检查材料是否齐全" },
  { id: "load", label: "读取数据", kind: "compute" as const, activity: "读取销售明细" },
  { id: "match", label: "匹配原因", kind: "judge" as const, activity: "匹配异常原因" },
  { id: "write", label: "撰写周报", kind: "judge" as const, activity: "撰写经营周报" },
  { id: "qc", label: "自动质检", kind: "qc" as const },
];

const submit: EventInput[] = [
  { type: "state_changed", from: "draft", to: "admission", trigger: "submit", actor: "initiator" },
  { type: "state_changed", from: "admission", to: "running", trigger: "admission_passed", actor: "system" },
];

const started = (id: string, reworkRound = 0): EventInput => ({ type: "step_started", stepId: id, attempt: 1, reworkRound });
const completed = (id: string, reworkRound = 0): EventInput => ({
  type: "step_completed",
  stepId: id,
  reworkRound,
  outputPath: `${id}.json`,
  outputSha256: "x",
});

describe("deriveStepRows：每个步骤一行", () => {
  const events = seqOf([
    ...submit,
    started("admission"),
    completed("admission"),
    started("load"),
    { type: "tool_called", stepId: "load", callId: "c1", tool: "load_sales", args: {}, attempt: 1 },
    { type: "tool_result", callId: "c1", ok: true },
    completed("load"),
    started("match"),
    { type: "model_called", stepId: "match", turn: 1, response: {}, durationMs: 1200, connection: "deepseek", modelName: "chat" },
    completed("match"),
    started("qc"),
  ]);

  test("按定义顺序列出全部步骤，业务说法优先", () => {
    const rows = deriveStepRows(events, DEFS);
    expect(rows.map((row) => row.label)).toEqual([
      "检查材料是否齐全",
      "读取销售明细",
      "匹配异常原因",
      "撰写经营周报",
      "自动质检", // 没有 activity 时退回标题
    ]);
    expect(rows.map((row) => row.kind)).toEqual(["compute", "compute", "judge", "judge", "qc"]);
  });

  test("状态从步骤事件推导：完成、运行中、还没轮到", () => {
    const rows = deriveStepRows(events, DEFS);
    expect(rows.map((row) => row.status)).toEqual(["done", "done", "done", "pending", "running"]);
  });

  test("耗时取步骤开始到结束的间隔", () => {
    const rows = deriveStepRows(events, DEFS);
    // seqOf 每个事件间隔 1 秒：admission 两个事件相邻（1000ms），match 中间夹了一次模型调用
    expect(rows[0]!.durationMs).toBe(1000);
    expect(rows[2]!.durationMs).toBe(2000);
    expect(rows[3]!.durationMs).toBeUndefined();
  });

  test("所用模型取判断步骤最后一次模型调用的连接与模型名", () => {
    const rows = deriveStepRows(events, DEFS);
    expect(rows[2]!.model).toBe("deepseek / chat");
    expect(rows[0]!.model).toBeUndefined();
  });
});

describe("deriveStepRows：运行中、失败与重做", () => {
  test("运行中的步骤按传入的当前时间算已用时间", () => {
    const events = seqOf([...submit, started("match")]);
    const nowMs = Date.parse(events.at(-1)!.ts) + 2500;
    const rows = deriveStepRows(events, DEFS, { nowMs });
    expect(rows[2]!.status).toBe("running");
    expect(rows[2]!.durationMs).toBe(2500);
  });

  test("剧本模式没有模型名时退回会话模型", () => {
    const events = seqOf([
      ...submit,
      started("match"),
      { type: "model_called", stepId: "match", turn: 1, response: {}, durationMs: 800 },
    ]);
    const rows = deriveStepRows(events, DEFS, { fallbackModel: "scripted / scripted-model" });
    expect(rows[2]!.model).toBe("scripted / scripted-model");
  });

  test("失败的步骤带原因；可选步骤被跳过时显示跳过", () => {
    const failed = deriveStepRows(
      seqOf([...submit, started("load"), { type: "step_failed", stepId: "load", error: "读取超时" }]),
      DEFS,
    );
    expect(failed[1]).toMatchObject({ status: "failed", error: "读取超时" });

    const skipped = deriveStepRows(
      seqOf([...submit, started("load"), { type: "step_failed", stepId: "load", error: "读取超时" }]),
      DEFS.map((def) => (def.id === "load" ? { ...def, status: "skipped" as const } : def)),
    );
    expect(skipped[1]!.status).toBe("skipped");
  });

  test("退回重做后，重跑的步骤重新变成运行中，耗时按新一轮算", () => {
    const events = seqOf([
      ...submit,
      started("write"),
      completed("write"),
      { type: "review_decision", decision: "return", comment: "原因不够具体" },
      { type: "state_changed", from: "awaiting_review", to: "running", trigger: "return", actor: "reviewer" },
      started("write", 1),
      completed("write", 1),
    ]);
    const rows = deriveStepRows(events, DEFS);
    expect(rows[3]).toMatchObject({ status: "done", durationMs: 1000 });
    // 首轮完成的步骤保留完成状态，不会因为新一轮开始退回未开始
    expect(rows[0]!.status).toBe("pending");
  });

  test("没有事件时全部等待，并给出全部步骤", () => {
    const rows = deriveStepRows([], DEFS);
    expect(rows).toHaveLength(5);
    expect(rows.every((row) => row.status === "pending")).toBe(true);
  });
});
