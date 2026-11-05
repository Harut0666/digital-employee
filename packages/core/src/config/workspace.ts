// 工作区与设置：服务端和命令行共用。工作区默认在 %APPDATA%/digital-employee，可用 DE_WORKSPACE 覆盖。
// settings.json 的读写与迁移统一走 `settings-document.ts`（T19）；这里只保留运行期视图。
import { homedir } from "node:os";
import { join } from "node:path";
import type { DemoSettings, ModelSettings } from "../contract";
import {
  isDeepSeekConnection,
  modelSettingsOf,
  readRawSettingsObject,
  readSettingsDocument,
  updateSettingsDocument,
  writeSettingsObject,
} from "./settings-document";

type Env = Record<string, string | undefined>;

export function resolveWorkspace(env: Env = process.env): string {
  if (env.DE_WORKSPACE) return env.DE_WORKSPACE;
  const appData = env.APPDATA ?? join(homedir(), "AppData", "Roaming");
  return join(appData, "digital-employee");
}

export const DEFAULT_DEMO_SETTINGS: DemoSettings = {
  toolFaults: {},
  corruptTraceability: false,
  crashOnNextStep: false,
};

// 兼容旧调用方：读原始 JSON / 写回 settings.json 都转到唯一入口（写入会 `revision + 1`）。
export const readSettingsObject = readRawSettingsObject;
export { writeSettingsObject };
export { isDeepSeekConnection };

/** 读取 <workspace>/settings.json；DEEPSEEK_API_KEY 优先于文件里的 Key。文件不存在时用默认的 DeepSeek 连接。 */
export function loadSettings(workspace: string, env: Env = process.env): ModelSettings {
  const settings: ModelSettings = modelSettingsOf(readSettingsDocument(workspace));
  const key = env.DEEPSEEK_API_KEY;
  if (key) settings.connections = settings.connections.map((c) => (isDeepSeekConnection(c) ? { ...c, apiKey: key } : c));
  return settings;
}

/** 读取演示开关；缺省或缺失字段用默认值补齐。 */
export function loadDemoSettings(workspace: string): DemoSettings {
  return readSettingsDocument(workspace).demo;
}

/**
 * 更新演示开关并写回 settings.json，返回更新后的完整演示设置。
 * 只替换传入的字段：toolFaults 整体替换，布尔开关各自替换。
 */
export function saveDemoSettings(workspace: string, patch: Partial<DemoSettings>): DemoSettings {
  const doc = updateSettingsDocument(workspace, (document) => {
    if (patch.toolFaults) document.demo.toolFaults = { ...patch.toolFaults };
    if (patch.corruptTraceability !== undefined) document.demo.corruptTraceability = patch.corruptTraceability;
    if (patch.crashOnNextStep !== undefined) document.demo.crashOnNextStep = patch.crashOnNextStep;
  });
  return doc.demo;
}

/** 清除一次性崩溃标记（引擎在 fault_injected 落盘之后、退出进程之前调用）。 */
export function clearCrashOnNextStep(workspace: string): void {
  saveDemoSettings(workspace, { crashOnNextStep: false });
}
