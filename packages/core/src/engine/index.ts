// T1 实现：任务引擎（状态机、事件日志、编排器、断点恢复）。
import { createHash, randomUUID } from "node:crypto";
import { cpSync, existsSync, mkdirSync, readFileSync, readdirSync, renameSync, statSync, writeFileSync, copyFileSync } from "node:fs";
import { basename, join, resolve } from "node:path";
import { createJudgmentRunner } from "../agent";
import { loadEmployeeConfigSync } from "../config";
import { createQcEngine } from "../qc";
import {
  STATE_LABELS,
  TRANSITIONS,
  type AttachmentInput,
  type AttachmentRef,
  type CommandResult,
  type DeliverableRef,
  type DraftReport,
  type EmployeeConfig,
  type EngineDeps,
  type JudgmentFailure,
  type JudgmentRunner,
  type PauseCode,
  type PauseReason,
  type QcCheckResult,
  type QcEngine,
  type QcInput,
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
import { appendEventLine, readEventLog } from "./event-log";
import { DRAFT_STEP_ID, TaskProjection, argsKey, type Checkpoint } from "./projection";
import { resolveInput } from "./refs";

/** 测试可注入的内部实现；生产环境不传，使用 ../agent 与 ../qc 的默认实现。 */
export interface EngineInternals {
  judgment?: JudgmentRunner;
  qc?: QcEngine;
}

/** 模拟进程被杀：`faults.crashAtStep` 命中时抛出。抛出之后这个引擎实例不再写任何事件。 */
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

/** 质检输入从这些步骤的输出里取（约定的步骤 id，见 T5 的 workflow.yaml）。 */
const QC_SOURCE_STEPS = { facts: "compute_metrics", minutes: "parse_minutes", rendered: "render_document" } as const;

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

  let judgment = internals.judgment;
  let qcEngine = internals.qc;
  const getJudgment = () => (judgment ??= createJudgmentRunner());
  const getQc = () => (qcEngine ??= createQcEngine());

  // ---------------------------------------------------------------- 事件与运行时

  function openRuntime(id: TaskId): Runtime | undefined {
    const cached = runtimes.get(id);
    if (cached) return cached;
    const dir = join(tasksDir, id);
    const logPath = join(dir, "events.jsonl");
    if (!existsSync(logPath)) return undefined;
    const events = readEventLog(logPath);
    if (events.length === 0) return undefined;
    const config = loadEmployeeConfigSync(join(dir, "config"));
    const proj = new TaskProjection(config);
    for (const e of events) proj.apply(e);
    const rt: Runtime = { id, dir, config, proj, events, logPath, listeners: new Set(), redrive: false, artifactCache: new Map() };
    runtimes.set(id, rt);
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
    const t = TRANSITIONS.find((x) => x.from === from && x.trigger === trigger);
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

  function stepOutput(rt: Runtime, stepId: string): unknown {
    const checkpoint = rt.proj.done.get(stepId);
    if (checkpoint) return readArtifact(rt, checkpoint);
    if (rt.proj.skipped.has(stepId)) return undefined;
    throw new Error(`步骤 ${stepId} 尚无输出`);
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
      transition(rt, "admission_failed", "system", {
        code: "tool_failed",
        message,
        options: initiatorOptions("tool_failed"),
      });
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
      args = resolveInput(step.input, {
        form: proj.form,
        taskDir: rt.dir,
        qcConfig: rt.config.qc,
        stepOutput: (id) => stepOutput(rt, id),
      });
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
    const crashArmed = deps.faults?.crashAtStep === step.id;
    if (crashArmed && step.kind === "compute") crash(rt, step.id);

    if (resolveError) return failStep(rt, step, "tool_failed", `步骤「${step.label}」的输入无法解析：${errorText(resolveError)}。${progressText(rt)}。`);

    return step.kind === "compute"
      ? runCompute(rt, step, args as Record<string, unknown>)
      : runJudge(rt, step, args as Record<string, unknown>, crashArmed);
  }

  function crash(rt: Runtime, stepId: string): never {
    append(rt, { type: "fault_injected", target: stepId, mode: "crash" });
    dead = new SimulatedCrash(stepId);
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
    if (rt.proj.state === "admission") {
      transition(rt, "admission_failed", "system", { code: "tool_failed", message, options: initiatorOptions("tool_failed"), detail });
    } else {
      pauseOperator(rt, code, code, message, detail);
    }
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
    if (proj.qc?.some((c) => !c.passed)) {
      for (const check of proj.qc) if (!check.passed) corrections.push(...check.issues.map((issue) => `质检「${check.label}」：${issue}`));
    }

    const nowMs = now().getTime();
    let result: Awaited<ReturnType<JudgmentRunner["run"]>>;
    try {
      result = await getJudgment().run({
        taskId: rt.id,
        step,
        systemPrompt: [rt.config.skill, prompt].filter(Boolean).join("\n\n"),
        context,
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
    if (deps.faults?.corruptTraceability && step.id === DRAFT_STEP_ID && !proj.faultsInjected.has(`${step.id}:corruptTraceability`)) {
      output = corruptFirstParagraph(output);
      append(rt, { type: "fault_injected", target: step.id, mode: "corruptTraceability" });
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
      if (deps.faults?.crashAtStep === qcStep.id) crash(rt, qcStep.id);

      let checks: QcCheckResult[];
      try {
        const rendered = optionalOutput(rt, QC_SOURCE_STEPS.rendered) as { estimatedPages?: number; chartCount?: number } | undefined;
        const input: QcInput = {
          config: rt.config.qc,
          draft: optionalOutput(rt, DRAFT_STEP_ID) as DraftReport,
          facts: optionalOutput(rt, QC_SOURCE_STEPS.facts) as QcInput["facts"],
          minutes: optionalOutput(rt, QC_SOURCE_STEPS.minutes) as QcInput["minutes"],
          rendered:
            rendered && typeof rendered.estimatedPages === "number" && typeof rendered.chartCount === "number"
              ? { estimatedPages: rendered.estimatedPages, chartCount: rendered.chartCount }
              : undefined,
          previousDraft:
            proj.reworkRound > 0 && proj.baselineDraft ? (readArtifact(rt, proj.baselineDraft) as DraftReport) : undefined,
          feedback: proj.reworkRound > 0 ? proj.feedback : undefined,
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

  const engine: TaskEngine = {
    async create({ employee, form, attachments, actor }) {
      if (actor !== "initiator" && actor !== "system") throw new Error(`角色 ${actor} 不能创建任务`);
      const source = join(resolve(deps.employeesDir), employee);
      if (!existsSync(source) || !statSync(source).isDirectory()) throw new Error(`员工配置目录不存在：${source}`);
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
        const rt = openRuntime(id);
        if (!rt) continue;
        summaries.push({ id, employee: rt.proj.employee, title: titleOf(rt), state: rt.proj.state, updatedAt: rt.proj.lastTs });
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
        const rt = openRuntime(id);
        if (rt && ACTIVE_STATES.includes(rt.proj.state)) kick(rt);
      }
    },
  };
  return engine;
}

type DistributiveOmit<T, K extends PropertyKey> = T extends unknown ? Omit<T, K> : never;

export type { Checkpoint };
void PAUSED_OR_FINAL;
