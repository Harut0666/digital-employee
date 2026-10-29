// 启动期的纯函数：工作区位置、设置读取、工具服务器子进程命令。main.ts 只负责把它们接起来。
import { existsSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { basename, join } from "node:path";
import type { ModelSettings } from "@de/core";

type Env = Record<string, string | undefined>;

/** 工作区默认在 %APPDATA%/digital-employee，可用 DE_WORKSPACE 覆盖。 */
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

/**
 * 工具服务器子进程的启动命令。开发时 `bun packages/tools/src/stdio.ts`；
 * 打包后的 exe 用自身加 mcp-server 子命令启动。
 */
export function toolServerCommand(opts: { execPath: string; toolsEntry: string }): { command: string; args: string[] } {
  const isBun = /^bun(\.exe)?$/i.test(basename(opts.execPath.replace(/\\/g, "/")));
  return isBun ? { command: opts.execPath, args: [opts.toolsEntry] } : { command: opts.execPath, args: ["mcp-server"] };
}
