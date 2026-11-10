// tl2：待办清单卡、提问卡、shell 确认卡的界面派生，以及模拟模式里的演示。纯函数，不依赖 React 与网络。
import { describe, expect, test } from "bun:test";
import { createMockApi } from "../src/api/mock";
import type { QuestionItem, SessionCard, SessionDetail, SessionMessage, TodoItem, UiRole } from "../src/api/types";
import { deriveChat } from "../src/view/chat-view";
import { derivePermissionCard, traceOf } from "../src/view/permission-mode";
import { answerText, buildAnswers, canSubmit, deriveQuestionCard, emptyDraft, setCustom, toggleOption, unanswered } from "../src/view/question";
import { cleanTodos, countTodos, deriveTodoCard, latestTodoId, todoSummary } from "../src/view/todo";

let clock = Date.parse("2026-11-02T09:00:00.000Z");
const msg = (input: Omit<SessionMessage, "id" | "seq" | "ts">): SessionMessage => {
  clock += 1000;
  const seq = clock / 1000;
  return { id: `s${seq}`, seq, ts: new Date(clock).toISOString(), ...input };
};

const session = (messages: SessionMessage[], patch: Partial<SessionDetail> = {}): SessionDetail => ({
  id: "s-1",
  workspaceId: "ws-1",
  status: "reception",
  readOnly: false,
  title: "t",
  attachments: [],
  createdAt: "2026-11-02T09:00:00.000Z",
  updatedAt: "2026-11-02T09:10:00.000Z",
  waitingFor: [],
  statusDot: "idle",
  unread: false,
  archived: false,
  messages,
  ...patch,
});

describe("待办清单", () => {
  const first: TodoItem[] = [
    { content: "读纪要", status: "in_progress" },
    { content: "写要点", status: "pending" },
  ];
  const second: TodoItem[] = [
    { content: "读纪要", status: "completed" },
    { content: "写要点", status: "in_progress" },
    { content: "发给审核人", status: "pending" },
  ];

  test("统计与摘要", () => {
    expect(countTodos(second)).toEqual({ pending: 1, inProgress: 1, completed: 1 });
    expect(todoSummary(second)).toBe("3 项待办，已完成 1 项，正在做：写要点");
    expect(todoSummary([{ content: "a", status: "completed" }])).toBe("1 项待办，已完成 1 项");
    expect(todoSummary([])).toBe("待办清单已清空");
  });

  test("三种状态三种图标", () => {
    const card = deriveTodoCard({ todos: second }, true);
    expect(card.rows.map((r) => [r.content, r.icon])).toEqual([
      ["读纪要", "check"],
      ["写要点", "progress"],
      ["发给审核人", "pending"],
    ]);
  });

  test("不认识的项丢掉，不让界面崩", () => {
    expect(cleanTodos([{ content: " ", status: "pending" }, { content: "好", status: "weird" as never }, { content: "对", status: "pending" }])).toEqual([{ content: "对", status: "pending" }]);
    expect(cleanTodos(undefined)).toEqual([]);
  });

  test("对话里每次更新一张卡：最新的展开，旧的收起", () => {
    const a = msg({ kind: "todo_written", todos: first });
    const b = msg({ kind: "todo_written", todos: second });
    const items = deriveChat(session([msg({ kind: "user", role: "initiator", text: "做" }), a, msg({ kind: "tool_called", tool: "todo_write" }), b]));
    const todos = items.filter((i) => i.kind === "todo");
    expect(todos.map((t) => t.kind === "todo" && t.latest)).toEqual([false, true]);
    expect(latestTodoId([a, b])).toBe(b.id);
    expect(latestTodoId([msg({ kind: "notice", text: "x" })])).toBeUndefined();
  });
});

