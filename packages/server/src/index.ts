// T3 实现：Elysia 服务。导出 App 类型供前端 Eden 使用。
import { readdir } from "node:fs/promises";
import { join } from "node:path";
import { Elysia } from "elysia";
import { loadEmployeeConfig, type EmployeeConfig, type TaskEngine } from "@de/core";
import { apiError, filesOf, parseAfter, parseCommand, parseJsonField, roleOf, ROLE_REQUIRED, saveUploads, statusOfResult } from "./http";

export interface ServerOptions {
  engine: TaskEngine;
  employeesDir: string;
  /** 前端构建产物目录；不传则不托管静态资源 */
  webDistDir?: string;
  /** 员工配置加载器，默认用 @de/core 的 loadEmployeeConfig；测试时可替换 */
  loadEmployee?: (dir: string) => Promise<EmployeeConfig>;
}

export function createApp(options: ServerOptions) {
  const { engine, employeesDir } = options;
  const loadEmployee = options.loadEmployee ?? loadEmployeeConfig;

  async function listEmployees() {
    const entries = await readdir(employeesDir, { withFileTypes: true }).catch(() => []);
    const dirs = entries.filter((e) => e.isDirectory() && !e.name.startsWith(".") && e.name !== "node_modules");
    const result = [];
    for (const d of dirs.sort((a, b) => a.name.localeCompare(b.name))) {
      const cfg = await loadEmployee(join(employeesDir, d.name));
      result.push({
        id: cfg.id,
        name: cfg.workflow.name,
        description: cfg.workflow.description,
        formSchema: cfg.formSchema,
      });
    }
    return result;
  }

  return new Elysia()
    .onError(({ error, set }) => {
      set.status = 500;
      return { ok: false, error: "internal", message: error instanceof Error ? error.message : String(error) };
    })
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
        }),
    );
}

export type App = ReturnType<typeof createApp>;
