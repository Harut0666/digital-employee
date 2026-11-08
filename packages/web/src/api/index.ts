import { createEdenApi } from "./eden";
import { createMockApi } from "./mock";
import type { MockWebApi, UiRole, WebApi } from "./types";
import { createMockWorkspaceApi, createWorkspaceApi, type WorkspaceApi } from "./workspaces";

/** VITE_MOCK=1 时用内存模拟，否则走 Eden 连后端。 */
export function createApi(getRole: () => UiRole): WebApi {
  return import.meta.env.VITE_MOCK === "1" ? createMockApi(getRole) : createEdenApi(getRole);
}

/**
 * 工作区与记录：真实模式是 Go 后端（读取部分在 internal/server/workspace.go）；
 * 模拟模式用 api 里同一份会话和任务，和界面一致。
 */
export function createWorkspaces(api: WebApi): WorkspaceApi {
  if (import.meta.env.VITE_MOCK === "1") return createMockWorkspaceApi(api as unknown as MockWebApi);
  return createWorkspaceApi();
}

export const IS_MOCK = import.meta.env.VITE_MOCK === "1";
export * from "./types";
