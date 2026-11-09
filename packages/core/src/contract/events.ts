// 事件日志契约（docs/design.md §8）。每个任务一份 events.jsonl，一行一个事件，追加写并落盘。
// 任务的状态、检查点全部由重放事件得到；大对象存为工作区内的产物文件，事件里只记路径和哈希。

import type { AttachmentRef } from "./tools";
import type { QcCheckResult } from "./qc";
import type { PauseReason, Role, TaskState, TransitionTrigger } from "./states";

interface EventBase {
  /** 任务内单调递增，从 1 开始 */
  seq: number;
  /** ISO 时间 */
  ts: string;
  taskId: string;
  /** 任务属于哪个工作区（Spec 0006；旧日志里没有这个字段） */
  workspaceId?: string;
}

/** 模型引用见 `settings.ts` 的 ModelRef（设置里的连接名 + 连接列出的模型 ID）。 */
import type { ModelRef } from "./settings";

export type TaskEvent = EventBase &
  (
    | {
        type: "task_created";
        employee: string;
        configHash: string;
        form: Record<string, unknown>;
        attachments: AttachmentRef[];
        /** 任务发起时的会话模型 */
        model?: ModelRef;
      }
    | {
        type: "model_switched";
        /** 切换后的模型 */
        model: ModelRef;
        /** 切换前的模型 */
        fromModel?: ModelRef;
      }
    | { type: "form_updated"; form: Record<string, unknown>; attachments: AttachmentRef[]; actor: Role }
    | { type: "state_changed"; from: TaskState; to: TaskState; trigger: TransitionTrigger; actor: Role; pause?: PauseReason }
    | { type: "transition_rejected"; from: TaskState; trigger: TransitionTrigger; actor: Role; reason: string }
    | { type: "step_started"; stepId: string; attempt: number; reworkRound: number }
    | { type: "step_completed"; stepId: string; reworkRound: number; outputPath: string; outputSha256: string }
    | { type: "step_failed"; stepId: string; error: string }
    | { type: "tool_called"; stepId: string; callId: string; tool: string; args: unknown; attempt: number }
    | { type: "tool_result"; callId: string; ok: boolean; error?: string; resultSha256?: string }
    | { type: "tool_denied"; stepId: string; tool: string; reason: string }
    | {
        type: "model_called";
        stepId: string;
        turn: number;
        /** 完整的 assistant 消息，断点恢复时按日志重放，不重新调用模型 */
        response: unknown;
        usage?: { input: number; output: number };
        durationMs: number;
        /** 这次调用用的连接；剧本模式没有 */
        connection?: string;
        /** 这次调用用的模型名；剧本模式没有 */
        modelName?: string;
      }
    | {
        type: "model_delta";
        /** 判断步骤的模型输出增量：只推送给正在看的人，不落盘，seq 恒为 0，重连后不补发 */
        stepId: string;
        /** 第几轮（同一步骤内从 0 开始） */
        turn: number;
        /** 这一段文字 */
        text: string;
      }
    | { type: "qc_result"; attempt: number; passed: boolean; checks: QcCheckResult[] }
    | { type: "review_decision"; decision: "approve" | "return"; comment?: string; sections?: string[] }
    | { type: "deliverable_created"; kind: "docx" | "md" | "qc_record"; path: string; sha256: string }
    | {
        type: "deliverable_filed";
        /** 复制到工作区“交付”文件夹里的文件；写不进去时为空 */
        filedPath?: string;
        /** 给业务人员看的说明（成功时是目标路径，失败时是原因） */
        filedReason?: string;
        /** 写不进去时的原因 */
        filedError?: string;
      }
    | { type: "fault_injected"; target: string; mode: string }
  );

export type TaskEventType = TaskEvent["type"];
