// 模拟数据模式的会话：internal/session 的手写简化版，建在模拟任务之上。
// 路由不调模型：按员工名称和描述里的字粗略匹配，匹配不到就列出员工手选。
// 任务卡片和 Go 一样读取时从任务事件投影，按钮取自快照。

import type { TaskEvent } from "@de/core/contract";
import { CHECKLISTS } from "./mock-data";
import { evaluateChecklist } from "./mock-rules";
import type { SessionApi } from "./sessions";
import type {
  EmployeeInfo,
  SessionAgentEvent,
  SessionAttachment,
  SessionCard,
  SessionDetail,
  SessionMessage,
  SessionResult,
  SessionSummary,
  TaskSnapshot,
  TaskSummary,
  TurnAccepted,
  UiRole,
  UploadItem,
  WebApi,
} from "./types";

/** 一句话怎么回：逐字出现的片段（确定性：按标点切成小节，再按两三个字一段） */
function chunksOf(text: string): string[] {
  const pieces = text.match(/[^，。；！？,;!?]+[，。；！？,;!?]?/g) ?? [text];
  const out: string[] = [];
  for (const piece of pieces) {
    for (let i = 0; i < piece.length; i += 2) out.push(piece.slice(i, i + 2));
  }
  return out;
}



type TaskBackend = Pick<WebApi, "listTasks" | "getTask" | "getEvents" | "createTask" | "command">;

interface MockSession {
  id: string;
  /** 正在说的一轮：逐字出现，说完写进消息、以 turn_end 收尾 */
  running?: { turn: number; timer: ReturnType<typeof setTimeout> };
  cancelled?: boolean;
  turn: number;
  /** 会话属于哪个工作区（Spec 0006） */
  workspaceId: string;
  employee?: string;
  taskId?: string;
  draft?: Record<string, unknown>;
  /** 入库卡片上起草的描述（模拟模式不调模型，按任务标题拼一句） */
  filingDraft?: string;
  /** 发起人确认跳过的清单项 */
  skipped: Set<string>;
  files: File[];
  messages: SessionMessage[];
  createdAt: string;
  updatedAt: string;
}

const LEGACY = "task-";
/** 模拟模式只有一个工作区（和 workspaces.ts 的 ws-demo 一致） */
const MOCK_WORKSPACE = "ws-demo";
const PAUSED = new Set(["awaiting_initiator", "awaiting_operator", "awaiting_review"]);

