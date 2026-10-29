// 启动期的纯函数：工具服务器子进程命令。工作区与设置的读取在 @de/core（loadSettings、resolveWorkspace）。
import { basename } from "node:path";

/**
 * 工具服务器子进程的启动命令。开发时 `bun packages/tools/src/stdio.ts`；
 * 打包后的 exe 用自身加 mcp-server 子命令启动。
 */
export function toolServerCommand(opts: { execPath: string; toolsEntry: string }): { command: string; args: string[] } {
  const isBun = /^bun(\.exe)?$/i.test(basename(opts.execPath.replace(/\\/g, "/")));
  return isBun ? { command: opts.execPath, args: [opts.toolsEntry] } : { command: opts.execPath, args: ["mcp-server"] };
}
