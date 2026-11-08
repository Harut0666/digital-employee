import { describe, expect, test } from "bun:test";
import type { RecordItem, SessionSummary } from "../src/api/types";
import { dotView, groupRecords, groupSessionsByWorkspace, unreadOf, type WorkspaceGroup } from "../src/view/sidebar-tree";

const summary = (id: string, workspaceId: string, patch: Partial<SessionSummary> = {}): SessionSummary =>
  ({
    id,
    workspaceId,
    status: "reception",
    readOnly: false,
    title: id,
    attachments: [],
    createdAt: "2026-10-01T00:00:00Z",
    updatedAt: "2026-10-01T00:00:00Z",
    waitingFor: [],
    statusDot: "idle" as const,
    unread: false,
    archived: false,
    ...patch,
  }) satisfies SessionSummary;

const record = (taskId: string, group: RecordItem["group"], patch: Partial<RecordItem> = {}): RecordItem => ({
  taskId,
  group,
  title: taskId,
  employee: "weekly-report",
  state: "running",
  statusText: "",
  statusDot: "running",
  workspaceId: "ws-1",
  workspaceName: "我的工作区",
  updatedAt: "2026-10-01T00:00:00Z",
  ...patch,
});

const workspace = (id: string, name: string, patch: Partial<WorkspaceGroup["workspace"]> = {}) => ({ id, name, path: `D:\\${name}`, missing: false, ...patch });

const ids = (group: WorkspaceGroup) => group.sessions.map((s) => s.id);

describe("工作区 tab：分组", () => {
  test("每个工作区一组，组内按更新时间倒序；已归属的会话不重复出现在未归属里", () => {
    const groups = groupSessionsByWorkspace(
      [workspace("ws-1", "我的工作区"), workspace("ws-2", "华南区")],
      [
        summary("a", "ws-1", { updatedAt: "2026-10-01T10:00:00Z" }),
        summary("b", "ws-1", { updatedAt: "2026-10-02T10:00:00Z" }),
        summary("c", "ws-2", { updatedAt: "2026-10-03T10:00:00Z" }),
      ],
      [],
      { includeArchived: false },
    );
    expect(groups.map((g) => g.workspace?.name ?? "未归属")).toEqual(["我的工作区", "华南区"]);
    expect(groups.map(ids)).toEqual([["b", "a"], ["c"]]);
  });

  test("未归属放在最后，只有取消登记的工作区留下的会话进这里", () => {
    const groups = groupSessionsByWorkspace(
      [workspace("ws-1", "我的工作区")],
      [
        summary("mine", "ws-1"),
        summary("gone", "ws-gone", { workspaceName: "华南区", updatedAt: "2026-10-05T10:00:00Z" }),
        summary("legacy", "", { workspaceName: "", updatedAt: "2026-10-04T10:00:00Z" }),
      ],
      [],
      { includeArchived: false },
    );
    expect(groups.map((g) => g.key)).toEqual(["ws-1", "ungrouped"]);
    // 未归属里每行带上它原来的工作区名，前端只按 workspaceId 分组，不自己判断
    expect(groups[1]?.label).toBe("未归属");
    expect(groups[1]?.sessions.map((s) => `${s.id}@${s.workspaceName ?? ""}`)).toEqual(["gone@华南区", "legacy@"]);
  });

  test("已归档的会话默认隐藏，打开开关后回到原位（灰掉），不带未读和新消息", () => {
    const sessions = [
      summary("shown", "ws-1", { updatedAt: "2026-10-02T10:00:00Z" }),
      summary("archived", "ws-1", { archived: true, unread: true, updatedAt: "2026-10-03T10:00:00Z" }),
    ];
    const hidden = groupSessionsByWorkspace([workspace("ws-1", "A")], sessions, [], { includeArchived: false });
    expect(ids(hidden[0]!)).toEqual(["shown"]);
    const shown = groupSessionsByWorkspace([workspace("ws-1", "A")], sessions, [], { includeArchived: true });
    expect(ids(shown[0]!)).toEqual(["archived", "shown"]);
    const archived = shown[0]!.sessions[0]!;
    expect(dotView(archived)).toEqual({ dot: "default", archived: true });
    expect(unreadOf(archived)).toBe(false);
    // 归档的会话没有新消息
    expect(unreadOf(summary("x", "ws-1", { archived: true, unread: true }))).toBe(false);
  });

  test("折叠状态和当前会话：折叠的组不展开会话但计数照常；当前会话在哪个组由 workspaceId 决定", () => {
    const groups = groupSessionsByWorkspace(
      [workspace("ws-1", "A"), workspace("ws-2", "B")],
      [summary("s1", "ws-1"), summary("s2", "ws-1"), summary("s3", "ws-2")],
      ["ws-1"],
      { includeArchived: false, currentId: "s3" },
    );
    expect(groups[0]).toMatchObject({ expanded: false, count: 2, sessions: [] });
    expect(groups[1]).toMatchObject({ expanded: true, count: 1 });
    expect(groups.map((g) => g.hasCurrent)).toEqual([false, true]);
  });

  test("还没收起过的新工作区默认展开；折叠的组里有当前会话时组标题照样标出来", () => {
    const groups = groupSessionsByWorkspace([workspace("ws-9", "新加的")], [summary("n", "ws-9")], [], { includeArchived: false });
    expect(groups[0]?.expanded).toBe(true);
    const folded = groupSessionsByWorkspace([workspace("ws-9", "新加的")], [summary("n", "ws-9")], ["ws-9"], { includeArchived: false, currentId: "n" });
    expect(folded[0]).toMatchObject({ expanded: false, hasCurrent: true, sessions: [] });
  });

  test("状态点直接用后端的值：等你处理 / 运行中 / 已完成未读 / 空闲", () => {
    expect(dotView(summary("a", "ws-1", { statusDot: "waiting" }))).toEqual({ dot: "warning", archived: false });
    expect(dotView(summary("b", "ws-1", { statusDot: "running" }))).toEqual({ dot: "processing", archived: false });
    expect(dotView(summary("c", "ws-1", { statusDot: "done", unread: true }))).toEqual({ dot: "success", archived: false });
    expect(dotView(summary("d", "ws-1", { statusDot: "idle" }))).toEqual({ dot: "default", archived: false });
    expect(unreadOf(summary("c", "ws-1", { statusDot: "done", unread: true }))).toBe(true);
    expect(unreadOf(summary("d", "ws-1", { statusDot: "idle" }))).toBe(false);
  });
});

