// 服务端启动入口：工作区 → 设置 → 工具子进程 → 模型 → 引擎 → 监听端口。
// 真实引擎在 T1 合并前会抛 "not implemented"，由 T7 集成。
import { existsSync, mkdirSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import { createMcpToolClient, createModelProvider, createTaskEngine } from "@de/core";
import { runStdioServer } from "@de/tools";
import { loadSettings, resolveWorkspace, toolServerCommand } from "./bootstrap";
import { createApp } from "./index";

async function start() {
  const workspaceDir = resolveWorkspace();
  mkdirSync(join(workspaceDir, "tasks"), { recursive: true });
  const settings = loadSettings(workspaceDir);

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

  const engine = createTaskEngine({ workspaceDir, employeesDir, toolClient, model: createModelProvider(settings) });
  await engine.resumeAll();

  const port = Number(process.env.DE_PORT ?? process.env.PORT ?? 3000);
  // SSE 是长连接，Bun 默认 10 秒空闲就会断开
  createApp({ engine, employeesDir, webDistDir }).listen({ port, idleTimeout: 120 });
  console.log(`digital-employee 服务已启动：http://localhost:${port}（工作区 ${workspaceDir}）`);

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
