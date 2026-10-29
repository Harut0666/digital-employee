// 前端数据层接口。真实实现走 Eden（对接 T3 的接口），VITE_MOCK=1 时换成内存模拟。
// 视图层只依赖这里的接口，不关心背后是谁。

import type {
  CommandResult,
  ConfigDiff,
  ConfigVersion,
  EmployeeConfigResponse,
  RestoreConfigResponse,
  Role,
  SaveConfigRequest,
  SaveConfigResponse,
  TaskCommand,
  TaskEvent,
  TaskSnapshot,
  TaskSummary,
} from "@de/core/contract";

/** 界面上可切换的三种角色；system 只出现在事件里。 */
export type UiRole = Exclude<Role, "system">;

export interface EmployeeInfo {
  id: string;
  name: string;
  description: string;
  /** 发起表单的 JSON Schema，交给 @rjsf/antd 渲染 */
  formSchema: Record<string, unknown>;
  /** 表单的 uiSchema（form.ui.json）；其中 ui:widget 为 file / files 的字段是上传项 */
  formUi?: Record<string, unknown>;
  /** 退回时可勾选的章节（取自 qc.yaml 的 structure.sections） */
  reviewSections?: { id: string; heading: string }[];
}

/** 附件所属的上传项，即 form.schema.json 里文件字段的名字（如 salesFile、minutesFiles）；没有文件字段时为 other。 */
export type UploadSlot = string;

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

/** 欢迎页的公开样例（服务端从工作区 samples 里读出，不含文件本体） */
export interface PublicSampleInfo {
  id: string;
  employee: string;
  title: string;
  form: Record<string, unknown>;
  files: { name: string; size: number }[];
}

export interface WebApi {
  listEmployees(): Promise<EmployeeInfo[]>;
  listTasks(): Promise<TaskSummary[]>;
  getTask(id: string): Promise<TaskSnapshot | undefined>;
  getEvents(id: string, afterSeq?: number): Promise<TaskEvent[]>;
  /** 建草稿任务（不提交）。提交用 command(id, {type:"submit"})。 */
  createTask(input: CreateTaskInput): Promise<TaskSnapshot>;
  command(id: string, command: TaskCommand, uploads?: UploadItem[]): Promise<CommandResult>;
  /** 欢迎页“载入公开样例”：用工作区里释放出来的样例建一份草稿任务（不提交）。 */
  loadPublicSample(): Promise<TaskSnapshot>;
  /** 订阅任务事件：先补发 afterSeq 之后的事件，再实时推送。返回取消函数。 */
  subscribe(id: string, afterSeq: number, onEvent: (event: TaskEvent) => void): () => void;
  /** 交付物下载地址；无则 undefined。 */
  deliverableUrl(id: string, kind: DeliverableKind): string | undefined;

  // ---- 员工配置页 ----
  /** 配置页的数据：每个文件的原文与解析结果、派生视图、版本列表。 */
  getEmployeeConfig(id: string): Promise<EmployeeConfigResponse>;
  /** 保存一个配置文件：content（源文件视图）或 data（表单视图）二选一。校验失败会抛错。 */
  saveEmployeeConfig(id: string, path: string, input: SaveConfigRequest): Promise<SaveConfigResponse>;
  listConfigVersions(id: string): Promise<ConfigVersion[]>;
  diffConfigVersion(id: string, versionId: string): Promise<ConfigDiff>;
  restoreConfigVersion(id: string, versionId: string): Promise<RestoreConfigResponse>;
}
