// 工作区与设置：服务端和命令行共用。工作区默认在 %APPDATA%/digital-employee，可用 DE_WORKSPACE 覆盖。
// settings.json 保存模型连接与演示开关（demo 段，见 docs/design.md §14.1）。
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import type { DemoSettings, FaultMode, ModelConnection, ModelSettings, ToolName } from "../contract";

type Env = Record<string, string | undefined>;

export function resolveWorkspace(env: Env = process.env): string {
  if (env.DE_WORKSPACE) return env.DE_WORKSPACE;
  const appData = env.APPDATA ?? join(homedir(), "AppData", "Roaming");
  return join(appData, "digital-employee");
}

const DEFAULT_SETTINGS: ModelSettings = {
  connections: [{ name: "deepseek", baseUrl: "https://api.deepseek.com", apiKey: "", models: ["deepseek-chat"] }],
  defaultConnection: "deepseek",
  defaultModel: "deepseek-chat",
};

export const DEFAULT_DEMO_SETTINGS: DemoSettings = {
  toolFaults: {},
  corruptTraceability: false,
  crashOnNextStep: false,
};

const FAULT_MODES: readonly FaultMode[] = ["timeout", "error", "invalid_output"];

/** DeepSeek 连接（按名称或地址判断）：只有它会用 DEEPSEEK_API_KEY 覆盖文件里的 Key。 */
export function isDeepSeekConnection(c: { name: string; baseUrl: string }): boolean {
  return c.name.toLowerCase() === "deepseek" || c.baseUrl.includes("deepseek.com");
}

function settingsPath(workspace: string): string {
  return join(workspace, "settings.json");
}

/** 读 settings.json 的原始对象；文件不存在返回空对象，损坏时抛出带路径的错误。 */
export function readSettingsObject(workspace: string): Record<string, unknown> {
  const file = settingsPath(workspace);
  if (!existsSync(file)) return {};
  try {
    const parsed: unknown = JSON.parse(readFileSync(file, "utf8"));
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) throw new Error("顶层必须是 JSON 对象");
    return parsed as Record<string, unknown>;
  } catch (err) {
    throw new Error(`无法读取 ${file}：${err instanceof Error ? err.message : String(err)}`);
  }
}

/** 写回 settings.json（保留未识别的字段，例如将来新增的配置）。 */
export function writeSettingsObject(workspace: string, value: Record<string, unknown>): void {
  writeFileSync(settingsPath(workspace), `${JSON.stringify(value, null, 2)}\n`);
}

function readConnections(raw: Record<string, unknown>): ModelConnection[] {
  if (!Array.isArray(raw.connections)) return structuredClone(DEFAULT_SETTINGS.connections);
  return raw.connections.filter(
    (c): c is ModelConnection =>
      typeof c === "object" &&
      c !== null &&
      typeof (c as ModelConnection).name === "string" &&
      typeof (c as ModelConnection).baseUrl === "string",
  );
}

/** 读取 <workspace>/settings.json；DEEPSEEK_API_KEY 优先于文件里的 Key。文件不存在时用默认的 DeepSeek 连接。 */
export function loadSettings(workspace: string, env: Env = process.env): ModelSettings {
  const raw = readSettingsObject(workspace);
  const settings: ModelSettings = {
    connections: readConnections(raw),
    defaultConnection:
      typeof raw.defaultConnection === "string" ? raw.defaultConnection : DEFAULT_SETTINGS.defaultConnection,
    defaultModel: typeof raw.defaultModel === "string" ? raw.defaultModel : DEFAULT_SETTINGS.defaultModel,
  };
  const key = env.DEEPSEEK_API_KEY;
  if (key) settings.connections = settings.connections.map((c) => (isDeepSeekConnection(c) ? { ...c, apiKey: key } : c));
  return settings;
}

function normalizeToolFaults(value: unknown): Partial<Record<ToolName, FaultMode>> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {};
  const out: Partial<Record<ToolName, FaultMode>> = {};
  for (const [tool, mode] of Object.entries(value)) {
    if (typeof mode === "string" && (FAULT_MODES as readonly string[]).includes(mode)) out[tool as ToolName] = mode as FaultMode;
  }
  return out;
}

function rawDemo(raw: Record<string, unknown>): Record<string, unknown> {
  const demo = raw.demo;
  return demo && typeof demo === "object" && !Array.isArray(demo) ? (demo as Record<string, unknown>) : {};
}

/** 读取演示开关；缺省或缺失字段用默认值补齐。 */
export function loadDemoSettings(workspace: string): DemoSettings {
  const demo = rawDemo(readSettingsObject(workspace));
  return {
    toolFaults: normalizeToolFaults(demo.toolFaults),
    corruptTraceability: demo.corruptTraceability === true,
    crashOnNextStep: demo.crashOnNextStep === true,
  };
}

/**
 * 更新演示开关并写回 settings.json，返回更新后的完整演示设置。
 * 只替换传入的字段：toolFaults 整体替换，布尔开关各自替换。
 */
export function saveDemoSettings(workspace: string, patch: Partial<DemoSettings>): DemoSettings {
  const raw = readSettingsObject(workspace);
  const current = loadDemoSettings(workspace);
  const demo: Record<string, unknown> = {
    ...rawDemo(raw),
    toolFaults: patch.toolFaults ?? current.toolFaults,
    corruptTraceability: patch.corruptTraceability ?? current.corruptTraceability,
    crashOnNextStep: patch.crashOnNextStep ?? current.crashOnNextStep,
  };
  raw.demo = demo;
  writeSettingsObject(workspace, raw);
  return {
    toolFaults: normalizeToolFaults(demo.toolFaults),
    corruptTraceability: demo.corruptTraceability === true,
    crashOnNextStep: demo.crashOnNextStep === true,
  };
}

/** 清除一次性崩溃标记（引擎在 fault_injected 落盘之后、退出进程之前调用）。 */
export function clearCrashOnNextStep(workspace: string): void {
  saveDemoSettings(workspace, { crashOnNextStep: false });
}
