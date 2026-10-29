// T6 实现：判断步骤执行器（pi Agent + 白名单 + 上限 + submit_result）与模型提供者工厂。
import type { JudgmentRunner, ModelProvider, ModelSettings } from "../contract";

/** 剧本式假模型的一轮：要么输出文本，要么发起工具调用。 */
export type ScriptedTurn =
  | { text: string }
  | { toolCalls: { name: string; arguments: Record<string, unknown> }[] }
  | { error: string }
  | { delayMs: number; then: ScriptedTurn };

/** 按步骤 id 分组的剧本；同一步骤被重跑时按顺序消费后续轮次。 */
export type ModelScript = Record<string, ScriptedTurn[]>;

export function createJudgmentRunner(): JudgmentRunner {
  throw new Error("not implemented (T6)");
}

export function createModelProvider(_settings: ModelSettings): ModelProvider {
  throw new Error("not implemented (T6)");
}

export function createScriptedModelProvider(_script: ModelScript): ModelProvider {
  throw new Error("not implemented (T6)");
}
