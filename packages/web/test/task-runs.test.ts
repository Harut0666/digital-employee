import { describe, expect, test } from "bun:test";
import type { TaskEvent } from "@de/core/contract";
import type { RecordItem, SessionDetail, SessionMessage } from "../src/api/types";
import { deriveChat, type PastRun } from "../src/view/chat-view";
import { groupRecords, runLabel } from "../src/view/sidebar-tree";
import { latestTaskId, pastTaskIds, taskIdsOf } from "../src/view/task-runs";
import { seqOf, STEP_DEFS } from "./helpers";

const T0 = Date.parse("2026-11-02T09:00:00.000Z");
let tick = 0;
const msg = (input: Omit<SessionMessage, "id" | "seq" | "ts">, id: string, offsetSec: number): SessionMessage => {
  tick += 1;
  return { id, seq: tick, ts: new Date(T0 + offsetSec * 1000).toISOString(), ...input };
};

const session = (messages: SessionMessage[], patch: Partial<SessionDetail> = {}): SessionDetail => ({
  id: "s-1",
  workspaceId: "ws-1",
  status: "task",
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
  ...patch,
});

describe("会话的任务列表", () => {
  test("按开工顺序；旧后端只给 taskId 时读作一个任务；没有任务时为空", () => {
    expect(taskIdsOf({ taskId: "t2", taskIds: ["t1", "t2"] })).toEqual(["t1", "t2"]);
    expect(taskIdsOf({ taskId: "t1" })).toEqual(["t1"]);
    expect(taskIdsOf({})).toEqual([]);
  });

  test("当前任务是最新的一个，其余是早先的任务", () => {
    const multi = { taskId: "t3", taskIds: ["t1", "t2", "t3"] };
    expect(latestTaskId(multi)).toBe("t3");
    expect(pastTaskIds(multi)).toEqual(["t1", "t2"]);
    expect(pastTaskIds({ taskId: "t1" })).toEqual([]);
    expect(latestTaskId({})).toBeUndefined();
  });
});

describe("多任务会话的对话", () => {
  // 第一个任务（已交付）的事件：从 09:00:10 开始
  const firstEvents: TaskEvent[] = seqOf(
    [
      { type: "task_created", employee: "weekly-report", configHash: "h", form: {}, attachments: [] },
      { type: "step_started", stepId: "load" },
      { type: "step_completed", stepId: "load", result: {} },
      { type: "state_changed", from: "awaiting_review", to: "delivered", trigger: "approve", actor: "reviewer" },
    ] as never,
    new Date(T0 + 10_000).toISOString(),
  ).map((event) => ({ ...event, taskId: "t1" }));
  const past: PastRun = {
    taskId: "t1",
    run: 1,
    state: "delivered",
    deliverables: [{ kind: "docx", name: "周报.docx", version: 1 } as never],
    events: firstEvents,
    steps: STEP_DEFS,
  };

  const messages = [
    msg({ kind: "user", role: "initiator", text: "出第 43 周" }, "s1", 0),
    msg({ kind: "card", card: { type: "form", employee: "weekly-report", submitted: true, taskId: "t1" } }, "t1", 10),
    msg({ kind: "notice", text: "任务已交付。" }, "t4", 13),
    msg({ kind: "user", role: "initiator", text: "再来一版第 44 周" }, "s9", 60),
    msg({ kind: "card", card: { type: "form", employee: "weekly-report", submitted: true, taskId: "t2" } }, "r2-t1", 70),
  ];
  const detail = session(messages, { taskId: "t2", taskIds: ["t1", "t2"], taskState: "running" });

  test("早先任务的步骤行和交付物留在它发生的时间点，不因后来有新任务而消失", () => {
    const items = deriveChat(detail, undefined, [], undefined, [past]);
    expect(items.map((i) => i.kind)).toEqual(["user", "form", "past_steps", "notice", "delivered", "user", "form"]);
    const steps = items.find((i) => i.kind === "past_steps");
    expect(steps).toMatchObject({ kind: "past_steps", taskId: "t1", run: 1 });
    expect(steps && steps.kind === "past_steps" ? steps.rows.map((row) => [row.id, row.status]).filter(([id]) => id === "load") : []).toEqual([["load", "done"]]);
    const delivered = items.find((i) => i.kind === "delivered");
    expect(delivered).toMatchObject({ kind: "delivered", taskId: "t1", run: 1 });
  });

  test("没有早先任务时和以前一样", () => {
    const items = deriveChat(session(messages.slice(0, 3), { taskId: "t1", taskIds: ["t1"] }));
    expect(items.map((i) => i.kind)).toEqual(["user", "form", "notice"]);
  });

  test("两张入库卡片各在自己任务交付的位置", () => {
    const filing = (taskId: string, id: string, offsetSec: number) =>
      msg({ kind: "card", card: { type: "knowledge_filing", taskId, title: taskId } }, id, offsetSec);
    const withFilings = session([...messages.slice(0, 3), filing("t1", "t4-filing", 13), ...messages.slice(3), filing("t2", "r2-t9-filing", 90)], {
      taskId: "t2",
      taskIds: ["t1", "t2"],
    });
    const items = deriveChat(withFilings, undefined, [], undefined, [past]);
    const order = items.map((i) => (i.kind === "filing" ? `filing:${i.taskId}` : i.kind));
    expect(order.indexOf("filing:t1")).toBeLessThan(order.indexOf("filing:t2"));
    // 第一张入库卡片紧跟第一个任务的交付物，在第二个任务开工之前
    expect(order.indexOf("filing:t1")).toBeLessThan(items.findIndex((i) => i.kind === "user" && i.text.includes("再来一版")));
  });
});

