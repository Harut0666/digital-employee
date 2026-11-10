import { describe, expect, test } from "bun:test";
import type { SessionDetail, SessionMessage } from "../src/api/types";
import { deriveChat } from "../src/view/chat-view";

let clock = Date.parse("2026-11-02T09:00:00.000Z");
const msg = (input: Omit<SessionMessage, "id" | "seq" | "ts">, id?: string): SessionMessage => {
  clock += 1000;
  const seq = clock / 1000;
  return { id: id ?? `s${seq}`, seq, ts: new Date(clock).toISOString(), ...input };
};

const session = (messages: SessionMessage[], patch: Partial<SessionDetail> = {}): SessionDetail => ({
  id: "s-1",
  workspaceId: "ws-1",
  status: "reception",
  readOnly: false,
  title: "帮我出第 44 周的周报",
  attachments: [],
  createdAt: "2026-11-02T09:00:00.000Z",
  updatedAt: "2026-11-02T09:10:00.000Z",
  waitingFor: [],
  statusDot: "waiting",
  unread: false,
  archived: false,
  messages,
  ...patch,
});

const EMPLOYEES = [
  { id: "weekly-report", name: "经营周报员工", description: "出经营周报" },
  { id: "kb-qa", name: "知识库问答员工", description: "回答历史结论" },
];

