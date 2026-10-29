import { describe, expect, test } from "bun:test";
import { createJudgmentRunner, createScriptedModelProvider } from "../../src/agent";
import { callTool, makeInput, makeStep, makeToolClient, submit } from "./helpers";

const runner = createJudgmentRunner();

describe("判断步骤执行器：正常路径", () => {
  test("调用工具 → 提交结果 → ok，回调次数正确", async () => {
    const model = createScriptedModelProvider({ s1: [callTool("query_task", { taskId: "t1" }), submit("好")] });
    const { input, rec, toolClient } = makeInput({ model });

    const result = await runner.run(input);

    expect(result).toEqual({ ok: true, output: { answer: "好" }, modelCalls: 2 });
    expect(toolClient.calls).toEqual([{ name: "query_task", args: { taskId: "t1" } }]);
    expect(rec.modelCalled.map((e) => e.turn)).toEqual([0, 1]);
    expect(rec.modelCalled.every((e) => e.durationMs >= 0)).toBe(true);
    expect(rec.toolCalled).toEqual([{ tool: "query_task", args: { taskId: "t1" }, ok: true }]);
    expect(rec.toolDenied).toEqual([]);
  });

  test("用户消息含上下文 JSON 和修正要求；模型只看到白名单工具和 submit_result", async () => {
    const model = createScriptedModelProvider({ s1: [submit("x")] });
    const { input } = makeInput({ model, corrections: ["摘要太长", "禁用词：同比"] });

    await runner.run(input);

    const [request] = model.requests;
    expect(request!.stepId).toBe("s1");
    expect(request!.context.systemPrompt).toBe("你是经营周报员工。");
    const user = request!.context.messages[0]!;
    const text = typeof user.content === "string" ? user.content : user.content.map((c) => (c.type === "text" ? c.text : "")).join("");
    expect(text).toContain('"week": 44');
    expect(text).toContain("摘要太长");
    expect(text).toContain("禁用词：同比");
    const toolNames = request!.context.tools!.map((t) => t.name);
    expect(toolNames).toEqual(["query_task", "submit_result"]);
    // submit_result 的参数就是输出 Schema
    expect(request!.context.tools!.find((t) => t.name === "submit_result")!.parameters).toMatchObject({ required: ["answer"] });
  });

  test("工具返回失败时，模型收到错误结果并可继续；回调 ok=false", async () => {
    const model = createScriptedModelProvider({ s1: [callTool("query_task"), submit("继续")] });
    const toolClient = makeToolClient({ query_task: () => ({ ok: false, error: "超时", retryable: true }) });
    const { input, rec } = makeInput({ model, toolClient });

    const result = await runner.run(input);

    expect(result.ok).toBe(true);
    expect(rec.toolCalled).toEqual([{ tool: "query_task", args: {}, ok: false, error: "超时" }]);
    const toolResult = model.requests[1]!.context.messages.find((m) => m.role === "toolResult");
    expect(toolResult).toMatchObject({ isError: true });
  });
});

describe("白名单", () => {
  test("调用白名单外的工具 → 被拦截并回调 onToolDenied，模型收到拒绝结果后继续", async () => {
    const model = createScriptedModelProvider({ s1: [callTool("web_search", { q: "天气" }), submit("改用数据")] });
    const { input, rec, toolClient } = makeInput({ model });

    const result = await runner.run(input);

    expect(result).toMatchObject({ ok: true, output: { answer: "改用数据" }, modelCalls: 2 });
    expect(toolClient.calls).toEqual([]); // 工具根本没被调用
    expect(rec.toolDenied).toHaveLength(1);
    expect(rec.toolDenied[0]!.tool).toBe("web_search");
    expect(rec.toolDenied[0]!.reason).toContain("web_search");
    expect(rec.toolCalled).toEqual([]);
    const denial = model.requests[1]!.context.messages.find((m) => m.role === "toolResult");
    expect(denial).toMatchObject({ isError: true, toolName: "web_search" });
    expect(JSON.stringify(denial)).toContain("未对本步骤开放");
  });

  test("工具服务器根本没有的工具名（幻觉）同样被拦截并留痕", async () => {
    const model = createScriptedModelProvider({ s1: [callTool("rm_rf", {}), submit("ok")] });
    const { input, rec } = makeInput({ model });

    const result = await runner.run(input);

    expect(result.ok).toBe(true);
    expect(rec.toolDenied.map((e) => e.tool)).toEqual(["rm_rf"]);
  });
});

