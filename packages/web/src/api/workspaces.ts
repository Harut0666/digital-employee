// 工作区与记录接口（Spec 0006“工作区与记录接口”）。
// 读取列表由服务端管：GET /api/workspaces 带每个工作区的会话摘要和末尾的“未归属”，GET /api/records 是记录 tab；
// 新建、重命名、取消登记由应用宿主处理（POST/PATCH/DELETE /api/workspaces）。
// 写操作返回结果对象：被拒（只读、重名目录）也是结果，不抛异常；读取类接口出错时抛异常。

import type { FetchLike } from "./sessions";
import type { MockSessionSources } from "./types";
import type { RecordItem, SessionSummary, WorkspaceInfo } from "./types";

export interface WorkspaceItem extends WorkspaceInfo {}

export interface WorkspaceList {
  /** 单目录模式（环境变量 DE_WORKSPACE 指定了目录）：不能新建、重命名或取消登记 */
  readOnly: boolean;
  reason?: string;
  /** 登记的工作区，按登记顺序；每项带会话摘要 */
  workspaces: WorkspaceItem[];
  /** 所属工作区已取消登记的会话，放在最后 */
  unassigned?: SessionSummary[];
}

export interface RecordGroupView {
  key: RecordItem["group"];
  label: string;
  items: RecordItem[];
}

export interface RecordsList {
  groups: RecordGroupView[];
}

export type WorkspaceRefusal = { ok: false; error: string; message: string };
export type CreateWorkspaceResult = { ok: true; workspace: WorkspaceItem } | WorkspaceRefusal;
export type UpdateWorkspaceResult = { ok: true; workspace: WorkspaceItem } | WorkspaceRefusal;
export type DeleteWorkspaceResult = { ok: true; id: string } | WorkspaceRefusal;
export type OpenWorkspaceResult = { ok: true; path: string } | WorkspaceRefusal;
/** 选目录：选中 `{ok:true,path}`；人取消 `{ok:true,cancelled:true}`；做不到（不是桌面窗口等）`{ok:false,message}` */
export type PickFolderResult = { ok: true; path: string } | { ok: true; cancelled: true } | WorkspaceRefusal;

export interface WorkspaceApi {
  /** ?archived=1 连归档的会话一起带（默认不带） */
  list(options?: { archived?: boolean }): Promise<WorkspaceList>;
  /** 记录 tab：所有工作区里已开工的任务，按状态分组 */
  records(): Promise<RecordsList>;
  /** path 省略时在应用目录下建一个 */
  create(name: string, path?: string): Promise<CreateWorkspaceResult>;
  rename(id: string, name: string): Promise<UpdateWorkspaceResult>;
  /** 取消登记：只删登记项，目录里的文件和已有记录都保留 */
  unregister(id: string): Promise<DeleteWorkspaceResult>;
  /** 在资源管理器里打开工作区目录 */
  open(id: string): Promise<OpenWorkspaceResult>;
  /** 弹出系统的选择目录对话框（POST /api/workspaces/pick-folder，请求体可空），“添加工作区”里的“浏览…”用 */
  pickFolder(): Promise<PickFolderResult>;
}

type Json = Record<string, unknown>;

const messageOf = (body: unknown, status: number) =>
  typeof body === "object" && body !== null && typeof (body as Json).message === "string" ? ((body as Json).message as string) : `请求失败（${status}）`;

export function createWorkspaceApi(origin: string = window.location.origin, fetchImpl: FetchLike = (input, init) => fetch(input, init)): WorkspaceApi {
  const url = (path = "") => `${origin}/api/workspaces${path}`;

  async function read<T>(target: string): Promise<T> {
    const res = await fetchImpl(target, { headers: { "x-role": "initiator" } });
    const body: unknown = await res.json().catch(() => undefined);
    if (!res.ok) throw new Error(messageOf(body, res.status));
    return body as T;
  }

  async function act<T>(method: "POST" | "PATCH" | "DELETE", target: string, body: Json = {}, headers: Record<string, string> = {}): Promise<T | WorkspaceRefusal> {
    const init: RequestInit = { method };
    if (method !== "DELETE") {
      init.headers = { "content-type": "application/json", ...headers };
      init.body = JSON.stringify(body);
    }
    const res = await fetchImpl(target, init);
    const parsed: unknown = await res.json().catch(() => undefined);
    if (typeof parsed === "object" && parsed !== null && "ok" in parsed) return parsed as T | WorkspaceRefusal;
    return { ok: false, error: "internal", message: messageOf(parsed, res.status) };
  }

  return {
    list: (options) => read<WorkspaceList>(url(options?.archived ? "?archived=1" : "")),
    records: () => read<RecordsList>(`${origin}/api/records`),
    create: (name, path) => act<CreateWorkspaceResult>("POST", url(), path ? { name, path } : { name }),
    rename: (id, name) => act<UpdateWorkspaceResult>("PATCH", url(`/${encodeURIComponent(id)}`), { name }),
    unregister: (id) => act<DeleteWorkspaceResult>("DELETE", url(`/${encodeURIComponent(id)}`)),
    open: (id) => act<OpenWorkspaceResult>("POST", url(`/${encodeURIComponent(id)}/open`)),
    // 选目录只有发起人能做（非发起人 403），请求头带身份；第一次弹出约要 1 秒
    pickFolder: () => act<PickFolderResult>("POST", url("/pick-folder"), {}, { "x-role": "initiator" }),
  };
}

