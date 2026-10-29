// 配置文件写回：先校验再生成最终文本。YAML 用 Document 接口保留注释，JSON Schema 用 ajv 编译。
import { describe, expect, test } from "bun:test";
import { cpSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { parse as parseYaml } from "yaml";
import { isWritableConfigPath, normalizeConfigPath } from "../../src/config/files";
import { prepareConfigText } from "../../src/config/write";

const repoRoot = join(import.meta.dir, "..", "..", "..", "..");
const employeeSource = join(repoRoot, "employees", "weekly-report");
const tmpDirs: string[] = [];

function copyEmployee(): string {
  const dir = mkdtempSync(join(tmpdir(), "de-write-"));
  tmpDirs.push(dir);
  cpSync(employeeSource, dir, { recursive: true });
  return dir;
}

function read(dir: string, rel: string): string {
  return readFileSync(join(dir, rel), "utf8");
}

function cleanup(): void {
  for (const d of tmpDirs.splice(0)) rmSync(d, { recursive: true, force: true });
}

describe("配置路径", () => {
  test("只允许写入配置文件；拒绝路径穿越与未知文件", () => {
    expect(isWritableConfigPath("workflow.yaml")).toBe(true);
    expect(isWritableConfigPath("prompts/write_report.md")).toBe(true);
    expect(isWritableConfigPath("schemas/write_report.json")).toBe(true);
    expect(isWritableConfigPath("routes.yaml")).toBe(false);
    expect(isWritableConfigPath("prompts/../../package.json")).toBe(false);
    expect(normalizeConfigPath("./workflow.yaml")).toBe("workflow.yaml");
    expect(normalizeConfigPath("prompts/write_report.md")).toBe("prompts/write_report.md");
    expect(normalizeConfigPath("../package.json")).toBeUndefined();
    expect(normalizeConfigPath("C:/windows/x")).toBeUndefined();
    expect(normalizeConfigPath("prompts/notes.txt")).toBeUndefined();
  });
});

describe("workflow.yaml", () => {
  test("结构化保存：只改上限，注释保留，内容生效", () => {
    const dir = copyEmployee();
    const current = read(dir, "workflow.yaml");
    const data = parseYaml(current) as Record<string, any>;
    data.limits.task.modelCalls = 42;
    data.steps.find((s: any) => s.id === "write_report").limits.rounds = 4;

    const result = prepareConfigText("workflow.yaml", { data }, current);
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(result.text).toContain("# 经营周报员工 · 工作步骤");
    expect(result.text).toContain("# 只在重做时执行（首轮记为跳过）");
    const parsed = parseYaml(result.text) as Record<string, any>;
    expect(parsed.limits.task.modelCalls).toBe(42);
    expect(parsed.steps.find((s: any) => s.id === "write_report").limits.rounds).toBe(4);
  });

  test("违反契约（模型调用上限为 0、删掉步骤）返回逐条错误", () => {
    const dir = copyEmployee();
    const current = read(dir, "workflow.yaml");
    const data = parseYaml(current) as Record<string, any>;

    const zero = structuredClone(data);
    zero.limits.task.modelCalls = 0;
    const result = prepareConfigText("workflow.yaml", { data: zero }, current);
    expect(result.ok).toBe(false);
    if (result.ok) return;
    expect(result.errors.join("\n")).toContain("modelCalls");

    const noSteps = structuredClone(data);
    noSteps.steps = [];
    const empty = prepareConfigText("workflow.yaml", { data: noSteps }, current);
    expect(empty.ok).toBe(false);
  });

  test("非法 YAML 返回解析错误；原文保存时按文本写回", () => {
    const dir = copyEmployee();
    const current = read(dir, "workflow.yaml");
    const broken = prepareConfigText("workflow.yaml", { content: "steps: [ ][" }, current);
    expect(broken.ok).toBe(false);
    if (!broken.ok) expect(broken.errors.join("\n")).toContain("YAML");

    // 合法原文：按用户输入的文本原样保存（不重新排版）
    const text = `${current}\n# 行尾注释\n`;
    const result = prepareConfigText("workflow.yaml", { content: text }, current);
    expect(result.ok).toBe(true);
    if (result.ok) expect(result.text).toBe(text);
  });

  test("qc.yaml 违反契约（未知模板）返回错误", () => {
    const dir = copyEmployee();
    const current = read(dir, "qc.yaml");
    const data = parseYaml(current) as Record<string, any>;
    data.custom = [{ id: "r1", label: "规则", template: "forbidden_terms", params: {} }];
    const result = prepareConfigText("qc.yaml", { data }, current);
    expect(result.ok).toBe(true);

    const bad = structuredClone(data);
    bad.custom[0].template = "make_it_nice";
    const rejected = prepareConfigText("qc.yaml", { data: bad }, current);
    expect(rejected.ok).toBe(false);
    if (!rejected.ok) expect(rejected.errors.join("\n")).toContain("template");
  });

  test("qc.yaml 结构化保存保留注释", () => {
    const dir = copyEmployee();
    const current = read(dir, "qc.yaml");
    const data = parseYaml(current) as Record<string, any>;
    data.builtin.anomaly_coverage.threshold = 0.12;
    const result = prepareConfigText("qc.yaml", { data }, current);
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(result.text).toContain("# 经营周报员工 · 质检规则");
    expect(result.text).toContain("# 3 异常覆盖");
    expect((parseYaml(result.text) as Record<string, any>).builtin.anomaly_coverage.threshold).toBe(0.12);
  });
});

describe("form.schema.json", () => {
  test("合法 Schema（含 x-purpose）通过", () => {
    const dir = copyEmployee();
    const current = read(dir, "form.schema.json");
    const data = JSON.parse(current) as Record<string, any>;
    data.properties.focusRegion = { type: "string", title: "重点关注区域", "x-purpose": "writing_requirement" };
    data.properties.period["x-purpose"] = "param:period";
    const result = prepareConfigText("form.schema.json", { data }, current);
    expect(result.ok).toBe(true);
  });

  test("非法 JSON、ajv 编译不过、x-purpose 未知都返回错误", () => {
    const dir = copyEmployee();
    const current = read(dir, "form.schema.json");

    expect(prepareConfigText("form.schema.json", { content: "{ not json" }, current).ok).toBe(false);

    const broken = JSON.parse(current) as Record<string, any>;
    broken.properties.period.type = "nonsense";
    const compiled = prepareConfigText("form.schema.json", { data: broken }, current);
    expect(compiled.ok).toBe(false);

    const badPurpose = JSON.parse(current) as Record<string, any>;
    badPurpose.properties.focusRegion = { type: "string", "x-purpose": "make_it_nice" };
    const rejected = prepareConfigText("form.schema.json", { data: badPurpose }, current);
    expect(rejected.ok).toBe(false);
    if (!rejected.ok) expect(rejected.errors.join("\n")).toContain("x-purpose");
  });
});

describe("SKILL.md 与 frontdesk.md", () => {
  test("岗位说明三个文本框拼回 SKILL.md，其余章节不动", () => {
    const dir = copyEmployee();
    const current = read(dir, "SKILL.md");
    const data = {
      duties: "你是新的岗位职责。\n",
      writing: current.slice(current.indexOf("## 最重要的规则"), current.indexOf("## 注意事项")),
      cautions: current.slice(current.indexOf("## 注意事项") + "## 注意事项".length),
    };
    const result = prepareConfigText("SKILL.md", { data }, current);
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(result.text).toContain("你是新的岗位职责。");
    expect(result.text).toContain("## 最重要的规则");
  });

  test("前台 Agent 人设 + 只读工具；未知工具被拒绝", () => {
    const dir = copyEmployee();
    const current = read(dir, "frontdesk.md");
    const ok = prepareConfigText("frontdesk.md", { data: { body: "# 前台 Agent\n\n说话简短。\n", tools: ["query_task"] } }, current);
    expect(ok.ok).toBe(true);
    if (!ok.ok) return;
    expect(ok.text).toContain("## 开放给你的只读工具");
    expect(ok.text).toContain("- query_task");

    const bad = prepareConfigText("frontdesk.md", { data: { body: "x", tools: ["web_search"] } }, current);
    expect(bad.ok).toBe(false);
  });

  test("提示词与输出 Schema 文件：文本直接校验 JSON，路径必须在白名单内", () => {
    const dir = copyEmployee();
    const prompt = read(dir, "prompts/write_report.md");
    expect(prepareConfigText("prompts/write_report.md", { content: `${prompt}\n补充一句。` }, prompt).ok).toBe(true);

    const schemaText = read(dir, "schemas/write_report.json");
    expect(prepareConfigText("schemas/write_report.json", { content: schemaText }, schemaText).ok).toBe(true);
    expect(prepareConfigText("schemas/write_report.json", { content: "{ broken" }, schemaText).ok).toBe(false);
    expect(prepareConfigText("../package.json", { content: "{}" }, "{}").ok).toBe(false);
  });
});