describe("上限", () => {
  test("超过轮次上限 → step_rounds，不再调用模型", async () => {
    const model = createScriptedModelProvider({ s1: [callTool("query_task"), callTool("query_task"), callTool("query_task"), submit("太晚")] });
    const { input } = makeInput({ model, step: makeStep({ limits: { rounds: 2, durationSec: 90 } }) });

    const result = await runner.run(input);

    expect(result).toMatchObject({ ok: false, failure: "step_rounds", modelCalls: 2 });
  });

  test("恰好在最后一轮提交 → 成功", async () => {
    const model = createScriptedModelProvider({ s1: [callTool("query_task"), submit("刚好")] });
    const { input } = makeInput({ model, step: makeStep({ limits: { rounds: 2, durationSec: 90 } }) });
    expect((await runner.run(input)).ok).toBe(true);
  });

  test("超过时长上限 → step_duration，并及时返回", async () => {
    const model = createScriptedModelProvider({ s1: [{ delayMs: 2000, then: submit("慢") }] });
    const { input } = makeInput({ model, step: makeStep({ limits: { rounds: 6, durationSec: 0.05 } }) });

    const started = Date.now();
    const result = await runner.run(input);

    expect(result).toMatchObject({ ok: false, failure: "step_duration" });
    expect(Date.now() - started).toBeLessThan(1000);
  });

  test("任务模型调用预算耗尽 → task_calls", async () => {
    const model = createScriptedModelProvider({ s1: [callTool("query_task"), submit("预算外")] });
    const { input } = makeInput({ model, budget: { remainingModelCalls: 1, deadline: Date.now() + 600_000 } });

    const result = await runner.run(input);

    expect(result).toMatchObject({ ok: false, failure: "task_calls", modelCalls: 1 });
    expect(model.requests).toHaveLength(1);
  });

  test("预算为 0 时一次模型都不调用", async () => {
    const model = createScriptedModelProvider({ s1: [submit("x")] });
    const { input } = makeInput({ model, budget: { remainingModelCalls: 0, deadline: Date.now() + 600_000 } });
    expect(await runner.run(input)).toMatchObject({ ok: false, failure: "task_calls", modelCalls: 0 });
    expect(model.requests).toHaveLength(0);
  });

  test("任务总时限已到 → task_duration", async () => {
    const model = createScriptedModelProvider({ s1: [{ delayMs: 2000, then: submit("慢") }] });
    const { input } = makeInput({ model, budget: { remainingModelCalls: 30, deadline: Date.now() + 50 } });
    const result = await runner.run(input);
    expect(result).toMatchObject({ ok: false, failure: "task_duration" });
  });

  test("外部取消 → 失败返回（model_error，说明已取消）", async () => {
    const model = createScriptedModelProvider({ s1: [{ delayMs: 2000, then: submit("慢") }] });
    const controller = new AbortController();
    const { input } = makeInput({ model, signal: controller.signal });
    setTimeout(() => controller.abort(), 30);
    const result = await runner.run(input);
    expect(result).toMatchObject({ ok: false, failure: "model_error" });
    if (!result.ok) expect(result.message).toContain("取消");
  });
});

