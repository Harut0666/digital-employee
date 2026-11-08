import { describe, expect, test } from "bun:test";
import { createSessionApi, type FetchLike } from "../src/api/sessions";

interface Call {
  url: string;
  method: string;
  headers: Record<string, string>;
  body: unknown;
}

/** 假的 fetch：记下请求，按路径回固定响应 */
function fakeFetch(routes: Record<string, [number, unknown]>) {
  const calls: Call[] = [];
  const impl: FetchLike = async (url, init) => {
    const path = new URL(url).pathname;
    calls.push({ url, method: init?.method ?? "GET", headers: (init?.headers ?? {}) as Record<string, string>, body: init?.body });
    const [status, body] = routes[`${init?.method ?? "GET"} ${path}`] ?? [404, { ok: false, error: "not_found", message: "没有" }];
    return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
  };
  return { calls, impl };
}

const session = { id: "s-1", workspaceId: "ws-1", status: "drafting", readOnly: false, title: "出周报", attachments: [], createdAt: "", updatedAt: "", waitingFor: [] };

describe("会话接口", () => {
  test("发消息带上当前角色，返回这次新写入的消息", async () => {
    const { calls, impl } = fakeFetch({
      "POST /api/sessions/s-1/messages": [200, { ok: true, messages: [{ id: "s2", seq: 2, ts: "", kind: "assistant", text: "好的" }], session }],
    });
    const api = createSessionApi("http://local", () => "reviewer", impl);
    const res = await api.sendMessage("s-1", "摘要太长");
    expect(res.ok && res.messages.map((m) => m.text)).toEqual(["好的"]);
    expect(calls[0]?.headers["x-role"]).toBe("reviewer");
    expect(JSON.parse(String(calls[0]?.body))).toEqual({ text: "摘要太长" });
  });

  test("没配 Key 时提交被拒，结果里带设置入口而不是抛错", async () => {
    const refused = { ok: false as const, error: "model_not_configured", message: "默认模型连接「默认」还没有配置 Key", settings: { section: "models", label: "去设置模型连接" } };
    const { impl } = fakeFetch({ "POST /api/sessions/s-1/submit": [412, refused] });
    const api = createSessionApi("http://local", () => "initiator", impl);
    const res = await api.submitSession("s-1", { period: { year: 2026, week: 44 } });
    expect(res).toEqual(refused);
  });

  test("会话忙、越权这类业务错误也作为结果返回", async () => {
    const { impl } = fakeFetch({ "POST /api/sessions/s-1/employee": [403, { ok: false, error: "forbidden_role", message: "只有发起人可以确认员工" }] });
    const api = createSessionApi("http://local", () => "reviewer", impl);
    expect(await api.confirmEmployee("s-1", "weekly-report")).toEqual({ ok: false, error: "forbidden_role", message: "只有发起人可以确认员工" });
  });

  test("确认跳过清单项发给本项的跳过接口，带上当前角色", async () => {
    const { calls, impl } = fakeFetch({ "POST /api/sessions/s-1/checklist/minutes/skip": [200, { ok: true, messages: [], session }] });
    const api = createSessionApi("http://local", () => "initiator", impl);
    expect((await api.skipChecklistItem("s-1", "minutes")).ok).toBe(true);
    expect(calls[0]?.headers["x-role"]).toBe("initiator");
  });

  test("新建会话带 workspaceId（Spec 0006）；不给时服务端取唯一的工作区", async () => {
    const { calls, impl } = fakeFetch({ "POST /api/sessions": [201, session] });
    const api = createSessionApi("http://local", () => "initiator", impl);
    expect((await api.createSession("ws-1")).workspaceId).toBe("ws-1");
    expect(JSON.parse(String(calls[0]?.body))).toEqual({ workspaceId: "ws-1" });
    expect(calls[0]?.headers["x-role"]).toBe("initiator");
    await api.createSession();
    expect(calls[1]?.body).toBeUndefined();
  });

  test("会话列表可以按工作区过滤", async () => {
    const { calls, impl } = fakeFetch({ "GET /api/sessions": [200, [session]] });
    const api = createSessionApi("http://local", () => "initiator", impl);
    expect((await api.listSessions("ws-1"))[0]?.workspaceId).toBe("ws-1");
    expect(calls[0]?.url).toBe("http://local/api/sessions?workspaceId=ws-1");
    await api.listSessions();
    expect(calls[1]?.url).toBe("http://local/api/sessions");
  });

  test("目录文件：列文件、确认后用目录里的文件，路径放在请求体里", async () => {
    const { calls, impl } = fakeFetch({
      "GET /api/sessions/s-1/folder-files": [200, { entries: [{ path: "资料/销售明细.xlsx", name: "销售明细.xlsx", size: 10 }], truncated: false }],
      "POST /api/sessions/s-1/folder-files": [200, { ok: true, messages: [], session }],
    });
    const api = createSessionApi("http://local", () => "initiator", impl);
    expect((await api.listFolderFiles("s-1")).entries[0]).toEqual({ path: "资料/销售明细.xlsx", name: "销售明细.xlsx", size: 10 });
    expect((await api.useFolderFiles("s-1", ["资料/销售明细.xlsx", "资料/周会纪要_2026W44.docx"])).ok).toBe(true);
    expect(JSON.parse(String(calls[1]?.body))).toEqual({ paths: ["资料/销售明细.xlsx", "资料/周会纪要_2026W44.docx"] });
    expect(calls[1]?.headers["x-role"]).toBe("initiator");
  });

  test("会话模型：读可选模型、PUT 选模型（带当前角色），提交前检查可以带任务", async () => {
    const { calls, impl } = fakeFetch({
      "GET /api/settings/models": [200, [{ name: "DeepSeek", models: ["deepseek-chat", "deepseek-reasoner"] }]],
      "PUT /api/sessions/s-1/model": [200, { ok: true, messages: [], session }],
      "GET /api/preflight": [200, { ok: true }],
    });
    const api = createSessionApi("http://local", () => "initiator", impl);
    expect((await api.modelOptions())[0]?.models).toEqual(["deepseek-chat", "deepseek-reasoner"]);
    expect((await api.selectModel("s-1", "DeepSeek", "deepseek-reasoner")).ok).toBe(true);
    expect(calls[1]?.method).toBe("PUT");
    expect(calls[1]?.headers["x-role"]).toBe("initiator");
    expect(JSON.parse(String(calls[1]?.body))).toEqual({ connection: "DeepSeek", model: "deepseek-reasoner" });
    await api.preflight("t-1");
    expect(calls[2]?.url).toBe("http://local/api/preflight?taskId=t-1");
  });

  test("会话列表带上归档开关；PATCH 重命名和归档，返回新的会话摘要", async () => {
    const { calls, impl } = fakeFetch({
      "GET /api/sessions": [200, [session]],
      "PATCH /api/sessions/s-1": [200, { ok: true, session: { ...session, title: "新的标题", archived: true }, statusDot: "idle" }],
    });
    const api = createSessionApi("http://local", () => "initiator", impl);
    await api.listSessions(undefined, { archived: true });
    expect(calls[0]?.url).toBe("http://local/api/sessions?archived=1");
    await api.listSessions("ws-1", { archived: true });
    expect(calls[1]?.url).toBe("http://local/api/sessions?workspaceId=ws-1&archived=1");
    const updated = await api.updateSession("s-1", { title: "新的标题" });
    expect(updated).toMatchObject({ id: "s-1", title: "新的标题", archived: true });
    expect(calls[2]).toMatchObject({ method: "PATCH", body: JSON.stringify({ title: "新的标题" }) });
    expect(calls[2]?.headers["x-role"]).toBe("initiator");
  });

  test("重命名被拒时抛错并带原因", async () => {
    const { impl } = fakeFetch({ "PATCH /api/sessions/s-1": [400, { ok: false, error: "invalid_input", message: "标题不能为空" }] });
    await expect(createSessionApi("http://local", () => "initiator", impl).updateSession("s-1", { title: " " })).rejects.toThrow("标题不能为空");
  });

  test("提交前检查和会话读取", async () => {
    const { impl } = fakeFetch({
      "GET /api/preflight": [200, { ok: true }],
      "GET /api/sessions/s-1": [200, { ...session, messages: [] }],
    });
    const api = createSessionApi("http://local", () => "initiator", impl);
    expect(await api.preflight()).toEqual({ ok: true });
    expect((await api.getSession("s-1")).title).toBe("出周报");
    await expect(api.getSession("s-x")).rejects.toThrow("没有");
  });
});
