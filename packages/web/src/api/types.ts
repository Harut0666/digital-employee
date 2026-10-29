// 前端数据层接口。真实实现走 Eden（对接 T3 的接口），VITE_MOCK=1 时换成内存模拟。
// 视图层只依赖这里的接口，不关心背后是谁。

import type { CommandResult, Role, TaskCommand, TaskEvent, TaskSnapshot, TaskSummary } from "@de/core/contract";

/** 界面上可切换的三种角色；system 只出现在事件里。 */
export type UiRole = Exclude<Role, "system">;

export interface EmployeeInfo {
  id: string;
  name: string;
  description: string;
  /** 发起表单的 JSON Schema，交给 @rjsf/antd 渲染 */
  formSchema: Record<string, unknown>;
}

/** 附件所属的上传区；决定放进哪个预检项。 */
export type UploadSlot = "sales" | "minutes" | "other";

export interface UploadItem {
  slot: UploadSlot;
  file: File;
}

export interface CreateTaskInput {
  employee: string;
  form: Record<string, unknown>;
  uploads: UploadItem[];
}

export type DeliverableKind = "docx" | "md" | "qc_record";

export interface WebApi {
  listEmployees(): Promise<EmployeeInfo[]>;
  listTasks(): Promise<TaskSummary[]>;
  getTask(id: string): Promise<TaskSnapshot | undefined>;
  getEvents(id: string, afterSeq?: number): Promise<TaskEvent[]>;
  /** 建草稿任务（不提交）。提交用 command(id, {type:"submit"})。 */
  createTask(input: CreateTaskInput): Promise<TaskSnapshot>;
  command(id: string, command: TaskCommand, uploads?: UploadItem[]): Promise<CommandResult>;
  /** 订阅任务事件：先补发 afterSeq 之后的事件，再实时推送。返回取消函数。 */
  subscribe(id: string, afterSeq: number, onEvent: (event: TaskEvent) => void): () => void;
  /** 交付物下载地址；无则 undefined。 */
  deliverableUrl(id: string, kind: DeliverableKind): string | undefined;
}
