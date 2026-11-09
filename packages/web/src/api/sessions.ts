// 会话接口（Go 后端 internal/server/sessions.go）。TS 后端没有这些路由，Eden 推不出类型，直接用 fetch。
// 人的操作（发消息、确认员工、上传、提交）返回 SessionResult：业务错误也是结果，不抛异常，
// 这样提交前检查不通过时界面能拿到设置入口。读取类接口出错时抛异常。

import type {
  EmployeeFormAnswer,
  FolderListing,
  ModelConnectionOption,
  ModelRef,
  PermissionDecision,
  PermissionMode,
  PreflightResult,
  SessionAgentEvent,
  SessionDetail,
  SessionResult,
  SessionSummary,
  StopResult,
  TurnAccepted,
  UiRole,
} from "./types";

export interface SessionApi {
  listSessions(workspaceId?: string, options?: { archived?: boolean }): Promise<SessionSummary[]>;
  getSession(id: string): Promise<SessionDetail>;
  /**
   * 在某个工作区里开新会话；不给 workspaceId 时服务端取唯一的工作区（单目录模式）。
   * model 是新对话页选中（T45）：从第一句话起用哪个模型；不给时沿用设置的 lastModel。
   */
  createSession(workspaceId?: string, model?: ModelRef): Promise<SessionSummary>;
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
  /** 目录里的材料：列出可用的文件、请人确认之后作为材料 */
  listFolderFiles(id: string): Promise<FolderListing>;
  useFolderFiles(id: string, paths: string[]): Promise<SessionResult>;
  /** form 省略时用草稿 */
  submitSession(id: string, form?: Record<string, unknown>): Promise<SessionResult>;
  /** 发起人在开工清单上确认跳过一项可跳过项 */
  skipChecklistItem(id: string, item: string): Promise<SessionResult>;
  /** 入库卡片：请模型起草描述；模型不可用时结果里是一条说明，由人手写。taskId 是要入库的任务，不给就是最新的 */
  draftFiling(id: string, taskId?: string): Promise<SessionResult>;
  /** 会话模型：可选的有 Key 的连接和模型、在会话里选模型 */
  modelOptions(): Promise<ModelConnectionOption[]>;
  selectModel(id: string, connection: string, model: string): Promise<SessionResult>;
  /** 权限模式（T48）：发起人在输入框左下角选；每个会话各自记住，新会话总是询问 */
  setPermissionMode(id: string, mode: PermissionMode): Promise<SessionResult>;
  /** 员工表单卡上的答复（T49）：确认开工（带人改过的字段、材料、确认跳过的项）或“不是这个员工”；只有发起人能答 */
  answerEmployeeForm(id: string, requestId: string, answer: EmployeeFormAnswer): Promise<SessionResult>;
  /** 欢迎页“载入公开样例”（T49）：把样例的输入预填成一张员工表单，人确认即开工 */
  loadSampleForm(id: string): Promise<SessionResult>;
  /** 权限确认卡上的答复：允许一次、本会话总是允许这一类、拒绝；只有发起人能答 */
  decidePermission(id: string, requestId: string, decision: PermissionDecision): Promise<SessionResult>;
  /** 改会话的标题、归档 */
  updateSession(id: string, patch: { title?: string; archived?: boolean }): Promise<SessionSummary>;
  /** 提交前检查：默认模型连接有没有可用 Key；给了任务就用任务的会话模型 */
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

  /** 人的操作：业务错误也是结果（不抛异常）；发消息、停止这些另有返回形状的也走它 */
  async function post<T>(target: string, body: BodyInit, json = true, method = "POST"): Promise<T | Exclude<SessionResult, { ok: true }>> {
    const headers: Record<string, string> = { "x-role": getRole() };
    if (json) headers["content-type"] = "application/json";
    const res = await fetchImpl(target, { method, headers, body });
    const parsed: unknown = await res.json().catch(() => undefined);
    if (typeof parsed === "object" && parsed !== null && "ok" in parsed) return parsed as T | Exclude<SessionResult, { ok: true }>;
    return { ok: false, error: "internal", message: messageOf(parsed, res.status) };
  }

  const act = (target: string, body: BodyInit, json = true, method = "POST") => post<SessionResult>(target, body, json, method);

  return {
    listSessions: (workspaceId, options) => {
      const query = new URLSearchParams();
      if (workspaceId) query.set("workspaceId", workspaceId);
      if (options?.archived) query.set("archived", "1");
      const suffix = query.size > 0 ? `?${query.toString()}` : "";
      return read<SessionSummary[]>(url(`sessions${suffix}`));
    },
    getSession: (id) => read<SessionDetail>(session(id)),
    createSession: (workspaceId, model) => {
      const body: Json = {};
      if (workspaceId) body.workspaceId = workspaceId;
      if (model) body.model = { connection: model.connection, model: model.model };
      return read<SessionSummary>(url("sessions"), {
        method: "POST",
        headers: { "x-role": getRole(), ...(workspaceId || model ? { "content-type": "application/json" } : {}) },
        ...(workspaceId || model ? { body: JSON.stringify(body) } : {}),
      });
    },
    modelOptions: () => read<ModelConnectionOption[]>(url("settings/models")),
    selectModel: (id, connection, model) => act(session(id, "/model"), JSON.stringify({ connection, model }), true, "PUT"),
    setPermissionMode: (id, mode) => act(session(id, "/permission-mode"), JSON.stringify({ mode }), true, "PUT"),
    answerEmployeeForm: (id, requestId, answer) => act(session(id, `/employee-forms/${encodeURIComponent(requestId)}`), JSON.stringify(answer)),
    loadSampleForm: (id) => act(session(id, "/sample"), JSON.stringify({})),
    decidePermission: (id, requestId, decision) => act(session(id, `/permissions/${encodeURIComponent(requestId)}`), JSON.stringify({ decision })),
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
    sendMessage: (id, text) => post<TurnAccepted>(session(id, "/messages"), JSON.stringify({ text })),
    stopTurn: (id) => post<StopResult>(session(id, "/stop"), JSON.stringify({})),
    confirmEmployee: (id, employee) => act(session(id, "/employee"), JSON.stringify({ employee })),
    uploadAttachments(id, files) {
      const form = new FormData();
      for (const file of files) form.append("attachments", file, file.name);
      return act(session(id, "/attachments"), form, false);
    },
    listFolderFiles: (id) => read<FolderListing>(session(id, "/folder-files"), { headers: { "x-role": getRole() } }),
    useFolderFiles: (id, paths) => act(session(id, "/folder-files"), JSON.stringify({ paths })),
    submitSession: (id, form) => act(session(id, "/submit"), JSON.stringify(form === undefined ? {} : { form })),
    skipChecklistItem: (id, item) => act(session(id, `/checklist/${encodeURIComponent(item)}/skip`), JSON.stringify({})),
    draftFiling: (id, taskId) => act(session(id, "/filing-draft"), JSON.stringify(taskId ? { taskId } : {})),
    preflight: (taskId) => read<PreflightResult>(url(taskId ? `preflight?taskId=${encodeURIComponent(taskId)}` : "preflight")),
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
