// 工作区与设置（与 @de/server 的 bootstrap 约定一致）：默认 %APPDATA%/digital-employee，可用 DE_WORKSPACE 覆盖。
import { existsSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import type { ModelSettings } from "@de/core/contract";

type Env = Record<string, string | undefined>;

export function resolveWorkspace(env: Env = process.env): string {
  if (env.DE_WORKSPACE) return env.DE_WORKSPACE;
  return join(env.APPDATA ?? join(homedir(), "AppData", "Roaming"), "digital-employee");
}

/** 读取 settings.json，DEEPSEEK_API_KEY 优先；文件不存在时用默认的 DeepSeek 连接。 */
export function loadSettings(workspace: string, env: Env = process.env): ModelSettings {
  const file = join(workspace, "settings.json");
  const settings: ModelSettings = existsSync(file)
    ? (JSON.parse(readFileSync(file, "utf8")) as ModelSettings)
    : {
        connections: [{ name: "deepseek", baseUrl: "https://api.deepseek.com", apiKey: "", models: ["deepseek-chat"] }],
        defaultConnection: "deepseek",
        defaultModel: "deepseek-chat",
      };
  const key = env.DEEPSEEK_API_KEY;
  if (key) {
    settings.connections = settings.connections.map((c) =>
      c.name.toLowerCase() === "deepseek" || c.baseUrl.includes("deepseek.com") ? { ...c, apiKey: key } : c,
    );
  }
  return settings;
}
