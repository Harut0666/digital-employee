// “关于”对话框的内容：应用版本、Electrobun 版本、Bun 运行时版本与工作区。
// 版本从 Electrobun 打包产物读取（Resources/version.json 与 Resources/build.json），
// 不在源码里硬编码；读取/格式化为纯函数，可在普通 Bun 进程里单测。
import { existsSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { APP_TITLE } from "./brand";

export interface AboutInfo {
  appVersion?: string;
  electrobunVersion?: string;
  bunVersion?: string;
}

export interface AboutOptions {
  /** 打包资源目录（含 version.json / build.json）；默认按主进程所在位置推断 */
  resourcesDir?: string;
  /** Bun 运行时版本；默认 process.versions.bun */
  bunVersion?: string;
  /** 工作区路径，显示在详情里方便演示时定位 */
  workspaceDir?: string;
}

export interface AboutMessage {
  title: string;
  message: string;
  detail: string;
}

export const UNKNOWN_VERSION = "未知";

/** 主进程位于 <Resources>/app/bun/index.js，向上两级即 Resources。 */
export function defaultResourcesDir(moduleDir: string = import.meta.dir): string {
  return resolve(moduleDir, "..", "..");
}

function readJson(path: string): Record<string, unknown> | undefined {
  if (!existsSync(path)) return undefined;
  try {
    const value: unknown = JSON.parse(readFileSync(path, "utf8"));
    if (typeof value !== "object" || value === null || Array.isArray(value)) return undefined;
    return value as Record<string, unknown>;
  } catch {
    return undefined;
  }
}

function text(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() !== "" ? value.trim() : undefined;
}

/** 从 Resources 目录读取版本信息；缺文件/坏 JSON 都返回 undefined 字段，不抛错。 */
export function readAboutInfo(options: AboutOptions = {}): AboutInfo {
  const resourcesDir = options.resourcesDir ?? defaultResourcesDir();
  const version = readJson(join(resourcesDir, "version.json"));
  const build = readJson(join(resourcesDir, "build.json"));
  const bunVersion = options.bunVersion ?? (typeof process.versions?.bun === "string" ? process.versions.bun : undefined);
  return {
    appVersion: text(version?.version),
    electrobunVersion: text(build?.electrobunVersion),
    bunVersion,
  };
}

/** 组装原生消息框内容：标题给应用名，message 给版本，detail 逐项列出运行时。 */
export function formatAbout(info: AboutInfo, options: { workspaceDir?: string } = {}): AboutMessage {
  const version = info.appVersion ?? "（开发构建）";
  const lines = [
    `应用版本：${info.appVersion ?? UNKNOWN_VERSION}`,
    `Electrobun：${info.electrobunVersion ?? UNKNOWN_VERSION}`,
    `Bun：${info.bunVersion ?? UNKNOWN_VERSION}`,
  ];
  if (options.workspaceDir) lines.push(`工作区：${options.workspaceDir}`);
  return {
    title: APP_TITLE,
    message: `${APP_TITLE} ${version}`,
    detail: lines.join("\n"),
  };
}
