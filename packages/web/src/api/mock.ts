// 模拟数据适配器（VITE_MOCK=1）：不依赖后端，在内存里模拟任务引擎。
// 可用操作、等待角色和暂停说明由 mock-rules.ts 扮演后端给出，视图层和接真实后端时一样只读快照。

import type { AttachmentRef, PauseReason, TaskCommand, TaskEvent, TaskState } from "@de/core/contract";
import { STATE_LABELS } from "@de/core/contract";
import { deriveSessionView } from "../view/session-view";
import { createMockConfigStore } from "./mock-config";
import { EMPLOYEES, QC_CHECKS_PASSED, REWORK_STEPS, SEEDS, STEP_DEFS, type EventInput } from "./mock-data";
import { actionsOf, pauseTextOf, waitingForOf } from "./mock-rules";
import type { CommandResult, CreateTaskInput, DeliverableKind, EmployeeInfo, PauseView, TaskSnapshot, TaskSummary, UiRole, UploadItem, WebApi } from "./types";

interface MockTask {
  id: string;
  employee: string;
  title: string;
  form: Record<string, unknown>;
  attachments: AttachmentRef[];
  uploads: UploadItem[];
  events: TaskEvent[];
  listeners: Set<(e: TaskEvent) => void>;
  allowMissingMinutes: boolean;
  allowPartial: boolean;
}

const STEP_GAP_MS = 900;
const MODEL_LIMIT = 30;
const TIME_LIMIT_MS = 600_000;

