// 模拟数据模式的会话：internal/session 的手写简化版，建在模拟任务之上。
// 路由不调模型：按员工名称和描述里的字粗略匹配，匹配不到就列出员工手选。
// 任务卡片和 Go 一样读取时从任务事件投影，按钮取自快照。状态点照 internal/session/status.go 的规则后算。

import type { TaskEvent } from "@de/core/contract";
import { CHECKLISTS } from "./mock-data";
import { evaluateChecklist } from "./mock-rules";
import type { SessionApi } from "./sessions";
import type {
  EmployeeInfo,
  SessionAgentEvent,
  ModelConnectionOption,
  ModelRef,
  PermissionChoice,
  PermissionDecision,
  PermissionMode,
  RecordGroup,
  RecordItem,
  SessionAttachment,
  SessionCard,
  SessionDetail,
  SessionMessage,
  SessionResult,
  SessionSummary,
  StatusDot,
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

type TaskBackend = Pick<WebApi, "listTasks" | "getTask" | "getEvents" | "createTask" | "command"> & {
  /** 运行中换模型：任务写一条“模型已切换”事件（对应 Go 的 Engine.SetTaskModel） */
  switchModel?(taskId: string, model: ModelRef): void;
};

/** 模拟模式的会话：会话接口，外带侧栏要用的全部会话和记录。 */
export interface MockSessions extends SessionApi {
  /** 全部会话（含归档的），带状态点 */
  all(): Promise<SessionSummary[]>;
  /** 记录 tab：所有已开工的任务 */
  records(role: UiRole): Promise<RecordItem[]>;
}

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
  /** 人改过的标题；没改过时取第一句话 */
  title?: string;
  archived: boolean;
  /** 已读到哪一条任务事件：交付或终止之后的新内容才算没读 */
  readUpTo: number;
  /** 发起人确认跳过的清单项 */
  skipped: Set<string>;
  /** 会话里选择的模型（Spec 0006）；没选过时沿用设置的 lastModel */
  selected?: ModelRef;
  /** 权限模式（T48）；新会话总是询问 */
  permissionMode: PermissionMode;
  /** 本会话总是允许的动作类别；换模式时清空 */
  always: Set<string>;
  /** 权限确认的答复（requestId → allow_once / always_allow / deny / cancelled） */
  decisions: Map<string, string>;
  /** 正在等人答复的权限确认 */
  pending?: { requestId: string; resolve: (decision: string) => void };
  files: File[];
  messages: SessionMessage[];
  createdAt: string;
  updatedAt: string;
}

interface MockFolderFile {
  path: string;
  name: string;
  size: number;
}

const LEGACY = "task-";
/** 模拟模式的工作区（和 workspaces.ts 的两个一致） */
const MOCK_WORKSPACE = "ws-demo";
const WORKSPACE_NAMES: Record<string, string> = { "ws-demo": "我的工作区", "ws-huanan": "华南区" };
const PAUSED = new Set(["awaiting_initiator", "awaiting_operator", "awaiting_review"]);
const RUNNING = new Set(["admission", "running", "qc"]);
const FINISHED = new Set(["delivered", "terminated"]);

/** 任务状态 → 记录分组（和 internal/session/status.go 的 recordGroupOf 一致） */
function recordGroupOf(state: string): RecordGroup {
  switch (state) {
    case "awaiting_initiator":
      return "supplement";
    case "awaiting_review":
      return "review";
    case "delivered":
      return "delivered";
    case "terminated":
      return "terminated";
    default:
      return "running";
  }
}

/**
 * 状态点（和 internal/session/status.go 的 StatusDot 一样的优先级）：
 * 等你处理 → 运行中 → 已完成未读 → 空闲。待人工处理不算等你处理。
 */
function dotOf(role: UiRole, status: string, state: string | undefined, unread: boolean): StatusDot {
  if (status === "reception" || status === "drafting") return role === "initiator" ? "waiting" : "idle";
  if (state === "awaiting_initiator" && role === "initiator") return "waiting";
  if (state === "awaiting_review" && role === "reviewer") return "waiting";
  if (state !== undefined && RUNNING.has(state)) return "running";
  if (state !== undefined && FINISHED.has(state) && unread) return "done";
  return "idle";
}

