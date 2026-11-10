// 桌面构建与开发共用的资源准备（前端产物、resvg wasm）与 Hutch 提示。
// build-desktop.ts 与 dev-desktop.ts 都从这里走，避免“构建有、dev 没有”导致干净检出起不来。
// 资源映射（desktopCopyMapping）与 electrobun.config.ts 共用，避免两份清单漂移。
// Hutch 的 build.copy 支持目录，但会把目录原样复制；employees/ 里有 workspace 的 node_modules 软链，
// 直接复制整个 employees 会因软链指向目录而报 IsDir。所以这里只映射真正含 workflow.yaml 的员工目录
//（与 gen-embedded 的“员工目录”判定一致）。
import { cpSync, existsSync, mkdirSync, readdirSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const SCRIPTS_REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");

/** 固定资源映射（源相对仓库根 → 安装包内路径）。 */
export const FIXED_DESKTOP_COPY: Readonly<Record<string, string>> = {
  "packages/web/dist": "web",
  "samples/scenario-a": "samples/scenario-a",
  "samples/cases": "samples/cases",
  "packages/desktop/assets/resvg.wasm": "assets/resvg.wasm",
};

/** employees/ 下真正含 workflow.yaml 的员工目录名（忽略 node_modules、test 等）。 */
export function employeeResourceDirs(repoRoot = SCRIPTS_REPO_ROOT): string[] {
  const root = join(repoRoot, "employees");
  if (!existsSync(root)) return [];
  return readdirSync(root, { withFileTypes: true })
    .filter((entry) => entry.isDirectory() && existsSync(join(root, entry.name, "workflow.yaml")))
    .map((entry) => entry.name)
    .sort();
}

/** Electrobun build.copy 的完整映射：固定资源 + 每个员工目录。 */
export function desktopCopyMapping(repoRoot = SCRIPTS_REPO_ROOT): Record<string, string> {
  const copy: Record<string, string> = { ...FIXED_DESKTOP_COPY };
  for (const name of employeeResourceDirs(repoRoot)) {
    copy[`employees/${name}`] = `employees/${name}`;
  }
  return copy;
}

/** 构建期拷贝到仓库里的 resvg wasm 目标路径。 */
export function desktopWasmTarget(repoRoot = SCRIPTS_REPO_ROOT): string {
  return join(repoRoot, "packages", "desktop", "assets", "resvg.wasm");
}

/** 从 @de/tools 的依赖里定位 resvg wasm 源文件。 */
export function resolveResvgWasmSource(repoRoot = SCRIPTS_REPO_ROOT): string {
  const pkg = Bun.resolveSync("@resvg/resvg-wasm/package.json", join(repoRoot, "packages", "tools"));
  return join(dirname(pkg), "index_bg.wasm");
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

/** 跑一条子命令，失败即抛错（带标签与用时）。 */
export async function runStep(label: string, cmd: string[], cwd = SCRIPTS_REPO_ROOT): Promise<void> {
  console.log(`\n▶ ${label}`);
  const started = performance.now();
  const proc = Bun.spawn({ cmd, cwd, stdout: "inherit", stderr: "inherit" });
  const code = await proc.exited;
  if (code !== 0) throw new Error(`${label} 失败（退出码 ${code}）`);
  console.log(`  完成，用时 ${((performance.now() - started) / 1000).toFixed(1)}s`);
}

/** 构建前端产物 packages/web/dist：桌面窗口里就是这份（dist/ 不入库，干净检出必须先生成）。 */
export async function buildWebDist(repoRoot = SCRIPTS_REPO_ROOT): Promise<void> {
  await runStep("构建前端（@de/web）", [process.execPath, "run", "--filter", "@de/web", "build"], repoRoot);
}

/** 拷贝 resvg wasm：桌面主进程不能依赖 `with { type: "file" }`，随包带一份。返回目标路径。 */
export async function copyResvgWasm(repoRoot = SCRIPTS_REPO_ROOT): Promise<string> {
  const source = resolveResvgWasmSource(repoRoot);
  if (!existsSync(source)) throw new Error(`找不到 resvg wasm：${source}；先跑 bun install`);
  const target = desktopWasmTarget(repoRoot);
  mkdirSync(dirname(target), { recursive: true });
  cpSync(source, target);
  console.log(`  ${source} → ${target}（${(statSync(target).size / 1024).toFixed(0)}KB）`);
  return target;
}

/** 桌面要加载的全部构建期资源：前端产物 + resvg wasm（build 与 dev 共用）。 */
export async function prepareDesktopResources(repoRoot = SCRIPTS_REPO_ROOT): Promise<void> {
  await buildWebDist(repoRoot);
  console.log("\n▶ 拷贝 resvg wasm");
  await copyResvgWasm(repoRoot);
}
