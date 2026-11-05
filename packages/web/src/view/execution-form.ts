// 执行与预算分区的表单解析与校验（接缝 D：纯函数，web 单测直接调用）。
//
// 表单只是 settings.json 的策展子集：这里把输入文本变成可 PATCH 的 `execution` 分区，
// 范围与枚举一律以 @de/core 的契约为准（不复制一套自己的数字）；
// 服务端仍会再校验一次，非法时 400 且不写文件——所以这里失败就“不发出请求”。
import {
  MODEL_RETRIES_MAX,
  QC_AUTOFIX_MAX,
  RETRY_BACKOFF_ITEM_MAX_MS,
  RETRY_BACKOFF_MAX,
  STEP_TIMEOUT_MAX_MS,
  STEP_TIMEOUT_MIN_MS,
  type ExecutionSettings,
} from "@de/core/contract";

/** 可直接放进 `PATCH /api/settings/app` 的 `execution` 分区。 */
export type ExecutionPatch = ExecutionSettings;

export type ParsedBackoff = { ok: true; values: number[] } | { ok: false; message: string };

const ITEM_MAX_LABEL = `${RETRY_BACKOFF_ITEM_MAX_MS}`;

/**
 * 把“500, 1500, 4000”这样的文本解析成毫秒数组。
 * 非法输入返回中文提示（指出第几项），调用方据此提示并**不写文件**。
 */
export function parseBackoffInput(text: string): ParsedBackoff {
  const normalized = text.replace(/[，、]/g, ",").trim();
  if (normalized === "") return { ok: false, message: "退避间隔至少要填一项（毫秒）" };
  const parts = normalized.split(",").map((part) => part.trim());
  if (parts.length > RETRY_BACKOFF_MAX) {
    return { ok: false, message: `退避间隔最多 ${RETRY_BACKOFF_MAX} 项（对应第 N 次重试取第 N 项）` };
  }
  const values: number[] = [];
  for (const [index, part] of parts.entries()) {
    const position = index + 1;
    if (part === "") return { ok: false, message: `第 ${position} 项是空的（不要有多余的逗号）` };
    if (!/^\d+$/.test(part)) return { ok: false, message: `第 ${position} 项必须是整数毫秒数字：${part}` };
    const value = Number(part);
    if (!Number.isSafeInteger(value)) return { ok: false, message: `第 ${position} 项太大：${part}` };
    if (value > RETRY_BACKOFF_ITEM_MAX_MS) {
      return { ok: false, message: `第 ${position} 项不能超过 ${ITEM_MAX_LABEL} 毫秒（10 分钟）` };
    }
    values.push(value);
  }
  return { ok: true, values };
}

/** 数组 → 逗号分隔文本（与 parseBackoffInput 互逆，便于表单回填）。 */
export function formatBackoffInput(values: number[]): string {
  return values.join(", ");
}

export interface ExecutionFormValues {
  /** 表单里没填时为 null（InputNumber 清空） */
  stepTimeoutMs: number | null;
  modelMaxRetries: number | null;
  backoffText: string;
  qcAutofix: number | null;
}

export type ExecutionValidation = { ok: true; patch: ExecutionPatch } | { ok: false; message: string };

/** 校验整张表单：任何一项不合法就返回提示，不产出 patch（因此也不会写文件）。 */
export function validateExecutionForm(values: ExecutionFormValues): ExecutionValidation {
  const { stepTimeoutMs, modelMaxRetries, qcAutofix } = values;
  if (stepTimeoutMs === null || !Number.isInteger(stepTimeoutMs) || stepTimeoutMs < STEP_TIMEOUT_MIN_MS || stepTimeoutMs > STEP_TIMEOUT_MAX_MS) {
    return {
      ok: false,
      message: `步骤超时必须是 ${STEP_TIMEOUT_MIN_MS}–${STEP_TIMEOUT_MAX_MS} 之间的整数（毫秒）`,
    };
  }
  if (modelMaxRetries === null || !Number.isInteger(modelMaxRetries) || modelMaxRetries < 0 || modelMaxRetries > MODEL_RETRIES_MAX) {
    return { ok: false, message: `模型重试次数必须是 0–${MODEL_RETRIES_MAX} 之间的整数` };
  }
  if (qcAutofix === null || !Number.isInteger(qcAutofix) || qcAutofix < 0 || qcAutofix > QC_AUTOFIX_MAX) {
    return { ok: false, message: `质检自动修正上限必须是 0–${QC_AUTOFIX_MAX} 之间的整数` };
  }
  const backoff = parseBackoffInput(values.backoffText);
  if (!backoff.ok) return backoff;
  return {
    ok: true,
    patch: {
      stepTimeoutMs,
      modelMaxRetries,
      retryBackoffMs: backoff.values,
      qcAutofix,
    },
  };
}
