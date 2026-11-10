// 任务引擎契约：这是整个系统的主测试接缝（docs/specs/0001-skeleton.md）。
// 服务端、命令行、测试都只通过 TaskEngine 驱动任务；引擎的外部依赖全部注入。

import type { TaskEvent } from "./events";
import type { QcCheckResult } from "./qc";
import type { PauseReason, Role, TaskState } from "./states";
import type { AttachmentRef, FaultConfig, FaultMode, ToolName } from "./tools";

export type TaskId = string;

export interface AttachmentInput {
  name: string;
  /** 源文件路径；引擎会复制进任务目录 */
  sourcePath: string;
}

export type TaskCommand =
  | { type: "submit" }
  | { type: "supplement"; form?: Record<string, unknown>; attachments?: AttachmentInput[] }
  | { type: "continue_without_minutes" }
  | { type: "continue_with_partial" }
  | { type: "retry" }
  | { type: "terminate" }
  | { type: "approve" }
  | { type: "return"; comment: string; sections?: string[] };

export type CommandResult =
  | { ok: true; snapshot: TaskSnapshot }
  | { ok: false; error: "illegal_transition" | "forbidden_role" | "invalid_input" | "not_found"; message: string };

export interface StepStatus {
  id: string;
  label: string;
  kind: "compute" | "judge" | "qc";
  status: "pending" | "running" | "done" | "failed" | "skipped";
}

export interface DeliverableRef {
  kind: "docx" | "md" | "qc_record";
  path: string;
  sha256: string;
}

export interface TaskSnapshot {
  id: TaskId;
  employee: string;
  configHash: string;
  state: TaskState;
  pause?: PauseReason;
  form: Record<string, unknown>;
  attachments: AttachmentRef[];
  steps: StepStatus[];
  budget: {
    modelCalls: { used: number; limit: number };
    elapsedMs: { used: number; limit: number };
  };
  reworkRound: number;
  qc?: QcCheckResult[];
  deliverables: DeliverableRef[];
  lastSeq: number;
}

export interface TaskSummary {
  id: TaskId;
  employee: string;
  title: string;
  state: TaskState;
  updatedAt: string;
  /** 事件日志损坏时给出的错误说明（任务仍以最后已知状态出现在列表里）；正常任务没有该字段 */
  error?: string;
  /** 任务属于哪个工作区（取自发起事件；Spec 0006 之前的旧任务没有该字段） */
  workspaceId?: string;
}

export interface TaskEngine {
  /** 建一个草稿任务（state=draft），不执行。 */
  create(input: { employee: string; form: Record<string, unknown>; attachments: AttachmentInput[]; actor: Role }): Promise<TaskSnapshot>;
  /** 发起命令。角色不符或流转非法时返回 ok=false，并写 transition_rejected 事件。 */
  command(taskId: TaskId, command: TaskCommand, actor: Role): Promise<CommandResult>;
  get(taskId: TaskId): Promise<TaskSnapshot | undefined>;
  list(): Promise<TaskSummary[]>;
  events(taskId: TaskId, afterSeq?: number): Promise<TaskEvent[]>;
  subscribe(taskId: TaskId, listener: (event: TaskEvent) => void): () => void;
  /** 等任务停在暂停态或终态，返回快照。测试和命令行用。 */
  waitForSettled(taskId: TaskId): Promise<TaskSnapshot>;
  /** 启动时调用：找出所有非终态、非暂停态的任务，从断点继续。 */
  resumeAll(): Promise<void>;
}

// ---- 依赖 ----

export interface ToolDescriptor {
  name: string;
  description: string;
  inputSchema: unknown;
}

export type ToolCallResult =
  | { ok: true; data: unknown }
  | { ok: false; error: string; retryable: boolean };

/** 工具调用客户端，生产环境经 MCP stdio，测试经 MCP InMemoryTransport 连真实工具服务器。 */
export interface ToolClient {
  listTools(): Promise<ToolDescriptor[]>;
  call(name: string, args: unknown, options?: { signal?: AbortSignal }): Promise<ToolCallResult>;
  close(): Promise<void>;
}

