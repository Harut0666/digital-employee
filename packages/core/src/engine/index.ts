// T1 实现：任务引擎（状态机、事件日志、编排器、断点恢复）。
import { createHash, randomUUID } from "node:crypto";
import { cpSync, existsSync, mkdirSync, readFileSync, readdirSync, renameSync, statSync, writeFileSync, copyFileSync } from "node:fs";
import { basename, dirname, join, resolve } from "node:path";
import { createJudgmentRunner } from "../agent";
import { loadEmployeeConfigSync, loadDemoSettings, readSettingsDocument, saveDemoSettings } from "../config";
import { collectWritingRequirements } from "../config/form";
import { createQcEngine, renderSlots } from "../qc";
import {
  STATE_LABELS,
  TRANSITIONS,
  isEmployeeId,
  type AttachmentInput,
  type AttachmentRef,
  type CommandResult,
  type DeliverableRef,
  type DraftReport,
  type EmployeeConfig,
  type EngineDeps,
  type ExecutionSettings,
  type FaultHooks,
  type JudgmentFailure,
  type JudgmentRunner,
  type PauseCode,
  type PauseReason,
  type QcCheckResult,
  type QcEngine,
  type QcInput,
  type WeeklyFacts,
  type Role,
  type StepConfig,
  type TaskCommand,
  type TaskEngine,
  type TaskEvent,
  type TaskId,
  type TaskSnapshot,
  type TaskState,
  type TaskSummary,
  type ToolCallResult,
  type TransitionTrigger,
} from "../contract";
import { appendEventLine, readEventLog, readEventLogSalvage } from "./event-log";
import { TaskProjection, argsKey, type Checkpoint } from "./projection";
import { replaceString, resolveInput } from "./refs";

/** 测试可注入的内部实现；生产环境不传，使用 ../agent 与 ../qc 的默认实现。 */
export interface EngineInternals {
  judgment?: JudgmentRunner;
  qc?: QcEngine;
}

/** 模拟进程被杀：崩溃注入命中时抛出。抛出之后这个引擎实例不再写任何事件。 */
export class SimulatedCrash extends Error {
  constructor(readonly stepId: string) {
    super(`simulated crash at step "${stepId}"`);
    this.name = "SimulatedCrash";
  }
}

// ---- 常量 ----

const COMMAND_TRIGGER: Record<TaskCommand["type"], TransitionTrigger> = {
  submit: "submit",
  supplement: "supplement",
  continue_without_minutes: "continue_without_minutes",
  continue_with_partial: "continue_with_partial",
  retry: "retry",
  terminate: "terminate",
  approve: "approve",
  return: "return",
};

const ACTIVE_STATES: readonly TaskState[] = ["admission", "running", "qc"];
const PAUSED_OR_FINAL: readonly TaskState[] = ["awaiting_initiator", "awaiting_operator", "awaiting_review", "delivered", "terminated"];
const TOOL_CALL_TIMEOUT_MS = 120_000;

/** 未配置 factsStep 时，事实取自这个步骤（场景 A 的约定）。 */
const DEFAULT_FACTS_STEP = "compute_metrics";

/** 用这个工具的计算步骤输出纪要矛盾，供“纪要矛盾覆盖”质检使用。 */
const CONTRADICTIONS_TOOL = "detect_contradictions";

/** 准入不通过时各暂停原因下发起人可以做的事。 */
function initiatorOptions(code: PauseCode): TaskCommand["type"][] {
  switch (code) {
    case "minutes_missing":
    case "minutes_week_mismatch":
      return ["supplement", "continue_without_minutes", "terminate"];
    case "period_incomplete":
      return ["supplement", "continue_with_partial", "terminate"];
    default:
      return ["supplement", "terminate"];
  }
}

const OPERATOR_OPTIONS: TaskCommand["type"][] = ["retry", "terminate"];

interface Runtime {
  id: TaskId;
  dir: string;
  config: EmployeeConfig;
  proj: TaskProjection;
  events: TaskEvent[];
  logPath: string;
  listeners: Set<(event: TaskEvent) => void>;
  driving?: Promise<void>;
  redrive: boolean;
  failure?: unknown;
  artifactCache: Map<string, unknown>;
}

type RejectError = Extract<CommandResult, { ok: false }>["error"];

function sha256(data: string | Buffer): string {
  return createHash("sha256").update(data).digest("hex");
}

