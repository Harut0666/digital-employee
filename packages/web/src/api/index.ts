import { createEdenApi } from "./eden";
import { createMockApi } from "./mock";
import type { UiRole, WebApi } from "./types";

/** VITE_MOCK=1 时用内存模拟，否则走 Eden 连后端。 */
export function createApi(getRole: () => UiRole): WebApi {
  return import.meta.env.VITE_MOCK === "1" ? createMockApi(getRole) : createEdenApi(getRole);
}

export const IS_MOCK = import.meta.env.VITE_MOCK === "1";
export * from "./types";