/** 模拟模式：内存里的两个工作区。会话和记录来自 mock-sessions.ts 的同一个实例（见 api/index.ts）。 */
export function createMockWorkspaceApi(sessionState: MockSessionSources): WorkspaceApi {
  const base = "C:\\Users\\demo\\Documents\\数字员工";
  const state: WorkspaceItem[] = [
    { id: "ws-demo", name: "我的工作区", path: base },
    { id: "ws-huanan", name: "华南区", path: "C:\\Users\\demo\\Documents\\华南区" },
  ];
  let seq = 0;
  return {
    async list(options) {
      const sessions = (await sessionState.all()).filter((s) => options?.archived === true || !s.archived);
      return {
        readOnly: false,
        // 每组里带自己的会话；所属工作区已取消登记的会话进未归属（模拟模式不会发生）
        workspaces: state.map((workspace) => ({ ...workspace, sessions: sessions.filter((s) => s.workspaceId === workspace.id) })),
        unassigned: sessions.filter((s) => !state.some((w) => w.id === s.workspaceId)),
      };
    },
    async records() {
      const order: RecordItem["group"][] = ["running", "supplement", "review", "returned", "delivered", "terminated"];
      const labels: Record<RecordItem["group"], string> = { running: "进行中", supplement: "待补充", review: "待审核", returned: "已退回", delivered: "已交付", terminated: "已终止" };
      const items = await sessionState.records("initiator");
      return { groups: order.map((key) => ({ key, label: labels[key], items: items.filter((i) => i.group === key) })) };
    },
    async create(name, path) {
      const trimmed = name.trim();
      if (!trimmed) return { ok: false, error: "invalid_input", message: "工作区名称不能为空，最多 40 个字" };
      const dir = path ?? `C:\\Users\\demo\\AppData\\Roaming\\digital-employee-app\\workspaces\\ws-mock-${seq + 1}`;
      if (state.some((w) => w.path.toLowerCase() === dir.toLowerCase())) return { ok: false, error: "invalid_input", message: "这个目录已经是工作区" };
      const workspace = { id: `ws-mock-${++seq}`, name: trimmed, path: dir };
      state.push(workspace);
      return { ok: true, workspace };
    },
    async rename(id, name) {
      const target = state.find((w) => w.id === id);
      if (!target) return { ok: false, error: "not_found", message: `工作区不存在：${id}` };
      const trimmed = name.trim();
      if (!trimmed) return { ok: false, error: "invalid_input", message: "工作区名称不能为空，最多 40 个字" };
      target.name = trimmed;
      return { ok: true, workspace: { ...target } };
    },
    async unregister(id) {
      if (!state.some((w) => w.id === id)) return { ok: false, error: "not_found", message: `工作区不存在：${id}` };
      // 只删登记项：目录里的文件和已有记录都留着，会话下次读列表时进“未归属”
      state.splice(0, state.length, ...state.filter((w) => w.id !== id));
      return { ok: true, id };
    },
    async open(id) {
      const target = state.find((w) => w.id === id);
      if (!target) return { ok: false, error: "not_found", message: `工作区不存在：${id}` };
      return { ok: true, path: target.path };
    },
    async pickFolder() {
      // 模拟模式没有系统对话框：给一个示例路径，名称缺省时用目录名补全
      return { ok: true, path: `C:\\Users\\demo\\Documents\\示例项目${seq + 1}` };
    },
  };
}