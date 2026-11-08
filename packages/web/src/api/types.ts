// 前端数据层接口。真实实现走 Eden（对接 T3 的接口），VITE_MOCK=1 时换成内存模拟。
// 视图层只依赖这里的接口，不关心背后是谁。

import type {
  CommandResult as ContractCommandResult,
  ConfigDiff,
  DeliverableRef,
  StepStatus,
  ConfigVersion,
  EmployeeConfigResponse,
  PauseCode,
  PauseReason,
  QcCheckResult,
  RestoreConfigResponse,
  Role,
  SaveConfigRequest,
  SaveConfigResponse,
  TaskCommand,
  TaskEvent,
  TaskSnapshot as ContractTaskSnapshot,
  TaskState,
  TaskSummary as ContractTaskSummary,
} from "@de/core/contract";
import type { KnowledgeApi } from "./knowledge";
import type { SessionApi } from "./sessions";

/** 界面上可切换的三种角色；system 只出现在事件里。 */
export type UiRole = Exclude<Role, "system">;

export type CommandType = TaskCommand["type"];

/** 快照给出的一个可用操作。按钮的文字、样式、归属角色都以它为准，前端不自行推算。 */
export interface TaskAction {
  command: CommandType;
  label: string;
  roles: UiRole[];
  style: "primary" | "default" | "danger";
}

/** 快照里的暂停原因：比事件里的多了读取时生成的 title、resolution。TS 契约已冻结，Go 新增的代码在这里补。 */
export interface PauseView extends Omit<PauseReason, "code"> {
  code: PauseCode | "material_issue" | "model_unavailable";
  title?: string;
  resolution?: string;
}

/** 快照里的步骤：Go 后端多了工作流配置里的业务话 activity。 */
export interface StepStatusView extends StepStatus {
  activity?: string;
}

/** 修改说明：逐条退回意见改了哪些章节、改了什么。 */
export interface RevisionNote {
  comment: string;
  sections: string[];
  change: string;
}

/** 一版交付物：每次送审一版，退回重做产出下一版。 */
export interface DeliverableVersion {
  version: number;
  reworkRound: number;
  createdAt: string;
  files: DeliverableRef[];
  /** 引出这一版的退回意见；第 1 版没有 */
  feedback?: { comment: string; sections?: string[] };
  revisionNotes?: RevisionNote[];
}

export interface TaskSnapshot extends Omit<ContractTaskSnapshot, "pause" | "steps"> {
  pause?: PauseView;
  steps: StepStatusView[];
  actions: TaskAction[];
  waitingFor: UiRole[];
  /** 历次送审的交付物，旧到新 */
  versions: DeliverableVersion[];
  /** 当前交付物下载时的文件名（按类型） */
  downloadNames?: Partial<Record<DeliverableKind, string>>;
}

export interface TaskSummary extends ContractTaskSummary {
  statusText: string;
  waitingFor: UiRole[];
}

export type CommandResult = Exclude<ContractCommandResult, { ok: true }> | { ok: true; snapshot: TaskSnapshot };

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

// ---- 会话（Go 后端 internal/session，TS 后端没有对应接口） ----

/** 接待中 → 员工已接手 → 任务进行中 → 已结束 */
export type SessionStatus = "reception" | "drafting" | "task" | "closed";

export interface SessionAttachment {
  name: string;
  size: number;
}

export interface SessionEmployee {
  id: string;
  name: string;
  description: string;
}

export type SessionCardType = "employee_confirm" | "form" | "pause" | "qc" | "review" | "knowledge_filing" | "folder_file";

/** 流程卡片：内容和按钮都由后端填写，字段按 type 取用。 */
export interface SessionCard {
  type: SessionCardType;
  // employee_confirm
  proposed?: string;
  source?: "default" | "model" | "manual";
  reason?: string;
  candidates?: SessionEmployee[];
  // form
  employee?: string;
  draft?: Record<string, unknown>;
  attachments?: SessionAttachment[];
  submitted?: boolean;
  // pause / qc / review
  taskId?: string;
  atSeq?: number;
  pause?: PauseView;
  /** 现在能点的按钮；卡片已失效时没有 */
  actions?: TaskAction[];
  passed?: boolean;
  checks?: QcCheckResult[];
  comment?: string;
  sections?: string[];
  // knowledge_filing：默认标题、模型起草的描述、起草是否失败、已入库的条目 ID
  title?: string;
  description?: string;
  draftFailed?: boolean;
  filed?: string;
  // folder_file：目录里的文件（path 相对工作区目录）、是否已确认（确认后材料已经进会话）
  path?: string;
  name?: string;
  size?: number;
  confirmed?: boolean;
}

