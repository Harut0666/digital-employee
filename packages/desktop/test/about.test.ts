// “关于”信息：从打包资源读版本 + 纯格式化；不依赖 Electrobun 运行时。
import { afterEach, describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  UNKNOWN_VERSION,
  defaultResourcesDir,
  formatAbout,
  readAboutInfo,
} from "../src/about";

const roots: string[] = [];

function tempResources(files: Record<string, unknown>): string {
  const root = mkdtempSync(join(tmpdir(), "de-about-"));
  roots.push(root);
  for (const [name, value] of Object.entries(files)) {
    writeFileSync(join(root, name), JSON.stringify(value));
  }
  return root;
}

afterEach(() => {
  for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true });
});

describe("关于对话框内容", () => {
  test("包含应用版本、Electrobun 与 Bun 运行时版本", () => {
    const body = formatAbout(
      { appVersion: "0.1.0", electrobunVersion: "2.0.6", bunVersion: "1.4.4" },
      { workspaceDir: "C:\\Users\\me\\AppData\\Roaming\\digital-employee" },
    );
    expect(body.title).toBe("迷你数字员工");
    expect(body.message).toContain("迷你数字员工");
    expect(body.message).toContain("0.1.0");
    expect(body.detail).toContain("应用版本：0.1.0");
    expect(body.detail).toContain("Electrobun：2.0.6");
    expect(body.detail).toContain("Bun：1.4.4");
    expect(body.detail).toContain("工作区：C:\\Users\\me");
  });

  test("开发构建缺版本信息时不伪造，显示“未知 / 开发构建”", () => {
    const body = formatAbout({});
    expect(body.message).toContain("开发构建");
    expect(body.detail).toContain(`应用版本：${UNKNOWN_VERSION}`);
    expect(body.detail).toContain(`Electrobun：${UNKNOWN_VERSION}`);
    expect(body.detail).toContain(`Bun：${UNKNOWN_VERSION}`);
  });
});

describe("读取打包版本", () => {
  test("从 Resources/version.json 与 build.json 读取版本", () => {
    const resources = tempResources({
      "version.json": { version: "0.1.0", channel: "stable" },
      "build.json": { electrobunVersion: "2.0.6", runtimeVersions: { bun: "1.4.4" } },
    });
    expect(readAboutInfo({ resourcesDir: resources, bunVersion: "1.4.2" })).toEqual({
      appVersion: "0.1.0",
      electrobunVersion: "2.0.6",
      bunVersion: "1.4.2",
    });
  });

  test("缺文件返回 undefined 字段，不抛错", () => {
    const resources = tempResources({});
    expect(readAboutInfo({ resourcesDir: resources, bunVersion: "1.4.2" })).toEqual({
      appVersion: undefined,
      electrobunVersion: undefined,
      bunVersion: "1.4.2",
    });
  });

  test("坏 JSON 视为缺失", () => {
    const resources = tempResources({});
    mkdirSync(join(resources, "app"), { recursive: true });
    writeFileSync(join(resources, "version.json"), "{ bad");
    writeFileSync(join(resources, "build.json"), "[]");
    expect(readAboutInfo({ resourcesDir: resources, bunVersion: "x" })).toEqual({
      appVersion: undefined,
      electrobunVersion: undefined,
      bunVersion: "x",
    });
  });

  test("默认资源目录从主进程位置向上两级到 Resources", () => {
    const dir = defaultResourcesDir("D:\\App\\Resources\\app\\bun");
    expect(dir.endsWith(join("App", "Resources"))).toBe(true);
  });});