describe("提问卡", () => {
  const questions: QuestionItem[] = [
    { id: "period", header: "统计周期", question: "哪一周？", options: [{ label: "第 44 周" }, { label: "第 43 周" }] },
    { id: "extras", question: "附加什么？", multiSelect: true, options: [{ label: "品类" }, { label: "门店" }, { label: "原因" }] },
  ];
  const card = (patch: Partial<SessionCard> = {}): SessionCard => ({ type: "question", requestId: "r1", questions, pending: true, ...patch });
  const as = (role: UiRole, writable = true) => ({ role, writable });

  test("等待中：只有发起人能答；别的角色只看到在等谁", () => {
    expect(deriveQuestionCard(card(), as("initiator"))).toMatchObject({ state: "pending", canAnswer: true, stateText: "等你回答" });
    expect(deriveQuestionCard(card(), as("reviewer"))).toMatchObject({ state: "pending", canAnswer: false, stateText: "等待发起人回答" });
    expect(deriveQuestionCard(card(), as("initiator", false)).canAnswer).toBe(false);
  });

  test("已答复显示每题的答案；停止显示已取消", () => {
    const answered = deriveQuestionCard(
      card({ pending: false, decision: "answered", answers: [{ id: "period", selected: ["第 44 周"] }, { id: "extras", selected: ["品类"], custom: "再加摘要" }] }),
      as("initiator"),
    );
    expect(answered.state).toBe("answered");
    expect(answered.answered.map((a) => [a.header, a.text])).toEqual([
      ["统计周期", "第 44 周"],
      ["附加什么？", "品类、其他：再加摘要"],
    ]);
    expect(deriveQuestionCard(card({ pending: false, decision: "cancelled" }), as("initiator"))).toMatchObject({ state: "cancelled", canAnswer: false });
    expect(answerText(undefined)).toBe("（未回答）");
  });

  test("单选换成新的选项（再点一次取消），多选来回切换", () => {
    let draft = emptyDraft(questions);
    draft = toggleOption(draft, questions[0]!, "第 44 周");
    draft = toggleOption(draft, questions[0]!, "第 43 周");
    expect(draft.period?.selected).toEqual(["第 43 周"]);
    draft = toggleOption(draft, questions[0]!, "第 43 周");
    expect(draft.period?.selected).toEqual([]);
    draft = toggleOption(draft, questions[1]!, "品类");
    draft = toggleOption(draft, questions[1]!, "原因");
    draft = toggleOption(draft, questions[1]!, "品类");
    expect(draft.extras?.selected).toEqual(["原因"]);
  });

  test("每题至少选一项或写“其他”才能提交；答案按卡片顺序，空的“其他”不带", () => {
    let draft = emptyDraft(questions);
    expect(canSubmit(questions, draft)).toBe(false);
    expect(unanswered(questions, draft).map((q) => q.id)).toEqual(["period", "extras"]);
    draft = toggleOption(draft, questions[0]!, "第 43 周");
    expect(unanswered(questions, draft).map((q) => q.id)).toEqual(["extras"]);
    draft = setCustom(draft, "extras", "  只要摘要  ");
    expect(canSubmit(questions, draft)).toBe(true);
    draft = toggleOption(draft, questions[1]!, "原因");
    draft = toggleOption(draft, questions[1]!, "品类");
    expect(buildAnswers(questions, draft)).toEqual([
      { id: "period", selected: ["第 43 周"] },
      { id: "extras", selected: ["品类", "原因"], custom: "只要摘要" },
    ]);
  });

  test("接进对话流", () => {
    const items = deriveChat(session([msg({ kind: "card", card: card() })]));
    const question = items.find((i) => i.kind === "question");
    expect(question && question.kind === "question" && question.view.canAnswer).toBe(true);
  });
});

describe("shell 确认卡", () => {
  const shellCard: SessionCard = {
    type: "permission", requestId: "r2", action: "run_command", actionLabel: "运行命令", tool: "shell", object: "运行命令", detail: "统计文件数",
    command: 'Get-ChildItem -Recurse | Measure-Object', workdir: "D:\\工作区", warnings: ["命令里出现了本机服务地址"], pending: true,
    choices: [{ decision: "allow_once", label: "允许一次", style: "primary" }, { decision: "deny", label: "拒绝", style: "danger" }],
  };

  test("原样给出命令、工作目录和警告；只有允许一次和拒绝", () => {
    const view = derivePermissionCard(shellCard, { writable: true, role: "initiator" });
    expect(view.shell).toEqual({ command: "Get-ChildItem -Recurse | Measure-Object", workdir: "D:\\工作区", warnings: ["命令里出现了本机服务地址"] });
    expect(view.detail).toBe("统计文件数");
    expect(view.object).toBe("");
    expect(view.buttons.map((b) => b.decision)).toEqual(["allow_once", "deny"]);
  });

  test("别的角色看不到按钮；普通权限确认卡没有 shell 块", () => {
    expect(derivePermissionCard(shellCard, { writable: true, role: "reviewer" }).buttons).toEqual([]);
    expect(derivePermissionCard({ type: "permission", action: "write_file", object: "a.md", pending: true }, { writable: true, role: "initiator" }).shell).toBeUndefined();
  });

  test("full 自动放行留痕里有完整命令；被模式拒绝的 shell 也留痕", () => {
    expect(traceOf(msg({ kind: "permission_allowed", tool: "shell", args: { command: "echo hi" }, reason: "完全权限模式：运行命令不用确认" }))?.text).toBe("已放行 shell echo hi：完全权限模式：运行命令不用确认");
    const long = traceOf(msg({ kind: "permission_allowed", tool: "shell", args: { command: "x".repeat(300) }, reason: "r" }));
    expect(long?.text).toContain("…");
    expect(traceOf(msg({ kind: "tool_denied", tool: "shell", args: { command: "ls" }, reason: "当前是仅可查看模式，不能运行命令" }))).toMatchObject({ tone: "denied" });
  });
});

