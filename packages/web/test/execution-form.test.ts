// 接缝 D（web 纯函数）：执行与预算分区里“退避数组”的解析与校验。
// 断言外部可观察行为：输入文本 → 解析结果或中文错误提示；不碰组件与表单状态。
import { describe, expect, test } from "bun:test";
import { formatBackoffInput, parseBackoffInput, validateExecutionForm } from "../src/view/execution-form";

describe("parseBackoffInput", () => {
  test("逗号分隔的毫秒数解析成数组（允许空格与中英文逗号）", () => {
    expect(parseBackoffInput("500, 1500, 4000")).toEqual({ ok: true, values: [500, 1500, 4000] });
    expect(parseBackoffInput("500，1500")).toEqual({ ok: true, values: [500, 1500] });
    expect(parseBackoffInput("  200  ")).toEqual({ ok: true, values: [200] });
  });

  test("0 合法（立即重试）", () => {
    expect(parseBackoffInput("0, 1000")).toEqual({ ok: true, values: [0, 1000] });
  });

  test("空输入给中文提示，不返回数组", () => {
    const result = parseBackoffInput("   ");
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.message).toContain("至少");
  });

  test("非数字指出第几项", () => {
    const result = parseBackoffInput("500, abc, 4000");
    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.message).toContain("第 2 项");
      expect(result.message).toContain("数字");
    }
  });

  test("负数与小数都不合法", () => {
    expect(parseBackoffInput("500, -1").ok).toBe(false);
    expect(parseBackoffInput("500, 1.5").ok).toBe(false);
  });

  test("单项超过 600000 毫秒（10 分钟）不合法", () => {
    const result = parseBackoffInput("500, 600001");
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.message).toContain("600000");
  });

  test("最多 10 项", () => {
    const result = parseBackoffInput(Array.from({ length: 11 }, () => "100").join(","));
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.message).toContain("10");
  });
});

describe("formatBackoffInput", () => {
  test("数组格式化成逗号分隔文本，可再次解析回同一个数组", () => {
    const text = formatBackoffInput([500, 1500, 4000]);
    expect(text).toBe("500, 1500, 4000");
    expect(parseBackoffInput(text)).toEqual({ ok: true, values: [500, 1500, 4000] });
  });

  test("空数组格式化成空串", () => {
    expect(formatBackoffInput([])).toBe("");
  });
});

describe("validateExecutionForm", () => {
  const valid = { stepTimeoutMs: 120000, modelMaxRetries: 2, backoffText: "500, 1500, 4000", qcAutofix: 3 };

  test("合法表单产出可直接 PATCH 的 execution 分区", () => {
    expect(validateExecutionForm(valid)).toEqual({
      ok: true,
      patch: { stepTimeoutMs: 120000, modelMaxRetries: 2, retryBackoffMs: [500, 1500, 4000], qcAutofix: 3 },
    });
  });

  test("步骤超时越界给中文提示，不产出 patch", () => {
    const result = validateExecutionForm({ ...valid, stepTimeoutMs: 0 });
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.message).toContain("1000");
  });

  test("重试次数必须是 0–10 的整数", () => {
    expect(validateExecutionForm({ ...valid, modelMaxRetries: 11 }).ok).toBe(false);
    expect(validateExecutionForm({ ...valid, modelMaxRetries: 1.5 }).ok).toBe(false);
    expect(validateExecutionForm({ ...valid, modelMaxRetries: 0 }).ok).toBe(true);
  });

  test("质检自动修正上限必须是 0–10 的整数", () => {
    expect(validateExecutionForm({ ...valid, qcAutofix: -1 }).ok).toBe(false);
    expect(validateExecutionForm({ ...valid, qcAutofix: 10 }).ok).toBe(true);
  });

  test("退避数组非法时把解析错误原样带出来", () => {
    const result = validateExecutionForm({ ...valid, backoffText: "500, abc" });
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.message).toContain("第 2 项");
  });

  test("没填的数值给提示（null）", () => {
    const result = validateExecutionForm({ ...valid, stepTimeoutMs: null });
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.message).toContain("步骤超时");
  });
});
