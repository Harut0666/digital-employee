import { describe, expect, test } from "bun:test";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { setup, tempDir } from "./helpers";

/** 在临时工作区里放一份和 exe 释放出来的一样的公开样例（samples/cases/A-01.json + samples/scenario-a/）。 */
function workspaceWithSample(): string {
  const workspaceDir = tempDir("de-sample-");
  const caseFile = join(workspaceDir, "samples", "cases", "A-01.json");
  mkdirSync(dirname(caseFile), { recursive: true });
  writeFileSync(
    caseFile,
    JSON.stringify({
      id: "A-01",
      title: "正常任务（含纪要与数据不一致）",
      employee: "weekly-report",
      form: { period: { year: 2026, week: 44 } },
      attachments: [
        { name: "销售明细.xlsx", path: "../scenario-a/销售明细.xlsx" },
        { name: "周会纪要_2026W44.docx", path: "../scenario-a/周会纪要_2026W44.docx" },
      ],
      steps: [],
    }),
  );
  const scenario = join(workspaceDir, "samples", "scenario-a");
  mkdirSync(scenario, { recursive: true });
  writeFileSync(join(scenario, "销售明细.xlsx"), "PK\u0003\u0004fake-xlsx");
  writeFileSync(join(scenario, "周会纪要_2026W44.docx"), "PK\u0003\u0004fake-docx");
  return workspaceDir;
}

describe("GET /api/samples", () => {
  test("工作区没有释放样例时返回空列表（不是错误）", async () => {
    const { call } = setup();
    const res = await call("/api/samples");
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ samples: [] });
  });

  test("有样例时给出发起表单与文件清单", async () => {
    const { call } = setup({ workspaceDir: workspaceWithSample() });
    const body = (await (await call("/api/samples")).json()) as {
      samples: { id: string; employee: string; title: string; form: unknown; files: { name: string }[] }[];
    };
    expect(body.samples).toHaveLength(1);
    const sample = body.samples[0]!;
    expect(sample.id).toBe("scenario-a");
    expect(sample.employee).toBe("weekly-report");
    expect(sample.title).toContain("正常任务");
    expect(sample.form).toEqual({ period: { year: 2026, week: 44 } });
    expect(sample.files.map((f) => f.name)).toEqual(["销售明细.xlsx", "周会纪要_2026W44.docx"]);
  });
});

describe("POST /api/samples/:id/tasks", () => {
  test("发起人一键建草稿任务，附件指向工作区里的样例文件", async () => {
    const workspaceDir = workspaceWithSample();
    const { call, engine } = setup({ workspaceDir });
    const res = await call("/api/samples/scenario-a/tasks", { method: "POST", headers: { "x-role": "initiator" } });
    expect(res.status).toBe(201);
    const snap = (await res.json()) as { id: string; state: string; employee: string };
    expect(snap.state).toBe("draft");
    expect(snap.employee).toBe("weekly-report");
    expect(engine.created).toHaveLength(1);
    const created = engine.created[0]!;
    expect(created.form).toEqual({ period: { year: 2026, week: 44 } });
    expect(created.attachments.map((a) => a.name)).toEqual(["销售明细.xlsx", "周会纪要_2026W44.docx"]);
    expect(created.attachments[0]!.sourcePath).toBe(join(workspaceDir, "samples", "scenario-a", "销售明细.xlsx"));
  });

  test("缺角色或非发起人时 403", async () => {
    const { call } = setup({ workspaceDir: workspaceWithSample() });
    const noRole = await call("/api/samples/scenario-a/tasks", { method: "POST" });
    expect(noRole.status).toBe(403);
    const reviewer = await call("/api/samples/scenario-a/tasks", { method: "POST", headers: { "x-role": "reviewer" } });
    expect(reviewer.status).toBe(403);
  });

  test("未知样例或工作区里没有样例时 404", async () => {
    const withSample = await setup({ workspaceDir: workspaceWithSample() }).call("/api/samples/unknown/tasks", {
      method: "POST",
      headers: { "x-role": "initiator" },
    });
    expect(withSample.status).toBe(404);
    const withoutSample = await setup().call("/api/samples/scenario-a/tasks", {
      method: "POST",
      headers: { "x-role": "initiator" },
    });
    expect(withoutSample.status).toBe(404);
  });
});
