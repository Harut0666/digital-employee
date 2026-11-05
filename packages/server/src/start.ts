// 服务端启动的主流程（开发入口和打包后的 exe 共用）：
// 工作区 → 演示开关 → 工具子进程 → 模型 → 引擎 → 选端口 → 监听 → 打印地址。
// 只负责“跑起来”；路径准备（源码目录 / 嵌入资源释放）由调用方完成。
import { mkdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import {
  createMcpToolClient,
  createModelProvider,
  createScriptedModelProvider,
  createTaskEngine,
  loadSettings,
  readSettingsDocument,
  resolveWorkspace,
  type EngineDeps,
  type FaultHooks,
  type ModelProvider,
  type ModelScript,
  type ModelSettings,
  type TaskEngine,
  type ToolClient,
} from "@de/core";
import { createApp } from "./index";
import { choosePort, isPortAvailable } from "./port";
import { consoleLogger, createLogger, setConsoleLogLevel, type Logger } from "./log";

export interface StartServerOptions {
  /** 工作区根目录，缺省 resolveWorkspace()（%APPDATA%/digital-employee，DE_WORKSPACE 可覆盖） */
  workspaceDir?: string;
  /** 员工配置目录（工作区里的副本） */
  employeesDir: string;
  /** “恢复默认员工配置”的来源；缺省同 employeesDir */
  defaultEmployeesDir?: string;
  /** 前端构建产物目录；不传则不托管静态资源 */
  webDistDir?: string;
  /** 工具服务器子进程命令（开发：bun + stdio.ts；exe：自身 + mcp-server） */
  toolServer: { command: string; args: string[] };
  /** 首选端口，缺省 8787 */
  port?: number;
  /** 首选端口被占用时往后试几个端口，缺省 1（不换端口，直接失败） */
  portAttempts?: number;
  /** 剧本式假模型文件（DE_SCRIPTED）；不传则按 settings.json 连真实模型 */
  scriptFile?: string;
  /** 模拟崩溃要真的退出进程（生产/exe 为 true） */
  exitOnCrash?: boolean;
  /** 注册 SIGINT/SIGTERM 收尾，缺省 true */
  handleSignals?: boolean;
  log?: (line: string) => void;
}

export interface RunningServer {
  engine: TaskEngine;
  toolClient: ToolClient;
  port: number;
  url: string;
  close(): Promise<void>;
}

/** 启动服务端并开始监听；返回后可继续做别的事（例如打开浏览器）。 */
export async function startServer(options: StartServerOptions): Promise<RunningServer> {
  const workspaceDir = options.workspaceDir ?? resolveWorkspace();
  mkdirSync(join(workspaceDir, "tasks"), { recursive: true });

  // 诊断日志级别（Spec 0004）：启动时按 settings.json 生效；
  // 设置页保存 diagnostics.logLevel 后由设置路由就地调整（不重启也生效）。
  const settingsDoc = readSettingsDocument(workspaceDir);
  setConsoleLogLevel(settingsDoc.diagnostics.logLevel);
  const logLine = options.log;
  const logger: Logger = logLine
    ? createLogger({ level: settingsDoc.diagnostics.logLevel, sink: (_level, line) => logLine(line) })
    : consoleLogger;
  logger.debug(
    `设置文档：v${settingsDoc.version} revision ${settingsDoc.revision}；日志级别 ${settingsDoc.diagnostics.logLevel}`,
  );

  // 演示开关持久化在 settings.json；运行时可变对象与引擎、设置路由共享。
  const demo = settingsDoc.demo;
  const faults: FaultHooks = {
    corruptTraceability: demo.corruptTraceability,
    crashOnNextStep: demo.crashOnNextStep,
    tools: demo.toolFaults,
  };

  const scriptFile = options.scriptFile;
  const createProvider = (settings?: ModelSettings): ModelProvider =>
    scriptFile
      ? createScriptedModelProvider(JSON.parse(readFileSync(scriptFile, "utf8")) as ModelScript)
      : createModelProvider(settings ?? loadSettings(workspaceDir));

  const toolClient = await createMcpToolClient(
    new StdioClientTransport({ command: options.toolServer.command, args: options.toolServer.args, stderr: "inherit" }),
  );
  logger.debug(`工具子进程：${options.toolServer.command} ${options.toolServer.args.join(" ")}`);

  // 启动时恢复上次演示留下的工具故障（设置页把开关持久化在 settings.json）
  try {
    await toolClient.call("admin_set_faults", { tools: demo.toolFaults });
  } catch (error) {
    logger.warn(`工具故障开关恢复失败：${error instanceof Error ? error.message : String(error)}`);
  }

  // deps.model 由引擎在每个判断步骤读取：保存设置后替换它，对新的判断步骤生效
  const engineDeps: EngineDeps = {
    workspaceDir,
    employeesDir: options.employeesDir,
    toolClient,
    model: createProvider(),
    faults,
    exitOnCrash: options.exitOnCrash ?? true,
  };
  const engine = createTaskEngine(engineDeps);
  await engine.resumeAll();

  const port = await choosePort({
    start: options.port ?? 8787,
    attempts: options.portAttempts ?? 1,
    isAvailable: isPortAvailable,
  });
  const app = createApp({
    engine,
    employeesDir: options.employeesDir,
    workspaceDir,
    toolClient,
    webDistDir: options.webDistDir,
    faults,
    defaultEmployeesDir: options.defaultEmployeesDir ?? options.employeesDir,
    onSettingsSaved(settings) {
      // 剧本模式下保持剧本模型不变；真实模式下重建，对新的判断步骤生效
      if (!scriptFile) engineDeps.model = createProvider(settings);
    },
  });
  // SSE 是长连接，Bun 默认 10 秒空闲就会断开
  app.listen({ port, idleTimeout: 120 });

  const url = `http://localhost:${port}`;
  logger.info(
    `digital-employee 服务已启动：${url}（工作区 ${workspaceDir}${scriptFile ? `，剧本式模型 ${scriptFile}` : ""}）`,
  );
  logger.debug(
    `监听端口 ${port}（首选 ${options.port ?? 8787}，最多尝试 ${options.portAttempts ?? 1} 个）；员工目录 ${options.employeesDir}`,
  );

  const close = async () => {
    await toolClient.close();
  };
  if (options.handleSignals ?? true) {
    const shutdown = async () => {
      await close();
      process.exit(0);
    };
    process.on("SIGINT", shutdown);
    process.on("SIGTERM", shutdown);
  }

  return { engine, toolClient, port, url, close };
}
