// exe 的主流程：准备工作区（释放嵌入资源）→ 启动服务端（端口 8787 起，冲突往后找）→ 打开浏览器。
import { join } from "node:path";
import { resolveWorkspace } from "@de/core";
import { toolServerCommand } from "@de/server/bootstrap";
import { startServer, type RunningServer } from "@de/server/start";
import { EMBEDDED_BUNDLES } from "./embedded";
import { openBrowser } from "./open-browser";
import { prepareWorkspace, type EmbeddedBundlePaths } from "./workspace";

/** 默认端口（设计 §14）；被占用时从它开始往后找 20 个。 */
export const DEFAULT_PORT = 8787;
const PORT_ATTEMPTS = 20;

export interface ServeOptions {
  /** 嵌入资源归档；测试可传入临时归档 */
  bundles?: EmbeddedBundlePaths;
  /** 是否自动打开浏览器，缺省看 DE_NO_OPEN（=1 时不打开） */
  open?: boolean;
}

export async function serveApp(options: ServeOptions = {}): Promise<RunningServer> {
  const workspaceDir = resolveWorkspace();
  const prepared = await prepareWorkspace({ workspaceDir, bundles: options.bundles ?? EMBEDDED_BUNDLES });

  const server = await startServer({
    workspaceDir,
    employeesDir: process.env.DE_EMPLOYEES_DIR ?? prepared.employeesDir,
    defaultEmployeesDir: prepared.defaultEmployeesDir,
    webDistDir: process.env.DE_WEB_DIST ?? prepared.webDistDir,
    // exe 里 process.execPath 就是自己：工具子进程用 `digital-employee.exe mcp-server` 启动
    toolServer: toolServerCommand({ execPath: process.execPath, toolsEntry: join(import.meta.dir, "../../tools/src/stdio.ts") }),
    port: Number(process.env.DE_PORT ?? process.env.PORT ?? DEFAULT_PORT),
    portAttempts: PORT_ATTEMPTS,
    scriptFile: process.env.DE_SCRIPTED,
  });

  if (options.open ?? process.env.DE_NO_OPEN !== "1") openBrowser(server.url);
  return server;
}
