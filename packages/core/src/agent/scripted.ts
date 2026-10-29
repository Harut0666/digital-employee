// 剧本式假模型：按步骤 id 依次返回 ScriptedTurn，经 pi-agent-core 的 streamFn 注入，不发任何网络请求。

import type { StreamFn } from "@mariozechner/pi-agent-core";
import type { AssistantMessage, Context, Model } from "@mariozechner/pi-ai";
import { completedStream, emptyAssistantMessage, failedStream, type JudgmentModelProvider } from "./provider";

/** 剧本式假模型的一轮：要么输出文本，要么发起工具调用。 */
export type ScriptedTurn =
  | { text: string }
  | { toolCalls: { name: string; arguments: Record<string, unknown> }[] }
  | { error: string }
  | { delayMs: number; then: ScriptedTurn };

/** 按步骤 id 分组的剧本；同一步骤被重跑时按顺序消费后续轮次。 */
export type ModelScript = Record<string, ScriptedTurn[]>;

/** 假模型每次被调用时收到的请求，测试用来检查“模型看到了什么”。 */
export interface ScriptedRequest {
  stepId: string;
  context: Context;
}

export interface ScriptedModelProvider extends JudgmentModelProvider {
  readonly kind: "scripted";
  readonly requests: readonly ScriptedRequest[];
}

const SCRIPTED_MODEL: Model<"openai-completions"> = {
  id: "scripted",
  name: "Scripted model",
  api: "openai-completions",
  provider: "scripted",
  baseUrl: "http://scripted.invalid",
  reasoning: false,
  input: ["text"],
  cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
  contextWindow: 128_000,
  maxTokens: 8_192,
};

/** 等待 ms 毫秒；信号中止时提前返回 true。 */
function sleep(ms: number, signal?: AbortSignal): Promise<boolean> {
  return new Promise((resolve) => {
    if (signal?.aborted) return resolve(true);
    const onAbort = () => {
      clearTimeout(timer);
      resolve(true);
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve(false);
    }, ms);
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

export function createScriptedModelProvider(script: ModelScript): ScriptedModelProvider {
  const cursors = new Map<string, number>();
  const requests: ScriptedRequest[] = [];
  let toolCallSeq = 0;

  function buildMessage(turn: Exclude<ScriptedTurn, { delayMs: number }>): AssistantMessage {
    const base = emptyAssistantMessage(SCRIPTED_MODEL);
    if ("text" in turn) return { ...base, content: [{ type: "text", text: turn.text }] };
    if ("error" in turn) return { ...base, stopReason: "error", errorMessage: turn.error };
    return {
      ...base,
      stopReason: "toolUse",
      content: turn.toolCalls.map((c) => ({ type: "toolCall" as const, id: `scripted_call_${++toolCallSeq}`, name: c.name, arguments: c.arguments })),
    };
  }

  function streamFor(stepId: string): StreamFn {
    return (model, context, options) => {
      requests.push({ stepId, context: { ...context, messages: [...context.messages], tools: context.tools ? [...context.tools] : undefined } });
      const index = cursors.get(stepId) ?? 0;
      cursors.set(stepId, index + 1);
      let turn: ScriptedTurn | undefined = script[stepId]?.[index];
      if (turn === undefined) return failedStream(model, "error", `剧本已用完：步骤 ${stepId} 第 ${index + 1} 轮没有预设响应`);

      if (!("delayMs" in turn)) return completedStream(buildMessage(turn));

      // 带延迟的一轮：等待期间若被中止，按 pi 约定以 aborted 结束。
      const signal = options?.signal;
      return (async () => {
        while (turn !== undefined && "delayMs" in turn) {
          if (await sleep(turn.delayMs, signal)) return failedStream(model, "aborted", "请求已中止");
          turn = turn.then;
        }
        return completedStream(buildMessage(turn as Exclude<ScriptedTurn, { delayMs: number }>));
      })();
    };
  }

  return {
    kind: "scripted",
    requests,
    resolve: (stepId) => ({ model: SCRIPTED_MODEL, streamFn: streamFor(stepId) }),
  };
}
