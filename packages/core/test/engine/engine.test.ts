import { afterEach, describe, expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import {
  cleanupAll,
  createHarness,
  expectContiguousSeq,
  startedSteps,
  stateTrail,
  types,
} from "./helpers/harness";

afterEach(cleanupAll);

describe("接缝 1 · 正常路径", () => {
  test("draft → admission → running → qc → awaiting_review → delivered，事件序列完整", async () => {
    const h = await createHarness();
    const settled = await h.run();

    expect(settled.state).toBe("awaiting_review");
    expect(settled.pause).toMatchObject({ code: "awaiting_review", options: ["approve", "return"] });
    expect(settled.steps.map((s) => [s.id, s.status])).toEqual([
      ["admission", "done"],
      ["load_data", "done"],
      ["compute_metrics", "done"],
      ["interpret_feedback", "skipped"],
      ["match_causes", "done"],
      ["write_report", "done"],
      ["render_document", "done"],
      ["qc", "done"],
    ]);

    const approved = await h.cmd(settled.id, { type: "approve" }, "reviewer");
    expect(approved.ok).toBe(true);
    const done = await h.engine.waitForSettled(settled.id);
    expect(done.state).toBe("delivered");

    const events = await h.engine.events(settled.id);
    expectContiguousSeq(events);
    expect(events[0]!.type).toBe("task_created");
    expect(stateTrail(events)).toEqual(["draft", "admission", "running", "qc", "awaiting_review", "delivered"]);
    expect(startedSteps(events)).toEqual([
      "admission",
      "load_data",
      "compute_metrics",
      "match_causes",
      "write_report",
      "render_document",
      "qc",
    ]);
    expect(types(events)).toContain("model_called");
    expect(types(events)).toContain("qc_result");
    expect(types(events)).toContain("review_decision");
    expect(types(events).filter((t) => t === "step_completed")).toHaveLength(7);
    // 计算步骤的工具调用都留痕
    const toolCalls = events.filter((e) => e.type === "tool_called").map((e) => (e as { tool: string }).tool);
    expect(toolCalls).toEqual(["validate_submission", "load_sales", "compute_metrics", "render_report_docx"]);

    // 交付物：Word 与检验记录
    expect(done.deliverables.map((d) => d.kind).sort()).toEqual(["docx", "qc_record"]);
    for (const d of done.deliverables) expect(existsSync(d.path)).toBe(true);
  });

  test("任务目录布局：events.jsonl、attachments、config 快照、artifacts、deliverables", async () => {
    const h = await createHarness();
    const settled = await h.run();
    const taskDir = join(h.workspaceDir, "tasks", settled.id);
    for (const name of ["events.jsonl", "attachments", "config", "artifacts", "deliverables"]) {
      expect(existsSync(join(taskDir, name))).toBe(true);
    }
    expect(existsSync(join(taskDir, "config", "workflow.yaml"))).toBe(true);

    // events.jsonl 一行一个事件
    const lines = readFileSync(join(taskDir, "events.jsonl"), "utf8").trimEnd().split("\n");
    expect(lines.length).toBe((await h.engine.events(settled.id)).length);
    expect(() => lines.forEach((l) => JSON.parse(l))).not.toThrow();

    // 步骤输出按 sha256 命名
    const completed = (await h.engine.events(settled.id)).find((e) => e.type === "step_completed");
    if (completed?.type !== "step_completed") throw new Error("缺少 step_completed");
    expect(completed.outputPath).toContain(completed.outputSha256);
    expect(existsSync(completed.outputPath)).toBe(true);
  });
});

describe("接缝 1 · 非法与越权命令", () => {
  test("发起人执行 approve → forbidden_role；draft 执行 retry → illegal_transition；都留痕", async () => {
    const h = await createHarness();
    const settled = await h.run();
    expect(settled.state).toBe("awaiting_review");

    const forbidden = await h.cmd(settled.id, { type: "approve" }, "initiator");
    expect(forbidden).toMatchObject({ ok: false, error: "forbidden_role" });

    const draft = await h.create();
    const illegal = await h.cmd(draft.id, { type: "retry" }, "operator");
    expect(illegal).toMatchObject({ ok: false, error: "illegal_transition" });

    const e1 = (await h.engine.events(settled.id)).filter((e) => e.type === "transition_rejected");
    expect(e1).toHaveLength(1);
    expect(e1[0]).toMatchObject({ from: "awaiting_review", trigger: "approve", actor: "initiator" });
    const e2 = (await h.engine.events(draft.id)).filter((e) => e.type === "transition_rejected");
    expect(e2).toHaveLength(1);
    expect(e2[0]).toMatchObject({ from: "draft", trigger: "retry", actor: "operator" });

    // 被拒绝的命令不改变状态
    expect((await h.engine.get(settled.id))?.state).toBe("awaiting_review");
    expect((await h.engine.get(draft.id))?.state).toBe("draft");
  });

  test("审核人不能提交表单；未知任务返回 not_found", async () => {
    const h = await createHarness();
    const draft = await h.create();
    expect(await h.cmd(draft.id, { type: "submit" }, "reviewer")).toMatchObject({ ok: false, error: "forbidden_role" });
    expect(await h.cmd("nope", { type: "submit" }, "initiator")).toMatchObject({ ok: false, error: "not_found" });
  });
});
