// 首次启动 / 换新 exe 时的准备工作区：释放嵌入的员工配置与样例、解出前端构建产物、写默认 settings.json。
// 嵌入资源以“归档文件”的形式跟着 exe（见 archive.ts）；这里只认归档路径，方便单测传入临时归档。
import { existsSync } from "node:fs";
import { join } from "node:path";
import { releaseFiles, writeDefaultSettings, type ReleaseOutcome } from "@de/server/bootstrap";
import { decodeArchive, type ArchiveEntry } from "./archive";

export interface EmbeddedBundlePaths {
  /** packages/web/dist 的归档，构建期生成 */
  web: string;
  /** employees/ 的归档 */
  employees: string;
  /** samples/scenario-a 与 samples/cases 的归档（不含体积大的 samples/fixtures） */
  samples: string;
}

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

/** 运行时资源（前端构建产物）放在工作区里，换 exe 版本时整体覆盖。 */
const RUNTIME_DIR = ".runtime";

async function readBundle(path: string, label: string): Promise<ArchiveEntry[]> {
  if (!existsSync(path)) throw new Error(`嵌入资源缺失（${label}）：${path}；请用 bun run build:exe 重新构建`);
  return decodeArchive(new Uint8Array(await Bun.file(path).arrayBuffer()));
}

const toFiles = (prefix: string, entries: ArchiveEntry[]) =>
  entries.map((e) => ({ path: prefix === "" ? e.key : `${prefix}/${e.key}`, data: e.data }));

export async function prepareWorkspace(options: { workspaceDir: string; bundles: EmbeddedBundlePaths }): Promise<PreparedWorkspace> {
  const { workspaceDir, bundles } = options;
  const [web, employees, samples] = await Promise.all([
    readBundle(bundles.web, "前端构建产物"),
    readBundle(bundles.employees, "员工配置"),
    readBundle(bundles.samples, "公开样例"),
  ]);

  // 员工配置与样例：已有文件不覆盖（用户改过的配置要保留）
  const releasedEmployees = releaseFiles(toFiles("employees", employees), workspaceDir);
  const releasedSamples = releaseFiles(toFiles("samples", samples), workspaceDir);
  // 前端构建产物与“默认员工配置”是内部资源：必须跟着 exe 版本走
  const webDistDir = join(workspaceDir, RUNTIME_DIR, "web");
  const releasedWeb = releaseFiles(toFiles("", web), webDistDir, { overwrite: true });
  const defaultEmployeesDir = join(workspaceDir, RUNTIME_DIR, "default-employees");
  const releasedDefaults = releaseFiles(toFiles("", employees), defaultEmployeesDir, { overwrite: true });

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
