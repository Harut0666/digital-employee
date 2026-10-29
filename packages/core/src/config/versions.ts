// 配置版本（docs/design.md §4.1、Spec 0002 的“配置版本”约定）。
//
// 每次保存前把当前配置复制到 <workspace>/config-versions/<员工>/<时间戳>-<哈希短码>/，
// 目录里放配置文件副本和 .version.json（时间、整份配置哈希、每个文件的哈希）。
// 回滚 = 先把当前内容留一个快照，再把目标版本的文件复制回来；回滚本身因此也产生一个新版本。

import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { isEmployeeId, type ConfigDiff, type ConfigDiffFile, type ConfigVersion, type RestoreResult } from "../contract";
import { listConfigFiles, readConfigFiles } from "./files";

export type { ConfigDiff, ConfigDiffFile, ConfigVersion, RestoreResult };

const META_FILE = ".version.json";

interface VersionMeta extends ConfigVersion {
  files: Record<string, string>;
}

export function versionsDirFor(workspaceDir: string, employeeId: string): string {
  if (!isEmployeeId(employeeId)) throw new Error(`员工 id 不合法：${employeeId}`);
  return join(resolve(workspaceDir), "config-versions", employeeId);
}

function sha256(text: string): string {
  return createHash("sha256").update(text).digest("hex");
}

function hashConfigFiles(files: Record<string, string>): string {
  const hash = createHash("sha256");
  for (const path of Object.keys(files).sort()) {
    hash.update(path);
    hash.update("\0");
    hash.update(files[path]!);
    hash.update("\0");
  }
  return hash.digest("hex");
}

/** ISO 时间 → 目录名用的紧凑格式（不含冒号、点，Windows 也能当目录名）。 */
function stampOf(date: Date): string {
  return date.toISOString().replace(/[-:.]/g, "");
}

function writeTextFile(path: string, text: string): void {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, text);
}

function readMeta(dir: string): VersionMeta | undefined {
  try {
    const meta = JSON.parse(readFileSync(join(dir, META_FILE), "utf8")) as VersionMeta;
    if (typeof meta.id !== "string" || typeof meta.createdAt !== "string" || typeof meta.hash !== "string") return undefined;
    if (typeof meta.files !== "object" || meta.files === null) return undefined;
    return meta;
  } catch {
    return undefined;
  }
}

function toVersion(meta: VersionMeta): ConfigVersion {
  return { id: meta.id, createdAt: meta.createdAt, hash: meta.hash };
}

/** 给当前配置拍一个版本快照。 */
export function createConfigVersion(options: {
  workspaceDir: string;
  employeeId: string;
  employeeDir: string;
  now?: () => Date;
}): ConfigVersion {
  const { workspaceDir, employeeId, employeeDir, now = () => new Date() } = options;
  const root = versionsDirFor(workspaceDir, employeeId);
  const files = readConfigFiles(employeeDir);
  const hash = hashConfigFiles(files);
  const stamp = stampOf(now());
  let id = `${stamp}-${hash.slice(0, 8)}`;
  for (let n = 2; existsSync(join(root, id)); n++) id = `${stamp}-${hash.slice(0, 8)}-${n}`;
  const dir = join(root, id);
  const meta: VersionMeta = {
    id,
    createdAt: now().toISOString(),
    hash,
    files: Object.fromEntries(Object.entries(files).map(([path, text]) => [path, sha256(text)])),
  };
  for (const [path, text] of Object.entries(files)) writeTextFile(join(dir, path), text);
  writeTextFile(join(dir, META_FILE), JSON.stringify(meta, null, 2));
  return toVersion(meta);
}

/** 列出版本，新的在前。 */
export function listConfigVersions(workspaceDir: string, employeeId: string): ConfigVersion[] {
  const root = versionsDirFor(workspaceDir, employeeId);
  if (!existsSync(root)) return [];
  const versions: ConfigVersion[] = [];
  for (const entry of readdirSync(root)) {
    const dir = join(root, entry);
    if (!statSync(dir).isDirectory()) continue;
    const meta = readMeta(dir);
    if (meta) versions.push(toVersion(meta));
  }
  return versions.sort((a, b) => (a.createdAt === b.createdAt ? (a.id < b.id ? 1 : -1) : a.createdAt < b.createdAt ? 1 : -1));
}

function versionDir(workspaceDir: string, employeeId: string, versionId: unknown): string | undefined {
  if (typeof versionId !== "string" || !/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(versionId)) return undefined;
  const root = versionsDirFor(workspaceDir, employeeId);
  const dir = join(root, versionId);
  if (resolve(dir) !== resolve(root, versionId)) return undefined;
  return existsSync(dir) && statSync(dir).isDirectory() ? dir : undefined;
}

/** 删除一个版本目录：保存写盘后整体校验失败时回滚用，保证没生效的保存不留版本。 */
export function removeConfigVersion(workspaceDir: string, employeeId: string, versionId: unknown): boolean {
  const dir = versionDir(workspaceDir, employeeId, versionId);
  if (!dir) return false;
  rmSync(dir, { recursive: true, force: true });
  return true;
}

