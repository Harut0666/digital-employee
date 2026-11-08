// 设置页后端（docs/design.md §14.1、Spec 0004）。
//
// 安全约定：
//   - API Key 永远不完整出现在任何响应里，只回末 4 位 + hasKey；
//   - DEEPSEEK_API_KEY 环境变量优先于 settings.json，返回里用 source 标明来源；
//   - 工具故障只经管理工具 admin_set_faults 下发给工具子进程，不开放给员工。
//   - 设置文档（v2）的所有写入都走 @de/core 的统一入口，写入时 revision +1；
//     导出默认脱敏，诊断包永远是脱敏快照。
import { spawn } from "node:child_process";
import { copyFileSync, cpSync, existsSync, mkdirSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { Elysia } from "elysia";
import {
  CONNECTION_RETRIES_MAX,
  CONNECTION_TIMEOUT_MAX_MS,
  CONNECTION_TIMEOUT_MIN_MS,
  ModelOptionsSchema,
  SetRoleRequestSchema,
  applyAppSettingsPatch,
  describeSettingsProblems,
  ensureSettingsFile,
  loadDemoSettings,
  loadSettings,
  normalizeSettingsDocument,
  parseAppSettingsPatch,
  readSettingsDocument,
  redactApiKey,
  saveDemoSettings,
  settingsDocumentOfImportBody,
  settingsDocumentView,
  settingsExport,
  settingsPathFor,
  testModelConnection,
  updateSettingsDocument,
  validateSettingsDocumentInput,
  writeSettingsDocument,
  type DemoSettings,
  type DiagnosticsInfo,
  type DiagnosticsTaskSummary,
  type FaultMode,
  type ModelConnection,
  type ModelOptions,
  type ModelSettings,
  type SettingsConflictBody,
  type SettingsExportResult,
  type SettingsImportResult,
  type TaskEvent,
  type ToolName,
} from "@de/core";
import { appendAuditEntry, redactAuditText, type AuditAction, type AuditResult } from "../audit";
import { apiError, roleOf } from "../http";
import { consoleLogger, setConsoleLogLevel } from "../log";
import { copyEmployeeConfigs } from "../bootstrap";
import type { ConnectionProbe, FileOpener, FolderOpener, RouteDeps } from "./deps";

/** 可注入故障的工具（管理工具自身、web_search 与 query_task 不在其中）。 */
const FAULTABLE_TOOLS: readonly { name: ToolName; label: string }[] = [
  { name: "validate_submission", label: "表单与附件校验" },
  { name: "load_sales", label: "读取并清洗销售数据" },
  { name: "compute_metrics", label: "计算指标与异常" },
  { name: "parse_minutes", label: "解析周会纪要" },
  { name: "render_report_docx", label: "生成 Word 报告" },
];

const FAULT_MODES: readonly FaultMode[] = ["timeout", "error", "invalid_output"];

interface ErrorBody {
  ok: false;
  error: "invalid_input" | "not_found" | "internal";
  message: string;
}

function internalError(message: string): ErrorBody {
  return { ok: false, error: "internal", message };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isValidHttpUrl(value: string): boolean {
  try {
    const url = new URL(value);
    return url.protocol === "http:" || url.protocol === "https:";
  } catch {
    return false;
  }
}

/** 连接名作为 URL 路径段与配置里的引用名：不许为空、不许有路径分隔符或控制字符。 */
function validConnectionName(name: string): boolean {
  return name.length > 0 && name.length <= 64 && !/[/\\\u0000-\u001f]/.test(name) && name.trim() === name;
}

function modelsOf(value: unknown): string[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const models = value.map((m) => (typeof m === "string" ? m.trim() : ""));
  if (models.some((m) => m === "")) return undefined;
  return [...new Set(models)];
}

function toolFaultsOf(value: unknown): DemoSettings["toolFaults"] | undefined {
  if (!isRecord(value)) return undefined;
  const out: DemoSettings["toolFaults"] = {};
  for (const [tool, mode] of Object.entries(value)) {
    if (!FAULTABLE_TOOLS.some((t) => t.name === tool)) return undefined;
    if (typeof mode !== "string" || !(FAULT_MODES as readonly string[]).includes(mode)) return undefined;
    out[tool as ToolName] = mode as FaultMode;
  }
  return out;
}

/** 打开工作区文件夹：Windows 用 explorer，macOS 用 open，其它用 xdg-open。 */
function openFolderWithSystem(dir: string): void {
  if (!existsSync(dir)) mkdirSync(dir, { recursive: true });
  const [command, args] =
    process.platform === "win32"
      ? ["explorer.exe", [dir]]
      : process.platform === "darwin"
        ? ["open", [dir]]
        : ["xdg-open", [dir]];
  const child = spawn(command, args, { detached: true, stdio: "ignore" });
  child.on("error", () => {
    /* 打开失败不应让接口报错：界面上再试一次即可 */
  });
  child.unref();
}

/** 用系统编辑器打开文件：Windows 用 notepad（不依赖关联程序），macOS/Linux 用关联程序。 */
function openFileWithSystem(path: string): void {
  const [command, args] =
    process.platform === "win32"
      ? ["notepad.exe", [path]]
      : process.platform === "darwin"
        ? ["open", [path]]
        : ["xdg-open", [path]];
  const child = spawn(command, args, { detached: true, stdio: "ignore" });
  child.on("error", () => {
    /* 打开失败不应让接口报错 */
  });
  child.unref();
}

/** 备份文件名用的紧凑时间戳（不含冒号、点，Windows 也能当文件名）。 */
function compactStamp(date: Date): string {
  return date.toISOString().replace(/[-:.]/g, "").slice(0, 15);
}

function uniqueFile(parent: string, name: string): string {
  const dot = name.lastIndexOf(".");
  const base = dot > 0 ? name.slice(0, dot) : name;
  const ext = dot > 0 ? name.slice(dot) : "";
  let candidate = join(parent, name);
  for (let i = 2; existsSync(candidate); i++) candidate = join(parent, `${base}-${i}${ext}`);
  return candidate;
}

function uniqueDir(parent: string, name: string): string {
  let candidate = join(parent, name);
  for (let i = 2; existsSync(candidate); i++) candidate = join(parent, `${name}-${i}`);
  return candidate;
}

function employeeDirsOf(dir: string): string[] {
  if (!existsSync(dir)) return [];
  return readdirSync(dir, { withFileTypes: true })
    .filter((e) => e.isDirectory() && existsSync(join(dir, e.name, "workflow.yaml")))
    .map((e) => e.name)
    .sort();
}

// ---- T20：连接卡片“高级”区（每模型参数 + 连接级超时/重试）
// 约定：字段缺省 = 保留原值；显式 null = 清除；值 = 校验后写入。

type ParsedField<T> = { ok: true; value: T | null } | { ok: false; message: string };

/** 每模型高级参数（按模型 id）；未知字段或超范围一律 400。 */
function modelOptionsOf(value: unknown): ParsedField<Record<string, ModelOptions>> {
  if (value === null) return { ok: true, value: null };
  if (!isRecord(value)) return { ok: false, message: "modelOptions 必须是 模型 id → 高级参数 的对象" };
  const options: Record<string, ModelOptions> = {};
  for (const [modelId, raw] of Object.entries(value)) {
    if (modelId.trim() === "" || modelId.trim() !== modelId) {
      return { ok: false, message: "modelOptions 的模型 id 不能为空，也不能有首尾空格" };
    }
    const parsed = ModelOptionsSchema.safeParse(raw);
    if (!parsed.success) {
      return { ok: false, message: `模型 ${modelId} 的高级参数不合法（${describeSettingsProblems(parsed.error)}）` };
    }
    options[modelId] = parsed.data;
  }
  return { ok: true, value: options };
}

/** 连接级超时（毫秒，整数，1 秒–1 小时）。 */
function connectionTimeoutOf(value: unknown): ParsedField<number> {
  if (value === null) return { ok: true, value: null };
  if (typeof value !== "number" || !Number.isInteger(value) || value < CONNECTION_TIMEOUT_MIN_MS || value > CONNECTION_TIMEOUT_MAX_MS) {
    return { ok: false, message: `timeoutMs 必须是 ${CONNECTION_TIMEOUT_MIN_MS}–${CONNECTION_TIMEOUT_MAX_MS} 之间的整数` };
  }
  return { ok: true, value };
}

/** 连接级重试次数（0–10，0 表示不重试）。 */
function connectionRetriesOf(value: unknown): ParsedField<number> {
  if (value === null) return { ok: true, value: null };
  if (typeof value !== "number" || !Number.isInteger(value) || value < 0 || value > CONNECTION_RETRIES_MAX) {
    return { ok: false, message: `maxRetries 必须是 0–${CONNECTION_RETRIES_MAX} 之间的整数` };
  }
  return { ok: true, value };
}

const ADVANCED_CONNECTION_KEYS = ["modelOptions", "timeoutMs", "maxRetries"] as const;
type AdvancedConnectionKey = (typeof ADVANCED_CONNECTION_KEYS)[number];
/** 三段同形：有值 = 校验后写入；null = 清除；缺省 = 保留原值。 */
type AdvancedConnectionValues = { [K in AdvancedConnectionKey]: ModelConnection[K] | null | undefined };

const ADVANCED_CONNECTION_PARSERS: {
  [K in AdvancedConnectionKey]: (value: unknown) => ParsedField<NonNullable<ModelConnection[K]>>;
} = {
  modelOptions: modelOptionsOf,
  timeoutMs: connectionTimeoutOf,
  maxRetries: connectionRetriesOf,
};

function parseAdvancedConnectionFields(
  fields: Record<string, unknown>,
): { ok: true; values: AdvancedConnectionValues } | { ok: false; message: string } {
  const values: AdvancedConnectionValues = { modelOptions: undefined, timeoutMs: undefined, maxRetries: undefined };
  for (const key of ADVANCED_CONNECTION_KEYS) {
    const raw = fields[key];
    if (raw === undefined) continue;
    const parsed = ADVANCED_CONNECTION_PARSERS[key](raw);
    if (!parsed.ok) return parsed;
    (values as Record<AdvancedConnectionKey, unknown>)[key] = parsed.value;
  }
  return { ok: true, values };
}

/** 缺省保留原值、null 清除、有值写入（旧界面不传时不能因为保存连接就丢掉高级参数）。 */
function mergeAdvancedConnectionFields(
  next: ModelConnection,
  existing: ModelConnection | undefined,
  fields: Record<string, unknown>,
  values: AdvancedConnectionValues,
): void {
  for (const key of ADVANCED_CONNECTION_KEYS) {
    if (fields[key] === undefined) {
      if (existing?.[key] !== undefined) (next as unknown as Record<string, unknown>)[key] = existing[key];
      continue;
    }
    const value = values[key];
    if (value !== null && value !== undefined) (next as unknown as Record<string, unknown>)[key] = value;
  }
}

export function createSettingsRoutes(deps: RouteDeps) {
  const workspaceDir = deps.workspaceDir;
  const env = () => deps.env ?? process.env;
  const now = deps.now ?? (() => new Date());

  /**
   * 写一条审计（Spec 0004「危险操作」、T23）：角色优先取请求头 x-role，
   * 没带时用设置里的 defaultRole（设置页请求由前端带角色，命令行/测试可以省略）。
   * 审计写失败只打 warn，绝不让业务操作因此失败。
   */
  function audit(
    action: AuditAction,
    target: string,
    result: AuditResult,
    headers?: Record<string, string | undefined>,
    detail?: string,
  ): void {
    try {
      const role = (headers ? roleOf(headers) : undefined) ?? readSettingsDocument(workspaceDir).permissions.defaultRole;
      appendAuditEntry(workspaceDir, {
        time: now(),
        role,
        action,
        target,
        result,
        ...(detail ? { detail: redactAuditText(detail) } : {}),
      });
    } catch (error) {
      consoleLogger.warn(`审计写入失败（${action}）：${error instanceof Error ? error.message : String(error)}`);
    }
  }

  /** 请求体是否意图切到危险权限（用于成功/失败都留痕；详情由路由判断）。 */
  function dangerousIntentOf(body: unknown): { sandbox: boolean; approval: boolean } {
    const permissions = isRecord(body) && isRecord(body.permissions) ? body.permissions : undefined;
    return {
      sandbox: permissions?.sandboxMode === "danger-full-access",
      approval: permissions?.approvalPolicy === "never",
    };
  }

  /** 危险预设被拒绝（400 / 409）时，两个字段分别留痕。 */
  function auditDangerousIntentFailures(
    intent: { sandbox: boolean; approval: boolean },
    headers: Record<string, string | undefined> | undefined,
    detail: string,
  ): void {
    if (intent.sandbox) audit("set-sandbox-mode", "permissions.sandboxMode", "failure", headers, detail);
    if (intent.approval) audit("set-approval-policy", "permissions.approvalPolicy", "failure", headers, detail);
  }

  /** settings.json 里的连接（不应用环境变量覆盖：写回时不能把环境变量的 Key 落到文件里）。 */
  const fileSettings = (): ModelSettings => loadSettings(workspaceDir, {});

  /** 生效中的设置（应用 DEEPSEEK_API_KEY 覆盖）。 */
  const effectiveSettings = (): ModelSettings => loadSettings(workspaceDir, env());

  /** 写连接：经过统一写入入口（`revision + 1`，v1 文档在这里迁移成 v2）。 */
  function persistSettings(settings: ModelSettings): void {
    updateSettingsDocument(workspaceDir, (doc) => {
      doc.connections = settings.connections;
      // 会话模型（Spec 0006）取代了 defaultModel：保存连接不再改“默认模型”，lastModel 保持不变
    });
  }

  /** 文档视图（密钥只出末 4 位 + hasKey + source）。 */
  function appView() {
    return settingsDocumentView(readSettingsDocument(workspaceDir), env());
  }

  function listConnections() {
    const view = appView();
    return {
      ok: true as const,
      lastModel: view.lastModel ?? null,
      envOverride: view.envOverride,
      connections: view.connections,
    };
  }

  async function notifySettingsSaved(): Promise<string | undefined> {
    if (!deps.onSettingsSaved) return undefined;
    try {
      await deps.onSettingsSaved(effectiveSettings());
      return undefined;
    } catch (error) {
      return error instanceof Error ? error.message : String(error);
    }
  }

  async function applyToolFaults(toolFaults: DemoSettings["toolFaults"]): Promise<string | undefined> {
    if (deps.faults) {
      deps.faults.tools = toolFaults;
    }
    if (!deps.toolClient) return undefined;
    try {
      const result = await deps.toolClient.call("admin_set_faults", { tools: toolFaults });
      return result.ok ? undefined : result.error;
    } catch (error) {
      return error instanceof Error ? error.message : String(error);
    }
  }

  const demoView = () => {
    const demo = loadDemoSettings(workspaceDir);
    return { ...demo, tools: FAULTABLE_TOOLS.map((t) => ({ name: t.name, label: t.label })) };
  };

  // ---------------------------------------------------------------- T19：设置文档 v2

  const appVersion = () => env().DE_VERSION ?? "0.0.0";

  function conflictBody(revision: number): SettingsConflictBody {
    return { ok: false, error: "revision_conflict", revision, message: "设置已被修改，请刷新后重试" };
  }

  const includeKeysOf = (query: unknown): boolean => {
    const raw = isRecord(query) ? query.includeKeys : undefined;
    return raw === "true" || raw === "1" || raw === true;
  };

  const exportFilename = (includeKeys: boolean, date: Date): string =>
    `settings${includeKeys ? "-with-keys" : ""}-${compactStamp(date)}.json`;

  /** 导入 / 恢复前把当前 settings.json 备份到 config-versions/settings/<时间戳>.json。 */
  function backupSettingsFile(): string | null {
    const file = settingsPathFor(workspaceDir);
    if (!existsSync(file)) return null;
    const dir = join(workspaceDir, "config-versions", "settings");
    mkdirSync(dir, { recursive: true });
    const path = uniqueFile(dir, `${compactStamp(now())}.json`);
    copyFileSync(file, path);
    return path;
  }

  /** 最近事件摘要：最近几个任务各自最后几条事件（诊断包里给人看，不写回任务数据）。 */
  async function recentTaskSummaries(limit = 3, eventsPerTask = 5): Promise<DiagnosticsTaskSummary[]> {
    try {
      const summaries = await deps.engine.list();
      const recent = [...summaries]
        .sort((a, b) => (a.updatedAt < b.updatedAt ? 1 : a.updatedAt > b.updatedAt ? -1 : 0))
        .slice(0, limit);
      const out: DiagnosticsTaskSummary[] = [];
      for (const summary of recent) {
        let events: TaskEvent[] = [];
        try {
          events = await deps.engine.events(summary.id);
        } catch {
          events = [];
        }
        out.push({
          taskId: summary.id,
          employee: summary.employee,
          state: summary.state,
          updatedAt: summary.updatedAt,
          events: events.slice(-eventsPerTask).map((event) => ({ seq: event.seq, type: event.type, ts: event.ts })),
        });
      }
      return out;
    } catch {
      return [];
    }
  }

  /** 诊断包内容：版本、运行时、工作区、脱敏设置快照、最近事件。永远不含明文 Key。 */
  async function diagnosticsInfo(): Promise<DiagnosticsInfo> {
    const doc = readSettingsDocument(workspaceDir);
    const settingsFile = settingsPathFor(workspaceDir);
    return {
      ok: true,
      generatedAt: now().toISOString(),
      app: { version: appVersion(), settingsVersion: doc.version },
      runtime: { bun: Bun.version, platform: process.platform, arch: process.arch },
      workspace: {
        workspaceDir,
        employeesDir: deps.employeesDir,
        configVersionsDir: join(workspaceDir, "config-versions"),
        settingsFile,
        settingsFileExists: existsSync(settingsFile),
      },
      settings: settingsDocumentView(doc, env()),
      tasks: await recentTaskSummaries(),
    };
  }

  return new Elysia({ prefix: "/api/settings" })
    .get("/", () => listConnections())
    .put("/connections/:name", async ({ params, body, set }) => {
      const name = params.name;
      if (!validConnectionName(name)) {
        set.status = 400;
        return apiError("invalid_input", "连接名不能为空，且不能包含路径分隔符或控制字符");
      }
      const fields = isRecord(body) ? body : {};
      const settings = fileSettings();
      const existingIndex = settings.connections.findIndex((c) => c.name === name);
      const existing = existingIndex >= 0 ? settings.connections[existingIndex] : undefined;

      const baseUrl = fields.baseUrl;
      if (baseUrl !== undefined) {
        if (typeof baseUrl !== "string" || !isValidHttpUrl(baseUrl.trim())) {
          set.status = 400;
          return apiError("invalid_input", "Base URL 必须是 http(s) 地址");
        }
      } else if (!existing) {
        set.status = 400;
        return apiError("invalid_input", "新增连接必须填写 Base URL");
      }

      const apiKey = fields.apiKey;
      if (apiKey !== undefined && typeof apiKey !== "string") {
        set.status = 400;
        return apiError("invalid_input", "apiKey 必须是字符串");
      }

      const models = fields.models !== undefined ? modelsOf(fields.models) : existing?.models;
      if (models === undefined) {
        set.status = 400;
        return apiError("invalid_input", "models 必须是非空字符串数组");
      }

      // 高级参数：先校验，全部合法才动文件
      const parsedAdvanced = parseAdvancedConnectionFields(fields);
      if (!parsedAdvanced.ok) {
        set.status = 400;
        return apiError("invalid_input", parsedAdvanced.message);
      }

      const next: ModelConnection = {
        name,
        baseUrl: typeof baseUrl === "string" ? baseUrl.trim() : existing!.baseUrl,
        // 空字符串表示保持原值；新增连接没有原值时为未配置
        apiKey: typeof apiKey === "string" && apiKey !== "" ? apiKey : (existing?.apiKey ?? ""),
        models,
      };
      mergeAdvancedConnectionFields(next, existing, fields, parsedAdvanced.values);
      if (existing) settings.connections[existingIndex] = next;
      else settings.connections.push(next);
      // 第一个连接自动成为默认连接
      if (!settings.connections.some((c) => c.name === settings.defaultConnection)) settings.defaultConnection = name;
      if (!settings.defaultModel && next.models[0]) settings.defaultModel = next.models[0];
      persistSettings(settings);
      const warning = await notifySettingsSaved();

      const view = appView();
      return {
        ok: true as const,
        connection: view.connections.find((c) => c.name === name) ?? view.connections[0],
        ...(warning ? { warning } : {}),
      };
    })
    .delete("/connections/:name", async ({ params, set }) => {
      const name = params.name;
      const settings = fileSettings();
      const index = settings.connections.findIndex((c) => c.name === name);
      if (index < 0) {
        set.status = 404;
        return apiError("not_found", `连接不存在：${name}`);
      }
      settings.connections.splice(index, 1);
      if (settings.defaultConnection === name) settings.defaultConnection = settings.connections[0]?.name ?? "";
      persistSettings(settings);
      const warning = await notifySettingsSaved();
      return {
        ok: true as const,
        connections: appView().connections,
        ...(warning ? { warning } : {}),
      };
    })
    .post("/connections/:name/test", async ({ params, body, set }) => {
      const name = params.name;
      const connection = effectiveSettings().connections.find((c) => c.name === name);
      if (!connection) {
        set.status = 404;
        return apiError("not_found", `连接不存在：${name}`);
      }
      const fields = isRecord(body) ? body : {};
      const requested = typeof fields.model === "string" && fields.model.trim() !== "" ? fields.model.trim() : undefined;
      const model = requested ?? connection.models[0] ?? effectiveSettings().defaultModel;
      if (!connection.apiKey) {
        return { ok: false as const, latencyMs: 0, model, error: "未配置 API Key，无法测试连接" };
      }
      const probe: ConnectionProbe = deps.probeConnection ?? testModelConnection;
      try {
        const result = await probe(connection, model);
        return {
          ok: result.ok,
          latencyMs: result.latencyMs,
          model: result.model || model,
          ...(result.error ? { error: redactApiKey(result.error, connection.apiKey) } : {}),
        };
      } catch (error) {
        return {
          ok: false as const,
          latencyMs: 0,
          model,
          error: redactApiKey(error instanceof Error ? error.message : String(error), connection.apiKey),
        };
      }
    })
    // ---- 设置文档 v2（T19）：GET/PATCH app、role、export/import、diagnostics、open-file
    .get("/app", () => appView())
    .patch("/app", ({ body, set, headers }) => {
      const intent = dangerousIntentOf(body);
      const parsed = parseAppSettingsPatch(body);
      if (!parsed.ok) {
        auditDangerousIntentFailures(intent, headers, parsed.message);
        set.status = 400;
        return apiError("invalid_input", parsed.message);
      }
      const current = readSettingsDocument(workspaceDir);
      if (parsed.patch.revision !== current.revision) {
        const message = conflictBody(current.revision).message;
        auditDangerousIntentFailures(intent, headers, message);
        set.status = 409;
        return conflictBody(current.revision);
      }
      const written = updateSettingsDocument(workspaceDir, (doc) => applyAppSettingsPatch(doc, parsed.patch));
      // 日志级别即时生效（Spec 0004）：与 start.ts 共用同一个 consoleLogger；重启后按文件值生效
      setConsoleLogLevel(written.diagnostics.logLevel);
      // 危险预设：成功也留痕（含“从不询问”，它只在显式确认后写入）
      if (intent.sandbox) audit("set-sandbox-mode", "permissions.sandboxMode", "success", headers, `sandboxMode=${written.permissions.sandboxMode}`);
      if (intent.approval) audit("set-approval-policy", "permissions.approvalPolicy", "success", headers, `approvalPolicy=${written.permissions.approvalPolicy}`);
      const changed = (["ui", "execution", "diagnostics", "permissions"] as const).filter(
        (key) => parsed.patch[key] !== undefined,
      );
      consoleLogger.debug(`设置已保存：revision ${written.revision}${changed.length > 0 ? `（${changed.join("、")}）` : ""}`);
      return settingsDocumentView(written, env());
    })
    .put("/role", ({ body, set }) => {
      const parsed = SetRoleRequestSchema.safeParse(body);
      if (!parsed.success) {
        set.status = 400;
        return apiError("invalid_input", describeSettingsProblems(parsed.error));
      }
      const current = readSettingsDocument(workspaceDir);
      if (parsed.data.revision !== undefined && parsed.data.revision !== current.revision) {
        set.status = 409;
        return conflictBody(current.revision);
      }
      const written = updateSettingsDocument(workspaceDir, (doc) => {
        doc.permissions.defaultRole = parsed.data.role;
      });
      return { ok: true as const, revision: written.revision, permissions: written.permissions };
    })
    .get("/export", ({ query, set, headers }) => {
      const includeKeys = includeKeysOf(query);
      const filename = exportFilename(includeKeys, now());
      try {
        const result: SettingsExportResult = {
          ok: true,
          redacted: !includeKeys,
          filename,
          document: settingsExport(readSettingsDocument(workspaceDir), { includeKeys }),
          exportedAt: now().toISOString(),
        };
        set.headers["content-disposition"] = `attachment; filename="${filename}"`;
        // 含密钥导出是危险操作：响应里带明文（用户已确认），但审计行永远不含 Key
        if (includeKeys) audit("export-settings-with-keys", filename, "success", headers, "导出文件含明文 API Key（已二次确认）");
        return result;
      } catch (error) {
        if (includeKeys) {
          audit("export-settings-with-keys", filename, "failure", headers, error instanceof Error ? error.message : String(error));
        }
        throw error;
      }
    })
    .post("/import", async ({ body, set, headers }) => {
      const candidate = settingsDocumentOfImportBody(body);
      const validation = validateSettingsDocumentInput(candidate);
      if (!validation.ok) {
        audit("import-settings", "settings.json", "failure", headers, validation.message);
        set.status = 400;
        return apiError("invalid_input", validation.message);
      }
      try {
        const current = readSettingsDocument(workspaceDir);
        const backupPath = backupSettingsFile();
        const imported = normalizeSettingsDocument(candidate as Record<string, unknown>);
        // 导入的文档可能带着其它机器的 revision：以本机当前值 +1 继续，避免客户端旧 revision 误判
        imported.revision = current.revision;
        const written = writeSettingsDocument(workspaceDir, imported);
        setConsoleLogLevel(written.diagnostics.logLevel);
        const warning = await notifySettingsSaved();
        // 导入的 demo 段也可能变了：同步运行时开关与工具子进程
        if (deps.faults) {
          deps.faults.corruptTraceability = written.demo.corruptTraceability;
          deps.faults.crashOnNextStep = written.demo.crashOnNextStep;
        }
        const toolError = await applyToolFaults(written.demo.toolFaults);
        const result: SettingsImportResult = {
          ok: true,
          revision: written.revision,
          backupPath,
          migrated: validation.migrated,
          ...(warning ? { warning } : {}),
          ...(toolError ? { toolError } : {}),
        };
        audit(
          "import-settings",
          "settings.json",
          "success",
          headers,
          `revision=${written.revision}${backupPath ? `，旧文件已备份` : ""}`,
        );
        return result;
      } catch (error) {
        audit("import-settings", "settings.json", "failure", headers, error instanceof Error ? error.message : String(error));
        throw error;
      }
    })
    .get("/diagnostics", async () => diagnosticsInfo())
    .post("/diagnostics/export", async () => {
      const info = await diagnosticsInfo();
      const dir = join(workspaceDir, "diagnostics");
      mkdirSync(dir, { recursive: true });
      const path = uniqueFile(dir, `diagnostic-${compactStamp(now())}.json`);
      writeFileSync(path, `${JSON.stringify(info, null, 2)}\n`);
      return { ok: true as const, path, generatedAt: info.generatedAt };
    })
    .post("/diagnostics/open", async ({ set }) => {
      // 导出成功后“打开所在目录”：诊断包统一落在 <workspace>/diagnostics
      const dir = join(workspaceDir, "diagnostics");
      mkdirSync(dir, { recursive: true });
      const open: FolderOpener = deps.openFolder ?? ((target) => openFolderWithSystem(target));
      try {
        await open(dir);
      } catch (error) {
        set.status = 500;
        return internalError(`打开诊断目录失败：${error instanceof Error ? error.message : String(error)}`);
      }
      return { ok: true as const, path: dir };
    })
    .post("/open-file", async ({ set }) => {
      // 没有 settings.json 时先写一份默认文档，保证“打开设置文件”总能打开点什么
      ensureSettingsFile(workspaceDir);
      const path = settingsPathFor(workspaceDir);
      const open: FileOpener = deps.openFile ?? ((target) => openFileWithSystem(target));
      try {
        await open(path);
      } catch (error) {
        set.status = 500;
        return internalError(`打开设置文件失败：${error instanceof Error ? error.message : String(error)}`);
      }
      return { ok: true as const, path };
    })
    .get("/demo", () => ({ ok: true as const, ...demoView() }))
    .put("/demo", async ({ body, set, headers }) => {
      const fields = isRecord(body) ? body : {};
      const patch: Partial<DemoSettings> = {};

      if ("toolFaults" in fields) {
        const toolFaults = toolFaultsOf(fields.toolFaults);
        if (toolFaults === undefined) {
          set.status = 400;
          return apiError("invalid_input", "toolFaults 必须是 工具名 → timeout / error / invalid_output 的对象");
        }
        patch.toolFaults = toolFaults;
      }
      for (const key of ["corruptTraceability", "crashOnNextStep"] as const) {
        if (!(key in fields)) continue;
        if (typeof fields[key] !== "boolean") {
          set.status = 400;
          return apiError("invalid_input", `${key} 必须是布尔值`);
        }
        patch[key] = fields[key] as boolean;
      }

      const demo = saveDemoSettings(workspaceDir, patch);
      if (deps.faults) {
        deps.faults.corruptTraceability = demo.corruptTraceability;
        deps.faults.crashOnNextStep = demo.crashOnNextStep;
      }
      // 模拟崩溃是会真的退出进程的危险操作：布防时留痕（解除不需要）
      if (patch.crashOnNextStep === true) {
        audit("simulate-crash", "demo.crashOnNextStep", "success", headers, "下一次步骤开始后进程退出（一次性）");
      }
      const toolError = await applyToolFaults(demo.toolFaults);
      return { ok: true as const, demo, tools: demoView().tools, ...(toolError ? { toolError } : {}) };
    })
    .get("/workspace", () => ({
      ok: true as const,
      workspaceDir,
      employeesDir: deps.employeesDir,
      defaultEmployeesDir: deps.defaultEmployeesDir ?? deps.employeesDir,
      configVersionsDir: join(workspaceDir, "config-versions"),
    }))
    .post("/workspace/open", async ({ set }) => {
      const open: FolderOpener = deps.openFolder ?? ((dir) => openFolderWithSystem(dir));
      try {
        await open(workspaceDir);
      } catch (error) {
        set.status = 500;
        return internalError(`打开工作区失败：${error instanceof Error ? error.message : String(error)}`);
      }
      return { ok: true as const, path: workspaceDir };
    })
    .post("/workspace/reset-employees", async ({ headers, set }) => {
      const destination = resolve(deps.employeesDir);
      const source = resolve(deps.defaultEmployeesDir ?? deps.employeesDir);
      if (!existsSync(source) || !statSync(source).isDirectory()) {
        const message = `默认员工配置目录不存在：${source}`;
        audit("reset-employees", destination, "failure", headers, message);
        set.status = 500;
        return internalError(message);
      }
      try {
        const stamp = now().toISOString().replace(/[-:T]/g, "").slice(0, 14) || `${Date.now()}`;
        let backupPath: string | undefined;
        if (existsSync(destination)) {
          const versions = join(workspaceDir, "config-versions", "employees");
          mkdirSync(versions, { recursive: true });
          backupPath = uniqueDir(versions, stamp);
          cpSync(destination, backupPath, { recursive: true });
        }
        // 只有两个目录不同才覆盖；开发时员工目录可能就是仓库里的默认目录，此时只备份
        if (destination !== source) {
          rmSync(destination, { recursive: true, force: true });
          copyEmployeeConfigs(source, destination);
        }
        audit("reset-employees", destination, "success", headers, backupPath ? "旧配置已备份到 config-versions/employees/" : "没有旧配置，直接创建默认配置");
        return { ok: true as const, backupPath: backupPath ?? null, employees: employeeDirsOf(destination) };
      } catch (error) {
        audit("reset-employees", destination, "failure", headers, error instanceof Error ? error.message : String(error));
        throw error;
      }
    });
}
