// 设置页的数据层：真实实现走 Eden，VITE_MOCK=1 时用内存模拟。
// 页面只依赖这里的接口；共享的 WebApi 不动，避免 T10/T11/T12 并行改同一个类型文件。
import { treaty } from "@elysiajs/eden";
import type { App } from "@de/server";
import { IS_MOCK } from "./index";

export type FaultMode = "timeout" | "error" | "invalid_output";
export type ApiKeySource = "env" | "file" | "none";

export interface SettingsConnectionView {
  name: string;
  baseUrl: string;
  models: string[];
  /** 只给末 4 位；没配置时为 null */
  apiKeyLast4: string | null;
  hasKey: boolean;
  source: ApiKeySource;
  isDefault: boolean;
}

export interface SettingsView {
  ok: true;
  defaultConnection: string;
  defaultModel: string;
  /** DEEPSEEK_API_KEY 优先于文件 */
  envOverride: boolean;
  connections: SettingsConnectionView[];
}

export interface SaveConnectionInput {
  baseUrl?: string;
  /** 空字符串或省略 = 保持原值 */
  apiKey?: string;
  models?: string[];
}

export interface ConnectionTestResult {
  ok: boolean;
  latencyMs: number;
  model: string;
  error?: string;
}

export interface DemoTool {
  name: string;
  label: string;
}

export interface DemoState {
  toolFaults: Partial<Record<string, FaultMode>>;
  corruptTraceability: boolean;
  crashOnNextStep: boolean;
}

export interface DemoView extends DemoState {
  ok: true;
  tools: DemoTool[];
}

export interface DemoSaveResult {
  ok: true;
  demo: DemoState;
  tools: DemoTool[];
  /** 下发到工具子进程失败时的说明；设置本身已保存 */
  toolError?: string;
}

export interface WorkspaceView {
  ok: true;
  workspaceDir: string;
  employeesDir: string;
  defaultEmployeesDir: string;
  configVersionsDir: string;
}

export interface ResetEmployeesResult {
  ok: true;
  backupPath: string | null;
  employees: string[];
}

export interface SettingsApi {
  getSettings(): Promise<SettingsView>;
  saveConnection(name: string, input: SaveConnectionInput): Promise<{ ok: true; connection: SettingsConnectionView; warning?: string }>;
  deleteConnection(name: string): Promise<{ ok: true; connections: SettingsConnectionView[]; warning?: string }>;
  testConnection(name: string, model?: string): Promise<ConnectionTestResult>;
  getDemo(): Promise<DemoView>;
  saveDemo(patch: Partial<DemoState>): Promise<DemoSaveResult>;
  getWorkspace(): Promise<WorkspaceView>;
  openWorkspace(): Promise<{ ok: true; path: string }>;
  resetEmployees(): Promise<ResetEmployeesResult>;
}

type Reply<T> = Promise<{ data: T | null; error: { status: unknown; value: unknown } | null }>;
type SavedConnection = { ok: true; connection: SettingsConnectionView; warning?: string };
type DeletedConnections = { ok: true; connections: SettingsConnectionView[]; warning?: string };

interface ConnectionRoute {
  put(body: unknown): Reply<SavedConnection>;
  delete(): Reply<DeletedConnections>;
  test: { post(body: unknown): Reply<ConnectionTestResult> };
}

/** Eden 的路由类型很深，这里按 packages/server/src/routes/settings.ts 的实际路由收窄。 */
interface SettingsShape {
  api: {
    settings: {
      get(): Reply<SettingsView>;
      connections: ((params: { name: string }) => ConnectionRoute) & {
        // Eden 只把动态路由暴露成函数
      };
      demo: {
        get(): Reply<DemoView>;
        put(body: unknown): Reply<DemoSaveResult>;
      };
      workspace: {
        get(): Reply<WorkspaceView>;
        open: { post(body?: unknown): Reply<{ ok: true; path: string }> };
        "reset-employees": { post(body?: unknown): Reply<ResetEmployeesResult> };
      };
    };
  };
}

function unwrap<T>(res: Awaited<Reply<T>>): T {
  if (res.error || res.data === null) {
    const v = res.error?.value;
    const msg =
      typeof v === "object" && v && "message" in v
        ? String((v as { message: unknown }).message)
        : `请求失败（${String(res.error?.status)}）`;
    throw new Error(msg);
  }
  return res.data;
}

function createEdenSettingsApi(origin: string): SettingsApi {
  const client = treaty<App>(origin) as unknown as SettingsShape;
  const settings = client.api.settings;
  const connection = (name: string) => settings.connections({ name });
  return {
    getSettings: async () => unwrap(await settings.get()),
    async saveConnection(name, input) {
      return unwrap(await connection(name).put(input));
    },
    async deleteConnection(name) {
      return unwrap(await connection(name).delete());
    },
    async testConnection(name, model) {
      return unwrap(await connection(name).test.post(model ? { model } : {}));
    },
    getDemo: async () => unwrap(await settings.demo.get()),
    async saveDemo(patch) {
      return unwrap(await settings.demo.put(patch));
    },
    getWorkspace: async () => unwrap(await settings.workspace.get()),
    async openWorkspace() {
      return unwrap(await settings.workspace.open.post({}));
    },
    async resetEmployees() {
      return unwrap(await settings.workspace["reset-employees"].post({}));
    },
  };
}

