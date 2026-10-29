// 事件投影：任务的状态、各步骤的检查点、预算，全部由重放事件得到（design.md §8）。
import type {
  AttachmentRef,
  DeliverableRef,
  EmployeeConfig,
  PauseReason,
  QcCheckResult,
  StepStatus,
  TaskEvent,
  TaskSnapshot,
  TaskState,
} from "../contract";

export interface ArtifactRef {
  path: string;
  sha256: string;
}

/** 已完成步骤的检查点。args 是计算步骤的入参（稳定序列化），用于判断重做时能否复用。 */
export interface Checkpoint extends ArtifactRef {
  args?: string;
}

const ACTIVE_STATES: readonly TaskState[] = ["admission", "running", "qc"];
const PAUSED_STATES: readonly TaskState[] = ["awaiting_initiator", "awaiting_operator", "awaiting_review"];

/** 报告草稿所在的步骤 id；退回时要保留重做前的版本，供“意见响应”质检对比。 */
export const DRAFT_STEP_ID = "write_report";

export class TaskProjection {
  employee = "";
  configHash = "";
  form: Record<string, unknown> = {};
  attachments: AttachmentRef[] = [];
  state: TaskState = "draft";
  pause?: PauseReason;
  reworkRound = 0;
  qc?: QcCheckResult[];
  qcAttempts = 0;
  lastSeq = 0;
  createdTs = "";
  lastTs = "";

  /** 当前一轮中已完成的步骤 */
  readonly done = new Map<string, Checkpoint>();
  /** 被回退的步骤的旧检查点，计算步骤入参不变时直接复用 */
  readonly prior = new Map<string, Checkpoint>();
  /** 可选步骤失败后被跳过 */
  readonly skipped = new Set<string>();
  readonly failed = new Set<string>();
  running?: string;
  readonly startCounts = new Map<string, number>();
  private readonly lastToolArgs = new Map<string, string>();
  /** 步骤自上次结束（完成或失败）以来记录的模型响应，断点恢复时重放 */
  readonly modelResponses = new Map<string, { turn: number; response: unknown }[]>();

  /** 预算窗口：任务开始，以及每次操作员重试、审核人退回后重新计算 */
  modelCalls = 0;
  private activeAccumMs = 0;
  private activeSince?: number;
  autofixCount = 0;

  feedback?: { comment: string; sections?: string[] };
  /** 退回时的报告草稿，供重做后的质检对比 */
  baselineDraft?: ArtifactRef;
  private readonly deliverableMap = new Map<DeliverableRef["kind"], DeliverableRef>();
  faultsInjected = new Set<string>();

  constructor(private readonly config: EmployeeConfig) {}

  private get steps() {
    return this.config.workflow.steps;
  }

  private stepIndex(id: string): number {
    return this.steps.findIndex((s) => s.id === id);
  }

  /** 退回后从哪个步骤重做：第一个标记了 rework 的判断步骤。 */
  firstReworkIndex(): number {
    const marked = this.steps.findIndex((s) => s.kind === "judge" && s.rework);
    if (marked >= 0) return marked;
    const anyJudge = this.steps.findIndex((s) => s.kind === "judge");
    return anyJudge >= 0 ? anyJudge : 0;
  }

  /** 质检不通过后从哪个步骤修正：不通过的检查给出的 fixStep 中最靠前的一个。 */
  fixIndex(): number {
    const indexes = (this.qc ?? [])
      .filter((c) => !c.passed && c.fixStep)
      .map((c) => this.stepIndex(c.fixStep!))
      .filter((i) => i >= 0);
    return indexes.length > 0 ? Math.min(...indexes) : this.firstReworkIndex();
  }

  private rewind(fromIndex: number): void {
    this.steps.forEach((step, i) => {
      if (i < fromIndex) return;
      const checkpoint = this.done.get(step.id);
      if (checkpoint) this.prior.set(step.id, checkpoint);
      this.done.delete(step.id);
      this.skipped.delete(step.id);
    });
  }

  elapsedMs(nowMs: number): number {
    return this.activeAccumMs + (this.activeSince !== undefined ? Math.max(0, nowMs - this.activeSince) : 0);
  }

  get deliverables(): DeliverableRef[] {
    return [...this.deliverableMap.values()];
  }

