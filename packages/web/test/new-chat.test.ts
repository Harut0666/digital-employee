import { describe, expect, test } from "bun:test";
import type { SessionSummary } from "../src/api/types";
import { currentWorkspaceOf, newChatEntry, newChatTarget, startWorkspaceId } from "../src/view/new-chat";

const ws = (id: string, name: string, patch: Record<string, unknown> = {}) => ({ id, name, path: `D:\\de\\${name}`, ...patch });
const list = (workspaces: ReturnType<typeof ws>[], patch: Record<string, unknown> = {}) => ({ readOnly: false, workspaces, ...patch });

describe("新对话入口：点“+ 新对话”之后怎么走", () => {
  test("还没读到工作区列表：直接开，工作区交给服务端取唯一的那个", () => {
    expect(newChatEntry(undefined)).toEqual({ kind: "direct", workspaceId: undefined });
  });

  test("只有一个工作区：直接在它下面开", () => {
    expect(newChatEntry(list([ws("ws-1", "华东区")]))).toEqual({ kind: "direct", workspaceId: "ws-1" });
  });

  test("多个工作区：让人选，默认是当前会话所在的工作区", () => {
    const entry = newChatEntry(list([ws("ws-1", "华东区"), ws("ws-2", "华南区")]), "ws-2");
    expect(entry.kind).toBe("choose");
    if (entry.kind !== "choose") return;
    expect(entry.defaultId).toBe("ws-2");
    expect(entry.options.map((o) => [o.id, o.name, o.disabled])).toEqual([
      ["ws-1", "华东区", false],
      ["ws-2", "华南区", false],
    ]);
  });

  test("多个工作区且没有当前会话：默认第一个", () => {
    const entry = newChatEntry(list([ws("ws-1", "华东区"), ws("ws-2", "华南区")]));
    expect(entry).toMatchObject({ kind: "choose", defaultId: "ws-1" });
  });

  test("目录缺失的工作区不能开新对话：不当默认，选项置灰", () => {
    const entry = newChatEntry(list([ws("ws-1", "华东区", { missing: true }), ws("ws-2", "华南区"), ws("ws-3", "华北区")]), "ws-1");
    expect(entry.kind).toBe("choose");
    if (entry.kind !== "choose") return;
    expect(entry.defaultId).toBe("ws-2");
    expect(entry.options.find((o) => o.id === "ws-1")?.disabled).toBe(true);
  });

  test("只剩一个目录没缺的工作区：直接在它下面开", () => {
    expect(newChatEntry(list([ws("ws-1", "华东区", { missing: true }), ws("ws-2", "华南区")]))).toEqual({ kind: "direct", workspaceId: "ws-2" });
  });

  test("一个工作区都没有（只剩“未归属”）：先引导添加工作区", () => {
    expect(newChatEntry(list([]))).toEqual({ kind: "add-workspace", reason: "empty", canAdd: true });
  });

  test("工作区目录全都缺失：也要先添加（或修复）工作区", () => {
    expect(newChatEntry(list([ws("ws-1", "华东区", { missing: true })]))).toEqual({ kind: "add-workspace", reason: "missing", canAdd: true });
  });

  test("单目录模式不能添加：入口带上原因", () => {
    const entry = newChatEntry(list([], { readOnly: true, reason: "工作区由环境变量 DE_WORKSPACE 指定" }));
    expect(entry).toEqual({ kind: "add-workspace", reason: "empty", canAdd: false, hint: "工作区由环境变量 DE_WORKSPACE 指定" });
  });
});

describe("新对话开在哪个工作区", () => {
  const two = list([ws("ws-1", "华东区"), ws("ws-2", "华南区")]);

  test("指定了可用的工作区就用它", () => {
    expect(startWorkspaceId(two, "ws-2")).toBe("ws-2");
  });

  test("没指定时：一个工作区取它，多个取默认，没有则交给服务端", () => {
    expect(startWorkspaceId(list([ws("ws-1", "华东区")]))).toBe("ws-1");
    expect(startWorkspaceId(two)).toBe("ws-1");
    expect(startWorkspaceId(list([]))).toBeUndefined();
    expect(startWorkspaceId(undefined)).toBeUndefined();
  });

  test("指定的工作区已经不存在时不硬用", () => {
    expect(startWorkspaceId(two, "ws-gone")).toBe("ws-1");
  });
});

describe("新对话页上显示的目标工作区", () => {
  test("只有一个工作区：显示它，不给更换", () => {
    expect(newChatTarget(list([ws("ws-1", "华东区")]))).toEqual({ current: { id: "ws-1", name: "华东区" }, choices: [] });
  });

  test("多个工作区：显示选中的，可更换成别的可用工作区", () => {
    const target = newChatTarget(list([ws("ws-1", "华东区"), ws("ws-2", "华南区"), ws("ws-3", "华北区", { missing: true })]), "ws-2");
    expect(target.current).toEqual({ id: "ws-2", name: "华南区" });
    expect(target.choices.map((c) => [c.id, c.disabled])).toEqual([
      ["ws-1", false],
      ["ws-2", false],
      ["ws-3", true],
    ]);
  });

  test("没有可用的工作区：被拦住，带添加工作区的说明", () => {
    expect(newChatTarget(list([])).blocked).toEqual({ kind: "add-workspace", reason: "empty", canAdd: true });
    expect(newChatTarget(list([])).current).toBeUndefined();
  });

  test("还没读到列表：不拦也不显示目标", () => {
    expect(newChatTarget(undefined)).toEqual({ choices: [] });
  });
});

describe("当前会话所在的工作区", () => {
  const session = (id: string, workspaceId: string) => ({ id, workspaceId }) as SessionSummary;
  const l = {
    readOnly: false,
    workspaces: [{ ...ws("ws-1", "华东区"), sessions: [session("a", "ws-1")] }, { ...ws("ws-2", "华南区"), sessions: [session("b", "ws-2")] }],
    unassigned: [session("c", "ws-gone")],
  };

  test("按会话摘要查；新对话页带着的工作区也算", () => {
    expect(currentWorkspaceOf(l, { kind: "session", id: "b" })).toBe("ws-2");
    expect(currentWorkspaceOf(l, { kind: "new", workspaceId: "ws-1" })).toBe("ws-1");
    expect(currentWorkspaceOf(l, { kind: "session", id: "c" })).toBe("ws-gone");
  });

  test("设置等页面、没读到的会话没有当前工作区", () => {
    expect(currentWorkspaceOf(l, { kind: "page", page: "settings" })).toBeUndefined();
    expect(currentWorkspaceOf(l, { kind: "session", id: "zzz" })).toBeUndefined();
    expect(currentWorkspaceOf(undefined, { kind: "session", id: "a" })).toBeUndefined();
  });
});
