// 设置页“演示与调试”的接缝 1 测试：运行时可变故障开关、一次性崩溃标记。
import { afterEach, describe, expect, test } from "bun:test";
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { SimulatedCrash } from "../../src/engine";
import type { FaultHooks, QcEngine } from "../../src/contract";
import {
  cleanupAll,
  createHarness,
  createStubJudgment,
  expectContiguousSeq,
  failingChecks,
  passingChecks,
  startedSteps,
} from "./helpers/harness";

afterEach(cleanupAll);

function writeSettings(workspaceDir: string, body: Record<string, unknown>): string {
  const file = join(workspaceDir, "settings.json");
  writeFileSync(file, JSON.stringify(body, null, 2));
  return file;
}

const CONNECTIONS = [
  { name: "deepseek", baseUrl: "https://api.deepseek.com", apiKey: "sk-secret-1234", models: ["deepseek-chat"] },
];
const BASE_SETTINGS = { connections: CONNECTIONS, defaultConnection: "deepseek", defaultModel: "deepseek-chat" };

describe("运行时故障开关 · corruptTraceability", () => {
  test("运行中打开 → 对下一个写作步骤生效一次 → 数据溯源不通过 → 自动修正后通过", async () => {
    const faults: FaultHooks = {};
    const drafts: unknown[] = [];
    const qc: QcEngine = {
      run(input) {
        drafts.push(input.draft);
        const corrupted = JSON.stringify(input.draft).includes("+12.3%");
        return corrupted ? failingChecks("write_report", "裸数字 +12.3%") : passingChecks;
      },
    };
    // 模拟操作员在 demo 页打开开关：在 match_causes 开始执行时把运行时 FaultHooks 改掉
    const judgment = createStubJudgment({
      onRun: (input) => {
        if (input.step.id === "match_causes") faults.corruptTraceability = true;
      },
    });
    const h = await createHarness({ judgment: judgment.runner, qc, faults });
    const settingsFile = writeSettings(h.workspaceDir, { ...BASE_SETTINGS, demo: { corruptTraceability: true } });

    const settled = await h.run();
    expect(settled.state).toBe("awaiting_review");

    // 第一次质检看到被篡改的草稿，修正后的第二次是干净的
    expect(drafts).toHaveLength(2);
    expect(JSON.stringify(drafts[0])).toContain("+12.3%");
    expect(JSON.stringify(drafts[1])).not.toContain("+12.3%");

    const events = await h.engine.events(settled.id);
    expect(events.filter((e) => e.type === "fault_injected")).toEqual([
      expect.objectContaining({ target: "write_report", mode: "corruptTraceability" }),
    ]);
    expect(startedSteps(events).filter((s) => s === "write_report")).toHaveLength(2);

    // 一次性：内存里的开关已清除，settings.json 里的标记也清除了，其它设置不变
    expect(faults.corruptTraceability).toBe(false);
    const raw = JSON.parse(readFileSync(settingsFile, "utf8")) as Record<string, any>;
    expect(raw.demo.corruptTraceability).toBe(false);
    expect(raw.connections).toEqual(CONNECTIONS);
  });
});

describe("一次性崩溃标记 · crashOnNextStep", () => {
  test("下一步开始后写 fault_injected → 清除 settings.json 标记 → 抛 SimulatedCrash；新引擎 resumeAll 走完", async () => {
    const h = await createHarness();
    const settingsFile = writeSettings(h.workspaceDir, { ...BASE_SETTINGS, demo: { crashOnNextStep: true } });

    const draft = await h.create();
    const res = await h.cmd(draft.id, { type: "submit" }, "initiator");
    expect(res.ok).toBe(true);
    await expect(h.engine.waitForSettled(draft.id)).rejects.toBeInstanceOf(SimulatedCrash);

    const crashed = await h.engine.events(draft.id);
    const injected = crashed.filter((e) => e.type === "fault_injected");
    expect(injected).toHaveLength(1);
    expect(injected[0]).toMatchObject({ mode: "crash" });
    // “下一个步骤开始后”：先有 step_started 再写 fault_injected
    expect(crashed.at(-2)!.type).toBe("step_started");
    expect(crashed.at(-1)!.type).toBe("fault_injected");

    // 标记已清除：重启后不会再崩
    const raw = JSON.parse(readFileSync(settingsFile, "utf8")) as Record<string, any>;
    expect(raw.demo.crashOnNextStep).toBe(false);
    expect(raw.connections).toEqual(CONNECTIONS);

    const restarted = h.newEngine({ faults: undefined });
    await restarted.resumeAll();
    const settled = await restarted.waitForSettled(draft.id);
    expect(settled.state).toBe("awaiting_review");

    const events = await restarted.events(draft.id);
    expectContiguousSeq(events);
    expect(events.filter((e) => e.type === "fault_injected")).toHaveLength(1);
    // 崩溃时 admission 刚开始，没有完成；恢复后重跑一次，其余步骤只跑一次
    const started = startedSteps(events);
    expect(started.filter((s) => s === "admission")).toHaveLength(2);
    for (const id of ["load_data", "compute_metrics", "match_causes", "write_report"]) {
      expect(started.filter((s) => s === id)).toHaveLength(1);
    }
  });
});
