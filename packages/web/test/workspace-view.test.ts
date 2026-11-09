import { describe, expect, test } from "bun:test";
import type { SessionSummary } from "../src/api/types";
import {
  DEFAULT_VIEW_OPTIONS,
  loadViewOptions,
  parseViewOptions,
  saveViewOptions,
  searchSessions,
  serializeViewOptions,
  VIEW_STORAGE_KEY,
  workspaceNameOf,
} from "../src/view/workspace-view";

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

describe("视图选项：读写", () => {
  test("存储键固定为 de.workspace.view.v1", () => {
    expect(VIEW_STORAGE_KEY).toBe("de.workspace.view.v1");
  });

  test("读不到、不是 JSON、不是对象时用默认值", () => {
    expect(parseViewOptions(undefined)).toEqual(DEFAULT_VIEW_OPTIONS);
    expect(parseViewOptions(null)).toEqual(DEFAULT_VIEW_OPTIONS);
    expect(parseViewOptions("{坏的")).toEqual(DEFAULT_VIEW_OPTIONS);
    expect(parseViewOptions("42")).toEqual(DEFAULT_VIEW_OPTIONS);
  });

  test("默认：按工作区、最近更新、隐藏已归档", () => {
    expect(DEFAULT_VIEW_OPTIONS).toEqual({ group: "workspace", sort: "recent", filter: "active" });
  });

  test("字段不合法或缺失时各自回到默认，合法的保留", () => {
    expect(parseViewOptions(JSON.stringify({ group: "flat", sort: "乱写", filter: "archived" }))).toEqual({ group: "flat", sort: "recent", filter: "archived" });
    expect(parseViewOptions(JSON.stringify({ sort: "name" }))).toEqual({ group: "workspace", sort: "name", filter: "active" });
  });

  test("写出再读回来一样", () => {
    const options = { group: "flat", sort: "name", filter: "all" } as const;
    expect(parseViewOptions(serializeViewOptions(options))).toEqual(options);
  });

  test("load / save 走给定的存储；存储抛错时不抛、回到默认", () => {
    const data = new Map<string, string>();
    const storage = { getItem: (k: string) => data.get(k) ?? null, setItem: (k: string, v: string) => void data.set(k, v) };
    saveViewOptions({ group: "flat", sort: "name", filter: "archived" }, storage);
    expect(data.has(VIEW_STORAGE_KEY)).toBe(true);
    expect(loadViewOptions(storage)).toEqual({ group: "flat", sort: "name", filter: "archived" });

    const broken = {
      getItem: () => {
        throw new Error("denied");
      },
      setItem: () => {
        throw new Error("denied");
      },
    };
    expect(loadViewOptions(broken)).toEqual(DEFAULT_VIEW_OPTIONS);
    expect(() => saveViewOptions(DEFAULT_VIEW_OPTIONS, broken)).not.toThrow();
    expect(loadViewOptions(undefined)).toEqual(DEFAULT_VIEW_OPTIONS);
  });
});

describe("搜索会话", () => {
  const workspaces = [
    { id: "ws-1", name: "我的工作区" },
    { id: "ws-2", name: "Sales North" },
  ];
  const sessions = [
    summary("周报", "ws-1", { title: "第 44 周经营周报", updatedAt: "2026-10-01T10:00:00Z" }),
    summary("合同", "ws-2", { title: "Contract review", updatedAt: "2026-10-03T10:00:00Z" }),
    summary("闲聊", "ws-2", { title: "随便聊聊", updatedAt: "2026-10-02T10:00:00Z", archived: true }),
  ];

  test("按标题子串过滤，不区分大小写", () => {
    expect(searchSessions(sessions, workspaces, "CONTRACT").map((h) => h.session.id)).toEqual(["合同"]);
    expect(searchSessions(sessions, workspaces, "周报").map((h) => h.session.id)).toEqual(["周报"]);
  });

  test("按所属工作区名称过滤，命中该工作区的全部会话，最近更新的在前", () => {
    expect(searchSessions(sessions, workspaces, "sales").map((h) => h.session.id)).toEqual(["合同", "闲聊"]);
  });

  test("每个结果带着工作区名；已归档的也能搜到", () => {
    const hits = searchSessions(sessions, workspaces, "聊聊");
    expect(hits).toHaveLength(1);
    expect(hits[0]).toMatchObject({ workspaceName: "Sales North" });
    expect(hits[0]?.session.archived).toBe(true);
  });

  test("关键字为空或只有空白时不搜，前后空白忽略", () => {
    expect(searchSessions(sessions, workspaces, "")).toEqual([]);
    expect(searchSessions(sessions, workspaces, "   ")).toEqual([]);
    expect(searchSessions(sessions, workspaces, "  contract ").map((h) => h.session.id)).toEqual(["合同"]);
  });

  test("所属工作区已取消登记：用会话自带的名字，都没有写未归属", () => {
    expect(workspaceNameOf(summary("a", "ws-gone", { workspaceName: "华南区" }), workspaces)).toBe("华南区");
    expect(workspaceNameOf(summary("b", "", { workspaceName: "" }), workspaces)).toBe("未归属");
    expect(searchSessions([summary("b", "", { title: "孤儿" })], workspaces, "未归属").map((h) => h.session.id)).toEqual(["b"]);
  });
});
