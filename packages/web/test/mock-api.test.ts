import { describe, expect, test } from "bun:test";
import { createMockApi } from "../src/api/mock";
import type { UiRole } from "../src/api/types";

const asRole = (initial: UiRole) => {
  let role = initial;
  return { api: createMockApi(() => role), setRole: (r: UiRole) => (role = r) };
};

describe("模拟数据模式", () => {
  test("工具故障的快照给出操作、等待角色和暂停说明", async () => {
    const { api } = asRole("initiator");
    const snapshot = await api.getTask("mock-44");
    expect(snapshot?.actions.map((a) => [a.command, a.roles])).toEqual([
      ["retry", ["initiator", "operator"]],
      ["terminate", ["operator"]],
    ]);
    expect(snapshot?.waitingFor).toEqual(["initiator", "operator"]);
    expect(snapshot?.pause?.title).toBe("工具调用失败");
    expect(snapshot?.pause?.resolution).toContain("重试");
  });

  test("任务列表给出状态文字和等待角色", async () => {
    const { api } = asRole("initiator");
    const list = await api.listTasks();
    const byId = (id: string) => list.find((t) => t.id === id);
    expect(byId("mock-45")).toMatchObject({ statusText: "缺少本周纪要", waitingFor: ["initiator"] });
    expect(byId("mock-43")).toMatchObject({ statusText: "待审核", waitingFor: ["reviewer"] });
    expect(byId("mock-42")).toMatchObject({ statusText: "已交付", waitingFor: [] });
  });

  test("发起人能重试工具故障，审核人被拒绝", async () => {
    const { api, setRole } = asRole("reviewer");
    const denied = await api.command("mock-44", { type: "retry" });
    expect(denied).toMatchObject({ ok: false, error: "forbidden_role" });
    setRole("initiator");
    const ok = await api.command("mock-44", { type: "retry" });
    expect(ok.ok).toBe(true);
  });

  test("发起人不能终止工具故障的任务；当前没有的操作按非法流转拒绝", async () => {
    const { api } = asRole("initiator");
    expect(await api.command("mock-44", { type: "terminate" })).toMatchObject({ ok: false, error: "forbidden_role" });
    expect(await api.command("mock-44", { type: "approve" })).toMatchObject({ ok: false, error: "illegal_transition" });
  });

  test("模型不可用的种子任务发起人可重试", async () => {
    const { api } = asRole("initiator");
    const snapshot = await api.getTask("mock-47");
    expect(snapshot?.pause?.code).toBe("model_unavailable");
    expect(snapshot?.actions[0]).toMatchObject({ command: "retry", roles: ["initiator", "operator"] });
  });

  test("草稿可以放弃", async () => {
    const { api } = asRole("initiator");
    const draft = await api.createTask({ employee: "weekly-report", form: {}, uploads: [] });
    expect(draft.actions.map((a) => a.label)).toEqual(["提交", "放弃草稿"]);
    expect(draft.waitingFor).toEqual(["initiator"]);
    const res = await api.command(draft.id, { type: "terminate" });
    expect(res.ok && res.snapshot.state).toBe("terminated");
  });
});

