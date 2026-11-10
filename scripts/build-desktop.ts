// 构建桌面安装包：
//   检查 Hutch → 准备前端产物与 resvg wasm → hutch electrobun build --env=stable
//   → 解压出可运行的绿色目录 dist/desktop/DigitalEmployee → 打便携 zip → 校验产物 → 打印体积
//   bun run build:desktop
// 资源准备（前端 + wasm）与 dev 模式共用 desktop-resources.ts，保证 clean checkout 两条路都可用。
// Hutch 是构建期工具（机器级安装），不进 package.json；中文用户名下需把 HUTCH_HOME 指到 ASCII 路径。
import { existsSync, mkdirSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import { basename, join } from "node:path";
import { REPO_ROOT } from "./gen-embedded";
import { prepareDesktopResources, hutchMissingMessage, runStep } from "./desktop-resources";

// 资源映射与资源准备与 electrobun.config.ts / dev-desktop.ts 共用（见 desktop-resources.ts），这里 re-export 便于单测。
export {
  FIXED_DESKTOP_COPY,
  desktopCopyMapping,
  desktopWasmTarget,
  employeeResourceDirs,
  hutchMissingMessage,
  resolveResvgWasmSource,
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

/** 本次构建应交付的产物路径（校验用）。 */
export interface DesktopArtifactPaths {
  /** 安装包（artifactFolder 下的 *-Setup.zip） */
  setupZips: string[];
  /** staging 的 Resources/*.tar.zst（绿色目录的来源） */
  resourcesTar?: string;
  /** 绿色目录（应含 bin/launcher.exe） */
  portableAppDir: string;
  /** 便携 zip */
  portableZip: string;
}

/**
 * 校验产物齐全：README/设计承诺安装包 + 绿色目录 + 便携 zip，缺任何一项都不算构建成功。
 * 返回每个缺项的“缺什么 + 下一步怎么办”；空数组表示齐全。
 * 不接受“只打个警告然后输出 ✅ 退出 0”——那样冒烟脚本默认指向的绿色目录可能根本不存在。
 */
export function desktopArtifactProblems(
  paths: DesktopArtifactPaths,
  exists: (path: string) => boolean = existsSync,
): string[] {
  const problems: string[] = [];
  if (paths.setupZips.length === 0) {
    problems.push(
      "缺少安装包（*-Setup.zip）：重跑 bun run build:desktop；仍缺就检查 hutch electrobun build 的输出与 Hutch 版本（hutch --version）",
    );
  }
  if (!paths.resourcesTar) {
    problems.push(
      `缺少 staging 的 Resources/*.tar.zst（${desktopStagingAppDir()}）：重跑 bun run build:desktop；仍缺就确认 Hutch 版本与 build/stable-win-x64 目录是否生成`,
    );
  }
  if (!exists(join(paths.portableAppDir, "bin", "launcher.exe"))) {
    problems.push(
      `缺少可运行的绿色目录（${paths.portableAppDir} 下没有 bin/launcher.exe）：重跑 bun run build:desktop，并检查上面的解压日志`,
    );
  }
  if (!exists(paths.portableZip)) {
    problems.push(
      `缺少便携 zip（${paths.portableZip}）：重跑 bun run build:desktop；若 PowerShell 打包失败，看上面的输出定位原因`,
    );
  }
  return problems;
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

export function formatSize(bytes: number): string {
  return `${(bytes / 1024 / 1024).toFixed(1)}MB`;
}

/** 产物目录里的 Setup.zip（按名字排序，便于稳定输出）。 */
export function findSetupArtifacts(dir: string): string[] {
  if (!existsSync(dir)) return [];
  return readdirSync(dir, { withFileTypes: true })
    .filter((entry) => entry.isFile() && /-Setup\.zip$/i.test(entry.name))
    .map((entry) => join(dir, entry.name))
    .sort();
}

async function main(): Promise<void> {
  const started = performance.now();
  const hutch = Bun.which("hutch");
  if (!hutch) throw new Error(hutchMissingMessage());

  // 1. 前端产物与 resvg wasm（与 dev 共用同一份准备逻辑）
  await prepareDesktopResources();

  // 2. Hutch 打包（首次会拉取 Electrobun devkit / Bun 运行时，需要网络）
  await runStep("打包桌面应用（Hutch + Electrobun）", [hutch, "electrobun", "build", "--env=stable"]);

  const artifactFolder = desktopArtifactFolder();
  const artifacts = findSetupArtifacts(artifactFolder);

  // 3. 绿色目录：staging 的 Resources/*.tar.zst 正是安装器解压到安装目录的内容
  const resourcesTar = findResourcesTar(desktopStagingAppDir());
  const appDir = portableAppDir(artifactFolder);
  const portableZip = portableZipPath(artifactFolder);
  if (resourcesTar) {
    console.log("\n▶ 解压可运行的绿色目录");
    const extracted = await extractPortableApp(resourcesTar, artifactFolder);
    console.log(`  ${extracted}（${formatSize(dirSize(extracted))}，${dirSize(extracted)} 字节）`);

    console.log("\n▶ 打便携 zip（免安装）");
    await createPortableZip(extracted, portableZip);
    console.log(`  ${portableZip}（${formatSize(statSync(portableZip).size)}，${statSync(portableZip).size} 字节）`);
  }

  // 4. 产物必须齐全：缺绿色目录或便携 zip 时按失败退出，不静默降级
  const problems = desktopArtifactProblems({ setupZips: artifacts, resourcesTar, portableAppDir: appDir, portableZip });
  if (problems.length > 0) {
    throw new Error(["桌面产物不完整（构建按失败处理）：", ...problems.map((line) => `  - ${line}`)].join("\n"));
  }

  console.log("\n✅ 桌面产物：");
  for (const file of artifacts) console.log(`   安装包 ${file}（${formatSize(statSync(file).size)}，${statSync(file).size} 字节）`);
  console.log(`   便携包 ${portableZip}（${formatSize(statSync(portableZip).size)}，${statSync(portableZip).size} 字节）`);
  console.log(`   绿色目录 ${appDir}（${formatSize(dirSize(appDir))}，${dirSize(appDir)} 字节）`);
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
