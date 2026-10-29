// T3 实现：Elysia 服务。导出 App 类型供前端 Eden 使用。
import { existsSync } from "node:fs";
import { readdir } from "node:fs/promises";
import { join } from "node:path";
import { cors } from "@elysiajs/cors";
import { Elysia } from "elysia";
import { loadEmployeeConfig, isEmployeeId, resolveWorkspace, type EmployeeConfig, type FaultHooks, type ModelConnectionTest, type ModelConnection, type ModelSettings, type TaskEngine, type ToolClient } from "@de/core";
import { downloadResponse, staticResponse } from "./files";
import { eventStream } from "./sse";
import { apiError, filesOf, parseAfter, parseCommand, parseJsonField, roleOf, ROLE_REQUIRED, saveUploads, statusOfResult } from "./http";
import { createChatRoutes } from "./routes/chat";
import { createConfigRoutes } from "./routes/config";
import { createSampleRoutes } from "./routes/samples";
import { createSettingsRoutes } from "./routes/settings";

export interface ServerOptions {
  engine: TaskEngine;
  employeesDir: string;
  /** 工作区根目录；不传时用 resolveWorkspace()（%APPDATA%/digital-employee，DE_WORKSPACE 可覆盖） */
  workspaceDir?: string;
  /** 工具服务器客户端，供设置页下发故障注入（admin_set_faults） */
  toolClient?: ToolClient;
  /** 设置保存后重建模型提供者，由启动入口提供 */
  onSettingsSaved?: (settings: ModelSettings) => void | Promise<void>;
  /** 演示开关的运行时可变对象，与引擎共享同一个引用 */
  faults?: FaultHooks;
  /** 默认员工配置目录（“恢复默认员工配置”的来源） */
  defaultEmployeesDir?: string;
  /** 测试连接的可注入实现；不传时发真实请求 */
  probeConnection?: (connection: ModelConnection, model: string) => Promise<ModelConnectionTest>;
  /** 打开文件夹的可注入实现 */
  openFolder?: (dir: string) => void | Promise<void>;
  /** 读取环境变量（DEEPSEEK_API_KEY 等），测试注入 */
  env?: Record<string, string | undefined>;
  /** 测试注入时钟 */
  now?: () => Date;
  /** 前端构建产物目录；不传则不托管静态资源 */
  webDistDir?: string;
  /** 员工配置加载器，默认用 @de/core 的 loadEmployeeConfig；测试时可替换 */
  loadEmployee?: (dir: string) => Promise<EmployeeConfig>;
}

