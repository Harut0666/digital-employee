// 真实模型提供者：用 pi-ai 的 openai-completions 自定义模型接 DeepSeek 等 OpenAI 兼容接口。
// 连接的 baseUrl、apiKey 来自本机设置文件（ModelSettings），不进仓库。
// 另提供“测试连接”：发一条极短请求，只报告可用性、延迟与错误（错误里不包含 API Key）。

import { completeSimple, streamSimple, type Model } from "@mariozechner/pi-ai";
import type { ModelConnection, ModelOptions, ModelProvider, ModelConnectionTest, ModelSettings } from "../contract";
import { normalizeThinkingLevel, type JudgmentModelProvider, type ModelRef } from "./provider";

const PROBE_TIMEOUT_MS = 15_000;
const PROBE_MAX_TOKENS = 8;

/** 缺省上下文窗口 / 输出上限：没配高级参数时行为与 T20 之前一致。 */
export const DEFAULT_CONTEXT_WINDOW = 128_000;
export const DEFAULT_MAX_TOKENS = 8_192;

/** 这个模型是否声明了推理能力：配了默认档位或可用档位列表即视为支持。 */
function declaresReasoning(options: ModelOptions | undefined): boolean {
  if (!options) return false;
  return options.defaultReasoningEffort !== undefined || (options.reasoningEfforts?.length ?? 0) > 0;
}

function toPiModel(connection: ModelConnection, modelId: string, reasoning: boolean): Model<"openai-completions"> {
  // 每模型参数（Spec 0004）：按模型 id 取，缺省沿用 128k / 8192
  const options = connection.modelOptions?.[modelId];
  return {
    id: modelId,
    name: modelId,
    api: "openai-completions",
    provider: connection.name,
    baseUrl: connection.baseUrl,
    reasoning,
    input: ["text"],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: options?.contextWindow ?? DEFAULT_CONTEXT_WINDOW,
    maxTokens: options?.maxTokens ?? DEFAULT_MAX_TOKENS,
  };
}

/** 把错误信息里可能出现的 API Key 换成 ***，密钥永远不进响应、日志或错误信息。 */
export function redactApiKey(text: string, apiKey: string | undefined): string {
  if (!apiKey) return text;
  return text.split(apiKey).join("***");
}

export function createModelProvider(settings: ModelSettings): ModelProvider {
  const provider: JudgmentModelProvider = {
    kind: "real",
    resolve(_stepId, ref: ModelRef) {
      const connectionName = ref.connection === "default" ? settings.defaultConnection : ref.connection;
      const connection = settings.connections.find((c) => c.name === connectionName);
      if (!connection) throw new Error(`未找到模型连接“${connectionName}”`);
      const modelId = ref.model === "default" ? settings.defaultModel : ref.model;
      // 档位：步骤 thinking → 连接上的模型默认档位 → off（未知值按 off）
      const options = connection.modelOptions?.[modelId];
      const thinkingLevel = normalizeThinkingLevel(ref.thinking ?? options?.defaultReasoningEffort);
      const reasoning = thinkingLevel !== "off" || declaresReasoning(options);
      return {
        model: toPiModel(connection, modelId, reasoning),
        streamFn: streamSimple,
        apiKey: connection.apiKey,
        thinkingLevel,
        ...(connection.timeoutMs !== undefined ? { timeoutMs: connection.timeoutMs } : {}),
        ...(connection.maxRetries !== undefined ? { maxRetries: connection.maxRetries } : {}),
      };
    },
  };
  return provider;
}

/**
 * 测试连接：用该连接发一条极短的对话请求（最多 8 个 token），返回是否可用与延迟。
 * 不做重试：测试连接要的是“现在能不能用”，重试会掩盖问题。
 */
export async function testModelConnection(
  connection: ModelConnection,
  modelId: string = connection.models[0] ?? "",
  timeoutMs: number = PROBE_TIMEOUT_MS,
): Promise<ModelConnectionTest> {
  const started = Date.now();
  if (!modelId) {
    return { ok: false, latencyMs: 0, model: modelId, error: "该连接没有配置模型" };
  }
  try {
    const message = await completeSimple(
      toPiModel(connection, modelId, declaresReasoning(connection.modelOptions?.[modelId])),
      { messages: [{ role: "user", content: "请只回复 OK", timestamp: Date.now() }] },
      { apiKey: connection.apiKey, maxTokens: PROBE_MAX_TOKENS, signal: AbortSignal.timeout(timeoutMs) },
    );
    if (message.stopReason === "error" || message.stopReason === "aborted") {
      return {
        ok: false,
        latencyMs: Date.now() - started,
        model: modelId,
        error: redactApiKey(message.errorMessage ?? "模型返回错误", connection.apiKey),
      };
    }
    return { ok: true, latencyMs: Date.now() - started, model: modelId };
  } catch (error) {
    return {
      ok: false,
      latencyMs: Date.now() - started,
      model: modelId,
      error: redactApiKey(error instanceof Error ? error.message : String(error), connection.apiKey),
    };
  }
}
