// 一条损坏的 events.jsonl 不能拖垮整个工作区：list() 仍然列出其他任务，
// 损坏任务按可解析的前缀给出最后已知状态并带上错误说明；服务启动时的 resumeAll() 不能被它中断。
import { afterEach, describe, expect, test } from "bun:test";
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { SimulatedCrash } from "../../src/engine";
import { cleanupAll, createHarness, createStubJudgment } from "./helpers/harness";

afterEach(cleanupAll);

/** 在日志中间插入一条无法解析的行（后面还有有效事件）：这是日志损坏，不是写入中断。 */
function corruptMiddle(workspaceDir: string, taskId: string): void {
  const logPath = join(workspaceDir, "tasks", taskId, "events.jsonl");
  const lines = readFileSync(logPath, "utf8").split("\n").filter(Boolean);
  const later = JSON.stringify({
    seq: lines.length + 1,
    ts: new Date().toISOString(),
    taskId,
    type: "step_started",
    stepId: "later",
    attempt: 1,
    reworkRound: 0,
  });
  writeFileSync(logPath, [...lines, "{broken", later].join("\n") + "\n");
}

describe("事件日志损坏时的任务隔离", () => {
  test("list()：一个任务日志损坏不影响其他任务，坏任务带出错误说明", async () => {
    const h = await createHarness();
    const healthy = await h.run();
    const broken = await h.create(); // 只有 task_created：用可解析的前缀就能算出摘要
    corruptMiddle(h.workspaceDir, broken.id);

    const fresh = h.newEngine(); // 模拟重启：内存里没有缓存
    const list = await fresh.list();
    expect(list.map((t) => t.id).sort()).toEqual([healthy.id, broken.id].sort());

    const bad = list.find((t) => t.id === broken.id)!;
    expect(bad.error).toMatch(/损坏/);
    expect(bad.state).toBe("draft"); // 最后已知状态（前缀事件推出的）
    expect(bad.title).toContain("2026 年第 44 周");
    expect(list.find((t) => t.id === healthy.id)!.error).toBeUndefined();
  });

  test("resumeAll()：一个任务日志损坏不挡住启动，其他活跃任务照常恢复", async () => {
    const judgment = createStubJudgment();
    const h = await createHarness({ judgment: judgment.runner, faults: { crashAtStep: "write_report" } });
    const broken = await h.create();
    corruptMiddle(h.workspaceDir, broken.id);
    const crashed = await h.create();
    await h.cmd(crashed.id, { type: "submit" }, "initiator");
    await expect(h.engine.waitForSettled(crashed.id)).rejects.toBeInstanceOf(SimulatedCrash);

    const restarted = h.newEngine({ faults: undefined, judgment: judgment.runner });
    await expect(restarted.resumeAll()).resolves.toBeUndefined();
    expect((await restarted.waitForSettled(crashed.id)).state).toBe("awaiting_review");
    expect((await restarted.list()).find((t) => t.id === broken.id)!.error).toMatch(/损坏/);
  });

  test("get()：直接访问损坏任务时给出明确的损坏错误", async () => {
    const h = await createHarness();
    const broken = await h.create();
    corruptMiddle(h.workspaceDir, broken.id);
    await expect(h.newEngine().get(broken.id)).rejects.toThrow(/损坏/);
  });
});
