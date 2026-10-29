// 任务状态机契约。流转表是“能否送审、能否交付”的唯一依据（docs/design.md §5）。

import type { TaskCommand } from "./engine";

export const TASK_STATES = [
  "draft",
  "admission",
  "awaiting_initiator",
  "running",
  "qc",
  "awaiting_operator",
  "awaiting_review",
  "delivered",
  "terminated",
] as const;
export type TaskState = (typeof TASK_STATES)[number];

export const STATE_LABELS: Record<TaskState, string> = {
  draft: "草稿",
  admission: "准入校验",
  awaiting_initiator: "待发起人补充",
  running: "执行中",
  qc: "质检中",
  awaiting_operator: "待人工处理",
  awaiting_review: "待审核",
  delivered: "已交付",
  terminated: "已终止",
};

export const TERMINAL_STATES: readonly TaskState[] = ["delivered", "terminated"];
export const PAUSED_STATES: readonly TaskState[] = [
  "awaiting_initiator",
  "awaiting_operator",
  "awaiting_review",
];

export type Role = "initiator" | "reviewer" | "operator" | "system";

export type TransitionTrigger =
  | "submit"
  | "admission_passed"
  | "admission_failed"
  | "supplement"
  | "continue_without_minutes"
  | "continue_with_partial"
  | "terminate"
  | "material_issue"
  | "steps_completed"
  | "limit_exceeded"
  | "tool_failed"
  | "qc_passed"
  | "qc_autofix"
  | "qc_exhausted"
  | "retry"
  | "approve"
  | "return";

export interface Transition {
  from: TaskState;
  to: TaskState;
  trigger: TransitionTrigger;
  actor: Role;
}

// 表外的流转一律拒绝，并写 transition_rejected 事件。
export const TRANSITIONS: readonly Transition[] = [
  { from: "draft", to: "admission", trigger: "submit", actor: "initiator" },
  { from: "admission", to: "running", trigger: "admission_passed", actor: "system" },
  { from: "admission", to: "awaiting_initiator", trigger: "admission_failed", actor: "system" },
  { from: "awaiting_initiator", to: "admission", trigger: "supplement", actor: "initiator" },
  { from: "awaiting_initiator", to: "admission", trigger: "continue_without_minutes", actor: "initiator" },
  { from: "awaiting_initiator", to: "admission", trigger: "continue_with_partial", actor: "initiator" },
  { from: "awaiting_initiator", to: "terminated", trigger: "terminate", actor: "initiator" },
  { from: "running", to: "awaiting_initiator", trigger: "material_issue", actor: "system" },
  { from: "running", to: "qc", trigger: "steps_completed", actor: "system" },
  { from: "running", to: "awaiting_operator", trigger: "limit_exceeded", actor: "system" },
  { from: "running", to: "awaiting_operator", trigger: "tool_failed", actor: "system" },
  { from: "qc", to: "awaiting_review", trigger: "qc_passed", actor: "system" },
  { from: "qc", to: "running", trigger: "qc_autofix", actor: "system" },
  { from: "qc", to: "awaiting_operator", trigger: "qc_exhausted", actor: "system" },
  { from: "awaiting_operator", to: "running", trigger: "retry", actor: "operator" },
  { from: "awaiting_operator", to: "terminated", trigger: "terminate", actor: "operator" },
  { from: "awaiting_review", to: "delivered", trigger: "approve", actor: "reviewer" },
  { from: "awaiting_review", to: "running", trigger: "return", actor: "reviewer" },
];

// 暂停原因。options 是该暂停下允许发起的命令（对应流程卡片上的按钮）。
export type PauseCode =
  // 待发起人补充
  | "missing_sales"
  | "missing_dimensions"
  | "period_out_of_range"
  | "period_incomplete"
  | "minutes_missing"
  | "minutes_week_mismatch"
  | "invalid_input"
  | "list_direction_mismatch"
  | "insufficient_material"
  // 待人工处理
  | "tool_failed"
  | "limit_exceeded"
  | "qc_exhausted"
  | "rework_needs_human"
  // 待审核
  | "awaiting_review";

export interface PauseReason {
  code: PauseCode;
  /** 给人看的中文说明，必须指出具体缺什么、为什么停。 */
  message: string;
  options: TaskCommand["type"][];
  detail?: Record<string, unknown>;
}
