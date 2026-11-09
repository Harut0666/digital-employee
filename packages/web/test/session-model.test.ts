// T39 会话模型：输入框右下角的下拉（连接 / 模型）、切换后的说明、轨迹里的模型名。
// T45 新对话页的模型下拉：同一份数据、空状态、第一句话带上所选模型。
import { describe, expect, test } from "bun:test";
import type { TaskEvent } from "@de/core/contract";
import { createSessionApi, type FetchLike } from "../src/api/sessions";
import type { SessionDetail, SessionMessage } from "../src/api/types";
import { deriveChat } from "../src/view/chat-view";
import {
  modelLabel,
  modelMenuItems,
  newSessionModelMenu,
  parseModelValue,
} from "../src/view/session-model";
import { deriveTrajectory } from "../src/view/trajectory";

const OPTIONS = [
  { name: "DeepSeek", models: ["deepseek-chat", "deepseek-reasoner"] },
  { name: "通义千问", models: ["qwen-plus"] },
];

let clock = Date.parse("2026-11-02T09:00:00.000Z");
const msg = (input: Omit<SessionMessage, "id" | "seq" | "ts">): SessionMessage => {
  clock += 1000;
  const seq = clock / 1000;
  return { id: `s${seq}`, seq, ts: new Date(clock).toISOString(), ...input };
};

describe("模型下拉", () => {
  test("显示“连接 / 模型”", () => {
    expect(modelLabel({ connection: "DeepSeek", model: "deepseek-chat" })).toBe("DeepSeek / deepseek-chat");
    expect(modelLabel(undefined)).toBe("选择模型");
  });

  test("按连接分组列出模型；当前选中的标出来", () => {
    const groups = modelMenuItems(OPTIONS, { connection: "DeepSeek", model: "deepseek-reasoner" });
    expect(groups.map((g) => [g.label, g.options.map((o) => o.label)])).toEqual([
      ["DeepSeek", ["deepseek-chat", "deepseek-reasoner"]],
      ["通义千问", ["qwen-plus"]],
    ]);
    expect(groups[0]!.options.find((o) => o.model === "deepseek-reasoner")?.selected).toBe(true);
    expect(groups[0]!.options.find((o) => o.model === "deepseek-chat")?.selected).toBe(false);
  });

  test("没有可用模型时给空分组（页面据此提示去设置）", () => {
    expect(modelMenuItems([], undefined)).toEqual([]);
  });

  test("下拉的值能还原成连接和模型（连接名里有空格也行）", () => {
    const groups = modelMenuItems(OPTIONS, undefined);
    expect(parseModelValue(groups[1]!.options[0]!.value)).toEqual({ connection: "通义千问", model: "qwen-plus" });
  });
});

describe("新对话页的模型下拉（T45）", () => {
  test("和会话里的下拉同一份数据：按连接分组，选中的是新会话沿用的模型", () => {
    const menu = newSessionModelMenu(OPTIONS, { connection: "通义千问", model: "qwen-plus" });
    expect(menu.empty).toBe(false);
    expect(menu.groups.map((g) => [g.label, g.options.map((o) => o.label)])).toEqual([
      ["DeepSeek", ["deepseek-chat", "deepseek-reasoner"]],
      ["通义千问", ["qwen-plus"]],
    ]);
    expect(menu.groups[1]!.options[0]!.selected).toBe(true);
    expect(menu.groups[0]!.options.every((o) => !o.selected)).toBe(true);
    expect(menu.selectedValue).toBe("通义千问/qwen-plus");
  });

  test("没有可用模型时显示“还没有可用的模型”，带设置入口，不能选", () => {
    const menu = newSessionModelMenu([], undefined);
    expect(menu.empty).toBe(true);
    expect(menu.label).toBe("还没有可用的模型");
    expect(menu.settingsEntry).toBe("去设置里配置模型连接");
    expect(menu.selectedValue).toBeUndefined();
    expect(menu.items.some((item) => item.type === "group")).toBe(false);

    const withModels = newSessionModelMenu(OPTIONS, undefined);
    expect(withModels.label).toBe("选择模型");
    expect(withModels.settingsEntry).toBe("去设置里配置模型连接");
  });
});

