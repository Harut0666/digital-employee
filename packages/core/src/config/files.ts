// 员工配置文件的清单与路径规则（docs/design.md §4）。
// 配置页既能读也能写，所以文件白名单集中在这里：路由与版本快照都用同一份清单。

import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { parse as parseYaml } from "yaml";
import type { ConfigFileInfo } from "../contract";

/** 员工目录根下的配置文件；可选文件不存在时不出现。 */
export const CONFIG_ROOT_FILES = ["SKILL.md", "workflow.yaml", "qc.yaml", "form.schema.json", "form.ui.json", "frontdesk.md"] as const;

const SUBDIR_FILES: { dir: string; ext: string }[] = [
  { dir: "prompts", ext: ".md" },
  { dir: "schemas", ext: ".json" },
];

/**
 * 规范化配置页给出的文件路径：返回相对员工目录的路径（正斜杠），不合法时返回 undefined。
 * 只允许白名单内的文件，拒绝绝对路径与 .. 穿越。
 */
export function normalizeConfigPath(raw: unknown): string | undefined {
  if (typeof raw !== "string" || raw.trim() === "") return undefined;
  const normalized = raw.replace(/\\/g, "/").replace(/^\.\//, "");
  if (normalized.startsWith("/") || /^[a-zA-Z]:/.test(normalized)) return undefined;
  const parts = normalized.split("/");
  if (parts.some((p) => p === "" || p === "." || p === "..")) return undefined;
  if (parts.length === 1) return (CONFIG_ROOT_FILES as readonly string[]).includes(parts[0]!) ? parts[0]! : undefined;
  if (parts.length !== 2) return undefined;
  const sub = SUBDIR_FILES.find((s) => s.dir === parts[0]);
  return sub && parts[1]!.endsWith(sub.ext) ? normalized : undefined;
}

export function isWritableConfigPath(raw: unknown): boolean {
  return normalizeConfigPath(raw) !== undefined;
}

/** 员工目录里实际存在的配置文件，按“根文件在前、子目录按字典序”排列。 */
export function listConfigFiles(dir: string): string[] {
  const out: string[] = [];
  for (const name of CONFIG_ROOT_FILES) if (existsSync(join(dir, name))) out.push(name);
  for (const sub of SUBDIR_FILES) {
    const subdir = join(dir, sub.dir);
    if (!existsSync(subdir)) continue;
    for (const entry of readdirSync(subdir).sort()) {
      const path = join(subdir, entry);
      if (entry.endsWith(sub.ext) && statSync(path).isFile()) out.push(`${sub.dir}/${entry}`);
    }
  }
  return out;
}

/** 读取员工目录里全部配置文件的内容，键为相对路径。 */
export function readConfigFiles(dir: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const rel of listConfigFiles(dir)) out[rel] = readFileSync(join(dir, rel), "utf8");
  return out;
}

/** 读一个配置文件；不存在或路径不合法时返回 undefined。 */
export function readConfigFileText(dir: string, rawPath: unknown): { path: string; text: string } | undefined {
  const rel = normalizeConfigPath(rawPath);
  if (!rel) return undefined;
  const path = join(dir, rel);
  if (!existsSync(path)) return undefined;
  return { path: rel, text: readFileSync(path, "utf8") };
}

/**
 * 配置页用的文件清单：每个文件带上原文与解析结果。
 * 解析失败不抛错，只在 error 里说明——这时配置加载会失败，但源文件视图仍要能打开修改。
 */
export function describeConfigFiles(dir: string): ConfigFileInfo[] {
  return listConfigFiles(dir).map((path) => {
    const content = readFileSync(join(dir, path), "utf8");
    const kind = path.endsWith(".yaml") ? "yaml" : path.endsWith(".json") ? "json" : "markdown";
    const info: ConfigFileInfo = { path, kind, content };
    if (kind === "yaml") {
      try {
        info.parsed = parseYaml(content);
      } catch (error) {
        info.error = `YAML 解析失败：${(error as Error).message}`;
      }
    } else if (kind === "json") {
      try {
        info.parsed = JSON.parse(content);
      } catch (error) {
        info.error = `JSON 解析失败：${(error as Error).message}`;
      }
    }
    return info;
  });
}
