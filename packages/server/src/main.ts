// 源码方式启动入口：`bun run dev:server`。打包后的 exe 走 packages/app/src/main.ts，两者共用 startServer。
import { existsSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { resolveWorkspace } from "@de/core";
import { runStdioServer } from "@de/tools";
import { initializeWorkspaceEmployees, toolServerCommand } from "./bootstrap";
import { startServer } from "./start";

async function start() {
  const workspaceDir = resolveWorkspace();
  const repoRoot = fileURLToPath(new URL("../../..", import.meta.url));
  const defaultEmployeesDir = process.env.DE_EMPLOYEES_DIR ?? join(repoRoot, "employees");
  // 首次启动把默认员工配置复制进工作区（design.md §14）；业务人员改的是工作区里这一份。
  // 设置了 DE_EMPLOYEES_DIR（开发/测试）时直接用它，不初始化工作区副本。
  const workspaceEmployeesDir = join(workspaceDir, "employees");
  if (!process.env.DE_EMPLOYEES_DIR) initializeWorkspaceEmployees(defaultEmployeesDir, workspaceEmployeesDir);
  const employeesDir = process.env.DE_EMPLOYEES_DIR ?? workspaceEmployeesDir;

  const defaultWebDist = join(repoRoot, "packages", "web", "dist");
  const webDistDir = process.env.DE_WEB_DIST ?? (existsSync(defaultWebDist) ? defaultWebDist : undefined);

  await startServer({
    workspaceDir,
    employeesDir,
    defaultEmployeesDir,
    webDistDir,
    toolServer: toolServerCommand({
      execPath: process.execPath,
      toolsEntry: join(repoRoot, "packages", "tools", "src", "stdio.ts"),
    }),
    port: Number(process.env.DE_PORT ?? process.env.PORT ?? 3000),
    portAttempts: 1,
    scriptFile: process.env.DE_SCRIPTED,
  });
}

if (process.argv[2] === "mcp-server") {
  await runStdioServer();
} else {
  await start();
}
