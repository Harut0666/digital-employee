// 设置文档（settings.json）的统一读写入口（Spec 0004、T19）。
//
// 约定：
//   - 读路径不写盘：v1 文档（无 `version`）只在内存里补默认值并视作 v2，迁移发生在首次写入时；
//   - 未识别的顶层键原样保留在 `extra` 里，写入时再展开回去；
//   - `revision` 存在文档里，每次写入 +1，写入只经过本文件的 `writeRawSettings` 一处；
//   - 密钥只经 `settingsDocumentView` / `settingsExport` 出去，默认脱敏。
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { z } from "zod";
import {
  AppSettingsPatchSchema,
  DemoSettingsSchema,
  ExecutionSettingsSchema,
  DiagnosticsSettingsSchema,
  ModelConnectionSchema,
  ModelOptionsSchema,
  PermissionSettingsSchema,
  SETTINGS_VERSION,
  UiSettingsSchema,
  describeSettingsProblems,
  defaultAppSettings,
  DEFAULT_DIAGNOSTICS_SETTINGS,
  DEFAULT_EXECUTION_SETTINGS,
  DEFAULT_PERMISSION_SETTINGS,
  DEFAULT_UI_SETTINGS,
  CONNECTION_RETRIES_MAX,
  CONNECTION_TIMEOUT_MAX_MS,
  CONNECTION_TIMEOUT_MIN_MS,
  type AppSettings,
  type AppSettingsPatch,
  type DemoSettings,
  type ExecutionSettings,
  type ModelOptions,
  type PermissionSettings,
  type SettingsDocument,
  type UiSettings,
} from "../contract";
import type { AppSettingsView, SettingsConnectionView } from "../contract";
import type { FaultMode, ModelConnection, ModelSettings, ToolName } from "../contract";

type Env = Record<string, string | undefined>;

/** 设置文件名（工作区根目录下）。 */
export const SETTINGS_FILE_NAME = "settings.json";

/** 默认模型连接：没有任何 settings.json 时用它（与 design.md §14.1 一致）。 */
export const DEFAULT_CONNECTIONS: ModelConnection[] = [
  { name: "deepseek", baseUrl: "https://api.deepseek.com", apiKey: "", models: ["deepseek-chat"] },
];

export const DEFAULT_DEFAULT_CONNECTION = "deepseek";
export const DEFAULT_DEFAULT_MODEL = "deepseek-chat";

/** 设置文件里由本模块认识的顶层键；其余键放进 `extra` 原样保留。 */
const KNOWN_TOP_LEVEL_KEYS = new Set([
  "version",
  "revision",
  "connections",
  "defaultConnection",
  "defaultModel",
  "demo",
  "ui",
  "execution",
  "diagnostics",
  "permissions",
]);

const FAULT_MODES: readonly FaultMode[] = ["timeout", "error", "invalid_output"];

const ConnectionTimeoutSchema = z.number().int().min(CONNECTION_TIMEOUT_MIN_MS).max(CONNECTION_TIMEOUT_MAX_MS);
const ConnectionRetriesSchema = z.number().int().min(0).max(CONNECTION_RETRIES_MAX);

export function settingsPathFor(workspace: string): string {
  return join(workspace, SETTINGS_FILE_NAME);
}

