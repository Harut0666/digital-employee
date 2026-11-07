// 模拟数据模式的会话：internal/session 的手写简化版，建在模拟任务之上。
// 路由不调模型：按员工名称和描述里的字粗略匹配，匹配不到就列出员工手选。
// 任务卡片和 Go 一样读取时从任务事件投影，按钮取自快照。

import type { TaskEvent } from "@de/core/contract";
import type { SessionApi } from "./sessions";
import type {
  EmployeeInfo,
  SessionAttachment,
  SessionCard,
  SessionDetail,
  SessionMessage,
  SessionResult,
  SessionSummary,
  TaskSnapshot,
  TaskSummary,
  UiRole,
  UploadItem,
  WebApi,
} from "./types";

type TaskBackend = Pick<WebApi, "listTasks" | "getTask" | "getEvents" | "createTask" | "command">;

interface MockSession {
  id: string;
  employee?: string;
  taskId?: string;
  draft?: Record<string, unknown>;
  files: File[];
  messages: SessionMessage[];
  createdAt: string;
  updatedAt: string;
}

const LEGACY = "task-";
const PAUSED = new Set(["awaiting_initiator", "awaiting_operator", "awaiting_review"]);

export function createMockSessions(employees: EmployeeInfo[], getRole: () => UiRole, tasks: TaskBackend): SessionApi {
  const sessions = new Map<string, MockSession>();
  let counter = 0;

  const fail = (error: string, message: string): SessionResult => ({ ok: false, error, message });
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

  async function summaryOf(taskId: string | undefined): Promise<TaskSummary | undefined> {
    return taskId ? (await tasks.listTasks()).find((t) => t.id === taskId) : undefined;
  }

  async function view(s: MockSession): Promise<SessionSummary> {
    const title = s.messages.find((m) => m.kind === "user")?.text?.slice(0, 30) ?? "新对话";
    const summary = await summaryOf(s.taskId);
    const closed = summary?.state === "delivered" || summary?.state === "terminated";
    return {
      id: s.id,
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

  async function withTask(messages: SessionMessage[], taskId: string): Promise<SessionMessage[]> {
    const events = await tasks.getEvents(taskId);
    const merged = [...messages.map((m) => ({ ...m })), ...events.flatMap((e) => project(e) ?? [])];
    merged.sort((a, b) => Date.parse(a.ts) - Date.parse(b.ts));
    const snapshot = await tasks.getTask(taskId);
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
    async listSessions() {
      const own = await Promise.all([...sessions.values()].map(view));
      const bound = new Set(own.map((s) => s.taskId));
      const legacy = await Promise.all((await tasks.listTasks()).filter((t) => !bound.has(t.id)).map(legacyDetail));
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
      return { ...(await view(s)), messages: s.taskId ? await withTask(messages, s.taskId) : messages };
    },

    async createSession() {
      const now = new Date(Date.now() + ++counter).toISOString();
      const s: MockSession = { id: `s-mock-${counter}`, files: [], messages: [], createdAt: now, updatedAt: now };
      sessions.set(s.id, s);
      return view(s);
    },

    async sendMessage(id, text) {
      const s = writable(id);
      if (isFailure(s)) return s;
      if (!text.trim()) return fail("invalid_input", "消息不能为空");
      const out: SessionMessage[] = [];
      write(s, { kind: "user", role: getRole(), text: text.trim() }, out);
      if (!s.employee) route(s, text, out);
      else if (s.taskId) write(s, { kind: "notice", text: "模拟模式不接模型。任务进展见卡片和右栏，操作请点卡片上的按钮。" }, out);
      else {
        draftFromWords(s);
        write(s, { kind: "assistant", text: "已按你说的更新表单，请检查后提交。" }, out);
        write(s, { kind: "card", card: formCard(s) }, out);
      }
      return reply(s, out);
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
      if (form) s.draft = form;
      // 模拟任务按上传项判定材料：会话里的文件没有上传项，按扩展名归位
      const uploads: UploadItem[] = s.files.map((file) => ({ slot: /\.docx$/i.test(file.name) ? "minutesFiles" : "salesFile", file }));
      const snapshot = await tasks.createTask({ employee: s.employee, form: s.draft ?? {}, uploads });
      s.taskId = snapshot.id;
      const res = await tasks.command(snapshot.id, { type: "submit" });
      if (!res.ok) return fail(res.error, res.message);
      return reply(s, []);
    },

    preflight: async () => ({ ok: true }),
  };
}