  apply(e: TaskEvent): void {
    this.lastSeq = e.seq;
    this.lastTs = e.ts;
    switch (e.type) {
      case "task_created":
        this.employee = e.employee;
        this.configHash = e.configHash;
        this.form = e.form;
        this.attachments = e.attachments;
        this.createdTs = e.ts;
        break;
      case "form_updated":
        this.form = e.form;
        this.attachments = e.attachments;
        break;
      case "step_started":
        this.running = e.stepId;
        this.failed.delete(e.stepId);
        this.startCounts.set(e.stepId, (this.startCounts.get(e.stepId) ?? 0) + 1);
        break;
      case "step_completed": {
        const args = this.running === e.stepId ? this.lastToolArgs.get(e.stepId) : this.prior.get(e.stepId)?.args;
        this.done.set(e.stepId, { path: e.outputPath, sha256: e.outputSha256, args });
        this.finishStep(e.stepId);
        break;
      }
      case "step_failed": {
        const step = this.steps[this.stepIndex(e.stepId)];
        if (step && "optional" in step && step.optional) this.skipped.add(e.stepId);
        else this.failed.add(e.stepId);
        this.finishStep(e.stepId);
        break;
      }
      case "tool_called":
        this.lastToolArgs.set(e.stepId, argsKey(e.args));
        break;
      case "model_called": {
        this.modelCalls += 1;
        const list = this.modelResponses.get(e.stepId) ?? [];
        list.push({ turn: e.turn, response: e.response });
        this.modelResponses.set(e.stepId, list);
        break;
      }
      case "qc_result":
        this.qc = e.checks;
        this.qcAttempts += 1;
        break;
      case "review_decision":
        if (e.decision === "return") this.feedback = { comment: e.comment ?? "", sections: e.sections };
        break;
      case "deliverable_created":
        this.deliverableMap.set(e.kind, { kind: e.kind, path: e.path, sha256: e.sha256 });
        break;
      case "fault_injected":
        this.faultsInjected.add(`${e.target}:${e.mode}`);
        break;
      case "state_changed":
        this.applyStateChange(e);
        break;
      default:
        break;
    }
  }

  private finishStep(stepId: string): void {
    if (this.running === stepId) this.running = undefined;
    this.modelResponses.delete(stepId);
  }

  private applyStateChange(e: Extract<TaskEvent, { type: "state_changed" }>): void {
    const ts = Date.parse(e.ts);
    const previousPause = this.pause;

    switch (e.trigger) {
      case "supplement":
      case "continue_without_minutes":
      case "continue_with_partial":
        this.rewind(0);
        break;
      case "qc_autofix":
        this.autofixCount += 1;
        this.rewind(this.fixIndex());
        break;
      case "retry":
        if (previousPause?.code === "qc_exhausted") this.rewind(this.fixIndex());
        this.resetWindow();
        break;
      case "return": {
        this.baselineDraft = this.done.get(DRAFT_STEP_ID);
        this.rewind(this.firstReworkIndex());
        this.reworkRound += 1;
        this.resetWindow();
        break;
      }
      default:
        break;
    }

    if (this.activeSince !== undefined) {
      this.activeAccumMs += Math.max(0, ts - this.activeSince);
      this.activeSince = undefined;
    }
    if (ACTIVE_STATES.includes(e.to)) this.activeSince = ts;

    this.state = e.to;
    this.pause = PAUSED_STATES.includes(e.to) ? e.pause : undefined;
    if (this.running) this.running = undefined;
  }

  private resetWindow(): void {
    this.modelCalls = 0;
    this.activeAccumMs = 0;
    this.activeSince = undefined;
    this.autofixCount = 0;
  }

  /** 第一个尚未完成的步骤（不含质检步骤）；全部完成返回 -1。 */
  nextStepIndex(excludeId?: string): number {
    return this.steps.findIndex(
      (s) => s.kind !== "qc" && s.id !== excludeId && !this.done.has(s.id) && !this.skipped.has(s.id),
    );
  }

  stepStatuses(): StepStatus[] {
    return this.steps.map((s) => {
      let status: StepStatus["status"] = "pending";
      if (this.skipped.has(s.id)) status = "skipped";
      else if (this.done.has(s.id)) status = "done";
      else if (this.running === s.id && ACTIVE_STATES.includes(this.state)) status = "running";
      else if (this.failed.has(s.id)) status = "failed";
      return { id: s.id, label: s.label, kind: s.kind, status };
    });
  }

  snapshot(taskId: string, nowMs: number): TaskSnapshot {
    const limits = this.config.workflow.limits.task;
    return {
      id: taskId,
      employee: this.employee,
      configHash: this.configHash,
      state: this.state,
      pause: this.pause,
      form: this.form,
      attachments: this.attachments,
      steps: this.stepStatuses(),
      budget: {
        modelCalls: { used: this.modelCalls, limit: limits.modelCalls },
        elapsedMs: { used: this.elapsedMs(nowMs), limit: limits.durationSec * 1000 },
      },
      reworkRound: this.reworkRound,
      qc: this.qc,
      deliverables: this.deliverables,
      lastSeq: this.lastSeq,
    };
  }
}

export function sortKeys(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(sortKeys);
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value as Record<string, unknown>)
        .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
        .map(([k, v]) => [k, sortKeys(v)]),
    );
  }
  return value;
}

/** 入参的稳定序列化（键排序），用于比较两次工具调用的入参是否相同。 */
export function argsKey(args: unknown): string {
  return JSON.stringify(sortKeys(args ?? null));
}