/** 模拟模式可选的模型（和 settings.ts 的模拟连接一致：只有 DeepSeek 配了 Key）。 */
const MOCK_MODEL_OPTIONS: ModelConnectionOption[] = [{ name: "DeepSeek", models: ["deepseek-chat", "deepseek-reasoner"] }];
/** 设置的 lastModel：人上一次在会话里选的模型，新会话沿用它 */
const MOCK_DEFAULT_MODEL: ModelRef = { connection: "DeepSeek", model: "deepseek-chat" };

const PERMISSION_MODE_VALUES: readonly string[] = ["plan", "ask", "accept_edits", "auto", "yolo"];
const PERMISSION_BUTTONS: PermissionChoice[] = [
  { decision: "allow_once", label: "允许一次", style: "primary" },
  { decision: "always_allow", label: "本会话总是允许这一类", style: "default" },
  { decision: "deny", label: "拒绝", style: "danger" },
];
/** 模拟模式不接模型：人说“把……写成文件”之类的话，就演示一次 write_file 经过权限关口 */
const WRITE_REQUEST = /(写|存|保存|整理).{0,12}(文件|md|文档)/;
const WRITE_ARGS = { path: "notes/周会要点.md", content: "# 周会要点\n- 华南环比下降\n- 华东持平\n" };

