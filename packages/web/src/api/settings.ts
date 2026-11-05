// 设置页的数据层：真实实现走 Eden，VITE_MOCK=1 时用内存模拟。
// 页面只依赖这里的接口；共享的 WebApi 不动，避免 T10/T11/T12 并行改同一个类型文件。
import { treaty } from "@elysiajs/eden";
import type { App } from "@de/server";
import {
  DEFAULT_DIAGNOSTICS_SETTINGS,
  DEFAULT_EXECUTION_SETTINGS,
  DEFAULT_PERMISSION_SETTINGS,
  DEFAULT_UI_SETTINGS,
  SETTINGS_VERSION,
  type AppSettingsPatch,
  type AppSettingsView,
  type DefaultRole,
  type DiagnosticsExportResult,
  type DiagnosticsInfo,
  type SettingsExportResult,
  type SettingsImportResult,
  type SettingsRoleResult,
} from "@de/core/contract";
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
  // ---- 设置文档 v2（T19 起的接口；分区界面由 T21/T22 使用）
  /** 设置文档视图（密钥只回末 4 位） */
  getAppSettings(): Promise<AppSettingsView>;
  /** 合并更新分区字段；revision 过期时抛“设置已被修改，请刷新后重试” */
  patchAppSettings(patch: AppSettingsPatch): Promise<AppSettingsView>;
  /** 只写 permissions.defaultRole */
  setDefaultRole(role: DefaultRole, revision?: number): Promise<SettingsRoleResult>;
  /** 导出设置；默认脱敏，includeKeys=true 才含明文 Key */
  exportSettings(includeKeys?: boolean): Promise<SettingsExportResult>;
  /** 导入设置（接受导出的 document，或裸设置文档）；后端会先备份当前文件 */
  importSettings(document: unknown): Promise<SettingsImportResult>;
  /** 诊断信息（版本/运行时/工作区/脱敏快照/最近事件） */
  getDiagnostics(): Promise<DiagnosticsInfo>;
  /** 导出诊断包到工作区，返回路径 */
  exportDiagnostics(): Promise<DiagnosticsExportResult>;
  /** 用系统编辑器打开 settings.json */
  openSettingsFile(): Promise<{ ok: true; path: string }>;
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
      app: {
        get(): Reply<AppSettingsView>;
        patch(body: unknown): Reply<AppSettingsView>;
      };
      role: {
        put(body: unknown): Reply<SettingsRoleResult>;
      };
      /** 关键字路由名：Eden 里按属性访问即可 */
      export: {
        get(options?: { query?: { includeKeys?: string } }): Reply<SettingsExportResult>;
      };
      import: {
        post(body: unknown): Reply<SettingsImportResult>;
      };
      diagnostics: {
        get(): Reply<DiagnosticsInfo>;
        export: { post(body?: unknown): Reply<DiagnosticsExportResult> };
      };
      "open-file": {
        post(body?: unknown): Reply<{ ok: true; path: string }>;
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
    getAppSettings: async () => unwrap(await settings.app.get()),
    async patchAppSettings(patch) {
      return unwrap(await settings.app.patch(patch));
    },
    async setDefaultRole(role, revision) {
      return unwrap(await settings.role.put(revision === undefined ? { role } : { role, revision }));
    },
    async exportSettings(includeKeys = false) {
      return unwrap(await settings.export.get(includeKeys ? { query: { includeKeys: "true" } } : {}));
    },
    async importSettings(document) {
      return unwrap(await settings.import.post(document));
    },
    getDiagnostics: async () => unwrap(await settings.diagnostics.get()),
    async exportDiagnostics() {
      return unwrap(await settings.diagnostics.export.post({}));
    },
    async openSettingsFile() {
      return unwrap(await settings["open-file"].post({}));
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

  // ---- 设置文档 v2 的内存模拟（T19 起的接口）
  const isRecord = (value: unknown): value is Record<string, any> => typeof value === "object" && value !== null && !Array.isArray(value);
  let revision = 0;
  let ui = { ...DEFAULT_UI_SETTINGS };
  let execution = { ...DEFAULT_EXECUTION_SETTINGS, retryBackoffMs: [...DEFAULT_EXECUTION_SETTINGS.retryBackoffMs] };
  let diagnosticsSettings = { ...DEFAULT_DIAGNOSTICS_SETTINGS };
  let permissions = { ...DEFAULT_PERMISSION_SETTINGS };
  const defaultModelOf = () => connections[0]?.models[0] ?? "deepseek-chat";
  const currentAppView = (): AppSettingsView => ({
    ok: true,
    version: SETTINGS_VERSION,
    revision,
    ui: { ...ui },
    execution: { ...execution, retryBackoffMs: [...execution.retryBackoffMs] },
    diagnostics: { ...diagnosticsSettings },
    permissions: { ...permissions },
    connections: connections.map(view),
    defaultConnection,
    defaultModel: defaultModelOf(),
    demo: { toolFaults: demoTools, ...demo },
    extra: {},
    envOverride: false,
  });
  const documentJson = (includeKeys: boolean): Record<string, unknown> => ({
    version: SETTINGS_VERSION,
    revision,
    connections: connections.map((c) => ({
      name: c.name,
      baseUrl: c.baseUrl,
      apiKey: includeKeys ? c.apiKey : "",
      models: [...c.models],
    })),
    defaultConnection,
    defaultModel: defaultModelOf(),
    demo: { toolFaults: demoTools, ...demo },
    ui: { ...ui },
    execution: { ...execution, retryBackoffMs: [...execution.retryBackoffMs] },
    diagnostics: { ...diagnosticsSettings },
    permissions: { ...permissions },
  });

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
    // ---- 设置文档 v2（T19）
    getAppSettings: () => later(currentAppView()),
    patchAppSettings: (patch) =>
      later(undefined).then(() => {
        if (patch.revision !== revision) throw new Error("设置已被修改，请刷新后重试");
        if (patch.ui) ui = { ...ui, ...patch.ui };
        if (patch.execution) execution = { ...execution, ...patch.execution };
        if (patch.diagnostics) diagnosticsSettings = { ...diagnosticsSettings, ...patch.diagnostics };
        if (patch.permissions) permissions = { ...permissions, ...patch.permissions };
        revision += 1;
        return currentAppView();
      }),
    setDefaultRole: (role) =>
      later(undefined).then(() => {
        permissions = { ...permissions, defaultRole: role };
        revision += 1;
        return { ok: true as const, revision, permissions: { ...permissions } };
      }),
    exportSettings: (includeKeys = false) =>
      later({
        ok: true as const,
        redacted: !includeKeys,
        filename: `settings${includeKeys ? "-with-keys" : ""}-20261105120000.json`,
        document: documentJson(includeKeys),
        exportedAt: new Date().toISOString(),
      }),
    importSettings: (document) =>
      later(undefined).then(() => {
        const raw = isRecord(document) && isRecord(document.document) ? document.document : document;
        if (!isRecord(raw)) throw new Error("设置文件必须是 JSON 对象");
        if (isRecord(raw.ui)) ui = { ...ui, ...raw.ui };
        if (isRecord(raw.execution)) execution = { ...execution, ...raw.execution };
        if (isRecord(raw.diagnostics)) diagnosticsSettings = { ...diagnosticsSettings, ...raw.diagnostics };
        if (isRecord(raw.permissions)) permissions = { ...permissions, ...raw.permissions };
        revision += 1;
        return {
          ok: true as const,
          revision,
          backupPath: `${workspace}\\config-versions\\settings\\20261105120000.json`,
          migrated: raw.version !== SETTINGS_VERSION,
        };
      }),
    getDiagnostics: () =>
      later<DiagnosticsInfo>({
        ok: true,
        generatedAt: new Date().toISOString(),
        app: { version: "0.0.0-mock", settingsVersion: SETTINGS_VERSION },
        runtime: { bun: "1.4.2", platform: "win32", arch: "x64" },
        workspace: {
          workspaceDir: workspace,
          employeesDir: `${workspace}\\employees`,
          configVersionsDir: `${workspace}\\config-versions`,
          settingsFile: `${workspace}\\settings.json`,
          settingsFileExists: true,
        },
        settings: currentAppView(),
        tasks: [],
      }),
    exportDiagnostics: () =>
      later<DiagnosticsExportResult>({
        ok: true,
        path: `${workspace}\\diagnostics\\diagnostic-20261105120000.json`,
        generatedAt: new Date().toISOString(),
      }),
    openSettingsFile: () => later({ ok: true as const, path: `${workspace}\\settings.json` }),
  };
}

export function createSettingsApi(origin: string = window.location.origin): SettingsApi {
  return IS_MOCK ? createMockSettingsApi() : createEdenSettingsApi(origin);
}