describe("断点重放", () => {
  test("replay 的前两轮不调用模型，第三轮才真正调用", async () => {
    // 第一次运行：记录三轮响应，假装在第三轮之后崩溃（只保留前两轮）。
    const first = createScriptedModelProvider({ s1: [callTool("query_task", { n: 1 }), callTool("query_task", { n: 2 }), submit("完成")] });
    const a = makeInput({ model: first });
    expect((await runner.run(a.input)).ok).toBe(true);
    const recorded = a.rec.modelCalled.map((e) => e.response);
    expect(recorded).toHaveLength(3);

    // 恢复：剧本里只剩第三轮；前两轮由日志重放。
    const second = createScriptedModelProvider({ s1: [submit("完成")] });
    const b = makeInput({ model: second, replay: recorded.slice(0, 2) });
    const result = await runner.run(b.input);

    expect(result).toEqual({ ok: true, output: { answer: "完成" }, modelCalls: 1 });
    expect(second.requests).toHaveLength(1);
    expect(b.rec.modelCalled.map((e) => e.turn)).toEqual([2]);
    // 重放轮次里的工具调用会重新执行以重建上下文
    expect(b.toolClient.calls.map((c) => c.args)).toEqual([{ n: 1 }, { n: 2 }]);
    // 真实调用的请求里能看到前两轮的历史
    expect(second.requests[0]!.context.messages.filter((m) => m.role === "assistant")).toHaveLength(2);
  });

  test("重放轮次也计入轮次上限，但不消耗任务预算", async () => {
    const first = createScriptedModelProvider({ s1: [callTool("query_task"), callTool("query_task")] });
    const a = makeInput({ model: first, step: makeStep({ limits: { rounds: 6, durationSec: 90 } }) });
    await runner.run(a.input);
    const recorded = a.rec.modelCalled.map((e) => e.response);

    const second = createScriptedModelProvider({ s1: [submit("x")] });
    const b = makeInput({
      model: second,
      replay: recorded,
      step: makeStep({ limits: { rounds: 2, durationSec: 90 } }),
      budget: { remainingModelCalls: 1, deadline: Date.now() + 600_000 },
    });
    // 两轮已用完上限 → 第三轮不会发生
    expect(await runner.run(b.input)).toMatchObject({ ok: false, failure: "step_rounds", modelCalls: 0 });
    expect(second.requests).toHaveLength(0);
  });
});

describe("输出校验与其它失败", () => {
  test("提交的结果不符合 Schema → invalid_output", async () => {
    const model = createScriptedModelProvider({ s1: [submit(123)] });
    const { input } = makeInput({ model });
    const result = await runner.run(input);
    expect(result).toMatchObject({ ok: false, failure: "invalid_output", modelCalls: 1 });
    if (!result.ok) expect(result.message).toContain("answer");
  });

  test("缺少必填字段 → invalid_output", async () => {
    const model = createScriptedModelProvider({ s1: [{ toolCalls: [{ name: "submit_result", arguments: {} }] }] });
    const { input } = makeInput({ model });
    expect(await runner.run(input)).toMatchObject({ ok: false, failure: "invalid_output" });
  });

  test("模型只回复文字、从不提交 → no_output", async () => {
    const model = createScriptedModelProvider({ s1: [{ text: "我觉得一切正常" }] });
    const { input } = makeInput({ model });
    expect(await runner.run(input)).toMatchObject({ ok: false, failure: "no_output", modelCalls: 1 });
  });

  test("模型返回错误 → model_error，带上错误信息", async () => {
    const model = createScriptedModelProvider({ s1: [{ error: "503 Service Unavailable" }] });
    const { input } = makeInput({ model });
    const result = await runner.run(input);
    expect(result).toMatchObject({ ok: false, failure: "model_error" });
    if (!result.ok) expect(result.message).toContain("503");
  });

  test("剧本用完仍被调用 → model_error", async () => {
    const model = createScriptedModelProvider({ s1: [callTool("query_task")] });
    const { input } = makeInput({ model });
    expect(await runner.run(input)).toMatchObject({ ok: false, failure: "model_error" });
  });

  test("同一步骤被重跑时，剧本按顺序消费后续轮次", async () => {
    const model = createScriptedModelProvider({ s1: [submit("第一次"), submit("第二次")] });
    const one = await runner.run(makeInput({ model }).input);
    const two = await runner.run(makeInput({ model }).input);
    expect(one).toMatchObject({ ok: true, output: { answer: "第一次" } });
    expect(two).toMatchObject({ ok: true, output: { answer: "第二次" } });
  });
});
