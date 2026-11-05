// 路由模块的共享依赖。
//
// 本目录的每个路由模块由主 agent 建好空壳并挂到 createApp 上，各 ticket 只填自己的文件，
// 这样并行开发时不需要改同一个入口文件。新增依赖时改这里与 main.ts，并在 ticket 里说明。
import type {
  FaultHooks,
  ModelConnection,
  ModelConnectionTest,
  ModelSettings,
  TaskEngine,
  ToolClient,
} from "@de/core/contract";

/** “测试连接”的提供者：生产用 pi-ai 发一条极短请求，测试注入假实现。 */
export type ConnectionProbe = (connection: ModelConnection, model: string) => Promise<ModelConnectionTest>;

/** 打开工作区文件夹：Windows 用 explorer，测试注入假实现。 */
export type FolderOpener = (dir: string) => void | Promise<void>;

/** 用系统编辑器打开一个文件（设置页“打开设置文件”）；测试注入假实现。 */
export type FileOpener = (path: string) => void | Promise<void>;

export interface RouteDeps {
  engine: TaskEngine;
  employeesDir: string;
  workspaceDir: string;
  /** 工具服务器客户端：T10 用它把故障开关下发给工具子进程（admin_set_faults）。 */
  toolClient?: ToolClient;
  /** 设置文件保存后重建模型提供者，由 main.ts 提供；T10 使用。 */
  onSettingsSaved?: (settings: ModelSettings) => void | Promise<void>;
  /** 演示开关的运行时可变对象，与引擎共享同一个引用（T10）。 */
  faults?: FaultHooks;
  /** 默认员工配置目录（“恢复默认员工配置”的来源），由 main.ts 提供。 */
  defaultEmployeesDir?: string;
  /** 测试连接的可注入实现；不传时用 pi-ai 发真实请求。 */
  probeConnection?: ConnectionProbe;
  /** 打开文件夹的可注入实现；不传时按平台调用 explorer/open/xdg-open。 */
  openFolder?: FolderOpener;
  /** 用系统编辑器打开文件的可注入实现；不传时按平台调用 notepad/open/xdg-open。 */
  openFile?: FileOpener;
  /** 读取环境变量（DEEPSEEK_API_KEY），测试注入；默认 process.env。 */
  env?: Record<string, string | undefined>;
  /** 测试注入时钟（备份目录时间戳）。 */
  now?: () => Date;
}

/** 存根路由的响应：接口已挂载但还没有实现。 */
export function notImplemented(feature: string) {
  return { ok: false as const, error: "not_implemented" as const, message: `${feature} 尚未实现` };
}
