// 接缝 B · 引擎把设置里的“执行与预算”默认值交给判断步骤：
// settings.execution 的模型重试 / 步骤超时对之后开始的判断步骤生效，
// 设置文件缺失或损坏时退回内置值（128k/8192/off 那套行为不变）。
import { afterEach, describe, expect, test } from "bun:test";
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { createScriptedModelProvider } from "../../src/agent";
import type { ScriptedTurn } from "../../src/agent";
import { DEFAULT_EXECUTION_SETTINGS } from "../../src/contract";
import { cleanupAll, createHarness, createStubQc, failingChecks } from "./helpers/harness";

afterEach(cleanupAll);

const submit = (arguments_: Record<string, unknown>): ScriptedTurn => ({ toolCalls: [{ name: "submit_result", arguments: arguments_ }] });

function script() {
  return createScriptedModelProvider({
    match_causes: [submit({ causes: [] })],
    write_report: [submit({ title: "报告" })],
  });
}

/** match_causes 慢 delayMs 才提交；write_report 立即提交。 */
function slowScript(delayMs: number) {
  return createScriptedModelProvider({
    match_causes: [{ delayMs, then: submit({ causes: [] }) }],
    write_report: [submit({ title: "报告" })],
  });
}

/** 判断步骤都没有写 durationSec（契约允许省略，交给设置默认值）。 */
function removeJudgeDurations(workflow: Record<string, any>): void {
  for (const step of workflow.steps as Record<string, any>[]) {
    if (step.kind === "judge") delete step.limits.durationSec;
  }
}

function writeSettings(workspaceDir: string, execution: Record<string, unknown>): void {
  writeFileSync(join(workspaceDir, "settings.json"), JSON.stringify({ version: 2, revision: 0, execution }, null, 2));
}

describe("引擎读取 settings.execution", () => {
  test("模型重试次数来自设置默认值（内置值是 2）", async () => {
    const model = script();
    const h = await createHarness({ useBuiltInJudgment: true, model });
    writeSettings(h.workspaceDir, { ...DEFAULT_EXECUTION_SETTINGS, modelMaxRetries: 7 });

    await h.run();

    expect(model.requests.length).toBeGreaterThan(0);
    for (const request of model.requests) expect(request.options.maxRetries).toBe(7);
  });

  test("没有设置文件时退回内置值 2", async () => {
    const model = script();
    const h = await createHarness({ useBuiltInJudgment: true, model });

    await h.run();

    expect(model.requests[0]!.options.maxRetries).toBe(2);
  });

  test("设置文件损坏时任务照常跑，并用内置值", async () => {
    const model = script();
    const h = await createHarness({ useBuiltInJudgment: true, model });
    writeFileSync(join(h.workspaceDir, "settings.json"), "{ 这不是 JSON");

    const settled = await h.run();

    expect(settled.state).toBe("awaiting_review");
    expect(model.requests[0]!.options.maxRetries).toBe(2);
  });
});

describe("步骤超时：步骤配置 → 设置默认 → 内置值（引擎接缝）", () => {
  test("步骤没写 durationSec 时用 settings.execution.stepTimeoutMs", async () => {
    // 设置里的步骤超时最小 1000ms（契约范围），模型故意慢 1.5 秒
    const model = slowScript(1_500);
    const h = await createHarness({ useBuiltInJudgment: true, model, patchWorkflow: removeJudgeDurations });
    writeSettings(h.workspaceDir, { ...DEFAULT_EXECUTION_SETTINGS, stepTimeoutMs: 1_000 });

    const settled = await h.run();

    expect(settled.state).toBe("awaiting_operator");
    expect(settled.pause).toMatchObject({ code: "limit_exceeded" });
    expect(settled.pause?.message).toContain("1 秒");
  });

  test("步骤写了 durationSec 时以步骤值为准（设置里的 1 秒不生效）", async () => {
    const model = slowScript(200);
    const h = await createHarness({ useBuiltInJudgment: true, model });
    // 迷你员工给每个判断步骤写了 90 秒
    writeSettings(h.workspaceDir, { ...DEFAULT_EXECUTION_SETTINGS, stepTimeoutMs: 1_000 });

    const settled = await h.run();

    expect(settled.state).toBe("awaiting_review");
  });

  test("步骤没写、设置也读不出来时用内置 120 秒", async () => {
    const model = slowScript(200);
    const h = await createHarness({ useBuiltInJudgment: true, model, patchWorkflow: removeJudgeDurations });
    writeFileSync(join(h.workspaceDir, "settings.json"), "{ 坏文件");

    const settled = await h.run();

    expect(settled.state).toBe("awaiting_review");
  });
});

describe("质检自动修正上限：员工配置 → 设置默认 → 内置值（引擎接缝）", () => {
  function removeQcAutofix(workflow: Record<string, any>): void {
    delete workflow.limits.qcAutofix;
  }

  function alwaysFailingQc() {
    return createStubQc(Array.from({ length: 10 }, () => failingChecks("write_report", "不过")));
  }

  test("员工配置没写 qcAutofix 时用 settings.execution.qcAutofix", async () => {
    const qc = alwaysFailingQc();
    const h = await createHarness({ qc: qc.engine, patchWorkflow: removeQcAutofix });
    writeSettings(h.workspaceDir, { ...DEFAULT_EXECUTION_SETTINGS, qcAutofix: 1 });

    const settled = await h.run();

    expect(settled.state).toBe("awaiting_operator");
    // 1 次质检 + 1 次自动修正 = 2 条 qc_result
    expect((await h.engine.events(settled.id)).filter((e) => e.type === "qc_result")).toHaveLength(2);
  });

  test("员工配置没写、也没有设置文件时用内置 3 次", async () => {
    const qc = alwaysFailingQc();
    const h = await createHarness({ qc: qc.engine, patchWorkflow: removeQcAutofix });

    const settled = await h.run();

    expect(settled.state).toBe("awaiting_operator");
    // 1 次质检 + 3 次自动修正 = 4 条 qc_result
    expect((await h.engine.events(settled.id)).filter((e) => e.type === "qc_result")).toHaveLength(4);
  });
});

describe("工具重试退避：员工配置（非空）→ 设置默认", () => {
  function emptyToolBackoff(workflow: Record<string, any>): void {
    workflow.limits.toolRetry.backoffMs = [];
  }

  test("员工配置的 backoffMs 为空数组时用 settings.execution.retryBackoffMs", async () => {
    const h = await createHarness({ patchWorkflow: emptyToolBackoff });
    writeSettings(h.workspaceDir, { ...DEFAULT_EXECUTION_SETTINGS, retryBackoffMs: [250] });
    h.tools.failTimes("load_sales", 1);

    const started = Date.now();
    const settled = await h.run();

    expect(settled.state).toBe("awaiting_review");
    // 失败一次后用设置里的 250ms 退避再重试
    expect(Date.now() - started).toBeGreaterThanOrEqual(200);
  });

  test("员工配置写了非空 backoffMs 时以员工配置为准（[0] 不等待）", async () => {
    const h = await createHarness({
      patchWorkflow: (workflow) => {
        workflow.limits.toolRetry.backoffMs = [0];
      },
    });
    writeSettings(h.workspaceDir, { ...DEFAULT_EXECUTION_SETTINGS, retryBackoffMs: [2_000] });
    h.tools.failTimes("load_sales", 1);

    const started = Date.now();
    const settled = await h.run();
    const elapsed = Date.now() - started;

    expect(settled.state).toBe("awaiting_review");
    expect(elapsed).toBeLessThan(1_000);
  });
});