describe("模拟数据模式的会话", () => {
  const cards = (messages: { card?: { type: string } }[]) => messages.flatMap((m) => (m.card ? [m.card.type] : []));

  test("说一句话 → 确认员工 → 表单草稿 → 上传 → 提交，会话绑定任务", async () => {
    const { api } = asRole("initiator");
    const created = await api.createSession();
    expect(created.status).toBe("reception");

    const said = await api.sendMessage(created.id, "帮我出第 44 周的周报");
    expect(said.ok && cards(said.messages)).toEqual(["employee_confirm"]);
    const proposal = said.ok ? said.messages.find((m) => m.card)?.card : undefined;
    expect(proposal?.proposed).toBe("weekly-report");

    const confirmed = await api.confirmEmployee(created.id, "weekly-report");
    expect(confirmed.ok && [confirmed.session.status, cards(confirmed.messages)]).toEqual(["drafting", ["form"]]);
    // 草稿取自人说过的周次
    expect(confirmed.ok && confirmed.session.draft).toEqual({ period: { year: 2026, week: 44 } });

    const uploaded = await api.uploadAttachments(created.id, [new File(["x"], "销售明细.xlsx"), new File(["y"], "周会纪要_2026W44.docx")]);
    expect(uploaded.ok && uploaded.session.attachments.map((a) => a.name)).toEqual(["销售明细.xlsx", "周会纪要_2026W44.docx"]);

    const submitted = await api.submitSession(created.id, { period: { year: 2026, week: 44 }, remark: "" });
    expect(submitted.ok && submitted.session.status).toBe("task");
    const taskId = submitted.ok ? submitted.session.taskId : undefined;
    expect((await api.getTask(taskId!))?.state).toBe("admission");

    const detail = await api.getSession(created.id);
    expect(cards(detail.messages)).toContain("form");
    expect(detail.messages.at(-1)?.card).toMatchObject({ type: "form", submitted: true });
  });

  test("开工清单：必需项不齐不能开工，可跳过项由人确认并写进表单", async () => {
    const { api, setRole } = asRole("initiator");
    const created = await api.createSession();
    await api.confirmEmployee(created.id, "weekly-report");
    const status = async () => (await api.getSession(created.id)).checklist?.items.map((i) => `${i.id}=${i.status}`).join(" ");
    expect(await status()).toBe("period=missing sales=missing minutes=missing");

    const refused = await api.submitSession(created.id, { period: { year: 2026, week: 44 } });
    expect(refused).toMatchObject({ ok: false, error: "checklist_incomplete" });
    expect(!refused.ok && refused.message).toContain("销售明细");

    await api.uploadAttachments(created.id, [new File(["x"], "销售明细.xlsx")]);
    setRole("reviewer");
    expect(await api.skipChecklistItem(created.id, "minutes")).toMatchObject({ ok: false, error: "forbidden_role" });
    setRole("initiator");
    expect(await api.skipChecklistItem(created.id, "sales")).toMatchObject({ ok: false, error: "invalid_input" });
    expect((await api.skipChecklistItem(created.id, "minutes")).ok).toBe(true);
    expect(await status()).toBe("period=missing sales=done minutes=skipped");

    const submitted = await api.submitSession(created.id, { period: { year: 2026, week: 44 } });
    const taskId = submitted.ok ? submitted.session.taskId : undefined;
    expect((await api.getTask(taskId!))?.form).toMatchObject({ allowMissingMinutes: true });
  });

  test("只有发起人能确认员工和提交；没确认员工不能提交", async () => {
    const { api, setRole } = asRole("initiator");
    const created = await api.createSession();
    expect(await api.submitSession(created.id)).toMatchObject({ ok: false, error: "employee_required" });
    setRole("reviewer");
    expect(await api.confirmEmployee(created.id, "weekly-report")).toMatchObject({ ok: false, error: "forbidden_role" });
  });

  test("会话列表合并新会话和种子任务的只读会话，任务只出现一次", async () => {
    const { api } = asRole("initiator");
    const created = await api.createSession();
    const list = await api.listSessions();
    expect(list[0]?.id).toBe(created.id);
    const legacy = list.find((s) => s.taskId === "mock-44");
    expect(legacy).toMatchObject({ id: "task-mock-44", readOnly: true, status: "task", waitingFor: ["initiator", "operator"] });
    const detail = await api.getSession("task-mock-44");
    expect(cards(detail.messages)).toContain("pause");
    expect(detail.messages.find((m) => m.card?.type === "pause")?.card?.actions?.map((a) => a.command)).toEqual(["retry", "terminate"]);
    expect((await api.preflight()).ok).toBe(true);
  });
});
