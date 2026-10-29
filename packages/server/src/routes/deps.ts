// 路由模块的共享依赖。
//
// 本目录的每个路由模块由主 agent 建好空壳并挂到 createApp 上，各 ticket 只填自己的文件，
// 这样并行开发时不需要改同一个入口文件。新增依赖时改这里与 main.ts，并在 ticket 里说明。
import type { ModelSettings, TaskEngine, ToolClient } from "@de/core/contract";

export interface RouteDeps {
  engine: TaskEngine;
  employeesDir: string;
  workspaceDir: string;
  /** 工具服务器客户端：T10 用它把故障开关下发给工具子进程（admin_set_faults）。 */
  toolClient?: ToolClient;
  /** 设置文件保存后重建模型提供者，由 main.ts 提供；T10 使用。 */
  onSettingsSaved?: (settings: ModelSettings) => void | Promise<void>;
}

/** 存根路由的响应：接口已挂载但还没有实现。 */
export function notImplemented(feature: string) {
  return { ok: false as const, error: "not_implemented" as const, message: `${feature} 尚未实现` };
}