describe("交付后准备下一次运行", () => {
  const form = (id: string, offsetSec: number, submitted: boolean) =>
    msg({ kind: "card", card: { type: "form", employee: "weekly-report", submitted, taskId: submitted ? "t1" : undefined } }, id, offsetSec);

  test("任务交付后，旧会话里已发的草稿表单卡都只读（T49 起开工入口是员工表单卡）", () => {
    const items = deriveChat(
      session([form("old-draft", 1, false), form("started", 10, true), msg({ kind: "notice", text: "任务已交付。" }, "done", 13), form("next-draft", 70, false)], {
        status: "closed",
        taskId: "t1",
        taskIds: ["t1"],
        draft: { period: { year: 2026, week: 45 } },
      }),
    );
    const modes = items.flatMap((i) => (i.kind === "form" ? [[i.key, i.mode]] : []));
    expect(modes).toEqual([
      ["old-draft", "superseded"],
      ["started", "submitted"],
      ["next-draft", "superseded"],
    ]);
  });

  test("有任务在进行时，表单卡不可编辑", () => {
    const items = deriveChat(session([form("started", 10, true), form("next-draft", 70, false)], { status: "task", taskId: "t1", taskIds: ["t1"] }));
    expect(items.flatMap((i) => (i.kind === "form" ? [i.mode] : []))).toEqual(["submitted", "superseded"]);
  });
});

describe("记录：同一会话的多个任务各占一行", () => {
  const row = (taskId: string, group: RecordItem["group"], runNumber: number, sessionId = "s-1"): RecordItem => ({
    taskId,
    group,
    title: "出周报",
    employee: "weekly-report",
    state: "delivered",
    statusText: "",
    statusDot: "idle",
    sessionId,
    runNumber,
    workspaceId: "ws-1",
    workspaceName: "我的工作区",
    updatedAt: "2026-11-02T09:00:00Z",
  });

  test("按各自的状态分组", () => {
    const groups = groupRecords([row("t2", "review", 2), row("t1", "delivered", 1)]);
    expect(groups.find((g) => g.key === "review")?.items.map((r) => r.taskId)).toEqual(["t2"]);
    expect(groups.find((g) => g.key === "delivered")?.items.map((r) => r.taskId)).toEqual(["t1"]);
  });

  test("同一会话有多次运行时每行标出“第几次运行”，只有一次运行的不标", () => {
    const records = [row("t2", "review", 2), row("t1", "delivered", 1), row("t9", "delivered", 1, "s-2")];
    expect(runLabel(records[0]!, records)).toBe("第 2 次运行");
    expect(runLabel(records[1]!, records)).toBe("第 1 次运行");
    expect(runLabel(records[2]!, records)).toBe("");
  });
});
