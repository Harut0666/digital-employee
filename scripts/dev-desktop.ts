// 开发模式：先补齐被 gitignore 的桌面资源（前端产物 + resvg wasm），再用 Hutch 起 Electrobun dev（--watch）。
// 干净检出直接 `bun run dev:desktop` 也能起窗口——不再依赖之前跑过 build:desktop。
// 只用于开发；交付产物走 build-desktop.ts（带 --env=stable 打包与产物校验）。
import { hutchMissingMessage, prepareDesktopResources } from "./desktop-resources";

/** Hutch dev 的子命令：--watch 让改主进程后自动重建；带 --env=stable 会走安装器流程并失败。 */
export function devElectrobunArgs(): string[] {
  return ["electrobun", "dev", "--watch"];
}

/** 完整命令行（第一个元素是 Bun.which 找到的 hutch 可执行文件）。 */
export function devElectrobunCommand(hutch: string): string[] {
  return [hutch, ...devElectrobunArgs()];
}

/** 找不到 Hutch 时的提示：dev 同样需要 Hutch，并给出安装命令与 HUTCH_HOME/PATH 指引。 */
export function devHutchMissingMessage(env: Record<string, string | undefined> = process.env): string {
  return [
    "缺少 Hutch：桌面开发模式（bun run dev:desktop）需要 Hutch 才能起 Electrobun 窗口。",
    "Hutch 只是开发/构建期工具，不进 package.json；应用产物本身不需要它。",
    hutchMissingMessage(env),
  ].join("\n");
}

async function main(): Promise<void> {
  const hutch = Bun.which("hutch");
  if (!hutch) throw new Error(devHutchMissingMessage());

  // 1. 前端产物与 resvg wasm：两者都不入库，干净检出必须先准备
  await prepareDesktopResources();

  // 2. Hutch dev：stdio 交给 Hutch，终端里能看到窗口日志；退出码原样带回
  console.log(`\n▶ 启动 Electrobun dev：${devElectrobunCommand(hutch).join(" ")}`);
  const proc = Bun.spawn({
    cmd: devElectrobunCommand(hutch),
    stdin: "inherit",
    stdout: "inherit",
    stderr: "inherit",
  });
  process.exitCode = await proc.exited;
}

if (import.meta.main) {
  try {
    await main();
  } catch (err) {
    console.error(`\n✗ ${err instanceof Error ? err.message : String(err)}`);
    process.exit(1);
  }
}