/** 读回一个版本的全部文件内容；版本不存在时返回 undefined。 */
export function readConfigVersion(workspaceDir: string, employeeId: string, versionId: unknown): Record<string, string> | undefined {
  const dir = versionDir(workspaceDir, employeeId, versionId);
  if (!dir) return undefined;
  const meta = readMeta(dir);
  if (!meta) return undefined;
  const files: Record<string, string> = {};
  for (const path of Object.keys(meta.files)) {
    const full = join(dir, path);
    if (!existsSync(full)) continue;
    files[path] = readFileSync(full, "utf8");
  }
  return files;
}

// ---------------------------------------------------------------- 行级差异

function lineDiff(before: string, after: string): string[] {
  const a = before.split("\n");
  const b = after.split("\n");
  const n = a.length;
  const m = b.length;
  const dp: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i]![j] = a[i] === b[j] ? dp[i + 1]![j + 1]! + 1 : Math.max(dp[i + 1]![j]!, dp[i]![j + 1]!);
    }
  }
  const ops: { kind: " " | "-" | "+"; text: string }[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      ops.push({ kind: " ", text: a[i]! });
      i++;
      j++;
    } else if (dp[i + 1]![j]! >= dp[i]![j + 1]!) {
      ops.push({ kind: "-", text: a[i]! });
      i++;
    } else {
      ops.push({ kind: "+", text: b[j]! });
      j++;
    }
  }
  while (i < n) ops.push({ kind: "-", text: a[i++]! });
  while (j < m) ops.push({ kind: "+", text: b[j++]! });

  const changed: number[] = [];
  ops.forEach((op, index) => {
    if (op.kind !== " ") changed.push(index);
  });
  if (changed.length === 0) return [];

  const CONTEXT = 3;
  const groups: [number, number][] = [];
  let start = Math.max(0, changed[0]! - CONTEXT);
  let end = Math.min(ops.length - 1, changed[0]! + CONTEXT);
  for (const index of changed.slice(1)) {
    if (index - end > CONTEXT * 2) {
      groups.push([start, end]);
      start = Math.max(0, index - CONTEXT);
    }
    end = Math.min(ops.length - 1, index + CONTEXT);
  }
  groups.push([start, end]);
  return groups.flatMap(([from, to]) => [`@@ -${from + 1},${to - from + 1} @@`, ...ops.slice(from, to + 1).map((op) => `${op.kind}${op.text}`)]);
}

/** 版本与当前配置的差异；版本不存在时返回 undefined。 */
export function diffConfigVersion(options: {
  workspaceDir: string;
  employeeId: string;
  employeeDir: string;
  versionId: unknown;
}): ConfigDiff | undefined {
  const { workspaceDir, employeeId, employeeDir, versionId } = options;
  const dir = versionDir(workspaceDir, employeeId, versionId);
  const meta = dir ? readMeta(dir) : undefined;
  if (!dir || !meta) return undefined;
  const before = readConfigVersion(workspaceDir, employeeId, meta.id) ?? {};
  const current = readConfigFiles(employeeDir);
  const paths = [...new Set([...Object.keys(before), ...Object.keys(current)])].sort();
  const files: ConfigDiffFile[] = [];
  for (const path of paths) {
    const oldText = before[path];
    const newText = current[path];
    if (oldText === undefined && newText !== undefined) files.push({ path, status: "added", hunks: lineDiff("", newText) });
    else if (oldText !== undefined && newText === undefined) files.push({ path, status: "removed", hunks: lineDiff(oldText, "") });
    else if (oldText !== undefined && newText !== undefined && oldText !== newText) files.push({ path, status: "modified", hunks: lineDiff(oldText, newText) });
  }
  return { version: toVersion(meta), hasChanges: files.length > 0, files };
}

/** 回滚：先给当前配置留快照，再把版本内容复制回来（版本里没有的配置文件会删掉）。 */
export function restoreConfigVersion(options: {
  workspaceDir: string;
  employeeId: string;
  employeeDir: string;
  versionId: unknown;
  now?: () => Date;
}): RestoreResult | undefined {
  const { workspaceDir, employeeId, employeeDir, versionId, now } = options;
  const dir = versionDir(workspaceDir, employeeId, versionId);
  const meta = dir ? readMeta(dir) : undefined;
  if (!dir || !meta) return undefined;
  const files = readConfigVersion(workspaceDir, employeeId, meta.id);
  if (!files) return undefined;

  const snapshot = createConfigVersion({ workspaceDir, employeeId, employeeDir, now });
  for (const path of listConfigFiles(employeeDir)) {
    if (!(path in files)) rmSync(join(employeeDir, path), { force: true });
  }
  for (const [path, text] of Object.entries(files)) writeTextFile(join(employeeDir, path), text);
  return { snapshot, restoredFrom: meta.id };
}
