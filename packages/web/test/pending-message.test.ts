// 乐观的“待确认用户消息”：发出去的话立刻显示，正式消息进了会话日志就让位；失败则移除。
import { describe, expect, test } from "bun:test";
import type { SessionAgentEvent, SessionDetail, SessionMessage, SessionSummary } from "../src/api/types";
import { deriveChat } from "../src/view/chat-view";
import {
  addPending,
  createPending,
  PENDING_TTL_MS,
  pendingOf,
  removePending,
  seedDetail,
  settlePending,
} from "../src/view/pending-message";
import { reloadOnEvent } from "../src/view/session-stream";

const T0 = Date.parse("2026-11-09T09:00:00.000Z");
let seq = 0;
const user = (text: string, role: SessionMessage["role"] = "initiator"): SessionMessage => {
  seq += 1;
  return { id: `m${seq}`, seq, ts: new Date(T0 + seq * 1000).toISOString(), kind: "user", role, text };
};
const assistant = (text: string, turn = 1): SessionMessage => {
  seq += 1;
  return { id: `m${seq}`, seq, ts: new Date(T0 + seq * 1000).toISOString(), kind: "assistant", text, turn };
};
const summary = (patch: Partial<SessionSummary> = {}): SessionSummary => ({
  id: "s-1",
  workspaceId: "ws-1",
  status: "reception",
  readOnly: false,
  title: "新对话",
  attachments: [],
  createdAt: "2026-11-09T09:00:00.000Z",
  updatedAt: "2026-11-09T09:00:00.000Z",
  waitingFor: [],
  statusDot: "idle",
  unread: false,
  archived: false,
  ...patch,
});
const detail = (messages: SessionMessage[], patch: Partial<SessionSummary> = {}): SessionDetail => ({ ...summary(patch), messages });
const send = (text: string, at: SessionDetail | undefined, existing: ReturnType<typeof createPending>[] = [], nowMs = T0) =>
  createPending({ id: `p${existing.length + 1}`, sessionId: "s-1", text, role: "initiator", nowMs, detail: at, existing });

describe("createPending", () => {
  test("文字去掉首尾空白（和服务端落盘的一致），带发送时间", () => {
    const p = send("  帮我出周报\n", undefined);
    expect(p.text).toBe("帮我出周报");
    expect(p.ts).toBe("2026-11-09T09:00:00.000Z");
  });
  test("会话还没读到（新对话第一句）：基线是 0", () => {
    expect(send("你好", undefined).baseline).toBe(0);
  });
  test("基线 = 日志里已有的同文字同角色的人的话 + 还没确认的同样的待确认条", () => {
    const at = detail([user("好"), user("好"), user("别的"), assistant("嗯")]);
    const first = send("好", at);
    expect(first.baseline).toBe(2);
    expect(send("好", at, [first]).baseline).toBe(3);
  });
});

describe("settlePending", () => {
  test("正式消息还没进日志：保留", () => {
    const p = send("你好", detail([]));
    expect(settlePending([p], detail([]), T0 + 1000)).toEqual([p]);
  });
  test("正式消息进了日志：让位", () => {
    const p = send("你好", detail([]));
    expect(settlePending([p], detail([user("你好")]), T0 + 1000)).toEqual([]);
  });
  test("早先说过同样的话不算：要比发送时多出一条才算确认", () => {
    const before = detail([user("好")]);
    const p = send("好", before);
    expect(settlePending([p], before, T0 + 1000)).toEqual([p]);
    expect(settlePending([p], detail([user("好"), user("好")]), T0 + 1000)).toEqual([]);
  });
  test("连发两句同样的话：日志里进一条只确认第一条", () => {
    const base = detail([]);
    const p1 = send("好", base);
    const p2 = send("好", base, [p1]);
    const one = detail([user("好")]);
    expect(settlePending([p1, p2], one, T0 + 1000)).toEqual([p2]);
    expect(settlePending([p2], detail([user("好"), user("好")]), T0 + 1000)).toEqual([]);
  });
  test("角色不同的同样文字不互相确认", () => {
    const p = send("通过", detail([]));
    expect(settlePending([p], detail([user("通过", "reviewer")]), T0 + 1000)).toEqual([p]);
  });
  test("别的会话的待确认条不受影响", () => {
    const other = { ...send("你好", undefined), sessionId: "s-2" };
    expect(settlePending([other], detail([user("你好")]), T0 + 1000)).toEqual([other]);
  });
  test("过了 TTL 仍没等到正式消息：丢弃，避免一直挂着", () => {
    const p = send("你好", detail([]));
    expect(settlePending([p], detail([]), T0 + PENDING_TTL_MS + 1)).toEqual([]);
  });
});

