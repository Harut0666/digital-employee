// 工作区接口（Go 后端 internal/app/host.go）：列出、新建、切换。TS 后端没有这些路由，直接用 fetch。
// 新建和切换返回结果对象：被拒（重名、只读、有任务在跑）也是结果，不抛异常；读取列表出错时抛异常。

import type { FetchLike } from "./sessions";

export interface WorkspaceItem {
  id: string;
  name: string;
  path: string;
}

export interface WorkspaceList {
  /** 当前工作区的 id */
  current: string;
  /** 环境变量 DE_WORKSPACE 指定了工作区：不能新建或切换 */
  readOnly: boolean;
  reason?: string;
  workspaces: WorkspaceItem[];
}

export type WorkspaceRefusal = { ok: false; error: string; message: string; taskIds?: string[] };
export type CreateWorkspaceResult = { ok: true; workspace: WorkspaceItem } | WorkspaceRefusal;
/** reload：切换已生效，界面要整页重新加载 */
export type SwitchWorkspaceResult = { ok: true; current: WorkspaceItem; reload: boolean } | WorkspaceRefusal;

export interface WorkspaceApi {
  list(): Promise<WorkspaceList>;
  create(name: string): Promise<CreateWorkspaceResult>;
  switchTo(id: string): Promise<SwitchWorkspaceResult>;
}

type Json = Record<string, unknown>;

const messageOf = (body: unknown, status: number) =>
  typeof body === "object" && body !== null && typeof (body as Json).message === "string" ? ((body as Json).message as string) : `请求失败（${status}）`;

export function createWorkspaceApi(origin: string = window.location.origin, fetchImpl: FetchLike = (input, init) => fetch(input, init)): WorkspaceApi {
  const url = (path = "") => `${origin}/api/workspaces${path}`;

  async function act<T>(target: string, body: Json): Promise<T | WorkspaceRefusal> {
    const res = await fetchImpl(target, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) });
    const parsed: unknown = await res.json().catch(() => undefined);
    if (typeof parsed === "object" && parsed !== null && "ok" in parsed) return parsed as T | WorkspaceRefusal;
    return { ok: false, error: "internal", message: messageOf(parsed, res.status) };
  }

  return {
    async list() {
      const res = await fetchImpl(url());
      const body: unknown = await res.json().catch(() => undefined);
      if (!res.ok) throw new Error(messageOf(body, res.status));
      return body as WorkspaceList;
    },
    create: (name) => act<CreateWorkspaceResult>(url(), { name }),
    switchTo: (id) => act<SwitchWorkspaceResult>(url(`/${encodeURIComponent(id)}/switch`), {}),
  };
}

/** 模拟模式：内存里的两个工作区，切换只改当前项，不重新加载。 */
export function createMockWorkspaceApi(): WorkspaceApi {
  const base = "C:\\Users\\demo\\AppData\\Roaming";
  const state: WorkspaceList = {
    current: "ws-demo",
    readOnly: false,
    workspaces: [{ id: "ws-demo", name: "digital-employee", path: `${base}\\digital-employee` }],
  };
  let seq = 0;
  return {
    list: async () => structuredClone(state),
    async create(name) {
      const trimmed = name.trim();
      if (!trimmed) return { ok: false, error: "invalid_input", message: "工作区名称不能为空，最多 40 个字" };
      if (state.workspaces.some((w) => w.name === trimmed)) return { ok: false, error: "invalid_input", message: `已有名为“${trimmed}”的工作区` };
      const workspace = { id: `ws-mock-${++seq}`, name: trimmed, path: `${base}\\digital-employee-app\\workspaces\\ws-mock-${seq}` };
      state.workspaces.push(workspace);
      return { ok: true, workspace };
    },
    async switchTo(id) {
      const target = state.workspaces.find((w) => w.id === id);
      if (!target) return { ok: false, error: "not_found", message: `工作区不存在：${id}` };
      state.current = id;
      return { ok: true, current: target, reload: false };
    },
  };
}
