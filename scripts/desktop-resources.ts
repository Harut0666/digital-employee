// 桌面构建的资源映射（构建脚本与 electrobun.config.ts 共用，避免两份清单漂移）。
// Hutch 的 build.copy 支持目录，但会把目录原样复制；employees/ 里有 workspace 的 node_modules 软链，
// 直接复制整个 employees 会因软链指向目录而报 IsDir。所以这里只映射真正含 workflow.yaml 的员工目录
//（与 gen-embedded 的“员工目录”判定一致）。
import { existsSync, readdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const SCRIPTS_REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");

/** 固定资源映射（源相对仓库根 → 安装包内路径）。 */
export const FIXED_DESKTOP_COPY: Readonly<Record<string, string>> = {
  "packages/web/dist": "web",
  "samples/scenario-a": "samples/scenario-a",
  "samples/cases": "samples/cases",
  "packages/desktop/assets/resvg.wasm": "assets/resvg.wasm",
};

/** employees/ 下真正含 workflow.yaml 的员工目录名（忽略 node_modules、test 等）。 */
export function employeeResourceDirs(repoRoot = SCRIPTS_REPO_ROOT): string[] {
  const root = join(repoRoot, "employees");
  if (!existsSync(root)) return [];
  return readdirSync(root, { withFileTypes: true })
    .filter((entry) => entry.isDirectory() && existsSync(join(root, entry.name, "workflow.yaml")))
    .map((entry) => entry.name)
    .sort();
}

/** Electrobun build.copy 的完整映射：固定资源 + 每个员工目录。 */
export function desktopCopyMapping(repoRoot = SCRIPTS_REPO_ROOT): Record<string, string> {
  const copy: Record<string, string> = { ...FIXED_DESKTOP_COPY };
  for (const name of employeeResourceDirs(repoRoot)) {
    copy[`employees/${name}`] = `employees/${name}`;
  }
  return copy;
}