export function createMockApi(getRole: () => UiRole): WebApi {
  const tasks = new Map<string, MockTask>();
  const configs = createMockConfigStore();
  let counter = 100;

  const emit = (t: MockTask, input: EventInput, ts = new Date()) => {
    const ev = { ...input, seq: t.events.length + 1, ts: ts.toISOString(), taskId: t.id } as TaskEvent;
    t.events.push(ev);
    for (const l of [...t.listeners]) l(ev);
    return ev;
  };
  const stateOf = (t: MockTask): TaskState => deriveSessionView(t.events).state ?? "draft";
  const change = (t: MockTask, to: TaskState, trigger: TaskEvent extends infer E ? (E extends { trigger: infer T } ? T : never) : never, actor: "initiator" | "reviewer" | "operator" | "system", pause?: PauseReason) =>
    emit(t, { type: "state_changed", from: stateOf(t), to, trigger, actor, pause } as EventInput);
  const defs = (t: MockTask) => STEP_DEFS[t.employee] ?? [];

  // ---- 种子 ----
  for (const seed of SEEDS) {
    const t: MockTask = {
      id: seed.id,
      employee: seed.employee,
      title: seed.title,
      form: seed.form,
      attachments: [],
      uploads: [],
      events: [],
      listeners: new Set(),
      allowMissingMinutes: false,
      allowPartial: false,
    };
    const base = Date.now() - seed.startedAgoMs;
    for (const [offset, input] of seed.script) emit(t, input, new Date(base + offset * 1000));
    tasks.set(t.id, t);
    if (seed.autoplay) setTimeout(() => runSteps(t, defs(t).slice(seed.autoplay!.fromStepIndex, -1).map((s) => s.id), 0), 2500);
  }

  // ---- 脚本化执行 ----
  /** 依次执行 stepIds（不含质检），然后进入质检、待审核。 */
  function runSteps(t: MockTask, stepIds: string[], round: number) {
    const queue: (() => void)[] = [];
    for (const id of stepIds) {
      const def = defs(t).find((d) => d.id === id);
      queue.push(() => emit(t, { type: "step_started", stepId: id, attempt: 1, reworkRound: round }));
      if (def?.kind === "judge") {
        queue.push(() => emit(t, { type: "model_called", stepId: id, turn: 1, response: {}, durationMs: 2400 }));
        queue.push(() => emit(t, { type: "model_called", stepId: id, turn: 2, response: {}, durationMs: 3100 }));
      }
      queue.push(() => emit(t, { type: "step_completed", stepId: id, reworkRound: round, outputPath: `steps/${id}.json`, outputSha256: "mock" }));
    }
    queue.push(() => change(t, "qc", "steps_completed", "system"));
    queue.push(() => emit(t, { type: "step_started", stepId: "qc", attempt: 1, reworkRound: round }));
    queue.push(() => {
      emit(t, { type: "qc_result", attempt: 1, passed: true, checks: QC_CHECKS_PASSED });
      emit(t, { type: "step_completed", stepId: "qc", reworkRound: round, outputPath: "qc.json", outputSha256: "mock" });
      emit(t, { type: "deliverable_created", kind: "docx", path: `经营周报_v${round + 1}.docx`, sha256: `docx-${round}` });
      emit(t, { type: "deliverable_created", kind: "qc_record", path: "检验记录.md", sha256: `qc-${round}` });
    });
    queue.push(() =>
      change(t, "awaiting_review", "qc_passed", "system", { code: "awaiting_review", message: "质检通过，请审核", options: ["approve", "return"] }),
    );
    queue.forEach((fn, i) => setTimeout(fn, (i + 1) * STEP_GAP_MS));
  }

  /** 准入校验：缺销售明细 / 缺纪要时停在待发起人补充。 */
  function admit(t: MockTask) {
    setTimeout(() => {
      const names = t.uploads.map((u) => u.file.name);
      const hasSales = t.employee !== "weekly-report" || t.uploads.some((u) => u.slot === "salesFile");
      const hasMinutes = t.employee !== "weekly-report" || t.uploads.some((u) => u.slot === "minutesFiles") || t.allowMissingMinutes;
      const needDims = t.employee === "weekly-report" && hasSales && !names.some((n) => /\.xlsx?$/i.test(n)) && !(names.some((n) => /门店/.test(n)) && names.some((n) => /目标/.test(n)));
      const options: TaskCommand["type"][] = ["supplement", "terminate"];
      if (!hasSales) {
        change(t, "awaiting_initiator", "admission_failed", "system", { code: "missing_sales", message: "缺少销售明细，请上传销售明细（Excel 或 CSV）。", options });
      } else if (needDims && !t.allowPartial) {
        change(t, "awaiting_initiator", "admission_failed", "system", {
          code: "missing_dimensions",
          message: "只上传了 CSV 销售明细，缺少门店信息和销售目标两张维表，无法计算区域和达成率。",
          options,
        });
      } else if (!hasMinutes) {
        change(t, "awaiting_initiator", "admission_failed", "system", {
          code: "minutes_missing",
          message: "缺少本周周会纪要。可以补传纪要，或选择“无纪要继续”（异常原因将标注“原因待核实”）。",
          options: [...options, "continue_without_minutes"],
        });
      } else {
        change(t, "running", "admission_passed", "system");
        runSteps(t, defs(t).slice(0, -1).map((s) => s.id), 0);
      }
    }, 600);
  }

  const actionsNow = (t: MockTask) => {
    const view = deriveSessionView(t.events);
    return actionsOf(view.state ?? "draft", view.pause?.code);
  };

  const summary = (t: MockTask): TaskSummary => {
    const state = stateOf(t);
    const pause = deriveSessionView(t.events).pause;
    return {
      id: t.id,
      employee: t.employee,
      title: t.title,
      state,
      updatedAt: t.events.at(-1)?.ts ?? new Date().toISOString(),
      statusText: pause ? pauseTextOf(state, pause.code).title : STATE_LABELS[state],
      waitingFor: waitingForOf(state, actionsNow(t)),
    };
  };

  const snapshot = (t: MockTask): TaskSnapshot => {
    const view = deriveSessionView(t.events, defs(t));
    const state = view.state ?? "draft";
    const actions = actionsNow(t);
    const pause: PauseView | undefined = view.pause && { ...view.pause, ...pauseTextOf(state, view.pause.code), options: actions.map((a) => a.command) };
    return {
      id: t.id,
      employee: t.employee,
      configHash: "mock",
      state,
      pause,
      actions,
      waitingFor: waitingForOf(state, actions),
      form: t.form,
      attachments: t.attachments,
      steps: view.steps,
      budget: {
        modelCalls: { used: view.counts.modelCalls, limit: MODEL_LIMIT },
        elapsedMs: { used: view.elapsedMs, limit: TIME_LIMIT_MS },
      },
      reworkRound: Math.max(0, ...t.events.flatMap((e) => (e.type === "step_started" ? [e.reworkRound] : []))),
      qc: view.qc?.checks,
      deliverables: view.deliverables,
      lastSeq: view.lastSeq,
    };
  };

  const attach = (t: MockTask, uploads: UploadItem[]) => {
    t.uploads.push(...uploads);
    t.attachments.push(...uploads.map((u) => ({ name: u.file.name, path: `workspace/${t.id}/${u.file.name}` })));
  };

  const later = <T>(v: T) => new Promise<T>((r) => setTimeout(() => r(v), 80));

  return {
    listEmployees: () => later(EMPLOYEES as EmployeeInfo[]),
    listTasks: () => later([...tasks.values()].map(summary)),
    getTask: (id) => later(tasks.get(id) && snapshot(tasks.get(id)!)),
    getEvents: (id, after = 0) => later((tasks.get(id)?.events ?? []).filter((e) => e.seq > after)),

    async createTask(input: CreateTaskInput) {
      const employee = EMPLOYEES.find((e) => e.id === input.employee);
      const period = input.form.period as { week?: number } | undefined;
      const t: MockTask = {
        id: `mock-n${++counter}`,
        employee: input.employee,
        title: period?.week ? `第 ${period.week} 周周报` : `${employee?.name ?? "任务"} ${counter}`,
        form: input.form,
        attachments: [],
        uploads: [],
        events: [],
        listeners: new Set(),
        allowMissingMinutes: false,
        allowPartial: false,
      };
      attach(t, input.uploads);
      emit(t, { type: "task_created", employee: t.employee, configHash: "mock", form: t.form, attachments: t.attachments });
      tasks.set(t.id, t);
      return later(snapshot(t));
    },

    async command(id, command, uploads) {
      const t = tasks.get(id);
      if (!t) return { ok: false, error: "not_found", message: "任务不存在" } satisfies CommandResult;
      const role = getRole();
      const from = stateOf(t);
      const action = actionsNow(t).find((a) => a.command === command.type);
      if (!action?.roles.includes(role)) {
        const forbidden = action !== undefined;
        const reason = forbidden ? "该角色无权执行此操作" : "当前状态不允许此操作";
        emit(t, { type: "transition_rejected", from, trigger: command.type as never, actor: role, reason });
        return later({ ok: false, error: forbidden ? "forbidden_role" : "illegal_transition", message: reason } satisfies CommandResult);
      }

      switch (command.type) {
        case "submit":
          change(t, "admission", "submit", role);
          admit(t);
          break;
        case "supplement":
          if (uploads?.length) attach(t, uploads);
          if (command.form) t.form = { ...t.form, ...command.form };
          emit(t, { type: "form_updated", form: t.form, attachments: t.attachments, actor: role });
          change(t, "admission", "supplement", role);
          admit(t);
          break;
        case "continue_without_minutes":
          t.allowMissingMinutes = true;
          change(t, "admission", "continue_without_minutes", role);
          admit(t);
          break;
        case "continue_with_partial":
          t.allowPartial = true;
          change(t, "admission", "continue_with_partial", role);
          admit(t);
          break;
        case "retry": {
          change(t, "running", "retry", role);
          const failed = deriveSessionView(t.events, defs(t)).steps.filter((s) => s.status !== "done" && s.kind !== "qc").map((s) => s.id);
          runSteps(t, failed, 0);
          break;
        }
        case "terminate":
          change(t, "terminated", "terminate", role);
          break;
        case "approve":
          emit(t, { type: "review_decision", decision: "approve" });
          change(t, "delivered", "approve", role);
          break;
        case "return": {
          emit(t, { type: "review_decision", decision: "return", comment: command.comment, sections: command.sections });
          change(t, "running", "return", role);
          const round = snapshot(t).reworkRound + 1;
          runSteps(t, REWORK_STEPS[t.employee] ?? [], round);
          break;
        }
      }
      return later({ ok: true, snapshot: snapshot(t) } satisfies CommandResult);
    },

    async loadPublicSample() {
      // 模拟公开样例：第 44 周的销售明细与周会纪要（真实后端读工作区 samples/ 里的文件）
      const t: MockTask = {
        id: `mock-sample-${++counter}`,
        employee: "weekly-report",
        title: "第 44 周周报（公开样例）",
        form: { period: { year: 2026, week: 44 } },
        attachments: [],
        uploads: [],
        events: [],
        listeners: new Set(),
        allowMissingMinutes: false,
        allowPartial: false,
      };
      attach(t, [
        { slot: "salesFile", file: new File(["mock-xlsx"], "销售明细.xlsx") },
        { slot: "minutesFiles", file: new File(["mock-docx"], "周会纪要_2026W44.docx") },
      ]);
      emit(t, { type: "task_created", employee: t.employee, configHash: "mock", form: t.form, attachments: t.attachments });
      tasks.set(t.id, t);
      return later(snapshot(t));
    },

    subscribe(id, afterSeq, onEvent) {
      const t = tasks.get(id);
      if (!t) return () => {};
      let closed = false;
      // 先同步补发 afterSeq 之后的事件，再监听新事件
      let last = afterSeq;
      const listener = (e: TaskEvent) => {
        if (closed || e.seq <= last) return;
        last = e.seq;
        onEvent(e);
      };
      t.events.forEach(listener);
      t.listeners.add(listener);
      return () => {
        closed = true;
        t.listeners.delete(listener);
      };
    },

    deliverableUrl: (_id, _kind: DeliverableKind) => undefined,

    // ---- 员工配置页 ----
    getEmployeeConfig: async (id) => later(configs.get(id)),
    saveEmployeeConfig: async (id, path, input) => configs.save(id, path, input),
    listConfigVersions: async (id) => later(configs.versions(id)),
    diffConfigVersion: async (id, versionId) => later(configs.diff(id, versionId)),
    restoreConfigVersion: async (id, versionId) => later(configs.restore(id, versionId)),
  };
}


