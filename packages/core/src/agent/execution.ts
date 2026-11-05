// 执行参数的三级解析（Spec 0004「执行与预算」）。
//
// 每个值都由“更靠近步骤的配置”优先，其次才是设置文档里的默认值，最后是内置值：
//   步骤超时     step.limits.durationSec       → settings.execution.stepTimeoutMs  → 120s
//   模型重试     连接上的 maxRetries            → settings.execution.modelMaxRetries → 2 次
//   质检自动修正 workflow.limits.qcAutofix      → settings.execution.qcAutofix      → 3 次
//   工具退避     workflow.limits.toolRetry.backoffMs（非空）→ settings.execution.retryBackoffMs → [500,1500,4000]
// 解析成纯函数，引擎只负责把设置读出来，运行器负责真正使用。

import { DEFAULT_EXECUTION_SETTINGS, type ExecutionSettings } from "../contract";

/** 内置步骤超时：设置文件缺失或没配时的兜底。 */
export const BUILTIN_STEP_TIMEOUT_MS = 120_000;
/** 内置模型重试次数（与 pi-ai 客户端默认值一致）。 */
export const BUILTIN_MODEL_MAX_RETRIES = 2;
/** 内置质检自动修正上限（Spec 0004）。 */
export const BUILTIN_QC_AUTOFIX = 3;

type ExecutionDefaults = Partial<ExecutionSettings> | undefined;

function positiveNumber(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) && value > 0 ? value : undefined;
}

function nonNegativeInt(value: unknown): number | undefined {
  return typeof value === "number" && Number.isInteger(value) && value >= 0 ? value : undefined;
}

/** 步骤超时（毫秒）：步骤配置 > 设置默认 > 内置 120 秒；非法值视作没配置。 */
export function resolveStepTimeoutMs(stepTimeoutMs: number | undefined, execution: ExecutionDefaults): number {
  return positiveNumber(stepTimeoutMs) ?? positiveNumber(execution?.stepTimeoutMs) ?? BUILTIN_STEP_TIMEOUT_MS;
}

/** 模型调用重试次数：连接配置 > 设置默认 > 内置 2 次；0 表示不重试，是合法值。 */
export function resolveModelMaxRetries(connectionMaxRetries: number | undefined, execution: ExecutionDefaults): number {
  return nonNegativeInt(connectionMaxRetries) ?? nonNegativeInt(execution?.modelMaxRetries) ?? BUILTIN_MODEL_MAX_RETRIES;
}

/** 质检自动修正上限：员工配置 > 设置默认 > 内置 3 次；0 表示不自动修正。 */
export function resolveQcAutofix(workflowQcAutofix: number | undefined, execution: ExecutionDefaults): number {
  return nonNegativeInt(workflowQcAutofix) ?? nonNegativeInt(execution?.qcAutofix) ?? BUILTIN_QC_AUTOFIX;
}

/**
 * 工具重试退避（毫秒数组）：员工配置写了非空数组就以它为准；
 * 空数组（或没写）时回落到设置默认值，最后是内置值。
 */
export function resolveToolRetryBackoffMs(workflowBackoffMs: readonly number[] | undefined, execution: ExecutionDefaults): number[] {
  const fromWorkflow = workflowBackoffMs && workflowBackoffMs.length > 0 ? workflowBackoffMs : undefined;
  const fromSettings =
    execution?.retryBackoffMs && execution.retryBackoffMs.length > 0 ? execution.retryBackoffMs : undefined;
  return [...(fromWorkflow ?? fromSettings ?? DEFAULT_EXECUTION_SETTINGS.retryBackoffMs)];
}
