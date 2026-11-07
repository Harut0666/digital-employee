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
