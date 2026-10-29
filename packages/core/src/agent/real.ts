// 真实模型提供者：用 pi-ai 的 openai-completions 自定义模型接 DeepSeek 等 OpenAI 兼容接口。
// 连接的 baseUrl、apiKey 来自本机设置文件（ModelSettings），不进仓库。

import { streamSimple, type Model } from "@mariozechner/pi-ai";
import type { ModelProvider, ModelSettings } from "../contract";
import type { JudgmentModelProvider, ModelRef } from "./provider";

export function createModelProvider(settings: ModelSettings): ModelProvider {
  const provider: JudgmentModelProvider = {
    kind: "real",
    resolve(_stepId, ref: ModelRef) {
      const connectionName = ref.connection === "default" ? settings.defaultConnection : ref.connection;
      const connection = settings.connections.find((c) => c.name === connectionName);
      if (!connection) throw new Error(`未找到模型连接“${connectionName}”`);
      const modelId = ref.model === "default" ? settings.defaultModel : ref.model;
      const model: Model<"openai-completions"> = {
        id: modelId,
        name: modelId,
        api: "openai-completions",
        provider: connection.name,
        baseUrl: connection.baseUrl,
        reasoning: false,
        input: ["text"],
        cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
        contextWindow: 128_000,
        maxTokens: 8_192,
      };
      return { model, streamFn: streamSimple, apiKey: connection.apiKey };
    },
  };
  return provider;
}
