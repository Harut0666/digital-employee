// 构建期脚本：扫描要嵌进 exe 的资源目录，生成 packages/app/assets/*.bundle（归档格式见 packages/app/src/archive.ts）。
//
//   web         ← packages/web/dist（先跑 bun run --filter @de/web build）
//   employees   ← employees/*（只收含 workflow.yaml 的员工目录）
//   samples     ← samples/scenario-a 与 samples/cases（故意不收 samples/fixtures：约 12MB，体积不划算）
//
// 用法：bun scripts/gen-embedded.ts [--strict]
//   --strict 时前端构建产物缺失直接报错（build:exe 用）；否则只告警（`bun run typecheck` 用）。
import { existsSync, mkdirSync, readdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { encodeArchive, type ArchiveEntry } from "../packages/app/src/archive";

export const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");
export const ASSETS_DIR = join(REPO_ROOT, "packages", "app", "assets");

interface SourceSpec {
  name: "web" | "employees" | "samples";
  /** 要扫描的目录（相对仓库根），可多个，键前缀为第二个元素 */
  roots: Array<[dir: string, prefix: string]>;
  /** 目录里只收含此文件的子目录（员工配置用） */
  onlyDirsWith?: string;
}

const SPECS: SourceSpec[] = [
  { name: "web", roots: [["packages/web/dist", ""]] },
  { name: "employees", roots: [["employees", ""]], onlyDirsWith: "workflow.yaml" },
  {
    name: "samples",
    roots: [
      ["samples/scenario-a", "scenario-a"],
      ["samples/cases", "cases"],
    ],
  },
];

function walk(dir: string, prefix: string, out: Array<{ key: string; path: string }>): void {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name);
    const key = prefix === "" ? entry.name : `${prefix}/${entry.name}`;
    if (entry.isDirectory()) walk(full, key, out);
    else if (entry.isFile()) out.push({ key, path: full });
  }
}

/** 收集一个来源的全部文件；目录不存在时返回 undefined。 */
export function collectSource(spec: SourceSpec, repoRoot = REPO_ROOT): Array<{ key: string; path: string }> | undefined {
  const files: Array<{ key: string; path: string }> = [];
  let foundAny = false;
  for (const [dir, prefix] of spec.roots) {
    const root = join(repoRoot, dir);
    if (!existsSync(root)) continue;
    foundAny = true;
    const subdirs = spec.onlyDirsWith
      ? readdirSync(root, { withFileTypes: true })
          .filter((e) => e.isDirectory() && existsSync(join(root, e.name, spec.onlyDirsWith!)))
          .map((e) => e.name)
      : undefined;
    if (subdirs) {
      for (const sub of subdirs) walk(join(root, sub), prefix === "" ? sub : `${prefix}/${sub}`, files);
    } else {
      walk(root, prefix, files);
    }
  }
  return foundAny ? files : undefined;
}

async function toEntries(files: Array<{ key: string; path: string }>): Promise<ArchiveEntry[]> {
  const entries: ArchiveEntry[] = [];
  for (const file of files) {
    entries.push({ key: file.key, data: new Uint8Array(await Bun.file(file.path).arrayBuffer()) });
  }
  return entries;
}

const kb = (bytes: number) => `${(bytes / 1024).toFixed(0)}KB`;

export interface GenerateOptions {
  repoRoot?: string;
  assetsDir?: string;
  /** 前端构建产物缺失时报错（构建 exe 时必须为 true） */
  strict?: boolean;
  log?: (line: string) => void;
}

/** 生成三份归档，返回每个归档的文件数与字节数。 */
export async function generateEmbedded(options: GenerateOptions = {}): Promise<Record<string, { files: number; bytes: number }>> {
  const repoRoot = options.repoRoot ?? REPO_ROOT;
  const assetsDir = options.assetsDir ?? ASSETS_DIR;
  const log = options.log ?? ((line: string) => console.log(line));
  mkdirSync(assetsDir, { recursive: true });
  const summary: Record<string, { files: number; bytes: number }> = {};

  for (const spec of SPECS) {
    const files = collectSource(spec, repoRoot);
    if (!files) {
      const message = `${spec.name}：源目录不存在（${spec.roots.map(([d]) => d).join("、")}）`;
      if (spec.name === "web" && options.strict) throw new Error(`${message}；先跑 bun run --filter @de/web build`);
      log(`跳过 ${message}`);
      continue;
    }
    const entries = await toEntries(files);
    const archive = encodeArchive(entries);
    await Bun.write(join(assetsDir, `${spec.name}.bundle`), archive);
    summary[spec.name] = { files: files.length, bytes: archive.length };
    log(`嵌入 ${spec.name}：${files.length} 个文件 → ${kb(archive.length)}`);
  }
  return summary;
}

if (import.meta.main) {
  try {
    await generateEmbedded({ strict: process.argv.includes("--strict") });
  } catch (err) {
    console.error(err instanceof Error ? err.message : String(err));
    process.exit(1);
  }
}
