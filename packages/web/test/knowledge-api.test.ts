import { describe, expect, test } from "bun:test";
import { createKnowledgeApi } from "../src/api/knowledge";
import { createSessionApi, type FetchLike } from "../src/api/sessions";

interface Call {
  method: string;
  path: string;
  headers: Record<string, string>;
  body: unknown;
}

function fakeFetch(routes: Record<string, [number, unknown]>) {
  const calls: Call[] = [];
  const impl: FetchLike = async (url, init) => {
    const path = decodeURIComponent(new URL(url).pathname);
    const method = init?.method ?? "GET";
    calls.push({ method, path, headers: (init?.headers ?? {}) as Record<string, string>, body: init?.body });
    const [status, body] = routes[`${method} ${path}`] ?? [404, { ok: false, error: "not_found", message: "知识库里没有这个条目" }];
    return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
  };
  return { calls, impl };
}

const entry = {
  id: "kb-1",
  title: "经营周报_2026年第44周",
  description: "第 44 周经营周报",
  tags: ["周报"],
  sourceTask: "t-1",
  sourceEmployee: "weekly-report",
  createdAt: "2026-11-07T00:00:00Z",
  fileName: "weekly_report.docx",
};

describe("知识库接口", () => {
  test("入库带上发起人角色和人确认过的描述，成功时返回条目", async () => {
    const { calls, impl } = fakeFetch({ "POST /api/knowledge": [201, entry] });
    const api = createKnowledgeApi("http://local", () => "initiator", impl);
    const res = await api.fileKnowledge({ taskId: "t-1", title: entry.title, description: "第 44 周经营周报", tags: ["周报"] });
    expect(res).toEqual({ ok: true, entry });
    expect(calls[0]?.headers["x-role"]).toBe("initiator");
    expect(JSON.parse(String(calls[0]?.body))).toEqual({ taskId: "t-1", title: entry.title, description: "第 44 周经营周报", tags: ["周报"] });
  });

  test("没有描述、角色不对这类业务错误作为结果返回，不抛异常", async () => {
    const refused = { ok: false as const, error: "invalid_input", message: "请写一句描述：员工先看描述再决定要不要读正文。" };
    const { impl } = fakeFetch({ "POST /api/knowledge": [400, refused] });
    const api = createKnowledgeApi("http://local", () => "initiator", impl);
    expect(await api.fileKnowledge({ taskId: "t-1", description: "" })).toEqual(refused);
  });

  test("浏览、查看、删除", async () => {
    const { calls, impl } = fakeFetch({
      "GET /api/knowledge": [200, [entry]],
      "GET /api/knowledge/kb-1": [200, { ...entry, text: "本周摘要" }],
      "DELETE /api/knowledge/kb-1": [200, { ok: true }],
      "DELETE /api/knowledge/kb-2": [403, { ok: false, error: "forbidden_role", message: "只有发起人能删除知识库条目" }],
    });
    const api = createKnowledgeApi("http://local", () => "initiator", impl);
    expect((await api.listKnowledge()).map((e) => e.id)).toEqual(["kb-1"]);
    expect((await api.readKnowledge("kb-1")).text).toBe("本周摘要");
    await expect(api.readKnowledge("kb-9")).rejects.toThrow("知识库里没有这个条目");
    expect(await api.deleteKnowledge("kb-1")).toEqual({ ok: true });
    expect(await api.deleteKnowledge("kb-2")).toEqual({ ok: false, error: "forbidden_role", message: "只有发起人能删除知识库条目" });
    expect(calls.find((c) => c.method === "DELETE")?.headers["x-role"]).toBe("initiator");
  });

  test("入库卡片请模型起草描述走会话接口", async () => {
    const session = { id: "s-1", status: "closed", readOnly: false, title: "出周报", attachments: [], createdAt: "", updatedAt: "", waitingFor: [] };
    const { calls, impl } = fakeFetch({ "POST /api/sessions/s-1/filing-draft": [200, { ok: true, messages: [], session }] });
    const api = createSessionApi("http://local", () => "initiator", impl);
    const res = await api.draftFiling("s-1");
    expect(res.ok).toBe(true);
    expect(calls[0]?.headers["x-role"]).toBe("initiator");
  });
});
