// 命令行要用的目录解析：员工配置与样例。
// 源码运行时在仓库里；打包后的 exe 里仓库不存在，用的是首次启动释放到工作区的那一份。
import { existsSync, readdirSync } from "node:fs";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { resolveWorkspace } from "@de/core";

export const REPO_ROOT = fileURLToPath(new URL("../../..", import.meta.url));

type Env = Record<string, string | undefined>;

function hasEmployee(dir: string): boolean {
  try {
    return readdirSync(dir, { withFileTypes: true }).some((e) => e.isDirectory() && existsSync(join(dir, e.name, "workflow.yaml")));
  } catch {
    return false;
  }
}

/** 员工配置目录：DE_EMPLOYEES_DIR > 工作区里的副本（exe 释放的）> 仓库里的默认配置。 */
export function resolveEmployeesDir(env: Env = process.env, repoRoot = REPO_ROOT): string {
  if (env.DE_EMPLOYEES_DIR) return resolve(env.DE_EMPLOYEES_DIR);
  const workspaceEmployees = join(resolveWorkspace(env), "employees");
  if (hasEmployee(workspaceEmployees)) return workspaceEmployees;
  return join(repoRoot, "employees");
}

/** 样例目录：工作区里的副本（exe 释放的）> 仓库里的 samples。 */
export function resolveSamplesDir(env: Env = process.env, repoRoot = REPO_ROOT): string {
  const workspaceSamples = join(resolveWorkspace(env), "samples");
  if (existsSync(join(workspaceSamples, "scenario-a"))) return workspaceSamples;
  return join(repoRoot, "samples");
}
