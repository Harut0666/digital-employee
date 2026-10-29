// 接缝 1 · 表单字段用途注入：x-purpose: writing_requirement 的字段进入每个判断步骤的上下文。
// 用剧本模型（createScriptedModelProvider）的 .requests 断言模型实际收到了什么。
import { afterEach, describe, expect, test } from "bun:test";
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { createJudgmentRunner, createScriptedModelProvider } from "../../src/agent";
import type { ScriptedRequest, ScriptedTurn } from "../../src/agent";
import { cleanupAll, createHarness } from "./helpers/harness";

afterEach(cleanupAll);

const submit = (arguments_: Record<string, unknown>): ScriptedTurn => ({ toolCalls: [{ name: "submit_result", arguments: arguments_ }] });

/** 模型看到的用户消息（describeContext 把上下文 JSON 放在里面）。 */
function userText(request: ScriptedRequest): string {
  return request.context.messages
    .map((m) => {
      if (m.role !== "user") return "";
      return typeof m.content === "string" ? m.content : m.content.map((c) => (c.type === "text" ? c.text : "")).join("");
    })
    .join("\n");
}

/** 给迷你员工的表单加一个“重点关注区域”写作要求字段。 */
function addWritingRequirementField(employeesDir: string, extra: Record<string, unknown> = { focusRegion: "华东" }): void {
  const properties = {
    period: { type: "object" },
    sales: { type: "boolean" },
    ...extra,
    focusRegion: { type: "string", title: "重点关注区域", "x-purpose": "writing_requirement" },
  };
  writeFileSync(join(employeesDir, "mini", "form.schema.json"), JSON.stringify({ type: "object", properties }));
}

describe("判断步骤上下文注入写作要求", () => {
  test("每个判断步骤都收到标题和取值；计算步骤不注入", async () => {
    const model = createScriptedModelProvider({
      match_causes: [submit({ causes: [] })],
      write_report: [submit({ title: "报告" })],
    });
    const h = await createHarness({ judgment: createJudgmentRunner(), model });
    addWritingRequirementField(h.employeesDir);

    const settled = await h.run({ focusRegion: "华东" });

    expect(settled.state).toBe("awaiting_review");
    const judgeRequests = model.requests.filter((r) => r.stepId === "match_causes" || r.stepId === "write_report");
    expect(judgeRequests.map((r) => r.stepId)).toEqual(["match_causes", "write_report"]);
    for (const request of judgeRequests) {
      const text = userText(request);
      expect(text).toContain("writingRequirements");
      expect(text).toContain("重点关注区域");
      expect(text).toContain("华东");
    }
  });

  test("任务的配置在启动时固定：改配置只对新任务生效", async () => {
    const model = createScriptedModelProvider({
      match_causes: [submit({ causes: [] }), submit({ causes: [] })],
      write_report: [submit({ title: "A" }), submit({ title: "B" })],
    });
    const h = await createHarness({ judgment: createJudgmentRunner(), model });
    // 先建草稿：此时配置副本里还没有“重点关注区域”
    const draft = await h.create();
    addWritingRequirementField(h.employeesDir);

    const submitted = await h.cmd(draft.id, { type: "submit" }, "initiator");
    expect(submitted.ok).toBe(true);
    const settledA = await h.engine.waitForSettled(draft.id);
    expect(settledA.state).toBe("awaiting_review");

    const [requestA] = model.requests.filter((r) => r.stepId === "match_causes");
    expect(userText(requestA!)).not.toContain("重点关注区域");

    // 新任务用改过的配置：上下文里有要求，configHash 也变了
    const settledB = await h.run({ focusRegion: "华东" });
    const requestB = model.requests.filter((r) => r.stepId === "match_causes")[1];
    expect(userText(requestB!)).toContain("重点关注区域");
    expect(userText(requestB!)).toContain("华东");
    expect(settledB.configHash).not.toBe(settledA.configHash);

    // 运行中的任务（已提交、已结束的那一个）的 configHash 保持启动时的值
    const again = await h.engine.get(draft.id);
    expect(again!.configHash).toBe(settledA.configHash);
  });
});
