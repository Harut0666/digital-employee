// 工作区与设置：服务端和命令行共用。工作区默认在 %APPDATA%/digital-employee，可用 DE_WORKSPACE 覆盖。
import { existsSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import type { ModelSettings } from "../contract";

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

function isDeepSeek(c: { name: string; baseUrl: string }): boolean {
  return c.name.toLowerCase() === "deepseek" || c.baseUrl.includes("deepseek.com");
}

/** 读取 <workspace>/settings.json；DEEPSEEK_API_KEY 优先于文件里的 Key。文件不存在时用默认的 DeepSeek 连接。 */
export function loadSettings(workspace: string, env: Env = process.env): ModelSettings {
  const file = join(workspace, "settings.json");
  let settings: ModelSettings = structuredClone(DEFAULT_SETTINGS);
  if (existsSync(file)) {
    try {
      settings = JSON.parse(readFileSync(file, "utf8")) as ModelSettings;
    } catch (err) {
      throw new Error(`无法读取 ${file}：${err instanceof Error ? err.message : String(err)}`);
    }
  }
  const key = env.DEEPSEEK_API_KEY;
  if (key) settings.connections = settings.connections.map((c) => (isDeepSeek(c) ? { ...c, apiKey: key } : c));
  return settings;
}
