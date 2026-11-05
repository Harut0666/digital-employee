// @de/app 的公共入口：共享启动装配（serveApp）、工作区准备与资源来源类型。
// 桌面版（@de/desktop）与 exe 用同一套装配，只是资源来源和运行参数不同。
export { DEFAULT_PORT, serveApp, type ServeOptions } from "./serve";
export { prepareWorkspace, type PreparedWorkspace } from "./workspace";
export {
  loadResourceFiles,
  type AppResourceSource,
  type EmbeddedBundlePaths,
  type WorkspaceResourceFiles,
} from "./resources";
