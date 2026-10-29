// 判断步骤执行器（docs/design.md §6）：每次 run 新建一个 pi Agent，只开放白名单工具加 submit_result。

import { Agent, type AgentTool, type StreamFn } from "@mariozechner/pi-agent-core";
import type { AssistantMessage, Tool, TSchema } from "@mariozechner/pi-ai";
import { Type } from "typebox";
import Ajv from "ajv";
import type { JudgmentFailure, JudgmentRunner, JudgmentStepInput, JudgmentStepResult, ToolDescriptor } from "../contract";
import { completedStream, failedStream, isJudgmentModelProvider } from "./provider";

export const SUBMIT_TOOL = "submit_result";

type Submission = { valid: true; output: unknown } | { valid: false; errors: string };

const ajv = new Ajv({ allErrors: true, strict: false });

function text(value: string) {
  return [{ type: "text" as const, text: value }];
}

function describeContext(context: Record<string, unknown>, corrections: string[] | undefined): string {
  const parts = ["## 任务上下文（JSON）", "```json", JSON.stringify(context, null, 2), "```"];
  if (corrections && corrections.length > 0) {
    parts.push("", "## 修正要求（上一版未通过检查，请逐条处理）", ...corrections.map((c, i) => `${i + 1}. ${c}`));
  }
  parts.push("", `完成后必须调用 ${SUBMIT_TOOL} 工具提交结果，不要只回复文字。`);
  return parts.join("\n");
}

function lastText(message: AssistantMessage): string {
  return message.errorMessage ?? "模型调用失败";
}

export function createJudgmentRunner(): JudgmentRunner {
  return { run };
}

