// 员工配置页的接口调用（T11）。
//
// 配置路由带通配文件名（PUT /api/employees/:id/config/*），Eden 的路径参数类型在这里不好表达，
// 所以配置相关请求直接用 fetch；返回值与错误体都按 @de/core/contract 的 config-api 类型解包。

import type {
  ConfigDiff,
  ConfigVersion,
  EmployeeConfigResponse,
  RestoreConfigResponse,
  SaveConfigRequest,
  SaveConfigResponse,
} from "@de/core/contract";

/** 配置接口的业务错误：message 是概要，errors 是逐条校验错误。 */
export class ConfigRequestError extends Error {
  readonly errors: string[];

  constructor(message: string, errors: string[] = []) {
    super(message);
    this.name = "ConfigRequestError";
    this.errors = errors;
  }
}

function configUrl(origin: string, id: string, rest = ""): string {
  const base = `${origin}/api/employees/${encodeURIComponent(id)}/config`;
  if (rest === "") return base;
  return `${base}/${rest.split("/").map(encodeURIComponent).join("/")}`;
}

async function requestJson<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    ...init,
    headers: { "content-type": "application/json", ...((init?.headers as Record<string, string> | undefined) ?? {}) },
  });
  const body: unknown = await res.json().catch(() => undefined);
  if (!res.ok) {
    const record = typeof body === "object" && body !== null ? (body as Record<string, unknown>) : {};
    const message = typeof record.message === "string" ? record.message : `请求失败（${res.status}）`;
    const errors = Array.isArray(record.errors) ? (record.errors as string[]) : [];
    throw new ConfigRequestError(message, errors);
  }
  return body as T;
}

export interface ConfigApi {
  getEmployeeConfig(id: string): Promise<EmployeeConfigResponse>;
  saveEmployeeConfig(id: string, path: string, input: SaveConfigRequest): Promise<SaveConfigResponse>;
  listConfigVersions(id: string): Promise<ConfigVersion[]>;
  diffConfigVersion(id: string, versionId: string): Promise<ConfigDiff>;
  restoreConfigVersion(id: string, versionId: string): Promise<RestoreConfigResponse>;
}

export function createConfigApi(origin: string = window.location.origin): ConfigApi {
  return {
    getEmployeeConfig: (id) => requestJson<EmployeeConfigResponse>(configUrl(origin, id)),
    saveEmployeeConfig: (id, path, input) =>
      requestJson<SaveConfigResponse>(configUrl(origin, id, path), { method: "PUT", body: JSON.stringify(input) }),
    listConfigVersions: (id) => requestJson<ConfigVersion[]>(`${configUrl(origin, id)}/versions`),
    diffConfigVersion: (id, versionId) => requestJson<ConfigDiff>(`${configUrl(origin, id)}/versions/${encodeURIComponent(versionId)}/diff`),
    restoreConfigVersion: (id, versionId) =>
      requestJson<RestoreConfigResponse>(`${configUrl(origin, id)}/versions/${encodeURIComponent(versionId)}/restore`, { method: "POST" }),
  };
}