// ---------------------------------------------------------------- 模拟数据

interface MockConnection extends SettingsConnectionView {
  apiKey: string;
}

function createMockSettingsApi(): SettingsApi {
  let connections: MockConnection[] = [
    {
      name: "DeepSeek",
      baseUrl: "https://api.deepseek.com",
      apiKey: "sk-demo-0000-a3f9",
      models: ["deepseek-chat", "deepseek-reasoner"],
      apiKeyLast4: "a3f9",
      hasKey: true,
      source: "file",
      isDefault: true,
    },
    {
      name: "通义千问",
      baseUrl: "https://dashscope.aliyuncs.com/compatible-mode/v1",
      apiKey: "",
      models: ["qwen-plus"],
      apiKeyLast4: null,
      hasKey: false,
      source: "none",
      isDefault: false,
    },
  ];
  let defaultConnection = "DeepSeek";
  let demoTools: Partial<Record<string, FaultMode>> = {};
  let demo = { corruptTraceability: false, crashOnNextStep: false };
  const tools: DemoTool[] = [
    { name: "validate_submission", label: "表单与附件校验" },
    { name: "load_sales", label: "读取并清洗销售数据" },
    { name: "compute_metrics", label: "计算指标与异常" },
    { name: "parse_minutes", label: "解析周会纪要" },
    { name: "render_report_docx", label: "生成 Word 报告" },
  ];
  const later = <T>(value: T, ms = 60) => new Promise<T>((resolve) => setTimeout(() => resolve(value), ms));
  const view = (c: MockConnection): SettingsConnectionView => {
    const { apiKey, ...rest } = c;
    void apiKey;
    return { ...rest, isDefault: c.name === defaultConnection };
  };
  const workspace = "C:\\Users\\demo\\AppData\\Roaming\\digital-employee";

  return {
    getSettings: () =>
      later({
        ok: true as const,
        defaultConnection,
        defaultModel: connections[0]?.models[0] ?? "deepseek-chat",
        envOverride: false,
        connections: connections.map(view),
      }),
    saveConnection: (name, input) =>
      later(undefined).then(() => {
        const existing = connections.find((c) => c.name === name);
        const apiKey = input.apiKey ? input.apiKey : (existing?.apiKey ?? "");
        const next: MockConnection = {
          name,
          baseUrl: input.baseUrl ?? existing?.baseUrl ?? "",
          apiKey,
          models: input.models ?? existing?.models ?? [],
          apiKeyLast4: apiKey ? apiKey.slice(-4) : null,
          hasKey: apiKey !== "",
          source: apiKey ? "file" : "none",
          isDefault: name === defaultConnection,
        };
        connections = existing ? connections.map((c) => (c.name === name ? next : c)) : [...connections, next];
        if (!connections.some((c) => c.name === defaultConnection)) defaultConnection = name;
        return { ok: true as const, connection: view(next) };
      }),
    deleteConnection: (name) =>
      later(undefined).then(() => {
        connections = connections.filter((c) => c.name !== name);
        if (defaultConnection === name) defaultConnection = connections[0]?.name ?? "";
        return { ok: true as const, connections: connections.map(view) };
      }),
    testConnection: (name, model) =>
      later({ ok: true, latencyMs: 320, model: model ?? connections.find((c) => c.name === name)?.models[0] ?? "deepseek-chat" }, 400),
    getDemo: () => later({ ok: true as const, toolFaults: demoTools, ...demo, tools }),
    saveDemo: (patch) =>
      later(undefined).then(() => {
        if (patch.toolFaults) demoTools = { ...patch.toolFaults };
        if (patch.corruptTraceability !== undefined) demo.corruptTraceability = patch.corruptTraceability;
        if (patch.crashOnNextStep !== undefined) demo.crashOnNextStep = patch.crashOnNextStep;
        return { ok: true as const, demo: { toolFaults: demoTools, ...demo }, tools };
      }),
    getWorkspace: () =>
      later({
        ok: true as const,
        workspaceDir: workspace,
        employeesDir: `${workspace}\\employees`,
        defaultEmployeesDir: `${workspace}\\employees`,
        configVersionsDir: `${workspace}\\config-versions`,
      }),
    openWorkspace: () => later({ ok: true as const, path: workspace }),
    resetEmployees: () =>
      later({ ok: true as const, backupPath: `${workspace}\\config-versions\\employees\\20261029`, employees: ["weekly-report"] }, 300),
  };
}

export function createSettingsApi(origin: string = window.location.origin): SettingsApi {
  return IS_MOCK ? createMockSettingsApi() : createEdenSettingsApi(origin);
}
