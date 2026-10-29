import { afterEach, describe, expect, test } from "bun:test";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { TaskEvent } from "../../src/contract";
import {
  cleanupAll,
  createHarness,
  createStubJudgment,
  createStubQc,
  expectContiguousSeq,
  failingChecks,
  passingChecks,
  startedSteps,
  stateTrail,
} from "./helpers/harness";

afterEach(cleanupAll);

const count = (events: TaskEvent[], type: string, pred: (e: any) => boolean = () => true) =>
  events.filter((e) => e.type === type && pred(e)).length;

describe("接缝 1 · 准入不通过与补充", () => {
  test("准入不通过 → awaiting_initiator → supplement → 从准入重新开始并走完", async () => {
    const h = await createHarness();
    const paused = await h.run({ sales: false });

    expect(paused.state).toBe("awaiting_initiator");
    expect(paused.pause).toMatchObject({ code: "missing_sales", options: ["supplement", "terminate"] });
    expect(paused.pause?.message).toContain("销售明细");
    expect(startedSteps(await h.engine.events(paused.id))).toEqual(["admission"]);

    const dir = mkdtempSync(join(tmpdir(), "de-att-"));
    const file = join(dir, "销售明细.xlsx");
    writeFileSync(file, "x");
    const res = await h.cmd(paused.id, { type: "supplement", form: { sales: true }, attachments: [{ name: "销售明细.xlsx", sourcePath: file }] }, "initiator");
    expect(res.ok).toBe(true);
    const settled = await h.engine.waitForSettled(paused.id);

    expect(settled.state).toBe("awaiting_review");
    expect(settled.attachments.map((a) => a.name)).toEqual(["销售明细.xlsx"]);
    const events = await h.engine.events(paused.id);
    expect(startedSteps(events).filter((s) => s === "admission")).toHaveLength(2);
    expect(stateTrail(events)).toEqual([
      "draft", "admission", "awaiting_initiator", "admission", "running", "qc", "awaiting_review",
    ]);
    expectContiguousSeq(events);
  });

  test("minutes_missing 提供“无纪要继续”，选择后表单带 allowMissingMinutes=true", async () => {
    const h = await createHarness();
    h.tools.setHandler("validate_submission", (args) =>
      args.allowMissingMinutes === true
        ? { ok: true }
        : { ok: false, pause: { code: "minutes_missing", message: "缺少本周纪要" } },
    );
    const paused = await h.run();
    expect(paused.pause).toMatchObject({ code: "minutes_missing", options: ["supplement", "continue_without_minutes", "terminate"] });

    await h.cmd(paused.id, { type: "continue_without_minutes" }, "initiator");
    const settled = await h.engine.waitForSettled(paused.id);
    expect(settled.state).toBe("awaiting_review");
    expect(settled.form.allowMissingMinutes).toBe(true);
  });

  test("发起人在待补充时终止", async () => {
    const h = await createHarness();
    const paused = await h.run({ sales: false });
    expect((await h.cmd(paused.id, { type: "terminate" }, "operator"))).toMatchObject({ ok: false, error: "forbidden_role" });
    const res = await h.cmd(paused.id, { type: "terminate" }, "initiator");
    expect(res.ok && res.snapshot.state).toBe("terminated");
  });
});

