// 构建桌面安装包：
//   检查 Hutch → 构建前端 → 拷贝 resvg wasm → hutch electrobun build --env=stable
//   → 解压出可运行的绿色目录 dist/desktop/DigitalEmployee → 打便携 zip → 打印体积
//   bun run build:desktop
// Hutch 是构建期工具（机器级安装），不进 package.json；中文用户名下需把 HUTCH_HOME 指到 ASCII 路径。
import { cpSync, existsSync, mkdirSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import { basename, dirname, join } from "node:path";
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

/** Electrobun 的构建输出目录（staging）：build/<channel>-win-x64/<AppName>。 */
export const DESKTOP_CHANNEL = "stable";
export const DESKTOP_APP_NAME = "DigitalEmployee";

export function desktopStagingAppDir(repoRoot = REPO_ROOT, channel = DESKTOP_CHANNEL): string {
  return join(repoRoot, "build", `${channel}-win-x64`, DESKTOP_APP_NAME);
}

/** staging 里真正的应用内容（安装器会解压到安装目录的那份 tar.zst）；没有就返回 undefined。 */
export function findResourcesTar(stagingAppDir: string): string | undefined {
  const resources = join(stagingAppDir, "Resources");
  if (!existsSync(resources)) return undefined;
  const tar = readdirSync(resources)
    .filter((name) => name.endsWith(".tar.zst"))
    .sort()[0];
  return tar ? join(resources, tar) : undefined;
}

/** 解压出来的绿色目录（dist/desktop/DigitalEmployee，含 bin/launcher.exe）。 */
export function portableAppDir(artifactFolder = desktopArtifactFolder()): string {
  return join(artifactFolder, DESKTOP_APP_NAME);
}

/** 免安装便携包（整个绿色目录压成一个 zip）。 */
export function portableZipPath(artifactFolder = desktopArtifactFolder()): string {
  return join(artifactFolder, `win-x64-${DESKTOP_APP_NAME}-portable.zip`);
}

/** 目录下所有文件的字节数（解压后/安装后的真实体积）。 */
export function dirSize(dir: string): number {
  if (!existsSync(dir)) return 0;
  let total = 0;
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) total += dirSize(path);
    else if (entry.isFile()) total += statSync(path).size;
  }
  return total;
}

/**
 * 把 staging 的 Resources/*.tar.zst 解压成可运行的绿色目录（<destRoot>/DigitalEmployee）。
 * 解压后校验 bin/launcher.exe 存在，避免“解压成功但跑不起来”被当成成功。
 * 返回绿色目录路径。
 */
export async function extractPortableApp(tarFile: string, destRoot: string): Promise<string> {
  const appDir = join(destRoot, DESKTOP_APP_NAME);
  rmSync(appDir, { recursive: true, force: true });
  mkdirSync(destRoot, { recursive: true });
  // 临时 tar 放在产物目录里（仓库路径是 ASCII），避开中文 %TEMP% 与解压器编码问题
  const tmpTar = join(destRoot, ".desktop-install.tar");
  writeFileSync(tmpTar, Bun.zstdDecompressSync(await Bun.file(tarFile).arrayBuffer()));
  try {
    const code = await Bun.spawn({
      cmd: ["tar", "-xf", tmpTar, "-C", destRoot],
      stdout: "inherit",
      stderr: "inherit",
    }).exited;
    if (code !== 0) throw new Error(`解压安装内容失败（tar 退出码 ${code}）：${tarFile}`);
  } finally {
    rmSync(tmpTar, { force: true });
  }
  if (!existsSync(join(appDir, "bin", "launcher.exe"))) {
    throw new Error(`解压后没有 bin/launcher.exe：${appDir}`);
  }
  return appDir;
}

/**
 * 把绿色目录打成便携 zip。
 * 不用 bsdtar：它的 zip 写入器会把中文文件名转成 CP437（样例文件名变成 ????），
 * 改用 .NET ZipFile（PowerShell 自带），条目名按 UTF-8 写入。
 */
export async function createPortableZip(appDir: string, zipFile: string): Promise<void> {
  if (basename(appDir) !== DESKTOP_APP_NAME) throw new Error(`便携 zip 的根目录应为 ${DESKTOP_APP_NAME}：${appDir}`);
  rmSync(zipFile, { force: true });
  const code = await Bun.spawn({
    cmd: [
      "powershell",
      "-NoProfile",
      "-NonInteractive",
      "-ExecutionPolicy",
      "Bypass",
      "-Command",
      "Add-Type -AssemblyName System.IO.Compression.FileSystem; " +
        "[System.IO.Compression.ZipFile]::CreateFromDirectory($env:DE_ZIP_SRC, $env:DE_ZIP_DST, " +
        "[System.IO.Compression.CompressionLevel]::Optimal, $true) | Out-Null",
    ],
    env: { ...process.env, DE_ZIP_SRC: appDir, DE_ZIP_DST: zipFile },
    stdout: "inherit",
    stderr: "inherit",
  }).exited;
  if (code !== 0) throw new Error(`打便携 zip 失败（PowerShell 退出码 ${code}）：${zipFile}`);
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

  // 绿色目录：staging 的 Resources/*.tar.zst 正是安装器解压到安装目录的内容
  const installedDir = portableAppDir(desktopArtifactFolder());
  let portableZip: string | undefined;
  const resourcesTar = findResourcesTar(desktopStagingAppDir());
  if (resourcesTar) {
    console.log("\n▶ 解压可运行的绿色目录");
    const appDir = await extractPortableApp(resourcesTar, desktopArtifactFolder());
    console.log(`  ${appDir}（${formatSize(dirSize(appDir))}，${dirSize(appDir)} 字节）`);

    console.log("\n▶ 打便携 zip（免安装）");
    portableZip = portableZipPath(desktopArtifactFolder());
    await createPortableZip(appDir, portableZip);
    console.log(`  ${portableZip}（${formatSize(statSync(portableZip).size)}，${statSync(portableZip).size} 字节）`);
  } else {
    console.log(`\n⚠ ${desktopStagingAppDir()} 下没有 Resources/*.tar.zst，跳过绿色目录与便携 zip（只交付安装包）`);
  }

  console.log("\n✅ 桌面产物：");
  for (const file of artifacts) console.log(`   安装包 ${file}（${formatSize(statSync(file).size)}，${statSync(file).size} 字节）`);
  if (portableZip) console.log(`   便携包 ${portableZip}（${formatSize(statSync(portableZip).size)}，${statSync(portableZip).size} 字节）`);
  if (existsSync(installedDir)) console.log(`   绿色目录 ${installedDir}（${formatSize(dirSize(installedDir))}，${dirSize(installedDir)} 字节）`);
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
