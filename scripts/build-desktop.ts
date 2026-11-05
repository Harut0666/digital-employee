// 构建桌面安装包：
//   检查 Hutch → 构建前端 → 拷贝 resvg wasm → hutch electrobun build --env=stable → 打印产物与体积
//   bun run build:desktop
// Hutch 是构建期工具（机器级安装），不进 package.json；中文用户名下需把 HUTCH_HOME 指到 ASCII 路径。
import { cpSync, existsSync, mkdirSync, readdirSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { REPO_ROOT } from "./gen-embedded";

// 资源映射与 electrobun.config.ts 共用（见 desktop-resources.ts），这里 re-export 便于单测。
export {
  FIXED_DESKTOP_COPY,
  desktopCopyMapping,
  employeeResourceDirs,
} from "./desktop-resources";

/** 桌面安装包产物目录（与 electrobun.config.ts 的 artifactFolder 一致）。 */
export function desktopArtifactFolder(repoRoot = REPO_ROOT): string {
  return join(repoRoot, "dist", "desktop");
}

/** 构建期拷贝到仓库里的 resvg wasm 目标路径。 */
export function desktopWasmTarget(repoRoot = REPO_ROOT): string {
  return join(repoRoot, "packages", "desktop", "assets", "resvg.wasm");
}

/** 从 @de/tools 的依赖里定位 resvg wasm 源文件。 */
export function resolveResvgWasmSource(repoRoot = REPO_ROOT): string {
  const pkg = Bun.resolveSync("@resvg/resvg-wasm/package.json", join(repoRoot, "packages", "tools"));
  return join(dirname(pkg), "index_bg.wasm");
}

export function formatSize(bytes: number): string {
  return `${(bytes / 1024 / 1024).toFixed(1)}MB`;
}

/** 找不到 Hutch 时的可执行提示（含安装命令与中文用户名下的 HUTCH_HOME 坑）。 */
export function hutchMissingMessage(env: Record<string, string | undefined> = process.env): string {
  const home = env.USERPROFILE ?? env.HOME ?? "";
  const nonAscii = /[^\x00-\x7F]/.test(home);
  return [
    "缺少 Hutch：桌面构建需要 Hutch（构建期工具，不写进 package.json）。",
    '安装（PowerShell）：powershell -NoProfile -ExecutionPolicy Bypass -Command "& ([scriptblock]::Create((irm https://hutch.blackboard.sh/hutch/install.ps1)))"',
    "安装后把 %USERPROFILE%\\.hutch\\bin 加进 PATH，或直接执行 ~/.hutch/bin/hutch。",
    nonAscii
      ? `当前用户目录含非 ASCII 字符（${home}）：请先设置 HUTCH_HOME 到纯 ASCII 路径（例如 D:\\hutch-home），否则会报 AccessDenied。`
      : "若安装后仍找不到，可设置 HUTCH_HOME 到纯 ASCII 路径（例如 D:\\hutch-home）。",
  ].join("\n");
}

/** 产物目录里的 Setup.zip（按名字排序，便于稳定输出）。 */
export function findSetupArtifacts(dir: string): string[] {
  if (!existsSync(dir)) return [];
  return readdirSync(dir, { withFileTypes: true })
    .filter((entry) => entry.isFile() && /-Setup\.zip$/i.test(entry.name))
    .map((entry) => join(dir, entry.name))
    .sort();
}

async function run(label: string, cmd: string[], cwd = REPO_ROOT): Promise<void> {
  console.log(`\n▶ ${label}`);
  const started = performance.now();
  const proc = Bun.spawn({ cmd, cwd, stdout: "inherit", stderr: "inherit" });
  const code = await proc.exited;
  if (code !== 0) throw new Error(`${label} 失败（退出码 ${code}）`);
  console.log(`  完成，用时 ${((performance.now() - started) / 1000).toFixed(1)}s`);
}

async function main(): Promise<void> {
  const started = performance.now();
  const hutch = Bun.which("hutch");
  if (!hutch) throw new Error(hutchMissingMessage());

  // 1. 前端构建产物：桌面窗口里就是这份
  await run("构建前端（@de/web）", [process.execPath, "run", "--filter", "@de/web", "build"]);

  // 2. resvg wasm：桌面主进程不能依赖 `with { type: "file" }`，随包拷一份
  console.log("\n▶ 拷贝 resvg wasm");
  const wasmSource = resolveResvgWasmSource();
  if (!existsSync(wasmSource)) throw new Error(`找不到 resvg wasm：${wasmSource}；先跑 bun install`);
  const wasmTarget = desktopWasmTarget();
  mkdirSync(dirname(wasmTarget), { recursive: true });
  cpSync(wasmSource, wasmTarget);
  console.log(`  ${wasmSource} → ${wasmTarget}（${(statSync(wasmTarget).size / 1024).toFixed(0)}KB）`);

  // 3. Hutch 打包（首次会拉取 Electrobun devkit / Bun 运行时，需要网络）
  await run("打包桌面应用（Hutch + Electrobun）", [hutch, "electrobun", "build", "--env=stable"]);

  const artifacts = findSetupArtifacts(desktopArtifactFolder());
  if (artifacts.length === 0) {
    throw new Error(`没有生成 Setup.zip：${desktopArtifactFolder()}（检查 hutch electrobun build 的输出）`);
  }
  console.log("\n✅ 桌面安装包：");
  for (const file of artifacts) console.log(`   ${file}（${formatSize(statSync(file).size)}）`);
  console.log(`   整体用时 ${((performance.now() - started) / 1000).toFixed(1)}s`);
}

if (import.meta.main) {
  try {
    await main();
  } catch (err) {
    console.error(`\n✗ ${err instanceof Error ? err.message : String(err)}`);
    process.exit(1);
  }
}
