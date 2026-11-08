// T39 会话模型：输入框右下角的下拉（连接 / 模型）、切换后的说明、轨迹里的模型名。
import { describe, expect, test } from "bun:test";
import type { TaskEvent } from "@de/core/contract";
import type { SessionDetail, SessionMessage } from "../src/api/types";
import { deriveChat } from "../src/view/chat-view";
import { modelLabel, modelMenuItems, modelNotices, modelSwitchedNotices, modelSwitchNotice, parseModelValue } from "../src/view/session-model";
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

  test("切换说明是一句话，写“连接 / 模型”，任务运行中说明从下一个判断步骤生效", () => {
    expect(modelSwitchNotice({ connection: "DeepSeek", model: "deepseek-reasoner" }, false)).toBe("模型已切换为 DeepSeek / deepseek-reasoner。");
    expect(modelSwitchNotice({ connection: "DeepSeek", model: "deepseek-reasoner" }, true)).toContain("下一个判断步骤");
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
    messages,
    model: { connection: "DeepSeek", model: "deepseek-reasoner" },
    modelSelected: true,
    ...patch,
  });

  test("会话里选过模型：对话末尾插一条切换说明", () => {
    const items = deriveChat(session([msg({ kind: "user", role: "initiator", text: "出周报" })]));
    expect(items.map((i) => i.kind)).toEqual(["user", "notice"]);
    const notice = items.at(-1);
    expect(notice?.kind === "notice" && notice.text).toContain("DeepSeek / deepseek-reasoner");
    // 没选过（沿用 lastModel，人没在会话里选过）就不插；只读的旧任务会话也不插
    expect(deriveChat(session([], { modelSelected: false })).length).toBe(0);
    expect(deriveChat(session([], { readOnly: true, taskId: "t1" })).length).toBe(0);
  });

  test("任务事件里的“模型已切换”每次一条说明，带上切换前后的模型", () => {
    const switched = {
      seq: 6,
      ts: "2026-11-02T09:05:00.000Z",
      taskId: "t1",
      type: "model_switched" as const,
      fromModel: { connection: "DeepSeek", model: "deepseek-chat" },
      model: { connection: "DeepSeek", model: "deepseek-reasoner" },
    };
    expect(modelSwitchedNotices([switched]).map((n) => n.text)).toEqual(["模型已切换为 DeepSeek / deepseek-reasoner，从下一个判断步骤开始生效。"]);
    // 之后又切回来：两条都要显示
    expect(
      modelNotices([
        switched,
        { ...switched, seq: 9, fromModel: switched.model, model: { connection: "DeepSeek", model: "deepseek-chat" } },
      ]),
    ).toHaveLength(2);
  });

  test("任务运行中切换：说明按时间插在对话里，不排在最后", () => {
    const messages = [
      msg({ kind: "user", role: "initiator", text: "出周报" }),
      msg({ kind: "card", card: { type: "form", employee: "weekly-report", submitted: true, attachments: [] } }),
      msg({ kind: "assistant", text: "任务开始了。" }),
    ];
    const switched = { seq: 9, ts: "2026-11-02T09:05:00.000Z", taskId: "t1", type: "model_switched" as const, model: { connection: "DeepSeek", model: "deepseek-reasoner" } };
    const items = deriveChat(session(messages, { status: "task", taskId: "t1" }), undefined, [switched]);
    expect(items.map((i) => i.kind)).toEqual(["user", "form", "agent", "notice"]);
    const earlier = { ...switched, seq: 3, ts: "2026-11-02T08:59:00.000Z" };
    expect(deriveChat(session(messages, { taskId: "t1" }), undefined, [earlier]).map((i) => i.kind)).toEqual(["notice", "user", "form", "agent"]);
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
