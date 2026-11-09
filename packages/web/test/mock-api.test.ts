import { describe, expect, test } from "bun:test";
import { createMockApi } from "../src/api/mock";
import type { MockWebApi, SessionAgentEvent, UiRole, WebApi } from "../src/api/types";

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

  /** 收一个会话的事件，等到这一轮说完（收到 turn_end） */
  async function turnOf(api: { subscribeSession(id: string, onEvent: (e: SessionAgentEvent) => void): () => void }, id: string, turn: number) {
    const events: SessionAgentEvent[] = [];
    const stop = api.subscribeSession(id, (e) => events.push(e));
    const deadline = Date.now() + 3000;
    while (!events.some((e) => e.type === "turn_end" && e.turn === turn)) {
      if (Date.now() > deadline) throw new Error(`等不到第 ${turn} 轮的结束：${JSON.stringify(events)}`);
      await new Promise((resolve) => setTimeout(resolve, 5));
    }
    stop();
    return events;
  }

  test("说一句话 → 确认员工 → 表单草稿 → 上传 → 提交，会话绑定任务", async () => {
    const { api } = asRole("initiator");
    const created = await api.createSession();
    expect(created.status).toBe("reception");

    // 接口立即受理；说的话在事件流里逐字出现，说完才写进消息
    const said = await api.sendMessage(created.id, "帮我出第 44 周的周报");
    expect(said).toEqual({ ok: true, turn: 1 });
    const events = await turnOf(api, created.id, 1);
    expect(events.map((e) => e.type)).toContain("text_delta");
    expect(events.at(-1)).toMatchObject({ type: "turn_end", reason: "completed" });
    // 先订阅、后发消息：最早的字可能已经先到，这里只核对收得全的部分\n    const streamed = events.filter((e) => e.type === "text_delta").map((e) => e.text).join("");\n    expect("这项工作适合由经营周报员工接手，请确认。".endsWith(streamed)).toBe(true);\n    expect(streamed.length).toBeGreaterThan(0);
    expect(events.filter((e) => e.type === "tool_start").map((e) => e.tool)).toEqual(["propose_employee"]);

    const first = await api.getSession(created.id);
    expect(cards(first.messages)).toEqual(["employee_confirm"]);
    const proposal = first.messages.find((m) => m.card)?.card;
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

  test("建会话时带模型、任务交付后换模型（T45）：和 Go 的规则一致", async () => {
    const { api, setRole } = asRole("initiator");
    // 带模型：从第一句话起就是它；不在连接列表里的模型被拒绝
    const created = await api.createSession(undefined, { connection: "DeepSeek", model: "deepseek-reasoner" });
    expect((await api.getSession(created.id)).model).toEqual({ connection: "DeepSeek", model: "deepseek-reasoner" });
    await expect(api.createSession(undefined, { connection: "DeepSeek", model: "nope" })).rejects.toThrow("不在连接");
    // 旧任务推导出的只读会话仍拒绝；只有发起人能选
    expect(await api.selectModel("task-mock-42", "DeepSeek", "deepseek-chat")).toMatchObject({ ok: false, error: "session_read_only" });
    setRole("reviewer");
    expect(await api.selectModel(created.id, "DeepSeek", "deepseek-chat")).toMatchObject({ ok: false, error: "forbidden_role" });

    // 交付之后：只改会话的选择，不写任务事件
    setRole("initiator");
    await api.confirmEmployee(created.id, "weekly-report");
    await api.uploadAttachments(created.id, [new File(["x"], "销售明细.csv"), new File(["y"], "周会纪要_2026W44.docx")]);
    const submitted = await api.submitSession(created.id, { period: { year: 2026, week: 44 } });
    const taskId = submitted.ok ? submitted.session.taskId : undefined;
    // 只有 CSV 销售明细、缺门店和目标维表，准入校验（约 0.6 秒）后任务停在待发起人补充；终止它就算结束了（不用等模拟任务自己走到待审核）
    for (let waited = 0; (await api.getTask(taskId!))?.state !== "awaiting_initiator"; waited += 50) {
      if (waited > 5000) throw new Error(`准入校验没有结束：${(await api.getTask(taskId!))?.state}`);
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
    expect((await api.command(taskId!, { type: "terminate" })).ok).toBe(true);
    expect((await api.getTask(taskId!))?.state).toBe("terminated");
    const eventsBefore = (await api.getEvents(taskId!)).length;
    expect((await api.selectModel(created.id, "DeepSeek", "deepseek-chat")).ok).toBe(true);
    expect((await api.getEvents(taskId!)).length).toBe(eventsBefore);
    expect((await api.getSession(created.id)).model).toEqual({ connection: "DeepSeek", model: "deepseek-chat" });
  });

  test("正在说的时候发的话排进这一轮；点停止，已说的部分以“已中断”保存", async () => {
    const { api } = asRole("initiator");
    const created = await api.createSession();
    const first = await api.sendMessage(created.id, "出周报");
    expect(first).toEqual({ ok: true, turn: 1 });
    // 还没说完就再发一句、再点停止
    const events = turnOf(api, created.id, 1);
    expect(await api.sendMessage(created.id, "顺便看看华南")).toEqual({ ok: true, turn: 1 });
    await new Promise((resolve) => setTimeout(resolve, 12));
    expect(await api.stopTurn(created.id)).toMatchObject({ ok: true, stopped: true, turn: 1 });
    const seen = await events;
    expect(seen.at(-1)).toMatchObject({ type: "turn_end", reason: "stopped" });
    const detail = await api.getSession(created.id);
    const last = detail.messages.at(-1);
    expect(last?.kind).toBe("assistant");
    expect(last?.text).toContain("（已中断）");
    // 没在说的时候停止什么也不做
    expect(await api.stopTurn(created.id)).toEqual({ ok: true, stopped: false });
  });

  test("只有发起人能确认员工和提交；没确认员工不能提交", async () => {
    const { api, setRole } = asRole("initiator");
    const created = await api.createSession();
    expect(await api.submitSession(created.id)).toMatchObject({ ok: false, error: "employee_required" });
    setRole("reviewer");
    expect(await api.confirmEmployee(created.id, "weekly-report")).toMatchObject({ ok: false, error: "forbidden_role" });
  });

  test("侧栏：会话带状态点，旧任务会话归到我的工作区；记录 tab 按状态分组", async () => {
    const { api: web } = asRole("initiator");
    const api = web as MockWebApi;
    await api.createSession();
    const summaries = await api.all();
    const legacy = summaries.find((s) => s.id === "task-mock-43");
    expect(legacy).toMatchObject({ workspaceId: "ws-demo", workspaceName: "我的工作区", statusDot: "idle", archived: false });
    expect(summaries.every((s) => ["waiting", "running", "done", "idle"].includes(s.statusDot))).toBe(true);
    const items = await api.records("initiator");
    const group = (key: string) => items.filter((i) => i.group === key).map((i) => i.taskId);
    expect(group("review")).toContain("mock-43");
    expect(group("delivered")).toContain("mock-42");
    expect(group("supplement")).toContain("mock-45");
    expect(items.find((i) => i.taskId === "mock-43")?.workspaceName).toBe("我的工作区");
  });

  test("会话有未结束的任务时拒绝再开新任务并说明原因；记录带“第几次运行”", async () => {
    const { api: web } = asRole("initiator");
    const api = web as MockWebApi;
    const created = await api.createSession();
    await api.confirmEmployee(created.id, "weekly-report");
    await api.uploadAttachments(created.id, [new File(["x"], "销售明细.xlsx"), new File(["y"], "周会纪要_2026W44.docx")]);
    const form = { period: { year: 2026, week: 44 }, remark: "" };
    const first = await api.submitSession(created.id, form);
    const firstId = first.ok ? first.session.taskId! : "";
    expect(first.ok && first.session.taskIds).toEqual([firstId]);

    const again = await api.submitSession(created.id, form);
    expect(again).toMatchObject({ ok: false, error: "session_has_task" });
    expect(!again.ok && again.message).toContain("还没结束");
    expect(!again.ok && again.message).toContain(firstId);

    const rows = (await api.records("initiator")).filter((r) => r.sessionId === created.id);
    expect(rows.map((r) => [r.taskId, r.runNumber])).toEqual([[firstId, 1]]);
  });
  test("会话可以重命名、归档：归档后默认不列，带 archived 才列", async () => {
    const { api } = asRole("initiator");
    const created = await api.createSession();
    expect((await api.updateSession(created.id, { title: "第 44 周华东周报" })).title).toBe("第 44 周华东周报");
    await api.updateSession(created.id, { archived: true });
    expect((await api.listSessions()).some((s) => s.id === created.id)).toBe(false);
    expect((await api.listSessions(undefined, { archived: true })).find((s) => s.id === created.id)?.archived).toBe(true);
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

describe("模拟数据模式的权限模式（T48）", () => {
  /** 等到会话里出现满足条件的消息 */
  async function waitFor(api: Pick<WebApi, "getSession">, id: string, found: (messages: Awaited<ReturnType<WebApi["getSession"]>>["messages"]) => boolean) {
    const deadline = Date.now() + 3000;
    for (;;) {
      const detail = await api.getSession(id);
      if (found(detail.messages)) return detail;
      if (Date.now() > deadline) throw new Error(`等不到：${JSON.stringify(detail.messages)}`);
      await new Promise((resolve) => setTimeout(resolve, 5));
    }
  }
  const pending = (messages: { card?: { type: string; pending?: boolean } }[]) => messages.some((m) => m.card?.type === "permission" && m.card.pending);
  const settled = (messages: { kind: string }[]) => messages.some((m) => m.kind === "assistant");

  async function drafting(asRoleApi = asRole("initiator")) {
    const created = await asRoleApi.api.createSession();
    await asRoleApi.api.confirmEmployee(created.id, "weekly-report");
    return { ...asRoleApi, id: created.id };
  }

  test("新会话是询问；只有发起人能选；选了留一条说明并写进会话", async () => {
    const { api, setRole, id } = await drafting();
    expect((await api.getSession(id)).permissionMode).toBe("ask");
    setRole("reviewer");
    expect(await api.setPermissionMode(id, "yolo")).toMatchObject({ ok: false, error: "forbidden_role" });
    setRole("initiator");
    expect(await api.setPermissionMode(id, "god" as never)).toMatchObject({ ok: false, error: "invalid_input" });
    const set = await api.setPermissionMode(id, "accept_edits");
    expect(set.ok && set.session.permissionMode).toBe("accept_edits");
    const detail = await api.getSession(id);
    expect(detail.messages.filter((m) => m.kind === "permission_mode_set").map((m) => m.mode)).toEqual(["accept_edits"]);
    expect((await api.createSession()).permissionMode).toBe("ask");
  });

  test("询问：写文件先弹确认卡，允许后写出并留一行写文件的工具行", async () => {
    const { api, id } = await drafting();
    await api.sendMessage(id, "把要点整理成一个文件");
    const asked = await waitFor(api, id, pending);
    const card = asked.messages.find((m) => m.card?.type === "permission")!.card!;
    expect(card).toMatchObject({ action: "write_file", actionLabel: "写入文件", pending: true });
    expect(card.choices?.map((c) => c.decision)).toEqual(["allow_once", "always_allow", "deny"]);
    expect((await api.decidePermission(id, card.requestId!, "allow_once")).ok).toBe(true);
    expect(await api.decidePermission(id, card.requestId!, "deny")).toMatchObject({ ok: false, error: "permission_settled" });
    const done = await waitFor(api, id, settled);
    expect(done.messages.find((m) => m.card?.type === "permission")?.card).toMatchObject({ pending: false, decision: "allow_once" });
    const written = done.messages.find((m) => m.kind === "tool_called" && m.tool === "write_file");
    expect(written?.result).toMatchObject({ action: "created" });
  });

  test("拒绝：不写文件，留下拒绝；本会话总是允许之后同类不再问", async () => {
    const { api, id } = await drafting();
    await api.sendMessage(id, "把要点整理成一个文件");
    const first = (await waitFor(api, id, pending)).messages.find((m) => m.card?.type === "permission")!.card!;
    await api.decidePermission(id, first.requestId!, "deny");
    const denied = await waitFor(api, id, settled);
    expect(denied.messages.some((m) => m.kind === "tool_denied" && m.reason?.startsWith("用户拒绝"))).toBe(true);
    expect(denied.messages.some((m) => m.kind === "tool_called" && m.tool === "write_file")).toBe(false);

    await api.sendMessage(id, "再把要点保存成文件");
    const again = await waitFor(api, id, (messages) => messages.filter((m) => m.card?.type === "permission" && m.card.pending).length === 1);
    const second = again.messages.filter((m) => m.card?.type === "permission").at(-1)!.card!;
    await api.decidePermission(id, second.requestId!, "always_allow");
    await waitFor(api, id, (messages) => messages.filter((m) => m.kind === "assistant").length === 2);

    // 同一会话里下一次不再问，留一条放行说明
    await api.sendMessage(id, "还要写一个文件");
    const third = await waitFor(api, id, (messages) => messages.filter((m) => m.kind === "assistant").length === 3);
    expect(third.messages.filter((m) => m.card?.type === "permission")).toHaveLength(2);
    expect(third.messages.some((m) => m.kind === "permission_allowed")).toBe(true);
    // 换模式清空
    await api.setPermissionMode(id, "ask");
    await api.sendMessage(id, "最后再写一个文件");
    await waitFor(api, id, pending);
  });

  test("计划：直接拒绝；接受编辑：直接写并留痕", async () => {
    const plan = await drafting();
    await plan.api.setPermissionMode(plan.id, "plan");
    await plan.api.sendMessage(plan.id, "把要点整理成一个文件");
    const planned = await waitFor(plan.api, plan.id, settled);
    expect(planned.messages.some((m) => m.kind === "tool_denied" && m.reason?.includes("计划模式"))).toBe(true);
    expect(planned.messages.some((m) => m.card?.type === "permission")).toBe(false);

    const accept = await drafting();
    await accept.api.setPermissionMode(accept.id, "accept_edits");
    await accept.api.sendMessage(accept.id, "把要点整理成一个文件");
    const written = await waitFor(accept.api, accept.id, settled);
    expect(written.messages.some((m) => m.kind === "permission_allowed")).toBe(true);
    expect(written.messages.some((m) => m.kind === "tool_called" && m.tool === "write_file")).toBe(true);
  });

  test("等确认时点停止：确认卡记为已取消，文件没写", async () => {
    const { api, id } = await drafting();
    await api.sendMessage(id, "把要点整理成一个文件");
    await waitFor(api, id, pending);
    expect(await api.stopTurn(id)).toMatchObject({ ok: true, stopped: true });
    const stopped = await waitFor(api, id, (messages) => messages.some((m) => m.card?.type === "permission" && !m.card.pending));
    expect(stopped.messages.find((m) => m.card?.type === "permission")?.card).toMatchObject({ pending: false, decision: "cancelled" });
    expect(stopped.messages.some((m) => m.kind === "tool_called" && m.tool === "write_file" && m.result && typeof m.result === "object")).toBe(false);
  });
});