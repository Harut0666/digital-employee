import { describe, expect, test } from "bun:test";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { releaseFiles, toolServerCommand, writeDefaultSettings } from "../src/bootstrap";

const utf8 = (s: string) => new TextEncoder().encode(s);
const tmp = () => mkdtempSync(join(tmpdir(), "de-release-"));

describe("toolServerCommand", () => {
  test("under bun (development) it runs packages/tools/src/stdio.ts", () => {
    const c = toolServerCommand({ execPath: "C:\\bun\\bun.exe", toolsEntry: "D:\\repo\\packages\\tools\\src\\stdio.ts" });
    expect(c).toEqual({ command: "C:\\bun\\bun.exe", args: ["D:\\repo\\packages\\tools\\src\\stdio.ts"] });
  });
  test("a packaged exe starts itself with the mcp-server subcommand", () => {
    const c = toolServerCommand({ execPath: "C:\\app\\digital-employee.exe", toolsEntry: "ignored" });
    expect(c).toEqual({ command: "C:\\app\\digital-employee.exe", args: ["mcp-server"] });
  });
});

describe("releaseFiles", () => {
  test("把嵌套文件按原相对路径释放到工作区", () => {
    const root = tmp();
    const outcome = releaseFiles(
      [
        { path: "weekly-report/workflow.yaml", data: utf8("steps: []") },
        { path: "weekly-report/prompts/write_report.md", data: utf8("# 写作") },
      ],
      root,
    );
    expect(outcome.written.sort()).toEqual(["weekly-report/prompts/write_report.md", "weekly-report/workflow.yaml"]);
    expect(outcome.skipped).toEqual([]);
    expect(readFileSync(join(root, "weekly-report", "prompts", "write_report.md"), "utf8")).toBe("# 写作");
  });

  test("已存在的文件不覆盖，内容保持用户改过的那份", () => {
    const root = tmp();
    mkdirSync(join(root, "weekly-report"), { recursive: true });
    writeFileSync(join(root, "weekly-report", "workflow.yaml"), "steps: 用户改过的");
    const outcome = releaseFiles([{ path: "weekly-report/workflow.yaml", data: utf8("steps: 默认的") }], root);
    expect(readFileSync(join(root, "weekly-report", "workflow.yaml"), "utf8")).toBe("steps: 用户改过的");
    expect(outcome.skipped).toEqual(["weekly-report/workflow.yaml"]);
    expect(outcome.written).toEqual([]);
  });

  test("overwrite 为真时覆盖旧文件（内部运行时资源换版本要能更新）", () => {
    const root = tmp();
    mkdirSync(join(root, "web"), { recursive: true });
    writeFileSync(join(root, "web", "index.html"), "旧版");
    const outcome = releaseFiles([{ path: "web/index.html", data: utf8("新版") }], root, { overwrite: true });
    expect(readFileSync(join(root, "web", "index.html"), "utf8")).toBe("新版");
    expect(outcome.written).toEqual(["web/index.html"]);
  });

  test("路径穿越与绝对路径一律拒绝，且不产生任何文件", () => {
    const root = tmp();
    for (const path of ["../evil.txt", "C:/Windows/evil.txt", "/etc/evil.txt", "a/../../evil.txt"]) {
      expect(() => releaseFiles([{ path, data: utf8("x") }], root)).toThrow(/非法/);
    }
    expect(existsSync(join(root, "..", "evil.txt"))).toBe(false);
    expect(existsSync(join(root, "evil.txt"))).toBe(false);
  });
});

describe("writeDefaultSettings", () => {
  const template = { connections: [{ name: "deepseek", baseUrl: "https://api.deepseek.com", apiKey: "", models: ["deepseek-chat"] }] };

  test("首次启动写入默认 settings.json（不含 key）", () => {
    const root = tmp();
    expect(writeDefaultSettings(root, template)).toBe("written");
    const raw = readFileSync(join(root, "settings.json"), "utf8");
    expect(raw.endsWith("\n")).toBe(true);
    const parsed = JSON.parse(raw) as { connections: { apiKey: string }[] };
    expect(parsed).toEqual(template);
    expect(parsed.connections[0]!.apiKey).toBe("");
  });

  test("已有 settings.json 时保留用户那份", () => {
    const root = tmp();
    writeFileSync(join(root, "settings.json"), '{"connections":[],"defaultModel":"mine"}\n');
    expect(writeDefaultSettings(root, template)).toBe("kept");
    expect(readFileSync(join(root, "settings.json"), "utf8")).toContain("mine");
  });
});
