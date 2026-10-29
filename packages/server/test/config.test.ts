// 员工配置页后端（/api/employees/:id/config）：读写、校验、版本与回滚。
// 请求级测试：用 app.handle() 打真实路由，断言状态码、响应体与磁盘上的文件。
import { afterEach, describe, expect, test } from "bun:test";
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type {
  ConfigDiff,
  ConfigVersion,
  EmployeeConfigResponse,
  RestoreConfigResponse,
  SaveConfigResponse,
} from "@de/core/contract";
import { createApp } from "../src/index";
import { createFakeEngine } from "./fake-engine";

const repoRoot = join(import.meta.dir, "..", "..", "..");
const employeeSource = join(repoRoot, "employees", "weekly-report");
const roots: string[] = [];

function setup() {
  const root = mkdtempSync(join(tmpdir(), "de-config-route-"));
  roots.push(root);
  const employeesDir = join(root, "employees");
  const workspaceDir = join(root, "workspace");
  mkdirSync(employeesDir, { recursive: true });
  mkdirSync(workspaceDir, { recursive: true });
  const employeeDir = join(employeesDir, "weekly-report");
  cpSync(employeeSource, employeeDir, { recursive: true });
  const app = createApp({ engine: createFakeEngine(), employeesDir, workspaceDir });
  const call = (path: string, init?: RequestInit) => app.handle(new Request(`http://localhost${path}`, init));
  const put = (path: string, body: unknown) =>
    call(path, { method: "PUT", headers: { "content-type": "application/json" }, body: JSON.stringify(body) });
  const config = async () => (await (await call("/api/employees/weekly-report/config")).json()) as EmployeeConfigResponse;
  return { root, call, put, config, employeeDir, workspaceDir };
}

function read(dir: string, rel: string): string {
  return readFileSync(join(dir, rel), "utf8");
}

function versionsDir(workspaceDir: string): string {
  return join(workspaceDir, "config-versions", "weekly-report");
}

function versionIds(workspaceDir: string): string[] {
  const dir = versionsDir(workspaceDir);
  return existsSync(dir) ? readdirSync(dir).sort() : [];
}

/** GET 返回的原始 YAML 解析结果（不带 zod 默认值），正是表单页编辑的对象。 */
async function workflowData(config: () => Promise<EmployeeConfigResponse>): Promise<Record<string, any>> {
  const body = await config();
  return structuredClone(body.files.find((f) => f.path === "workflow.yaml")!.parsed) as Record<string, any>;
}

afterEach(() => {
  for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true });
});

describe("GET /api/employees/:id/config", () => {
  test("返回每个文件的原文与解析结果，以及加载后的配置和派生视图", async () => {
    const { call } = setup();
    const res = await call("/api/employees/weekly-report/config");
    expect(res.status).toBe(200);
    const body = (await res.json()) as EmployeeConfigResponse;

    expect(body.id).toBe("weekly-report");
    expect(body.name).toBe("经营周报员工");
    expect(body.error).toBeUndefined();
    expect(body.hash).toMatch(/^[0-9a-f]{64}$/);
    expect(body.config!.workflow.steps.map((s) => s.id)).toContain("write_report");
    expect(body.config!.qc.builtin.anomaly_coverage.threshold).toBe(0.1);

    const paths = body.files.map((f) => f.path);
    expect(paths).toEqual(expect.arrayContaining(["SKILL.md", "workflow.yaml", "qc.yaml", "form.schema.json", "form.ui.json", "frontdesk.md"]));
    const workflow = body.files.find((f) => f.path === "workflow.yaml")!;
    expect(workflow.kind).toBe("yaml");
    expect(workflow.content).toContain("# 经营周报员工 · 工作步骤");
    expect((workflow.parsed as { id: string }).id).toBe("weekly-report");
    const prompt = body.files.find((f) => f.path === "prompts/write_report.md")!;
    expect(prompt.kind).toBe("markdown");
    expect(prompt.content.length).toBeGreaterThan(0);
    const schema = body.files.find((f) => f.path === "schemas/write_report.json")!;
    expect(schema.kind).toBe("json");
    expect((schema.parsed as { type: string }).type).toBe("object");

    // 派生视图：SKILL.md 三个文本框、前台 Agent 只读工具、可绑定的参数与规则模板
    expect(body.views.skill.duties).toContain("青禾烘焙运营部");
    expect(body.views.frontdesk.tools).toEqual(["query_task"]);
    expect(body.views.formParams.map((p) => p.name)).toContain("period");
    expect(body.views.qcTemplates).toHaveLength(6);
    expect(body.versions).toEqual([]);
  });

  test("员工不存在 404；id 不合法 400", async () => {
    const { call } = setup();
    expect((await call("/api/employees/nope/config")).status).toBe(404);
    const bad = await call("/api/employees/Bad_ID/config");
    expect(bad.status).toBe(400);
    expect(((await bad.json()) as { error: string }).error).toBe("invalid_input");
  });

  test("源文件损坏时仍返回原文与逐文件错误，方便用“查看源文件”修", async () => {
    const { call, employeeDir } = setup();
    writeFileSync(join(employeeDir, "workflow.yaml"), "steps: [ ][");
    const res = await call("/api/employees/weekly-report/config");
    expect(res.status).toBe(200);
    const body = (await res.json()) as EmployeeConfigResponse;
    expect(body.error).toBeDefined();
    expect(body.config).toBeUndefined();
    const workflow = body.files.find((f) => f.path === "workflow.yaml")!;
    expect(workflow.error).toBeDefined();
    expect(workflow.content).toBe("steps: [ ][");
  });
});

