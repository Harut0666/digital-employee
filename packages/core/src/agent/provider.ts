// 模型提供者的内部形状。契约里 ModelProvider 只有 kind，这里在其上加 resolve：
// 判断步骤执行器按“步骤 id + 步骤配置里的模型引用”向提供者要一份可用的 pi 模型和 streamFn。

import type { StreamFn } from "@mariozechner/pi-agent-core";
import { createAssistantMessageEventStream, type AssistantMessage, type AssistantMessageEventStream, type Model } from "@mariozechner/pi-ai";
import type { ModelProvider } from "../contract";

export interface ModelRef {
  connection: string;
  model: string;
}

export interface ResolvedStepModel {
  model: Model<any>;
  /** 交给 pi Agent 的 streamFn；真实模型即 streamSimple，剧本式假模型是内存里的实现 */
  streamFn: StreamFn;
  apiKey?: string;
}

export interface JudgmentModelProvider extends ModelProvider {
  resolve(stepId: string, ref: ModelRef): ResolvedStepModel;
}

export function isJudgmentModelProvider(p: ModelProvider): p is JudgmentModelProvider {
  return typeof (p as Partial<JudgmentModelProvider>).resolve === "function";
}

const ZERO_USAGE = {
  input: 0,
  output: 0,
  cacheRead: 0,
  cacheWrite: 0,
  totalTokens: 0,
  cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
};

export function emptyAssistantMessage(model: Pick<Model<any>, "api" | "provider" | "id">): AssistantMessage {
  return {
    role: "assistant",
    content: [],
    api: model.api,
    provider: model.provider,
    model: model.id,
    usage: { ...ZERO_USAGE, cost: { ...ZERO_USAGE.cost } },
    stopReason: "stop",
    timestamp: Date.now(),
  };
}

/** 以错误或中止结束的流（StreamFn 约定：失败编码进流，不抛异常）。 */
export function failedStream(model: Pick<Model<any>, "api" | "provider" | "id">, reason: "error" | "aborted", errorMessage: string): AssistantMessageEventStream {
  const stream = createAssistantMessageEventStream();
  const message: AssistantMessage = { ...emptyAssistantMessage(model), stopReason: reason, errorMessage };
  stream.push({ type: "error", reason, error: message });
  return stream;
}

/** 以一条完整的助手消息结束的流（剧本式假模型、断点重放共用）。 */
export function completedStream(message: AssistantMessage): AssistantMessageEventStream {
  const stream = createAssistantMessageEventStream();
  stream.push({ type: "start", partial: { ...message, content: [] } });
  if (message.stopReason === "error" || message.stopReason === "aborted") {
    stream.push({ type: "error", reason: message.stopReason, error: message });
  } else {
    stream.push({ type: "done", reason: message.stopReason, message });
  }
  return stream;
}