describe("记录 tab：分组", () => {
  const records = [
    record("t1", "supplement", { updatedAt: "2026-10-01T10:00:00Z" }),
    record("t2", "running", { statusDot: "running", updatedAt: "2026-10-02T10:00:00Z" }),
    record("t3", "review", { state: "awaiting_review", updatedAt: "2026-10-03T10:00:00Z" }),
    record("t4", "delivered", { state: "delivered", statusDot: "done", updatedAt: "2026-10-04T10:00:00Z" }),
    record("t5", "terminated", { state: "terminated", statusDot: "idle", updatedAt: "2026-10-05T10:00:00Z" }),
    record("t6", "returned", { updatedAt: "2026-10-06T10:00:00Z" }),
    record("t7", "supplement", { updatedAt: "2026-10-07T10:00:00Z" }),
  ];

  test("按 进行中、待补充、待审核、已退回、已交付、已终止 的顺序分组，空的组也保留", () => {
    const groups = groupRecords(records);
    expect(groups.map((g) => g.label)).toEqual(["进行中", "待补充", "待审核", "已退回", "已交付", "已终止"]);
    expect(groups.map((g) => g.items.map((r) => r.taskId))).toEqual([["t2"], ["t1", "t7"], ["t3"], ["t6"], ["t4"], ["t5"]]);
    expect(groupRecords([]).every((g) => g.items.length === 0)).toBe(true);
  });

  test("读的人换了角色，状态点跟着后端给的变：审核人看待审核是等你处理，发起人看成空闲", () => {
    const forReviewer = groupRecords([record("t3", "review", { statusDot: "waiting" })]);
    expect(dotView(forReviewer[2]!.items[0]!)).toEqual({ dot: "warning", archived: false });
    const forInitiator = groupRecords([record("t3", "review", { statusDot: "idle" })]);
    expect(dotView(forInitiator[2]!.items[0]!)).toEqual({ dot: "default", archived: false });
  });

  test("后端把任务归到别的组时以前端不管：组键完全照后端的 group", () => {
    const groups = groupRecords([record("t9", "running", { state: "awaiting_operator" })]);
    expect(groups[0]!.items.map((r) => r.taskId)).toEqual(["t9"]);
    expect(groups[1]!.items).toEqual([]);
  });
});
