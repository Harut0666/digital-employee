// 会话接口（Go 后端 internal/server/sessions.go）。TS 后端没有这些路由，Eden 推不出类型，直接用 fetch。
// 人的操作（发消息、确认员工、上传、提交）返回 SessionResult：业务错误也是结果，不抛异常，
// 这样提交前检查不通过时界面能拿到设置入口。读取类接口出错时抛异常。

import type { ModelConnectionOption, PreflightResult, SessionDetail, SessionResult, SessionSummary, UiRole } from "./types";

export interface SessionApi {
  listSessions(workspaceId?: string): Promise<SessionSummary[]>;
  getSession(id: string): Promise<SessionDetail>;
  /** 在某个工作区里开新会话；不给 workspaceId 时服务端取唯一的工作区（单目录模式） */
  createSession(workspaceId?: string): Promise<SessionSummary>;
  /** 会话里可以选的模型：只列有 Key 的连接和它们列出的模型 */
  modelOptions(): Promise<ModelConnectionOption[]>;
  /** 选模型：会话日志里写一条选择，同时更新设置的 lastModel；任务运行中从下一个判断步骤生效 */
  selectModel(id: string, connection: string, model: string): Promise<SessionResult>;
  sendMessage(id: string, text: string): Promise<SessionResult>;
  confirmEmployee(id: string, employee: string): Promise<SessionResult>;
  uploadAttachments(id: string, files: File[]): Promise<SessionResult>;
  /** form 省略时用草稿 */
  submitSession(id: string, form?: Record<string, unknown>): Promise<SessionResult>;
  /** 发起人在开工清单上确认跳过一项可跳过项 */
  skipChecklistItem(id: string, item: string): Promise<SessionResult>;
  /** 入库卡片：请模型起草描述；模型不可用时结果里是一条说明，由人手写 */
  draftFiling(id: string): Promise<SessionResult>;
  /** 提交前检查：会话模型所在的连接有没有 Key、模型还在不在列表里；给任务 ID 时检查任务的会话模型 */
  preflight(taskId?: string): Promise<PreflightResult>;
}

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
    listSessions: (workspaceId) =>
      read<SessionSummary[]>(url(workspaceId ? `sessions?workspaceId=${encodeURIComponent(workspaceId)}` : "sessions")),
    getSession: (id) => read<SessionDetail>(session(id)),
    createSession: (workspaceId) =>
      read<SessionSummary>(url("sessions"), {
        method: "POST",
        headers: { "x-role": getRole(), ...(workspaceId ? { "content-type": "application/json" } : {}) },
        ...(workspaceId ? { body: JSON.stringify({ workspaceId }) } : {}),
      }),
    modelOptions: () => read<ModelConnectionOption[]>(url("settings/models")),
    selectModel: (id, connection, model) => act(session(id, "/model"), JSON.stringify({ connection, model }), true, "PUT"),
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
