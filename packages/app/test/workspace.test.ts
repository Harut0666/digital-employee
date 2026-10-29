import { describe, expect, test } from "bun:test";
import { existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { loadSettings } from "@de/core";
import { encodeArchive, type ArchiveEntry } from "../src/archive";
import { prepareWorkspace, type EmbeddedBundlePaths } from "../src/workspace";

const utf8 = (s: string) => new TextEncoder().encode(s);
const tmp = () => mkdtempSync(join(tmpdir(), "de-ws-"));

/** 在临时目录里造三个归档，模拟构建期产物。 */
async function makeBundles(dir: string): Promise<EmbeddedBundlePaths> {
  const write = async (name: string, entries: ArchiveEntry[]) => {
    const path = join(dir, name);
    await Bun.write(path, encodeArchive(entries));
    return path;
  };
  return {
    web: await write("web.bundle", [
      { key: "index.html", data: utf8("<html>周报</html>") },
      { key: "assets/index-abc.js", data: utf8("console.log('js')") },
    ]),
    employees: await write("employees.bundle", [
      { key: "weekly-report/workflow.yaml", data: utf8("steps: 默认") },
      { key: "weekly-report/prompts/write_report.md", data: utf8("# 写作提示词") },
    ]),
    samples: await write("samples.bundle", [
      { key: "scenario-a/销售明细.xlsx", data: new Uint8Array([80, 75, 3, 4]) },
      { key: "cases/A-01.json", data: utf8('{"id":"A-01"}') },
    ]),
  };
}

describe("prepareWorkspace", () => {
  test("首次启动释放员工配置与样例、写默认 settings.json、解出前端构建产物", async () => {
    const bundles = await makeBundles(tmp());
    const workspaceDir = tmp();
    const prepared = await prepareWorkspace({ workspaceDir, bundles });

    expect(readFileSync(join(workspaceDir, "employees", "weekly-report", "workflow.yaml"), "utf8")).toBe("steps: 默认");
    expect(readFileSync(join(workspaceDir, "samples", "cases", "A-01.json"), "utf8")).toBe('{"id":"A-01"}');
    expect(existsSync(join(prepared.employeesDir, "weekly-report", "workflow.yaml"))).toBe(true);
    // 默认员工配置另存一份未改动的副本，供设置页“恢复默认”使用
    expect(readFileSync(join(prepared.defaultEmployeesDir, "weekly-report", "workflow.yaml"), "utf8")).toBe("steps: 默认");
    expect(existsSync(join(prepared.webDistDir, "index.html"))).toBe(true);
    expect(readFileSync(join(prepared.webDistDir, "assets", "index-abc.js"), "utf8")).toContain("console.log");

    // 默认设置：DeepSeek 连接，但没有 Key（用户自己填）
    expect(prepared.settings).toBe("written");
    const settings = loadSettings(workspaceDir);
    expect(settings.connections[0]!.baseUrl).toBe("https://api.deepseek.com");
    expect(settings.connections[0]!.apiKey).toBe("");
    expect(settings.defaultModel).toBe("deepseek-chat");
  });

  test("再次启动不覆盖用户改过的员工配置与样例，前端构建产物换新版", async () => {
    const bundles = await makeBundles(tmp());
    const workspaceDir = tmp();
    await prepareWorkspace({ workspaceDir, bundles });

    writeFileSync(join(workspaceDir, "employees", "weekly-report", "workflow.yaml"), "steps: 用户改的");
    writeFileSync(join(workspaceDir, "samples", "cases", "A-01.json"), '{"id":"用户改的"}');
    writeFileSync(join(workspaceDir, "settings.json"), '{"defaultModel":"mine"}\n');

    // 新版 exe 的归档里多了一个文件、覆盖了员工配置的默认内容
    const next = await makeBundles(tmp());
    await Bun.write(
      next.employees,
      encodeArchive([
        { key: "weekly-report/workflow.yaml", data: utf8("steps: 新默认") },
        { key: "weekly-report/qc.yaml", data: utf8("builtin: {}") },
      ]),
    );
    const prepared = await prepareWorkspace({ workspaceDir, bundles: next });

    expect(readFileSync(join(workspaceDir, "employees", "weekly-report", "workflow.yaml"), "utf8")).toBe("steps: 用户改的");
    expect(readFileSync(join(workspaceDir, "samples", "cases", "A-01.json"), "utf8")).toBe('{"id":"用户改的"}');
    expect(existsSync(join(workspaceDir, "employees", "weekly-report", "qc.yaml"))).toBe(true);
    // 默认副本跟着 exe 换新，但用户那份不受影响
    expect(readFileSync(join(prepared.defaultEmployeesDir, "weekly-report", "workflow.yaml"), "utf8")).toBe("steps: 新默认");
    expect(prepared.settings).toBe("kept");
    expect(JSON.parse(readFileSync(join(workspaceDir, "settings.json"), "utf8"))).toEqual({ defaultModel: "mine" });
  });

  test("嵌入归档缺失时报错说明是构建问题", async () => {
    const workspaceDir = tmp();
    const missing = join(tmp(), "web.bundle");
    await expect(
      prepareWorkspace({ workspaceDir, bundles: { web: missing, employees: missing, samples: missing } }),
    ).rejects.toThrow(/嵌入资源/);
  });
});