/** DeepSeek 连接（按名称或地址判断）：只有它会用 DEEPSEEK_API_KEY 覆盖文件里的 Key。 */
export function isDeepSeekConnection(c: { name: string; baseUrl: string }): boolean {
  return c.name.toLowerCase() === "deepseek" || c.baseUrl.includes("deepseek.com");
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isNonNegativeInt(value: unknown): value is number {
  return typeof value === "number" && Number.isInteger(value) && value >= 0;
}

// ---------------------------------------------------------------- 原始文件读写

/** 读 settings.json 的原始对象；文件不存在返回空对象，损坏时抛出带路径的错误。 */
export function readRawSettingsObject(workspace: string): Record<string, unknown> {
  const file = settingsPathFor(workspace);
  if (!existsSync(file)) return {};
  try {
    const parsed: unknown = JSON.parse(readFileSync(file, "utf8"));
    if (!isRecord(parsed)) throw new Error("顶层必须是 JSON 对象");
    return parsed;
  } catch (err) {
    throw new Error(`无法读取 ${file}：${err instanceof Error ? err.message : String(err)}`);
  }
}

/** 唯一的物理写入点：其它函数都必须经过 `writeSettingsDocument`。 */
function writeRawSettings(workspace: string, value: Record<string, unknown>): void {
  const file = settingsPathFor(workspace);
  mkdirSync(dirname(file), { recursive: true });
  writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`);
}

// ---------------------------------------------------------------- 归一化 / 迁移

/** 按已知字段逐个取用：合法就用，不合法就用默认值（读路径从不因手改坏值而失败）。 */
function mergeSection<T extends object>(defaults: T, value: unknown, schema: z.ZodObject<z.ZodRawShape>): T {
  const out = structuredClone(defaults) as Record<string, unknown>;
  if (!isRecord(value)) return out as T;
  const shape = schema.shape as Record<string, z.ZodType>;
  for (const [key, raw] of Object.entries(value)) {
    const field = shape[key];
    if (!field) continue;
    const parsed = field.safeParse(raw);
    if (parsed.success) out[key] = parsed.data;
  }
  return out as T;
}

function normalizeToolFaults(value: unknown): Partial<Record<ToolName, FaultMode>> {
  if (!isRecord(value)) return {};
  const out: Partial<Record<ToolName, FaultMode>> = {};
  for (const [tool, mode] of Object.entries(value)) {
    if (typeof mode === "string" && (FAULT_MODES as readonly string[]).includes(mode)) out[tool as ToolName] = mode as FaultMode;
  }
  return out;
}

function normalizeDemo(value: unknown): DemoSettings {
  const raw = isRecord(value) ? value : {};
  return {
    toolFaults: normalizeToolFaults(raw.toolFaults),
    corruptTraceability: raw.corruptTraceability === true,
    crashOnNextStep: raw.crashOnNextStep === true,
  };
}

/** 一个连接：名字与地址必须合法，其余高级字段能解析就保留。 */
function normalizeConnection(value: unknown): ModelConnection | undefined {
  if (!isRecord(value)) return undefined;
  const { name, baseUrl } = value;
  if (typeof name !== "string" || name.trim() === "" || typeof baseUrl !== "string") return undefined;
  const connection: ModelConnection = {
    name,
    baseUrl,
    apiKey: typeof value.apiKey === "string" ? value.apiKey : "",
    models: Array.isArray(value.models) ? value.models.filter((m): m is string => typeof m === "string" && m !== "") : [],
  };
  if (isRecord(value.modelOptions)) {
    const options: Record<string, ModelOptions> = {};
    for (const [modelId, raw] of Object.entries(value.modelOptions)) {
      const parsed = ModelOptionsSchema.safeParse(raw);
      if (parsed.success) options[modelId] = parsed.data;
    }
    // 空对象也保留：表示“用户清空了高级参数”
    connection.modelOptions = options;
  }
  const timeout = ConnectionTimeoutSchema.safeParse(value.timeoutMs);
  if (timeout.success) connection.timeoutMs = timeout.data;
  const retries = ConnectionRetriesSchema.safeParse(value.maxRetries);
  if (retries.success) connection.maxRetries = retries.data;
  return connection;
}

/**
 * 把任意形状的 settings.json 归一化成 v2 文档（v1 → v2 的迁移也在这里）。
 * 只读不写；未知顶层键进 `extra`。
 */
export function normalizeSettingsDocument(raw: Record<string, unknown>): SettingsDocument {
  const connections = Array.isArray(raw.connections)
    ? raw.connections.map(normalizeConnection).filter((c): c is ModelConnection => c !== undefined)
    : structuredClone(DEFAULT_CONNECTIONS);
  const extra: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(raw)) {
    if (!KNOWN_TOP_LEVEL_KEYS.has(key)) extra[key] = value;
  }
  const defaults = defaultAppSettings(isNonNegativeInt(raw.revision) ? raw.revision : 0);
  return {
    ...defaults,
    ui: mergeSection<UiSettings>(DEFAULT_UI_SETTINGS, raw.ui, UiSettingsSchema),
    execution: mergeSection<ExecutionSettings>(DEFAULT_EXECUTION_SETTINGS, raw.execution, ExecutionSettingsSchema),
    diagnostics: mergeSection(DEFAULT_DIAGNOSTICS_SETTINGS, raw.diagnostics, DiagnosticsSettingsSchema),
    permissions: mergeSection<PermissionSettings>(DEFAULT_PERMISSION_SETTINGS, raw.permissions, PermissionSettingsSchema),
    connections,
    defaultConnection:
      typeof raw.defaultConnection === "string" ? raw.defaultConnection : (connections[0]?.name ?? DEFAULT_DEFAULT_CONNECTION),
    defaultModel: typeof raw.defaultModel === "string" ? raw.defaultModel : (connections[0]?.models[0] ?? DEFAULT_DEFAULT_MODEL),
    demo: normalizeDemo(raw.demo),
    extra,
  };
}

/** 读设置文档；v1 文件会在内存里补成 v2，但不写盘（迁移只发生在写入时）。 */
export function readSettingsDocument(workspace: string): SettingsDocument {
  return normalizeSettingsDocument(readRawSettingsObject(workspace));
}

// ---------------------------------------------------------------- 写

function serializeConnection(connection: ModelConnection): Record<string, unknown> {
  const out: Record<string, unknown> = {
    name: connection.name,
    baseUrl: connection.baseUrl,
    apiKey: connection.apiKey,
    models: [...connection.models],
  };
  if (connection.modelOptions !== undefined) out.modelOptions = structuredClone(connection.modelOptions);
  if (connection.timeoutMs !== undefined) out.timeoutMs = connection.timeoutMs;
  if (connection.maxRetries !== undefined) out.maxRetries = connection.maxRetries;
  return out;
}

/** 文档 → 磁盘上的 JSON 对象（`extra` 展开回顶层，保持“未知键原样保留”）。 */
export function serializeSettingsDocument(doc: SettingsDocument): Record<string, unknown> {
  return {
    ...doc.extra,
    version: SETTINGS_VERSION,
    revision: doc.revision,
    connections: doc.connections.map(serializeConnection),
    defaultConnection: doc.defaultConnection,
    defaultModel: doc.defaultModel,
    demo: { ...doc.demo, toolFaults: { ...doc.demo.toolFaults } },
    ui: { ...doc.ui },
    execution: { ...doc.execution, retryBackoffMs: [...doc.execution.retryBackoffMs] },
    diagnostics: { ...doc.diagnostics },
    permissions: { ...doc.permissions },
  };
}

/** 写设置文档：`revision + 1` 后落盘，返回写后的文档。 */
export function writeSettingsDocument(workspace: string, doc: SettingsDocument): SettingsDocument {
  const next: SettingsDocument = { ...doc, version: SETTINGS_VERSION, revision: doc.revision + 1 };
  writeRawSettings(workspace, serializeSettingsDocument(next));
  return next;
}

/** 读 → 改 → 写（唯一的日常写入入口）：每次调用 `revision + 1`。 */
export function updateSettingsDocument(workspace: string, mutate: (doc: SettingsDocument) => void): SettingsDocument {
  const doc = readSettingsDocument(workspace);
  mutate(doc);
  return writeSettingsDocument(workspace, doc);
}

/**
 * 兼容旧调用方（原先直接 writeFileSync 覆盖）：把原始对象按 v2 归一化后写入，
 * 同样经过唯一写入点并 `revision + 1`。
 */
export function writeSettingsObject(workspace: string, value: Record<string, unknown>): void {
  writeSettingsDocument(workspace, normalizeSettingsDocument(value));
}

/** settings.json 不存在时写一份默认文档（“打开设置文件”前调用）。 */
export function ensureSettingsFile(workspace: string): SettingsDocument {
  if (existsSync(settingsPathFor(workspace))) return readSettingsDocument(workspace);
  return writeSettingsDocument(workspace, normalizeSettingsDocument({}));
}

// ---------------------------------------------------------------- 视图与导出

function effectiveKey(connection: ModelConnection, env: Env): { key: string; source: "env" | "file" | "none" } {
  const envKey = env.DEEPSEEK_API_KEY;
  if (envKey && isDeepSeekConnection(connection)) return { key: envKey, source: "env" };
  if (connection.apiKey) return { key: connection.apiKey, source: "file" };
  return { key: "", source: "none" };
}

function connectionView(connection: ModelConnection, defaultConnection: string, env: Env): SettingsConnectionView {
  const { key, source } = effectiveKey(connection, env);
  const view: SettingsConnectionView = {
    name: connection.name,
    baseUrl: connection.baseUrl,
    models: [...connection.models],
    apiKeyLast4: key ? key.slice(-4) : null,
    hasKey: key !== "",
    source,
    isDefault: connection.name === defaultConnection,
  };
  if (connection.modelOptions !== undefined) view.modelOptions = structuredClone(connection.modelOptions);
  if (connection.timeoutMs !== undefined) view.timeoutMs = connection.timeoutMs;
  if (connection.maxRetries !== undefined) view.maxRetries = connection.maxRetries;
  return view;
}

/** v2 文档的脱敏视图：密钥只出末 4 位 + hasKey + source（GET /api/settings/app）。 */
export function settingsDocumentView(doc: SettingsDocument, env: Env = {}): AppSettingsView {
  return {
    ok: true,
    version: doc.version,
    revision: doc.revision,
    ui: { ...doc.ui },
    execution: { ...doc.execution, retryBackoffMs: [...doc.execution.retryBackoffMs] },
    diagnostics: { ...doc.diagnostics },
    permissions: { ...doc.permissions },
    connections: doc.connections.map((c) => connectionView(c, doc.defaultConnection, env)),
    defaultConnection: doc.defaultConnection,
    defaultModel: doc.defaultModel,
    demo: { ...doc.demo, toolFaults: { ...doc.demo.toolFaults } },
    extra: structuredClone(doc.extra),
    envOverride: Boolean(env.DEEPSEEK_API_KEY),
  };
}

/**
 * 导出用的 JSON 对象：默认把 Key 抹成空串（形状仍可再导入）；
 * `includeKeys` 为 true 时原样写出明文 Key（调用方负责二次确认与文件名警示）。
 */
export function settingsExport(doc: SettingsDocument, options: { includeKeys?: boolean } = {}): Record<string, unknown> {
  const raw = serializeSettingsDocument(doc);
  if (options.includeKeys) return raw;
  raw.connections = doc.connections.map((connection) => serializeConnection({ ...connection, apiKey: "" }));
  return raw;
}

// ---------------------------------------------------------------- 写入校验

/** PATCH 请求体：形状 + 范围 + 枚举（必带 revision）。只保留请求里真的出现的字段。 */
export function parseAppSettingsPatch(value: unknown): { ok: true; patch: AppSettingsPatch } | { ok: false; message: string } {
  const parsed = AppSettingsPatchSchema.safeParse(value);
  if (!parsed.success) return { ok: false, message: describeSettingsProblems(parsed.error) };
  // 分区 schema 带默认值：`partial()` 会把没传的字段也补成默认值，合并时会覆盖已有配置。
  // 这里按请求体里真实出现的键收窄，只改用户改过的字段。
  const raw = value as Record<string, unknown>;
  const patch: AppSettingsPatch = { revision: parsed.data.revision };
  for (const section of ["ui", "execution", "diagnostics", "permissions"] as const) {
    const input = raw[section];
    if (!isRecord(input)) continue;
    const parsedSection = parsed.data[section] as Record<string, unknown> | undefined;
    if (!parsedSection) continue;
    const picked: Record<string, unknown> = {};
    for (const key of Object.keys(input)) {
      if (key in parsedSection) picked[key] = parsedSection[key];
    }
    (patch as Record<string, unknown>)[section] = picked;
  }
  return { ok: true, patch };
}

/** 把 PATCH 的分区字段合并进文档（只碰传了的分区）。 */
export function applyAppSettingsPatch(doc: SettingsDocument, patch: AppSettingsPatch): void {
  if (patch.ui) doc.ui = { ...doc.ui, ...patch.ui };
  if (patch.execution) {
    doc.execution = {
      ...doc.execution,
      ...patch.execution,
      retryBackoffMs: [...(patch.execution.retryBackoffMs ?? doc.execution.retryBackoffMs)],
    };
  }
  if (patch.diagnostics) doc.diagnostics = { ...doc.diagnostics, ...patch.diagnostics };
  if (patch.permissions) doc.permissions = { ...doc.permissions, ...patch.permissions };
}

export type SettingsValidation = { ok: true; migrated: boolean } | { ok: false; message: string };

/**
 * 导入文件校验：逐段检查枚举 / 范围 / 连接形状，v1 文件（无 version）允许并标记 migrated。
 * 返回 ok=false 时调用方必须 400 且不写盘。
 */
export function validateSettingsDocumentInput(value: unknown): SettingsValidation {
  if (!isRecord(value)) return { ok: false, message: "设置文件必须是 JSON 对象" };
  if (value.version !== undefined && value.version !== 1 && value.version !== 2) {
    return { ok: false, message: `不支持的设置版本：${String(value.version)}` };
  }
  if (value.revision !== undefined && !isNonNegativeInt(value.revision)) {
    return { ok: false, message: "revision 必须是非负整数" };
  }
  if (value.connections !== undefined) {
    if (!Array.isArray(value.connections)) return { ok: false, message: "connections 必须是数组" };
    for (const [index, connection] of value.connections.entries()) {
      const parsed = ModelConnectionSchema.safeParse(connection);
      if (!parsed.success) {
        return { ok: false, message: `第 ${index + 1} 个连接不合法（${describeSettingsProblems(parsed.error)}）` };
      }
    }
  }
  for (const key of ["defaultConnection", "defaultModel"] as const) {
    const raw = value[key];
    if (raw !== undefined && typeof raw !== "string") return { ok: false, message: `${key} 必须是字符串` };
  }
  const sections = [
    ["ui", UiSettingsSchema],
    ["execution", ExecutionSettingsSchema],
    ["diagnostics", DiagnosticsSettingsSchema],
    ["permissions", PermissionSettingsSchema],
    ["demo", DemoSettingsSchema],
  ] as const;
  for (const [key, schema] of sections) {
    const raw = value[key];
    if (raw === undefined) continue;
    if (!isRecord(raw)) return { ok: false, message: `${key} 必须是对象` };
    const parsed = schema.partial().safeParse(raw);
    if (!parsed.success) return { ok: false, message: describeSettingsProblems(parsed.error) };
  }
  return { ok: true, migrated: value.version !== SETTINGS_VERSION };
}

/** 导入请求体既接受导出的信封（`{ document }` / `{ settings }`），也接受裸设置文档。 */
export function settingsDocumentOfImportBody(body: unknown): unknown {
  if (!isRecord(body)) return body;
  if (isRecord(body.document)) return body.document;
  if (isRecord(body.settings)) return body.settings;
  return body;
}

/** 文档的四个分区默认值 + revision（测试与迁移共用）。 */
export function appSettingsWithDefaults(overrides: Partial<AppSettings> = {}): AppSettings {
  return { ...defaultAppSettings(), ...overrides };
}

/** 供 `loadSettings` 使用：把文档转成运行期的模型设置（不含 v2 分区）。 */
export function modelSettingsOf(doc: SettingsDocument): ModelSettings {
  return {
    connections: doc.connections,
    defaultConnection: doc.defaultConnection,
    defaultModel: doc.defaultModel,
  };
}
