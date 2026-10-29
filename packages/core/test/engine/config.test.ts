import { afterEach, describe, expect, test } from "bun:test";
import { cpSync, mkdtempSync, rmSync, writeFileSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { loadEmployeeConfig } from "../../src/config";

const fixture = join(import.meta.dir, "..", "fixtures", "employees", "mini");
const tmpDirs: string[] = [];

function copyFixture(): string {
  const dir = mkdtempSync(join(tmpdir(), "de-config-"));
  tmpDirs.push(dir);
  cpSync(fixture, dir, { recursive: true });
  return dir;
}

afterEach(() => {
  for (const d of tmpDirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

describe("loadEmployeeConfig", () => {
  test("加载员工目录，得到校验后的完整配置", async () => {
    const config = await loadEmployeeConfig(fixture);
    expect(config.id).toBe("mini");
    expect(config.workflow.steps.map((s) => s.id)).toEqual([
      "admission",
      "load_data",
      "compute_metrics",
      "interpret_feedback",
      "match_causes",
      "write_report",
      "render_document",
      "qc",
    ]);
    expect(config.skill).toContain("槽位");
    expect(config.prompts["prompts/match_causes.md"]).toContain("submit_result");
    expect(config.outputSchemas["schemas/write_report.json"]).toMatchObject({ type: "object" });
    expect(config.qc.contradiction.flatTolerance).toBe(0.02);
    expect(config.formSchema).toMatchObject({ type: "object" });
  });

  test("可选的 form.ui.json 加载为 formUi；没有则为 undefined", async () => {
    expect((await loadEmployeeConfig(fixture)).formUi).toBeUndefined();
    const dir = copyFixture();
    writeFileSync(join(dir, "form.ui.json"), JSON.stringify({ "ui:order": ["period"] }));
    expect((await loadEmployeeConfig(dir)).formUi).toEqual({ "ui:order": ["period"] });
  });

  test("哈希只取决于文件内容：同内容同哈希，改一个字就变", async () => {
    const a = copyFixture();
    const b = copyFixture();
    const hashA = (await loadEmployeeConfig(a)).hash;
    expect(hashA).toMatch(/^[0-9a-f]{64}$/);
    expect((await loadEmployeeConfig(b)).hash).toBe(hashA);

    writeFileSync(join(b, "SKILL.md"), readFileSync(join(b, "SKILL.md"), "utf8") + "多一句");
    expect((await loadEmployeeConfig(b)).hash).not.toBe(hashA);
  });

  test("admissionStep 指向不存在的步骤时报错", async () => {
    const dir = copyFixture();
    const file = join(dir, "workflow.yaml");
    writeFileSync(file, readFileSync(file, "utf8").replace("admissionStep: admission", "admissionStep: nope"));
    await expect(loadEmployeeConfig(dir)).rejects.toThrow(/admissionStep/);
  });

  test("步骤引用的提示词文件不存在时报错", async () => {
    const dir = copyFixture();
    rmSync(join(dir, "prompts", "write_report.md"));
    await expect(loadEmployeeConfig(dir)).rejects.toThrow(/write_report\.md/);
  });

  test("缺少 workflow.yaml 时报错", async () => {
    const dir = copyFixture();
    rmSync(join(dir, "workflow.yaml"));
    await expect(loadEmployeeConfig(dir)).rejects.toThrow(/workflow\.yaml/);
  });
});

describe("workflow.qcFixSteps", () => {
  test("缺省为空映射（由引擎回落到场景 A 的默认 id）；显式配置原样加载", async () => {
    expect((await loadEmployeeConfig(fixture)).workflow.qcFixSteps).toEqual({ causes: "match_causes" });
    const dir = copyFixture();
    const file = join(dir, "workflow.yaml");
    writeFileSync(file, readFileSync(file, "utf8") + "\nqcFixSteps:\n  write: write_report\n  causes: match_causes\n");
    expect((await loadEmployeeConfig(dir)).workflow.qcFixSteps).toEqual({ write: "write_report", causes: "match_causes" });
  });

  test("显式配置指向不存在的步骤时报错", async () => {
    const dir = copyFixture();
    const file = join(dir, "workflow.yaml");
    writeFileSync(file, readFileSync(file, "utf8") + "\nqcFixSteps:\n  write: nope\n");
    await expect(loadEmployeeConfig(dir)).rejects.toThrow(/qcFixSteps/);
  });
});