/** 工作区目录里的一项（@ 选择和 Agent 的 list_files 用）；路径相对工作区目录，用 / 分隔 */
export interface FolderEntry {
  path: string;
  name: string;
  dir?: boolean;
  size?: number;
}

/** GET /api/sessions/{id}/folder-files 的响应 */
export interface FolderListing {
  entries: FolderEntry[];
  truncated: boolean;
}

export type SessionMessageKind = "user" | "assistant" | "notice" | "card" | "tool_called" | "tool_denied" | "model_failed";

export interface SessionMessage {
  id: string;
  seq: number;
  ts: string;
  kind: SessionMessageKind;
  role?: UiRole;
  text?: string;
  card?: SessionCard;
  tool?: string;
  reason?: string;
}

/** 开工清单的一项：状态只由后端按上传的文件和表单判定 */
export interface SessionChecklistItem {
  id: string;
  label: string;
  required: boolean;
  status: "done" | "missing" | "skipped";
  /** 可跳过项的跳过说法，如“无纪要继续” */
  skipLabel?: string;
}

export interface SessionChecklist {
  items: SessionChecklistItem[];
  /** 必需项都齐了，可以开工 */
  ready: boolean;
  /** 还缺的必需项名称 */
  missing: string[];
}

export interface SessionSummary {
  id: string;
  /** 会话属于哪个工作区（Spec 0006；侧栏按它分组是 T38） */
  workspaceId: string;
  status: SessionStatus;
  /** 由旧任务推导出的只读会话 */
  readOnly: boolean;
  title: string;
  employee?: string;
  taskId?: string;
  draft?: Record<string, unknown>;
  attachments: SessionAttachment[];
  createdAt: string;
  updatedAt: string;
  /** 来自任务摘要，没有任务时为空 */
  taskState?: TaskState;
  statusText?: string;
  waitingFor: UiRole[];
}

export interface SessionDetail extends SessionSummary {
  /** 开工清单（T30）；读单个会话时才有，员工没有清单时为空 */
  checklist?: SessionChecklist;
  messages: SessionMessage[];
}

/** 设置页入口：提交前检查不通过时带人去哪 */
export interface SettingsEntry {
  section: string;
  label: string;
}

export interface PreflightResult {
  ok: boolean;
  error?: string;
  message?: string;
  settings?: SettingsEntry;
}

// ---- 知识库（Go 后端 internal/knowledge） ----

/** 知识库索引里的一个条目；正文按 ID 另读 */
export interface KnowledgeEntry {
  id: string;
  title: string;
  description: string;
  tags: string[];
  sourceTask: string;
  sourceEmployee: string;
  createdAt: string;
  fileName: string;
}

export interface KnowledgeDetail extends KnowledgeEntry {
  text: string;
}

/** 入库：哪个已交付任务的交付物，人确认过的标题、描述和标签 */
export interface KnowledgeFiling {
  taskId: string;
  title?: string;
  description: string;
  tags?: string[];
}

export type KnowledgeResult = { ok: true; entry: KnowledgeEntry } | { ok: false; error: string; message: string };

export type SessionResult =
  | { ok: true; messages: SessionMessage[]; session: SessionSummary }
  | { ok: false; error: string; message: string; settings?: SettingsEntry };

export interface WebApi extends SessionApi, KnowledgeApi {
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
  /** 交付物下载地址；无则 undefined。给了版本号时取那一版。 */
  deliverableUrl(id: string, kind: DeliverableKind, version?: number): string | undefined;

  // ---- 员工配置页 ----
  /** 配置页的数据：每个文件的原文与解析结果、派生视图、版本列表。 */
  getEmployeeConfig(id: string): Promise<EmployeeConfigResponse>;
  /** 保存一个配置文件：content（源文件视图）或 data（表单视图）二选一。校验失败会抛错。 */
  saveEmployeeConfig(id: string, path: string, input: SaveConfigRequest): Promise<SaveConfigResponse>;
  listConfigVersions(id: string): Promise<ConfigVersion[]>;
  diffConfigVersion(id: string, versionId: string): Promise<ConfigDiff>;
  restoreConfigVersion(id: string, versionId: string): Promise<RestoreConfigResponse>;
}
