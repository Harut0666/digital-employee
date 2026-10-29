// SSE：先补发 after 之后的历史事件，再推送实时事件。
// 先订阅、后读历史，订阅期间到达的事件先缓冲，补发完再按 seq 去重冲刷，保证不丢不重。
import type { TaskEngine, TaskEvent } from "@de/core/contract";

const HEARTBEAT_MS = 15_000;

export function encodeEvent(event: TaskEvent): string {
  return `id: ${event.seq}\nevent: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`;
}

export function eventStream(engine: TaskEngine, taskId: string, after: number, signal?: AbortSignal): Response {
  const encoder = new TextEncoder();
  let unsubscribe: (() => void) | undefined;
  let heartbeat: ReturnType<typeof setInterval> | undefined;
  let closed = false;

  function close() {
    if (closed) return;
    closed = true;
    unsubscribe?.();
    if (heartbeat) clearInterval(heartbeat);
  }

  const stream = new ReadableStream<Uint8Array>({
    async start(controller) {
      let lastSeq = after;
      let replaying = true;
      const buffered: TaskEvent[] = [];

      const push = (event: TaskEvent) => {
        if (closed || event.seq <= lastSeq) return;
        lastSeq = event.seq;
        controller.enqueue(encoder.encode(encodeEvent(event)));
      };

      unsubscribe = engine.subscribe(taskId, (event) => {
        if (replaying) buffered.push(event);
        else push(event);
      });
      signal?.addEventListener("abort", () => {
        close();
        try {
          controller.close();
        } catch {
          /* 已关闭 */
        }
      });

      try {
        for (const event of await engine.events(taskId, after)) push(event);
        replaying = false;
        for (const event of buffered.sort((a, b) => a.seq - b.seq)) push(event);
        buffered.length = 0;
      } catch (err) {
        close();
        controller.error(err);
        return;
      }
      if (closed) return; // 回放期间客户端已断开：close() 已经跑过，不能再启动心跳
      heartbeat = setInterval(() => {
        if (!closed) controller.enqueue(encoder.encode(": keep-alive\n\n"));
      }, HEARTBEAT_MS);
    },
    cancel() {
      close();
    },
  });

  return new Response(stream, {
    headers: {
      "content-type": "text/event-stream; charset=utf-8",
      "cache-control": "no-cache, no-transform",
      connection: "keep-alive",
      "x-accel-buffering": "no",
    },
  });
}
