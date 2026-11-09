import { describe, expect, test } from "bun:test";
import { createWorkspaceApi, type RecordGroupView } from "../src/api/workspaces";
import type { FetchLike } from "../src/api/sessions";

/** 假的 fetch：记下请求，按“方法 路径”回固定响应 */
function fakeFetch(routes: Record<string, [number, unknown]>) {
  const calls: { method: string; path: string; body: unknown; headers?: HeadersInit }[] = [];
  const impl: FetchLike = async (url, init) => {
    const method = init?.method ?? "GET";
    const path = new URL(url).pathname;
    calls.push({ method, path, body: init?.body, headers: init?.headers });
    const [status, body] = routes[`${method} ${path}`] ?? [404, { ok: false, error: "not_found", message: "路径不存在" }];
    return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
  };
  return { calls, impl };
}

const huadong = { id: "ws-1", name: "华东区", path: "D:\\de\\华东区" };
const huanan = { id: "ws-2", name: "华南区", path: "D:\\de\\华南区" };

describe("工作区接口（Spec 0006：没有切换）", () => {
  test("新建工作区带名称和目录，返回新建的工作区", async () => {
    const { calls, impl } = fakeFetch({ "POST /api/workspaces": [201, { ok: true, workspace: huanan }] });
    const api = createWorkspaceApi("http://local", impl);
    const res = await api.create("华南区", "D:\\de\\华南区");
    expect(res).toEqual({ ok: true, workspace: huanan });
    expect(JSON.parse(String(calls[0]?.body))).toEqual({ name: "华南区", path: "D:\\de\\华南区" });
  });

  test("目录已经是工作区时被拒，结果里带原因而不是抛错", async () => {
    const { impl } = fakeFetch({ "POST /api/workspaces": [409, { ok: false, error: "invalid_input", message: "这个目录已经是工作区“华东区”" }] });
    const res = await createWorkspaceApi("http://local", impl).create("华南区", "D:\\de\\华东区");
    expect(res).toEqual({ ok: false, error: "invalid_input", message: "这个目录已经是工作区“华东区”" });
  });

  test("重命名和取消登记", async () => {
    const { calls, impl } = fakeFetch({
      "PATCH /api/workspaces/ws-2": [200, { ok: true, workspace: { ...huanan, name: "南大区" } }],
      "DELETE /api/workspaces/ws-2": [200, { ok: true, id: "ws-2" }],
    });
    const api = createWorkspaceApi("http://local", impl);
    expect(await api.rename("ws-2", "南大区")).toEqual({ ok: true, workspace: { ...huanan, name: "南大区" } });
    expect(await api.unregister("ws-2")).toEqual({ ok: true, id: "ws-2" });
    expect(calls[0]).toMatchObject({ method: "PATCH", body: JSON.stringify({ name: "南大区" }) });
    expect(calls[1]?.method).toBe("DELETE");
  });

  test("工作区列表带会话摘要和未归属；记录按状态分组；在资源管理器中打开", async () => {
    const sessions = [{ id: "s-1", workspaceId: "ws-1", statusDot: "waiting", unread: false, archived: false }];
    const groups: RecordGroupView[] = [{ key: "running", label: "进行中", items: [] }];
    const { calls, impl } = fakeFetch({
      "GET /api/workspaces": [200, { readOnly: false, workspaces: [{ ...huadong, sessions }], unassigned: [] }],
      "GET /api/records": [200, { groups }],
      "POST /api/workspaces/ws-1/open": [200, { ok: true, path: "D:\\\\de\\\\华东区" }],
    });
    const api = createWorkspaceApi("http://local", impl);
    const list = await api.list();
    expect(list.workspaces[0]?.sessions?.[0]?.statusDot).toBe("waiting");
    expect((await api.records()).groups).toEqual(groups);
    expect(await api.open("ws-1")).toEqual({ ok: true, path: "D:\\\\de\\\\华东区" });
    await api.list({ archived: true });
    expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual([
      "GET /api/workspaces",
      "GET /api/records",
      "POST /api/workspaces/ws-1/open",
      "GET /api/workspaces",
    ]);
  });

  test("选目录：选中、取消、失败都是结果，不抛错；请求体可空", async () => {
    const picked = fakeFetch({ "POST /api/workspaces/pick-folder": [200, { ok: true, path: "D:\\de\\华南区" }] });
    expect(await createWorkspaceApi("http://local", picked.impl).pickFolder()).toEqual({ ok: true, path: "D:\\de\\华南区" });
    expect(picked.calls[0]).toMatchObject({ method: "POST", path: "/api/workspaces/pick-folder" });
    // 只有发起人能选目录：请求头带身份
    expect((picked.calls[0]?.headers as Record<string, string>)["x-role"]).toBe("initiator");

    const cancelled = fakeFetch({ "POST /api/workspaces/pick-folder": [200, { ok: true, cancelled: true }] });
    expect(await createWorkspaceApi("http://local", cancelled.impl).pickFolder()).toEqual({ ok: true, cancelled: true });

    const failed = fakeFetch({ "POST /api/workspaces/pick-folder": [409, { ok: false, error: "workspace_read_only", message: "工作区由环境变量指定" }] });
    expect(await createWorkspaceApi("http://local", failed.impl).pickFolder()).toEqual({ ok: false, error: "workspace_read_only", message: "工作区由环境变量指定" });

    const missing = fakeFetch({});
    const res = await createWorkspaceApi("http://local", missing.impl).pickFolder();
    expect(res.ok).toBe(false);
  });

  test("读取列表失败时抛错；单目录模式带只读原因", async () => {
    const { impl } = fakeFetch({ "GET /api/workspaces": [500, { ok: false, error: "internal", message: "读取工作区登记失败" }] });
    await expect(createWorkspaceApi("http://local", impl).list()).rejects.toThrow("读取工作区登记失败");
    const ok = fakeFetch({ "GET /api/workspaces": [200, { readOnly: true, reason: "工作区由环境变量 DE_WORKSPACE 指定", workspaces: [huadong] }] });
    const list = await createWorkspaceApi("http://local", ok.impl).list();
    expect(list.readOnly).toBe(true);
    expect(list.reason).toBe("工作区由环境变量 DE_WORKSPACE 指定");
  });
});
