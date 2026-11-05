// 执行参数的三级解析（Spec 0004「执行与预算」）：
//   步骤超时：步骤配置 → 设置 execution.stepTimeoutMs → 内置 120s
//   模型重试：步骤引用的连接配置 → 设置 execution.modelMaxRetries → 内置 2
// 期望值直接来自规格里的默认值，不由被测代码推导。
import { describe, expect, test } from "bun:test";
import {
  BUILTIN_MODEL_MAX_RETRIES,
  BUILTIN_QC_AUTOFIX,
  BUILTIN_STEP_TIMEOUT_MS,
  resolveModelMaxRetries,
  resolveQcAutofix,
  resolveStepTimeoutMs,
  resolveToolRetryBackoffMs,
} from "../../src/agent";
import { DEFAULT_EXECUTION_SETTINGS } from "../../src/contract";

describe("步骤超时：步骤配置 → 设置默认 → 内置值", () => {
  test("步骤配置优先于设置默认值", () => {
    expect(resolveStepTimeoutMs(3_000, { ...DEFAULT_EXECUTION_SETTINGS, stepTimeoutMs: 20_000 })).toBe(3_000);
  });

  test("步骤没配置时用设置里的默认值", () => {
    expect(resolveStepTimeoutMs(undefined, { ...DEFAULT_EXECUTION_SETTINGS, stepTimeoutMs: 20_000 })).toBe(20_000);
  });

  test("都没有时用内置 120 秒", () => {
    expect(resolveStepTimeoutMs(undefined, undefined)).toBe(120_000);
    expect(BUILTIN_STEP_TIMEOUT_MS).toBe(120_000);
    // 设置文件缺失/损坏时也不能返回 undefined
    expect(resolveStepTimeoutMs(undefined, {})).toBe(120_000);
  });

  test("非法步骤值（0、负数、NaN）视作没配置，继续往下回落", () => {
    expect(resolveStepTimeoutMs(0, { ...DEFAULT_EXECUTION_SETTINGS, stepTimeoutMs: 20_000 })).toBe(20_000);
    expect(resolveStepTimeoutMs(-5, undefined)).toBe(120_000);
    expect(resolveStepTimeoutMs(Number.NaN, { ...DEFAULT_EXECUTION_SETTINGS, stepTimeoutMs: 20_000 })).toBe(20_000);
  });
});

describe("模型重试次数：连接配置 → 设置默认 → 内置值", () => {
  test("连接上的重试次数优先于设置默认值", () => {
    expect(resolveModelMaxRetries(5, { ...DEFAULT_EXECUTION_SETTINGS, modelMaxRetries: 1 })).toBe(5);
  });

  test("连接没配时用设置里的默认值", () => {
    expect(resolveModelMaxRetries(undefined, { ...DEFAULT_EXECUTION_SETTINGS, modelMaxRetries: 7 })).toBe(7);
  });

  test("都没有时用内置 2 次", () => {
    expect(resolveModelMaxRetries(undefined, undefined)).toBe(2);
    expect(BUILTIN_MODEL_MAX_RETRIES).toBe(2);
    expect(resolveModelMaxRetries(undefined, {})).toBe(2);
  });

  test("0 是合法值（不重试），不被当成没配置", () => {
    expect(resolveModelMaxRetries(0, { ...DEFAULT_EXECUTION_SETTINGS, modelMaxRetries: 7 })).toBe(0);
  });
});

describe("质检自动修正上限：员工配置 → 设置默认 → 内置值", () => {
  test("员工配置写了就以它为准", () => {
    expect(resolveQcAutofix(1, { ...DEFAULT_EXECUTION_SETTINGS, qcAutofix: 8 })).toBe(1);
  });

  test("员工配置没写时用设置里的默认值", () => {
    expect(resolveQcAutofix(undefined, { ...DEFAULT_EXECUTION_SETTINGS, qcAutofix: 5 })).toBe(5);
  });

  test("都没有时用内置 3 次", () => {
    expect(resolveQcAutofix(undefined, undefined)).toBe(3);
    expect(BUILTIN_QC_AUTOFIX).toBe(3);
    expect(resolveQcAutofix(undefined, {})).toBe(3);
  });

  test("0 是合法值（不自动修正）", () => {
    expect(resolveQcAutofix(0, { ...DEFAULT_EXECUTION_SETTINGS, qcAutofix: 5 })).toBe(0);
  });
});

describe("工具重试退避：员工配置（非空）→ 设置默认 → 内置值", () => {
  test("员工配置写了非空数组时就以它为准", () => {
    expect(resolveToolRetryBackoffMs([100, 200], { ...DEFAULT_EXECUTION_SETTINGS, retryBackoffMs: [9_000] })).toEqual([100, 200]);
  });

  test("员工配置是空数组时回落到设置里的默认值", () => {
    expect(resolveToolRetryBackoffMs([], { ...DEFAULT_EXECUTION_SETTINGS, retryBackoffMs: [200, 800] })).toEqual([200, 800]);
  });

  test("员工配置没写、设置也没写时用内置 500 / 1500 / 4000", () => {
    expect(resolveToolRetryBackoffMs(undefined, undefined)).toEqual([500, 1_500, 4_000]);
    expect(resolveToolRetryBackoffMs([], { retryBackoffMs: [] })).toEqual([500, 1_500, 4_000]);
  });

  test("返回副本：调用方改数组不会污染设置文档", () => {
    const settings = [200, 800];
    const resolved = resolveToolRetryBackoffMs([], { ...DEFAULT_EXECUTION_SETTINGS, retryBackoffMs: settings });
    resolved.push(1_600);
    expect(settings).toEqual([200, 800]);
  });
});
