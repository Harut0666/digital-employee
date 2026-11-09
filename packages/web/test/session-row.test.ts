import { describe, expect, test } from "bun:test";
import type { SessionSummary } from "../src/api/types";
import { sessionRowView, sessionTime, sessionTone, stateTone } from "../src/view/session-row";

describe("侧栏里的时间", () => {
  const now = new Date(2026, 10, 9, 15, 30, 0);
  const at = (...args: [number, number, number, number?, number?, number?]) => new Date(...args).toISOString();

  test("刚更新、今天、昨天、今年、往年", () => {
    expect(sessionTime(new Date(2026, 10, 9, 15, 29, 40).toISOString(), now)).toBe("刚刚");
    expect(sessionTime(at(2026, 10, 9, 9, 5), now)).toBe("09:05");
    expect(sessionTime(at(2026, 10, 8, 23, 50), now)).toBe("昨天");
    expect(sessionTime(at(2026, 9, 21, 10, 0), now)).toBe("10月21日");
    expect(sessionTime(at(2025, 11, 30, 10, 0), now)).toBe("2025/12/30");
  });

  test("读不出时间时留空", () => {
    expect(sessionTime("", now)).toBe("");
    expect(sessionTime("not-a-time", now)).toBe("");
  });
});

const summary = (patch: Partial<SessionSummary> = {}): SessionSummary =>
  ({
    id: "s-1",
    workspaceId: "ws-1",
    status: "reception",
    readOnly: false,
    title: "第 44 周周报",
    attachments: [],
    createdAt: "2026-10-01T00:00:00Z",
    updatedAt: "2026-10-01T00:00:00Z",
    waitingFor: [],
    statusDot: "idle" as const,
    unread: false,
    archived: false,
    ...patch,
  }) satisfies SessionSummary;

describe("侧栏会话行", () => {
  test("普通会话：可以重命名和归档", () => {
    const row = sessionRowView(summary());
    expect(row.menu).toEqual([
      { key: "rename", label: "重命名" },
      { key: "archive", label: "归档" },
    ]);
    expect(row.readOnly).toBe(false);
  });

  test("只读会话（旧任务推导）：隐藏重命名，只能归档，行上标“只读”", () => {
    const row = sessionRowView(summary({ readOnly: true }));
    expect(row.menu).toEqual([{ key: "archive", label: "归档" }]);
    expect(row.readOnly).toBe(true);
    expect(row.canRename).toBe(false);
  });

  test("已归档的会话（含只读）：可以取消归档", () => {
    expect(sessionRowView(summary({ archived: true })).menu.at(-1)).toEqual({ key: "archive", label: "取消归档" });
    expect(sessionRowView(summary({ archived: true, readOnly: true })).menu).toEqual([{ key: "archive", label: "取消归档" }]);
  });

  test("“已完成未读”才有未读标记，归档的不算；已读的完成点变成空闲", () => {
    expect(sessionRowView(summary({ statusDot: "done", unread: true })).unread).toBe(true);
    expect(sessionRowView(summary({ statusDot: "done", unread: false })).tone).toBe("idle");
    expect(sessionRowView(summary({ statusDot: "done", unread: true, archived: true })).unread).toBe(false);
    expect(sessionRowView(summary({ statusDot: "done", unread: true, archived: true })).tone).toBe("idle");
  });

  test("状态点的语义固定：等你处理、运行中、已完成、空闲", () => {
    expect(sessionTone("waiting")).toBe("waiting");
    expect(sessionTone("running")).toBe("running");
    expect(sessionTone("done")).toBe("done");
    expect(sessionTone("idle")).toBe("idle");
    expect(sessionTone(undefined)).toBe("idle");
  });
});

describe("任务状态的语义色", () => {
  test("同一个状态在侧栏、对话顶部、轨迹里是同一种语义", () => {
    expect(stateTone("running")).toBe("running");
    expect(stateTone("admission")).toBe("running");
    expect(stateTone("qc")).toBe("running");
    expect(stateTone("awaiting_initiator")).toBe("waiting");
    expect(stateTone("awaiting_review")).toBe("waiting");
    expect(stateTone("awaiting_operator")).toBe("error");
    expect(stateTone("delivered")).toBe("done");
    expect(stateTone("draft")).toBe("idle");
    expect(stateTone("terminated")).toBe("idle");
    expect(stateTone(undefined)).toBe("idle");
  });
});
