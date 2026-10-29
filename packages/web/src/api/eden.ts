// 真实数据层：Eden treaty 对接 T3 的 Elysia 服务，SSE 订阅任务事件。
//
// 注意：packages/server 目前导出的 App 还是空 Elysia（T3 未合并），treaty<App> 推导不出路由类型，
// 所以这里把 client 收窄成下面的 ApiShape，路由与请求体按 docs/specs/tickets/T3-server-cli.md 约定书写。
// T3 合并后，如果路由类型可用，可以去掉 ApiShape 直接用 treaty<App> 的推导结果。

import { treaty } from "@elysiajs/eden";
import type { CommandResult, TaskEvent, TaskSnapshot, TaskSummary } from "@de/core/contract";
import type { App } from "@de/server";
import type { CreateTaskInput, DeliverableKind, EmployeeInfo, UiRole, UploadItem, WebApi } from "./types";

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
    subscribe(id, afterSeq, onEvent) {
      // EventSource 不能带自定义请求头；SSE 只读，不依赖角色。
      const es = new EventSource(`${origin}/api/tasks/${encodeURIComponent(id)}/stream?after=${afterSeq}`);
      es.onmessage = (m) => {
        try {
          onEvent(JSON.parse(m.data) as TaskEvent);
        } catch {
          /* 忽略心跳等非 JSON 消息 */
        }
      };
      return () => es.close();
    },
    deliverableUrl: (id, kind: DeliverableKind) => `${origin}/api/tasks/${encodeURIComponent(id)}/deliverables/${kind}`,
  };
}
