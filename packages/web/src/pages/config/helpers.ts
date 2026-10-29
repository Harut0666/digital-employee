// 员工配置页的共享小工具：草稿、文件定位、百分比换算、工作流/质检数据的类型收窄。

import type { ConfigFileInfo, EmployeeConfigResponse } from "@de/core/contract";

export type Json = Record<string, any>;

/** 保存前在界面里攒下的改动：path → { data } 或 { content }。 */
export interface ConfigDraft {
  data?: unknown;
  content?: string;
}

export type DraftMap = Record<string, ConfigDraft>;

export interface TabProps {
  config: EmployeeConfigResponse;
  drafts: DraftMap;
  /** 表单视图的改动：整份结构化数据 */
  setData(path: string, data: unknown): void;
  /** 源文件视图的改动：整份原文 */
  setContent(path: string, content: string): void;
}

export function fileOf(config: EmployeeConfigResponse, path: string): ConfigFileInfo | undefined {
  return config.files.find((file) => file.path === path);
}

/** 原文：优先用草稿，否则用文件内容。 */
export function textOf(config: EmployeeConfigResponse, drafts: DraftMap, path: string): string {
  const draft = drafts[path];
  if (draft && typeof draft.content === "string") return draft.content;
  return fileOf(config, path)?.content ?? "";
}

export function asRecord(value: unknown): Json {
  return typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Json) : {};
}

export function asArray<T = Json>(value: unknown): T[] {
  return Array.isArray(value) ? (value as T[]) : [];
}

/** 比例转百分比输入：0.1 → 10 */
export function percentOf(value: unknown): number | undefined {
  return typeof value === "number" ? Math.round(value * 10000) / 100 : undefined;
}

/** 百分比输入转比例：10 → 0.1 */
export function ratioOf(value: number | null | undefined): number | undefined {
  return typeof value === "number" ? value / 100 : undefined;
}

/** 已经存在的类名（工作流里的字段名）会用过就返回 false，避免新增字段重名。 */
export function isValidFieldName(name: string, existing: string[]): boolean {
  return /^[a-zA-Z_][a-zA-Z0-9_]*$/.test(name) && !existing.includes(name);
}

export function formatTime(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return `${date.toLocaleDateString("zh-CN")} ${date.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", second: "2-digit" })}`;
}

export function shortHash(hash: string | undefined): string {
  return hash ? hash.slice(0, 8) : "—";
}