describe("新对话的第一句话带上所选模型", () => {
  test("建会话时把所选模型发给服务端", async () => {
    const calls: { url: string; body: unknown }[] = [];
    const impl: FetchLike = async (url, init) => {
      calls.push({ url, body: init?.body });
      return new Response(JSON.stringify({ id: "s-1", workspaceId: "ws-1", status: "reception" }), { status: 201, headers: { "content-type": "application/json" } });
    };
    const api = createSessionApi("http://local", () => "initiator", impl);
    await api.createSession(undefined, { connection: "DeepSeek", model: "deepseek-reasoner" });
    expect(JSON.parse(String(calls[0]?.body))).toEqual({ model: { connection: "DeepSeek", model: "deepseek-reasoner" } });
  });

  test("没选模型时只带工作区", async () => {
    const calls: { body: unknown }[] = [];
    const impl: FetchLike = async (_url, init) => {
      calls.push({ body: init?.body });
      return new Response(JSON.stringify({ id: "s-1" }), { status: 201, headers: { "content-type": "application/json" } });
    };
    const api = createSessionApi("http://local", () => "initiator", impl);
    await api.createSession("ws-1");
    expect(JSON.parse(String(calls[0]?.body))).toEqual({ workspaceId: "ws-1" });
  });
});

describe("对话里的模型切换说明", () => {
  const session = (messages: SessionMessage[], patch: Partial<SessionDetail> = {}): SessionDetail => ({
    id: "s-1",
    workspaceId: "ws-1",
    status: "drafting",
    readOnly: false,
    title: "出周报",
    attachments: [],
    createdAt: "2026-11-02T09:00:00.000Z",
    updatedAt: "2026-11-02T09:10:00.000Z",
    waitingFor: [],
    statusDot: "idle",
    unread: false,
    archived: false,
    messages,
    model: { connection: "DeepSeek", model: "deepseek-reasoner" },
    modelSelected: true,
    ...patch,
  });

  test("会话里选过模型、任务运行中切换模型：对话里都不插说明（下拉已显示当前模型）", () => {
    const messages = [
      msg({ kind: "user", role: "initiator", text: "出周报" }),
      msg({ kind: "card", card: { type: "form", employee: "weekly-report", submitted: true, attachments: [] } }),
      msg({ kind: "assistant", text: "任务开始了。" }),
    ];
    expect(deriveChat(session([msg({ kind: "user", role: "initiator", text: "出周报" })])).map((i) => i.kind)).toEqual(["user"]);
    const switched = { seq: 9, ts: "2026-11-02T09:05:00.000Z", taskId: "t1", type: "model_switched" as const, model: { connection: "DeepSeek", model: "deepseek-reasoner" } };
    const items = deriveChat(session(messages, { status: "task", taskId: "t1" }), undefined, [switched]);
    expect(items.map((i) => i.kind)).toEqual(["user", "form", "agent"]);
  });

  test("轨迹里仍保留“模型已切换”事件", () => {
    const events = [
      { seq: 1, ts: "2026-11-02T09:00:00.000Z", taskId: "t1", type: "step_started", stepId: "match_causes", attempt: 1, reworkRound: 0 },
      { seq: 2, ts: "2026-11-02T09:00:01.000Z", taskId: "t1", type: "model_switched", model: { connection: "DeepSeek", model: "deepseek-reasoner" } },
    ] as TaskEvent[];
    expect(JSON.stringify(deriveTrajectory(events, []))).toContain("deepseek-reasoner");
  });
  test("轨迹里的模型调用带连接和模型名", () => {
    const events: TaskEvent[] = [
      { seq: 1, ts: "2026-11-02T09:00:00.000Z", taskId: "t1", type: "step_started", stepId: "match_causes", attempt: 1, reworkRound: 0 },
      { seq: 2, ts: "2026-11-02T09:00:01.000Z", taskId: "t1", type: "model_called", stepId: "match_causes", turn: 0, connection: "DeepSeek", modelName: "deepseek-reasoner" },
    ] as TaskEvent[];
    const trace = deriveTrajectory(events, [{ id: "match_causes", label: "匹配原因", kind: "judge" }]);
    const model = trace.rounds[0]?.items.find((i) => i.id === "r0:match_causes")?.children?.[0];
    expect(model?.kind).toBe("model");
    expect(model?.detail).toBe("DeepSeek / deepseek-reasoner");
    expect(model?.label).toBe("模型第 0 轮");
  });
});
