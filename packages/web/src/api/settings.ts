// 设置页的数据层：真实实现走 Eden，VITE_MOCK=1 时用内存模拟。
// 页面只依赖这里的接口；共享的 WebApi 不动，避免 T10/T11/T12 并行改同一个类型文件。
import { treaty } from "@elysiajs/eden";
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
  type ModelOptions,
  type SettingsConnectionView,
  type SettingsExportResult,
  type SettingsImportResult,
  type SettingsRoleResult,
} from "@de/core/contract";
import { IS_MOCK } from "./index";
import type { ModelRef } from "./types";

export type FaultMode = "timeout" | "error" | "invalid_output";
export type ApiKeySource = SettingsConnectionView["source"];

/** 连接的脱敏视图来自 contract，前端只做展示，不重复定义形状（README：契约是唯一事实来源）。 */
export type { SettingsConnectionView };

export interface SettingsView {
  ok: true;
  /** 最近一次在会话里选的模型（T39）；还没选过为 null */
  lastModel: ModelRef | null;
  connections: SettingsConnectionView[];
}

export interface SaveConnectionInput {
  baseUrl?: string;
  /** 空字符串或省略 = 保持原值 */
  apiKey?: string;
  models?: string[];
  /** 每模型高级参数；省略保持原值，null 清除 */
  modelOptions?: Record<string, ModelOptions> | null;
  /** 连接级超时；省略保持原值，null 清除 */
  timeoutMs?: number | null;
  /** 连接级重试次数；省略保持原值，null 清除 */
  maxRetries?: number | null;
}

export interface ConnectionTestResult {
  ok: boolean;
  latencyMs: number;
  model: string;
  error?: string;
}

/** 拉取模型的请求体：可带未保存的 baseUrl / apiKey（新建连接时先拉取；空串 = 用已保存的值）。 */
export interface FetchModelsInput {
  baseUrl?: string;
  apiKey?: string;
}

/** 拉取模型的响应：成功给模型 ID 列表，失败给原因（Key 已脱敏）；拉取结果不写设置。 */
export type FetchModelsResult = { ok: true; models: string[] } | { ok: false; error: string };

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
  /** 应用目录：设置、员工、知识库、会话和任务都在这里（Spec 0006） */
  appDir: string;
  /** 工作区目录（单目录模式下就是应用目录） */
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
  /** 拉取 {baseUrl}/models 的模型列表（只读，不写设置） */
  fetchModels(name: string, input?: FetchModelsInput): Promise<FetchModelsResult>;
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
  /** 用系统文件管理器打开诊断包所在目录 */
  openDiagnosticsFolder(): Promise<{ ok: true; path: string }>;
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
  models: { post(body: unknown): Reply<FetchModelsResult> };
}

/** Eden 的路由类型很深，这里按 internal/server 的设置路由收窄。 */
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
        open: { post(body?: unknown): Reply<{ ok: true; path: string }> };
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

