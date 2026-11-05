// 应用主流程：资源 → 工作区 → 服务端（可选打开浏览器）。
// 资源来源与是否打开浏览器、监听地址、工具客户端都由调用方决定：
//   exe 默认传归档、打开浏览器；桌面传目录、只监听回环、注入 InMemoryTransport 工具客户端。
import { join } from "node:path";
import { resolveWorkspace, type ToolClient } from "@de/core";
import { toolServerCommand } from "@de/server/bootstrap";
import { startServer, type RunningServer } from "@de/server/start";
import { type AppResourceSource, type EmbeddedBundlePaths } from "./resources";
import { openBrowser } from "./open-browser";
import { prepareWorkspace } from "./workspace";

/** 默认端口（设计 §14）；被占用时从它开始往后找 20 个。 */
export const DEFAULT_PORT = 8787;
const PORT_ATTEMPTS = 20;

export interface ServeOptions {
  /** 资源来源（归档或目录）；不传时用 bundles */
  source?: AppResourceSource;
  /** 嵌入资源归档（exe）；等价于 archive source */
  bundles?: EmbeddedBundlePaths;
  /** 工作区根目录，缺省 resolveWorkspace()（DE_WORKSPACE 可覆盖） */
  workspaceDir?: string;
  /** 首选端口，缺省 DE_PORT / PORT / 8787 */
  port?: number;
  /** 首选端口被占用时往后试几个，缺省 20 */
  portAttempts?: number;
  /** 监听地址；缺省由 Bun 决定。桌面只传 127.0.0.1 */
  hostname?: string;
  /** 剧本式假模型文件，缺省 DE_SCRIPTED */
  scriptFile?: string;
  /** 已建好的工具客户端（桌面 MCP InMemoryTransport）；不传则起工具子进程 */
  toolClient?: ToolClient;
  /** 注册 SIGINT/SIGTERM 收尾，缺省 true；桌面交给宿主，传 false */
  handleSignals?: boolean;
  /** 模拟崩溃要真的退出进程，缺省 true */
  exitOnCrash?: boolean;
  /** 是否自动打开浏览器，缺省看 DE_NO_OPEN（=1 时不打开）；桌面传 false */
  open?: boolean;
  log?: (line: string) => void;
}

export async function serveApp(options: ServeOptions = {}): Promise<RunningServer> {
  const workspaceDir = options.workspaceDir ?? resolveWorkspace();
  const source = options.source ?? (options.bundles ? { kind: "archive" as const, bundles: options.bundles } : undefined);
  if (!source) throw new Error("serveApp：需要资源来源（source 或 bundles）");
  const prepared = await prepareWorkspace({ workspaceDir, source });

  const serverOptions = {
    workspaceDir,
    employeesDir: process.env.DE_EMPLOYEES_DIR ?? prepared.employeesDir,
    defaultEmployeesDir: prepared.defaultEmployeesDir,
    webDistDir: process.env.DE_WEB_DIST ?? prepared.webDistDir,
    port: options.port ?? Number(process.env.DE_PORT ?? process.env.PORT ?? DEFAULT_PORT),
    portAttempts: options.portAttempts ?? PORT_ATTEMPTS,
    scriptFile: options.scriptFile ?? process.env.DE_SCRIPTED,
    hostname: options.hostname,
    handleSignals: options.handleSignals,
    exitOnCrash: options.exitOnCrash,
    log: options.log,
  };

  const server = await startServer(
    options.toolClient
      ? { ...serverOptions, toolClient: options.toolClient }
      : {
          ...serverOptions,
          // exe 里 process.execPath 就是自己：工具子进程用 `digital-employee.exe mcp-server` 启动
          toolServer: toolServerCommand({
            execPath: process.execPath,
            toolsEntry: join(import.meta.dir, "../../tools/src/stdio.ts"),
          }),
        },
  );

  if (options.open ?? process.env.DE_NO_OPEN !== "1") openBrowser(server.url);
  return server;
}
