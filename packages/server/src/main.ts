// 服务端启动入口：工作区 → 设置 → 工具子进程 → 模型 → 引擎 → 监听端口。
import { existsSync, mkdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import {
  createMcpToolClient,
  createModelProvider,
  createScriptedModelProvider,
  createTaskEngine,
  loadDemoSettings,
  loadSettings,
  resolveWorkspace,
  type EngineDeps,
  type FaultHooks,
  type ModelProvider,
  type ModelScript,
} from "@de/core";
import { runStdioServer } from "@de/tools";
import { initializeWorkspaceEmployees, toolServerCommand } from "./bootstrap";
import { createApp } from "./index";

async function start() {
  const workspaceDir = resolveWorkspace();
  mkdirSync(join(workspaceDir, "tasks"), { recursive: true });

  const repoRoot = fileURLToPath(new URL("../../..", import.meta.url));
  const defaultEmployeesDir = process.env.DE_EMPLOYEES_DIR ?? join(repoRoot, "employees");
  // 首次启动把默认员工配置复制进工作区（design.md §14）；业务人员改的是工作区里这一份。
  // 设置了 DE_EMPLOYEES_DIR（开发/测试）时直接用它，不初始化工作区副本。
  const workspaceEmployeesDir = join(workspaceDir, "employees");
  if (!process.env.DE_EMPLOYEES_DIR) initializeWorkspaceEmployees(defaultEmployeesDir, workspaceEmployeesDir);
  const employeesDir = process.env.DE_EMPLOYEES_DIR ?? workspaceEmployeesDir;

  // 演示开关持久化在 settings.json；运行时可变对象与引擎、设置路由共享。
  const demo = loadDemoSettings(workspaceDir);
  const faults: FaultHooks = {
    corruptTraceability: demo.corruptTraceability,
    crashOnNextStep: demo.crashOnNextStep,
    tools: demo.toolFaults,
  };

  // DE_SCRIPTED=<剧本.json>：用剧本式假模型代替真实模型（离线演示、联调，不发任何网络请求）
  const scriptFile = process.env.DE_SCRIPTED;
  const createProvider = (): ModelProvider =>
    scriptFile
      ? createScriptedModelProvider(JSON.parse(readFileSync(scriptFile, "utf8")) as ModelScript)
      : createModelProvider(loadSettings(workspaceDir));

  const { command, args } = toolServerCommand({
    execPath: process.execPath,
    toolsEntry: join(repoRoot, "packages", "tools", "src", "stdio.ts"),
  });
  const toolClient = await createMcpToolClient(new StdioClientTransport({ command, args, stderr: "inherit" }));

  // 启动时恢复上次演示留下的工具故障（设置页把开关持久化在 settings.json）
  try {
    await toolClient.call("admin_set_faults", { tools: demo.toolFaults });
  } catch (error) {
    console.warn(`工具故障开关恢复失败：${error instanceof Error ? error.message : String(error)}`);
  }

  // deps.model 由引擎在每个判断步骤读取：保存设置后替换它，对新的判断步骤生效
  const engineDeps: EngineDeps = { workspaceDir, employeesDir, toolClient, model: createProvider(), faults, exitOnCrash: true };
  const engine = createTaskEngine(engineDeps);
  await engine.resumeAll();

  const defaultWebDist = join(repoRoot, "packages", "web", "dist");
  const webDistDir = process.env.DE_WEB_DIST ?? (existsSync(defaultWebDist) ? defaultWebDist : undefined);
  const port = Number(process.env.DE_PORT ?? process.env.PORT ?? 3000);
  // SSE 是长连接，Bun 默认 10 秒空闲就会断开
  createApp({
    engine,
    employeesDir,
    workspaceDir,
    toolClient,
    webDistDir,
    faults,
    defaultEmployeesDir,
    onSettingsSaved(settings) {
      // 剧本模式下保持剧本模型不变；真实模式下重建，对新的判断步骤生效
      if (!scriptFile) engineDeps.model = createModelProvider(settings);
    },
  }).listen({ port, idleTimeout: 120 });
  console.log(`digital-employee 服务已启动：http://localhost:${port}（工作区 ${workspaceDir}${scriptFile ? `，剧本式模型 ${scriptFile}` : ""}）`);

  const shutdown = async () => {
    await toolClient.close();
    process.exit(0);
  };
  process.on("SIGINT", shutdown);
  process.on("SIGTERM", shutdown);
}

if (process.argv[2] === "mcp-server") {
  await runStdioServer();
} else {
  await start();
}
