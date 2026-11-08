// 工作区接口（Go 后端 internal/app/host.go）：列出、新建、重命名、取消登记。
// Spec 0006 起工作区只是目录，所有工作区同时可用，不再有“切换”；侧栏按工作区分组是 T38。
// 新建和取消登记返回结果对象：被拒（只读、重名目录）也是结果，不抛异常；读取列表出错时抛异常。

import type { FetchLike } from "./sessions";

export interface WorkspaceItem {
  id: string;
  name: string;
  path: string;
  createdAt?: string;
  updatedAt?: string;
  /** 目录不存在：登记保留，会话仍可查看 */
  missing?: boolean;
  missingText?: string;
}

export interface WorkspaceList {
  /** 单目录模式（环境变量 DE_WORKSPACE 指定了目录）：不能新建、重命名或取消登记 */
  readOnly: boolean;
  reason?: string;
  workspaces: WorkspaceItem[];
}

export type WorkspaceRefusal = { ok: false; error: string; message: string };
export type CreateWorkspaceResult = { ok: true; workspace: WorkspaceItem } | WorkspaceRefusal;
export type UpdateWorkspaceResult = { ok: true; workspace: WorkspaceItem } | WorkspaceRefusal;
export type DeleteWorkspaceResult = { ok: true; id: string } | WorkspaceRefusal;

export interface WorkspaceApi {
  list(): Promise<WorkspaceList>;
  /** path 省略时在应用目录下建一个 */
  create(name: string, path?: string): Promise<CreateWorkspaceResult>;
  rename(id: string, name: string): Promise<UpdateWorkspaceResult>;
  /** 取消登记：只删登记项，目录里的文件和已有记录都保留 */
  unregister(id: string): Promise<DeleteWorkspaceResult>;
}

type Json = Record<string, unknown>;

const messageOf = (body: unknown, status: number) =>
  typeof body === "object" && body !== null && typeof (body as Json).message === "string" ? ((body as Json).message as string) : `请求失败（${status}）`;

export function createWorkspaceApi(origin: string = window.location.origin, fetchImpl: FetchLike = (input, init) => fetch(input, init)): WorkspaceApi {
  const url = (path = "") => `${origin}/api/workspaces${path}`;

  async function act<T>(method: "POST" | "PATCH" | "DELETE", target: string, body: Json = {}): Promise<T | WorkspaceRefusal> {
    const init: RequestInit = { method };
    if (method !== "DELETE") {
      init.headers = { "content-type": "application/json" };
      init.body = JSON.stringify(body);
    }
    const res = await fetchImpl(target, init);
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
    create: (name, path) => act<CreateWorkspaceResult>("POST", url(), path ? { name, path } : { name }),
    rename: (id, name) => act<UpdateWorkspaceResult>("PATCH", url(`/${encodeURIComponent(id)}`), { name }),
    unregister: (id) => act<DeleteWorkspaceResult>("DELETE", url(`/${encodeURIComponent(id)}`)),
  };
}

/** 模拟模式：内存里的两个工作区。 */
export function createMockWorkspaceApi(): WorkspaceApi {
  const base = "C:\\Users\\demo\\Documents\\数字员工";
  const state: WorkspaceList = {
    readOnly: false,
    workspaces: [{ id: "ws-demo", name: "我的工作区", path: base }],
  };
  let seq = 0;
  return {
    list: async () => structuredClone(state),
    async create(name, path) {
      const trimmed = name.trim();
      if (!trimmed) return { ok: false, error: "invalid_input", message: "工作区名称不能为空，最多 40 个字" };
      const dir = path ?? `C:\\Users\\demo\\AppData\\Roaming\\digital-employee-app\\workspaces\\ws-mock-${seq + 1}`;
      if (state.workspaces.some((w) => w.path.toLowerCase() === dir.toLowerCase())) return { ok: false, error: "invalid_input", message: "这个目录已经是工作区" };
      const workspace = { id: `ws-mock-${++seq}`, name: trimmed, path: dir };
      state.workspaces.push(workspace);
      return { ok: true, workspace };
    },
    async rename(id, name) {
      const target = state.workspaces.find((w) => w.id === id);
      if (!target) return { ok: false, error: "not_found", message: `工作区不存在：${id}` };
      const trimmed = name.trim();
      if (!trimmed) return { ok: false, error: "invalid_input", message: "工作区名称不能为空，最多 40 个字" };
      target.name = trimmed;
      return { ok: true, workspace: { ...target } };
    },
    async unregister(id) {
      if (!state.workspaces.some((w) => w.id === id)) return { ok: false, error: "not_found", message: `工作区不存在：${id}` };
      state.workspaces = state.workspaces.filter((w) => w.id !== id);
      return { ok: true, id };
    },
  };
}