export function createMockSessions(
  employees: EmployeeInfo[],
  getRole: () => UiRole,
  tasks: TaskBackend,
  knowledge?: { filedFrom(taskId: string): string | undefined },
): MockSessions {
  const sessions = new Map<string, MockSession>();
  /** 任务最后一条事件的序号 */
  const taskSeqs = new Map<string, number>();
  /** 打开过的旧任务会话（旧任务推导出的会话没有会话日志） */
  const legacyRead = new Set<string>();
  let counter = 0;
  // 和 Go 一样：选过的模型记在设置里，新会话沿用
  let lastModel: ModelRef = MOCK_DEFAULT_MODEL;
  /** 模拟任务当前的会话模型（运行中切换时更新；任务事件由 mock.ts 写入） */
  const taskModels = new Map<string, ModelRef>();

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
  const titleOf = (s: MockSession) => s.title ?? s.messages.find((m) => m.kind === "user")?.text?.slice(0, 30) ?? "新对话";
  const checklistOf = (s: MockSession, form = s.draft) => {
    const items = s.employee ? CHECKLISTS[s.employee] : undefined;
    return items ? evaluateChecklist(items, form ?? {}, s.files, s.skipped) : undefined;
  };
  const lastSeq = (taskId: string | undefined) => (taskId ? (taskSeqs.get(taskId) ?? 0) : 0);
  const unreadOf = (s: MockSession, summary: TaskSummary | undefined) =>
    summary !== undefined && FINISHED.has(summary.state) && lastSeq(s.taskId) > s.readUpTo;

  async function summaryOf(taskId: string | undefined): Promise<TaskSummary | undefined> {
    if (!taskId) return undefined;
    const events = await tasks.getEvents(taskId);
    const tail = events[events.length - 1];
    if (tail) taskSeqs.set(taskId, tail.seq);
    return (await tasks.listTasks()).find((t) => t.id === taskId);
  }

  async function view(s: MockSession): Promise<SessionSummary> {
    const title = titleOf(s);
    const summary = await summaryOf(s.taskId);
    const closed = summary !== undefined && FINISHED.has(summary.state);
    const status: SessionSummary["status"] = s.taskId ? (closed ? "closed" : "task") : s.employee ? "drafting" : "reception";
    const unread = unreadOf(s, summary);
    return {
      // 绑定了任务时是任务的当前模型；还没任务时是选过的模型，没选过沿用 lastModel
      ...(s.taskId ? { model: taskModels.get(s.taskId) } : { model: s.selected ?? lastModel, modelSelected: s.selected !== undefined }),
      permissionMode: s.permissionMode,
      id: s.id,
      workspaceId: s.workspaceId,
      status,
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
      statusDot: dotOf(getRole(), status, summary?.state, unread),
      unread,
      archived: s.archived,
      workspaceName: WORKSPACE_NAMES[s.workspaceId] ?? "",
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
    const tail = events[events.length - 1];
    if (tail) taskSeqs.set(taskId, tail.seq);
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

  const modeName = (mode: string) => ({ plan: "计划", ask: "询问", accept_edits: "接受编辑", auto: "自动", yolo: "放开" })[mode] ?? mode;

  /** 权限确认卡读取时配上状态：有没有答复、是不是还在等、能点的按钮（对应 Go 的 projectPermissions） */
  function projectPermissions(s: MockSession, messages: SessionMessage[]): SessionMessage[] {
    return messages.map((m) => {
      const card = m.card;
      if (!card || card.type !== "permission" || !card.requestId) return m;
      const waiting = s.pending?.requestId === card.requestId;
      const decision = s.decisions.get(card.requestId);
      return {
        ...m,
        card: waiting ? { ...card, pending: true, decision: undefined, choices: PERMISSION_BUTTONS } : { ...card, pending: false, decision: decision ?? "cancelled", choices: undefined },
      };
    });
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

  /** 一次 write_file 经过权限关口（对应 Go 的 turnLoop.gate）：计划拒绝、询问弹确认卡、其余放行，每一步都留痕 */
  function startWriteTurn(s: MockSession): number {
    const turn = ++s.turn;
    s.cancelled = false;
    s.running = { turn, timer: setTimeout(() => {}, 0) };
    emit(s, turn, { type: "turn_start" });
    const args = { ...WRITE_ARGS };
    const finish = (text: string, reason: "completed" | "stopped" = "completed") => {
      if (text) write(s, { kind: "assistant", text }, []);
      s.running = undefined;
      emit(s, turn, { type: "turn_end", reason });
    };
    void (async () => {
      emit(s, turn, { type: "tool_start", id: `mock_call_${turn}_write`, tool: "write_file", args });
      await later(pace(30));
      const end = (extra: Partial<SessionAgentEvent>) => emit(s, turn, { type: "tool_end", id: `mock_call_${turn}_write`, tool: "write_file", args, ...extra });
      const deny = (reason: string) => {
        write(s, { kind: "tool_denied", tool: "write_file", args, reason }, []);
        emit(s, turn, { type: "message" });
        end({ reason, isErr: true });
      };
      const run = () => {
        write(s, { kind: "tool_called", tool: "write_file", args, result: { path: args.path, action: "created", bytes: new TextEncoder().encode(args.content).length } }, []);
        emit(s, turn, { type: "message" });
        end({ result: { path: args.path, action: "created" } });
        finish(`已把要点写进 ${args.path}。`);
      };
      if (s.permissionMode === "plan") {
        deny(`计划模式下只能读，不能写入文件：${args.path}。请先把打算怎么做告诉发起人，等 TA 切换权限模式后再动手。`);
        return finish("计划模式下我只能看不能动。打算把要点整理成 notes/周会要点.md，请切换模式后再让我写。");
      }
      if (s.permissionMode === "ask" && !s.always.has("write_file")) {
        const requestId = Math.random().toString(16).slice(2, 10).padEnd(8, "0") + Date.now().toString(16).slice(-8).padStart(8, "0");
        const cardOut: SessionMessage[] = [];
        write(
          s,
          { kind: "card", card: { type: "permission", requestId, action: "write_file", actionLabel: "写入文件", tool: "write_file", object: args.path, path: args.path, detail: `新建文件，写入 ${args.content.length} 个字`, pending: true, choices: PERMISSION_BUTTONS } },
          cardOut,
        );
        emit(s, turn, { type: "message", message: cardOut[0] });
        const decision = await new Promise<string>((resolve) => {
          s.pending = { requestId, resolve };
        });
        s.pending = undefined;
        s.decisions.set(requestId, decision);
        if (decision === "cancelled" || s.cancelled) {
          s.decisions.set(requestId, "cancelled");
          write(s, { kind: "tool_called", tool: "write_file", args, result: "已取消：这一轮被停止，工具没有执行。" }, []);
          end({ reason: "已取消", isErr: true });
          return finish("", "stopped");
        }
        if (decision === "deny") {
          deny(`用户拒绝：发起人没有允许写入文件：${args.path}。请换个办法，或问问发起人想怎么做。`);
          return finish("好的，那我不写了。");
        }
        if (decision === "always_allow") s.always.add("write_file");
        return run();
      }
      write(
        s,
        { kind: "permission_allowed", tool: "write_file", args, reason: s.permissionMode === "ask" ? "本会话已选择总是允许「写入文件」" : `${modeName(s.permissionMode)}模式：写入文件不用确认` },
        [],
      );
      emit(s, turn, { type: "message" });
      run();
    })();
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
    const finished = FINISHED.has(task.state);
    const unread = finished && !legacyRead.has(task.id);
    const status: SessionSummary["status"] = finished ? "closed" : "task";
    const workspaceId = task.workspaceId ?? MOCK_WORKSPACE;
    return {
      id: `${LEGACY}${task.id}`,
      workspaceId,
      status,
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
      statusDot: dotOf(getRole(), status, task.state, unread),
      unread,
      archived: false,
      permissionMode: "ask",
      workspaceName: WORKSPACE_NAMES[workspaceId] ?? "",
      messages: [],
    };
  }

  return {
    async modelOptions() {
      return MOCK_MODEL_OPTIONS;
    },

    /** 选模型：规则照搬 Go 的 SelectModel（只能选连接列出的模型；任务运行中从下一个判断步骤生效） */
    async selectModel(id, connection, model) {
      if (getRole() !== "initiator") return fail("forbidden_role", "只有发起人可以选模型");
      const s = writable(id);
      if (isFailure(s)) return s;
      const option = MOCK_MODEL_OPTIONS.find((o) => o.name === connection);
      if (!option) return fail("invalid_input", `没有这个连接：${connection}`);
      if (!option.models.includes(model)) return fail("invalid_input", `模型「${model}」不在连接「${connection}」的模型列表里`);
      if (s.taskId) {
        const task = await tasks.getTask(s.taskId);
        if (task?.state === "delivered" || task?.state === "terminated") return fail("session_read_only", "任务已结束，模型不能再改；下一个任务请开新对话");
      }
      const next: ModelRef = { connection, model };
      if (s.selected && s.selected.connection === connection && s.selected.model === model) return reply(s, []);
      const out: SessionMessage[] = [];
      s.selected = next;
      lastModel = next;
      if (s.taskId) {
        taskModels.set(s.taskId, next);
        tasks.switchModel?.(s.taskId, next);
      }
      return reply(s, out);
    },

    /** 选权限模式：规则照搬 Go 的 SetPermissionMode（只有发起人；换模式清空“本会话总是允许”） */
    async setPermissionMode(id, mode) {
      if (getRole() !== "initiator") return fail("forbidden_role", "只有发起人可以选权限模式");
      const s = writable(id);
      if (isFailure(s)) return s;
      if (!PERMISSION_MODE_VALUES.includes(mode)) return fail("invalid_input", `没有这个权限模式：${mode}`);
      s.permissionMode = mode;
      s.always.clear();
      const out: SessionMessage[] = [];
      write(s, { kind: "permission_mode_set", mode }, out);
      return reply(s, out);
    },

    /** 答复权限确认：只有发起人；只能答一次，已答复或已取消的确认不能再答 */
    async decidePermission(id, requestId, decision: PermissionDecision) {
      if (getRole() !== "initiator") return fail("forbidden_role", "只有发起人可以答复权限确认");
      const s = writable(id);
      if (isFailure(s)) return s;
      if (!["allow_once", "always_allow", "deny"].includes(decision)) return fail("invalid_input", `答复必须是 allow_once、always_allow 或 deny，收到 ${decision}`);
      if (!s.messages.some((m) => m.card?.type === "permission" && m.card.requestId === requestId)) return fail("not_found", `没有这张权限确认：${requestId}`);
      const waiting = s.pending;
      if (!waiting || waiting.requestId !== requestId) return fail("permission_settled", "这张权限确认已经答复或取消了");
      s.pending = undefined;
      waiting.resolve(decision);
      return reply(s, []);
    },

    async listSessions(workspaceId, options) {
      const own = await Promise.all(
        [...sessions.values()].filter((s) => (!workspaceId || s.workspaceId === workspaceId) && (options?.archived || !s.archived)).map(view),
      );
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
        // 打开旧任务的会话就算读过（它们没有会话日志，记在内存里）
        legacyRead.add(task.id);
        const detail = await legacyDetail(task);
        return { ...detail, statusDot: "idle", unread: false, messages: await withTask([], task.id) };
      }
      const s = sessions.get(id);
      if (!s) throw new Error(`会话不存在：${id}`);
      // 打开会话就算已读（Go 后端写在会话日志里，模拟模式记在内存里）
      s.readUpTo = lastSeq(s.taskId);
      // 写文件的工具行要给界面（Go 后端把全部工具留痕都返回，界面只取写文件的）；别的工具留痕模拟模式不给
      const messages = projectPermissions(s, s.messages.filter((m) => m.kind !== "tool_called" || m.tool === "write_file"));
      return { ...(await view(s)), checklist: checklistOf(s), messages: s.taskId ? await withTask(messages, s.taskId, s) : messages };
    },

    async updateSession(id, patch) {
      const s = writable(id);
      if (isFailure(s)) throw new Error(!s.ok ? s.message : "操作被拒绝");
      if ("archived" in patch) {
        s.archived = patch.archived === true;
        return view(s);
      }
      if (typeof patch.title !== "string" || !patch.title.trim()) throw new Error("标题不能为空");
      s.title = patch.title.trim();
      return view(s);
    },

    async createSession(workspaceId) {
      const now = new Date(Date.now() + ++counter).toISOString();
      const s: MockSession = {
        id: `s-mock-${counter}`,
        workspaceId: workspaceId ?? MOCK_WORKSPACE,
        archived: false,
        readUpTo: 0,
        skipped: new Set(),
        permissionMode: "ask",
        always: new Set(),
        decisions: new Map(),
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
      if (WRITE_REQUEST.test(text)) return { ok: true, turn: startWriteTurn(s) };
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
      // 等权限确认时被停止：未决的确认记为已取消
      s.pending?.resolve("cancelled");
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

    // 模拟模式的工作区目录：固定的几份样例文件（对应工作区目录里人放进去的样例）
    async listFolderFiles() {
      const files: MockFolderFile[] = [
        { path: "销售明细.xlsx", name: "销售明细.xlsx", size: 28_000 },
        { path: "周会纪要_2026W44.docx", name: "周会纪要_2026W44.docx", size: 18_000 },
      ];
      return { entries: files.map((f) => ({ path: f.path, name: f.name, size: f.size })), truncated: false };
    },

    // @ 选择即视为人已确认：和卡片上的“用这个文件”一样，复制进会话材料
    async useFolderFiles(id, paths) {
      if (getRole() !== "initiator") return fail("forbidden_role", "只有发起人可以确认目录里的文件");
      const s = writable(id);
      if (isFailure(s)) return s;
      if (s.taskId) return fail("session_has_task", "任务已经开始，请在暂停卡片上补充材料");
      if (paths.length === 0) return fail("invalid_input", "没有选择文件");
      const files: File[] = [];
      for (const p of paths) {
        const name = p.split("/").at(-1) ?? p;
        files.push(new File([`模拟的 ${name}`], name));
      }
      s.files = [...s.files.filter((f) => !files.some((n) => n.name === f.name)), ...files];
      const out: SessionMessage[] = [];
      write(s, { kind: "notice", text: `已把目录里的文件作为材料：${paths.join("、")}` }, out);
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
      const snapshot = await tasks.createTask({ employee: s.employee, form: startForm, uploads, model: s.selected ?? lastModel });
      s.taskId = snapshot.id;
      taskModels.set(snapshot.id, s.selected ?? lastModel);
      const res = await tasks.command(snapshot.id, { type: "submit" });
      if (!res.ok) return fail("illegal_transition", "提交被拒绝");
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

    // ---- 侧栏“工作区”和“记录”两个 tab ----

    all: allSummaries,

    async records(role) {
      const summaries = await allSummaries();
      const items: RecordItem[] = [];
      for (const summary of summaries) {
        if (!summary.taskId || !summary.taskState) continue;
        items.push({
          taskId: summary.taskId,
          group: recordGroupOf(summary.taskState),
          title: summary.title,
          employee: summary.employee ?? "",
          state: summary.taskState,
          statusText: summary.statusText ?? "",
          // 记录里的状态点按读的人的角色算，和侧栏列表走同一套规则
          statusDot: dotOf(role, summary.status, summary.taskState, false),
          sessionId: summary.id,
          workspaceId: summary.workspaceId,
          workspaceName: summary.workspaceName ?? "",
          updatedAt: summary.updatedAt,
        });
      }
      return items;
    },
  };

  /** 全部会话（含归档的），带状态点：工作区列表和记录 tab 都从这里取 */
  async function allSummaries(): Promise<SessionSummary[]> {
    const own = await Promise.all([...sessions.values()].map(view));
    const bound = new Set(own.map((s) => s.taskId));
    // 种子任务的旧任务会话也列出来：和 Go 一样，没绑会话的任务以只读会话出现
    const legacy = await Promise.all((await tasks.listTasks()).filter((t) => !bound.has(t.id)).map(legacyDetail));
    const summaries = [...own, ...legacy.map(({ messages: _m, ...rest }) => rest)];
    return summaries.sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt));
  }
}
