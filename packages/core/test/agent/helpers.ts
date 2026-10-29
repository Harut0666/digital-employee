import type { JudgeStepConfig, JudgmentStepInput, ToolCallResult, ToolClient, ToolDescriptor } from "../../src/contract";

export const OUTPUT_SCHEMA = {
  type: "object",
  properties: { answer: { type: "string" } },
  required: ["answer"],
  additionalProperties: false,
};

export function makeStep(overrides: Partial<JudgeStepConfig> = {}): JudgeStepConfig {
  return {
    id: "s1",
    label: "测试判断步骤",
    kind: "judge",
    prompt: "prompts/s1.md",
    input: {},
    tools: ["query_task"],
    outputSchema: "schemas/s1.json",
    limits: { rounds: 6, durationSec: 90 },
    model: { connection: "default", model: "default" },
    rework: false,
    when: "always",
    optional: false,
    ...overrides,
  };
}

export interface FakeToolClient extends ToolClient {
  calls: { name: string; args: unknown }[];
}

/** 假工具客户端：只记录调用，不碰网络。 */
export function makeToolClient(handlers: Record<string, (args: unknown) => ToolCallResult> = {}): FakeToolClient {
  const descriptors: ToolDescriptor[] = ["query_task", "web_search", "load_sales"].map((name) => ({
    name,
    description: `${name} 工具`,
    inputSchema: { type: "object", additionalProperties: true },
  }));
  const calls: { name: string; args: unknown }[] = [];
  return {
    calls,
    async listTools() {
      return descriptors;
    },
    async call(name, args) {
      calls.push({ name, args });
      const handler = handlers[name];
      return handler ? handler(args) : { ok: true, data: { echo: name } };
    },
    async close() {},
  };
}

export interface Recorder {
  modelCalled: Parameters<JudgmentStepInput["onModelCalled"]>[0][];
  toolCalled: Parameters<JudgmentStepInput["onToolCalled"]>[0][];
  toolDenied: Parameters<JudgmentStepInput["onToolDenied"]>[0][];
}

export function makeInput(
  base: Pick<JudgmentStepInput, "model"> & Partial<JudgmentStepInput>,
): { input: JudgmentStepInput; rec: Recorder; toolClient: FakeToolClient } {
  const rec: Recorder = { modelCalled: [], toolCalled: [], toolDenied: [] };
  const toolClient = (base.toolClient as FakeToolClient | undefined) ?? makeToolClient();
  const input: JudgmentStepInput = {
    taskId: "t1",
    step: makeStep(),
    systemPrompt: "你是经营周报员工。",
    context: { period: { year: 2026, week: 44 } },
    toolClient,
    outputSchema: OUTPUT_SCHEMA,
    budget: { remainingModelCalls: 30, deadline: Date.now() + 600_000 },
    replay: [],
    async onModelCalled(e) {
      rec.modelCalled.push(e);
    },
    async onToolCalled(e) {
      rec.toolCalled.push(e);
    },
    async onToolDenied(e) {
      rec.toolDenied.push(e);
    },
    ...base,
  };
  return { input, rec, toolClient };
}

export const submit = (answer: unknown) => ({ toolCalls: [{ name: "submit_result", arguments: { answer } }] });
export const callTool = (name: string, args: Record<string, unknown> = {}) => ({ toolCalls: [{ name, arguments: args }] });