describe("PUT /api/employees/:id/config/:file 校验", () => {
  test("非法 YAML 返回 400 与逐条错误，文件没被改动", async () => {
    const { put, employeeDir, workspaceDir } = setup();
    const before = read(employeeDir, "workflow.yaml");
    const res = await put("/api/employees/weekly-report/config/workflow.yaml", { content: "steps: [ ][" });
    expect(res.status).toBe(400);
    const body = (await res.json()) as { ok: false; error: string; message: string; errors: string[] };
    expect(body.ok).toBe(false);
    expect(body.error).toBe("invalid_input");
    expect(body.errors.length).toBeGreaterThan(0);
    expect(body.errors.join("\n")).toContain("YAML");
    expect(read(employeeDir, "workflow.yaml")).toBe(before);
    expect(versionIds(workspaceDir)).toEqual([]);
  });

  test("违反契约返回 400，文件没被改动，也不产生版本", async () => {
    const { put, config, employeeDir, workspaceDir } = setup();
    const before = read(employeeDir, "workflow.yaml");
    const data = await workflowData(config);
    data.limits.task.modelCalls = 0;
    const res = await put("/api/employees/weekly-report/config/workflow.yaml", { data });
    expect(res.status).toBe(400);
    expect(((await res.json()) as { errors: string[] }).errors.join("\n")).toContain("modelCalls");
    expect(read(employeeDir, "workflow.yaml")).toBe(before);
    expect(versionIds(workspaceDir)).toEqual([]);
  });

  test("跨文件问题（提示词文件不存在）在写盘后回滚，文件没被改动、不留版本", async () => {
    const { put, config, employeeDir, workspaceDir } = setup();
    const before = read(employeeDir, "workflow.yaml");
    const data = await workflowData(config);
    data.steps.find((s: any) => s.id === "write_report").prompt = "prompts/nope.md";
    const res = await put("/api/employees/weekly-report/config/workflow.yaml", { data });
    expect(res.status).toBe(400);
    expect(((await res.json()) as { errors: string[] }).errors.join("\n")).toContain("prompts/nope.md");
    expect(read(employeeDir, "workflow.yaml")).toBe(before);
    expect(versionIds(workspaceDir)).toEqual([]);
  });

  test("form.schema.json 的 x-purpose 未知时 400；合法时保存成功", async () => {
    const { put, config, employeeDir } = setup();
    const before = read(employeeDir, "form.schema.json");
    const bad = JSON.parse(before) as Record<string, any>;
    bad.properties.focusRegion = { type: "string", title: "重点关注区域", "x-purpose": "requirement" };
    const rejected = await put("/api/employees/weekly-report/config/form.schema.json", { data: bad });
    expect(rejected.status).toBe(400);
    expect(((await rejected.json()) as { errors: string[] }).errors.join("\n")).toContain("x-purpose");
    expect(read(employeeDir, "form.schema.json")).toBe(before);

    const good = JSON.parse(before) as Record<string, any>;
    good.properties.focusRegion = { type: "string", title: "重点关注区域", "x-purpose": "writing_requirement" };
    expect((await put("/api/employees/weekly-report/config/form.schema.json", { data: good })).status).toBe(200);
    const after = await config();
    expect((after.config!.formSchema as any).properties.focusRegion["x-purpose"]).toBe("writing_requirement");
  });

  test("路径穿越与未知文件一律拒绝", async () => {
    const { put } = setup();
    for (const path of ["..%2F..%2Fpackage.json", "routes.yaml", "prompts/notes.txt", "C%3A%2Fwindows%2Fx"]) {
      const res = await put(`/api/employees/weekly-report/config/${path}`, { content: "x" });
      expect(res.status).toBe(400);
    }
  });

  test("员工不存在时 PUT 404", async () => {
    const { put } = setup();
    expect((await put("/api/employees/nope/config/workflow.yaml", { content: "x" })).status).toBe(404);
  });
});

