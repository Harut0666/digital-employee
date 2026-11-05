// 首次启动 / 换新版本时的准备工作区：释放员工配置与样例、解出前端构建产物、写默认 settings.json。
// 资源来源由 resources.ts 抽象成“归档”（exe）与“目录”（桌面）两种，这里只做释放语义。
import { join } from "node:path";
import { releaseFiles, writeDefaultSettings, type ReleaseOutcome } from "@de/server/bootstrap";
import type { ArchiveEntry } from "./archive";
import { loadResourceFiles, type AppResourceSource, type EmbeddedBundlePaths } from "./resources";

export type { AppResourceSource, EmbeddedBundlePaths } from "./resources";

export interface PreparedWorkspace {
  workspaceDir: string;
  /** 员工配置目录（工作区里的副本，用户可能改过） */
  employeesDir: string;
  /** 未改动的默认员工配置副本，供设置页“恢复默认员工配置”使用 */
  defaultEmployeesDir: string;
  /** 前端构建产物解出后的目录，交给服务端托管静态资源 */
  webDistDir: string;
  settings: "written" | "kept";
  released: { employees: ReleaseOutcome; samples: ReleaseOutcome; web: ReleaseOutcome; defaults: ReleaseOutcome };
}

/** 默认 settings.json：只带一个没有 Key 的 DeepSeek 连接，用户在设置页填 Key。 */
export const DEFAULT_SETTINGS_TEMPLATE: Record<string, unknown> = {
  connections: [{ name: "deepseek", baseUrl: "https://api.deepseek.com", apiKey: "", models: ["deepseek-chat"] }],
  defaultConnection: "deepseek",
  defaultModel: "deepseek-chat",
  demo: { toolFaults: {}, corruptTraceability: false, crashOnNextStep: false },
};

/** 运行时资源（前端构建产物）放在工作区里，换版本时整体覆盖。 */
const RUNTIME_DIR = ".runtime";

const toFiles = (prefix: string, entries: ArchiveEntry[]) =>
  entries.map((e) => ({ path: prefix === "" ? e.key : `${prefix}/${e.key}`, data: e.data }));

export interface PrepareWorkspaceOptions {
  workspaceDir: string;
  /** 资源来源（归档 / 目录） */
  source?: AppResourceSource;
  /** 兼容旧调用：等价于 { kind: "archive", bundles } */
  bundles?: EmbeddedBundlePaths;
}

export async function prepareWorkspace(options: PrepareWorkspaceOptions): Promise<PreparedWorkspace> {
  const { workspaceDir, bundles, source } = options;
  const resolved = source ?? (bundles ? { kind: "archive" as const, bundles } : undefined);
  if (!resolved) throw new Error("prepareWorkspace：需要资源来源（source 或 bundles）");
  const files = await loadResourceFiles(resolved);

  // 员工配置与样例：已有文件不覆盖（用户改过的配置要保留）
  const releasedEmployees = releaseFiles(toFiles("employees", files.employees), workspaceDir);
  const releasedSamples = releaseFiles(toFiles("samples", files.samples), workspaceDir);
  // 前端构建产物与“默认员工配置”是内部资源：必须跟着版本走
  const webDistDir = join(workspaceDir, RUNTIME_DIR, "web");
  const releasedWeb = releaseFiles(toFiles("", files.web), webDistDir, { overwrite: true });
  const defaultEmployeesDir = join(workspaceDir, RUNTIME_DIR, "default-employees");
  const releasedDefaults = releaseFiles(toFiles("", files.employees), defaultEmployeesDir, { overwrite: true });

  const settings = writeDefaultSettings(workspaceDir, DEFAULT_SETTINGS_TEMPLATE);
  return {
    workspaceDir,
    employeesDir: join(workspaceDir, "employees"),
    defaultEmployeesDir,
    webDistDir,
    settings,
    released: { employees: releasedEmployees, samples: releasedSamples, web: releasedWeb, defaults: releasedDefaults },
  };
}
