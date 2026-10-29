// 设置页后端（docs/design.md §14.1）：模型连接、演示与调试、工作区。
//
// 安全约定：
//   - API Key 永远不完整出现在任何响应里，只回末 4 位 + hasKey；
//   - DEEPSEEK_API_KEY 环境变量优先于 settings.json，返回里用 source 标明来源；
//   - 工具故障只经管理工具 admin_set_faults 下发给工具子进程，不开放给员工。
import { spawn } from "node:child_process";
import { cpSync, existsSync, mkdirSync, readdirSync, rmSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { Elysia } from "elysia";
import {
  isDeepSeekConnection,
  loadDemoSettings,
  loadSettings,
  readSettingsObject,
  redactApiKey,
  saveDemoSettings,
  testModelConnection,
  writeSettingsObject,
  type DemoSettings,
  type FaultMode,
  type ModelConnection,
  type ModelSettings,
  type ToolName,
} from "@de/core";
import { apiError } from "../http";
import { copyEmployeeConfigs } from "../bootstrap";
import type { ConnectionProbe, FolderOpener, RouteDeps } from "./deps";

/** 可注入故障的工具（管理工具自身、web_search 与 query_task 不在其中）。 */
const FAULTABLE_TOOLS: readonly { name: ToolName; label: string }[] = [
  { name: "validate_submission", label: "表单与附件校验" },
  { name: "load_sales", label: "读取并清洗销售数据" },
  { name: "compute_metrics", label: "计算指标与异常" },
  { name: "parse_minutes", label: "解析周会纪要" },
  { name: "render_report_docx", label: "生成 Word 报告" },
];

const FAULT_MODES: readonly FaultMode[] = ["timeout", "error", "invalid_output"];

interface ConnectionView {
  name: string;
  baseUrl: string;
  models: string[];
  apiKeyLast4: string | null;
  hasKey: boolean;
  source: "env" | "file" | "none";
  isDefault: boolean;
}

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

export function createSettingsRoutes(deps: RouteDeps) {
  const workspaceDir = deps.workspaceDir;
  const env = () => deps.env ?? process.env;
  const now = deps.now ?? (() => new Date());

  /** settings.json 里的连接（不应用环境变量覆盖：写回时不能把环境变量的 Key 落到文件里）。 */
  const fileSettings = (): ModelSettings => loadSettings(workspaceDir, {});

  /** 生效中的设置（应用 DEEPSEEK_API_KEY 覆盖）。 */
  const effectiveSettings = (): ModelSettings => loadSettings(workspaceDir, env());

  function persistSettings(settings: ModelSettings): void {
    const raw = readSettingsObject(workspaceDir);
    raw.connections = settings.connections;
    raw.defaultConnection = settings.defaultConnection;
    raw.defaultModel = settings.defaultModel;
    writeSettingsObject(workspaceDir, raw);
  }

  function viewOf(connection: ModelConnection, defaultConnection: string): ConnectionView {
    const effective = effectiveSettings().connections.find((c) => c.name === connection.name) ?? connection;
    const envKey = env().DEEPSEEK_API_KEY;
    const fromEnv = Boolean(envKey) && isDeepSeekConnection(connection);
    const key = effective.apiKey;
    return {
      name: connection.name,
      baseUrl: connection.baseUrl,
      models: [...connection.models],
      apiKeyLast4: key ? key.slice(-4) : null,
      hasKey: key !== "",
      source: fromEnv ? "env" : key ? "file" : "none",
      isDefault: connection.name === defaultConnection,
    };
  }

  function listConnections() {
    const effective = effectiveSettings();
    const defaultConnection = effective.defaultConnection;
    return {
      ok: true as const,
      defaultConnection,
      defaultModel: effective.defaultModel,
      envOverride: Boolean(env().DEEPSEEK_API_KEY),
      connections: effective.connections.map((c) => viewOf(c, defaultConnection)),
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

      const next: ModelConnection = {
        name,
        baseUrl: typeof baseUrl === "string" ? baseUrl.trim() : existing!.baseUrl,
        // 空字符串表示保持原值；新增连接没有原值时为未配置
        apiKey: typeof apiKey === "string" && apiKey !== "" ? apiKey : (existing?.apiKey ?? ""),
        models,
      };
      if (existing) settings.connections[existingIndex] = next;
      else settings.connections.push(next);
      // 第一个连接自动成为默认连接
      if (!settings.connections.some((c) => c.name === settings.defaultConnection)) settings.defaultConnection = name;
      if (!settings.defaultModel && next.models[0]) settings.defaultModel = next.models[0];
      persistSettings(settings);
      const warning = await notifySettingsSaved();

      const effective = effectiveSettings();
      return {
        ok: true as const,
        connection: viewOf(next, effective.defaultConnection),
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
      const effective = effectiveSettings();
      return {
        ok: true as const,
        connections: effective.connections.map((c) => viewOf(c, effective.defaultConnection)),
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
    .get("/demo", () => ({ ok: true as const, ...demoView() }))
    .put("/demo", async ({ body, set }) => {
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
    .post("/workspace/open", async () => {
      const open: FolderOpener = deps.openFolder ?? ((dir) => openFolderWithSystem(dir));
      try {
        await open(workspaceDir);
      } catch (error) {
        return internalError(`打开工作区失败：${error instanceof Error ? error.message : String(error)}`);
      }
      return { ok: true as const, path: workspaceDir };
    })
    .post("/workspace/reset-employees", async () => {
      const destination = resolve(deps.employeesDir);
      const source = resolve(deps.defaultEmployeesDir ?? deps.employeesDir);
      if (!existsSync(source) || !statSync(source).isDirectory()) {
        return internalError(`默认员工配置目录不存在：${source}`);
      }
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
      return { ok: true as const, backupPath: backupPath ?? null, employees: employeeDirsOf(destination) };
    });
}
