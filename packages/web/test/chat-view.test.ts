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
  status: "reception",
  readOnly: false,
  title: "帮我出第 44 周的周报",
  attachments: [],
  createdAt: "2026-11-02T09:00:00.000Z",
  updatedAt: "2026-11-02T09:10:00.000Z",
  waitingFor: [],
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
        msg({ kind: "tool_called", tool: "propose_employee" }),
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

  test("员工确认卡片：接待中只有最后一张能确认，员工接手后都失效", () => {
    const cards = [
      msg({ kind: "card", card: { type: "employee_confirm", source: "manual", candidates: EMPLOYEES } }),
      msg({ kind: "card", card: { type: "employee_confirm", proposed: "weekly-report", source: "model", reason: "要出周报", candidates: EMPLOYEES } }),
    ];
    const reception = deriveChat(session(cards));
    expect(reception.map((i) => (i.kind === "employee_confirm" ? [i.proposed, i.source, i.active] : []))).toEqual([
      [undefined, "manual", false],
      ["weekly-report", "model", true],
    ]);
    const last = reception[1];
    expect(last?.kind === "employee_confirm" && last.reason).toBe("要出周报");
    expect(last?.kind === "employee_confirm" && last.candidates.map((c) => c.id)).toEqual(["weekly-report", "kb-qa"]);

    const drafting = deriveChat(session(cards, { status: "drafting", employee: "weekly-report" }));
    expect(drafting.map((i) => i.kind === "employee_confirm" && i.active)).toEqual([false, false]);
    // 只读会话里什么都不能点
    expect(deriveChat(session(cards, { readOnly: true })).map((i) => i.kind === "employee_confirm" && i.active)).toEqual([false, false]);
  });

  test("表单卡片：最后一张可编辑，内容取会话里最新的草稿和材料；更早的已被取代；提交过的只读", () => {
    const first = msg({ kind: "card", card: { type: "form", employee: "weekly-report", attachments: [] } });
    const second = msg({ kind: "card", card: { type: "form", employee: "weekly-report", draft: { period: { year: 2026, week: 44 } }, attachments: [] } });
    const drafting = session([first, second], {
      status: "drafting",
      employee: "weekly-report",
      draft: { period: { year: 2026, week: 44 }, remark: "先写区域" },
      attachments: [{ name: "销售明细.xlsx", size: 10 }],
    });
    const items = deriveChat(drafting);
    expect(items.map((i) => (i.kind === "form" ? i.mode : ""))).toEqual(["superseded", "editable"]);
    const editable = items[1];
    expect(editable?.kind === "form" && [editable.employee, editable.draft, editable.attachments]).toEqual([
      "weekly-report",
      { period: { year: 2026, week: 44 }, remark: "先写区域" },
      [{ name: "销售明细.xlsx", size: 10 }],
    ]);

    // 提交后：任务投影出的已提交表单卡片只读，之前的草稿卡片不能再改
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
    // 审核在右栏进行：对话里的审核卡只是一句提示；Agent 起草的退回意见是确认卡片
    expect(items.map((i) => [i.kind, "active" in i ? i.active : undefined])).toEqual([
      ["pause", false],
      ["qc", undefined],
      ["review_notice", true],
      ["return_confirm", true],
    ]);
    const qc = items[1];
    expect(qc?.kind === "qc" && [qc.passed, qc.checks.length]).toEqual([true, 1]);
    const review = items[2];
    expect(review?.kind === "review_notice" && review.pause?.title).toBe("待审核");
    // 确认卡片只给退回这一个操作，由审核人确认后才退回
    const drafted = items[3];
    expect(drafted?.kind === "return_confirm" && [drafted.comment, drafted.sections, drafted.actions.map((a) => a.command)]).toEqual([
      "摘要太长",
      ["summary"],
      ["return"],
    ]);
    // 失效卡片没有按钮
    const pause = items[0];
    expect(pause?.kind === "pause" && pause.actions).toEqual([]);
  });

  test("任务交付后，对话末尾给出交付物", () => {
    const delivered = deriveChat(session([msg({ kind: "notice", text: "任务已交付。" }, "t20")], { status: "closed", taskId: "t1" }), {
      state: "delivered",
      deliverables: [{ kind: "docx", path: "经营周报.docx", sha256: "x" }],
    });
    expect(delivered.map((i) => i.kind)).toEqual(["notice", "delivered"]);
    const last = delivered[1];
    expect(last?.kind === "delivered" && last.deliverables.map((d) => d.kind)).toEqual(["docx"]);
    // 没交付就不给
    expect(deriveChat(session([], { status: "task", taskId: "t1" }), { state: "awaiting_review", deliverables: [] }).map((i) => i.kind)).toEqual([]);
  });

  test("入库确认卡片排在交付物之后；入库前可以操作，入库后只读；只读会话不能请模型起草", () => {
    const deliveredTask = { state: "delivered" as const, deliverables: [{ kind: "docx" as const, path: "经营周报.docx", sha256: "x" }] };
    const filing = (patch: Record<string, unknown>) =>
      msg({ kind: "card", card: { type: "knowledge_filing", taskId: "t1", title: "经营周报_2026年第44周", ...patch } }, "t20-filing");
    const notice = msg({ kind: "notice", text: "任务已交付。" }, "t20");

    const offered = deriveChat(session([notice, filing({ description: "第 44 周经营周报" })], { status: "closed", taskId: "t1" }), deliveredTask);
    expect(offered.map((i) => i.kind)).toEqual(["notice", "delivered", "filing"]);
    const card = offered[2];
    expect(card?.kind === "filing" && [card.taskId, card.title, card.description, card.active, card.canDraft]).toEqual([
      "t1",
      "经营周报_2026年第44周",
      "第 44 周经营周报",
      true,
      true,
    ]);

    const filed = deriveChat(session([notice, filing({ filed: "kb-1" })], { status: "closed", taskId: "t1" }), deliveredTask).at(-1);
    expect(filed?.kind === "filing" && [filed.filed, filed.active]).toEqual(["kb-1", false]);

    const legacy = deriveChat(session([notice, filing({ draftFailed: true })], { status: "closed", taskId: "t1", readOnly: true }), deliveredTask).at(-1);
    expect(legacy?.kind === "filing" && [legacy.active, legacy.canDraft, legacy.draftFailed]).toEqual([true, false, true]);
  });
});
