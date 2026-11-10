// 真实数据层：Eden treaty 对接 T3 的 Elysia 服务，SSE 订阅任务事件。
//
// treaty<App> 的路由类型很深（multipart 联合体、带 `{ id }` 的动态路由），这里先把 client 收窄成下面的
// ApiShape，路由和请求体按 packages/server/src/index.ts 的实际路由书写；返回值仍然由 Eden 解包。

import { treaty } from "@elysiajs/eden";
import type { TaskEvent, TaskEventType } from "@de/core/contract";
import type { App } from "@de/server";
import { createConfigApi } from "./config";
import { createKnowledgeApi } from "./knowledge";
import { createSessionApi } from "./sessions";
import type { CommandResult, CreateTaskInput, DeliverableKind, EmployeeInfo, PublicSampleInfo, TaskSnapshot, TaskSummary, UiRole, UploadItem, WebApi } from "./types";

/** 全部事件类型；用 Record 保证契约新增事件类型时这里编译报错而不是悄悄漏订阅。 */
const EVENT_TYPE_SET: Record<TaskEventType, true> = {
  task_created: true,
  model_switched: true,
  form_updated: true,
  state_changed: true,
  transition_rejected: true,
  step_started: true,
  step_completed: true,
  step_failed: true,
  tool_called: true,
  tool_result: true,
  tool_denied: true,
  model_called: true,
  model_delta: true,
  qc_result: true,
  review_decision: true,
  deliverable_created: true,
  deliverable_filed: true,
  fault_injected: true,
};
const EVENT_TYPES = Object.keys(EVENT_TYPE_SET) as TaskEventType[];

type Reply<T> = Promise<{ data: T | null; error: { status: unknown; value: unknown } | null }>;

interface TaskRoute {
  get(): Reply<TaskSnapshot>;
  events: { get(opts?: { query?: { after?: number } }): Reply<TaskEvent[]> };
  commands: { post(body: unknown): Reply<CommandResult> };
}
interface ApiShape {
  api: {
    employees: { get(): Reply<EmployeeInfo[]> };
    tasks: {
      get(): Reply<TaskSummary[]>;
      post(body: { employee: string; form: string; attachments: File[] }): Reply<TaskSnapshot>;
    } & ((params: { id: string }) => TaskRoute);
    samples: { get(): Reply<{ samples: PublicSampleInfo[] }> } & ((params: { id: string }) => { tasks: { post(): Reply<TaskSnapshot> } });
  };
}

function unwrap<T>(res: Awaited<Reply<T>>): T {
  if (res.error || res.data === null) {
    const v = res.error?.value;
    const msg = typeof v === "object" && v && "message" in v ? String((v as { message: unknown }).message) : `请求失败（${String(res.error?.status)}）`;
    throw new Error(msg);
  }
  return res.data;
}

export function createEdenApi(getRole: () => UiRole, origin: string = window.location.origin): WebApi {
  const client = treaty<App>(origin, { headers: () => ({ "x-role": getRole() }) }) as unknown as ApiShape;
  const task = (id: string) => client.api.tasks({ id });
  const configApi = createConfigApi(origin);

  return {
    listEmployees: async () => unwrap(await client.api.employees.get()),
    listTasks: async () => unwrap(await client.api.tasks.get()),
    async getTask(id) {
      const res = await task(id).get();
      if (res.error && res.error.status === 404) return undefined;
      return unwrap(res);
    },
    getEvents: async (id, afterSeq) => unwrap(await task(id).events.get({ query: { after: afterSeq } })),
    async createTask(input: CreateTaskInput) {
      return unwrap(
        await client.api.tasks.post({
          employee: input.employee,
          form: JSON.stringify(input.form),
          attachments: input.uploads.map((u) => u.file),
        }),
      );
    },
    async command(id, command, uploads?: UploadItem[]) {
      // 带附件的 supplement 走 multipart，其余直接发 JSON
      const body = uploads?.length ? { command: JSON.stringify(command), attachments: uploads.map((u) => u.file) } : command;
      const res = await task(id).commands.post(body);
      // 403 / 409 等业务错误体本身就是 CommandResult（ok=false），不当异常抛
      if (res.data) return res.data;
      const v = res.error?.value as CommandResult | undefined;
      if (v && typeof v === "object" && "ok" in v) return v;
      return unwrap(res);
    },
    async loadPublicSample() {
      const list = unwrap(await client.api.samples.get());
      const sample = list.samples[0];
      if (!sample) throw new Error("工作区里没有公开样例（首次启动会自动释放到 samples/）");
      return unwrap(await client.api.samples({ id: sample.id }).tasks.post());
    },
    subscribe(id, afterSeq, onEvent) {
      // EventSource 不能带自定义请求头；SSE 只读，不依赖角色。
      // 服务端为每条事件写了 `event: <type>`，是具名事件，onmessage 收不到，要按类型逐个监听。
      const es = new EventSource(`${origin}/api/tasks/${encodeURIComponent(id)}/stream?after=${afterSeq}`);
      const handle = (m: MessageEvent<string>) => {
        try {
          onEvent(JSON.parse(m.data) as TaskEvent);
        } catch {
          /* 忽略非 JSON 消息 */
        }
      };
      for (const type of EVENT_TYPES) es.addEventListener(type, handle as EventListener);
      es.onmessage = handle;
      return () => es.close();
    },
    deliverableUrl: (id, kind: DeliverableKind, version?: number) =>
      `${origin}/api/tasks/${encodeURIComponent(id)}/deliverables/${kind}${version ? `?version=${version}` : ""}`,

    async openDeliverable(id, kind: DeliverableKind, mode, version) {
      // 打开的是本机应用，由后端（本机进程）去启动；只能打开这个任务登记的交付物
      const res = await fetch(`${origin}/api/tasks/${encodeURIComponent(id)}/deliverables/${kind}/open`, {
        method: "POST",
        headers: { "content-type": "application/json", "x-role": getRole() },
        body: JSON.stringify({ mode, ...(version ? { version } : {}) }),
      }).catch((e: unknown) => e as Error);
      if (res instanceof Error) return { ok: false as const, message: res.message };
      if (res.ok) return { ok: true as const };
      const body: unknown = await res.json().catch(() => undefined);
      const text = typeof body === "object" && body !== null && "message" in body ? String((body as { message: unknown }).message) : `请求失败（${res.status}）`;
      return { ok: false as const, message: text };
    },

    // ---- 员工配置页 ----
    getEmployeeConfig: configApi.getEmployeeConfig,
    saveEmployeeConfig: configApi.saveEmployeeConfig,
    listConfigVersions: configApi.listConfigVersions,
    diffConfigVersion: configApi.diffConfigVersion,
    restoreConfigVersion: configApi.restoreConfigVersion,

    // ---- 会话 ----
    ...createSessionApi(origin, getRole),

    // ---- 知识库 ----
    ...createKnowledgeApi(origin, getRole),
  };
}