describe("模拟模式演示新工具", () => {
  async function waitFor<T>(read: () => Promise<T>, ok: (value: T) => boolean): Promise<T> {
    const deadline = Date.now() + 3000;
    for (;;) {
      const value = await read();
      if (ok(value)) return value;
      if (Date.now() > deadline) throw new Error(`等不到：${JSON.stringify(value)}`);
      await new Promise((resolve) => setTimeout(resolve, 5));
    }
  }

  test("todo：说“列待办”得到两张待办清单卡，会话带最新清单", async () => {
    const api = createMockApi(() => "initiator");
    const { id } = await api.createSession();
    await api.sendMessage(id, "先列个待办");
    const detail = await waitFor(() => api.getSession(id), (d) => d.messages.filter((m) => m.kind === "todo_written").length === 2 && d.messages.some((m) => m.kind === "assistant"));
    expect(detail.todos?.map((t) => t.status)).toEqual(["completed", "completed", "in_progress"]);
    const cards = deriveChat(detail).filter((i) => i.kind === "todo");
    expect(cards.map((c) => c.kind === "todo" && c.latest)).toEqual([false, true]);
  });

  test("提问：弹出提问卡，校验答案，提交后继续；停止时记为已取消", async () => {
    const api = createMockApi(() => "initiator");
    const { id } = await api.createSession();
    await api.sendMessage(id, "有不清楚的就问我");
    const asked = await waitFor(() => api.getSession(id), (d) => d.messages.some((m) => m.card?.type === "question" && m.card.pending));
    const card = asked.messages.find((m) => m.card?.type === "question")!.card!;
    expect(await api.answerQuestion(id, card.requestId!, [{ id: "period", selected: ["第 99 周"] }, { id: "extras", selected: ["品类"] }])).toMatchObject({ ok: false, error: "invalid_input" });
    expect(await api.answerQuestion(id, card.requestId!, [{ id: "period", selected: ["第 44 周（推荐）"] }])).toMatchObject({ ok: false, error: "invalid_input" });
    expect((await api.answerQuestion(id, card.requestId!, [{ id: "period", selected: ["第 44 周（推荐）"] }, { id: "extras", selected: [], custom: "摘要" }])).ok).toBe(true);
    const done = await waitFor(() => api.getSession(id), (d) => d.messages.some((m) => m.kind === "assistant"));
    expect(done.messages.find((m) => m.card?.type === "question")?.card).toMatchObject({ pending: false, decision: "answered" });
    // 另一个会话：停止
    const other = await api.createSession();
    await api.sendMessage(other.id, "有不清楚的就问我");
    await waitFor(() => api.getSession(other.id), (d) => d.messages.some((m) => m.card?.type === "question" && m.card.pending));
    await api.stopTurn(other.id);
    const stopped = await waitFor(() => api.getSession(other.id), (d) => d.messages.some((m) => m.card?.type === "question" && !m.card.pending));
    expect(stopped.messages.find((m) => m.card?.type === "question")?.card).toMatchObject({ pending: false, decision: "cancelled" });
  });

  test("提问：非发起人不能答", async () => {
    let role: UiRole = "initiator";
    const api = createMockApi(() => role);
    const { id } = await api.createSession();
    await api.sendMessage(id, "选一个吧");
    const asked = await waitFor(() => api.getSession(id), (d) => d.messages.some((m) => m.card?.type === "question" && m.card.pending));
    role = "reviewer";
    expect(await api.answerQuestion(id, asked.messages.find((m) => m.card?.type === "question")!.card!.requestId!, [])).toMatchObject({ ok: false, error: "forbidden_role" });
  });

  test("shell：仅限工作区模式弹带命令的确认卡，只有允许一次和拒绝", async () => {
    const api = createMockApi(() => "initiator");
    const { id } = await api.createSession();
    await api.sendMessage(id, "运行命令看看有多少个文件");
    const asked = await waitFor(() => api.getSession(id), (d) => d.messages.some((m) => m.card?.type === "permission" && m.card.pending));
    const card = asked.messages.find((m) => m.card?.type === "permission")!.card!;
    expect(card).toMatchObject({ action: "run_command", tool: "shell" });
    expect(card.command).toContain("Get-ChildItem");
    expect(card.choices?.map((c) => c.decision)).toEqual(["allow_once", "deny"]);
  });
});
