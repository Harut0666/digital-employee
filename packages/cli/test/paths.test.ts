import { describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { resolveEmployeesDir, resolveSamplesDir } from "../src/paths";

function makeWorkspace(): string {
  const dir = mkdtempSync(join(tmpdir(), "de-cli-paths-"));
  mkdirSync(join(dir, "samples", "scenario-a"), { recursive: true });
  writeFileSync(join(dir, "samples", "scenario-a", "数据说明.md"), "# 说明");
  return dir;
}

describe("resolveEmployeesDir", () => {
  test("DE_EMPLOYEES_DIR 优先", () => {
    const workspace = makeWorkspace();
    const dir = resolveEmployeesDir({ DE_EMPLOYEES_DIR: join(workspace, "auto"), DE_WORKSPACE: workspace }, "/repo");
    expect(dir.replace(/\\/g, "/")).toContain("/auto");
  });

  test("没设环境变量时用工作区里释放出来的员工配置（exe 场景）", () => {
    const workspace = makeWorkspace();
    mkdirSync(join(workspace, "employees", "weekly-report"), { recursive: true });
    writeFileSync(join(workspace, "employees", "weekly-report", "workflow.yaml"), "steps: []");
    const dir = resolveEmployeesDir({ DE_WORKSPACE: workspace }, "/repo");
    expect(dir.replace(/\\/g, "/")).toBe(join(workspace, "employees").replace(/\\/g, "/"));
  });

  test("工作区里没有员工配置时回退到仓库默认配置（源码运行）", () => {
    const workspace = makeWorkspace();
    mkdirSync(join(workspace, "employees"), { recursive: true });
    expect(resolveEmployeesDir({ DE_WORKSPACE: workspace }, join("/repo")).replace(/\\/g, "/")).toBe("/repo/employees");
  });
});

describe("resolveSamplesDir", () => {
  test("工作区里有样例时用工作区那份", () => {
    const workspace = makeWorkspace();
    expect(resolveSamplesDir({ DE_WORKSPACE: workspace }, "/repo").replace(/\\/g, "/")).toBe(join(workspace, "samples").replace(/\\/g, "/"));
  });

  test("工作区里没有样例时回退到仓库 samples", () => {
    const empty = mkdtempSync(join(tmpdir(), "de-cli-empty-"));
    expect(resolveSamplesDir({ DE_WORKSPACE: empty }, "/repo").replace(/\\/g, "/")).toBe("/repo/samples");
  });
});
