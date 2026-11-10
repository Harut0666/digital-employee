import { describe, expect, test } from "bun:test";
import type { SessionDetail, SessionMessage } from "../src/api/types";
import type { PastRun } from "../src/view/chat-view";
import { earlierDeliveries, filingOf } from "../src/view/delivery";

let seq = 0;
const filingCard = (taskId: string | undefined, patch: Record<string, unknown> = {}): SessionMessage => {
  seq += 1;
  return { id: `m${seq}`, seq, ts: new Date(Date.parse("2026-11-02T09:00:00Z") + seq * 1000).toISOString(), kind: "card", card: { type: "knowledge_filing", taskId, title: "经营周报", ...patch } };
};

const session = (messages: SessionMessage[], patch: Partial<SessionDetail> = {}): SessionDetail => ({
  id: "s-1",
  workspaceId: "ws-1",
  status: "closed",
  readOnly: false,
  title: "t",
  attachments: [],
  createdAt: "2026-11-02T09:00:00.000Z",
  updatedAt: "2026-11-02T09:10:00.000Z",
  waitingFor: [],
  statusDot: "waiting",
  unread: false,
  archived: false,
  messages,
  taskId: "t2",
  ...patch,
});

describe("filingOf", () => {
  test("取这个任务最近一张入库确认；入库前可以操作，入库后只读", () => {
    const open = filingOf(session([filingCard("t2", { description: "第 44 周经营周报" })]), "t2");
    expect(open && [open.taskId, open.title, open.description, open.active, open.canDraft]).toEqual(["t2", "经营周报", "第 44 周经营周报", true, true]);
    const filed = filingOf(session([filingCard("t2"), filingCard("t2", { filed: "kb-1" })]), "t2");
    expect(filed && [filed.filed, filed.active]).toEqual(["kb-1", false]);
  });

  test("卡片没写任务号时算会话当前任务的；别的任务的不串", () => {
    expect(filingOf(session([filingCard(undefined)]), "t2")?.taskId).toBe("t2");
    expect(filingOf(session([filingCard("t1")]), "t2")).toBeUndefined();
    expect(filingOf(session([filingCard("t2")]), undefined)).toBeUndefined();
  });

  test("只读会话不能请模型起草", () => {
    const view = filingOf(session([filingCard("t2", { draftFailed: true })], { readOnly: true }), "t2");
    expect(view && [view.active, view.canDraft, view.draftFailed]).toEqual([true, false, true]);
  });
});

describe("earlierDeliveries", () => {
  const run = (taskId: string, n: number, state: PastRun["state"], deliverables: PastRun["deliverables"]): PastRun => ({ taskId, run: n, state, deliverables, events: [], steps: [] });

  test("只列早先已交付的任务，带上各自的入库确认", () => {
    const docx = [{ kind: "docx" as const, path: "周报.docx", sha256: "x" }];
    const list = earlierDeliveries(session([filingCard("t1", { description: "第一版" })]), [run("t1", 1, "delivered", docx), run("t0", 0, "terminated", docx), run("tx", 2, "delivered", [])]);
    expect(list.map((d) => [d.taskId, d.run, d.deliverables.length, d.filing?.description])).toEqual([["t1", 1, 1, "第一版"]]);
  });
});