describe("deriveChat", () => {
  test("人和 Agent 的话成为气泡，程序说明单独显示，工具留痕不进对话", () => {
    const items = deriveChat(
      session([
        msg({ kind: "user", role: "initiator", text: "帮我出第 44 周的周报" }),
        msg({ kind: "tool_called", tool: "run_employee" }),
        msg({ kind: "tool_denied", tool: "submit", reason: "没有开放" }),
        msg({ kind: "model_failed", reason: "401" }),
        msg({ kind: "assistant", text: "这项工作适合经营周报员工。" }),
        msg({ kind: "notice", text: "经营周报员工已接手。" }),
      ]),
    );
    expect(items.map((i) => [i.kind, "text" in i ? i.text : ""])).toEqual([
      ["user", "帮我出第 44 周的周报"],
      ["agent", "这项工作适合经营周报员工。"],
      ["notice", "经营周报员工已接手。"],
    ]);
  });

  test("旧会话里的确认员工卡（T49 之前）只读显示，什么状态下都没有按钮", () => {
    const cards = [
      msg({ kind: "card", card: { type: "employee_confirm", source: "manual", candidates: EMPLOYEES } }),
      msg({ kind: "card", card: { type: "employee_confirm", proposed: "weekly-report", source: "model", reason: "要出周报", candidates: EMPLOYEES } }),
    ];
    const reception = deriveChat(session(cards));
    expect(reception.map((i) => (i.kind === "employee_confirm" ? [i.proposed, i.source, i.active] : []))).toEqual([
      [undefined, "manual", false],
      ["weekly-report", "model", false],
    ]);
    const last = reception[1];
    expect(last?.kind === "employee_confirm" && last.reason).toBe("要出周报");
    expect(last?.kind === "employee_confirm" && last.candidates.map((c) => c.id)).toEqual(["weekly-report", "kb-qa"]);
    expect(deriveChat(session(cards, { readOnly: true })).map((i) => i.kind === "employee_confirm" && i.active)).toEqual([false, false]);
  });

  test("员工表单卡：带着卡片本身，能不能操作看会话是否可写", () => {
    const form = msg({
      kind: "card",
      card: { type: "employee_form", requestId: "r1", employee: "weekly-report", employeeName: "经营周报员工", pending: true, draft: { period: { year: 2026, week: 44 } }, prefilled: ["period"] },
    });
    const items = deriveChat(session([form]));
    const item = items[0];
    expect(item?.kind === "employee_form" && [item.card.requestId, item.card.prefilled, item.writable]).toEqual(["r1", ["period"], true]);
    const readOnly = deriveChat(session([form], { readOnly: true }))[0];
    expect(readOnly?.kind === "employee_form" && readOnly.writable).toBe(false);
  });

  test("旧会话里的表单卡：草稿表单只读（已不再使用）；任务里的已提交表单只读", () => {
    const first = msg({ kind: "card", card: { type: "form", employee: "weekly-report", attachments: [] } });
    const second = msg({ kind: "card", card: { type: "form", employee: "weekly-report", draft: { period: { year: 2026, week: 44 } }, attachments: [] } });
    const drafting = session([first, second], { status: "drafting", employee: "weekly-report", draft: { period: { year: 2026, week: 44 }, remark: "先写区域" } });
    expect(deriveChat(drafting).map((i) => (i.kind === "form" ? i.mode : ""))).toEqual(["superseded", "superseded"]);

    const submitted = msg(
      { kind: "card", card: { type: "form", employee: "weekly-report", draft: { period: { year: 2026, week: 44 } }, attachments: [{ name: "销售明细.xlsx", size: 0 }], submitted: true, taskId: "t1", atSeq: 1 } },
      "t1",
    );
    const task = deriveChat(session([first, second, submitted], { status: "task", taskId: "t1", employee: "weekly-report" }));
    expect(task.map((i) => (i.kind === "form" ? i.mode : ""))).toEqual(["superseded", "superseded", "submitted"]);
    const last = task[2];
    expect(last?.kind === "form" && last.attachments.map((a) => a.name)).toEqual(["销售明细.xlsx"]);
  });
  test("任务卡片：后端配了按钮的才是当前要处理的，其余已处理", () => {
    const approve = { command: "approve" as const, label: "通过并交付", roles: ["reviewer" as const], style: "primary" as const };
    const giveBack = { command: "return" as const, label: "退回修改", roles: ["reviewer" as const], style: "default" as const };
    const items = deriveChat(
      session(
        [
          msg({ kind: "card", card: { type: "pause", taskId: "t1", atSeq: 5, pause: { code: "tool_failed", message: "工具失败", options: [] } } }, "t5"),
          msg({ kind: "card", card: { type: "qc", taskId: "t1", atSeq: 9, passed: true, checks: [{ id: "structure", label: "结构完整", passed: true, issues: [] }] } }, "t9"),
          msg(
            {
              kind: "card",
              card: {
                type: "review",
                taskId: "t1",
                atSeq: 10,
                pause: { code: "awaiting_review", message: "请审核", options: [], title: "待审核" },
                actions: [approve, giveBack],
              },
            },
            "t10",
          ),
          msg({ kind: "card", card: { type: "review", taskId: "t1", atSeq: 10, comment: "摘要太长", sections: ["summary"], actions: [approve, giveBack] } }),
        ],
        { status: "task", taskId: "t1", waitingFor: ["reviewer"] },
      ),
    );
    // 对话里只留要人答复的卡片：质检结果、送审提示在右栏；Agent 起草的退回意见是确认卡片
    expect(items.map((i) => [i.kind, "active" in i ? i.active : undefined])).toEqual([
      ["pause", false],
      ["return_confirm", true],
    ]);
    // 确认卡片只给退回这一个操作，由审核人确认后才退回
    const drafted = items[1];
    expect(drafted?.kind === "return_confirm" && [drafted.comment, drafted.sections, drafted.actions.map((a) => a.command)]).toEqual([
      "摘要太长",
      ["summary"],
      ["return"],
    ]);
    // 失效卡片没有按钮
    const pause = items[0];
    expect(pause?.kind === "pause" && pause.actions).toEqual([]);
  });

  test("交付之后对话里没有“已交付”（含“任务已交付。”那行说明）和“放入知识库”：它们都在右栏交付物下面", () => {
    const deliveredTask = { state: "delivered" as const, deliverables: [{ kind: "docx" as const, path: "经营周报.docx", sha256: "x" }] };
    const filing = msg({ kind: "card", card: { type: "knowledge_filing", taskId: "t1", title: "经营周报_2026年第44周", description: "第 44 周经营周报" } }, "t20-filing");
    const notice = msg({ kind: "notice", text: "任务已交付。" }, "t20");
    const items = deriveChat(session([notice, filing], { status: "closed", taskId: "t1" }), deliveredTask);
    expect(items.map((i) => i.kind)).toEqual([]);
  });
  test("正在说的一轮：逐字出现的文字跟在最后一条之后，工具调用单独列出", () => {
    const items = deriveChat(
      session([msg({ kind: "user", role: "initiator", text: "出周报" })], { status: "drafting", employee: "weekly-report" }),
      undefined,
      [],
      {
        turn: 2,
        text: "先看看任务",
        tools: [{ id: "c1", tool: "query_task", args: { taskId: "t1" }, ended: true, result: { state: "awaiting_initiator" } }],
      },
    );
    expect(items.map((i) => i.kind)).toEqual(["user", "tools", "streaming"]);
    const tools = items[1];
    expect(tools?.kind === "tools" && tools.turn).toBe(2);
    expect(tools?.kind === "tools" && tools.items.map((t) => [t.tool, t.ended, t.isErr ?? false])).toEqual([["query_task", true, false]]);
    const streaming = items[2];
    expect(streaming?.kind === "streaming" && [streaming.turn, streaming.text]).toEqual([2, "先看看任务"]);
  });

  test("没在说的时候不显示空的行；只发了工具调用也照常列出", () => {
    const base = session([msg({ kind: "user", role: "initiator", text: "出周报" })], { status: "drafting", employee: "weekly-report" });
    expect(deriveChat(base, undefined, [], { turn: 1, text: "", tools: [] }).map((i) => i.kind)).toEqual(["user"]);
    expect(deriveChat(base).map((i) => i.kind)).toEqual(["user"]);
    const calling = deriveChat(base, undefined, [], { turn: 1, text: "", tools: [{ id: "c1", tool: "fill_form_draft", ended: false }] });
    expect(calling.map((i) => i.kind)).toEqual(["user", "tools"]);
  });

  const formCard = { type: "employee_form" as const, requestId: "r1", employee: "weekly-report", employeeName: "经营周报员工", pending: true, draft: {}, prefilled: [] };
  const runEmployee = [{ id: "c1", tool: "run_employee", args: { employee: "weekly-report" }, ended: true, result: { requestId: "r1" } }];

  test("交给员工：这一轮的工具行排在它弹出的员工表单之前，不会落到表单下面", () => {
    const items = deriveChat(
      session([
        msg({ kind: "user", role: "initiator", text: "出周报" }),
        msg({ kind: "tool_called", tool: "run_employee", turn: 1 }),
        msg({ kind: "card", card: formCard, turn: 1 }),
      ]),
      undefined,
      [],
      { turn: 1, text: "", tools: runEmployee, running: false },
    );
    expect(items.map((i) => i.kind)).toEqual(["user", "tools", "employee_form"]);
  });

  test("工具行排在这一轮第一项之前：有回复时在回复前，卡片在回复之后也一样", () => {
    const items = deriveChat(
      session([
        msg({ kind: "user", role: "initiator", text: "出周报" }),
        msg({ kind: "tool_called", tool: "run_employee", turn: 1 }),
        msg({ kind: "assistant", text: "好的，我请经营周报员工来办。", turn: 1 }),
        msg({ kind: "card", card: formCard, turn: 1 }),
      ]),
      undefined,
      [],
      { turn: 1, text: "好的，我请经营周报员工来办。", tools: runEmployee, running: false },
    );
    expect(items.map((i) => i.kind)).toEqual(["user", "tools", "agent", "employee_form"]);
  });

  test("调用还在进行、日志里还没有这一轮的内容时，工具行排在最后", () => {
    const items = deriveChat(
      session([msg({ kind: "user", role: "initiator", text: "出周报" })]),
      undefined,
      [],
      { turn: 1, text: "", tools: [{ id: "c1", tool: "run_employee", ended: false }], running: true },
    );
    expect(items.map((i) => i.kind)).toEqual(["user", "tools"]);
  });

  test("刷新后没有事件流：带轮次的工具调用按日志在原位置补一行，连着的合成一行，不同轮分开", () => {
    const items = deriveChat(
      session([
        msg({ kind: "user", role: "initiator", text: "出周报" }),
        msg({ kind: "tool_called", tool: "query_task", turn: 1 }),
        msg({ kind: "tool_called", tool: "run_employee", turn: 1 }),
        msg({ kind: "card", card: formCard, turn: 1 }),
        msg({ kind: "user", role: "initiator", text: "再查一下" }),
        msg({ kind: "tool_called", tool: "query_task", turn: 2 }),
        msg({ kind: "assistant", text: "查到了。", turn: 2 }),
      ]),
    );
    expect(items.map((i) => i.kind)).toEqual(["user", "tools", "employee_form", "user", "tools", "agent"]);
    const first = items[1];
    expect(first?.kind === "tools" && first.items.map((t) => t.tool)).toEqual(["query_task", "run_employee"]);
    expect(new Set(items.filter((i) => i.kind === "tools").map((i) => i.key)).size).toBe(2);
  });

  test("这一轮事件流里有工具调用时，日志里同一轮的记录不再重复生成一行", () => {
    const items = deriveChat(
      session([msg({ kind: "user", role: "initiator", text: "出周报" }), msg({ kind: "tool_called", tool: "run_employee", turn: 1 })]),
      undefined,
      [],
      { turn: 1, text: "", tools: runEmployee, running: false },
    );
    expect(items.filter((i) => i.kind === "tools")).toHaveLength(1);
  });
});