/**
 * 模型提供者。具体形状由 Go 后端（internal/agent）内部定义，
 * 外部只通过工厂创建：真实模型或剧本式假模型（DE_SCRIPTED）。
 */
export interface ModelProvider {
  readonly kind: "real" | "scripted";
}

export interface ModelConnection {
  name: string;
  baseUrl: string;
  apiKey: string;
  models: string[];
  /**
   * 每个模型单独的高级参数（按模型 id）：上下文窗口、输出上限、推理档位。
   * Spec 0004「每模型参数」；缺省时沿用 128k / 8192 / off（由 T20 的模型提供者消费）。
   */
  modelOptions?: Record<string, import("./settings").ModelOptions>;
  /** 连接级超时，毫秒；缺省由模型提供者决定 */
  timeoutMs?: number;
  /** 连接级模型调用重试次数 */
  maxRetries?: number;
}

export interface ModelSettings {
  connections: ModelConnection[];
  /** 最近一次在会话里选的模型（Spec 0006）；还没选过为 null */
  lastModel?: import("./settings").ModelRef | null;
  /** 旧文档的键：与 lastModel 同时出现时以 lastModel 为准 */
  defaultConnection: string;
  defaultModel: string;
}

/**
 * 设置页“演示与调试”的开关（docs/design.md §14.1），持久化在 `<workspace>/settings.json` 的 demo 段。
 * 工具故障发生在工具子进程里，由服务端通过 `admin_set_faults` 下发；
 * 数据溯源与崩溃是引擎进程内的故障，通过可变的 FaultHooks 生效。
 */
export interface DemoSettings {
  /** 工具名 → 故障模式；一次性故障（数据溯源、崩溃）不在这里 */
  toolFaults: Partial<Record<ToolName, FaultMode>>;
  /** 下一个写入步骤（qcSources.draft）注入一次裸数字，生效后自动清除 */
  corruptTraceability: boolean;
  /** 下一个步骤开始后退出进程一次，触发前持久化，触发后自动清除 */
  crashOnNextStep: boolean;
}

/** “测试连接”的结果：发一条极短请求，只报告可用性、延迟和错误。 */
export interface ModelConnectionTest {
  ok: boolean;
  latencyMs: number;
  model: string;
  /** ok=false 时的中文说明；不包含 API Key */
  error?: string;
}

export interface FaultHooks {
  /** 在指定步骤执行到一半时让进程“崩溃”（测试中抛出 SimulatedCrash），用于演示断点恢复。 */
  crashAtStep?: string;
  /** 下一个步骤开始执行后崩溃一次（设置页的一次性开关，命中后清除）。 */
  crashOnNextStep?: boolean;
  /** 注入到判断步骤之后、质检之前：篡改一个数字，用于演示“数据溯源不通过 → 自动修正”。命中后清除。 */
  corruptTraceability?: boolean;
  tools?: FaultConfig["tools"];
}

export interface EngineDeps {
  /** 工作区根目录，任务数据在 <workspaceDir>/tasks/<taskId>/ */
  workspaceDir: string;
  /** 员工配置根目录，含 weekly-report/ 等子目录 */
  employeesDir: string;
  toolClient: ToolClient;
  model: ModelProvider;
  now?: () => Date;
  /** 运行时可变：服务端持有同一个对象，设置页改动后立即对新步骤生效。 */
  faults?: FaultHooks;
  /**
   * 崩溃注入是否真的退出进程（生产入口为 true；测试默认 false，抛 SimulatedCrash 让测试可断言）。
   * 不靠环境变量判断测试环境，避免“猜测试环境”。
   */
  exitOnCrash?: boolean;
}

/** Go 后端（internal/engine）提供的任务引擎工厂形状 */
export type CreateTaskEngine = (deps: EngineDeps) => TaskEngine;