describe("add / remove / pendingOf", () => {
  test("按会话取，按发送先后", () => {
    const a = send("甲", undefined);
    const b = { ...send("乙", undefined, [a]), sessionId: "s-2" };
    const c = send("丙", undefined, [a, b]);
    const all = [a, b, c].reduce(addPending, [] as typeof a[]);
    expect(pendingOf(all, "s-1").map((p) => p.text)).toEqual(["甲", "丙"]);
    expect(removePending(all, a.id).map((p) => p.text)).toEqual(["乙", "丙"]);
  });
});

describe("seedDetail", () => {
  test("刚建好的会话摘要补上空消息，就能先显示出来，不用等第一次读取", () => {
    expect(seedDetail(summary()).messages).toEqual([]);
  });
});

describe("对话里显示待确认的话", () => {
  test("新对话第一句：会话刚建好、没有任何消息，也立即显示在末尾", () => {
    const p = send("帮我出第 44 周的周报", undefined);
    const items = deriveChat(seedDetail(summary()), undefined, [], undefined, [], "initiator", [p]);
    expect(items).toHaveLength(1);
    expect(items[0]).toMatchObject({ kind: "user", text: "帮我出第 44 周的周报", pending: true, role: "initiator", ts: p.ts });
  });
  test("排在已有消息之后，Agent 这一轮的工具行、流式文字之前", () => {
    const p = send("再看看华南", detail([user("你好"), assistant("你好，我是前台 Agent", 1)]));
    const items = deriveChat(
      detail([user("你好"), assistant("你好，我是前台 Agent", 1)]),
      undefined,
      [],
      { turn: 2, text: "好的，我看看", tools: [{ id: "c1", tool: "list_files", ended: false }], running: true },
      [],
      "initiator",
      [p],
    );
    expect(items.map((i) => i.kind)).toEqual(["user", "agent", "user", "tools", "streaming"]);
    expect(items[2]).toMatchObject({ pending: true, text: "再看看华南" });
  });
  test("正式消息出现后（待确认条被 settle 掉），对话里只有一份", () => {
    const p = send("你好", undefined);
    const after = detail([user("你好")]);
    const items = deriveChat(after, undefined, [], undefined, [], "initiator", settlePending([p], after, T0 + 1000));
    expect(items.filter((i) => i.kind === "user")).toHaveLength(1);
    expect(items[0]).not.toHaveProperty("pending", true);
  });
  test("不传待确认条：行为和以前一样", () => {
    const d = detail([user("你好")]);
    expect(deriveChat(d)).toEqual(deriveChat(d, undefined, [], undefined, [], "initiator", []));
  });
});

describe("reloadOnEvent：哪些会话事件要重读会话", () => {
  const event = (patch: Partial<SessionAgentEvent>): SessionAgentEvent => ({ type: "text_delta", turn: 1, ...patch });
  test("一轮开始：用户的话这时一定已写进日志，重读让正式消息尽早进来", () => {
    expect(reloadOnEvent(event({ type: "turn_start" }))).toBe(true);
  });
  test("一轮结束", () => {
    expect(reloadOnEvent(event({ type: "turn_end" }))).toBe(true);
  });
  test("带流程卡片的消息", () => {
    expect(reloadOnEvent(event({ type: "message", message: { id: "m", seq: 1, ts: "", kind: "card", card: { type: "permission" } as never } }))).toBe(true);
  });
  test("文字增量、工具进度、不带卡片的消息不重读", () => {
    expect(reloadOnEvent(event({ type: "text_delta", text: "你" }))).toBe(false);
    expect(reloadOnEvent(event({ type: "tool_start" }))).toBe(false);
    expect(reloadOnEvent(event({ type: "tool_end" }))).toBe(false);
    expect(reloadOnEvent(event({ type: "message", message: { id: "m", seq: 1, ts: "", kind: "assistant", text: "好" } }))).toBe(false);
  });
});
