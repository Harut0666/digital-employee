// 应用资源的两种来源：exe 用构建期生成的归档（archive），桌面版用 Electrobun build.copy 拷进应用资源的目录（directory）。
// 两种来源都归一到同一组 ArchiveEntry，后续工作区释放逻辑（releaseFiles）不需要关心资源从哪来。
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { decodeArchive, type ArchiveEntry } from "./archive";

export interface EmbeddedBundlePaths {
  /** packages/web/dist 的归档，构建期生成 */
  web: string;
  /** employees/ 的归档 */
  employees: string;
  /** samples/scenario-a 与 samples/cases 的归档（不含体积大的 samples/fixtures） */
  samples: string;
}

/** 资源来源：归档（exe 现状）或目录（桌面构建期资源）。 */
export type AppResourceSource =
  | { kind: "archive"; bundles: EmbeddedBundlePaths }
  | { kind: "directory"; root: string };

export interface WorkspaceResourceFiles {
  /** 相对 packages/web/dist 的路径 */
  web: ArchiveEntry[];
  /** 相对 employees/ 的路径（只含含 workflow.yaml 的员工目录） */
  employees: ArchiveEntry[];
  /** 相对 samples/ 的路径（scenario-a/…、cases/…） */
  samples: ArchiveEntry[];
}

async function readBundle(path: string, label: string): Promise<ArchiveEntry[]> {
  if (!existsSync(path)) throw new Error(`嵌入资源缺失（${label}）：${path}；请用 bun run build:exe 重新构建`);
  return decodeArchive(new Uint8Array(await Bun.file(path).arrayBuffer()));
}

/** 递归收集一个目录里的文件，key 相对 prefix（统一用 / 分隔）。 */
function walk(dir: string, prefix: string, out: ArchiveEntry[]): void {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name);
    const key = prefix === "" ? entry.name : `${prefix}/${entry.name}`;
    if (entry.isDirectory()) walk(full, key, out);
    else if (entry.isFile()) out.push({ key, data: new Uint8Array(readFileSync(full)) });
  }
}

function requireDir(dir: string, label: string): string {
  if (!existsSync(dir)) throw new Error(`桌面资源缺失（${label}）：${dir}；请用 bun run build:desktop 重新构建`);
  return dir;
}

/**
 * 桌面资源目录的约定布局（electrobun.config.ts 的 build.copy 目标）：
 *   web/                ← packages/web/dist
 *   employees/<员工>/   ← employees/<员工>（只收含 workflow.yaml 的目录，与 gen-embedded 一致）
 *   samples/scenario-a/ ← samples/scenario-a
 *   samples/cases/      ← samples/cases
 */
export function readDirectoryFiles(root: string): WorkspaceResourceFiles {
  const web: ArchiveEntry[] = [];
  walk(requireDir(join(root, "web"), "前端构建产物"), "", web);

  const employees: ArchiveEntry[] = [];
  const employeesRoot = requireDir(join(root, "employees"), "员工配置");
  for (const entry of readdirSync(employeesRoot, { withFileTypes: true })) {
    if (!entry.isDirectory()) continue;
    const dir = join(employeesRoot, entry.name);
    // employees/ 下还有 test、node_modules 等非员工内容，只收含 workflow.yaml 的员工目录
    if (!existsSync(join(dir, "workflow.yaml"))) continue;
    walk(dir, entry.name, employees);
  }

  const samples: ArchiveEntry[] = [];
  for (const name of ["scenario-a", "cases"] as const) {
    walk(requireDir(join(root, "samples", name), `公开样例（${name}）`), name, samples);
  }

  return { web, employees, samples };
}

/** 把资源来源读成内存里的文件清单；归档缺失时报构建问题，目录缺失时报桌面资源问题。 */
export async function loadResourceFiles(source: AppResourceSource): Promise<WorkspaceResourceFiles> {
  if (source.kind === "directory") return readDirectoryFiles(source.root);
  const [web, employees, samples] = await Promise.all([
    readBundle(source.bundles.web, "前端构建产物"),
    readBundle(source.bundles.employees, "员工配置"),
    readBundle(source.bundles.samples, "公开样例"),
  ]);
  return { web, employees, samples };
}
