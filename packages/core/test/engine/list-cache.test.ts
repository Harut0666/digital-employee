// list() 只产出摘要：不能因为列一次任务就把每个任务的完整 Runtime（全部事件 + 配置）永久缓存下来。
// 活跃任务的 Runtime 仍保留（恢复、推演与订阅都依赖它）。
import { afterEach, describe, expect, test } from "bun:test";
import { readFileSync, rmSync } from "node:fs";
import { join } from "node:path";
import type { JudgmentRunner, TaskEvent } from "../../src/contract";
import { appendEventLine } from "../../src/engine/event-log";
import { cleanupAll, createHarness } from "./helpers/harness";

afterEach(cleanupAll);

/** 直到任务进入指定状态（驱动是异步的）。 */
async function waitForState(engine: { get(id: string): Promise<{ state: string } | undefined> }, id: string, state: string) {
  for (let i = 0; i < 200; i++) {
    const snapshot = await engine.get(id);
    if (snapshot?.state === state) return snapshot;
    await new Promise((r) => setTimeout(r, 5));
  }
  return undefined;
}

/** 像另一个进程（CLI 或另一个服务实例）那样，给任务日志追加一条合法事件：周期改成指定周。 */
function appendWeek(workspaceDir: string, taskId: string, week: number): void {
  const logPath = join(workspaceDir, "tasks", taskId, "events.jsonl");
  const seq = readFileSync(logPath, "utf8").split("\n").filter(Boolean).length;
  const event: TaskEvent = {
    seq: seq + 1,
    ts: new Date().toISOString(),
    taskId,
    type: "form_updated",
    form: { period: { year: 2026, week } },
    attachments: [],
    actor: "system",
  };
  appendEventLine(logPath, event);
}

describe("list()：空闲任务不缓存完整 Runtime", () => {
  test("外部追加事件后，再次 list 看到磁盘上的最新状态，而不是第一次 list 的缓存", async () => {
    const h = await createHarness();
    const snap = await h.run(); // 停在 awaiting_review：空闲任务
    expect(snap.state).toBe("awaiting_review");

    const fresh = h.newEngine(); // 模拟进程重启：内存里没有任何 Runtime
    const first = await fresh.list();
    expect(first.map((t) => t.id)).toEqual([snap.id]);
    expect(first[0]!.title).toContain("第 44 周");

    // 另一个进程（CLI 或另一个服务实例）给同一任务追加了一条合法事件：周期改成第 45 周
    appendWeek(h.workspaceDir, snap.id, 45);

    const second = await fresh.list();
    expect(second[0]!.title).toContain("第 45 周");
  });

  test("resumeAll（server 启动时的扫描）同样不缓存空闲任务", async () => {
    const h = await createHarness();
    const snap = await h.run(); // 停在 awaiting_review：空闲任务

    const fresh = h.newEngine();
    await fresh.resumeAll();

    appendWeek(h.workspaceDir, snap.id, 45);

    expect((await fresh.list())[0]!.title).toContain("第 45 周");
  });

  test("活跃任务的 Runtime 在 list 之后仍然保留", async () => {
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    const judgment: JudgmentRunner = {
      async run() {
        await gate;
        return { ok: true, output: {}, modelCalls: 0 };
      },
    };
    const h = await createHarness({ judgment });
    const draft = await h.create();
    await h.engine.command(draft.id, { type: "submit" }, "initiator");
    expect((await waitForState(h.engine, draft.id, "running"))?.state).toBe("running");

    const fresh = h.newEngine({ judgment });
    const listed = await fresh.list();
    expect(listed.find((t) => t.id === draft.id)!.state).toBe("running");

    // 活跃任务的 Runtime 被保留：即使日志被移走，内存里的快照仍可返回
    rmSync(join(h.workspaceDir, "tasks", draft.id, "events.jsonl"));
    expect(await fresh.get(draft.id)).toBeDefined();

    release();
    await new Promise((r) => setTimeout(r, 30));
  });
});
