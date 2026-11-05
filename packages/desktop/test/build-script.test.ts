import { describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import {
  FIXED_DESKTOP_COPY,
  desktopArtifactFolder,
  desktopCopyMapping,
  desktopWasmTarget,
  findSetupArtifacts,
  formatSize,
  hutchMissingMessage,
  resolveResvgWasmSource,
} from "../../../scripts/build-desktop";

const REPO_ROOT = resolve(import.meta.dir, "..", "..", "..");

describe("桌面构建脚本（纯函数，不启动 Hutch）", () => {
  test("资源映射与 electrobun.config.ts 的 build.copy 一致（员工按目录展开，避开 node_modules 软链）", () => {
    const mapping = desktopCopyMapping(REPO_ROOT);
    expect(mapping).toMatchObject({
      "packages/web/dist": "web",
      "samples/scenario-a": "samples/scenario-a",
      "samples/cases": "samples/cases",
      "packages/desktop/assets/resvg.wasm": "assets/resvg.wasm",
      // 只映射含 workflow.yaml 的员工目录；整个 employees/ 里有 workspace 的 node_modules 软链
      "employees/weekly-report": "employees/weekly-report",
    });
    expect(mapping["employees"]).toBeUndefined();
    expect(mapping["employees/node_modules"]).toBeUndefined();
    // 返回副本，调用方改不到模块常量
    mapping["samples/scenario-a"] = "changed";
    expect(FIXED_DESKTOP_COPY["samples/scenario-a"]).toBe("samples/scenario-a");
  });

  test("产物目录与 wasm 目标路径", () => {
    expect(desktopArtifactFolder("/repo")).toBe(join("/repo", "dist", "desktop"));
    expect(desktopWasmTarget("/repo")).toBe(join("/repo", "packages", "desktop", "assets", "resvg.wasm"));
    expect(desktopArtifactFolder(REPO_ROOT)).toBe(join(REPO_ROOT, "dist", "desktop"));
  });

  test("resvg wasm 源文件能从 @de/tools 的依赖里解析到", () => {
    const source = resolveResvgWasmSource(REPO_ROOT);
    expect(source.endsWith(join("@resvg", "resvg-wasm", "index_bg.wasm"))).toBe(true);
    expect(Bun.file(source).size).toBeGreaterThan(0);
  });

  test("只收集 Setup.zip，按名字排序", () => {
    const dir = mkdtempSync(join(tmpdir(), "de-artifacts-"));
    try {
      writeFileSync(join(dir, "win-x64-Digital Employee-Setup.zip"), "a");
      writeFileSync(join(dir, "stable-win-x64-update.json"), "{}");
      mkdirSync(join(dir, "nested-Setup.zip"));
      expect(findSetupArtifacts(dir)).toEqual([join(dir, "win-x64-Digital Employee-Setup.zip")]);
      expect(findSetupArtifacts(join(dir, "missing"))).toEqual([]);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });

  test("缺少 Hutch 的提示包含安装命令；中文用户名下点明 HUTCH_HOME", () => {
    const chinese = hutchMissingMessage({ USERPROFILE: "C:\\Users\\花人局" });
    expect(chinese).toContain("hutch/install.ps1");
    expect(chinese).toContain("HUTCH_HOME");
    expect(chinese).toContain("AccessDenied");
    const ascii = hutchMissingMessage({ USERPROFILE: "C:\\Users\\dev" });
    expect(ascii).toContain("HUTCH_HOME");
    expect(ascii).not.toContain("AccessDenied");
  });

  test("体积格式化为 MB 一位小数", () => {
    expect(formatSize(45.3 * 1024 * 1024)).toBe("45.3MB");
  });
});
