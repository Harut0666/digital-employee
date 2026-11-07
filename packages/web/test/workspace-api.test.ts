import { describe, expect, test } from "bun:test";
import { createWorkspaceApi } from "../src/api/workspaces";
import type { FetchLike } from "../src/api/sessions";

/** 假的 fetch：记下请求，按“方法 路径”回固定响应 */
function fakeFetch(routes: Record<string, [number, unknown]>) {
  const calls: { method: string; path: string; body: unknown }[] = [];
  const impl: FetchLike = async (url, init) => {
    const method = init?.method ?? "GET";
    const path = new URL(url).pathname;
    calls.push({ method, path, body: init?.body });
    const [status, body] = routes[`${method} ${path}`] ?? [404, { ok: false, error: "not_found", message: "路径不存在" }];
    return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
  };
  return { calls, impl };
}

const huadong = { id: "ws-1", name: "华东区", path: "D:\\de\\华东区" };
const huanan = { id: "ws-2", name: "华南区", path: "D:\\de\\华南区" };

describe("工作区接口", () => {
  test("新建工作区只带名称，返回新建的工作区", async () => {
    const { calls, impl } = fakeFetch({ "POST /api/workspaces": [201, { ok: true, workspace: huanan }] });
    const api = createWorkspaceApi("http://local", impl);
    const res = await api.create("华南区");
    expect(res).toEqual({ ok: true, workspace: huanan });
    expect(JSON.parse(String(calls[0]?.body))).toEqual({ name: "华南区" });
  });

  test("重名时新建被拒，结果里带原因而不是抛错", async () => {
    const { impl } = fakeFetch({ "POST /api/workspaces": [409, { ok: false, error: "invalid_input", message: "已有名为“华南区”的工作区" }] });
    const res = await createWorkspaceApi("http://local", impl).create("华南区");
    expect(res).toEqual({ ok: false, error: "invalid_input", message: "已有名为“华南区”的工作区" });
  });

  test("有任务在跑时切换被拒，结果里带原因和任务", async () => {
    const busy = { ok: false as const, error: "workspace_busy", message: "有任务正在准入校验、执行中或质检中", taskIds: ["t-1"] };
    const { calls, impl } = fakeFetch({ "POST /api/workspaces/ws-2/switch": [409, busy] });
    const res = await createWorkspaceApi("http://local", impl).switchTo("ws-2");
    expect(res).toEqual(busy);
    expect(calls[0]?.method).toBe("POST");
  });

  test("切换成功时要求重新加载", async () => {
    const { impl } = fakeFetch({ "POST /api/workspaces/ws-2/switch": [200, { ok: true, current: huanan, reload: true }] });
    const res = await createWorkspaceApi("http://local", impl).switchTo("ws-2");
    expect(res).toEqual({ ok: true, current: huanan, reload: true });
  });

  test("读取列表失败时抛错", async () => {
    const { impl } = fakeFetch({ "GET /api/workspaces": [500, { ok: false, error: "internal", message: "读取工作区列表失败" }] });
    await expect(createWorkspaceApi("http://local", impl).list()).rejects.toThrow("读取工作区列表失败");
    const ok = fakeFetch({ "GET /api/workspaces": [200, { current: "ws-1", readOnly: false, workspaces: [huadong, huanan] }] });
    expect((await createWorkspaceApi("http://local", ok.impl).list()).workspaces).toHaveLength(2);
  });
});