describe("保存与版本", () => {
  test("合法保存写回文件、保留注释、多一个版本；GET 能看到新值", async () => {
    const { put, call, config, employeeDir, workspaceDir } = setup();
    const data = await workflowData(config);
    data.limits.task.modelCalls = 42;
    data.steps.find((s: any) => s.id === "write_report").limits.rounds = 4;

    const res = await put("/api/employees/weekly-report/config/workflow.yaml", { data });
    expect(res.status).toBe(200);
    const body = (await res.json()) as SaveConfigResponse;
    expect(body.ok).toBe(true);
    expect(body.path).toBe("workflow.yaml");
    expect(body.version.id).toMatch(/^\d{8}T\d{9}Z-[0-9a-f]{8}$/);
    expect(versionIds(workspaceDir)).toEqual([body.version.id]);

    const saved = read(employeeDir, "workflow.yaml");
    expect(saved).toContain("# 经营周报员工 · 工作步骤");
    expect(saved).toContain("# 只在重做时执行（首轮记为跳过）");
    // 哈希随配置变化，GET 立即反映
    const after = (await (await call("/api/employees/weekly-report/config")).json()) as EmployeeConfigResponse;
    expect(after.hash).not.toBe(undefined);
    expect(after.config!.workflow.limits.task.modelCalls).toBe(42);
    const writeStep = after.config!.workflow.steps.find((s) => s.id === "write_report")!;
    expect(writeStep.kind === "judge" ? writeStep.limits.rounds : undefined).toBe(4);
    expect(after.versions).toHaveLength(1);
  });

  test("SKILL.md 三个文本框写回：改岗位职责不影响其余章节", async () => {
    const { put, config, employeeDir } = setup();
    const before = await config();
    const res = await put("/api/employees/weekly-report/config/SKILL.md", {
      data: { ...before.views.skill, duties: "你是测试岗位职责。\n" },
    });
    expect(res.status).toBe(200);
    const saved = read(employeeDir, "SKILL.md");
    expect(saved).toContain("你是测试岗位职责。");
    expect(saved).toContain("## 最重要的规则");
    expect(saved).toContain("## 注意事项");
  });

  test("版本列表、差异与回滚：回滚内容一致且多出一个新版本", async () => {
    const { put, call, config, employeeDir, workspaceDir } = setup();
    const data = await workflowData(config);
    data.limits.task.modelCalls = 42;
    const first = (await (await put("/api/employees/weekly-report/config/workflow.yaml", { data })).json()) as SaveConfigResponse;
    data.limits.task.modelCalls = 45;
    const second = (await (await put("/api/employees/weekly-report/config/workflow.yaml", { data })).json()) as SaveConfigResponse;

    const list = (await (await call("/api/employees/weekly-report/config/versions")).json()) as ConfigVersion[];
    expect(list.map((v) => v.id)).toEqual([second.version.id, first.version.id]);

    // 与最初版本（保存前，modelCalls=30）的差异
    const diff = (await (await call(`/api/employees/weekly-report/config/versions/${first.version.id}/diff`)).json()) as ConfigDiff;
    expect(diff.hasChanges).toBe(true);
    expect(diff.files.map((f) => f.path)).toEqual(["workflow.yaml"]);
    const diffText = diff.files[0]!.hunks.join("\n");
    expect(diffText).toMatch(/^-.*modelCalls: 30$/m);
    expect(diffText).toMatch(/^\+.*modelCalls: 45$/m);

    // 回滚到最初版本
    const restored = await call(`/api/employees/weekly-report/config/versions/${first.version.id}/restore`, { method: "POST" });
    expect(restored.status).toBe(200);
    const restoreBody = (await restored.json()) as RestoreConfigResponse;
    expect(restoreBody.restoredFrom).toBe(first.version.id);
    expect(restoreBody.versions).toHaveLength(3);
    expect((await config()).config!.workflow.limits.task.modelCalls).toBe(30);

    const after = (await (await call("/api/employees/weekly-report/config/versions")).json()) as ConfigVersion[];
    expect(after).toHaveLength(3);
    expect(after[0]!.id).toBe(restoreBody.version.id);

    // 回滚本身也是版本：最新的快照里留着回滚前（45）的内容
    const snapshotDiff = (await (await call(`/api/employees/weekly-report/config/versions/${restoreBody.version.id}/diff`)).json()) as ConfigDiff;
    expect(snapshotDiff.files[0]!.hunks.join("\n")).toContain("modelCalls: 45");
    expect(versionIds(workspaceDir)).toHaveLength(3);
  });

  test("不存在的版本与差异 404", async () => {
    const { call } = setup();
    expect((await call("/api/employees/weekly-report/config/versions/nope/diff")).status).toBe(404);
    expect((await call("/api/employees/weekly-report/config/versions/nope/restore", { method: "POST" })).status).toBe(404);
  });
});
