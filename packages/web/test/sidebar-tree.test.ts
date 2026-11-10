import { describe, expect, test } from "bun:test";
import type { RecordItem, SessionSummary } from "../src/api/types";
import { dotView, flatSessionList, flattenSessions, groupRecords, groupSessionsByWorkspace, unreadOf, type WorkspaceGroup } from "../src/view/sidebar-tree";

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
      {},
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
      {},
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
    const hidden = groupSessionsByWorkspace([workspace("ws-1", "A")], sessions, [], {});
    expect(ids(hidden[0]!)).toEqual(["shown"]);
    const shown = groupSessionsByWorkspace([workspace("ws-1", "A")], sessions, [], { filter: "all" });
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
      { currentId: "s3" },
    );
    expect(groups[0]).toMatchObject({ expanded: false, count: 2, sessions: [] });
    expect(groups[1]).toMatchObject({ expanded: true, count: 1 });
    expect(groups.map((g) => g.hasCurrent)).toEqual([false, true]);
  });

  test("还没收起过的新工作区默认展开；折叠的组里有当前会话时组标题照样标出来", () => {
    const groups = groupSessionsByWorkspace([workspace("ws-9", "新加的")], [summary("n", "ws-9")], [], {});
    expect(groups[0]?.expanded).toBe(true);
    const folded = groupSessionsByWorkspace([workspace("ws-9", "新加的")], [summary("n", "ws-9")], ["ws-9"], { currentId: "n" });
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

describe("工作区 tab：筛选、排序、折叠旧会话", () => {
  const A = [workspace("ws-1", "A")];
  const many = (n: number, patch: (i: number) => Partial<SessionSummary> = () => ({})) =>
    Array.from({ length: n }, (_, i) => summary(`s${i}`, "ws-1", { updatedAt: new Date(Date.UTC(2026, 9, 1, i)).toISOString(), ...patch(i) }));

  test("筛选：仅已归档只留归档的，全部两者都留，默认隐藏归档", () => {
    const sessions = [summary("live", "ws-1"), summary("old", "ws-1", { archived: true })];
    expect(ids(groupSessionsByWorkspace(A, sessions, [], {})[0]!)).toEqual(["live"]);
    expect(ids(groupSessionsByWorkspace(A, sessions, [], { filter: "archived" })[0]!)).toEqual(["old"]);
    expect(groupSessionsByWorkspace(A, sessions, [], { filter: "all" })[0]!.count).toBe(2);
  });

  test("排序：按名称时不分大小写依字母顺序，按最近更新时新的在前", () => {
    const sessions = [
      summary("1", "ws-1", { title: "charlie", updatedAt: "2026-10-03T00:00:00Z" }),
      summary("2", "ws-1", { title: "beta", updatedAt: "2026-10-01T00:00:00Z" }),
      summary("3", "ws-1", { title: "Alpha", updatedAt: "2026-10-02T00:00:00Z" }),
    ];
    expect(ids(groupSessionsByWorkspace(A, sessions, [], { sort: "name" })[0]!)).toEqual(["3", "2", "1"]);
    expect(ids(groupSessionsByWorkspace(A, sessions, [], { sort: "recent" })[0]!)).toEqual(["1", "3", "2"]);
  });

  test("每个工作区默认只显示最近 5 个，其余折成“展开 N 条”，count 仍是总数", () => {
    const group = groupSessionsByWorkspace(A, many(8), [], {})[0]!;
    expect(group.count).toBe(8);
    expect(ids(group)).toEqual(["s7", "s6", "s5", "s4", "s3"]);
    expect(group.hidden).toBe(3);
    expect(group.showingAll).toBe(false);
  });

  test("运行中、等你处理、未读、当前选中的会抬进显示范围，仍按排序位置排", () => {
    const sessions = many(9, (i) => ({
      statusDot: i === 0 ? ("running" as const) : i === 1 ? ("waiting" as const) : i === 2 ? ("done" as const) : ("idle" as const),
      unread: i === 2,
    }));
    const group = groupSessionsByWorkspace(A, sessions, [], { currentId: "s3" })[0]!;
    // 最近的 5 个（s8..s4）+ 抬高的 4 个（s0 运行中、s1 等你处理、s2 已完成未读、s3 当前选中），顺序仍是最近更新在前
    expect(ids(group)).toEqual(["s8", "s7", "s6", "s5", "s4", "s3", "s2", "s1", "s0"]);
    expect(group.hidden).toBe(0);
    const calm = groupSessionsByWorkspace(A, many(9), [], { currentId: "s1" })[0]!;
    expect(ids(calm)).toEqual(["s8", "s7", "s6", "s5", "s4", "s1"]);
    expect(calm.hidden).toBe(3);
  });

  test("已读的已完成、归档的不抬高", () => {
    const sessions = many(7, (i) => (i === 0 ? { statusDot: "done" as const, unread: false } : {}));
    expect(groupSessionsByWorkspace(A, sessions, [], {})[0]!.hidden).toBe(2);
  });

  test("点“展开 N 条”后（showAll 带着组键）显示全部", () => {
    const group = groupSessionsByWorkspace(A, many(8), [], { showAll: ["ws-1"] })[0]!;
    expect(group.sessions).toHaveLength(8);
    expect(group.hidden).toBe(0);
    expect(group.showingAll).toBe(true);
  });

  test("少于 5 条时不折叠", () => {
    const group = groupSessionsByWorkspace(A, many(5), [], {})[0]!;
    expect(group.sessions).toHaveLength(5);
    expect(group.hidden).toBe(0);
    expect(group.showingAll).toBe(false);
  });

  test("单列表：所有工作区的会话合成一个列表，每行带工作区名，同样按筛选和排序", () => {
    const sessions = [
      summary("a", "ws-1", { title: "甲", updatedAt: "2026-10-01T00:00:00Z" }),
      summary("b", "ws-2", { title: "乙", updatedAt: "2026-10-02T00:00:00Z" }),
      summary("c", "ws-2", { title: "丙", archived: true, updatedAt: "2026-10-03T00:00:00Z" }),
    ];
    const spaces = [workspace("ws-1", "A"), workspace("ws-2", "B")];
    expect(flatSessionList(spaces, sessions, {}).map((r) => [r.session.id, r.workspaceName])).toEqual([
      ["b", "B"],
      ["a", "A"],
    ]);
    const named = [summary("x", "ws-1", { title: "Beta" }), summary("y", "ws-2", { title: "Alpha", archived: true })];
    expect(flatSessionList(spaces, named, { filter: "all", sort: "name" }).map((r) => r.session.id)).toEqual(["y", "x"]);
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

describe("工作区列表 → 全部会话", () => {
  test("各工作区自带的会话加上未归属；未归属里已被登记的不重复", () => {
    const a = summary("a", "ws-1");
    const b = summary("b", "ws-gone");
    const dup = summary("dup", "ws-1");
    const list = { readOnly: false, workspaces: [{ ...workspace("ws-1", "我的工作区"), sessions: [a] }], unassigned: [b, dup] };
    expect(flattenSessions(list).map((s) => s.id)).toEqual(["a", "b"]);
    expect(flattenSessions(undefined)).toEqual([]);
  });
});
