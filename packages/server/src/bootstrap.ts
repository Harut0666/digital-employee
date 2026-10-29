// 启动期的纯函数：工具服务器子进程命令、工作区首次初始化时的文件释放。
// 工作区与设置的读取在 @de/core（loadSettings、resolveWorkspace）。
import { cpSync, existsSync, mkdirSync, readdirSync, writeFileSync } from "node:fs";
import { basename, dirname, join, sep } from "node:path";

/**
 * 工具服务器子进程的启动命令。开发时 `bun packages/tools/src/stdio.ts`；
 * 打包后的 exe 用自身加 mcp-server 子命令启动。
 */
export function toolServerCommand(opts: { execPath: string; toolsEntry: string }): { command: string; args: string[] } {
  const isBun = /^bun(\.exe)?$/i.test(basename(opts.execPath.replace(/\\/g, "/")));
  return isBun ? { command: opts.execPath, args: [opts.toolsEntry] } : { command: opts.execPath, args: ["mcp-server"] };
}

/**
 * 把默认员工配置复制到目标目录：只复制含 workflow.yaml 的员工目录，
 * 不复制 employees/ 下的 node_modules、test 等非员工内容。
 */
export function copyEmployeeConfigs(source: string, target: string): string[] {
  mkdirSync(target, { recursive: true });
  const copied: string[] = [];
  for (const entry of readdirSync(source, { withFileTypes: true })) {
    if (!entry.isDirectory()) continue;
    const from = join(source, entry.name);
    if (!existsSync(join(from, "workflow.yaml"))) continue;
    cpSync(from, join(target, entry.name), { recursive: true });
    copied.push(entry.name);
  }
  return copied.sort();
}

/** 首次启动时把仓库里的默认员工配置复制到工作区；已存在则不动。 */
export function initializeWorkspaceEmployees(defaultDir: string, workspaceEmployeesDir: string): void {
  if (existsSync(workspaceEmployeesDir) || !existsSync(defaultDir)) return;
  copyEmployeeConfigs(defaultDir, workspaceEmployeesDir);
}

export interface ReleaseFile {
  /** 工作区相对路径，统一用 / 分隔 */
  path: string;
  data: Uint8Array;
}

export interface ReleaseOutcome {
  written: string[];
  skipped: string[];
}

/** 释放路径必须是工作区内的相对路径：绝对路径、盘符、`..` 段一律拒绝。 */
function safeRelativePath(raw: string): string {
  const normalized = raw.replace(/\\/g, "/");
  const illegal = () => {
    throw new Error(`释放资源失败：非法路径 ${JSON.stringify(raw)}`);
  };
  if (normalized === "" || normalized.startsWith("/") || /^[a-zA-Z]:/.test(normalized)) illegal();
  const parts = normalized.split("/").filter((p) => p !== "" && p !== ".");
  if (parts.length === 0 || parts.some((p) => p === "..")) illegal();
  return parts.join(sep);
}

/**
 * 把嵌入资源释放到工作区（exe 首次启动用）。默认不覆盖已有文件：
 * 用户改过的员工配置、样例不会被升级覆盖；内部运行时资源（前端构建产物）用 overwrite 换新版。
 */
export function releaseFiles(files: ReleaseFile[], targetRoot: string, options: { overwrite?: boolean } = {}): ReleaseOutcome {
  const written: string[] = [];
  const skipped: string[] = [];
  for (const file of files) {
    const rel = safeRelativePath(file.path);
    const target = join(targetRoot, rel);
    if (!options.overwrite && existsSync(target)) {
      skipped.push(file.path);
      continue;
    }
    mkdirSync(dirname(target), { recursive: true });
    writeFileSync(target, file.data);
    written.push(file.path);
  }
  return { written, skipped };
}

/** 工作区里没有 settings.json 时写一份模板（模板里不含任何 API Key）；已有则原样保留。 */
export function writeDefaultSettings(workspaceDir: string, template: Record<string, unknown>): "written" | "kept" {
  const file = join(workspaceDir, "settings.json");
  if (existsSync(file)) return "kept";
  mkdirSync(dirname(file), { recursive: true });
  writeFileSync(file, `${JSON.stringify(template, null, 2)}\n`);
  return "written";
}
