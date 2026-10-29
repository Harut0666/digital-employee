// T7 端到端（接缝 1）：createTaskEngine + MCP InMemoryTransport 连真实工具服务器 + 真实 employees/weekly-report 配置 + 剧本式假模型。
// 质检、槽位渲染、Word 生成都是真的；剧本（samples/cases/scripts）里的叙述只含槽位，质检靠它真实通过。
import { afterEach, describe, expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { SimulatedCrash, type ModelScript, type TaskSnapshot } from "@de/core";
import {
  SAMPLE,
  cleanupE2E,
  createE2E,
  loadScript,
  ofType,
  readArtifact,
  readDocxText,
  startedSteps,
  stateTrail,
  stepOutputPath,
} from "./helpers";

afterEach(cleanupE2E);

const QC_IDS = ["structure", "traceability", "anomaly_coverage", "quote_integrity", "contradiction_coverage", "forbidden_terms", "length"];

function qcFailures(snapshot: TaskSnapshot): string[] {
  return (snapshot.qc ?? []).filter((c) => !c.passed).map((c) => `${c.label}：${c.issues.join("；")}`);
}

describe("E2E · A-01 正常任务（含纪要与数据不一致）", () => {
  test("完整走到 delivered：docx 与检验记录存在，质检真实通过，数字来自事实", async () => {
    const e2e = await createE2E({ script: loadScript("A-01") });
    const review = await e2e.launch({ week: 44, attachments: [SAMPLE.sales, SAMPLE.minutes(44)] });

    expect(qcFailures(review)).toEqual([]);
    expect(review.state).toBe("awaiting_review");
    expect(review.pause).toMatchObject({ code: "awaiting_review", options: ["approve", "return"] });
    expect(review.qc?.map((c) => c.id)).toEqual(QC_IDS);
    // 首轮不解读退回意见；两次判断步骤共两次模型调用
    expect(review.steps.find((s) => s.id === "interpret_feedback")?.status).toBe("skipped");
    expect(review.budget.modelCalls.used).toBe(2);

    // 非法流转被拒绝并留痕：发起人试图点“通过”
    expect(await e2e.cmd(review.id, { type: "approve" }, "initiator")).toMatchObject({ ok: false, error: "forbidden_role" });
    expect(await e2e.cmd(review.id, { type: "submit" }, "initiator")).toMatchObject({ ok: false, error: "illegal_transition" });
    expect(ofType(await e2e.events(review.id), "transition_rejected").map((e) => e.trigger)).toEqual(["approve", "submit"]);

    const done = await e2e.cmd(review.id, { type: "approve" }, "reviewer");
    expect(done.ok && done.snapshot.state).toBe("delivered");
    const final = (await e2e.engine.get(review.id))!;
    const docx = final.deliverables.find((d) => d.kind === "docx");
    const record = final.deliverables.find((d) => d.kind === "qc_record");
    expect(docx && existsSync(docx.path)).toBe(true);
    expect(record && existsSync(record.path)).toBe(true);
    expect(readFileSync(docx!.path).subarray(0, 2).toString()).toBe("PK");
    expect(JSON.parse(readFileSync(record!.path, "utf8"))).toMatchObject({ passed: true, taskId: review.id });

    // Word 里的数字是槽位渲染出来的事实（判定要点：-9.3%、573,635 元、毛利率 61.1%、华南 -12.7%）
    const text = readDocxText(docx!.path);
    for (const expected of ["573,635 元", "-9.3%", "61.1%", "-12.7%", "-12.0%", "-38.3%", "以数据为准", "-260 元"]) {
      expect(text).toContain(expected);
    }
    expect(text).not.toContain("{{");
    for (const forbidden of ["客单价", "同比"]) expect(text).not.toContain(forbidden);

    const events = await e2e.events(review.id);
    expect(ofType(events, "tool_denied")).toHaveLength(0);
    expect(stateTrail(events)).toEqual(["draft", "admission", "running", "qc", "awaiting_review", "delivered"]);
    events.forEach((e, i) => expect(e.seq).toBe(i + 1));

    // 纪要矛盾由程序判定：事件流里有 detect_contradictions 步骤，判出且只判出华南“持平”一条
    expect(review.steps.find((s) => s.id === "detect_contradictions")?.status).toBe("done");
    const contradictionsCall = ofType(events, "tool_called").find((e) => e.tool === "detect_contradictions");
    expect(contradictionsCall?.stepId).toBe("detect_contradictions");
    const detected = readArtifact<{ contradictions: { id: string; rule: string; subject: { level: string; key: string; name: string } }[] }>(
      stepOutputPath(events, "detect_contradictions")!,
    );
    expect(detected.contradictions).toHaveLength(1);
    expect(detected.contradictions[0]).toMatchObject({ id: "contradiction:region:华南", rule: "flat", subject: { level: "region", key: "华南", name: "华南" } });
    // 检验记录里有“纪要矛盾覆盖”，且真实通过
    const recordJson = JSON.parse(readFileSync(record!.path, "utf8")) as { checks: { id: string; passed: boolean }[] };
    expect(recordJson.checks.map((c) => c.id)).toContain("contradiction_coverage");
    expect(recordJson.checks.find((c) => c.id === "contradiction_coverage")).toMatchObject({ passed: true });
  });

  test("渲染步骤收到的是已渲染槽位的草稿，并带任务幂等键", async () => {
    const e2e = await createE2E({ script: loadScript("A-01") });
    const review = await e2e.launch({ week: 44, attachments: [SAMPLE.sales, SAMPLE.minutes(44)] });
    const call = ofType(await e2e.events(review.id), "tool_called").find((e) => e.tool === "render_report_docx")!;
    const args = call.args as { report: { sections: { paragraphs: string[]; bullets?: string[] }[] }; idempotencyKey: string };
    expect(args.idempotencyKey).toStartWith(`${review.id}-render_document-r0-`);
    const all = args.report.sections.flatMap((s) => [...s.paragraphs, ...(s.bullets ?? [])]).join("\n");
    expect(all).not.toContain("{{");
    expect(all).toContain("以数据为准");
  });

  test("A-01 变体：审核退回 → 重做（只重跑判断步骤）→ 修改说明覆盖意见 → 通过", async () => {
    const e2e = await createE2E({ script: loadScript("A-01-rework") });
    const first = await e2e.launch({ week: 44, attachments: [SAMPLE.sales, SAMPLE.minutes(44)] });
    expect(first.state).toBe("awaiting_review");
    const metricsBefore = stepOutputPath(await e2e.events(first.id), "compute_metrics");

    const comment = "摘要请再精简，突出本周最重要的两个异常。";
    expect(await e2e.cmd(first.id, { type: "return", comment }, "initiator")).toMatchObject({ ok: false, error: "forbidden_role" });
    expect(await e2e.cmd(first.id, { type: "return", comment: " " }, "reviewer")).toMatchObject({ ok: false, error: "invalid_input" });
    const returned = await e2e.cmd(first.id, { type: "return", comment, sections: ["summary"] }, "reviewer");
    expect(returned.ok).toBe(true);
    const second = await e2e.engine.waitForSettled(first.id);

    expect(qcFailures(second)).toEqual([]);
    expect(second.state).toBe("awaiting_review");
    expect(second.reworkRound).toBe(1);
    expect(second.qc?.find((c) => c.id === "feedback_response")).toMatchObject({ passed: true });

    const events = await e2e.events(first.id);
    const afterReturn = events.slice(events.findIndex((e) => e.type === "review_decision"));
    // 数字类的计算步骤不重跑，结果沿用
    expect(startedSteps(afterReturn)).toEqual(["interpret_feedback", "match_causes", "write_report", "render_document", "qc"]);
    expect(stepOutputPath(events, "compute_metrics")).toBe(metricsBefore);

    // 修改说明逐条对应审核意见
    const draft = readArtifact<{ revisionNotes: { comment: string; sections: string[]; change: string }[] }>(stepOutputPath(events, "write_report")!);
    expect(draft.revisionNotes).toHaveLength(1);
    expect(draft.revisionNotes[0]).toMatchObject({ comment, sections: ["summary"] });

    const approved = await e2e.cmd(first.id, { type: "approve" }, "reviewer");
    expect(approved.ok && approved.snapshot.state).toBe("delivered");
    expect((await e2e.engine.get(first.id))!.deliverables.find((d) => d.kind === "qc_record")?.path).toContain("r1");
  });
});

describe("E2E · A-02、A-04 数据口径与数据质量说明", () => {
  test("A-02（第 43 周，重复记录）：去重后写入数据质量说明，质检通过", async () => {
    const e2e = await createE2E({ script: loadScript("A-02") });
    const review = await e2e.launch({ week: 43, attachments: [SAMPLE.sales, SAMPLE.minutes(43)] });
    expect(qcFailures(review)).toEqual([]);
    expect(review.state).toBe("awaiting_review");
    await e2e.cmd(review.id, { type: "approve" }, "reviewer");
    const final = (await e2e.engine.get(review.id))!;
    const text = readDocxText(final.deliverables.find((d) => d.kind === "docx")!.path);
    for (const expected of ["632,486 元", "+4.4%", "+19.0%", "+30.1%", "5,629 元", "上周存在数据缺失日"]) expect(text).toContain(expected);
  });

  test("A-04（第 42 周，数据缺失与退货）：退货笔数与金额、缺失日说明，质检通过", async () => {
    const e2e = await createE2E({ script: loadScript("A-04") });
    const review = await e2e.launch({ week: 42, attachments: [SAMPLE.sales, SAMPLE.minutes(42)] });
    expect(qcFailures(review)).toEqual([]);
    expect(review.state).toBe("awaiting_review");
    await e2e.cmd(review.id, { type: "approve" }, "reviewer");
    const final = (await e2e.engine.get(review.id))!;
    const text = readDocxText(final.deliverables.find((d) => d.kind === "docx")!.path);
    for (const expected of ["605,909 元", "-11.7%", "-15.8%", "15 笔", "-656 元", "手工台账"]) expect(text).toContain(expected);
  });
});

describe("E2E · A-03 缺材料后补传", () => {
  test("停在 missing_sales → supplement 补传 xlsx → 走完", async () => {
    const e2e = await createE2E({ script: loadScript("A-03") });
    const paused = await e2e.launch({ week: 44, attachments: [SAMPLE.minutes(44)] });

    expect(paused.state).toBe("awaiting_initiator");
    expect(paused.pause).toMatchObject({ code: "missing_sales" });
    expect(paused.pause?.message).toContain("销售明细");
    // 没有销售数据，就不会算任何数字，也不会调用模型
    expect(startedSteps(await e2e.events(paused.id))).toEqual(["admission"]);
    expect(paused.budget.modelCalls.used).toBe(0);
    expect(paused.deliverables).toEqual([]);

    const res = await e2e.cmd(paused.id, { type: "supplement", attachments: [SAMPLE.sales] }, "initiator");
    expect(res.ok).toBe(true);
    const review = await e2e.engine.waitForSettled(paused.id);
    expect(qcFailures(review)).toEqual([]);
    expect(review.state).toBe("awaiting_review");
    expect(review.attachments.map((a) => a.name).sort()).toEqual(["周会纪要_2026W44.docx", "销售明细.xlsx"].sort());

    const approved = await e2e.cmd(review.id, { type: "approve" }, "reviewer");
    expect(approved.ok && approved.snapshot.state).toBe("delivered");
    const events = await e2e.events(paused.id);
    expect(stateTrail(events)).toEqual(["draft", "admission", "awaiting_initiator", "admission", "running", "qc", "awaiting_review", "delivered"]);
  });
});

describe("E2E · A-05 超出数据范围", () => {
  test("停在 period_out_of_range，没有生成任何周报数字", async () => {
    const e2e = await createE2E({ script: {} });
    const paused = await e2e.launch({ week: 46, attachments: [SAMPLE.sales] });

    expect(paused.state).toBe("awaiting_initiator");
    expect(paused.pause?.code).toBe("period_out_of_range");
    expect(paused.pause?.message).toContain("2026-11-01");
    expect(paused.pause?.options).toEqual(["supplement", "terminate"]);

    const events = await e2e.events(paused.id);
    expect(startedSteps(events)).toEqual(["admission"]);
    expect(ofType(events, "model_called")).toHaveLength(0);
    expect(ofType(events, "tool_called").map((e) => e.tool)).toEqual(["validate_submission"]);
    expect(paused.deliverables).toEqual([]);
    expect(paused.steps.filter((s) => s.status === "done").map((s) => s.id)).toEqual(["admission"]);
  });
});

describe("E2E · 工具故障注入", () => {
  test("工具连续失败 → 停在 tool_failed → 关闭故障后 retry → 从失败的步骤继续走完", async () => {
    const e2e = await createE2E({ script: loadScript("A-01"), toolFaults: { tools: { compute_metrics: "error" } } });
    const paused = await e2e.launch({ week: 44, attachments: [SAMPLE.sales, SAMPLE.minutes(44)] });

    expect(paused.state).toBe("awaiting_operator");
    expect(paused.pause).toMatchObject({ code: "tool_failed", options: ["retry", "terminate"] });
    expect(paused.pause?.message).toContain("compute_metrics");
    expect(paused.pause?.message).toContain("已完成 2/10 步");
    const events = await e2e.events(paused.id);
    expect(ofType(events, "tool_called").filter((e) => e.tool === "compute_metrics").map((e) => e.attempt)).toEqual([1, 2, 3]);
    expect(ofType(events, "tool_result").filter((e) => !e.ok)).toHaveLength(3);

    e2e.tools.setFaults({ tools: {} });
    expect(await e2e.cmd(paused.id, { type: "retry" }, "initiator")).toMatchObject({ ok: false, error: "forbidden_role" });
    expect((await e2e.cmd(paused.id, { type: "retry" }, "operator")).ok).toBe(true);
    const review = await e2e.engine.waitForSettled(paused.id);
    expect(qcFailures(review)).toEqual([]);
    expect(review.state).toBe("awaiting_review");

    const after = await e2e.events(paused.id);
    expect(startedSteps(after).filter((s) => s === "admission")).toHaveLength(1);
    expect(startedSteps(after).filter((s) => s === "load_data")).toHaveLength(1);
    expect(startedSteps(after).filter((s) => s === "compute_metrics")).toHaveLength(2);
  }, 20_000);

  test("准入工具本身故障 → 待人工处理（不是缺材料）→ retry 回到准入", async () => {
    const e2e = await createE2E({ script: loadScript("A-01"), toolFaults: { tools: { validate_submission: "error" } } });
    const paused = await e2e.launch({ week: 44, attachments: [SAMPLE.sales, SAMPLE.minutes(44)] });
    expect(paused.state).toBe("awaiting_operator");
    expect(paused.pause?.code).toBe("tool_failed");

    e2e.tools.setFaults({ tools: {} });
    await e2e.cmd(paused.id, { type: "retry" }, "operator");
    const review = await e2e.engine.waitForSettled(paused.id);
    expect(review.state).toBe("awaiting_review");
    expect(stateTrail(await e2e.events(paused.id)).slice(0, 5)).toEqual(["draft", "admission", "awaiting_operator", "admission", "running"]);
  }, 20_000);

  test("工具返回不合规的数据（invalid_output）同样走重试与暂停，不崩溃、不编造", async () => {
    const e2e = await createE2E({ script: loadScript("A-01"), toolFaults: { tools: { load_sales: "invalid_output" } } });
    const paused = await e2e.launch({ week: 44, attachments: [SAMPLE.sales, SAMPLE.minutes(44)] });
    expect(paused.state).toBe("awaiting_operator");
    expect(paused.pause?.code).toBe("tool_failed");
    expect(paused.deliverables).toEqual([]);
  }, 20_000);
});

describe("E2E · 断点恢复", () => {
  test("在 write_report 崩溃 → 重启 resumeAll → 已完成步骤不重跑，模型响应按日志重放，结果一致", async () => {
    // 对照：不崩溃的完整运行
    const clean = await createE2E({ script: loadScript("A-01") });
    const cleanReview = await clean.launch({ week: 44, attachments: [SAMPLE.sales, SAMPLE.minutes(44)] });
    const cleanEvents = await clean.events(cleanReview.id);

    const e2e = await createE2E({ script: loadScript("A-01"), faults: { crashAtStep: "write_report" } });
    await expect(e2e.launch({ week: 44, attachments: [SAMPLE.sales, SAMPLE.minutes(44)] })).rejects.toBeInstanceOf(SimulatedCrash);
    const [summary] = await e2e.engine.list();
    const taskId = summary!.id;
    const crashed = await e2e.events(taskId);
    expect(ofType(crashed, "fault_injected")).toEqual([expect.objectContaining({ target: "write_report", mode: "crash" })]);
    expect(ofType(crashed, "model_called").filter((e) => e.stepId === "write_report")).toHaveLength(1);

    // 重启：新引擎实例，空剧本——任何真实的模型调用都会失败，所以能走完就证明是重放
    const engine = e2e.restart({ script: {} });
    await engine.resumeAll();
    const review = await engine.waitForSettled(taskId);
    expect(qcFailures(review)).toEqual([]);
    expect(review.state).toBe("awaiting_review");

    const events = await e2e.events(taskId);
    events.forEach((ev, i) => expect(ev.seq).toBe(i + 1));
    const started = startedSteps(events);
    for (const id of ["admission", "load_data", "compute_metrics", "parse_minutes", "match_causes", "detect_contradictions", "render_document", "qc"]) {
      expect(started.filter((s) => s === id)).toHaveLength(1);
    }
    expect(started.filter((s) => s === "write_report")).toHaveLength(2);
    // 重放的响应不重复记录
    expect(ofType(events, "model_called").filter((e) => e.stepId === "write_report")).toHaveLength(1);
    expect(ofType(events, "model_called")).toHaveLength(2);

    // 结果一致：关键步骤的输出哈希与不崩溃的运行相同（含文件路径的步骤，如 parse_minutes，两次运行的工作区不同，不比较）
    const sha = (evs: typeof events, id: string) => ofType(evs, "step_completed").find((e) => e.stepId === id)?.outputSha256;
    for (const id of ["compute_metrics", "match_causes", "write_report"]) {
      expect(sha(events, id)).toBeDefined();
      expect(sha(events, id)).toBe(sha(cleanEvents, id));
    }
  }, 30_000);
});

describe("E2E · 越权拦截", () => {
  test("判断步骤调用 web_search → 被拒绝并留下 tool_denied 事件，任务不受影响", async () => {
    const script = loadScript("A-01");
    const submit = script.match_causes![0]!;
    const withSearch: ModelScript = {
      ...script,
      match_causes: [{ toolCalls: [{ name: "web_search", arguments: { query: "杭州湖滨店 停业 原因" } }] }, submit],
    };
    const e2e = await createE2E({ script: withSearch });
    const review = await e2e.launch({ week: 44, attachments: [SAMPLE.sales, SAMPLE.minutes(44)] });

    expect(review.state).toBe("awaiting_review");
    const events = await e2e.events(review.id);
    const denied = ofType(events, "tool_denied");
    expect(denied).toHaveLength(1);
    expect(denied[0]).toMatchObject({ stepId: "match_causes", tool: "web_search" });
    expect(denied[0]!.reason).toContain("web_search");
    // 工具根本没有被执行
    expect(ofType(events, "tool_called").some((e) => e.tool === "web_search")).toBe(false);
    expect(ofType(events, "model_called").filter((e) => e.stepId === "match_causes")).toHaveLength(2);
  });
});

describe("E2E · 质检自动修正", () => {
  test("corruptTraceability → 数据溯源不通过 → 自动修正 → 通过", async () => {
    const script = loadScript("A-01");
    const twice: ModelScript = { ...script, write_report: [script.write_report![0]!, script.write_report![0]!] };
    const e2e = await createE2E({ script: twice, faults: { corruptTraceability: true } });
    const review = await e2e.launch({ week: 44, attachments: [SAMPLE.sales, SAMPLE.minutes(44)] });

    expect(qcFailures(review)).toEqual([]);
    expect(review.state).toBe("awaiting_review");
    const events = await e2e.events(review.id);
    expect(ofType(events, "fault_injected")).toEqual([expect.objectContaining({ target: "write_report", mode: "corruptTraceability" })]);
    const results = ofType(events, "qc_result");
    expect(results.map((r) => r.passed)).toEqual([false, true]);
    const trace = results[0]!.checks.find((c) => c.id === "traceability")!;
    expect(trace.passed).toBe(false);
    expect(trace.issues.join("\n")).toContain("+12.3%");
    expect(trace.fixStep).toBe("write_report");
    expect(stateTrail(events)).toEqual(["draft", "admission", "running", "qc", "running", "qc", "awaiting_review"]);

    // 只重跑 write_report 及其后的步骤
    const started = startedSteps(events);
    expect(started.filter((s) => s === "write_report")).toHaveLength(2);
    expect(started.filter((s) => s === "match_causes")).toHaveLength(1);
    expect(started.filter((s) => s === "render_document")).toHaveLength(2);
  });

  test("质检自动修正用尽 → 转给操作员（qc_exhausted）", async () => {
    // 剧本里的草稿始终带裸数字，自动修正两次仍不通过
    const script = loadScript("A-01");
    const bad = JSON.parse(JSON.stringify(script.write_report![0]!)) as { toolCalls: { arguments: { title: string } }[] };
    bad.toolCalls[0]!.arguments.title += " 环比下降 9.3%";
    const e2e = await createE2E({ script: { ...script, write_report: [bad as never, bad as never, bad as never] } });
    const paused = await e2e.launch({ week: 44, attachments: [SAMPLE.sales, SAMPLE.minutes(44)] });
    expect(paused.state).toBe("awaiting_operator");
    expect(paused.pause?.code).toBe("qc_exhausted");
    expect(paused.pause?.message).toContain("9.3%");
    expect(ofType(await e2e.events(paused.id), "qc_result")).toHaveLength(3);
  });
});
