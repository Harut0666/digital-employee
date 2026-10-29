import { describe, expect, test } from "bun:test";
import { jsonPost, setup } from "./helpers";

interface SseMessage {
  id?: string;
  event?: string;
  data?: string;
}

/** 从响应体里按需读取 SSE 消息；done 时取消流。 */
function sseReader(res: Response) {
  const reader = res.body!.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  async function next(timeoutMs = 2000): Promise<SseMessage> {
    for (;;) {
      const sep = buf.indexOf("\n\n");
      if (sep >= 0) {
        const block = buf.slice(0, sep);
        buf = buf.slice(sep + 2);
        const msg: SseMessage = {};
        let any = false;
        for (const line of block.split("\n")) {
          if (line.startsWith(":")) continue;
          const i = line.indexOf(":");
          if (i < 0) continue;
          (msg as Record<string, string>)[line.slice(0, i)] = line.slice(i + 1).trim();
          any = true;
        }
        if (any) return msg;
        continue;
      }
      const chunk = await Promise.race([
        reader.read(),
        new Promise<never>((_, rej) => setTimeout(() => rej(new Error("SSE read timeout")), timeoutMs)),
      ]);
      if (chunk.done) throw new Error("SSE closed");
      buf += decoder.decode(chunk.value, { stream: true });
    }
  }
  return { next, cancel: () => reader.cancel() };
}

async function newTask(call: ReturnType<typeof setup>["call"]) {
  const fd = new FormData();
  fd.set("employee", "weekly-report");
  fd.set("form", "{}");
  return ((await (await call("/api/tasks", { method: "POST", headers: { "x-role": "initiator" }, body: fd })).json()) as { id: string }).id;
}

describe("GET /api/tasks/:id/stream", () => {
  test("replays events after ?after= first, then pushes live events in order without gaps or duplicates", async () => {
    const { call, engine } = setup();
    const id = await newTask(call); // seq 1
    await call(...jsonPost(`/api/tasks/${id}/commands`, { type: "submit" }, "initiator")); // seq 2..5

    const res = await call(`/api/tasks/${id}/stream?after=3`);
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toContain("text/event-stream");
    const sse = sseReader(res);

    const first = await sse.next();
    const second = await sse.next();
    expect([first.id, second.id]).toEqual(["4", "5"]);
    expect(first.event).toBe("state_changed");
    expect(JSON.parse(first.data!)).toMatchObject({ seq: 4, type: "state_changed", taskId: id });

    // 实时事件
    const live = engine.emit(id, { type: "fault_injected", target: "load_sales", mode: "timeout" });
    const msg = await sse.next();
    expect(msg.id).toBe(String(live.seq));
    expect(msg.event).toBe("fault_injected");
    await sse.cancel();
  });

  test("without ?after= it replays the whole log", async () => {
    const { call } = setup();
    const id = await newTask(call);
    const sse = sseReader(await call(`/api/tasks/${id}/stream`));
    expect((await sse.next()).id).toBe("1");
    await sse.cancel();
  });

  test("Last-Event-ID header resumes after that event", async () => {
    const { call } = setup();
    const id = await newTask(call);
    await call(...jsonPost(`/api/tasks/${id}/commands`, { type: "submit" }, "initiator"));
    const sse = sseReader(await call(`/api/tasks/${id}/stream`, { headers: { "last-event-id": "4" } }));
    expect((await sse.next()).id).toBe("5");
    await sse.cancel();
  });

  test("cancelling the stream unsubscribes from the engine", async () => {
    const { call, engine } = setup();
    const id = await newTask(call);
    let active = 0;
    const origSub = engine.subscribe.bind(engine);
    engine.subscribe = (taskId, listener) => {
      active++;
      const off = origSub(taskId, listener);
      return () => {
        active--;
        off();
      };
    };
    const sse = sseReader(await call(`/api/tasks/${id}/stream`));
    await sse.next();
    expect(active).toBe(1);
    await sse.cancel();
    await Bun.sleep(20);
    expect(active).toBe(0);
  });

  test("unknown task is 404 and bad after is 400", async () => {
    const { call } = setup();
    expect((await call("/api/tasks/nope/stream")).status).toBe(404);
    const id = await newTask(call);
    expect((await call(`/api/tasks/${id}/stream?after=-1`)).status).toBe(400);
  });
});
