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

/** 带高级参数的连接：每模型窗口/上限/默认档位，连接级超时与重试。 */
const advanced: ModelSettings = {
  connections: [
    {
      name: "deepseek",
      baseUrl: "https://api.deepseek.com/v1",
      apiKey: "sk-test-not-real",
      models: ["deepseek-chat", "deepseek-reasoner"],
      modelOptions: {
        "deepseek-reasoner": { contextWindow: 65_536, maxTokens: 4_096, defaultReasoningEffort: "high" },
        "deepseek-chat": { maxTokens: 2_048 },
      },
      timeoutMs: 45_000,
      maxRetries: 4,
    },
  ],
  defaultConnection: "deepseek",
  defaultModel: "deepseek-reasoner",
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

  test("没有高级参数时，上下文窗口 / 输出上限 / 档位保持现状（128k / 8192 / off）", () => {
    const r = provider.resolve("write_report", { connection: "default", model: "default" });
    expect(r.model).toMatchObject({ contextWindow: 128_000, maxTokens: 8_192, reasoning: false });
    expect(r.thinkingLevel).toBe("off");
    expect(r.timeoutMs).toBeUndefined();
    expect(r.maxRetries).toBeUndefined();
  });
});

describe("createModelProvider（每模型高级参数）", () => {
  const provider = createModelProvider(advanced) as JudgmentModelProvider;

  test("modelOptions 的 contextWindow / maxTokens 进入 pi Model，其它模型不受影响", () => {
    const reasoner = provider.resolve("match_causes", { connection: "default", model: "deepseek-reasoner" });
    expect(reasoner.model).toMatchObject({ id: "deepseek-reasoner", contextWindow: 65_536, maxTokens: 4_096 });
    // 同一连接下的另一个模型只配了输出上限，窗口沿用缺省
    const chat = provider.resolve("write_report", { connection: "default", model: "deepseek-chat" });
    expect(chat.model).toMatchObject({ contextWindow: 128_000, maxTokens: 2_048 });
  });

  test("推理档位：步骤 thinking 覆盖连接默认档位；都没配时 off", () => {
    expect(provider.resolve("a", { connection: "default", model: "deepseek-reasoner" }).thinkingLevel).toBe("high");
    expect(provider.resolve("b", { connection: "default", model: "deepseek-reasoner", thinking: "low" }).thinkingLevel).toBe("low");
    expect(provider.resolve("c", { connection: "default", model: "deepseek-chat" }).thinkingLevel).toBe("off");
    // 未知档位按 off 处理（设置文件手改坏值也不能把请求发坏）
    expect(provider.resolve("d", { connection: "default", model: "deepseek-chat", thinking: "turbo" }).thinkingLevel).toBe("off");
  });

  test("配置了推理档位的模型把 reasoning 标为可用；没配的保持不支持", () => {
    expect(provider.resolve("a", { connection: "default", model: "deepseek-reasoner" }).model.reasoning).toBe(true);
    expect(provider.resolve("b", { connection: "default", model: "deepseek-chat" }).model.reasoning).toBe(false);
  });

  test("步骤显式要求思考时，即使模型没配高级参数也标为支持推理", () => {
    const plain = createModelProvider(settings) as JudgmentModelProvider;
    expect(plain.resolve("a", { connection: "default", model: "deepseek-chat", thinking: "medium" }).model.reasoning).toBe(true);
  });

  test("resolve 带出连接级超时与重试", () => {
    const r = provider.resolve("a", { connection: "default", model: "deepseek-reasoner" });
    expect(r.timeoutMs).toBe(45_000);
    expect(r.maxRetries).toBe(4);
  });
});
