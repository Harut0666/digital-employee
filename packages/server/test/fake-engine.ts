// 测试用的内存 TaskEngine 假实现：只覆盖服务端冒烟测试需要的行为。
// 流转规则直接用契约里的 TRANSITIONS，系统触发的流转在 submit 后同步走完。

import {
  TRANSITIONS,
  type AttachmentInput,
  type CommandResult,
  type Role,
  type TaskCommand,
  type TaskEngine,
  type TaskEvent,
  type TaskSnapshot,
  type TaskSummary,
  type TransitionTrigger,
} from "@de/core/contract";

type EventBody = TaskEvent extends infer E ? (E extends unknown ? Omit<E, "seq" | "ts" | "taskId"> : never) : never;

interface Record_ {
  snapshot: TaskSnapshot;
  events: TaskEvent[];
  listeners: Set<(event: TaskEvent) => void>;
}

export interface FakeEngine extends TaskEngine {
  /** 测试里用来观察引擎收到的附件 */
  readonly created: { employee: string; form: Record<string, unknown>; attachments: AttachmentInput[]; actor: Role }[];
  readonly commands: { taskId: string; command: TaskCommand; actor: Role }[];
  /** 测试里直接往任务事件流里塞一个事件 */
  emit(taskId: string, body: EventBody): TaskEvent;
  setDeliverable(taskId: string, kind: "docx" | "md" | "qc_record", path: string): void;
}

const SYSTEM_CHAIN: TransitionTrigger[] = ["admission_passed", "steps_completed", "qc_passed"];

export function createFakeEngine(): FakeEngine {
  const tasks = new Map<string, Record_>();
  let counter = 0;
  const created: FakeEngine["created"] = [];
  const commands: FakeEngine["commands"] = [];

  function emit(taskId: string, body: EventBody): TaskEvent {
    const rec = tasks.get(taskId)!;
    const seq = rec.events.length + 1;
    const event = { ...body, seq, ts: new Date(1_700_000_000_000 + seq * 1000).toISOString(), taskId } as TaskEvent;
    rec.events.push(event);
    rec.snapshot.lastSeq = seq;
    for (const l of [...rec.listeners]) l(event);
    return event;
  }

  function move(taskId: string, trigger: TransitionTrigger, actor: Role): boolean {
    const rec = tasks.get(taskId)!;
    const from = rec.snapshot.state;
    const t = TRANSITIONS.find((x) => x.from === from && x.trigger === trigger);
    if (!t) return false;
    rec.snapshot.state = t.to;
    emit(taskId, { type: "state_changed", from, to: t.to, trigger, actor });
    return true;
  }

  const engine: FakeEngine = {
    created,
    commands,
    emit,
    setDeliverable(taskId, kind, path) {
      tasks.get(taskId)!.snapshot.deliverables.push({ kind, path, sha256: "0".repeat(64) });
    },
    async create(input) {
      created.push(input);
      const id = `T-${++counter}`;
      const snapshot: TaskSnapshot = {
        id,
        employee: input.employee,
        configHash: "hash",
        state: "draft",
        form: input.form,
        attachments: input.attachments.map((a) => ({ name: a.name, path: a.sourcePath })),
        steps: [],
        budget: { modelCalls: { used: 0, limit: 10 }, elapsedMs: { used: 0, limit: 1000 } },
        reworkRound: 0,
        deliverables: [],
        lastSeq: 0,
      };
      tasks.set(id, { snapshot, events: [], listeners: new Set() });
      emit(id, { type: "task_created", employee: input.employee, configHash: "hash", form: input.form, attachments: snapshot.attachments });
      return structuredClone(snapshot);
    },
    async command(taskId, command, actor): Promise<CommandResult> {
      commands.push({ taskId, command, actor });
      const rec = tasks.get(taskId);
      if (!rec) return { ok: false, error: "not_found", message: "任务不存在" };
      const trigger = command.type as TransitionTrigger;
      const candidates = TRANSITIONS.filter((x) => x.from === rec.snapshot.state && x.trigger === trigger);
      const hit = candidates.find((x) => x.actor === actor);
      if (!hit) {
        const known = candidates.length > 0;
        emit(taskId, {
          type: "transition_rejected",
          from: rec.snapshot.state,
          trigger,
          actor,
          reason: known ? "forbidden_role" : "illegal_transition",
        });
        return known
          ? { ok: false, error: "forbidden_role", message: `角色 ${actor} 不能执行 ${command.type}` }
          : { ok: false, error: "illegal_transition", message: `当前状态不能执行 ${command.type}` };
      }
      move(taskId, trigger, actor);
      if (command.type === "submit") for (const step of SYSTEM_CHAIN) move(taskId, step, "system");
      return { ok: true, snapshot: structuredClone(rec.snapshot) };
    },
    async get(taskId) {
      const rec = tasks.get(taskId);
      return rec ? structuredClone(rec.snapshot) : undefined;
    },
    async list(): Promise<TaskSummary[]> {
      return [...tasks.values()].map((r) => ({
        id: r.snapshot.id,
        employee: r.snapshot.employee,
        title: `${r.snapshot.employee} ${r.snapshot.id}`,
        state: r.snapshot.state,
        updatedAt: "2023-11-14T22:13:20.000Z",
      }));
    },
    async events(taskId, afterSeq = 0) {
      return (tasks.get(taskId)?.events ?? []).filter((e) => e.seq > afterSeq);
    },
    subscribe(taskId, listener) {
      const rec = tasks.get(taskId);
      if (!rec) return () => {};
      rec.listeners.add(listener);
      return () => rec.listeners.delete(listener);
    },
    async waitForSettled(taskId) {
      const rec = tasks.get(taskId)!;
      return structuredClone(rec.snapshot);
    },
    async resumeAll() {},
  };
  return engine;
}
