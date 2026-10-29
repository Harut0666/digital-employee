import { describe, expect, test } from "bun:test";
import { createModelProvider, type JudgmentModelProvider } from "../../src/agent";
import type { ModelSettings } from "../../src/contract";

const settings: ModelSettings = {
  connections: [
    { name: "deepseek", baseUrl: "https://api.deepseek.com/v1", apiKey: "sk-test-not-real", models: ["deepseek-chat", "deepseek-reasoner"] },
    { name: "local", baseUrl: "http://localhost:11434/v1", apiKey: "none", models: ["qwen"] },
  ],
  defaultConnection: "deepseek",
  defaultModel: "deepseek-chat",
};

// 只检查构造出的 pi 模型，不发起任何请求。
describe("createModelProvider（真实模型，仅检查形状）", () => {
  const provider = createModelProvider(settings) as JudgmentModelProvider;

  test("kind 为 real；default 连接和模型取自 settings", () => {
    expect(provider.kind).toBe("real");
    const r = provider.resolve("write_report", { connection: "default", model: "default" });
    expect(r.model).toMatchObject({ id: "deepseek-chat", api: "openai-completions", baseUrl: "https://api.deepseek.com/v1", provider: "deepseek" });
    expect(r.apiKey).toBe("sk-test-not-real");
    expect(typeof r.streamFn).toBe("function");
  });

  test("步骤可以指定连接和模型", () => {
    const r = provider.resolve("match_causes", { connection: "local", model: "qwen" });
    expect(r.model).toMatchObject({ id: "qwen", baseUrl: "http://localhost:11434/v1", provider: "local" });
    expect(r.apiKey).toBe("none");
  });

  test("连接不存在时抛出清楚的错误", () => {
    expect(() => provider.resolve("x", { connection: "nope", model: "default" })).toThrow("nope");
  });
});
