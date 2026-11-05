// 桌面版的后端装配（纯逻辑，可在普通 Bun 进程里单测）：
// 资源目录 → 共享装配（工作区准备 + 服务端）→ 返回窗口要加载的地址与关闭函数。
// 工具服务器在同一个进程里通过 MCP InMemoryTransport 运行，不起子进程；
// 只监听回环、不打开浏览器、不注册进程信号（生命周期交给 Electrobun 宿主）。
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import { createMcpToolClient, resolveWorkspace, type ToolClient } from "@de/core";
import { serveApp } from "@de/app";
import { createToolsServer } from "@de/tools";
import { resolveDesktopResvgWasm, type DesktopPathEnv } from "./paths";

export interface DesktopBackendOptions {
  /** 构建期资源目录（web / employees / samples / assets） */
  assetsDir: string;
  /** 工作区根目录，缺省 resolveWorkspace()（%APPDATA%/digital-employee，DE_WORKSPACE 可覆盖） */
  workspaceDir?: string;
  /** 首选端口，缺省 DE_PORT / PORT / 8787 */
  port?: number;
  /** 首选端口被占用时往后试几个，缺省 20 */
  portAttempts?: number;
  /** 监听地址，缺省 127.0.0.1（只监听回环） */
  hostname?: string;
  /** 剧本式假模型文件，缺省 DE_SCRIPTED */
  scriptFile?: string;
  /** resvg wasm 路径，缺省 DE_RESVG_WASM / <资源根>/assets/resvg.wasm */
  resvgWasm?: string;
  /** 环境变量（测试注入） */
  env?: DesktopPathEnv;
  log?: (line: string) => void;
}

export interface DesktopBackend {
  url: string;
  port: number;
  workspaceDir: string;
  toolClient: ToolClient;
  /** 先停 HTTP（释放端口），再关工具客户端；可重复调用 */
  close(): Promise<void>;
}

export async function startDesktopBackend(options: DesktopBackendOptions): Promise<DesktopBackend> {
  const env = options.env ?? process.env;
  const workspaceDir = options.workspaceDir ?? resolveWorkspace();
  const hostname = options.hostname ?? "127.0.0.1";
  const resvgWasm = options.resvgWasm ?? resolveDesktopResvgWasm(options.assetsDir, env);

  // 同一个进程里的工具服务器：wasm 读随包拷贝的资源，不依赖 Bun 的编译期文件导入
  const tools = createToolsServer({ resvgWasm });
  const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  await tools.server.connect(serverTransport);
  const toolClient = await createMcpToolClient(clientTransport);

  let server: Awaited<ReturnType<typeof serveApp>>;
  try {
    server = await serveApp({
      source: { kind: "directory", root: options.assetsDir },
      workspaceDir,
      hostname,
      open: false,
      handleSignals: false,
      toolClient,
      port: options.port,
      portAttempts: options.portAttempts,
      scriptFile: options.scriptFile,
      log: options.log,
    });
  } catch (error) {
    await toolClient.close();
    throw error;
  }

  let closing: Promise<void> | undefined;
  const close = (): Promise<void> => {
    closing ??= (async () => {
      // serveApp 拿到的是注入的客户端，不会替我们关；这里先停 HTTP，再关工具客户端
      await server.close();
      await toolClient.close();
    })();
    return closing;
  };

  return {
    url: server.url,
    port: server.port,
    workspaceDir,
    toolClient,
    close,
  };
}
