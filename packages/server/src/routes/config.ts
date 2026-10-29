// 员工配置页后端（T11）：配置读写、校验、版本与回滚（docs/design.md §4.1）。
//
// 配置文件是唯一的数据来源：GET 返回每个文件的原文与解析结果，PUT 先校验再写回；
// 每次保存前把当前配置复制到 <workspace>/config-versions/<员工>/<时间戳>-<哈希短码>/。
// 保存写盘后整体加载失败时回滚文件并删掉那个版本，保证“校验失败不写任何文件”。

import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { Elysia } from "elysia";
import {
  FORM_PARAMS,
  FORM_PARAM_NAMES,
  QC_TEMPLATES,
  createConfigVersion,
  describeConfigFiles,
  diffConfigVersion,
  isEmployeeId,
  listConfigVersions,
  loadEmployeeConfigSync,
  normalizeConfigPath,
  parseFrontdesk,
  prepareConfigText,
  readConfigFileText,
  removeConfigVersion,
  restoreConfigVersion,
  splitSkill,
  validateFormSchemaPurposes,
  type EmployeeConfigResponse,
  type RestoreConfigResponse,
  type SaveConfigResponse,
} from "@de/core";
import { apiError } from "../http";
import type { RouteDeps } from "./deps";

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export function createConfigRoutes(deps: RouteDeps) {
  const employeesRoot = resolve(deps.employeesDir);
  const workspaceDir = resolve(deps.workspaceDir);

  /** 员工目录：id 合法且目录存在才算数；与 /api/employees 的判定一致。 */
  function employeeDir(id: string): string | undefined {
    if (!isEmployeeId(id)) return undefined;
    const dir = join(employeesRoot, id);
    const workflow = join(dir, "workflow.yaml");
    try {
      readFileSync(workflow, "utf8");
      return dir;
    } catch {
      return undefined;
    }
  }

  /** 组装配置页需要的全部内容；配置加载失败时仍返回文件原文，方便用“查看源文件”修。 */
  function payload(id: string, dir: string): EmployeeConfigResponse {
    const files = describeConfigFiles(dir);
    const skillText = files.find((f) => f.path === "SKILL.md")?.content ?? "";
    const frontdeskText = files.find((f) => f.path === "frontdesk.md")?.content ?? "";

    let config: EmployeeConfigResponse["config"];
    let hash: string | undefined;
    let error: string | undefined;
    try {
      const loaded = loadEmployeeConfigSync(dir);
      hash = loaded.hash;
      config = {
        skill: loaded.skill,
        frontdesk: loaded.frontdesk,
        workflow: loaded.workflow,
        qc: loaded.qc,
        formSchema: loaded.formSchema,
        ...(loaded.formUi ? { formUi: loaded.formUi } : {}),
        prompts: loaded.prompts,
        outputSchemas: loaded.outputSchemas,
      };
    } catch (failure) {
      error = `员工配置无法加载：${errorMessage(failure)}`;
    }

    const formSchema =
      config?.formSchema ?? (files.find((f) => f.path === "form.schema.json")?.parsed as Record<string, unknown> | undefined) ?? {};
    const response: EmployeeConfigResponse = {
      id,
      name: config?.workflow.name ?? id,
      dir,
      files,
      views: {
        skill: splitSkill(skillText),
        frontdesk: parseFrontdesk(frontdeskText),
        formParams: FORM_PARAM_NAMES.map((name) => ({ name, label: FORM_PARAMS[name] })),
        qcTemplates: QC_TEMPLATES,
      },
      formSchemaErrors: validateFormSchemaPurposes(formSchema),
      versions: listConfigVersions(workspaceDir, id),
      ...(hash ? { hash } : {}),
      ...(config ? { config } : {}),
      ...(error ? { error } : {}),
    };
    return response;
  }

  /** 员工 id / 目录的公共校验：不合法 400，不存在 404。 */
  function lookup(id: string, set: { status?: number | string }) {
    if (!isEmployeeId(id)) {
      set.status = 400;
      return { ok: false as const, response: apiError("invalid_input", "员工 id 不合法：只允许小写字母、数字和连字符") };
    }
    const dir = employeeDir(id);
    if (!dir) {
      set.status = 404;
      return { ok: false as const, response: apiError("not_found", `员工不存在：${id}`) };
    }
    return { ok: true as const, dir };
  }

  return new Elysia({ prefix: "/api/employees" })
    .get("/:id/config", ({ params, set }) => {
      const found = lookup(params.id, set);
      if (!found.ok) return found.response;
      return payload(params.id, found.dir);
    })
    .get("/:id/config/versions", ({ params, set }) => {
      const found = lookup(params.id, set);
      if (!found.ok) return found.response;
      return listConfigVersions(workspaceDir, params.id);
    })
    .get("/:id/config/versions/:version/diff", ({ params, set }) => {
      const found = lookup(params.id, set);
      if (!found.ok) return found.response;
      const diff = diffConfigVersion({ workspaceDir, employeeId: params.id, employeeDir: found.dir, versionId: params.version });
      if (!diff) {
        set.status = 404;
        return apiError("not_found", `配置版本不存在：${params.version}`);
      }
      return diff;
    })
    .post("/:id/config/versions/:version/restore", ({ params, set }) => {
      const found = lookup(params.id, set);
      if (!found.ok) return found.response;
      const result = restoreConfigVersion({ workspaceDir, employeeId: params.id, employeeDir: found.dir, versionId: params.version });
      if (!result) {
        set.status = 404;
        return apiError("not_found", `配置版本不存在：${params.version}`);
      }
      let hash: string | undefined;
      try {
        hash = loadEmployeeConfigSync(found.dir).hash;
      } catch {
        // 回滚到的版本本身可能加载不了（例如人工改坏过）；文件已经复制回来，交给“查看源文件”处理
      }
      const response: RestoreConfigResponse = {
        ok: true,
        version: result.snapshot,
        restoredFrom: result.restoredFrom,
        versions: listConfigVersions(workspaceDir, params.id),
        ...(hash ? { hash } : {}),
      };
      return response;
    })
    .put("/:id/config/*", ({ params, body, set }) => {
      const found = lookup(params.id, set);
      if (!found.ok) return found.response;
      const dir = found.dir;
      const rawPath = (params as Record<string, unknown>)["*"];
      const relPath = normalizeConfigPath(rawPath);
      if (!relPath) {
        set.status = 400;
        return { ...apiError("invalid_input", `不允许写入配置文件：${String(rawPath)}`), errors: [`不允许写入配置文件：${String(rawPath)}`] };
      }

      const fields = (typeof body === "object" && body !== null ? body : {}) as Record<string, unknown>;
      const input = { content: fields.content as string | undefined, data: fields.data };
      const current = readConfigFileText(dir, relPath);
      const prepared = prepareConfigText(relPath, input, current?.text);
      if (!prepared.ok) {
        set.status = 400;
        return { ok: false as const, error: "invalid_input" as const, message: "配置校验未通过", errors: prepared.errors };
      }

      // 保存前能加载的配置，保存后也必须能加载：写盘后整体加载校验，失败就回滚文件并删掉快照
      let loadedBefore = true;
      try {
        loadEmployeeConfigSync(dir);
      } catch {
        loadedBefore = false;
      }

      const version = createConfigVersion({ workspaceDir, employeeId: params.id, employeeDir: dir });
      const target = join(dir, relPath);
      const backup = current?.text;
      mkdirSync(dirname(target), { recursive: true });
      writeFileSync(target, prepared.text);

      let hash: string | undefined;
      let loadError: string | undefined;
      try {
        hash = loadEmployeeConfigSync(dir).hash;
      } catch (failure) {
        loadError = errorMessage(failure);
      }
      if (loadedBefore && loadError) {
        if (backup === undefined) rmSync(target, { force: true });
        else writeFileSync(target, backup);
        removeConfigVersion(workspaceDir, params.id, version.id);
        set.status = 400;
        return { ok: false as const, error: "invalid_input" as const, message: "配置保存后无法加载，已回滚", errors: [loadError] };
      }

      const response: SaveConfigResponse = { ok: true, path: relPath, version, ...(hash ? { hash } : {}) };
      return response;
    });
}
