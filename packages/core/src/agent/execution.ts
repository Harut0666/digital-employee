// 执行参数的三级解析（Spec 0004「执行与预算」）。
//
// 两个值都由“更靠近步骤的配置”优先，其次才是设置文档里的默认值，最后是内置值：
//   步骤超时   step.limits.durationSec → settings.execution.stepTimeoutMs → 120s
//   模型重试   连接上的 maxRetries      → settings.execution.modelMaxRetries → 2 次
// 解析成纯函数，引擎只负责把设置读出来，运行器负责真正使用。

import { DEFAULT_EXECUTION_SETTINGS, type ExecutionSettings } from "../contract";

/** 内置步骤超时：设置文件缺失或没配时的兜底。 */
export const BUILTIN_STEP_TIMEOUT_MS = 120_000;
/** 内置模型重试次数（与 pi-ai 客户端默认值一致）。 */
export const BUILTIN_MODEL_MAX_RETRIES = 2;

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

/** 设置文档缺失或读失败时的执行默认值（引擎读盘失败不影响任务本身）。 */
export const FALLBACK_EXECUTION_SETTINGS: ExecutionSettings = { ...DEFAULT_EXECUTION_SETTINGS, retryBackoffMs: [...DEFAULT_EXECUTION_SETTINGS.retryBackoffMs] };
