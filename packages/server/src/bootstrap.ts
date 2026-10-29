// 启动期的纯函数：工具服务器子进程命令、工作区首次初始化时的员工配置复制。
// 工作区与设置的读取在 @de/core（loadSettings、resolveWorkspace）。
import { cpSync, existsSync, mkdirSync, readdirSync } from "node:fs";
import { basename, join } from "node:path";

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
