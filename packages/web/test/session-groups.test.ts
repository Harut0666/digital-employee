import { describe, expect, test } from "bun:test";
import type { SessionStatus, SessionSummary, UiRole } from "../src/api/types";
import { groupSessions, groupTodos } from "../src/view/session-groups";

const s = (id: string, status: SessionStatus, updatedAt: string, waitingFor: UiRole[], statusText?: string, patch: Partial<SessionSummary> = {}) =>
  ({
    id,
    status,
    readOnly: false,
    title: id,
    attachments: [],
    createdAt: updatedAt,
    updatedAt,
    waitingFor,
    statusText,
    taskId: status === "task" || status === "closed" ? `task-of-${id}` : undefined,
    ...patch,
  }) satisfies SessionSummary;

const sessions = [
  s("a", "task", "2026-10-28T14:22:00Z", ["initiator", "operator"], "工具调用失败"),
  s("b", "task", "2026-10-29T09:00:00Z", ["reviewer"], "待审核"),
  s("c", "task", "2026-10-28T16:40:00Z", [], "执行中"),
  s("d", "closed", "2026-10-21T18:20:00Z", [], "已交付", { taskState: "delivered" }),
  s("e", "closed", "2026-10-22T10:00:00Z", [], "已终止", { readOnly: true, taskState: "terminated" }),
  s("f", "task", "2026-10-27T10:00:00Z", ["initiator"], "缺少本周纪要"),
  s("g", "task", "2026-10-26T10:00:00Z", ["operator"], "已达执行上限"),
];

const ids = (g: { items: { id: string }[] }) => g.items.map((x) => x.id);

describe("groupSessions", () => {
  test("需要我处理由摘要的 waitingFor 决定：工具故障同时算发起人和操作员的", () => {
    const [mine, active, done] = groupSessions(sessions, "initiator");
    expect([mine?.label, active?.label, done?.label]).toEqual(["需要我处理", "运行中", "已结束"]);
    expect(ids(mine!)).toEqual(["a", "f"]);
    expect(ids(active!)).toEqual(["b", "c", "g"]);
    expect(ids(groupSessions(sessions, "operator")[0]!)).toEqual(["a", "g"]);
    expect(ids(groupSessions(sessions, "reviewer")[0]!)).toEqual(["b"]);
  });

  test("已结束的会话按更新时间倒序，并带状态色点；旧任务的只读会话一样列出", () => {
    const done = groupSessions(sessions, "initiator")[2]!;
    expect(ids(done)).toEqual(["e", "d"]);
    expect(done.items[0]?.dot).toBe("default");
    expect(done.items[1]?.dot).toBe("success");
  });

  test("别人的待办在副标题里说明在等谁；我的待办只说状态", () => {
    const [mine, active] = groupSessions(sessions, "initiator");
    expect(mine!.items.find((t) => t.id === "a")?.sub).toBe("工具调用失败");
    expect(active!.items.find((t) => t.id === "g")?.sub).toBe("已达执行上限 · 等操作员");
    expect(active!.items.find((t) => t.id === "c")?.sub).toBe("执行中");
  });

  test("还没提交的会话在等发起人：接待中等确认员工，接手后等填表提交", () => {
    const drafts = [
      s("r", "reception", "2026-10-30T10:00:00Z", []),
      s("w", "drafting", "2026-10-30T11:00:00Z", [], undefined, { employee: "weekly-report" }),
    ];
    const [mine] = groupSessions(drafts, "initiator");
    expect(ids(mine!)).toEqual(["w", "r"]);
    expect(mine!.items.map((i) => i.sub)).toEqual(["待提交", "待确认员工"]);
    const [, active] = groupSessions(drafts, "reviewer");
    expect(active!.items.map((i) => i.sub)).toEqual(["待提交 · 等发起人", "待确认员工 · 等发起人"]);
  });
});

describe("groupTodos", () => {
  test("全部待办按角色列出，一个任务等多个角色时在每个角色下都出现", () => {
    const groups = groupTodos([...sessions, s("w", "drafting", "2026-10-30T11:00:00Z", [])]);
    expect(groups.map((g) => g.label)).toEqual(["发起人", "审核人", "操作员"]);
    expect(groups.map(ids)).toEqual([["w", "a", "f"], ["b"], ["a", "g"]]);
  });
});