async function run(input: JudgmentStepInput): Promise<JudgmentStepResult> {
  let realCalls = 0;
  const fail = (failure: JudgmentFailure, message: string): JudgmentStepResult => ({ ok: false, failure, message, modelCalls: realCalls });

  if (!isJudgmentModelProvider(input.model)) return fail("model_error", "model 不是由 createModelProvider / createScriptedModelProvider 创建的");
  let resolved;
  try {
    resolved = input.model.resolve(input.step.id, input.step.model);
  } catch (e) {
    return fail("model_error", e instanceof Error ? e.message : String(e));
  }

  let validate;
  try {
    validate = ajv.compile(input.outputSchema);
  } catch (e) {
    return fail("invalid_output", `输出 Schema 无法编译：${e instanceof Error ? e.message : String(e)}`);
  }

  let descriptors: ToolDescriptor[];
  try {
    descriptors = await input.toolClient.listTools();
  } catch (e) {
    return fail("model_error", `读取工具列表失败：${e instanceof Error ? e.message : String(e)}`);
  }

  const { step } = input;
  const whitelist = new Set(step.tools);
  const rounds = step.limits.rounds;

  // 运行期状态
  let turns = 0;
  let failure: { kind: JudgmentFailure; message: string } | undefined;
  let submission: Submission | undefined;
  let modelError: string | undefined;
  const denied = new Set<string>();
  const registered = new Set<string>();

  // ---- 工具 ----
  const anyObject = Type.Object({}, { additionalProperties: true });

  const toAgentTool = (d: ToolDescriptor): AgentTool => {
    registered.add(d.name);
    if (!whitelist.has(d.name)) {
      // 非白名单工具也注册（参数不限），这样模型的越权调用会走到 beforeToolCall，被拦截并留痕。
      return {
        name: d.name,
        label: d.name,
        description: d.description,
        parameters: anyObject,
        execute: async () => {
          throw new Error(`工具 ${d.name} 未对本步骤开放`);
        },
      };
    }
    return {
      name: d.name,
      label: d.name,
      description: d.description,
      parameters: d.inputSchema as TSchema,
      execute: async (_id, params, signal) => {
        let result;
        try {
          result = await input.toolClient.call(d.name, params, { signal });
        } catch (e) {
          const error = e instanceof Error ? e.message : String(e);
          await input.onToolCalled({ tool: d.name, args: params, ok: false, error });
          throw new Error(error);
        }
        if (!result.ok) {
          await input.onToolCalled({ tool: d.name, args: params, ok: false, error: result.error });
          throw new Error(result.error);
        }
        await input.onToolCalled({ tool: d.name, args: params, ok: true });
        return { content: text(JSON.stringify(result.data) ?? "null"), details: result.data };
      },
    };
  };

  const submitTool: AgentTool = {
    name: SUBMIT_TOOL,
    label: SUBMIT_TOOL,
    description: "提交本步骤的最终结果。参数必须符合输出 Schema。提交后本步骤结束。",
    // 校验由我们自己做（不合法返回 invalid_output），所以对 pi 暴露的参数不设限。
    parameters: anyObject,
    execute: async (_id, params) => {
      if (validate(params)) {
        submission = { valid: true, output: params };
      } else {
        submission = { valid: false, errors: ajv.errorsText(validate.errors, { dataVar: "output" }) };
      }
      return { content: text(submission.valid ? "已提交" : "提交的结果不符合输出 Schema"), details: {}, terminate: true };
    },
  };

  const agentTools: AgentTool[] = [...descriptors.map(toAgentTool), submitTool];

  // 模型看到的工具表：白名单工具 + 带真实输出 Schema 的 submit_result。
  const visibleTools: Tool[] = [
    ...agentTools.filter((t) => whitelist.has(t.name)).map((t) => ({ name: t.name, description: t.description, parameters: descriptors.find((d) => d.name === t.name)!.inputSchema as TSchema })),
    { name: SUBMIT_TOOL, description: submitTool.description, parameters: input.outputSchema as unknown as TSchema },
  ];

  // ---- 拦截 ----
  const deny = async (toolCallId: string, tool: string): Promise<string> => {
    const reason = `工具 ${tool} 未对本步骤开放，调用被拒绝。本步骤可用的工具：${[...whitelist, SUBMIT_TOOL].join("、")}。`;
    if (!denied.has(toolCallId)) {
      denied.add(toolCallId);
      await input.onToolDenied({ tool, reason });
    }
    return reason;
  };

  // ---- 模型调用（streamFn 包装：轮次、预算、重放、回调）----
  const baseStream = resolved.streamFn;
  const streamFn: StreamFn = async (model, context, options) => {
    if (failure) return failedStream(model, "error", failure.message);
    if (submission) return failedStream(model, "aborted", "结果已提交");
    if (turns >= rounds) {
      failure = { kind: "step_rounds", message: `超过本步骤的轮次上限（${rounds} 轮）` };
      return failedStream(model, "error", failure.message);
    }
    const turn = turns++;

    if (turn < input.replay.length) {
      // 断点重放：直接使用记录的响应，不调用模型、不记预算、不再回调 onModelCalled。
      return completedStream(input.replay[turn] as AssistantMessage);
    }

    if (realCalls >= input.budget.remainingModelCalls) {
      failure = { kind: "task_calls", message: `任务的模型调用预算已用完（剩余 ${input.budget.remainingModelCalls} 次）` };
      return failedStream(model, "error", failure.message);
    }
    if (Date.now() >= input.budget.deadline) {
      failure = { kind: "task_duration", message: "任务总时长已到" };
      return failedStream(model, "error", failure.message);
    }

    realCalls++;
    const started = Date.now();
    let stream;
    try {
      stream = await baseStream(model, { ...context, tools: visibleTools }, options);
    } catch (e) {
      return failedStream(model, "error", e instanceof Error ? e.message : String(e));
    }
    const final = await stream.result();
    // 出错或被中止的响应不记录（重放它没有意义，恢复后应当重新请求）。
    if (final.stopReason !== "error" && final.stopReason !== "aborted") {
      await input.onModelCalled({
        turn,
        response: final,
        usage: { input: final.usage.input, output: final.usage.output },
        durationMs: Date.now() - started,
      });
    }
    return stream;
  };

  const agent = new Agent({
    initialState: { systemPrompt: input.systemPrompt, model: resolved.model, tools: agentTools, thinkingLevel: "off" },
    streamFn,
    toolExecution: "sequential",
    getApiKey: resolved.apiKey === undefined ? undefined : () => resolved.apiKey,
    beforeToolCall: async ({ toolCall }) => {
      if (whitelist.has(toolCall.name) || toolCall.name === SUBMIT_TOOL) return undefined;
      return { block: true, reason: await deny(toolCall.id, toolCall.name) };
    },
  });

  agent.subscribe(async (event) => {
    if (event.type === "tool_execution_start" && !registered.has(event.toolName) && event.toolName !== SUBMIT_TOOL) {
      // 工具服务器根本没有的名字：pi 会直接回“找不到”，不经过 beforeToolCall，这里补上留痕。
      await deny(event.toolCallId, event.toolName);
    }
    if (event.type === "message_end" && event.message.role === "assistant" && event.message.stopReason === "error") {
      modelError = lastText(event.message);
    }
  });

  // ---- 计时器与取消 ----
  const stop = (kind: JudgmentFailure, message: string) => {
    if (failure || submission) return;
    failure = { kind, message };
    agent.abort();
  };
  const timers: ReturnType<typeof setTimeout>[] = [];
  const untilDeadline = input.budget.deadline - Date.now();
  if (untilDeadline <= 0) return fail("task_duration", "任务总时长已到");
  timers.push(setTimeout(() => stop("step_duration", `超过本步骤的时长上限（${step.limits.durationSec} 秒）`), step.limits.durationSec * 1000));
  timers.push(setTimeout(() => stop("task_duration", "任务总时长已到"), untilDeadline));
  const onAbort = () => stop("model_error", "步骤已被取消");
  if (input.signal?.aborted) {
    timers.forEach(clearTimeout);
    return fail("model_error", "步骤已被取消");
  }
  input.signal?.addEventListener("abort", onAbort, { once: true });

  try {
    await agent.prompt(describeContext(input.context, input.corrections));
  } catch (e) {
    modelError ??= e instanceof Error ? e.message : String(e);
  } finally {
    timers.forEach(clearTimeout);
    input.signal?.removeEventListener("abort", onAbort);
  }

  // 提交优先：已经提交的结果不因随后的计时器或取消而作废。
  const sub = submission as Submission | undefined;
  if (sub) {
    return sub.valid ? { ok: true, output: sub.output, modelCalls: realCalls } : fail("invalid_output", `提交的结果不符合输出 Schema：${sub.errors}`);
  }
  const f = failure as { kind: JudgmentFailure; message: string } | undefined;
  if (f) return fail(f.kind, f.message);
  if (modelError !== undefined) return fail("model_error", modelError);
  return fail("no_output", "模型没有调用 submit_result 提交结果");
}