function normalizeRef(ref: string): string {
  return ref.replace(/\\/g, "/").replace(/^\.\//, "");
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function uniquePath(dir: string, name: string): string {
  let candidate = join(dir, name);
  for (let i = 2; existsSync(candidate); i++) {
    const dot = name.lastIndexOf(".");
    candidate = join(dir, dot > 0 ? `${name.slice(0, dot)}-${i}${name.slice(dot)}` : `${name}-${i}`);
  }
  return candidate;
}

export function createTaskEngine(deps: EngineDeps, internals: EngineInternals = {}): TaskEngine {
  const workspaceDir = resolve(deps.workspaceDir);
  const tasksDir = join(workspaceDir, "tasks");
  const now = deps.now ?? (() => new Date());
  const runtimes = new Map<TaskId, Runtime>();
  /** 一旦“崩溃”，这个实例就不能再写任何事件（模拟进程已死）。 */
  let dead: SimulatedCrash | undefined;
  /**
   * 故障开关是运行时可变对象：服务端持有同一引用，设置页改动立即对后续步骤生效。
   * 一次性开关（数据溯源、崩溃）命中后会在这里被清掉。
   */
  const faults: FaultHooks = deps.faults ?? {};
  const exitOnCrash = deps.exitOnCrash === true;

  /** settings.json 里持久化的一次性崩溃标记；读取失败不影响任务执行。 */
  function persistedCrashArmed(): boolean {
    try {
      return loadDemoSettings(workspaceDir).crashOnNextStep;
    } catch {
      return false;
    }
  }

  /** 一次性开关命中后同步清除持久化标记（崩溃时必须在进程退出之前完成）。 */
  function persistFaultCleared(patch: { crashOnNextStep?: boolean; corruptTraceability?: boolean }): void {
    try {
      saveDemoSettings(workspaceDir, patch);
    } catch {
      /* 演示开关的持久化失败不应影响任务本身 */
    }
  }

  let judgment = internals.judgment;
  let qcEngine = internals.qc;

  /**
   * 执行与预算的默认值（settings.execution，Spec 0004）。
   * 每个判断步骤开始时读一次：设置页保存后对之后新建/新开始的步骤立即生效；
   * 设置文件缺失或损坏时返回 undefined，由运行器退回内置值（120s / 2 次）。
   */
  function executionDefaults(): Partial<ExecutionSettings> | undefined {
    try {
      return readSettingsDocument(workspaceDir).execution;
    } catch {
      return undefined;
    }
  }

  const getJudgment = () => (judgment ??= createJudgmentRunner({ execution: executionDefaults }));
  const getQc = () => (qcEngine ??= createQcEngine());

  // ---------------------------------------------------------------- 事件与运行时

  /** 从磁盘读出一个任务的完整 Runtime，不写缓存（list 用）。 */
  function loadRuntime(id: TaskId): Runtime | undefined {
    const dir = join(tasksDir, id);
    const logPath = join(dir, "events.jsonl");
    if (!existsSync(logPath)) return undefined;
    const events = readEventLog(logPath);
    if (events.length === 0) return undefined;
    const config = loadEmployeeConfigSync(join(dir, "config"));
    const proj = new TaskProjection(config);
    for (const e of events) proj.apply(e);
    return { id, dir, config, proj, events, logPath, listeners: new Set(), redrive: false, artifactCache: new Map() };
  }

  /** 取任务的 Runtime：命中缓存则复用，否则读盘并缓存。 */
  function openRuntime(id: TaskId): Runtime | undefined {
    const cached = runtimes.get(id);
    if (cached) return cached;
    const rt = loadRuntime(id);
    if (rt) runtimes.set(id, rt);
    return rt;
  }

  function append(rt: Runtime, event: DistributiveOmit<TaskEvent, "seq" | "ts" | "taskId">): TaskEvent {
    if (dead) throw dead;
    const full = { ...event, seq: rt.proj.lastSeq + 1, ts: now().toISOString(), taskId: rt.id } as TaskEvent;
    appendEventLine(rt.logPath, full);
    rt.events.push(full);
    rt.proj.apply(full);
    for (const listener of [...rt.listeners]) {
      try {
        listener(full);
      } catch {
        /* 订阅者的错误不影响引擎 */
      }
    }
    return full;
  }

  function snapshotOf(rt: Runtime): TaskSnapshot {
    return rt.proj.snapshot(rt.id, now().getTime());
  }

  function transition(rt: Runtime, trigger: TransitionTrigger, actor: Role, pause?: PauseReason): void {
    const from = rt.proj.state;
    const candidates = TRANSITIONS.filter((x) => x.from === from && x.trigger === trigger);
    // retry 在 awaiting_operator 下有两个目标：失败的是准入步骤（尚未完成）回到准入，否则回到执行
    const t =
      trigger === "retry" && candidates.length > 1
        ? candidates.find((x) => (x.to === "admission") === !rt.proj.done.has(rt.config.workflow.admissionStep))
        : candidates[0];
    if (!t) throw new Error(`流转表中没有 ${from} --${trigger}-->`);
    append(rt, { type: "state_changed", from, to: t.to, trigger, actor, pause });
  }

  function writeArtifact(rt: Runtime, value: unknown): { path: string; sha256: string } {
    const json = JSON.stringify(value ?? null);
    const hash = sha256(json);
    const dir = join(rt.dir, "artifacts");
    mkdirSync(dir, { recursive: true });
    const path = join(dir, `${hash}.json`);
    if (!existsSync(path)) {
      const tmp = `${path}.${process.pid}.tmp`;
      writeFileSync(tmp, json);
      renameSync(tmp, path);
    }
    rt.artifactCache.set(path, JSON.parse(json));
    return { path, sha256: hash };
  }

  function readArtifact(rt: Runtime, ref: { path: string }): unknown {
    if (rt.artifactCache.has(ref.path)) return rt.artifactCache.get(ref.path);
    const value = JSON.parse(readFileSync(ref.path, "utf8"));
    rt.artifactCache.set(ref.path, value);
    return value;
  }

  /** 某步骤最近一次完成的输出；重做或修正中被回退的步骤，取回退前的那一版。 */
  function stepOutput(rt: Runtime, stepId: string): unknown {
    const checkpoint = rt.proj.done.get(stepId) ?? rt.proj.prior.get(stepId);
    return checkpoint ? readArtifact(rt, checkpoint) : undefined;
  }

  function factsStepId(rt: Runtime): string {
    return rt.config.workflow.factsStep ?? DEFAULT_FACTS_STEP;
  }

  /** 程序判定纪要矛盾的步骤（工具为 detect_contradictions 的计算步骤）；没有则不检查矛盾覆盖。 */
  function contradictionsOf(rt: Runtime): QcInput["contradictions"] {
    const step = rt.config.workflow.steps.find((s) => s.kind === "compute" && s.tool === CONTRADICTIONS_TOOL);
    if (!step) return undefined;
    const output = optionalOutput(rt, step.id) as { contradictions?: unknown } | undefined;
    return Array.isArray(output?.contradictions) ? (output.contradictions as QcInput["contradictions"]) : undefined;
  }

  /** 某步骤输出的报告草稿，用事实步骤的输出做槽位渲染。 */
  function renderedOutput(rt: Runtime, stepId: string): unknown {
    const draft = stepOutput(rt, stepId) as DraftReport | undefined;
    const facts = stepOutput(rt, factsStepId(rt)) as WeeklyFacts | undefined;
    if (!draft || typeof draft !== "object" || !facts) return undefined;
    const render = (text: string) => renderSlots(text, facts).text;
    return {
      ...draft,
      title: typeof draft.title === "string" ? render(draft.title) : draft.title,
      sections: (draft.sections ?? []).map((s) => ({
        ...s,
        heading: render(s.heading),
        paragraphs: (s.paragraphs ?? []).map(render),
        ...(s.bullets ? { bullets: s.bullets.map(render) } : {}),
      })),
    };
  }

  function optionalOutput(rt: Runtime, stepId: string): unknown {
    const checkpoint = rt.proj.done.get(stepId);
    return checkpoint ? readArtifact(rt, checkpoint) : undefined;
  }

  // ---------------------------------------------------------------- 编排

  function kick(rt: Runtime): void {
    if (rt.driving) {
      rt.redrive = true;
      return;
    }
    const p: Promise<void> = Promise.resolve()
      .then(async () => {
        try {
          do {
            rt.redrive = false;
            await drive(rt);
          } while (rt.redrive);
        } catch (error) {
          rt.failure = error;
        }
      })
      .finally(() => {
        if (rt.driving === p) rt.driving = undefined;
      });
    rt.driving = p;
  }

  async function drive(rt: Runtime): Promise<void> {
    for (;;) {
      const state = rt.proj.state;
      if (!ACTIVE_STATES.includes(state)) return;
      try {
        if (state === "admission") await stepAdmission(rt);
        else if (state === "running") await stepRunning(rt);
        else await stepQc(rt);
      } catch (error) {
        if (error instanceof SimulatedCrash || dead) throw error;
        unexpectedFailure(rt, error);
      }
    }
  }

  /** 引擎自身遇到未预料的错误：留痕并转人工，不吞掉也不崩溃。 */
  function unexpectedFailure(rt: Runtime, error: unknown): void {
    const message = `任务执行时发生内部错误：${errorText(error)}`;
    const state = rt.proj.state;
    if (state === "admission") {
      pauseOperator(rt, "tool_failed", "tool_failed", message);
    } else if (state === "running") {
      pauseOperator(rt, "tool_failed", "tool_failed", message);
    } else {
      pauseOperator(rt, "qc_exhausted", "tool_failed", message);
    }
  }

  function pauseOperator(rt: Runtime, trigger: TransitionTrigger, code: PauseCode, message: string, detail?: Record<string, unknown>): void {
    transition(rt, trigger, "system", { code, message, options: OPERATOR_OPTIONS, detail });
  }

  function progressText(rt: Runtime): string {
    return `已完成 ${rt.proj.done.size}/${rt.config.workflow.steps.length} 步`;
  }

  function stepsOf(rt: Runtime): StepConfig[] {
    return rt.config.workflow.steps;
  }

  async function stepAdmission(rt: Runtime): Promise<void> {
    const step = stepsOf(rt).find((s) => s.id === rt.config.workflow.admissionStep)!;
    if (!rt.proj.done.has(step.id)) {
      const outcome = await executeStep(rt, step);
      if (outcome === "paused") return;
    }
    const checkpoint = rt.proj.done.get(step.id);
    const output = (checkpoint ? readArtifact(rt, checkpoint) : undefined) as
      | { ok?: boolean; pause?: { code?: PauseCode; message?: string; detail?: Record<string, unknown> } }
      | undefined;
    if (output && output.ok === false) {
      const code = output.pause?.code ?? "invalid_input";
      transition(rt, "admission_failed", "system", {
        code,
        message: output.pause?.message ?? "准入校验不通过。",
        options: initiatorOptions(code),
        detail: output.pause?.detail,
      });
    } else {
      transition(rt, "admission_passed", "system");
    }
  }

  async function stepRunning(rt: Runtime): Promise<void> {
    const proj = rt.proj;
    const idx = proj.nextStepIndex(rt.config.workflow.admissionStep);
    if (idx < 0) {
      transition(rt, "steps_completed", "system");
      return;
    }
    const step = stepsOf(rt)[idx]!;
    const limits = rt.config.workflow.limits.task;
    const elapsed = proj.elapsedMs(now().getTime());
    if (elapsed >= limits.durationSec * 1000) {
      pauseOperator(rt, "limit_exceeded", "limit_exceeded", `任务总时长已达上限（${limits.durationSec} 秒），在步骤「${step.label}」之前暂停。${progressText(rt)}。`, {
        limit: "durationSec",
        used: elapsed,
      });
      return;
    }
    if (step.kind === "judge" && proj.modelCalls >= limits.modelCalls) {
      pauseOperator(rt, "limit_exceeded", "limit_exceeded", `模型调用次数已达上限（${proj.modelCalls}/${limits.modelCalls}），在步骤「${step.label}」之前暂停。${progressText(rt)}。`, {
        limit: "modelCalls",
        used: proj.modelCalls,
      });
      return;
    }
    await executeStep(rt, step);
  }

  /** 执行一个计算步骤或判断步骤。失败时已完成状态流转（或跳过可选步骤），返回 "paused" / "done"。 */
  async function executeStep(rt: Runtime, step: StepConfig): Promise<"done" | "paused"> {
    if (step.kind === "qc") throw new Error("质检步骤不由此处执行");
    const proj = rt.proj;
    const isAdmission = step.id === rt.config.workflow.admissionStep;

    let args: unknown;
    let resolveError: unknown;
    try {
      const placeholder = `\u0000idempotencyKey:${step.id}`;
      const resolved = resolveInput(step.input, {
        form: proj.form,
        taskDir: rt.dir,
        attachments: proj.attachments,
        feedback: proj.feedback ?? null,
        qcConfig: rt.config.qc,
        stepOutput: (id) => stepOutput(rt, id),
        renderedOutput: (id) => renderedOutput(rt, id),
        idempotencyKey: placeholder,
      });
      // 幂等键 = 任务 + 步骤 + 重做轮次 + 入参哈希（入参哈希不含键本身）
      const key = `${rt.id}-${step.id}-r${proj.reworkRound}-${sha256(argsKey(resolved)).slice(0, 16)}`;
      args = replaceString(resolved, placeholder, key);
    } catch (error) {
      resolveError = error;
    }

    // 重做：计算步骤的入参和上次相同，直接复用已有输出，不重新执行
    if (!resolveError && step.kind === "compute" && !isAdmission) {
      const prior = proj.prior.get(step.id);
      if (prior && prior.args === argsKey(args)) {
        append(rt, { type: "step_completed", stepId: step.id, reworkRound: proj.reworkRound, outputPath: prior.path, outputSha256: prior.sha256 });
        return "done";
      }
    }

    append(rt, { type: "step_started", stepId: step.id, attempt: (proj.startCounts.get(step.id) ?? 0) + 1, reworkRound: proj.reworkRound });
    const crashArmed = faults.crashAtStep === step.id || faults.crashOnNextStep === true || persistedCrashArmed();
    if (crashArmed && step.kind === "compute") crash(rt, step.id);

    if (resolveError) return failStep(rt, step, "tool_failed", `步骤「${step.label}」的输入无法解析：${errorText(resolveError)}。${progressText(rt)}。`);

    return step.kind === "compute"
      ? runCompute(rt, step, args as Record<string, unknown>)
      : runJudge(rt, step, args as Record<string, unknown>, crashArmed);
  }

  /** 模拟进程崩溃：先写 fault_injected，再清除一次性标记，最后退出进程。 */
  function crash(rt: Runtime, stepId: string): never {
    append(rt, { type: "fault_injected", target: stepId, mode: "crash" });
    // 一次性：内存开关与 settings.json 里的标记都清掉，重启后 resumeAll 不会再次崩溃
    faults.crashOnNextStep = false;
    persistFaultCleared({ crashOnNextStep: false });
    dead = new SimulatedCrash(stepId);
    // 生产入口 main.ts 传 exitOnCrash=true，真正退出进程；测试与命令行默认抛 SimulatedCrash
    if (exitOnCrash) process.exit(1);
    throw dead;
  }

  /** 步骤失败：写 step_failed；可选步骤直接跳过，否则转暂停。 */
  function failStep(
    rt: Runtime,
    step: StepConfig,
    code: "tool_failed" | "limit_exceeded",
    message: string,
    detail?: Record<string, unknown>,
  ): "done" | "paused" {
    append(rt, { type: "step_failed", stepId: step.id, error: message });
    if ("optional" in step && step.optional) return "done";
    // 准入步骤的工具本身故障也是工具故障（转操作员），不是缺材料
    pauseOperator(rt, rt.proj.state === "admission" ? "tool_failed" : code, code, message, detail);
    return "paused";
  }

  function completeStep(rt: Runtime, step: StepConfig, output: unknown): void {
    const artifact = writeArtifact(rt, output);
    append(rt, { type: "step_completed", stepId: step.id, reworkRound: rt.proj.reworkRound, outputPath: artifact.path, outputSha256: artifact.sha256 });
  }

  async function runCompute(rt: Runtime, step: Extract<StepConfig, { kind: "compute" }>, args: Record<string, unknown>): Promise<"done" | "paused"> {
    const retry = rt.config.workflow.limits.toolRetry;
    let result: ToolCallResult = { ok: false, error: "未调用", retryable: false };
    let tried = 0;
    for (let attempt = 1; attempt <= retry.attempts; attempt++) {
      tried = attempt;
      const callId = randomUUID();
      append(rt, { type: "tool_called", stepId: step.id, callId, tool: step.tool, args, attempt });
      try {
        result = await deps.toolClient.call(step.tool, args, { signal: AbortSignal.timeout(TOOL_CALL_TIMEOUT_MS) });
      } catch (error) {
        result = { ok: false, error: errorText(error), retryable: true };
      }
      if (dead) throw dead;
      append(rt, {
        type: "tool_result",
        callId,
        ok: result.ok,
        error: result.ok ? undefined : result.error,
        resultSha256: result.ok ? sha256(JSON.stringify(result.data ?? null)) : undefined,
      });
      if (result.ok || !result.retryable || attempt === retry.attempts) break;
      const wait = retry.backoffMs[Math.min(attempt - 1, retry.backoffMs.length - 1)] ?? 0;
      if (wait > 0) await sleep(wait);
    }
    if (!result.ok) {
      return failStep(rt, step, "tool_failed", `工具 ${step.tool} 调用失败（共尝试 ${tried} 次）：${result.error}。${progressText(rt)}。`, {
        tool: step.tool,
        attempts: tried,
        error: result.error,
        completedSteps: rt.proj.done.size,
        totalSteps: rt.config.workflow.steps.length,
      });
    }
    completeStep(rt, step, result.data);
    return "done";
  }

  const JUDGMENT_PAUSE: Record<JudgmentFailure, "tool_failed" | "limit_exceeded"> = {
    step_rounds: "limit_exceeded",
    step_duration: "limit_exceeded",
    task_calls: "limit_exceeded",
    task_duration: "limit_exceeded",
    model_error: "tool_failed",
    no_output: "tool_failed",
    invalid_output: "tool_failed",
  };

  async function runJudge(
    rt: Runtime,
    step: Extract<StepConfig, { kind: "judge" }>,
    context: Record<string, unknown>,
    crashArmed: boolean,
  ): Promise<"done" | "paused"> {
    const proj = rt.proj;
    const limits = rt.config.workflow.limits.task;
    const prompt = rt.config.prompts[normalizeRef(step.prompt)] ?? "";
    const outputSchema = rt.config.outputSchemas[normalizeRef(step.outputSchema)] ?? {};
    const replay = [...(proj.modelResponses.get(step.id) ?? [])].sort((a, b) => a.turn - b.turn).map((r) => r.response);

    const corrections: string[] = [];
    if (proj.reworkRound > 0 && proj.feedback) {
      const sections = proj.feedback.sections?.length ? `（涉及章节：${proj.feedback.sections.join("、")}）` : "";
      corrections.push(`审核人退回意见${sections}：${proj.feedback.comment}`);
    }
    // 质检问题只交给 fixStep 指向本步骤的那些判断步骤，其余步骤不需要看到与自己无关的问题
    for (const check of proj.qc ?? []) {
      if (check.passed || check.fixStep !== step.id) continue;
      corrections.push(...check.issues.map((issue) => `质检「${check.label}」：${issue}`));
    }

    const nowMs = now().getTime();
    // 表单里标了 x-purpose: writing_requirement 的字段，标题和取值作为“发起人的写作要求”
    // 注入每个判断步骤的上下文（设计 §4.1）。字段只影响判断步骤，计算步骤的入参不变。
    const writingRequirements = collectWritingRequirements(rt.config.formSchema, proj.form);
    const judgmentContext = writingRequirements.length > 0 ? { ...context, writingRequirements } : context;
    let result: Awaited<ReturnType<JudgmentRunner["run"]>>;
    try {
      result = await getJudgment().run({
        taskId: rt.id,
        step,
        systemPrompt: [rt.config.skill, prompt].filter(Boolean).join("\n\n"),
        context: judgmentContext,
        corrections: corrections.length > 0 ? corrections : undefined,
        toolClient: deps.toolClient,
        model: deps.model,
        outputSchema,
        budget: {
          remainingModelCalls: Math.max(0, limits.modelCalls - proj.modelCalls) + replay.length,
          deadline: nowMs + Math.max(0, limits.durationSec * 1000 - proj.elapsedMs(nowMs)),
        },
        replay,
        async onModelCalled(event) {
          const seen = proj.modelResponses.get(step.id)?.some((r) => r.turn === event.turn);
          if (!seen) {
            append(rt, { type: "model_called", stepId: step.id, turn: event.turn, response: event.response, usage: event.usage, durationMs: event.durationMs });
          }
          if (crashArmed) crash(rt, step.id);
        },
        async onToolCalled(event) {
          const callId = randomUUID();
          append(rt, { type: "tool_called", stepId: step.id, callId, tool: event.tool, args: event.args, attempt: 1 });
          append(rt, { type: "tool_result", callId, ok: event.ok, error: event.error });
        },
        async onToolDenied(event) {
          append(rt, { type: "tool_denied", stepId: step.id, tool: event.tool, reason: event.reason });
        },
      });
    } catch (error) {
      if (error instanceof SimulatedCrash || dead) throw dead ?? error;
      result = { ok: false, failure: "model_error", message: errorText(error), modelCalls: 0 };
    }
    if (dead) throw dead;
    if (crashArmed) crash(rt, step.id);

    if (!result.ok) {
      const code = JUDGMENT_PAUSE[result.failure];
      return failStep(rt, step, code, `步骤「${step.label}」未能完成（${result.failure}）：${result.message}。${progressText(rt)}。`, {
        failure: result.failure,
        completedSteps: proj.done.size,
        totalSteps: rt.config.workflow.steps.length,
      });
    }

    let output = result.output;
    if (output && typeof output === "object" && (output as { needsHuman?: unknown }).needsHuman === true) {
      const reason = (output as { needsHumanReason?: unknown }).needsHumanReason;
      const message = `步骤「${step.label}」判断需要人工处理：${typeof reason === "string" && reason ? reason : "该意见超出自动处理范围"}。${progressText(rt)}。`;
      append(rt, { type: "step_failed", stepId: step.id, error: message });
      pauseOperator(rt, "needs_human", "rework_needs_human", message, { output });
      return "paused";
    }
    // 数据溯源故障：只对下一个写作步骤生效一次，命中后立即清除运行时与持久化开关
    if (faults.corruptTraceability && step.id === rt.config.workflow.qcSources.draft && !proj.faultsInjected.has(`${step.id}:corruptTraceability`)) {
      output = corruptFirstParagraph(output);
      append(rt, { type: "fault_injected", target: step.id, mode: "corruptTraceability" });
      faults.corruptTraceability = false;
      persistFaultCleared({ corruptTraceability: false });
    }
    completeStep(rt, step, output);
    return "done";
  }

  async function stepQc(rt: Runtime): Promise<void> {
    const proj = rt.proj;
    const qcStep = stepsOf(rt).find((s) => s.kind === "qc");
    if (!qcStep) {
      finishQcPassed(rt, []);
      return;
    }
    if (!proj.done.has(qcStep.id)) {
      append(rt, { type: "step_started", stepId: qcStep.id, attempt: (proj.startCounts.get(qcStep.id) ?? 0) + 1, reworkRound: proj.reworkRound });
      if (faults.crashAtStep === qcStep.id || faults.crashOnNextStep === true || persistedCrashArmed()) crash(rt, qcStep.id);

      let checks: QcCheckResult[];
      try {
        const sources = rt.config.workflow.qcSources;
        const rendered = optionalOutput(rt, sources.rendered) as { estimatedPages?: number; chartCount?: number } | undefined;
        const input: QcInput = {
          config: rt.config.qc,
          draft: optionalOutput(rt, sources.draft) as DraftReport,
          facts: optionalOutput(rt, factsStepId(rt)) as QcInput["facts"],
          contradictions: contradictionsOf(rt),
          minutes: optionalOutput(rt, sources.minutes) as QcInput["minutes"],
          rendered:
            rendered && typeof rendered.estimatedPages === "number" && typeof rendered.chartCount === "number"
              ? { estimatedPages: rendered.estimatedPages, chartCount: rendered.chartCount }
              : undefined,
          previousDraft:
            proj.reworkRound > 0 && proj.baselineDraft ? (readArtifact(rt, proj.baselineDraft) as DraftReport) : undefined,
          feedback: proj.reworkRound > 0 ? proj.feedback : undefined,
          fixSteps: { write: rt.config.workflow.qcFixSteps.write ?? sources.draft, causes: rt.config.workflow.qcFixSteps.causes },
        };
        checks = getQc().run(input);
      } catch (error) {
        const message = `质检引擎出错：${errorText(error)}。${progressText(rt)}。`;
        append(rt, { type: "step_failed", stepId: qcStep.id, error: message });
        pauseOperator(rt, "qc_exhausted", "tool_failed", message);
        return;
      }
      append(rt, { type: "qc_result", attempt: proj.qcAttempts + 1, passed: checks.every((c) => c.passed), checks });
      completeStep(rt, qcStep, checks);
    }

    const checks = readArtifact(rt, proj.done.get(qcStep.id)!) as QcCheckResult[];
    const failed = checks.filter((c) => !c.passed);
    if (failed.length === 0) {
      finishQcPassed(rt, checks);
      return;
    }
    const issues = failed.flatMap((c) => c.issues.map((i) => `${c.label}：${i}`));
    const fixable = failed.every((c) => c.fixStep && stepsOf(rt).some((s) => s.id === c.fixStep));
    const limit = rt.config.workflow.limits.qcAutofix;
    if (fixable && proj.autofixCount < limit) {
      transition(rt, "qc_autofix", "system");
      return;
    }
    const why = fixable ? `自动修正已达上限（${limit} 次）` : "存在无法自动修正的问题";
    pauseOperator(rt, "qc_exhausted", "qc_exhausted", `质检未通过，${why}：${issues.join("；")}`, { checks: failed });
  }

  function finishQcPassed(rt: Runtime, checks: QcCheckResult[]): void {
    createDeliverables(rt, checks);
    transition(rt, "qc_passed", "system", {
      code: "awaiting_review",
      message: "已通过质检，等待审核人决定。",
      options: ["approve", "return"],
    });
  }

  /** 把渲染步骤产出的文件登记为交付物，并写检验记录。 */
  function createDeliverables(rt: Runtime, checks: QcCheckResult[]): void {
    const dir = join(rt.dir, "deliverables");
    mkdirSync(dir, { recursive: true });

    let source: { kind: DeliverableRef["kind"]; path: string } | undefined;
    for (const step of stepsOf(rt)) {
      const out = optionalOutput(rt, step.id) as Record<string, unknown> | undefined;
      if (!out || typeof out !== "object") continue;
      if (typeof out.docxPath === "string") source = { kind: "docx", path: out.docxPath };
      else if (typeof out.mdPath === "string") source = { kind: "md", path: out.mdPath };
    }
    if (source) {
      if (!existsSync(source.path)) throw new Error(`交付物文件不存在：${source.path}`);
      const hash = sha256(readFileSync(source.path));
      const name = basename(source.path);
      let target = join(dir, name);
      if (existsSync(target) && sha256(readFileSync(target)) !== hash) target = uniquePath(dir, name);
      if (!existsSync(target)) copyFileSync(source.path, target);
      append(rt, { type: "deliverable_created", kind: source.kind, path: target, sha256: hash });
    }

    const record = JSON.stringify(
      {
        taskId: rt.id,
        employee: rt.proj.employee,
        configHash: rt.proj.configHash,
        reworkRound: rt.proj.reworkRound,
        generatedAt: now().toISOString(),
        passed: checks.every((c) => c.passed),
        checks,
      },
      null,
      2,
    );
    const recordPath = join(dir, `qc_record-r${rt.proj.reworkRound}.json`);
    writeFileSync(recordPath, record);
    append(rt, { type: "deliverable_created", kind: "qc_record", path: recordPath, sha256: sha256(record) });
  }

  function corruptFirstParagraph(output: unknown): unknown {
    const clone = JSON.parse(JSON.stringify(output ?? null)) as Record<string, any> | null;
    if (!clone || typeof clone !== "object") return output;
    const section = Array.isArray(clone.sections) ? clone.sections[0] : undefined;
    if (section && Array.isArray(section.paragraphs) && typeof section.paragraphs[0] === "string") {
      section.paragraphs[0] += " 环比 +12.3%";
    } else if (typeof clone.title === "string") {
      clone.title += " 环比 +12.3%";
    }
    return clone;
  }

  // ---------------------------------------------------------------- 命令

  function reject(rt: Runtime, trigger: TransitionTrigger, actor: Role, error: RejectError, reason: string): CommandResult {
    append(rt, { type: "transition_rejected", from: rt.proj.state, trigger, actor, reason });
    return { ok: false, error, message: reason };
  }

  function copyAttachments(rt: Runtime, inputs: AttachmentInput[]): AttachmentRef[] {
    const dir = join(rt.dir, "attachments");
    mkdirSync(dir, { recursive: true });
    return inputs.map((input) => {
      const target = uniquePath(dir, basename(input.name));
      copyFileSync(input.sourcePath, target);
      return { name: input.name, path: target };
    });
  }

  function titleOf(rt: Runtime): string {
    const form = rt.proj.form as { period?: { year?: number; week?: number }; year?: number; week?: number };
    const year = form.period?.year ?? form.year;
    const week = form.period?.week ?? form.week;
    const base = rt.config.workflow.name;
    return year && week ? `${base} · ${year} 年第 ${week} 周` : base;
  }

  function summaryOf(rt: Runtime): TaskSummary {
    return { id: rt.id, employee: rt.proj.employee, title: titleOf(rt), state: rt.proj.state, updatedAt: rt.proj.lastTs };
  }

  /**
   * 日志损坏的任务也要出现在列表里：按可解析的前缀给出最后已知状态，并带上错误说明。
   * 只读，不改写损坏的日志；前缀里连一条事件都没有时无法给出摘要，只能靠在直接访问该任务时报错。
   */
  function salvagedSummary(id: TaskId, failure: unknown): TaskSummary | undefined {
    try {
      const dir = join(tasksDir, id);
      const { events, error } = readEventLogSalvage(join(dir, "events.jsonl"));
      if (events.length === 0) return undefined;
      const config = loadEmployeeConfigSync(join(dir, "config"));
      const proj = new TaskProjection(config);
      for (const e of events) proj.apply(e);
      const rt: Runtime = { id, dir, config, proj, events, logPath: join(dir, "events.jsonl"), listeners: new Set(), redrive: false, artifactCache: new Map() };
      return { ...summaryOf(rt), error: error ?? errorText(failure) };
    } catch {
      return undefined;
    }
  }

  const engine: TaskEngine = {
    async create({ employee, form, attachments, actor }) {
      if (actor !== "initiator" && actor !== "system") throw new Error(`角色 ${actor} 不能创建任务`);
      // employee 来自不可信输入（HTTP 请求）：先校验再做任何文件操作
      if (!isEmployeeId(employee)) {
        throw new Error(`员工 id 不合法：${JSON.stringify(employee)}（只允许小写字母、数字和连字符）`);
      }
      const employeesRoot = resolve(deps.employeesDir);
      const source = join(employeesRoot, employee);
      // 兜底：即使将来放宽 id 规则，解析后的路径也必须仍在员工配置目录里
      if (dirname(resolve(source)) !== employeesRoot) throw new Error(`员工 id 不合法：${JSON.stringify(employee)}（超出员工配置目录）`);
      if (!existsSync(source) || !statSync(source).isDirectory()) throw new Error(`员工配置目录不存在：${source}`);
      if (!existsSync(join(source, "workflow.yaml"))) throw new Error(`员工配置目录缺少 workflow.yaml：${source}`);
      for (const a of attachments) if (!existsSync(a.sourcePath)) throw new Error(`附件不存在：${a.sourcePath}`);

      const stamp = now().toISOString().replace(/[-:T]/g, "").slice(0, 14);
      const id = `${stamp}-${randomUUID().slice(0, 6)}`;
      const dir = join(tasksDir, id);
      for (const sub of ["attachments", "artifacts", "deliverables"]) mkdirSync(join(dir, sub), { recursive: true });
      // 启动时把员工配置复制进任务目录，运行中的任务始终使用这一版
      cpSync(source, join(dir, "config"), { recursive: true });
      const config = loadEmployeeConfigSync(join(dir, "config"));

      const rt: Runtime = {
        id,
        dir,
        config,
        proj: new TaskProjection(config),
        events: [],
        logPath: join(dir, "events.jsonl"),
        listeners: new Set(),
        redrive: false,
        artifactCache: new Map(),
      };
      runtimes.set(id, rt);
      const refs = copyAttachments(rt, attachments);
      append(rt, { type: "task_created", employee, configHash: config.hash, form, attachments: refs });
      return snapshotOf(rt);
    },

    async command(taskId, command, actor) {
      const rt = openRuntime(taskId);
      if (!rt) return { ok: false, error: "not_found", message: `任务 ${taskId} 不存在` };
      const trigger = COMMAND_TRIGGER[command.type];
      const from = rt.proj.state;
      const candidates = TRANSITIONS.filter((t) => t.from === from && t.trigger === trigger);
      if (candidates.length === 0) {
        return reject(rt, trigger, actor, "illegal_transition", `任务当前处于「${STATE_LABELS[from]}」，不能执行「${command.type}」。`);
      }
      const matched = candidates.find((t) => t.actor === actor);
      if (!matched) {
        return reject(rt, trigger, actor, "forbidden_role", `角色 ${actor} 无权在「${STATE_LABELS[from]}」执行「${command.type}」，应由 ${candidates.map((t) => t.actor).join(" / ")} 执行。`);
      }

      switch (command.type) {
        case "supplement": {
          for (const a of command.attachments ?? []) {
            if (!existsSync(a.sourcePath)) return { ok: false, error: "invalid_input", message: `附件不存在：${a.sourcePath}` };
          }
          const added = copyAttachments(rt, command.attachments ?? []);
          append(rt, {
            type: "form_updated",
            form: { ...rt.proj.form, ...(command.form ?? {}) },
            attachments: [...rt.proj.attachments, ...added],
            actor,
          });
          break;
        }
        case "continue_without_minutes":
          append(rt, { type: "form_updated", form: { ...rt.proj.form, allowMissingMinutes: true }, attachments: rt.proj.attachments, actor });
          break;
        case "continue_with_partial":
          append(rt, { type: "form_updated", form: { ...rt.proj.form, allowPartialPeriod: true }, attachments: rt.proj.attachments, actor });
          break;
        case "approve":
          append(rt, { type: "review_decision", decision: "approve" });
          break;
        case "return": {
          const comment = command.comment?.trim();
          if (!comment) return { ok: false, error: "invalid_input", message: "退回必须填写意见。" };
          append(rt, { type: "review_decision", decision: "return", comment, sections: command.sections });
          break;
        }
        default:
          break;
      }
      transition(rt, trigger, actor);
      const snapshot = snapshotOf(rt);
      if (ACTIVE_STATES.includes(rt.proj.state)) kick(rt);
      return { ok: true, snapshot };
    },

    async get(taskId) {
      const rt = openRuntime(taskId);
      return rt ? snapshotOf(rt) : undefined;
    },

    async list() {
      const summaries: TaskSummary[] = [];
      if (!existsSync(tasksDir)) return summaries;
      for (const id of readdirSync(tasksDir)) {
        const cached = runtimes.get(id);
        if (cached) {
          summaries.push(summaryOf(cached));
          continue;
        }
        try {
          const rt = loadRuntime(id);
          if (!rt) continue;
          // 只保留活跃任务的完整 Runtime；空闲任务（草稿/暂停/终态）按需重读，
          // 否则列一次任务就会把所有任务的全部事件与配置永久缓存下来。
          if (ACTIVE_STATES.includes(rt.proj.state)) runtimes.set(id, rt);
          summaries.push(summaryOf(rt));
        } catch (failure) {
          // 一个任务的日志损坏不能拖垮整个列表：它仍以最后已知状态出现，并带上错误说明
          const salvaged = salvagedSummary(id, failure);
          if (salvaged) summaries.push(salvaged);
        }
      }
      return summaries.sort((a, b) => (a.updatedAt < b.updatedAt ? 1 : a.updatedAt > b.updatedAt ? -1 : 0));
    },

    async events(taskId, afterSeq = 0) {
      const rt = openRuntime(taskId);
      return rt ? rt.events.filter((e) => e.seq > afterSeq) : [];
    },

    subscribe(taskId, listener) {
      const rt = openRuntime(taskId);
      if (!rt) return () => {};
      rt.listeners.add(listener);
      return () => rt.listeners.delete(listener);
    },

    async waitForSettled(taskId) {
      const rt = openRuntime(taskId);
      if (!rt) throw new Error(`任务 ${taskId} 不存在`);
      for (;;) {
        if (rt.failure) throw rt.failure;
        const pending = rt.driving;
        if (!pending) break;
        await pending;
      }
      if (rt.failure) throw rt.failure;
      return snapshotOf(rt);
    },

    async resumeAll() {
      if (!existsSync(tasksDir)) return;
      for (const id of readdirSync(tasksDir)) {
        try {
          const cached = runtimes.get(id);
          const rt = cached ?? loadRuntime(id);
          if (!rt || !ACTIVE_STATES.includes(rt.proj.state)) continue;
          // 只有需要恢复执行的活跃任务才留在内存里；暂停/终态任务读完就丢，和 list() 一致
          if (!cached) runtimes.set(id, rt);
          kick(rt);
        } catch {
          // 单个任务日志损坏不能挡住服务启动；它会在 list() 里以带 error 的摘要出现，等待人工处理
          continue;
        }
      }
    },
  };
  return engine;
}

type DistributiveOmit<T, K extends PropertyKey> = T extends unknown ? Omit<T, K> : never;

export type { Checkpoint };
void PAUSED_OR_FINAL;
