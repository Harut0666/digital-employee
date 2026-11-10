// 模拟数据模式的会话：internal/session 的手写简化版，建在模拟任务之上。
// 路由不调模型：按员工名称和描述里的字粗略匹配，匹配不到就列出员工手选。
// 任务卡片和 Go 一样读取时从任务事件投影，按钮取自快照。状态点照 internal/session/status.go 的规则后算。

import { STATE_LABELS, type TaskEvent } from "@de/core/contract";
import { CHECKLISTS } from "./mock-data";
import { evaluateChecklist } from "./mock-rules";
import { normalizePermissionMode, permissionModeLabel } from "../view/permission-mode";
import type { SessionApi } from "./sessions";
import type {
  EmployeeInfo,
  SessionAgentEvent,
  ModelConnectionOption,
  ModelRef,
  PermissionChoice,
  PermissionDecision,
  FormMaterial,
  PermissionMode,
  QuestionAnswer,
  QuestionItem,
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
  TodoItem,
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
  /** 当前任务：会话里最新的一个；taskIds 是按开工顺序的全部任务 */
  taskId?: string;
  taskIds: string[];
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
  /** 权限模式（ADR-0007 增补二）；新会话总是仅限工作区（workspace） */
  permissionMode: PermissionMode;
  /** 本会话总是允许的动作类别；换模式时清空 */
  always: Set<string>;
  /** 权限确认的答复（requestId → allow_once / always_allow / deny / cancelled） */
  decisions: Map<string, string>;
  /** 正在等人答复的权限确认 */
  pending?: { requestId: string; resolve: (decision: string) => void };
  /** 提问卡（ask_user_question）：还在等人答复的（resolve）、已答复的答案或已取消 */
  questions: Map<string, { resolve?: (answers: QuestionAnswer[] | "cancelled") => void }>;
  questionResults: Map<string, QuestionAnswer[] | "cancelled">;
  /** Agent 最新的待办清单（todo_write） */
  todos: TodoItem[];
  /** 员工表单卡的答复（requestId → confirmed / rejected / cancelled，confirmed 时带任务号） */
  formDecisions: Map<string, { decision: string; taskId?: string }>;
  /** 还在等人答复的员工表单：Agent 弹出的有一轮在等（resolve），载入公开样例弹出的没有 */
  forms: Map<string, { resolve?: (result: { rejected: boolean; taskId?: string }) => void }>;
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
function dotOf(role: UiRole, status: string, state: string | undefined, unread: boolean, waitingForm = false): StatusDot {
  // 员工表单在等发起人确认（T49）；没有任务的会话只是聊天，空闲
  if (waitingForm && role === "initiator") return "waiting";
  if (state === "awaiting_initiator" && role === "initiator") return "waiting";
  if (state === "awaiting_review" && role === "reviewer") return "waiting";
  if (state !== undefined && RUNNING.has(state)) return "running";
  if (state !== undefined && FINISHED.has(state) && unread) return "done";
  return "idle";
}

/** 模拟模式可选的模型（和 settings.ts 的模拟连接一致：只有示例厂商配了 Key）。 */
const MOCK_MODEL_OPTIONS: ModelConnectionOption[] = [{ name: "示例厂商", models: ["demo-chat", "demo-reasoner"] }];
/** 设置的 lastModel：人上一次在会话里选的模型，新会话沿用它 */
const MOCK_DEFAULT_MODEL: ModelRef = { connection: "示例厂商", model: "demo-chat" };

/** 选权限模式时收的取值：三个新值，加旧五档的值（后端同样收，映射成新值） */
const ACCEPTED_MODE_VALUES: readonly string[] = ["read_only", "workspace", "full", "plan", "ask", "accept_edits", "auto", "yolo"];
const PERMISSION_BUTTONS: PermissionChoice[] = [
  { decision: "allow_once", label: "允许一次", style: "primary" },
  { decision: "always_allow", label: "本会话总是允许这一类", style: "default" },
  { decision: "deny", label: "拒绝", style: "danger" },
];
/** 模拟模式不接模型：人说“把……写成文件”之类的话，就演示一次 write_file 经过权限关口 */
const WRITE_REQUEST = /(写|存|保存|整理).{0,12}(文件|md|文档)/;
/** 模拟模式的新工具演示：说“待办 / 计划步骤”演示 todo_write，说“问我 / 提问”演示 ask_user_question，说“运行命令”演示 shell 确认卡 */
const TODO_REQUEST = /待办|todo|列.{0,4}步骤|分几步/i;
const QUESTION_REQUEST = /问我|提问|选一个|让我选/;
const SHELL_REQUEST = /运行命令|跑个命令|命令行|shell/i;
const DEMO_QUESTIONS: QuestionItem[] = [
  {
    id: "period",
    header: "统计周期",
    question: "要出哪一周的周报？",
    options: [
      { label: "第 44 周（推荐）", description: "目录里最新的一周" },
      { label: "第 43 周", description: "上一周" },
    ],
  },
  {
    id: "extras",
    header: "附加章节",
    question: "除了正文，还要附上哪些内容？",
    multiSelect: true,
    options: [{ label: "品类明细" }, { label: "门店排名" }, { label: "异常原因" }],
  },
];
const DEMO_SHELL = { command: "Get-ChildItem -Recurse -Filter *.md | Measure-Object", description: "统计工作区里有多少个 Markdown 文件", workdir: "D:\\工作区\\我的工作区" };
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
  /** 旧任务推导出的只读会话可以归档（不能重命名、发消息、选模型） */
  const legacyArchived = new Set<string>();
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

  /** 会话里还没结束的任务（只可能是最新的那个）；和 internal/session/tasks.go 的 openTask 一致 */
  async function openTask(s: MockSession): Promise<TaskSummary | undefined> {
    const summary = await summaryOf(s.taskId);
    return summary && !FINISHED.has(summary.state) ? summary : undefined;
  }

  async function view(s: MockSession): Promise<SessionSummary> {
    const title = titleOf(s);
    const summary = await summaryOf(s.taskId);
    const closed = summary !== undefined && FINISHED.has(summary.state);
    const status: SessionSummary["status"] = s.taskId ? (closed ? "closed" : "task") : s.employee ? "drafting" : "reception";
    const unread = unreadOf(s, summary);
    return {
      // 绑定未结束的任务时是任务的当前模型；任务已结束（T45）或还没任务时是选过的模型，没选过沿用 lastModel
      ...(s.taskId && !closed ? { model: taskModels.get(s.taskId) } : { model: s.selected ?? lastModel, modelSelected: s.selected !== undefined }),
      permissionMode: s.permissionMode,
      todos: [...s.todos],
      id: s.id,
      workspaceId: s.workspaceId,
      status,
      readOnly: false,
      title,
      employee: s.employee,
      taskId: s.taskId,
      taskIds: [...s.taskIds],
      draft: s.draft,
      attachments: attachments(s),
      createdAt: s.createdAt,
      updatedAt: summary && summary.updatedAt > s.updatedAt ? summary.updatedAt : s.updatedAt,
      taskState: summary?.state,
      statusText: summary?.statusText,
      waitingFor: summary?.waitingFor ?? [],
      statusDot: dotOf(getRole(), status, summary?.state, unread, s.forms.size > 0),
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

  /** 每个任务分别投影；第二次运行起的消息 ID 加上运行次数前缀（每个任务的事件序号都从 1 开始） */
  async function withTasks(messages: SessionMessage[], taskIds: string[], s?: MockSession): Promise<SessionMessage[]> {
    let merged = messages.map((m) => ({ ...m }));
    for (const [run, taskId] of taskIds.entries()) {
      const part = await withTask([], taskId, s);
      merged = merged.concat(run === 0 ? part : part.map((m) => ({ ...m, id: `r${run + 1}-${m.id}` })));
    }
    merged.sort((a, b) => Date.parse(a.ts) - Date.parse(b.ts));
    return merged;
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

  const modeName = (mode: string) => permissionModeLabel(mode);

  /** 权限确认卡读取时配上状态：有没有答复、是不是还在等、能点的按钮（对应 Go 的 projectPermissions） */
  function projectPermissions(s: MockSession, messages: SessionMessage[]): SessionMessage[] {
    return messages.map((m) => {
      const card = m.card;
      if (!card || card.type !== "permission" || !card.requestId) return m;
      const waiting = s.pending?.requestId === card.requestId;
      const decision = s.decisions.get(card.requestId);
      return {
        ...m,
        card: waiting ? { ...card, pending: true, decision: undefined, choices: card.action === "run_command" ? PERMISSION_BUTTONS.filter((c) => c.decision !== "always_allow") : PERMISSION_BUTTONS } : { ...card, pending: false, decision: decision ?? "cancelled", choices: undefined },
      };
    });
  }

  /** 员工表单卡读取时配上状态（对应 Go 的 projectEmployeeForms） */
  function projectForms(s: MockSession, messages: SessionMessage[]): SessionMessage[] {
    return messages.map((m) => {
      const card = m.card;
      if (!card || card.type !== "employee_form" || !card.requestId) return m;
      const decided = s.formDecisions.get(card.requestId);
      if (decided) return { ...m, card: { ...card, pending: false, decision: decided.decision, ...(decided.taskId ? { taskId: decided.taskId } : {}) } };
      return { ...m, card: s.forms.has(card.requestId) ? { ...card, pending: true, decision: undefined } : { ...card, pending: false, decision: "cancelled" } };
    });
  }

  /** 提问卡读取时配上状态：已答复（带答案）、仍在等、或已取消（对应 Go 的 projectQuestions） */
function projectQuestions(s: MockSession, messages: SessionMessage[]): SessionMessage[] {
  return messages.map((m) => {
    const card = m.card;
    if (!card || card.type !== "question" || !card.requestId) return m;
    const result = s.questionResults.get(card.requestId);
    if (Array.isArray(result)) return { ...m, card: { ...card, pending: false, decision: "answered", answers: result } };
    if (result === "cancelled" || !s.questions.has(card.requestId)) return { ...m, card: { ...card, pending: false, decision: "cancelled", answers: undefined } };
    return { ...m, card: { ...card, pending: true, decision: undefined, answers: undefined } };
  });
}

const reply = async (s: MockSession, out: SessionMessage[]): Promise<SessionResult> => ({ ok: true, messages: projectForms(s, out), session: await view(s) });

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

  /** 一次 write_file 经过权限关口（对应 Go 的 turnLoop.gate）：仅可查看拒绝，仅限工作区和完全权限直接写（不再询问），每一步都留痕 */
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
      const deny = (reason: string, byInitiator = false) => {
        write(s, { kind: "tool_denied", tool: "write_file", args, reason, ...(byInitiator ? { decision: "deny" } : {}) }, []);
        emit(s, turn, { type: "message" });
        end({ reason, isErr: true });
      };
      const run = () => {
        write(s, { kind: "tool_called", tool: "write_file", args, result: { path: args.path, action: "created", bytes: new TextEncoder().encode(args.content).length } }, []);
        emit(s, turn, { type: "message" });
        end({ result: { path: args.path, action: "created" } });
        finish(`已把要点写进 ${args.path}。`);
      };
      if (s.permissionMode === "read_only") {
        deny(`当前是仅可查看模式，不能写入文件：${args.path}。请改为给出方案告诉发起人，等 TA 切换权限模式后再动手。`);
        return finish("仅可查看模式下我只能看不能动。打算把要点整理成 notes/周会要点.md，请切换模式后再让我写。");
      }
      write(s, { kind: "permission_allowed", tool: "write_file", args, reason: `${modeName(s.permissionMode)}模式：写入文件不用确认` }, []);
      emit(s, turn, { type: "message" });
      run();
    })();
    return turn;
  }

  /** 演示 todo_write：整表替换两次，对话里出现两张待办清单卡（最新的展开，旧的收起） */
  function startTodoTurn(s: MockSession): number {
    const turn = ++s.turn;
    s.cancelled = false;
    s.running = { turn, timer: setTimeout(() => {}, 0) };
    emit(s, turn, { type: "turn_start" });
    const lists: TodoItem[][] = [
      [
        { content: "读本周的销售明细和纪要", status: "in_progress" },
        { content: "核对统计周期", status: "pending" },
        { content: "把要点写进 notes/周会要点.md", status: "pending" },
      ],
      [
        { content: "读本周的销售明细和纪要", status: "completed" },
        { content: "核对统计周期", status: "completed" },
        { content: "把要点写进 notes/周会要点.md", status: "in_progress" },
      ],
    ];
    void (async () => {
      for (const todos of lists) {
        if (s.cancelled) break;
        const id = `mock_call_${turn}_todo_${todos[0]?.status}`;
        emit(s, turn, { type: "tool_start", id, tool: "todo_write", args: { todos } });
        await later(pace(30));
        s.todos = todos;
        const out: SessionMessage[] = [];
        write(s, { kind: "todo_written", todos }, out);
        emit(s, turn, { type: "message", message: out[0] });
        emit(s, turn, { type: "tool_end", id, tool: "todo_write", args: { todos }, result: { message: "已更新待办" } });
        await later(pace(60));
      }
      if (!s.cancelled) write(s, { kind: "assistant", text: "计划列好了，接下来我按清单一项项做。" }, []);
      s.running = undefined;
      emit(s, turn, { type: "turn_end", reason: s.cancelled ? "stopped" : "completed" });
    })();
    return turn;
  }

  /** 演示 ask_user_question：弹出提问卡，这一轮暂停，人选完提交后继续；停止时记为已取消 */
  function startQuestionTurn(s: MockSession): number {
    const turn = ++s.turn;
    s.cancelled = false;
    s.running = { turn, timer: setTimeout(() => {}, 0) };
    emit(s, turn, { type: "turn_start" });
    void (async () => {
      const requestId = newRequestId();
      const out: SessionMessage[] = [];
      write(s, { kind: "card", card: { type: "question", requestId, questions: DEMO_QUESTIONS, pending: true } }, out);
      emit(s, turn, { type: "message", message: out[0] });
      const result = await new Promise<QuestionAnswer[] | "cancelled">((resolve) => {
        s.questions.set(requestId, { resolve });
      });
      s.questions.delete(requestId);
      s.questionResults.set(requestId, result);
      if (result === "cancelled" || s.cancelled) {
        s.questionResults.set(requestId, "cancelled");
        s.running = undefined;
        emit(s, turn, { type: "turn_end", reason: "stopped" });
        return;
      }
      write(s, { kind: "assistant", text: `好的，按你选的来：${result.map((a) => [...a.selected, a.custom].filter(Boolean).join("、")).join("；")}。` }, []);
      s.running = undefined;
      emit(s, turn, { type: "turn_end", reason: "completed" });
    })();
    return turn;
  }

  /** 演示 shell 经过权限关口：仅可查看拒绝、完全权限直接放行（留痕）、仅限工作区弹带命令和警告的确认卡（没有“总是允许”） */
  function startShellTurn(s: MockSession): number {
    const turn = ++s.turn;
    s.cancelled = false;
    s.running = { turn, timer: setTimeout(() => {}, 0) };
    emit(s, turn, { type: "turn_start" });
    const args = { description: DEMO_SHELL.description, command: DEMO_SHELL.command };
    void (async () => {
      const finish = (text: string, reason: "completed" | "stopped" = "completed") => {
        if (text) write(s, { kind: "assistant", text }, []);
        s.running = undefined;
        emit(s, turn, { type: "turn_end", reason });
      };
      if (s.permissionMode === "read_only") {
        write(s, { kind: "tool_denied", tool: "shell", args, reason: "当前是仅可查看模式，不能运行命令。请改为给出方案（包括打算运行的命令）告诉发起人，等 TA 切换权限模式后再动手。" }, []);
        return finish(`仅可查看模式下我不能运行命令。打算运行：${DEMO_SHELL.command}（${DEMO_SHELL.description}），请切换模式后再让我跑。`);
      }
      if (s.permissionMode !== "full") {
        const requestId = newRequestId();
        const out: SessionMessage[] = [];
        const choices = PERMISSION_BUTTONS.filter((c) => c.decision !== "always_allow");
        write(
          s,
          {
            kind: "card",
            card: {
              type: "permission", requestId, action: "run_command", actionLabel: "运行命令", tool: "shell", object: "运行命令", detail: DEMO_SHELL.description,
              command: DEMO_SHELL.command, workdir: DEMO_SHELL.workdir, warnings: [], pending: true, choices,
            },
          },
          out,
        );
        emit(s, turn, { type: "message", message: out[0] });
        const decision = await new Promise<string>((resolve) => {
          s.pending = { requestId, resolve };
        });
        s.pending = undefined;
        s.decisions.set(requestId, decision);
        if (decision === "cancelled" || s.cancelled) {
          s.decisions.set(requestId, "cancelled");
          return finish("", "stopped");
        }
        if (decision === "deny") {
          write(s, { kind: "tool_denied", tool: "shell", args, reason: "发起人拒绝：没有允许运行命令。请换个办法，或问问发起人想怎么做。", decision: "deny" }, []);
          return finish("好的，那我不运行命令了。");
        }
      } else {
        write(s, { kind: "permission_allowed", tool: "shell", args, reason: "完全权限模式：运行命令不用确认" }, []);
      }
      write(s, { kind: "tool_called", tool: "shell", args, result: { output: "Count    : 12\n", exitCode: 0 } }, []);
      finish("命令跑完了：工作区里有 12 个 Markdown 文件。");
    })();
    return turn;
  }
  const newRequestId = () => Math.random().toString(16).slice(2, 10).padEnd(8, "0") + Date.now().toString(16).slice(-8).padStart(8, "0");

  /** 会话里按员工的开工清单判定：会话材料文件名 + 表单 + 人确认跳过的项 */
  const checklistFor = (employee: string, form: Record<string, unknown>, names: string[], skipped: ReadonlySet<string>) => {
    const items = CHECKLISTS[employee];
    return items ? evaluateChecklist(items, form, names.map((name) => ({ name })), skipped) : undefined;
  };

  /** 开工：和 Go 的 startEmployeeTask 一样，清单备齐才建任务；跳过的项开工时写进表单字段并留痕 */
  async function beginTask(s: MockSession, employee: string, form: Record<string, unknown>, files: File[], skipped: ReadonlySet<string>): Promise<SessionResult & { taskId?: string }> {
    const open = await openTask(s);
    if (open) return fail("session_has_task", `这个会话里的任务 ${open.id} 还没结束（现在是“${STATE_LABELS[open.state] ?? open.state}”），等它交付或终止后才能开新任务。`);
    const checklist = checklistFor(employee, form, files.map((f) => f.name), skipped);
    if (checklist && !checklist.ready) return fail("checklist_incomplete", `开工清单还缺：${checklist.missing.join("、")}。备齐后才能开工。`);
    const out: SessionMessage[] = [];
    const skippedItems = (CHECKLISTS[employee] ?? []).filter((i) => i.skipField && checklist?.items.some((e) => e.id === i.id && e.status === "skipped"));
    for (const item of skippedItems) {
      write(s, { kind: "notice", text: `发起人确认跳过开工清单的“${item.label}”${item.skipLabel ? `（${item.skipLabel}）` : ""}。` }, out);
    }
    const startForm = { ...form, ...Object.fromEntries(skippedItems.map((i) => [i.skipField!, true])) };
    // 模拟任务按上传项判定材料：会话里的文件没有上传项，按扩展名归位
    const uploads: UploadItem[] = files.map((file) => ({ slot: /\.docx$/i.test(file.name) ? "minutesFiles" : "salesFile", file }));
    const snapshot = await tasks.createTask({ employee, form: startForm, uploads, model: s.selected ?? lastModel });
    s.employee = employee;
    s.taskId = snapshot.id;
    s.taskIds.push(snapshot.id);
    taskModels.set(snapshot.id, s.selected ?? lastModel);
    const res = await tasks.command(snapshot.id, { type: "submit" });
    if (!res.ok) return fail("illegal_transition", "提交被拒绝");
    return { ok: true, messages: out, session: await view(s), taskId: snapshot.id };
  }

  const materialFile = (name: string) => new File([`模拟的 ${name}`], name);

  /** 一次 run_employee（对应 Go 的 runEmployee）：仅可查看拒绝；完全权限在表单完整有效且必需项备齐时直接开工，否则（含仅限工作区）弹员工表单卡等人答复 */
  function startRunEmployeeTurn(s: MockSession, employee: EmployeeInfo, text: string): number {
    const turn = ++s.turn;
    s.cancelled = false;
    s.running = { turn, timer: setTimeout(() => {}, 0) };
    emit(s, turn, { type: "turn_start" });
    const week = /第\s*(\d{1,2})\s*周/.exec(text)?.[1];
    const fields: Record<string, unknown> = week ? { period: { year: 2026, week: Number(week) } } : {};
    // 有开工清单的员工（经营周报）：目录里的销售明细和纪要就是材料
    const materials: FormMaterial[] = CHECKLISTS[employee.id]
      ? [
          { name: "销售明细.xlsx", size: 28_000, source: "folder", path: "销售明细.xlsx" },
          { name: "周会纪要_2026W44.docx", size: 18_000, source: "folder", path: "周会纪要_2026W44.docx" },
        ]
      : [];
    const args = { employee: employee.id, fields, materials: materials.map((m) => m.path ?? m.name) };
    const callId = `mock_call_${turn}_run_employee`;
    const finish = (said: string, reason: "completed" | "stopped" = "completed") => {
      if (said) write(s, { kind: "assistant", text: said }, []);
      s.running = undefined;
      emit(s, turn, { type: "turn_end", reason });
    };
    void (async () => {
      emit(s, turn, { type: "tool_start", id: callId, tool: "run_employee", args });
      await later(pace(30));
      const end = (extra: Partial<SessionAgentEvent>) => emit(s, turn, { type: "tool_end", id: callId, tool: "run_employee", args, ...extra });
      const open = await openTask(s);
      if (open) {
        const reason = `这个会话里的任务 ${open.id} 还没结束（现在是“${STATE_LABELS[open.state] ?? open.state}”），等它交付或终止后才能开新任务。`;
        end({ reason, isErr: true });
        return finish(reason);
      }
      if (s.permissionMode === "read_only") {
        const reason = `当前是仅可查看模式，不能运行员工：${employee.name}（${employee.id}）。请改为给出方案告诉发起人，等 TA 切换权限模式后再动手。`;
        write(s, { kind: "tool_denied", tool: "run_employee", args, reason, decision: "deny" }, []);
        emit(s, turn, { type: "message" });
        end({ reason, isErr: true });
        return finish(`仅可查看模式下我只能给方案：打算让${employee.name}来做，请切换模式后再让我开工。`);
      }
      const ready = Boolean(week) && (checklistFor(employee.id, fields, materials.map((m) => m.name), new Set())?.ready ?? true);
      if (ready && s.permissionMode === "full") {
        const started = await beginTask(s, employee.id, fields, materials.map((m) => materialFile(m.name)), new Set());
        if (!started.ok) {
          end({ reason: started.message, isErr: true });
          return finish(started.message);
        }
        write(s, { kind: "permission_allowed", tool: "run_employee", args, reason: `${modeName(s.permissionMode)}模式：${employee.name}的表单完整有效，必需的开工清单项已备齐，不用确认直接开工` }, []);
        emit(s, turn, { type: "message" });
        end({ result: { started: true, taskId: started.taskId } });
        return finish(`${employee.name}已经开工了。`);
      }
      const requestId = newRequestId();
      const cardOut: SessionMessage[] = [];
      write(
        s,
        {
          kind: "card",
          card: {
            type: "employee_form",
            requestId,
            employee: employee.id,
            employeeName: employee.name,
            draft: fields,
            prefilled: Object.keys(fields),
            materials,
            checklist: CHECKLISTS[employee.id],
            baseline: s.files.map((f) => f.name),
            pending: true,
            reason: employee.description,
          },
        },
        cardOut,
      );
      emit(s, turn, { type: "message", message: projectForms(s, cardOut)[0] });
      const result = await new Promise<{ rejected: boolean; taskId?: string; cancelled?: boolean }>((resolve) => {
        s.forms.set(requestId, { resolve });
      });
      s.forms.delete(requestId);
      if (result.cancelled || s.cancelled) {
        s.formDecisions.set(requestId, { decision: "cancelled" });
        end({ reason: "已取消", isErr: true });
        return finish("", "stopped");
      }
      if (result.rejected) {
        const reason = "发起人拒绝，原因：选错员工。发起人认为这项工作不该由这个员工做，请重新判断该由哪个员工做，或问问发起人想让谁做。";
        write(s, { kind: "tool_denied", tool: "run_employee", args, reason, decision: "deny" }, []);
        emit(s, turn, { type: "message" });
        end({ reason, isErr: true });
        return finish("好的，我再确认一下该由谁来做。");
      }
      end({ result: { started: true, taskId: result.taskId } });
      finish(`${employee.name}已经开工了。`);
    })();
    return turn;
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
      taskIds: [task.id],
      attachments: [],
      createdAt: task.updatedAt,
      updatedAt: task.updatedAt,
      taskState: task.state,
      statusText: task.statusText,
      waitingFor: task.waitingFor,
      statusDot: dotOf(getRole(), status, task.state, unread),
      unread,
      archived: legacyArchived.has(task.id),
      permissionMode: "workspace",
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
      const next: ModelRef = { connection, model };
      if (s.selected && s.selected.connection === connection && s.selected.model === model) return reply(s, []);
      // T45：任务已结束（交付、终止）时只改会话的选择，不写任务事件；下一个任务用它
      const task = s.taskId ? await tasks.getTask(s.taskId) : undefined;
      const ended = task !== undefined && FINISHED.has(task.state);
      const out: SessionMessage[] = [];
      s.selected = next;
      lastModel = next;
      if (s.taskId && !ended) {
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
      // 旧五档的值也收（映射成新值），和后端一致
      const normalized = ACCEPTED_MODE_VALUES.includes(mode) ? normalizePermissionMode(mode) : undefined;
      if (!normalized) return fail("invalid_input", `没有这个权限模式：${mode}。可选 read_only（仅可查看）、workspace（仅限工作区）、full（完全权限）`);
      mode = normalized;
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

    async answerQuestion(id, requestId, answers) {
      if (getRole() !== "initiator") return fail("forbidden_role", "只有发起人可以回答提问");
      const s = writable(id);
      if (isFailure(s)) return s;
      const message = s.messages.find((m) => m.card?.type === "question" && m.card.requestId === requestId);
      const card = message?.card;
      if (!card) return fail("not_found", `没有这张提问：${requestId}`);
      const waiting = s.questions.get(requestId);
      if (!waiting || s.questionResults.has(requestId)) return fail("permission_settled", "这张确认已经答复或取消了");
      // 校验规则照搬 Go 的 validateAnswers：逐题对应、选项存在、单选只能选一个、每题至少回答一点
      const questions = card.questions ?? [];
      const checked: QuestionAnswer[] = [];
      for (const q of questions) {
        const a = answers.find((x) => x.id === q.id);
        if (!a) return fail("invalid_input", `问题 ${q.id} 还没有回答`);
        const labels = new Set(q.options.map((o) => o.label));
        if (a.selected.some((l) => !labels.has(l))) return fail("invalid_input", `问题 ${q.id} 没有这个选项`);
        if (a.selected.length > 1 && !q.multiSelect) return fail("invalid_input", `问题 ${q.id} 是单选，只能选一个`);
        const custom = (a.custom ?? "").trim();
        if (a.selected.length === 0 && !custom) return fail("invalid_input", `问题 ${q.id} 还没有回答`);
        checked.push(custom ? { id: q.id, selected: a.selected, custom } : { id: q.id, selected: a.selected });
      }
      if (answers.some((a) => !questions.some((q) => q.id === a.id))) return fail("invalid_input", "答案里有不存在的问题");
      waiting.resolve?.(checked);
      return reply(s, []);
    },

    async listSessions(workspaceId, options) {
      const own = await Promise.all(
        [...sessions.values()].filter((s) => (!workspaceId || s.workspaceId === workspaceId) && (options?.archived || !s.archived)).map(view),
      );
      const bound = new Set(own.flatMap((s) => s.taskIds ?? []));
      // 由旧任务推导出的只读会话：发起事件里没有工作区的旧任务带不出归属，按不属于任何工作区处理
      const legacy = workspaceId
        ? []
        : await Promise.all((await tasks.listTasks()).filter((t) => !bound.has(t.id)).map(legacyDetail));
      const all: SessionSummary[] = [...own, ...legacy.filter((s) => options?.archived || !s.archived).map(({ messages: _m, ...rest }) => rest)];
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
      const messages = projectQuestions(s, projectForms(s, projectPermissions(s, s.messages.filter((m) => m.kind !== "tool_called" || m.tool === "write_file"))));
      return { ...(await view(s)), checklist: checklistOf(s), messages: s.taskIds.length > 0 ? await withTasks(messages, s.taskIds, s) : messages };
    },

    async updateSession(id, patch) {
      if (id.startsWith(LEGACY) && "archived" in patch) {
        const task = (await tasks.listTasks()).find((t) => `${LEGACY}${t.id}` === id);
        if (!task) throw new Error("会话不存在");
        if (patch.archived === true) legacyArchived.add(task.id);
        else legacyArchived.delete(task.id);
        const { messages: _m, ...summary } = await legacyDetail(task);
        return summary;
      }
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

    /** 建会话（T45）：带 model 时按选模型的规则校验、从第一句话起用它；不带时沿用 lastModel */
    async createSession(workspaceId, model) {
      if (model) {
        const option = MOCK_MODEL_OPTIONS.find((o) => o.name === model.connection);
        if (!option) throw new Error(`没有这个连接：${model.connection}`);
        if (!option.models.includes(model.model)) throw new Error(`模型「${model.model}」不在连接「${model.connection}」的模型列表里`);
        lastModel = { connection: model.connection, model: model.model };
      }
      const now = new Date(Date.now() + ++counter).toISOString();
      const s: MockSession = {
        id: `s-mock-${counter}`,
        selected: model,
        workspaceId: workspaceId ?? MOCK_WORKSPACE,
        archived: false,
        taskIds: [],
        readUpTo: 0,
        skipped: new Set(),
        permissionMode: "workspace",
        always: new Set(),
        decisions: new Map(),
        questions: new Map(),
        questionResults: new Map(),
        todos: [],
        formDecisions: new Map(),
        forms: new Map(),
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
      if (WRITE_REQUEST.test(text)) return { ok: true, turn: startWriteTurn(s) };
      if (TODO_REQUEST.test(text)) return { ok: true, turn: startTodoTurn(s) };
      if (QUESTION_REQUEST.test(text)) return { ok: true, turn: startQuestionTurn(s) };
      if (SHELL_REQUEST.test(text)) return { ok: true, turn: startShellTurn(s) };
      // 模拟模式不接模型：按员工名里的字粗略匹配，匹配上就演示一次 run_employee；已有任务时只有说“再来一版”之类才算要再开工
      const guess = employees.find((e) => [...e.name.replace(/员工$/, "")].filter((ch) => text.includes(ch)).length >= 2);
      if (guess && (!s.taskId || /再来|再出|重新|再做|新的一?版|下一/.test(text))) return { ok: true, turn: startRunEmployeeTurn(s, guess, text) };
      if (s.taskId) {
        const turn = startTurn(s, [{ text: "模拟模式不接模型。任务进展见卡片和右栏，操作请点卡片上的按钮。" }]);
        return { ok: true, turn };
      }
      const turn = startTurn(s, [{ text: "模拟模式不接模型，我判断不出该让哪位员工做。可以说“出第 44 周周报”试试。" }]);
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
      for (const question of s.questions.values()) question.resolve?.("cancelled");
      for (const form of s.forms.values()) form.resolve?.({ rejected: false, cancelled: true } as never);
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

    /** 员工表单卡上的答复：规则照搬 Go 的 AnswerEmployeeForm（只有发起人；清单没备齐拒绝；只能答一次） */
    async answerEmployeeForm(id, requestId, answer) {
      if (getRole() !== "initiator") return fail("forbidden_role", "只有发起人可以答复员工表单");
      const s = writable(id);
      if (isFailure(s)) return s;
      const message = s.messages.find((m) => m.card?.type === "employee_form" && m.card.requestId === requestId);
      const card = message?.card;
      if (!card) return fail("not_found", `没有这张员工表单：${requestId}`);
      const waiting = s.forms.get(requestId);
      if (!waiting || s.formDecisions.has(requestId)) return fail("form_settled", "这张表单已经答复或取消了");
      if (answer.decision === "reject") {
        s.forms.delete(requestId);
        s.formDecisions.set(requestId, { decision: "rejected" });
        waiting.resolve?.({ rejected: true });
        return reply(s, []);
      }
      const form = answer.form ?? card.draft ?? {};
      const files = [
        ...(card.materials ?? []).filter((m) => m.source !== "attachment").map((m) => materialFile(m.name)),
        ...s.files.filter((f) => answer.attachments.includes(f.name) || (card.materials ?? []).some((m) => m.source === "attachment" && m.name === f.name)),
      ];
      const items = CHECKLISTS[card.employee ?? ""] ?? [];
      for (const item of answer.skipped) {
        const found = items.find((i) => i.id === item);
        if (!found) return fail("invalid_input", `开工清单里没有这一项：${item}`);
        if (found.required) return fail("invalid_input", `“${found.label}”是必需项，不能跳过`);
      }
      const started = await beginTask(s, card.employee ?? "", form, files, new Set(answer.skipped));
      if (!started.ok) return started;
      s.forms.delete(requestId);
      s.formDecisions.set(requestId, { decision: "confirmed", taskId: started.taskId });
      waiting.resolve?.({ rejected: false, taskId: started.taskId });
      return reply(s, started.messages);
    },

    /** 载入公开样例：把样例的输入预填成一张员工表单，人确认即开工（样例材料不写进人的工作目录） */
    async loadSampleForm(id) {
      if (getRole() !== "initiator") return fail("forbidden_role", "只有发起人可以载入公开样例");
      const s = writable(id);
      if (isFailure(s)) return s;
      const open = await openTask(s);
      if (open) return fail("session_has_task", `这个会话里的任务 ${open.id} 还没结束（现在是“${STATE_LABELS[open.state] ?? open.state}”），等它交付或终止后才能开新任务。`);
      const employee = employees.find((e) => e.id === "weekly-report") ?? employees[0];
      if (!employee) return fail("not_found", "没有公开样例");
      const requestId = newRequestId();
      s.forms.set(requestId, {});
      const out: SessionMessage[] = [];
      const fields = { period: { year: 2026, week: 44 } };
      write(
        s,
        {
          kind: "card",
          card: {
            type: "employee_form",
            requestId,
            employee: employee.id,
            employeeName: employee.name,
            draft: fields,
            prefilled: Object.keys(fields),
            materials: [
              { name: "销售明细.xlsx", size: 28_000, source: "sample" },
              { name: "周会纪要_2026W44.docx", size: 18_000, source: "sample" },
            ],
            checklist: CHECKLISTS[employee.id],
            baseline: s.files.map((f) => f.name),
            pending: true,
            standalone: true,
            reason: "公开样例：正常任务（含纪要与数据不一致）",
          },
        },
        out,
      );
      return reply(s, out);
    },

    /** 直接找员工：给指定员工弹出一张空白员工表单，人填好确认后开工 */
    async openEmployeeForm(id, employeeId) {
      if (getRole() !== "initiator") return fail("forbidden_role", "只有发起人可以找员工开工");
      const s = writable(id);
      if (isFailure(s)) return s;
      const open = await openTask(s);
      if (open) return fail("session_has_task", `这个会话里的任务 ${open.id} 还没结束（现在是“${STATE_LABELS[open.state] ?? open.state}”），等它交付或终止后才能开新任务。`);
      const employee = employees.find((e) => e.id === employeeId);
      if (!employee) return fail("not_found", `没有这个员工：${employeeId}`);
      const requestId = newRequestId();
      s.forms.set(requestId, {});
      const out: SessionMessage[] = [];
      write(
        s,
        {
          kind: "card",
          card: {
            type: "employee_form",
            requestId,
            employee: employee.id,
            employeeName: employee.name,
            draft: {},
            prefilled: [],
            materials: [],
            checklist: CHECKLISTS[employee.id],
            baseline: s.files.map((f) => f.name),
            pending: true,
            standalone: true,
            reason: `你直接选了${employee.name}，没有经过前台 Agent`,
          },
        },
        out,
      );
      return reply(s, out);
    },

    async uploadAttachments(id, files) {
      if (getRole() !== "initiator") return fail("forbidden_role", "只有发起人可以上传材料");
      const s = writable(id);
      if (isFailure(s)) return s;
      if (await openTask(s)) return fail("session_has_task", "任务已经开始，请在暂停卡片上补充材料");
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

    // 模拟模式的工作区文件：只有样例文件的名字，没有内容
    async browseWorkspace(_id, path) {
      if (path) return { entries: [], truncated: false };
      return {
        entries: [
          { path: "销售明细.xlsx", name: "销售明细.xlsx", size: 28_000 },
          { path: "周会纪要_2026W44.docx", name: "周会纪要_2026W44.docx", size: 18_000 },
        ],
        truncated: false,
      };
    },
    async readWorkspaceFile() {
      throw new Error("模拟模式没有文件内容");
    },
    async openWorkspacePath() {
      return { ok: false as const, message: "模拟模式没有文件可以打开" };
    },

    // @ 选择即视为人已确认：和卡片上的“用这个文件”一样，复制进会话材料
    async useFolderFiles(id, paths) {
      if (getRole() !== "initiator") return fail("forbidden_role", "只有发起人可以确认目录里的文件");
      const s = writable(id);
      if (isFailure(s)) return s;
      if (await openTask(s)) return fail("session_has_task", "任务已经开始，请在暂停卡片上补充材料");
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
      const open = await openTask(s);
      if (open) return fail("session_has_task", `这个会话里的任务 ${open.id} 还没结束（现在是“${STATE_LABELS[open.state] ?? open.state}”），等它交付或终止后才能开新任务。`);
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
      s.taskIds.push(snapshot.id);
      taskModels.set(snapshot.id, s.selected ?? lastModel);
      const res = await tasks.command(snapshot.id, { type: "submit" });
      if (!res.ok) return fail("illegal_transition", "提交被拒绝");
      return reply(s, []);
    },

    async draftFiling(id, forTask) {
      if (getRole() !== "initiator") return fail("forbidden_role", "只有发起人可以把交付物放进知识库");
      const s = writable(id);
      if (isFailure(s)) return s;
      const target = forTask ?? s.taskId;
      const task = target && s.taskIds.includes(target) ? await tasks.getTask(target) : undefined;
      if (!task || task.state !== "delivered") return fail("illegal_transition", "任务交付之后才能放进知识库");
      const title = (await summaryOf(task.id))?.title ?? task.id;
      s.filingDraft = `${title}（模拟模式按标题起草的描述，请按实际内容修改）`;
      return reply(s, []);
    },

    async skipChecklistItem(id, itemId) {
      if (getRole() !== "initiator") return fail("forbidden_role", "只有发起人可以确认跳过清单项");
      const s = writable(id);
      if (isFailure(s)) return s;
      if (await openTask(s)) return fail("session_has_task", "任务已经开始，清单不能再改");
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
      const taskList = await tasks.listTasks();
      const items: RecordItem[] = [];
      for (const summary of summaries) {
        // 每个任务一行：同一个会话先后运行的任务各占一行，按各自的状态分组
        const ids = summary.taskIds ?? (summary.taskId ? [summary.taskId] : []);
        ids.forEach((taskId, index) => {
          const task = taskList.find((t) => t.id === taskId);
          if (!task) return;
          const latest = index === ids.length - 1;
          items.push({
            taskId,
            group: recordGroupOf(task.state),
            title: summary.title,
            employee: summary.employee ?? "",
            state: task.state,
            statusText: task.statusText ?? "",
            // 记录里的状态点按读的人的角色算，和侧栏列表走同一套规则；早先的任务已经结束，不再提示
            statusDot: latest ? dotOf(role, summary.status, task.state, false) : "idle",
            sessionId: summary.id,
            // 由旧任务推导出的只读会话没有“第几次运行”
            ...(summary.readOnly ? {} : { runNumber: index + 1 }),
            workspaceId: summary.workspaceId,
            workspaceName: summary.workspaceName ?? "",
            updatedAt: latest ? summary.updatedAt : task.updatedAt,
          });
        });
      }
      return items;
    },
  };
  /** 全部会话（含归档的），带状态点：工作区列表和记录 tab 都从这里取 */
  async function allSummaries(): Promise<SessionSummary[]> {
    const own = await Promise.all([...sessions.values()].map(view));
    const bound = new Set(own.flatMap((s) => s.taskIds ?? []));
    // 种子任务的旧任务会话也列出来：和 Go 一样，没绑会话的任务以只读会话出现
    const legacy = await Promise.all((await tasks.listTasks()).filter((t) => !bound.has(t.id)).map(legacyDetail));
    const summaries = [...own, ...legacy.map(({ messages: _m, ...rest }) => rest)];
    return summaries.sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt));
  }
}
