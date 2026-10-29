// 任务引擎契约：这是整个系统的主测试接缝（docs/specs/0001-skeleton.md）。
// 服务端、命令行、测试都只通过 TaskEngine 驱动任务；引擎的外部依赖全部注入。

import type { TaskEvent } from "./events";
import type { QcCheckResult } from "./qc";
import type { PauseReason, Role, TaskState } from "./states";
import type { AttachmentRef, FaultConfig } from "./tools";

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
 * 模型提供者。具体形状由 @de/core 内部定义（封装 pi-ai 的 Model 与 pi-agent-core 的 streamFn），
 * 外部只通过工厂创建：createModelProvider(settings) 用真实模型，createScriptedModelProvider(script) 用剧本式假模型。
 */
export interface ModelProvider {
  readonly kind: "real" | "scripted";
}

export interface ModelConnection {
  name: string;
  baseUrl: string;
  apiKey: string;
  models: string[];
}

export interface ModelSettings {
  connections: ModelConnection[];
  defaultConnection: string;
  defaultModel: string;
}

export interface FaultHooks {
  /** 在指定步骤执行到一半时让进程“崩溃”（测试中抛出 SimulatedCrash），用于演示断点恢复。 */
  crashAtStep?: string;
  /** 注入到判断步骤之后、质检之前：篡改一个数字，用于演示“数据溯源不通过 → 自动修正”。 */
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
  faults?: FaultHooks;
}

/** @de/core 提供：export function createTaskEngine(deps: EngineDeps): TaskEngine */
export type CreateTaskEngine = (deps: EngineDeps) => TaskEngine;
