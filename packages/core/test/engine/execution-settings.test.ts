// 接缝 B · 引擎把设置里的“执行与预算”默认值交给判断步骤：
// settings.execution 的模型重试 / 步骤超时对之后开始的判断步骤生效，
// 设置文件缺失或损坏时退回内置值（128k/8192/off 那套行为不变）。
import { afterEach, describe, expect, test } from "bun:test";
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { createScriptedModelProvider } from "../../src/agent";
import type { ScriptedTurn } from "../../src/agent";
import { DEFAULT_EXECUTION_SETTINGS } from "../../src/contract";
import { cleanupAll, createHarness } from "./helpers/harness";

afterEach(cleanupAll);

const submit = (arguments_: Record<string, unknown>): ScriptedTurn => ({ toolCalls: [{ name: "submit_result", arguments: arguments_ }] });

function script() {
  return createScriptedModelProvider({
    match_causes: [submit({ causes: [] })],
    write_report: [submit({ title: "报告" })],
  });
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