function createEdenSettingsApi(origin: string, getRole?: () => DefaultRole): SettingsApi {
  // 设置请求带上当前界面角色：服务端审计要记录“谁”做的危险操作（缺省时回落到默认角色）
  const client = treaty<any>(origin, {
    headers: getRole ? () => ({ "x-role": getRole() }) : {},
  }) as unknown as SettingsShape;
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
    async fetchModels(name, input) {
      // 拉取失败（401、超时等）由后端以 200 + ok:false 说明原因；只有路径/参数错误才抛异常
      return unwrap(await connection(name).models.post(input ?? {}));
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
    async openDiagnosticsFolder() {
      return unwrap(await settings.diagnostics.open.post({}));
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

// Spec 0006：应用目录放全局内容（设置、员工、知识库），工作区是用户的普通目录
const workspace = "C:\\Users\\demo\\Documents\\数字员工";
const appDir = "C:\\Users\\demo\\AppData\\Roaming\\digital-employee-app";

/** 模拟的 /models 返回：按连接名给一份固定剧本，不访问网络。 */
const MOCK_MODEL_CATALOG: Record<string, string[]> = {
  示例厂商: ["demo-chat", "demo-reasoner", "demo-coder"],
  通义千问: ["qwen-plus", "qwen-max", "qwen-long", "qwen-turbo"],
};

/**
 * 模拟拉取模型（对应 Go 的 handleFetchModels）：
 * 连接不存在且没给 baseUrl → 404 式错误；没 Key → ok:false；baseUrl 含 demo-error → ok:false（Key 打码）。
 */
function mockFetchModels(
  name: string,
  input: FetchModelsInput | undefined,
  connections: readonly MockConnection[],
): FetchModelsResult {
  const existing = connections.find((c) => c.name === name);
  const baseUrl = input?.baseUrl?.trim() ? input.baseUrl.trim() : existing?.baseUrl ?? "";
  if (!baseUrl) throw new Error(`连接不存在：${name}（新建连接时请先填写 Base URL）`);
  const apiKey = input?.apiKey?.trim() ? input.apiKey.trim() : existing?.apiKey ?? "";
  if (!apiKey) return { ok: false, error: "未配置 API Key，无法拉取模型" };
  if (/demo-error/i.test(baseUrl)) return { ok: false, error: "模型接口返回 401：Incorrect API key provided: ***" };
  const catalog = MOCK_MODEL_CATALOG[name] ?? ["mock-model-a", "mock-model-b"];
  const models = [...new Set([...(existing?.models ?? []), ...catalog])];
  return { ok: true, models };
}

export function createMockSettingsApi(): SettingsApi {
  let connections: MockConnection[] = [
    {
      name: "示例厂商",
      baseUrl: "https://api.example.com/v1",
      apiKey: "sk-demo-0000-a3f9",
      models: ["demo-chat", "demo-reasoner"],
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
  let defaultConnection = "示例厂商";
  // 最近一次在会话里选的模型（Spec 0006）；模拟环境和会话模块一样，新会话默认用它
  let lastModel: ModelRef = { connection: "示例厂商", model: "demo-chat" };
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
  /** 模拟保存时的“省略=保留、null=清除”约定。 */
  const advancedOf = (
    existing: MockConnection | undefined,
    input: SaveConnectionInput,
  ): Pick<SettingsConnectionView, "modelOptions" | "timeoutMs" | "maxRetries"> => {
    const out: Pick<SettingsConnectionView, "modelOptions" | "timeoutMs" | "maxRetries"> = {};
    if (input.modelOptions !== undefined ? input.modelOptions !== null : existing?.modelOptions !== undefined) {
      out.modelOptions = structuredClone(input.modelOptions ?? existing!.modelOptions!);
    }
    if (input.timeoutMs !== undefined ? input.timeoutMs !== null : existing?.timeoutMs !== undefined) {
      out.timeoutMs = (input.timeoutMs ?? existing!.timeoutMs)!;
    }
    if (input.maxRetries !== undefined ? input.maxRetries !== null : existing?.maxRetries !== undefined) {
      out.maxRetries = (input.maxRetries ?? existing!.maxRetries)!;
    }
    return out;
  };
  // ---- 设置文档 v2 的内存模拟（T19 起的接口）
  const isRecord = (value: unknown): value is Record<string, any> => typeof value === "object" && value !== null && !Array.isArray(value);
  let revision = 0;
  let ui = { ...DEFAULT_UI_SETTINGS };
  let execution = { ...DEFAULT_EXECUTION_SETTINGS, retryBackoffMs: [...DEFAULT_EXECUTION_SETTINGS.retryBackoffMs] };
  let diagnosticsSettings = { ...DEFAULT_DIAGNOSTICS_SETTINGS };
  let permissions = { ...DEFAULT_PERMISSION_SETTINGS };
  const currentAppView = (): AppSettingsView => ({
    ok: true,
    version: SETTINGS_VERSION,
    revision,
    ui: { ...ui },
    execution: { ...execution, retryBackoffMs: [...execution.retryBackoffMs] },
    diagnostics: { ...diagnosticsSettings },
    permissions: { ...permissions },
    connections: connections.map(view),
    lastModel: { ...lastModel },
    demo: { toolFaults: demoTools, ...demo },
    extra: {},
  });
  const documentJson = (includeKeys: boolean): Record<string, unknown> => ({
    version: SETTINGS_VERSION,
    revision,
    connections: connections.map((c) => ({
      name: c.name,
      baseUrl: c.baseUrl,
      apiKey: includeKeys ? c.apiKey : "",
      models: [...c.models],
      ...(c.modelOptions !== undefined ? { modelOptions: structuredClone(c.modelOptions) } : {}),
      ...(c.timeoutMs !== undefined ? { timeoutMs: c.timeoutMs } : {}),
      ...(c.maxRetries !== undefined ? { maxRetries: c.maxRetries } : {}),
    })),
    lastModel: { ...lastModel },
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
        lastModel: { ...lastModel },
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
          ...advancedOf(existing, input),
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
      later({ ok: true, latencyMs: 320, model: model ?? connections.find((c) => c.name === name)?.models[0] ?? "demo-chat" }, 400),
    // ---- 拉取模型（T37）：规则照搬 Go 的 handleFetchModels，用固定剧本代替网络
    fetchModels: (name, input) =>
      later<FetchModelsResult>(mockFetchModels(name, input, connections)).then((result) =>
        result.ok ? { ok: true as const, models: result.models } : result,
      ),
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
        appDir,
        workspaceDir: workspace,
        employeesDir: `${appDir}\\employees`,
        defaultEmployeesDir: `${appDir}\\employees`,
        configVersionsDir: `${appDir}\\config-versions`,
      }),
    openWorkspace: () => later({ ok: true as const, path: workspace }),
    resetEmployees: () =>
      later({ ok: true as const, backupPath: `${appDir}\\config-versions\\employees\\20261029`, employees: ["weekly-report"] }, 300),
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
          backupPath: `${appDir}\\config-versions\\settings\\20261105120000.json`,
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
          workspaceDir: appDir,
          employeesDir: `${appDir}\\employees`,
          configVersionsDir: `${appDir}\\config-versions`,
          settingsFile: `${appDir}\\settings.json`,
          settingsFileExists: true,
        },
        settings: currentAppView(),
        tasks: [],
      }),
    exportDiagnostics: () =>
      later<DiagnosticsExportResult>({
        ok: true,
        path: `${appDir}\\diagnostics\\diagnostic-20261105120000.json`,
        generatedAt: new Date().toISOString(),
      }),
    openDiagnosticsFolder: () => later({ ok: true as const, path: `${appDir}\\diagnostics` }),
    openSettingsFile: () => later({ ok: true as const, path: `${appDir}\\settings.json` }),
  };
}

export function createSettingsApi(origin: string = window.location.origin, getRole?: () => DefaultRole): SettingsApi {
  return IS_MOCK ? createMockSettingsApi() : createEdenSettingsApi(origin, getRole);
}