export function createMockSessions(
  employees: EmployeeInfo[],
  getRole: () => UiRole,
  tasks: TaskBackend,
  knowledge?: { filedFrom(taskId: string): string | undefined },
): SessionApi {
  const sessions = new Map<string, MockSession>();
  let counter = 0;

  const fail = (error: string, message: string) => ({ ok: false as const, error, message });
  const write = (s: MockSession, input: Omit<SessionMessage, "id" | "seq" | "ts">, out: SessionMessage[]) => {
    const seq = s.messages.length + 1;
    const message = { id: `s${seq}`, seq, ts: new Date().toISOString(), ...input } as SessionMessage;
    s.messages.push(message);
    s.updatedAt = message.ts;
    out.push(message);
  };
  const attachments = (s: MockSession): SessionAttachment[] => s.files.map((f) => ({ name: f.name, size: f.size }));
  const formCard = (s: MockSession): SessionCard => ({ type: "form", employee: s.employee, draft: s.draft, attachments: attachments(s) });
  const roster = () => employees.map((e) => ({ id: e.id, name: e.name, description: e.description }));
  const checklistOf = (s: MockSession, form = s.draft) => {
    const items = s.employee ? CHECKLISTS[s.employee] : undefined;
    return items ? evaluateChecklist(items, form ?? {}, s.files, s.skipped) : undefined;
  };

  async function summaryOf(taskId: string | undefined): Promise<TaskSummary | undefined> {
    return taskId ? (await tasks.listTasks()).find((t) => t.id === taskId) : undefined;
  }

  async function view(s: MockSession): Promise<SessionSummary> {
    const title = s.messages.find((m) => m.kind === "user")?.text?.slice(0, 30) ?? "新对话";
    const summary = await summaryOf(s.taskId);
    const closed = summary?.state === "delivered" || summary?.state === "terminated";
    return {
      id: s.id,
      workspaceId: s.workspaceId,
      status: s.taskId ? (closed ? "closed" : "task") : s.employee ? "drafting" : "reception",
      readOnly: false,
      title,
      employee: s.employee,
      taskId: s.taskId,
      draft: s.draft,
      attachments: attachments(s),
      createdAt: s.createdAt,
      updatedAt: summary && summary.updatedAt > s.updatedAt ? summary.updatedAt : s.updatedAt,
      taskState: summary?.state,
      statusText: summary?.statusText,
      waitingFor: summary?.waitingFor ?? [],
    };
  }

  /** 任务事件 → 会话里的卡片，和 internal/session/cards.go 的 projectEvent 一致 */
  function project(ev: TaskEvent): SessionMessage | undefined {
    const base = { id: `t${ev.seq}`, seq: ev.seq, ts: ev.ts };
    const card = (c: SessionCard): SessionMessage => ({ ...base, kind: "card", card: { ...c, taskId: ev.taskId, atSeq: ev.seq } });
    const notice = (text: string): SessionMessage => ({ ...base, kind: "notice", text });
    switch (ev.type) {
      case "task_created":
        return card({ type: "form", employee: ev.employee, draft: ev.form, attachments: ev.attachments.map((a) => ({ name: a.name, size: 0 })), submitted: true });
      case "qc_result":
        return card({ type: "qc", passed: ev.passed, checks: ev.checks });
      case "review_decision":
        return notice(ev.decision === "return" ? `审核人退回：${ev.comment ?? ""}` : "审核人已通过。");
      case "state_changed":
        if (ev.to === "awaiting_initiator" || ev.to === "awaiting_operator") return card({ type: "pause", pause: ev.pause });
        if (ev.to === "awaiting_review") return card({ type: "review", pause: ev.pause });
        if (ev.to === "delivered") return notice("任务已交付。");
        if (ev.to === "terminated") return notice("任务已终止。");
        return undefined;
      default:
        return undefined;
    }
  }

  async function withTask(messages: SessionMessage[], taskId: string, s?: MockSession): Promise<SessionMessage[]> {
    const events = await tasks.getEvents(taskId);
    const snapshot = await tasks.getTask(taskId);
    const title = (await summaryOf(taskId))?.title;
    // 交付之后紧跟一张入库确认卡片（和 internal/session/cards.go 的 filingMessage 一致）
    const filing = (ev: TaskEvent): SessionMessage[] =>
      knowledge && ev.type === "state_changed" && ev.to === "delivered"
        ? [
            {
              id: `t${ev.seq}-filing`,
              seq: ev.seq,
              ts: ev.ts,
              kind: "card",
              card: { type: "knowledge_filing", taskId, atSeq: ev.seq, title, description: s?.filingDraft, filed: knowledge.filedFrom(taskId) },
            },
          ]
        : [];
    const merged = [...messages.map((m) => ({ ...m })), ...events.flatMap((e) => {
      const projected = project(e);
      return projected ? [projected, ...filing(e)] : filing(e);
    })];
    merged.sort((a, b) => Date.parse(a.ts) - Date.parse(b.ts));
    attachActions(merged, snapshot, events);
    return merged;
  }

  function attachActions(messages: SessionMessage[], snapshot: TaskSnapshot | undefined, events: TaskEvent[]) {
    if (!snapshot || !PAUSED.has(snapshot.state)) return;
    const opened = events.filter((e) => e.type === "state_changed").at(-1)?.seq ?? 0;
    for (const m of messages) {
      const c = m.card;
      if (c && c.taskId === snapshot.id && (c.atSeq ?? 0) >= opened && (c.type === "pause" || c.type === "review")) {
        m.card = { ...c, actions: snapshot.actions, pause: snapshot.pause };
      }
    }
  }

  const reply = async (s: MockSession, out: SessionMessage[]): Promise<SessionResult> => ({ ok: true, messages: out, session: await view(s) });

  // ---- 正在说的一轮：和 Go 后端一样逐字出现，说完再写进消息、推 turn_end ----
  const listeners = new Map<string, Set<(event: SessionAgentEvent) => void>>();
  const emit = (s: MockSession, turn: number, event: Omit<SessionAgentEvent, "turn">) => {
    for (const listener of listeners.get(s.id) ?? []) listener({ turn, ...event } as SessionAgentEvent);
  };
  const later = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
  const pace = (ms = 8): number => (import.meta.env?.MODE === "test" ? 0 : ms);

  /** 说一轮：文字逐字出现、工具一个个调用；被停止时已说的部分以“已中断”保存，没开始的工具记“已取消” */
  async function runTurn(s: MockSession, turn: number, steps: { text: string; tools?: string[] }[]) {
    const said: string[] = [];
    for (const step of steps) {
      let delta = "";
      for (const piece of chunksOf(step.text)) {
        if (s.cancelled) break;
        emit(s, turn, { type: "text_delta", text: piece });
        delta += piece;
        await later(pace());
      }
      if (delta) said.push(delta);
      if (s.cancelled) break;
      for (const [index, name] of (step.tools ?? []).entries()) {
        if (s.cancelled) {
          // 没开始的工具记“已取消”
          write(s, { kind: "tool_called", tool: name, reason: "已取消：这一轮被停止，工具没有执行。" }, []);
          emit(s, turn, { type: "message" });
          continue;
        }
        const id = `mock_call_${turn}_${name}_${index}`;
        emit(s, turn, { type: "tool_start", id, tool: name, args: {} });
        await later(pace(30));
        emit(s, turn, { type: "tool_end", id, tool: name, result: { ok: true } });
        write(s, { kind: "tool_called", tool: name, result: { ok: true } }, []);
      }
    }
    const text = said.join("");
    if (text) write(s, { kind: "assistant", text: s.cancelled ? `${text}（已中断）` : text }, []);
    s.running = undefined;
  }

  /**
   * 后台开始说一轮：接口立即返回，内容在事件流里出现。
   * extra 是在这一轮里顺便落到会话里的东西（比如员工确认卡片），写完立刻推送。
   */
  function startTurn(s: MockSession, steps: { text: string; tools?: string[] }[], extra?: () => void): number {
    const turn = ++s.turn;
    s.cancelled = false;
    s.running = { turn, timer: setTimeout(() => {}, 0) };
    emit(s, turn, { type: "turn_start" });
    void runTurn(s, turn, steps).then(() => {
      if (extra && !s.cancelled) {
        extra();
        emit(s, turn, { type: "message" });
      }
      emit(s, turn, { type: "turn_end", reason: s.cancelled ? "stopped" : "completed" });
    });
    return turn;
  }

  function route(s: MockSession, text: string, out: SessionMessage[]) {
    const guess = employees.find((e) => [...e.name.replace(/员工$/, "")].filter((ch) => text.includes(ch)).length >= 2);
    if (guess) {
      write(s, { kind: "assistant", text: `这项工作适合由${guess.name}接手，请确认。` }, out);
      write(s, { kind: "card", card: { type: "employee_confirm", proposed: guess.id, source: "model", reason: guess.description, candidates: roster() } }, out);
    } else {
      write(s, { kind: "assistant", text: "我判断不出该由谁接手，请从下面选一位员工。" }, out);
      write(s, { kind: "card", card: { type: "employee_confirm", source: "manual", candidates: roster() } }, out);
    }
  }

  /** 从人说过的话里取“第 N 周”填进草稿 */
  function draftFromWords(s: MockSession) {
    const said = s.messages.filter((m) => m.kind === "user").map((m) => m.text ?? "").join(" ");
    const week = /第\s*(\d{1,2})\s*周/.exec(said)?.[1];
    if (week) s.draft = { ...s.draft, period: { year: 2026, week: Number(week) } };
  }

  function writable(id: string): MockSession | SessionResult {
    if (id.startsWith(LEGACY)) return fail("session_read_only", "这是由旧任务推导出的只读会话，要继续工作请开新会话");
    return sessions.get(id) ?? fail("not_found", `会话不存在：${id}`);
  }
  const isFailure = (v: MockSession | SessionResult): v is SessionResult => "ok" in v;

  async function legacyDetail(task: TaskSummary): Promise<SessionDetail> {
    return {
      id: `${LEGACY}${task.id}`,
      workspaceId: task.workspaceId ?? "",
      status: task.state === "delivered" || task.state === "terminated" ? "closed" : "task",
      readOnly: true,
      title: task.title,
      employee: task.employee,
      taskId: task.id,
      attachments: [],
      createdAt: task.updatedAt,
      updatedAt: task.updatedAt,
      taskState: task.state,
      statusText: task.statusText,
      waitingFor: task.waitingFor,
      messages: [],
    };
  }

  return {
    async listSessions(workspaceId) {
      const own = await Promise.all([...sessions.values()].filter((s) => !workspaceId || s.workspaceId === workspaceId).map(view));
      const bound = new Set(own.map((s) => s.taskId));
      // 由旧任务推导出的只读会话：发起事件里没有工作区的旧任务带不出归属，按不属于任何工作区处理
      const legacy = workspaceId
        ? []
        : await Promise.all((await tasks.listTasks()).filter((t) => !bound.has(t.id)).map(legacyDetail));
      const all: SessionSummary[] = [...own, ...legacy.map(({ messages: _m, ...rest }) => rest)];
      return all.sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt));
    },

    async getSession(id) {
      if (id.startsWith(LEGACY)) {
        const task = (await tasks.listTasks()).find((t) => `${LEGACY}${t.id}` === id);
        if (!task) throw new Error(`会话不存在：${id}`);
        const detail = await legacyDetail(task);
        return { ...detail, messages: await withTask([], task.id) };
      }
      const s = sessions.get(id);
      if (!s) throw new Error(`会话不存在：${id}`);
      const messages = s.messages.filter((m) => m.kind !== "tool_called");
      return { ...(await view(s)), checklist: checklistOf(s), messages: s.taskId ? await withTask(messages, s.taskId, s) : messages };
    },

    async createSession(workspaceId) {
      const now = new Date(Date.now() + ++counter).toISOString();
      const s: MockSession = {
        id: `s-mock-${counter}`,
        workspaceId: workspaceId ?? MOCK_WORKSPACE,
        skipped: new Set(),
        files: [],
        messages: [],
        turn: 0,
        createdAt: now,
        updatedAt: now,
      };
      sessions.set(s.id, s);
      return view(s);
    },

    async sendMessage(id, text): Promise<TurnAccepted | { ok: false; error: string; message: string }> {
      if (id.startsWith(LEGACY)) return fail("session_read_only", "这是由旧任务推导出的只读会话，要继续工作请开新会话");
      const s = sessions.get(id);
      if (!s) return fail("not_found", `会话不存在：${id}`);
      if (!text.trim()) return fail("invalid_input", "消息不能为空");
      write(s, { kind: "user", role: getRole(), text: text.trim() }, []);
      // 正在说：这句话排进这一轮，在下一步之前送达（模拟模式直接接在这轮的话后面说）
      if (s.running) return { ok: true, turn: s.running.turn };
      if (!s.employee) {
        const guess = employees.find((e) => [...e.name.replace(/员工$/, "")].filter((ch) => text.includes(ch)).length >= 2);
        const steps = guess
          ? [
              { text: `这项工作适合由${guess.name}接手，请确认。`, tools: ["propose_employee"] },
            ]
          : [{ text: "我判断不出该由谁接手，请从下面选一位员工。" }];
        // 卡片在轮次说完之后落到会话里
        const turn = startTurn(s, steps, () => {
          if (s.employee) return;
          write(s, { kind: "card", card: guess ? { type: "employee_confirm", proposed: guess.id, source: "model", reason: guess.description, candidates: roster() } : { type: "employee_confirm", source: "manual", candidates: roster() } }, []);
        });
        return { ok: true, turn };
      }
      if (s.taskId) {
        const turn = startTurn(s, [{ text: "模拟模式不接模型。任务进展见卡片和右栏，操作请点卡片上的按钮。" }]);
        return { ok: true, turn };
      }
      draftFromWords(s);
      const steps = [{ text: "已按你说的更新表单，请检查后提交。" }];
      const turn = startTurn(s, steps, () => write(s, { kind: "card", card: formCard(s) }, []));
      return { ok: true, turn };
    },

    async stopTurn(id) {
      if (id.startsWith(LEGACY)) return fail("session_read_only", "这是由旧任务推导出的只读会话");
      const s = sessions.get(id);
      if (!s) return fail("not_found", `会话不存在：${id}`);
      const running = s.running;
      if (!running) return { ok: true, stopped: false };
      s.cancelled = true;
      s.running = undefined;
      return { ok: true, stopped: true, turn: running.turn };
    },

    subscribeSession(id, onEvent) {
      const set = listeners.get(id) ?? new Set();
      set.add(onEvent);
      listeners.set(id, set);
      return () => {
        set.delete(onEvent);
      };
    },

    async confirmEmployee(id, employee) {
      if (getRole() !== "initiator") return fail("forbidden_role", "只有发起人可以确认员工");
      const s = writable(id);
      if (isFailure(s)) return s;
      if (s.employee) return fail("illegal_transition", "这个会话已由员工接手");
      const picked = employees.find((e) => e.id === employee);
      if (!picked) return fail("invalid_input", `没有这个员工：${employee}`);
      const out: SessionMessage[] = [];
      s.employee = employee;
      write(s, { kind: "notice", text: `${picked.name}已接手。` }, out);
      draftFromWords(s);
      write(s, { kind: "card", card: formCard(s) }, out);
      return reply(s, out);
    },

    async uploadAttachments(id, files) {
      if (getRole() !== "initiator") return fail("forbidden_role", "只有发起人可以上传材料");
      const s = writable(id);
      if (isFailure(s)) return s;
      if (s.taskId) return fail("session_has_task", "任务已经开始，请在暂停卡片上补充材料");
      if (files.length === 0) return fail("invalid_input", "没有收到文件");
      s.files = [...s.files.filter((f) => !files.some((n) => n.name === f.name)), ...files];
      const out: SessionMessage[] = [];
      write(s, { kind: "notice", text: `已收到材料：${files.map((f) => f.name).join("、")}` }, out);
      return reply(s, out);
    },

    async submitSession(id, form) {
      if (getRole() !== "initiator") return fail("forbidden_role", "只有发起人可以提交");
      const s = writable(id);
      if (isFailure(s)) return s;
      if (s.taskId) return fail("session_has_task", "这个会话已有任务，要做新任务请开新会话");
      if (!s.employee) return fail("employee_required", "请先确认由哪个员工接手");
      const checklist = checklistOf(s, form ?? s.draft);
      if (checklist && !checklist.ready) return fail("checklist_incomplete", `开工清单还缺：${checklist.missing.join("、")}。备齐后才能开工。`);
      if (form) s.draft = form;
      // 跳过的项开工时写进表单字段
      const skipped = (CHECKLISTS[s.employee] ?? []).filter((i) => i.skipField && checklist?.items.some((e) => e.id === i.id && e.status === "skipped"));
      const startForm = { ...s.draft, ...Object.fromEntries(skipped.map((i) => [i.skipField!, true])) };
      // 模拟任务按上传项判定材料：会话里的文件没有上传项，按扩展名归位
      const uploads: UploadItem[] = s.files.map((file) => ({ slot: /\.docx$/i.test(file.name) ? "minutesFiles" : "salesFile", file }));
      const snapshot = await tasks.createTask({ employee: s.employee, form: startForm, uploads });
      s.taskId = snapshot.id;
      const res = await tasks.command(snapshot.id, { type: "submit" });
      if (!res.ok) return fail(res.error, res.message);
      return reply(s, []);
    },

    async draftFiling(id) {
      if (getRole() !== "initiator") return fail("forbidden_role", "只有发起人可以把交付物放进知识库");
      const s = writable(id);
      if (isFailure(s)) return s;
      const task = s.taskId ? await tasks.getTask(s.taskId) : undefined;
      if (!task || task.state !== "delivered") return fail("illegal_transition", "任务交付之后才能放进知识库");
      const title = (await summaryOf(task.id))?.title ?? task.id;
      s.filingDraft = `${title}（模拟模式按标题起草的描述，请按实际内容修改）`;
      return reply(s, []);
    },

    async skipChecklistItem(id, itemId) {
      if (getRole() !== "initiator") return fail("forbidden_role", "只有发起人可以确认跳过清单项");
      const s = writable(id);
      if (isFailure(s)) return s;
      if (s.taskId) return fail("session_has_task", "任务已经开始，清单不能再改");
      const item = (s.employee ? CHECKLISTS[s.employee] : undefined)?.find((i) => i.id === itemId);
      if (!item) return fail("invalid_input", `开工清单里没有这一项：${itemId}`);
      if (item.required) return fail("invalid_input", `“${item.label}”是必需项，不能跳过`);
      s.skipped.add(item.id);
      const out: SessionMessage[] = [];
      write(s, { kind: "notice", text: `发起人确认跳过开工清单的“${item.label}”${item.skipLabel ? `（${item.skipLabel}）` : ""}。` }, out);
      return reply(s, out);
    },

    preflight: async () => ({ ok: true }),
  };
}
