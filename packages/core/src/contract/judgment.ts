// 后端引擎内部的两块拆分接口：编排器（engine）调用判断步骤执行器和质检引擎。
// 二者分属不同 ticket 并行开发，在这里约定边界。

import type { JudgeStepConfig, QcConfig } from "./employee-config";
import type { ModelProvider, ToolClient } from "./engine";
import type { WeeklyFacts } from "./facts";
import type { QcCheckResult } from "./qc";
import type { DetectedContradiction, ParseMinutesOutput } from "./tools";

export type JudgmentFailure =
  | "step_rounds"
  | "step_duration"
  | "task_calls"
  | "task_duration"
  | "model_error"
  | "no_output"
  | "invalid_output";

export interface JudgmentStepInput {
  taskId: string;
  step: JudgeStepConfig;
  /** SKILL.md + 步骤提示词，已拼好 */
  systemPrompt: string;
  /** 已解析的 step.input，序列化后作为用户消息 */
  context: Record<string, unknown>;
  /** 质检不通过或退回时附加的修正要求 */
  corrections?: string[];
  toolClient: ToolClient;
  model: ModelProvider;
  /** 步骤输出必须符合此 JSON Schema，经 submit_result 工具提交 */
  outputSchema: Record<string, unknown>;
  /** 任务级剩余预算 */
  budget: { remainingModelCalls: number; deadline: number };
  /** 断点恢复：本步骤已记录的模型响应，按顺序重放而不调用模型 */
  replay: unknown[];
  signal?: AbortSignal;
  /** 每次模型调用完成后回调，引擎据此写 model_called 事件 */
  onModelCalled(event: { turn: number; response: unknown; usage?: { input: number; output: number }; durationMs: number }): Promise<void>;
  onToolCalled(event: { tool: string; args: unknown; ok: boolean; error?: string }): Promise<void>;
  onToolDenied(event: { tool: string; reason: string }): Promise<void>;
}

export type JudgmentStepResult =
  | { ok: true; output: unknown; modelCalls: number }
  | { ok: false; failure: JudgmentFailure; message: string; modelCalls: number };

export interface JudgmentRunner {
  run(input: JudgmentStepInput): Promise<JudgmentStepResult>;
}

/** 判断步骤写出的报告草稿：叙述中只含槽位 {{path}}，不含裸数字（ADR-0002）。 */
export interface DraftReport {
  title: string;
  /** 段落；章节可能只有要点（如品类表现、下周关注），此时省略 paragraphs */
  sections: { id: string; heading: string; paragraphs?: string[]; bullets?: string[] }[];
  /** 原因匹配结果，供“异常覆盖”“引用真实”两项质检使用 */
  causes: {
    anomalyId: string;
    cause: string;
    level: "store" | "store_type" | "region" | "company" | "unknown";
    sourceWeek?: { year: number; week: number };
    quote?: string;
  }[];
  /** 重做时的修改说明 */
  revisionNotes?: { comment: string; sections: string[]; change: string }[];
  /** 引用的纪要矛盾 id（来自 detect_contradictions 的输出），供“纪要矛盾覆盖”质检核对 */
  contradictionRefs?: string[];
}

export interface QcInput {
  config: QcConfig;
  draft: DraftReport;
  facts: WeeklyFacts;
  minutes: ParseMinutesOutput;
  /** 程序判出的纪要矛盾（detect_contradictions 步骤的输出）；缺省视为没有矛盾 */
  contradictions?: DetectedContradiction[];
  /** 渲染后的统计，由 render_report_docx 返回 */
  rendered?: { estimatedPages: number; chartCount: number };
  /** 重做时的上一版草稿和退回意见 */
  previousDraft?: DraftReport;
  feedback?: { comment: string; sections?: string[] };
  /** 各类问题应重跑的步骤 id（来自 workflow.qcFixSteps）；缺省为场景 A 的 write_report / match_causes */
  fixSteps?: { write: string; causes: string };
}

export interface QcEngine {
  run(input: QcInput): QcCheckResult[];
}

/** 槽位渲染：把 {{path}} 替换为格式化后的事实值；unresolved 为无法解析的槽位 */
export type RenderSlots = (text: string, facts: WeeklyFacts) => { text: string; unresolved: string[] };