export function createApp(options: ServerOptions) {
  const { engine, employeesDir } = options;
  const loadEmployee = options.loadEmployee ?? loadEmployeeConfig;
  const routeDeps = {
    engine,
    employeesDir,
    workspaceDir: options.workspaceDir ?? resolveWorkspace(),
    toolClient: options.toolClient,
    onSettingsSaved: options.onSettingsSaved,
    faults: options.faults,
    defaultEmployeesDir: options.defaultEmployeesDir,
    probeConnection: options.probeConnection,
    openFolder: options.openFolder,
    env: options.env,
    now: options.now,
  };

  async function listEmployees() {
    const entries = await readdir(employeesDir, { withFileTypes: true }).catch(() => []);
    // 只有含 workflow.yaml、且目录名是合法员工 id 的目录才是员工（employees/test 之类的目录不是）
    const dirs = entries.filter(
      (e) =>
        e.isDirectory() &&
        !e.name.startsWith(".") &&
        e.name !== "node_modules" &&
        isEmployeeId(e.name) &&
        existsSync(join(employeesDir, e.name, "workflow.yaml")),
    );
    const result = [];
    for (const d of dirs.sort((a, b) => a.name.localeCompare(b.name))) {
      const cfg = await loadEmployee(join(employeesDir, d.name));
      result.push({
        id: cfg.id,
        name: cfg.workflow.name,
        description: cfg.workflow.description,
        formSchema: cfg.formSchema,
        formUi: cfg.formUi,
        /** 退回时可勾选的章节（取自 qc.yaml 的 structure.sections） */
        reviewSections: cfg.qc.builtin.structure.sections,
      });
    }
    return result;
  }

  return new Elysia()
    .use(cors())
    .onError(({ code, error, set }) => {
      if (code === "NOT_FOUND") {
        set.status = 404;
        return apiError("not_found", "路径不存在");
      }
      set.status = 500;
      return { ok: false, error: "internal", message: error instanceof Error ? error.message : String(error) };
    })
    // 设置页、员工配置页、前台对话各自一个路由模块（T10 / T11 / T12 填充）。
    .use(createSettingsRoutes(routeDeps))
    .use(createConfigRoutes(routeDeps))
    .use(createChatRoutes(routeDeps))
    .use(createSampleRoutes(routeDeps))
    .group("/api", (api) =>
      api
        .get("/employees", () => listEmployees())
        .get("/tasks", () => engine.list())
        .post("/tasks", async ({ body, headers, set }) => {
          const role = roleOf(headers);
          if (!role) {
            set.status = 403;
            return ROLE_REQUIRED;
          }
          const fields = (body ?? {}) as Record<string, unknown>;
          const employee = fields.employee;
          if (typeof employee !== "string" || employee === "") {
            set.status = 400;
            return apiError("invalid_input", "缺少 employee");
          }
          // 与契约同一套规则：非法 id 在这里就是 400，而不是等引擎抛错变成 500
          if (!isEmployeeId(employee)) {
            set.status = 400;
            return apiError("invalid_input", "员工 id 不合法：只允许小写字母、数字和连字符");
          }
          const form = parseJsonField(fields.form ?? "{}");
          if (!form.ok || typeof form.value !== "object" || form.value === null || Array.isArray(form.value)) {
            set.status = 400;
            return apiError("invalid_input", "form 必须是 JSON 对象");
          }
          const uploads = await saveUploads(filesOf(fields.attachments));
          try {
            const snapshot = await engine.create({
              employee,
              form: form.value as Record<string, unknown>,
              attachments: uploads.attachments,
              actor: role,
            });
            set.status = 201;
            return snapshot;
          } finally {
            await uploads.cleanup();
          }
        })
        .get("/tasks/:id", async ({ params, set }) => {
          const snapshot = await engine.get(params.id);
          if (!snapshot) {
            set.status = 404;
            return apiError("not_found", `任务不存在：${params.id}`);
          }
          return snapshot;
        })
        .post("/tasks/:id/commands", async ({ params, body, headers, set }) => {
          const role = roleOf(headers);
          if (!role) {
            set.status = 403;
            return ROLE_REQUIRED;
          }
          // JSON：body 即命令；multipart：command 字段是命令 JSON，附件在 attachments 里
          // （Elysia 会把 multipart 里形如 JSON 的字段预先解析成对象，字符串则需要自己解析）
          const fields = (typeof body === "object" && body !== null ? body : {}) as Record<string, unknown>;
          const isMultipart = "command" in fields;
          let raw: unknown = body;
          if (isMultipart) {
            const parsed = parseJsonField(fields.command);
            if (!parsed.ok) {
              set.status = 400;
              return apiError("invalid_input", "command 不是合法的 JSON");
            }
            raw = parsed.value;
          }
          const parsed = parseCommand(raw);
          if (!parsed.ok) {
            set.status = 400;
            return apiError("invalid_input", parsed.message);
          }
          const files = isMultipart ? filesOf(fields.attachments) : [];
          const uploads = await saveUploads(files);
          try {
            let command = parsed.command;
            if (command.type === "supplement" && uploads.attachments.length > 0) {
              command = { ...command, attachments: [...(command.attachments ?? []), ...uploads.attachments] };
            }
            const result = await engine.command(params.id, command, role);
            set.status = statusOfResult(result);
            return result;
          } finally {
            await uploads.cleanup();
          }
        })
        .get("/tasks/:id/events", async ({ params, query, set }) => {
          const after = parseAfter(query.after);
          if (after === undefined) {
            set.status = 400;
            return apiError("invalid_input", "after 必须是非负整数");
          }
          if (!(await engine.get(params.id))) {
            set.status = 404;
            return apiError("not_found", `任务不存在：${params.id}`);
          }
          return engine.events(params.id, after);
        })
        .get("/tasks/:id/stream", async ({ params, query, headers, request, set }) => {
          // 重连时浏览器会带 Last-Event-ID，优先于 ?after=
          const after = parseAfter(headers["last-event-id"] ?? query.after);
          if (after === undefined) {
            set.status = 400;
            return apiError("invalid_input", "after 必须是非负整数");
          }
          if (!(await engine.get(params.id))) {
            set.status = 404;
            return apiError("not_found", `任务不存在：${params.id}`);
          }
          return eventStream(engine, params.id, after, request.signal);
        })
        .get("/tasks/:id/deliverables/:kind", async ({ params, set }) => {
          const snapshot = await engine.get(params.id);
          if (!snapshot) {
            set.status = 404;
            return apiError("not_found", `任务不存在：${params.id}`);
          }
          const ref = snapshot.deliverables.find((d) => d.kind === params.kind);
          const res = ref ? await downloadResponse(ref.path) : undefined;
          if (!res) {
            set.status = 404;
            return apiError("not_found", `没有可下载的交付物：${params.kind}`);
          }
          return res;
        }),
    )
    .get("/*", async ({ path, set }) => {
      const isApi = path === "/api" || path.startsWith("/api/");
      const res = options.webDistDir && !isApi ? await staticResponse(options.webDistDir, path) : undefined;
      if (res) return res;
      set.status = 404;
      return apiError("not_found", "路径不存在");
    });
}

export type App = ReturnType<typeof createApp>;
