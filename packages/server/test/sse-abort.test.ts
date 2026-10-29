import { afterEach, describe, expect, test } from "bun:test";
import type { TaskEngine, TaskEvent } from "@de/core/contract";
import { eventStream } from "../src/sse";

const realSetInterval = globalThis.setInterval;
const realClearInterval = globalThis.clearInterval;
afterEach(() => {
  globalThis.setInterval = realSetInterval;
  globalThis.clearInterval = realClearInterval;
});

describe("eventStream：回放期间客户端断开", () => {
  test("断开后回放才结束，不会留下未清理的心跳定时器", async () => {
    const live = new Set<unknown>();
    globalThis.setInterval = ((fn: () => void, ms?: number) => {
      const t = realSetInterval(fn, ms);
      live.add(t);
      return t;
    }) as typeof setInterval;
    globalThis.clearInterval = ((t: unknown) => {
      live.delete(t);
      realClearInterval(t as ReturnType<typeof setInterval>);
    }) as typeof clearInterval;

    let release!: (events: TaskEvent[]) => void;
    const history = new Promise<TaskEvent[]>((r) => (release = r));
    const engine = { subscribe: () => () => {}, events: () => history } as unknown as TaskEngine;

    const ctrl = new AbortController();
    eventStream(engine, "t1", 0, ctrl.signal);
    await new Promise((r) => setTimeout(r, 10));
    ctrl.abort(); // 客户端在回放过程中断开
    release([]);
    await new Promise((r) => setTimeout(r, 20));

    expect(live.size).toBe(0);
    for (const t of live) realClearInterval(t as ReturnType<typeof setInterval>);
  });
});
