// dev:desktop 是“干净检出也能起窗口”的入口：先补齐被 gitignore 的前端产物与 resvg wasm，再用 Hutch 起 Electrobun dev。
// 这里只断言可离线验证的部分：命令构造与缺 Hutch 时的提示，以及三个入口脚本的接线。
import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { devElectrobunArgs, devElectrobunCommand, devHutchMissingMessage } from "../../../scripts/dev-desktop";

const REPO_ROOT = resolve(import.meta.dir, "..", "..", "..");

function readJson(path: string): Record<string, unknown> {
  return JSON.parse(readFileSync(path, "utf8")) as Record<string, unknown>;
}

describe("dev:desktop（资源准备 + Hutch dev）", () => {
  test("Hutch dev 命令带 --watch，不再带会走安装器流程的 --env=stable", () => {
    const args = devElectrobunArgs();
    expect(args).toEqual(["electrobun", "dev", "--watch"]);
    expect(args).not.toContain("--env=stable");
    // 第一个元素是可执行文件本身，便于把 Bun.which 找到的路径直接塞进去
    expect(devElectrobunCommand("C:\\hutch\\hutch.exe")).toEqual([
      "C:\\hutch\\hutch.exe",
      "electrobun",
      "dev",
      "--watch",
    ]);
  });

  test("缺少 Hutch 的提示包含安装命令、PATH/HUTCH_HOME 与 dev:desktop", () => {
    const message = devHutchMissingMessage({ USERPROFILE: "C:\\Users\\花人局" });
    expect(message).toContain("dev:desktop");
    expect(message).toContain("hutch/install.ps1");
    expect(message).toContain("PATH");
    expect(message).toContain("HUTCH_HOME");
    // 中文用户名的已知坑要能在这条提示里看到
    expect(message).toContain("AccessDenied");
  });

  test("三处 dev 入口都指向 scripts/dev-desktop.ts，且都不再出现 --env=stable", () => {
    const rootPkg = readJson(join(REPO_ROOT, "package.json"));
    expect((rootPkg.scripts as Record<string, string>)["dev:desktop"]).toBe("bun scripts/dev-desktop.ts");

    const desktopPkg = readJson(join(REPO_ROOT, "packages", "desktop", "package.json"));
    expect((desktopPkg.scripts as Record<string, string>).dev).toBe("bun ../../scripts/dev-desktop.ts");

    const hutchConfig = readFileSync(join(REPO_ROOT, "hutch.config.ts"), "utf8");
    expect(hutchConfig).toContain("scripts/dev-desktop.ts");
    expect(hutchConfig).not.toContain("electrobun\", \"dev\", \"--env=stable\"");
    // 构建任务仍然直接跑 Hutch 的打包命令（--env=stable 只属于 build）
    expect(hutchConfig).toContain("build:desktop");
  });
});
