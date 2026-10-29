// 命令行测试用的内存 TaskEngine 假实现：流转由测试给的 rules 决定。
import type {
  AttachmentInput,
  CommandResult,
  PauseCode,
  Role,
  TaskCommand,
  TaskEngine,
  TaskEvent,
  TaskSnapshot,
  TaskState,
} from "@de/core/contract";

export interface Outcome {
  state: TaskState;
  pauseCode?: PauseCode;
  deliverables?: TaskSnapshot["deliverables"];
}

export type Rule = (ctx: { state: TaskState; command: TaskCommand; actor: Role; attachments: AttachmentInput[] }) => Outcome | { error: "forbidden_role" | "illegal_transition" };

export interface FakeEngine extends TaskEngine {
  readonly calls: { type: "create" | "command" | "wait"; detail?: unknown }[];
}

export function createFakeEngine(rule: Rule): FakeEngine {
  const calls: FakeEngine["calls"] = [];
  const snapshots = new Map<string, TaskSnapshot>();
  const events = new Map<string, TaskEvent[]>();

  function push(id: string, body: Record<string, unknown>) {
    const list = events.get(id)!;
    const seq = list.length + 1;
    list.push({ seq, ts: `2026-10-28T10:00:${String(seq).padStart(2, "0")}.000Z`, taskId: id, ...body } as TaskEvent);
    snapshots.get(id)!.lastSeq = seq;
  }

  return {
    calls,
    async create(input) {
      calls.push({ type: "create", detail: input });
      const id = `T${snapshots.size + 1}`;
      snapshots.set(id, {
        id,
        employee: input.employee,
        configHash: "h",
        state: "draft",
        form: input.form,
        attachments: input.attachments.map((a) => ({ name: a.name, path: a.sourcePath })),
        steps: [],
        budget: { modelCalls: { used: 0, limit: 1 }, elapsedMs: { used: 0, limit: 1 } },
        reworkRound: 0,
        deliverables: [],
        lastSeq: 0,
      });
      events.set(id, []);
      push(id, { type: "task_created", employee: input.employee, configHash: "h", form: input.form, attachments: [] });
      return structuredClone(snapshots.get(id)!);
    },
    async command(taskId, command, actor): Promise<CommandResult> {
      calls.push({ type: "command", detail: { taskId, command, actor } });
      const snap = snapshots.get(taskId);
      if (!snap) return { ok: false, error: "not_found", message: "无此任务" };
      const attachments = command.type === "supplement" ? (command.attachments ?? []) : [];
      const out = rule({ state: snap.state, command, actor, attachments });
      if ("error" in out) {
        push(taskId, { type: "transition_rejected", from: snap.state, trigger: command.type, actor, reason: out.error });
        return { ok: false, error: out.error, message: `被拒绝：${out.error}` };
      }
      push(taskId, { type: "state_changed", from: snap.state, to: out.state, trigger: command.type, actor });
      snap.state = out.state;
      snap.pause = out.pauseCode ? { code: out.pauseCode, message: "m", options: [] } : undefined;
      if (out.deliverables) snap.deliverables = out.deliverables;
      return { ok: true, snapshot: structuredClone(snap) };
    },
    async get(id) {
      const s = snapshots.get(id);
      return s ? structuredClone(s) : undefined;
    },
    async list() {
      return [];
    },
    async events(id, after = 0) {
      return (events.get(id) ?? []).filter((e) => e.seq > after);
    },
    subscribe: () => () => {},
    async waitForSettled(id) {
      calls.push({ type: "wait", detail: id });
      return structuredClone(snapshots.get(id)!);
    },
    async resumeAll() {},
  };
}
