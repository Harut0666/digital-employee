// 会话接口（Go 后端 internal/server/sessions.go）。TS 后端没有这些路由，Eden 推不出类型，直接用 fetch。
// 人的操作（发消息、确认员工、上传、提交）返回 SessionResult：业务错误也是结果，不抛异常，
// 这样提交前检查不通过时界面能拿到设置入口。读取类接口出错时抛异常。

import type { PreflightResult, SessionAgentEvent, SessionDetail, SessionResult, SessionSummary, StopResult, TurnAccepted, UiRole } from "./types";

export interface SessionApi {
  listSessions(workspaceId?: string): Promise<SessionSummary[]>;
  getSession(id: string): Promise<SessionDetail>;
  /** 在某个工作区里开新会话；不给 workspaceId 时服务端取唯一的工作区（单目录模式） */
  createSession(workspaceId?: string): Promise<SessionSummary>;
  /**
   * 发一句话：接口立即受理（202），回复在会话事件流里逐字出现。
   * 正在回复时再发的话排进同一轮，在下一步之前送达。
   */
  sendMessage(id: string, text: string): Promise<TurnAccepted | Exclude<SessionResult, { ok: true }>>;
  /** 停止这一轮：说过的部分以“已中断”保存，没开始的工具调用记“已取消” */
  stopTurn(id: string): Promise<StopResult>;
  /** 订阅一个会话的事件流；返回取消订阅的函数 */
  subscribeSession(id: string, onEvent: (event: SessionAgentEvent) => void): () => void;
  confirmEmployee(id: string, employee: string): Promise<SessionResult>;
  uploadAttachments(id: string, files: File[]): Promise<SessionResult>;
  /** form 省略时用草稿 */
  submitSession(id: string, form?: Record<string, unknown>): Promise<SessionResult>;
  /** 发起人在开工清单上确认跳过一项可跳过项 */
  skipChecklistItem(id: string, item: string): Promise<SessionResult>;
  /** 入库卡片：请模型起草描述；模型不可用时结果里是一条说明，由人手写 */
  draftFiling(id: string): Promise<SessionResult>;
  /** 提交前检查：默认模型连接有没有可用 Key */
  preflight(): Promise<PreflightResult>;
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

  async function post<T>(target: string, body: BodyInit, json = true): Promise<T | Exclude<SessionResult, { ok: true }>> {
    const headers: Record<string, string> = { "x-role": getRole() };
    if (json) headers["content-type"] = "application/json";
    const res = await fetchImpl(target, { method: "POST", headers, body });
    const parsed: unknown = await res.json().catch(() => undefined);
    if (typeof parsed === "object" && parsed !== null && "ok" in parsed) return parsed as T | Exclude<SessionResult, { ok: true }>;
    return { ok: false, error: "internal", message: messageOf(parsed, res.status) };
  }

  const act = (target: string, body: BodyInit, json = true) => post<SessionResult>(target, body, json);

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
    sendMessage: (id, text) => post<TurnAccepted>(session(id, "/messages"), JSON.stringify({ text })),
    stopTurn: (id) => post<StopResult>(session(id, "/stop"), JSON.stringify({})),
    confirmEmployee: (id, employee) => act(session(id, "/employee"), JSON.stringify({ employee })),
    uploadAttachments(id, files) {
      const form = new FormData();
      for (const file of files) form.append("attachments", file, file.name);
      return act(session(id, "/attachments"), form, false);
    },
    submitSession: (id, form) => act(session(id, "/submit"), JSON.stringify(form === undefined ? {} : { form })),
    skipChecklistItem: (id, item) => act(session(id, `/checklist/${encodeURIComponent(item)}/skip`), JSON.stringify({})),
    draftFiling: (id) => act(session(id, "/filing-draft"), JSON.stringify({})),
    preflight: () => read<PreflightResult>(url("preflight")),
    subscribeSession(id, onEvent) {
      // 服务端每件事都带 event 名；EventSource 带不了请求头，但事件流是只读的，不依赖角色
      const source = new EventSource(session(id, "/stream"));
      const handle = (event: MessageEvent<string>) => {
        try {
          onEvent(JSON.parse(event.data) as SessionAgentEvent);
        } catch {
          /* 忽略不认识的帧 */
        }
      };
      const types = ["turn_start", "text_delta", "tool_start", "tool_end", "message", "turn_end"];
      for (const type of types) source.addEventListener(type, handle as EventListener);
      return () => source.close();
    },
  };
}
