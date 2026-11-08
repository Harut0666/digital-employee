// 会话接口（Go 后端 internal/server/sessions.go）。TS 后端没有这些路由，Eden 推不出类型，直接用 fetch。
// 人的操作（发消息、确认员工、上传、提交）返回 SessionResult：业务错误也是结果，不抛异常，
// 这样提交前检查不通过时界面能拿到设置入口。读取类接口出错时抛异常。


import type { ModelConnectionOption, PreflightResult, SessionApi, SessionDetail, SessionResult, SessionSummary, UiRole } from "./types";

export type { SessionApi };

type Json = Record<string, unknown>;

/** 只用到 fetch 的调用签名，测试可以传假的 */
export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

const messageOf = (body: unknown, status: number) =>
  typeof body === "object" && body !== null && typeof (body as Json).message === "string" ? ((body as Json).message as string) : `请求失败（${status}）`;

export function createSessionApi(origin: string, getRole: () => UiRole, fetchImpl: FetchLike = (input, init) => fetch(input, init)): SessionApi {
  const url = (path: string) => `${origin}/api/${path}`;
  const session = (id: string, rest = "") => url(`sessions/${encodeURIComponent(id)}${rest}`);

  async function read<T>(target: string, init?: RequestInit): Promise<T> {
    const res = await fetchImpl(target, init);
    const body: unknown = await res.json().catch(() => undefined);
    if (!res.ok) throw new Error(messageOf(body, res.status));
    return body as T;
  }

  async function act(target: string, body: BodyInit, json = true, method = "POST"): Promise<SessionResult> {
    const headers: Record<string, string> = { "x-role": getRole() };
    if (json) headers["content-type"] = "application/json";
    const res = await fetchImpl(target, { method, headers, body });
    const parsed: unknown = await res.json().catch(() => undefined);
    if (typeof parsed === "object" && parsed !== null && "ok" in parsed) return parsed as SessionResult;
    return { ok: false, error: "internal", message: messageOf(parsed, res.status) };
  }

  return {
    listSessions: (workspaceId, options) => {
      const query = new URLSearchParams();
      if (workspaceId) query.set("workspaceId", workspaceId);
      if (options?.archived) query.set("archived", "1");
      const suffix = query.size > 0 ? `?${query.toString()}` : "";
      return read<SessionSummary[]>(url(`sessions${suffix}`));
    },
    getSession: (id) => read<SessionDetail>(session(id)),
    createSession: (workspaceId) =>
      read<SessionSummary>(url("sessions"), {
        method: "POST",
        headers: { "x-role": getRole(), ...(workspaceId ? { "content-type": "application/json" } : {}) },
        ...(workspaceId ? { body: JSON.stringify({ workspaceId }) } : {}),
      }),
    modelOptions: () => read<ModelConnectionOption[]>(url("settings/models")),
    selectModel: (id, connection, model) => act(session(id, "/model"), JSON.stringify({ connection, model }), true, "PUT"),
    async updateSession(id, patch) {
      // 服务端返回 {ok, session, statusDot}：取里面的会话摘要
      const body = await read<{ session?: SessionSummary }>(session(id), {
        method: "PATCH",
        headers: { "content-type": "application/json", "x-role": getRole() },
        body: JSON.stringify(patch),
      });
      if (!body.session) throw new Error("响应里没有会话");
      return body.session;
    },
    sendMessage: (id, text) => act(session(id, "/messages"), JSON.stringify({ text })),
    confirmEmployee: (id, employee) => act(session(id, "/employee"), JSON.stringify({ employee })),
    uploadAttachments(id, files) {
      const form = new FormData();
      for (const file of files) form.append("attachments", file, file.name);
      return act(session(id, "/attachments"), form, false);
    },
    submitSession: (id, form) => act(session(id, "/submit"), JSON.stringify(form === undefined ? {} : { form })),
    skipChecklistItem: (id, item) => act(session(id, `/checklist/${encodeURIComponent(item)}/skip`), JSON.stringify({})),
    draftFiling: (id) => act(session(id, "/filing-draft"), JSON.stringify({})),
    preflight: (taskId) => read<PreflightResult>(url(taskId ? `preflight?taskId=${encodeURIComponent(taskId)}` : "preflight")),
  };
}
