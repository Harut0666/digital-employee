// 回答重复的修复：一轮说完、会话重读之后，流式的临时内容与正式消息是同一个节点，对话里只有一份。
import { describe, expect, test } from "bun:test";
import type { SessionDetail, SessionMessage } from "../src/api/types";
import { deriveChat, type LiveTurn } from "../src/view/chat-view";

let clock = Date.parse("2026-11-02T09:00:00.000Z");
const msg = (input: Omit<SessionMessage, "id" | "seq" | "ts">, id?: string): SessionMessage => {
  clock += 1000;
  const seq = clock / 1000;
  return { id: id ?? `s${seq}`, seq, ts: new Date(clock).toISOString(), ...input };
};
const session = (messages: SessionMessage[]): SessionDetail => ({
  id: "s-1",
  workspaceId: "ws-1",
  status: "reception",
  readOnly: false,
  title: "t",
  attachments: [],
  createdAt: "2026-11-02T09:00:00.000Z",
  updatedAt: "2026-11-02T09:10:00.000Z",
  waitingFor: [],
  statusDot: "idle",
  unread: false,
  archived: false,
  messages,
});
const kinds = (items: ReturnType<typeof deriveChat>) => items.map((i) => i.kind);
const live = (patch: Partial<LiveTurn> = {}): LiveTurn => ({ turn: 2, text: "你好，我是员工。", tools: [], running: true, ...patch });

describe("流式内容与正式消息", () => {
  test("说话中、会话里还没有这条回复：显示一份流式内容", () => {
    const items = deriveChat(session([msg({ kind: "user", text: "hi" })]), undefined, [], live());
    expect(kinds(items)).toEqual(["user", "streaming"]);
  });

  test("turn_end 之后会话重读，正式消息已进来：只剩一份（按轮次认出）", () => {
    const items = deriveChat(session([msg({ kind: "user", text: "hi" }), msg({ kind: "assistant", text: "你好，我是员工。", turn: 2 })]), undefined, [], live({ running: false }));
    expect(kinds(items)).toEqual(["user", "agent"]);
  });

  test("还没有 turn 字段的消息（模拟模式）：说完之后文字对得上就算同一条", () => {
    const items = deriveChat(session([msg({ kind: "user", text: "hi" }), msg({ kind: "assistant", text: "你好，我是员工。" })]), undefined, [], live({ running: false }));
    expect(kinds(items)).toEqual(["user", "agent"]);
  });

  test("下一句话开始（新的一轮 turn_start）：上一条正式回复不受影响，只多出新一轮的流式内容", () => {
    const messages = [
      msg({ kind: "user", text: "hi" }),
      msg({ kind: "assistant", text: "你好，我是员工。", turn: 2 }),
      msg({ kind: "user", text: "再说一句" }),
    ];
    const items = deriveChat(session(messages), undefined, [], live({ turn: 3, text: "好的", running: true }));
    expect(kinds(items)).toEqual(["user", "agent", "user", "streaming"]);
  });

  test("再下一轮说完：两条回复各一份，不会一条变两条", () => {
    const messages = [
      msg({ kind: "assistant", text: "第一答", turn: 2 }),
      msg({ kind: "user", text: "再说一句" }),
      msg({ kind: "assistant", text: "第二答", turn: 3 }),
    ];
    const items = deriveChat(session(messages), undefined, [], live({ turn: 3, text: "第二答", running: false }));
    expect(kinds(items)).toEqual(["agent", "user", "agent"]);
  });

  test("用户点停止：正式消息是已说出的部分加“已中断”，流式内容让位", () => {
    const items = deriveChat(
      session([msg({ kind: "user", text: "hi" }), msg({ kind: "assistant", text: "你好，我是员工。（已中断）", turn: 2 })]),
      undefined,
      [],
      live({ running: false }),
    );
    expect(kinds(items)).toEqual(["user", "agent"]);
  });

  test("模型报错、没有正式回复：已说出的部分留在对话里，不再闪光标", () => {
    const items = deriveChat(session([msg({ kind: "user", text: "hi" }), msg({ kind: "model_failed", reason: "超时" })]), undefined, [], live({ running: false }));
    expect(kinds(items)).toEqual(["user", "streaming"]);
    const last = items.at(-1);
    expect(last?.kind === "streaming" && last.running).toBe(false);
  });

  test("流式与正式消息共用同一个 key（同一个节点），文字增长时 key 不变", () => {
    const streaming = deriveChat(session([]), undefined, [], live({ text: "你" })).at(-1);
    const longer = deriveChat(session([]), undefined, [], live({ text: "你好，我" })).at(-1);
    const final = deriveChat(session([msg({ kind: "assistant", text: "你好，我是员工。", turn: 2 })]), undefined, [], live({ running: false })).at(-1);
    expect(streaming?.key).toBe(longer?.key ?? "");
    expect(final?.key).toBe(streaming?.key ?? "");
  });

  test("这一轮的工具行留在对话里，并排在这一轮正式回复之前", () => {
    const tools = [{ id: "c1", tool: "read_file", args: { path: "a.md" }, ended: true, result: "x" }];
    const items = deriveChat(
      session([msg({ kind: "user", text: "hi" }), msg({ kind: "assistant", text: "你好，我是员工。", turn: 2 })]),
      undefined,
      [],
      live({ running: false, tools }),
    );
    expect(kinds(items)).toEqual(["user", "tools", "agent"]);
  });

  test("写文件的工具行已经有“改动”行时不重复", () => {
    const tools = [
      { id: "c1", tool: "write_file", args: { path: "a.md" }, ended: true, result: { path: "a.md" } },
      { id: "c2", tool: "read_file", args: { path: "b.md" }, ended: true, result: "x" },
    ];
    const messages = [
      msg({ kind: "tool_called", tool: "write_file", args: { path: "a.md", content: "hi" }, result: { path: "a.md", action: "created", bytes: 2 }, turn: 2 }),
      msg({ kind: "assistant", text: "写好了。", turn: 2 }),
    ];
    const items = deriveChat(session(messages), undefined, [], live({ running: false, text: "写好了。", tools }));
    const tooled = items.find((i) => i.kind === "tools");
    expect(tooled?.kind === "tools" && tooled.items.map((t) => t.tool)).toEqual(["read_file"]);
    expect(kinds(items)).toContain("file_change");
  });
});
