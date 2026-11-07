import { describe, expect, test } from "bun:test";
import type { TaskSummary, UiRole } from "../src/api/types";
import { groupSessions, groupTodos } from "../src/view/session-groups";

const s = (id: string, state: string, updatedAt: string, waitingFor: UiRole[], statusText = id) =>
  ({ id, employee: "weekly-report", title: id, state, updatedAt, waitingFor, statusText }) as TaskSummary;

const tasks = [
  s("a", "awaiting_operator", "2026-10-28T14:22:00Z", ["initiator", "operator"], "工具调用失败"),
  s("b", "awaiting_review", "2026-10-29T09:00:00Z", ["reviewer"], "待审核"),
  s("c", "running", "2026-10-28T16:40:00Z", [], "执行中"),
  s("d", "delivered", "2026-10-21T18:20:00Z", [], "已交付"),
  s("e", "terminated", "2026-10-22T10:00:00Z", [], "已终止"),
  s("f", "awaiting_initiator", "2026-10-27T10:00:00Z", ["initiator"], "缺少本周纪要"),
  s("g", "awaiting_operator", "2026-10-26T10:00:00Z", ["operator"], "已达执行上限"),
];

const ids = (g: { items: { id: string }[] }) => g.items.map((x) => x.id);

describe("groupSessions", () => {
  test("需要我处理由摘要的 waitingFor 决定：工具故障同时算发起人和操作员的", () => {
    const [mine, active, done] = groupSessions(tasks, "initiator");
    expect([mine?.label, active?.label, done?.label]).toEqual(["需要我处理", "运行中", "已结束"]);
    expect(ids(mine!)).toEqual(["a", "f"]);
    expect(ids(active!)).toEqual(["b", "c", "g"]);
    expect(ids(groupSessions(tasks, "operator")[0]!)).toEqual(["a", "g"]);
    expect(ids(groupSessions(tasks, "reviewer")[0]!)).toEqual(["b"]);
  });

  test("已交付和已终止都归入已结束，按更新时间倒序，并带状态色点", () => {
    const done = groupSessions(tasks, "initiator")[2]!;
    expect(ids(done)).toEqual(["e", "d"]);
    expect(done.items[0]?.dot).toBe("default");
    expect(done.items[1]?.dot).toBe("success");
  });

  test("别人的待办在副标题里说明在等谁；我的待办只说状态", () => {
    const [mine, active] = groupSessions(tasks, "initiator");
    expect(mine!.items.find((t) => t.id === "a")?.sub).toBe("工具调用失败");
    expect(active!.items.find((t) => t.id === "g")?.sub).toBe("已达执行上限 · 等操作员");
    expect(active!.items.find((t) => t.id === "c")?.sub).toBe("执行中");
  });
});

describe("groupTodos", () => {
  test("全部待办按角色列出，一个任务等多个角色时在每个角色下都出现", () => {
    const groups = groupTodos(tasks);
    expect(groups.map((g) => g.label)).toEqual(["发起人", "审核人", "操作员"]);
    expect(groups.map(ids)).toEqual([["a", "f"], ["b"], ["a", "g"]]);
  });
});
