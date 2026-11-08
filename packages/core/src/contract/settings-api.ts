// 设置中心 HTTP 接口的返回形状（服务端与前端共用）。
//
// 密钥约定（Spec 0004「密钥不落响应」）：设置视图、导出、诊断包都只出末 4 位 + hasKey + source；
// 导出含明文 Key 必须显式 `includeKeys=true`，文件名追加 `-with-keys` 警示。
import type { DemoSettings } from "./engine";
import type { ModelRef } from "./settings";
import type {
  DiagnosticsSettings,
  ExecutionSettings,
  ModelOptions,
  PermissionSettings,
  SettingsDocument,
  UiSettings,
} from "./settings";

/** 一个连接的脱敏视图：没有 apiKey 字段，只有末 4 位与来源。 */
export interface SettingsConnectionView {
  name: string;
  baseUrl: string;
  models: string[];
  modelOptions?: Record<string, ModelOptions>;
  timeoutMs?: number;
  maxRetries?: number;
  /** 只给末 4 位；没配置时为 null */
  apiKeyLast4: string | null;
  hasKey: boolean;
  source: "env" | "file" | "none";
  isDefault: boolean;
}

/** GET /api/settings/app：v2 文档的视图。 */
export interface AppSettingsView {
  ok: true;
  version: number;
  revision: number;
  ui: UiSettings;
  execution: ExecutionSettings;
  diagnostics: DiagnosticsSettings;
  permissions: PermissionSettings;
  connections: SettingsConnectionView[];
  /** 最近一次在会话里选的模型（Spec 0006“会话模型”）；还没选过为 null。取代了旧的 defaultModel */
  lastModel?: ModelRef | null;
  demo: DemoSettings;
  /** 未识别的顶层键（只读展示；写入时原样保留） */
  extra: Record<string, unknown>;
  /** DEEPSEEK_API_KEY 覆盖了文件里的 Key */
  envOverride: boolean;
}

/** PATCH /api/settings/app 冲突时的响应（409）。 */
export interface SettingsConflictBody {
  ok: false;
  error: "revision_conflict";
  /** 服务端当前 revision，界面用它提示“刷新后重试” */
  revision: number;
  message: string;
}

/** PUT /api/settings/role 的响应。 */
export interface SettingsRoleResult {
  ok: true;
  revision: number;
  permissions: PermissionSettings;
}

/** GET /api/settings/export：默认脱敏；`document` 可直接再导入。 */
export interface SettingsExportResult {
  ok: true;
  /** true 表示密钥已抹成空串 */
  redacted: boolean;
  /** 建议的下载文件名；含密钥时带 -with-keys */
  filename: string;
  /** 磁盘格式的 v2 文档（未知顶层键已展开，可直接回传给 import） */
  document: Record<string, unknown>;
  exportedAt: string;
}

/** POST /api/settings/import 的响应。 */
export interface SettingsImportResult {
  ok: true;
  revision: number;
  /** 导入前旧文件的备份路径；原来没有文件时为 null */
  backupPath: string | null;
  /** 导入的是 v1 文件、已经迁移 */
  migrated: boolean;
  /** 重建模型提供者失败时的说明；设置本身已写入 */
  warning?: string;
  /** 工具故障下发失败时的说明；设置本身已写入 */
  toolError?: string;
}

export interface DiagnosticsRuntime {
  bun: string;
  platform: string;
  arch: string;
}

export interface DiagnosticsWorkspace {
  workspaceDir: string;
  employeesDir: string;
  configVersionsDir: string;
  settingsFile: string;
  settingsFileExists: boolean;
}

/** 最近事件摘要：只取最近几个任务各自最后几条事件。 */
export interface DiagnosticsTaskSummary {
  taskId: string;
  employee: string;
  state: string;
  updatedAt: string;
  events: { seq: number; type: string; ts: string }[];
}

/** GET /api/settings/diagnostics：诊断包内容（永远脱敏）。 */
export interface DiagnosticsInfo {
  ok: true;
  generatedAt: string;
  /** 应用版本（诊断页“关于”显示）与设置文档版本 */
  app: { version: string; settingsVersion: number };
  runtime: DiagnosticsRuntime;
  workspace: DiagnosticsWorkspace;
  settings: AppSettingsView;
  tasks: DiagnosticsTaskSummary[];
}

/** POST /api/settings/diagnostics/export 的响应。 */
export interface DiagnosticsExportResult {
  ok: true;
  /** 落盘的诊断包路径 */
  path: string;
  generatedAt: string;
}
