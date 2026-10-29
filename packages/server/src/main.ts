// 服务端启动入口：工作区 → 设置 → 工具子进程 → 模型 → 引擎 → 监听端口。
// 真实引擎在 T1 合并前会抛 "not implemented"，由 T7 集成。
import { existsSync, mkdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import {
  createMcpToolClient,
  createModelProvider,
  createScriptedModelProvider,
  createTaskEngine,
  loadSettings,
  resolveWorkspace,
  type ModelScript,
} from "@de/core";
import { runStdioServer } from "@de/tools";
import { toolServerCommand } from "./bootstrap";
import { createApp } from "./index";

async function start() {
  const workspaceDir = resolveWorkspace();
  mkdirSync(join(workspaceDir, "tasks"), { recursive: true });
  // DE_SCRIPTED=<剧本.json>：用剧本式假模型代替真实模型（离线演示、联调，不发任何网络请求）
  const scriptFile = process.env.DE_SCRIPTED;
  const model = scriptFile
    ? createScriptedModelProvider(JSON.parse(readFileSync(scriptFile, "utf8")) as ModelScript)
    : createModelProvider(loadSettings(workspaceDir));

  const repoRoot = fileURLToPath(new URL("../../..", import.meta.url));
  const employeesDir =
    process.env.DE_EMPLOYEES_DIR ??
    (existsSync(join(workspaceDir, "employees")) ? join(workspaceDir, "employees") : join(repoRoot, "employees"));
  const defaultWebDist = join(repoRoot, "packages", "web", "dist");
  const webDistDir = process.env.DE_WEB_DIST ?? (existsSync(defaultWebDist) ? defaultWebDist : undefined);

  const { command, args } = toolServerCommand({
    execPath: process.execPath,
    toolsEntry: join(repoRoot, "packages", "tools", "src", "stdio.ts"),
  });
  const toolClient = await createMcpToolClient(new StdioClientTransport({ command, args, stderr: "inherit" }));

  const engine = createTaskEngine({ workspaceDir, employeesDir, toolClient, model });
  await engine.resumeAll();

  const port = Number(process.env.DE_PORT ?? process.env.PORT ?? 3000);
  // SSE 是长连接，Bun 默认 10 秒空闲就会断开
  createApp({ engine, employeesDir, workspaceDir, toolClient, webDistDir }).listen({ port, idleTimeout: 120 });
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