describe("接缝 1 · 工具失败与重试", () => {
  test("一直失败 → 重试次数等于配置 → awaiting_operator → retry 后从该步骤继续", async () => {
    const h = await createHarness(); // toolRetry.attempts = 3
    h.tools.failTimes("load_sales", Infinity);
    const paused = await h.run();

    expect(paused.state).toBe("awaiting_operator");
    expect(paused.pause).toMatchObject({ code: "tool_failed", options: ["retry", "terminate"] });
    expect(paused.pause?.message).toContain("load_sales");
    expect(paused.pause?.message).toContain("已完成 1/8 步");
    expect(h.tools.callCount("load_sales")).toBe(3);
    const events = await h.engine.events(paused.id);
    expect(events.filter((e) => e.type === "tool_called" && e.tool === "load_sales").map((e) => (e as any).attempt)).toEqual([1, 2, 3]);
    expect(count(events, "step_failed")).toBe(1);

    h.tools.failTimes("load_sales", 0);
    expect(await h.cmd(paused.id, { type: "retry" }, "initiator")).toMatchObject({ ok: false, error: "forbidden_role" });
    expect((await h.cmd(paused.id, { type: "retry" }, "operator")).ok).toBe(true);
    const settled = await h.engine.waitForSettled(paused.id);
    expect(settled.state).toBe("awaiting_review");

    const after = await h.engine.events(paused.id);
    // 从失败的步骤继续：准入不再重跑
    expect(startedSteps(after).filter((s) => s === "admission")).toHaveLength(1);
    expect(startedSteps(after).filter((s) => s === "load_data")).toHaveLength(2);
    expectContiguousSeq(after);
  });

  test("工具声明不可重试时只调用一次", async () => {
    const h = await createHarness();
    h.tools.failTimes("load_sales", Infinity, false);
    const paused = await h.run();
    expect(paused.state).toBe("awaiting_operator");
    expect(h.tools.callCount("load_sales")).toBe(1);
  });

  test("重试成功则不暂停", async () => {
    const h = await createHarness();
    h.tools.failTimes("compute_metrics", 2);
    const settled = await h.run();
    expect(settled.state).toBe("awaiting_review");
    expect(h.tools.callCount("compute_metrics")).toBe(3);
  });

  test("可选步骤失败被跳过，任务继续", async () => {
    const h = await createHarness({
      patchWorkflow: (w) => {
        w.steps.find((s: any) => s.id === "load_data").optional = true;
      },
    });
    h.tools.failTimes("load_sales", Infinity);
    const settled = await h.run();
    expect(settled.state).toBe("awaiting_review");
    expect(settled.steps.find((s) => s.id === "load_data")?.status).toBe("skipped");
  });
});

