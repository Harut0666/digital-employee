import { afterEach, describe, expect, test } from "bun:test";
import { appendFileSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { SimulatedCrash } from "../../src/engine";
import type { TaskEvent } from "../../src/contract";
import {
  cleanupAll,
  createHarness,
  createStubJudgment,
  createStubQc,
  expectContiguousSeq,
  failingChecks,
  startedSteps,
  stateTrail,
} from "./helpers/harness";

afterEach(cleanupAll);

/** 每个步骤最终输出的 sha256（render_document 的输出带任务目录路径，不参与比较） */
function outputHashes(events: TaskEvent[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const e of events) {
    if (e.type === "step_completed" && e.stepId !== "render_document") out[e.stepId] = e.outputSha256;
  }
  return out;
}

describe("接缝 1 · 断点恢复", () => {
  test("在判断步骤崩溃 → 新引擎 resumeAll → 已完成的步骤不重跑，seq 连续，结果与不崩溃时一致", async () => {
    // 参照：不崩溃的一次完整运行
    const reference = createStubJudgment();
    const hRef = await createHarness({ judgment: reference.runner });
    const ref = await hRef.run();
    const refEvents = await hRef.engine.events(ref.id);

    // 崩溃：共用同一个桩（计数器连续），第二次启动才是“新进程”
    const judgment = createStubJudgment();
    const h = await createHarness({ judgment: judgment.runner, faults: { crashAtStep: "write_report" } });
    const draft = await h.create();
    await h.cmd(draft.id, { type: "submit" }, "initiator");
    await expect(h.engine.waitForSettled(draft.id)).rejects.toBeInstanceOf(SimulatedCrash);

    const crashed = await h.engine.events(draft.id);
    expect(crashed.at(-1)!.type).toBe("fault_injected");
    expect(crashed.some((e) => e.type === "step_completed" && e.stepId === "write_report")).toBe(false);
    expect(crashed.some((e) => e.type === "model_called" && e.stepId === "write_report")).toBe(true);
    const callsBeforeCrash = judgment.realCalls();

    const restarted = h.newEngine({ faults: undefined, judgment: judgment.runner });
    await restarted.resumeAll();
    const settled = await restarted.waitForSettled(draft.id);
    expect(settled.state).toBe("awaiting_review");

    const events = await restarted.events(draft.id);
    expectContiguousSeq(events);
    // 已完成的步骤没有新的 step_started；崩溃的步骤重新开始一次
    const started = startedSteps(events);
    for (const s of ["admission", "load_data", "compute_metrics", "match_causes"]) {
      expect(started.filter((x) => x === s)).toHaveLength(1);
    }
    expect(started.filter((x) => x === "write_report")).toHaveLength(2);

    // 模型响应按日志重放，没有重新调用模型
    expect(judgment.realCalls()).toBe(callsBeforeCrash);
    const resumedRun = judgment.runs.filter((r) => r.step.id === "write_report").at(-1)!;
    expect(resumedRun.replay).toHaveLength(1);

    // 最终结果与不崩溃时一致
    expect(outputHashes(events)).toEqual(outputHashes(refEvents));
    expect(stateTrail(events)).toEqual(stateTrail(refEvents));
  });

  test("在计算步骤崩溃也能恢复", async () => {
    const h = await createHarness({ faults: { crashAtStep: "compute_metrics" } });
    const draft = await h.create();
    await h.cmd(draft.id, { type: "submit" }, "initiator");
    await expect(h.engine.waitForSettled(draft.id)).rejects.toBeInstanceOf(SimulatedCrash);
    expect(h.tools.callCount("compute_metrics")).toBe(0);

    const restarted = h.newEngine({ faults: undefined });
    await restarted.resumeAll();
    const settled = await restarted.waitForSettled(draft.id);
    expect(settled.state).toBe("awaiting_review");
    const events = await restarted.events(draft.id);
    expectContiguousSeq(events);
    expect(startedSteps(events).filter((s) => s === "load_data")).toHaveLength(1);
    expect(h.tools.callCount("load_sales")).toBe(1);
  });

  test("崩溃的引擎实例不再写任何事件", async () => {
    const h = await createHarness({ faults: { crashAtStep: "load_data" } });
    const draft = await h.create();
    await h.cmd(draft.id, { type: "submit" }, "initiator");
    await h.engine.waitForSettled(draft.id).catch(() => undefined);
    const n = (await h.engine.events(draft.id)).length;
    await expect(h.cmd(draft.id, { type: "terminate" }, "initiator")).rejects.toBeInstanceOf(SimulatedCrash);
    expect((await h.engine.events(draft.id)).length).toBe(n);
  });

  test("resumeAll 不动暂停态和终态的任务", async () => {
    const h = await createHarness();
    const paused = await h.run({ sales: false });
    const done = await h.run();
    await h.cmd(done.id, { type: "approve" }, "reviewer");
    const e1 = await h.engine.events(paused.id);

    const restarted = h.newEngine();
    await restarted.resumeAll();
    expect((await restarted.get(paused.id))?.state).toBe("awaiting_initiator");
    expect((await restarted.get(done.id))?.state).toBe("delivered");
    expect(await restarted.events(paused.id)).toEqual(e1);
  });

  test("在质检自动修正中途崩溃：恢复后继续修正并通过", async () => {
    const qc = createStubQc([failingChecks("write_report", "裸数字")]);
    const h = await createHarness({ qc: qc.engine, faults: { crashAtStep: "write_report" } });
    // 第一次 write_report 正常崩溃点在首轮，先让它崩溃并恢复，再看修正
    const draft = await h.create();
    await h.cmd(draft.id, { type: "submit" }, "initiator");
    await h.engine.waitForSettled(draft.id).catch(() => undefined);
    const restarted = h.newEngine({ faults: undefined, qc: qc.engine });
    await restarted.resumeAll();
    const settled = await restarted.waitForSettled(draft.id);
    expect(settled.state).toBe("awaiting_review");
    const events = await restarted.events(draft.id);
    expect(events.filter((e) => e.type === "qc_result")).toHaveLength(2);
    expectContiguousSeq(events);
  });

  test("日志最后一行只写了一半时，重新加载会丢弃它而不损坏日志", async () => {
    const h = await createHarness();
    const paused = await h.run({ sales: false });
    const path = join(h.workspaceDir, "tasks", paused.id, "events.jsonl");
    const good = (await h.engine.events(paused.id)).length;
    appendFileSync(path, '{"seq":99,"type":"step_sta');

    const restarted = h.newEngine();
    const events = await restarted.events(paused.id);
    expect(events).toHaveLength(good);
    // 后续追加不会接在半行后面
    await restarted.command(paused.id, { type: "terminate" }, "initiator");
    const lines = readFileSync(path, "utf8").trimEnd().split("\n");
    expect(() => lines.forEach((l) => JSON.parse(l))).not.toThrow();
    expect(lines).toHaveLength(good + 1);
    expectContiguousSeq(await restarted.events(paused.id));
  });

  test("写文件的工具重试：同一幂等键只产生一个文件", async () => {
    const h = await createHarness();
    // 渲染步骤先失败一次再成功：重试时幂等键不变
    h.tools.failTimes("render_report_docx", 1);
    const settled = await h.run();
    expect(settled.state).toBe("awaiting_review");
    const renders = h.tools.calls.filter((c) => c.tool === "render_report_docx");
    expect(renders).toHaveLength(2);
    expect(renders[0]!.args.idempotencyKey).toBe(renders[1]!.args.idempotencyKey);
  });
});

describe("faults.corruptTraceability", () => {
  test("草稿被注入裸数字，只注入一次；质检自动修正后下一版干净", async () => {
    const seen: unknown[] = [];
    const qc = {
      run(input: any) {
        seen.push(input.draft);
        const bad = JSON.stringify(input.draft).includes("+12.3%");
        return bad
          ? [{ id: "traceability", label: "数据溯源", passed: false, issues: ["裸数字"], fixStep: "write_report" }]
          : [{ id: "traceability", label: "数据溯源", passed: true, issues: [] }];
      },
    };
    const h = await createHarness({ qc, faults: { corruptTraceability: true } });
    const settled = await h.run();
    expect(settled.state).toBe("awaiting_review");
    expect(seen).toHaveLength(2);
    const events = await h.engine.events(settled.id);
    expect(events.filter((e) => e.type === "fault_injected" && e.mode === "corruptTraceability")).toHaveLength(1);
  });
});

describe("接缝 1 · 判断步骤重放时重复执行工具", () => {
  test("判断步骤中断后重新执行，同一个工具调用留下重复的 tool_called 事件，引擎照常完成，seq 连续", async () => {
    // 模拟：判断执行器在恢复后重新执行了白名单工具（模型响应按日志重放，工具调用没有被重放）
    let attempt = 0;
    const judgment = createStubJudgment({
      override: async (input) => {
        if (input.step.id !== "match_causes") return undefined;
        attempt += 1;
        await input.onToolCalled({ tool: "query_task", args: { id: "a" }, ok: true });
        if (attempt === 1) await input.onToolCalled({ tool: "query_task", args: { id: "a" }, ok: true });
        return { ok: true, output: { causes: [] }, modelCalls: 0 };
      },
    });
    const h = await createHarness({ judgment: judgment.runner });
    const settled = await h.run();
    expect(settled.state).toBe("awaiting_review");
    const events = await h.engine.events(settled.id);
    expect(events.filter((e) => e.type === "tool_called" && e.tool === "query_task")).toHaveLength(2);
    expectContiguousSeq(events);
    expect(settled.steps.find((s) => s.id === "match_causes")?.status).toBe("done");
  });
});