describe("接缝 1 · 质检", () => {
  test("两次不通过后通过：只重跑 fixStep 及其后的步骤", async () => {
    const qc = createStubQc([failingChecks("write_report", "正文出现裸数字"), failingChecks("write_report", "仍有裸数字")]);
    const judgment = createStubJudgment();
    const h = await createHarness({ qc: qc.engine, judgment: judgment.runner });
    const settled = await h.run();

    expect(settled.state).toBe("awaiting_review");
    const events = await h.engine.events(settled.id);
    expect(count(events, "qc_result")).toBe(3);
    expect(count(events, "qc_result", (e) => e.passed)).toBe(1);
    expect(stateTrail(events)).toEqual([
      "draft", "admission", "running", "qc", "running", "qc", "running", "qc", "awaiting_review",
    ]);
    const started = startedSteps(events);
    expect(started.filter((s) => s === "write_report")).toHaveLength(3);
    expect(started.filter((s) => s === "match_causes")).toHaveLength(1);
    expect(started.filter((s) => s === "load_data")).toHaveLength(1);
    // 计算步骤 render_document 入参随草稿变化，重新执行
    expect(started.filter((s) => s === "render_document")).toHaveLength(3);

    // 修正要求带着质检的 issues 交给判断步骤
    const reruns = judgment.runs.filter((r) => r.step.id === "write_report");
    expect(reruns[0]!.corrections).toBeUndefined();
    expect(reruns[1]!.corrections?.join("\n")).toContain("正文出现裸数字");
    expect(reruns[2]!.corrections?.join("\n")).toContain("仍有裸数字");
  });

  test("重跑的判断步骤只收到 fixStep 指向自己的质检问题", async () => {
    const mixed = [
      ...failingChecks("match_causes", "异常 A 没有原因"),
      ...failingChecks("write_report", "正文出现裸数字"),
    ];
    const qc = createStubQc([mixed]);
    const judgment = createStubJudgment();
    const h = await createHarness({ qc: qc.engine, judgment: judgment.runner });
    const settled = await h.run();
    expect(settled.state).toBe("awaiting_review");

    const causesRerun = judgment.runs.filter((r) => r.step.id === "match_causes")[1]!;
    expect(causesRerun.corrections?.join("\n")).toContain("异常 A 没有原因");
    expect(causesRerun.corrections?.join("\n")).not.toContain("裸数字");
    const writeRerun = judgment.runs.filter((r) => r.step.id === "write_report")[1]!;
    expect(writeRerun.corrections?.join("\n")).toContain("正文出现裸数字");
    expect(writeRerun.corrections?.join("\n")).not.toContain("异常 A");
  });

  test("质检始终不通过 → awaiting_operator（qc_exhausted）；无 fixStep 的问题直接转人工", async () => {
    const always = Array.from({ length: 10 }, () => failingChecks("write_report", "不过"));
    const h = await createHarness({ qc: createStubQc(always).engine });
    const settled = await h.run();
    expect(settled.state).toBe("awaiting_operator");
    expect(settled.pause).toMatchObject({ code: "qc_exhausted", options: ["retry", "terminate"] });
    expect(count(await h.engine.events(settled.id), "qc_result")).toBe(3); // 1 次 + 自动修正 2 次

    const h2 = await createHarness({ qc: createStubQc([failingChecks(undefined, "无法自动修正")]).engine });
    const direct = await h2.run();
    expect(direct.pause?.code).toBe("qc_exhausted");
    expect(count(await h2.engine.events(direct.id), "qc_result")).toBe(1);
  });

  test("qc_exhausted 后操作员重试：重新获得自动修正机会并可通过", async () => {
    const queue = Array.from({ length: 3 }, () => failingChecks("write_report", "不过"));
    const qc = createStubQc(queue);
    const h = await createHarness({ qc: qc.engine });
    const paused = await h.run();
    expect(paused.state).toBe("awaiting_operator");
    await h.cmd(paused.id, { type: "retry" }, "operator");
    const settled = await h.engine.waitForSettled(paused.id);
    expect(settled.state).toBe("awaiting_review");
  });

  test("质检输入取自约定步骤的输出", async () => {
    const qc = createStubQc();
    const h = await createHarness({ qc: qc.engine });
    await h.run();
    const input = qc.inputs[0]!;
    expect((input.draft as any).title).toMatch(/^报告#/);
    expect((input.facts as any).revenueCents).toBe(10000);
    expect(input.previousDraft).toBeUndefined();
    expect(input.feedback).toBeUndefined();
  });
});

describe("接缝 1 · 退回与重做", () => {
  test("退回 → 只重跑 rework 步骤，计算步骤的 step_started 不再出现", async () => {
    const qc = createStubQc();
    const judgment = createStubJudgment();
    const h = await createHarness({ qc: qc.engine, judgment: judgment.runner });
    const first = await h.run();
    const before = await h.engine.events(first.id);

    expect(await h.cmd(first.id, { type: "return", comment: "第二段要改" }, "initiator")).toMatchObject({ ok: false, error: "forbidden_role" });
    expect(await h.cmd(first.id, { type: "return", comment: "  " }, "reviewer")).toMatchObject({ ok: false, error: "invalid_input" });
    const res = await h.cmd(first.id, { type: "return", comment: "第二段要改", sections: ["核心指标"] }, "reviewer");
    expect(res.ok).toBe(true);
    const settled = await h.engine.waitForSettled(first.id);
    expect(settled.state).toBe("awaiting_review");
    expect(settled.reworkRound).toBe(1);

    const after = (await h.engine.events(first.id)).filter((e) => e.seq > before.at(-1)!.seq);
    const started = startedSteps(after);
    expect(started).toEqual(["interpret_feedback", "match_causes", "write_report", "render_document", "qc"]);
    for (const computeStep of ["admission", "load_data", "compute_metrics"]) expect(started).not.toContain(computeStep);
    expect(after.find((e) => e.type === "review_decision")).toMatchObject({ decision: "return", comment: "第二段要改", sections: ["核心指标"] });
    expect(after.filter((e) => e.type === "step_started").every((e) => (e as any).reworkRound === 1)).toBe(true);

    // 退回意见作为输入交给判断步骤；重做轮次里 interpret_feedback 才执行
    const feedbackRun = judgment.runs.find((r) => r.step.id === "interpret_feedback")!;
    expect(feedbackRun.context.feedback).toEqual({ comment: "第二段要改", sections: ["核心指标"] });
    expect(feedbackRun.context.previousDraft).toMatchObject({ title: expect.stringMatching(/^报告#/) });
    expect(judgment.runs.filter((r) => r.step.id === "write_report").at(-1)!.corrections?.join("")).toContain("第二段要改");

    // 质检拿到上一版草稿和退回意见
    const secondQc = qc.inputs.at(-1)!;
    expect(secondQc.feedback).toEqual({ comment: "第二段要改", sections: ["核心指标"] });
    expect(secondQc.previousDraft).toMatchObject({ title: expect.stringMatching(/^报告#/) });
    expect((secondQc.previousDraft as any).title).not.toBe((secondQc.draft as any).title);

    // 每一轮各有一份检验记录
    expect(settled.deliverables.find((d) => d.kind === "qc_record")?.path).toContain("r1");
  });

  test("首轮 when: rework 的步骤记为 skipped，重做后变为 done", async () => {
    const h = await createHarness();
    const first = await h.run();
    expect(first.steps.find((s) => s.id === "interpret_feedback")?.status).toBe("skipped");
    await h.cmd(first.id, { type: "return", comment: "改" }, "reviewer");
    const second = await h.engine.waitForSettled(first.id);
    expect(second.steps.find((s) => s.id === "interpret_feedback")?.status).toBe("done");
  });

  test("判断输出 needsHuman → awaiting_operator，code=rework_needs_human", async () => {
    const judgment = createStubJudgment({
      outputs: { interpret_feedback: () => ({ items: [], needsHuman: true, needsHumanReason: "意见要求更换毛利口径" }) },
    });
    const h = await createHarness({ judgment: judgment.runner });
    const first = await h.run();
    await h.cmd(first.id, { type: "return", comment: "毛利按含税算" }, "reviewer");
    const paused = await h.engine.waitForSettled(first.id);
    expect(paused.state).toBe("awaiting_operator");
    expect(paused.pause).toMatchObject({ code: "rework_needs_human", options: ["retry", "terminate"] });
    expect(paused.pause?.message).toContain("毛利口径");
  });
});

describe("接缝 1 · 预算", () => {
  test("模型调用次数超限 → awaiting_operator（limit_exceeded）；重试后重新计算预算", async () => {
    const h = await createHarness({ patchWorkflow: (w) => (w.limits.task.modelCalls = 1) });
    const paused = await h.run();
    expect(paused.state).toBe("awaiting_operator");
    expect(paused.pause).toMatchObject({ code: "limit_exceeded" });
    expect(paused.budget.modelCalls).toEqual({ used: 1, limit: 1 });
    expect(paused.steps.find((s) => s.id === "match_causes")?.status).toBe("done");
    expect(paused.steps.find((s) => s.id === "write_report")?.status).toBe("pending");

    await h.cmd(paused.id, { type: "retry" }, "operator");
    const settled = await h.engine.waitForSettled(paused.id);
    expect(settled.state).toBe("awaiting_review");
  });

  test("总时长超限 → awaiting_operator（limit_exceeded）", async () => {
    let t = Date.parse("2026-10-29T00:00:00Z");
    const judgment = createStubJudgment({ onRun: () => (t += 20_000) });
    const h = await createHarness({
      patchWorkflow: (w) => (w.limits.task.durationSec = 10),
      judgment: judgment.runner,
      now: () => new Date(t),
    });
    const paused = await h.run();
    expect(paused.state).toBe("awaiting_operator");
    expect(paused.pause?.code).toBe("limit_exceeded");
    expect(paused.pause?.message).toContain("时长");
  });

  test("判断执行器报告的失败按原因转人工", async () => {
    const cases = [
      ["step_rounds", "limit_exceeded"],
      ["task_calls", "limit_exceeded"],
      ["step_duration", "limit_exceeded"],
      ["model_error", "tool_failed"],
      ["invalid_output", "tool_failed"],
      ["no_output", "tool_failed"],
    ] as const;
    for (const [failure, code] of cases) {
      const judgment = createStubJudgment({
        override: (input) => (input.step.id === "match_causes" ? { ok: false, failure, message: "测试", modelCalls: 0 } : undefined),
      });
      const h = await createHarness({ judgment: judgment.runner });
      const paused = await h.run();
      expect(paused.state).toBe("awaiting_operator");
      expect(paused.pause?.code).toBe(code);
      expect(paused.pause?.message).toContain("已完成");
    }
  });

  test("判断执行器抛出异常时按模型错误处理，不崩溃", async () => {
    const judgment = createStubJudgment({
      override: () => {
        throw new Error("连接被重置");
      },
    });
    const h = await createHarness({ judgment: judgment.runner });
    const paused = await h.run();
    expect(paused.pause).toMatchObject({ code: "tool_failed" });
    expect(paused.pause?.message).toContain("连接被重置");
  });
});

describe("接缝 1 · 判断步骤回调留痕与引用解析", () => {
  test("model_called / tool_called / tool_denied 回调写入事件", async () => {
    const judgment = createStubJudgment({
      override: async (input) => {
        if (input.step.id !== "match_causes") return undefined;
        await input.onModelCalled({ turn: 1, response: { r: 1 }, durationMs: 3 });
        await input.onToolCalled({ tool: "query_task", args: { a: 1 }, ok: true });
        await input.onToolDenied({ tool: "web_search", reason: "未开放给本步骤" });
        return { ok: true, output: { causes: [] }, modelCalls: 1 };
      },
    });
    const h = await createHarness({ judgment: judgment.runner });
    const settled = await h.run();
    const events = await h.engine.events(settled.id);
    expect(events.find((e) => e.type === "tool_denied")).toMatchObject({ stepId: "match_causes", tool: "web_search" });
    expect(events.find((e) => e.type === "model_called" && e.stepId === "match_causes")).toMatchObject({ turn: 1, response: { r: 1 } });
    expect(events.find((e) => e.type === "tool_called" && e.tool === "query_task")).toMatchObject({ stepId: "match_causes" });
  });

  test("$ 引用：form / steps / task.dir / config 解析后传给工具；幂等键含任务与步骤", async () => {
    const h = await createHarness();
    const settled = await h.run({ n: 5 });
    const load = h.tools.calls.find((c) => c.tool === "load_sales")!;
    expect(load.args).toEqual({ dir: expect.stringContaining(settled.id), n: 5 });
    const metrics = h.tools.calls.find((c) => c.tool === "compute_metrics")!;
    expect(metrics.args).toEqual({ data: { rows: 5 }, threshold: 0.02 });
    const render = h.tools.calls.find((c) => c.tool === "render_report_docx")!;
    expect(render.args.idempotencyKey).toMatch(new RegExp(`^${settled.id}-render_document-r0-[0-9a-f]{16}$`));
    expect((render.args.report as any).title).toMatch(/^报告#/);
  });

  test("引用不存在时解析为 null，不报错", async () => {
    const h = await createHarness({
      patchWorkflow: (w) => (w.steps.find((s: any) => s.id === "load_data").input.missing = "$steps.nope.a.b"),
    });
    const settled = await h.run();
    expect(settled.state).toBe("awaiting_review");
    expect(h.tools.calls.find((c) => c.tool === "load_sales")!.args.missing).toBeNull();
  });
});

describe("接缝 1 · 查询接口", () => {
  test("get / list / events(afterSeq) / subscribe", async () => {
    const h = await createHarness();
    const draft = await h.create();
    const seen: number[] = [];
    const off = h.engine.subscribe(draft.id, (e) => seen.push(e.seq));
    await h.cmd(draft.id, { type: "submit" }, "initiator");
    const settled = await h.engine.waitForSettled(draft.id);
    off();
    expect(seen.length).toBeGreaterThan(5);
    expect(seen).toEqual(Array.from({ length: seen.length }, (_, i) => i + 2)); // 订阅在 task_created（seq 1）之后

    expect((await h.engine.get(settled.id))?.lastSeq).toBe(settled.lastSeq);
    expect(await h.engine.get("nope")).toBeUndefined();
    const after = await h.engine.events(settled.id, 10);
    expect(after[0]!.seq).toBe(11);

    const list = await h.engine.list();
    expect(list).toHaveLength(1);
    expect(list[0]).toMatchObject({ id: settled.id, employee: "mini", state: "awaiting_review" });
    expect(list[0]!.title).toContain("2026 年第 44 周");
  });

  test("任务使用启动时的配置快照，之后修改员工配置不影响运行中的任务", async () => {
    const h = await createHarness();
    const draft = await h.create();
    const snapshotHash = draft.configHash;
    writeFileSync(join(h.employeesDir, "mini", "SKILL.md"), "改过了");
    const judgment = createStubJudgment();
    const engine2 = h.newEngine({ judgment: judgment.runner });
    await engine2.command(draft.id, { type: "submit" }, "initiator");
    await engine2.waitForSettled(draft.id);
    expect(judgment.runs[0]!.systemPrompt).toContain("槽位"); // 旧版 SKILL.md
    expect(judgment.runs[0]!.systemPrompt).not.toContain("改过了");
    const fresh = await h.create();
    expect(fresh.configHash).not.toBe(snapshotHash);
  });
});

describe("接缝 1 · T7 接线：准入工具故障、needs_human、质检输入源", () => {
  test("准入工具重试用尽 → awaiting_operator（tool_failed）；operator 重试回到准入并走完", async () => {
    const h = await createHarness();
    h.tools.failTimes("validate_submission", Infinity);
    const paused = await h.run();

    expect(paused.state).toBe("awaiting_operator");
    expect(paused.pause).toMatchObject({ code: "tool_failed", options: ["retry", "terminate"] });
    expect(paused.pause?.message).toContain("validate_submission");
    expect(h.tools.callCount("validate_submission")).toBe(3);
    const failedEvents = await h.engine.events(paused.id);
    expect(failedEvents.filter((e) => e.type === "state_changed").at(-1)).toMatchObject({ from: "admission", to: "awaiting_operator", trigger: "tool_failed" });

    // 发起人不能重试；操作员重试后回到准入校验，而不是 running
    expect(await h.cmd(paused.id, { type: "retry" }, "initiator")).toMatchObject({ ok: false, error: "forbidden_role" });
    h.tools.failTimes("validate_submission", 0);
    const res = await h.cmd(paused.id, { type: "retry" }, "operator");
    expect(res.ok).toBe(true);
    const settled = await h.engine.waitForSettled(paused.id);
    expect(settled.state).toBe("awaiting_review");
    const events = await h.engine.events(paused.id);
    expect(stateTrail(events)).toEqual(["draft", "admission", "awaiting_operator", "admission", "running", "qc", "awaiting_review"]);
    expectContiguousSeq(events);
  });

  test("运行中的工具故障，重试仍回到 running 并从失败的步骤继续", async () => {
    const h = await createHarness();
    h.tools.failTimes("compute_metrics", Infinity);
    const paused = await h.run();
    h.tools.failTimes("compute_metrics", 0);
    await h.cmd(paused.id, { type: "retry" }, "operator");
    await h.engine.waitForSettled(paused.id);
    const trail = stateTrail(await h.engine.events(paused.id));
    expect(trail.slice(0, 5)).toEqual(["draft", "admission", "running", "awaiting_operator", "running"]);
  });

  test("判断输出 needsHuman 走 needs_human 触发器，不再复用 tool_failed", async () => {
    const judgment = createStubJudgment({
      outputs: { interpret_feedback: () => ({ items: [], needsHuman: true, needsHumanReason: "意见要求更换毛利口径" }) },
    });
    const h = await createHarness({ judgment: judgment.runner });
    const first = await h.run();
    await h.cmd(first.id, { type: "return", comment: "毛利按含税算" }, "reviewer");
    await h.engine.waitForSettled(first.id);
    const events = await h.engine.events(first.id);
    expect(events.filter((e) => e.type === "state_changed").at(-1)).toMatchObject({ to: "awaiting_operator", trigger: "needs_human" });
  });

  test("质检输入源步骤 id 读自 workflow.qcSources，而不是写死", async () => {
    const rename = (w: Record<string, any>) => {
      const text = JSON.stringify(w)
        .replaceAll('"write_report"', '"compose_report"')
        .replaceAll("$steps.write_report", "$steps.compose_report");
      for (const key of Object.keys(w)) delete w[key];
      Object.assign(w, JSON.parse(text), { qcSources: { draft: "compose_report" } });
    };
    const qc = createStubQc();
    const h = await createHarness({ patchWorkflow: rename, qc: qc.engine });
    const first = await h.run();
    expect(first.state).toBe("awaiting_review");
    expect(first.steps.map((s) => s.id)).toContain("compose_report");
    expect((qc.inputs[0]!.draft as any).title).toMatch(/^报告#/);

    // 退回后，“上一版草稿”也取自配置里的草稿步骤
    await h.cmd(first.id, { type: "return", comment: "改" }, "reviewer");
    await h.engine.waitForSettled(first.id);
    expect(qc.inputs.at(-1)!.previousDraft).toMatchObject({ title: expect.stringMatching(/^报告#/) });
  });
});

void passingChecks;

describe("接缝 1 · QC fixStep 配置", () => {
  test("QcInput.fixSteps 默认为场景 A 的 id；workflow.qcFixSteps 可改，write 缺省跟随 qcSources.draft", async () => {
    const qc = createStubQc();
    const h = await createHarness({ qc: qc.engine });
    await h.run();
    expect(qc.inputs[0]!.fixSteps).toEqual({ write: "write_report", causes: "match_causes" });

    const qc2 = createStubQc();
    const h2 = await createHarness({
      qc: qc2.engine,
      patchWorkflow: (w) => {
        w.qcFixSteps = { causes: "interpret_feedback" };
      },
    });
    await h2.run();
    expect(qc2.inputs[0]!.fixSteps).toEqual({ write: "write_report", causes: "interpret_feedback" });
  });
});