// 打包入口的子命令分派（main.ts 只负责接线）：
//   无参数 → 启动服务端并打开浏览器
//   init   → 只初始化工作区（冒烟脚本和排障用）
//   mcp-server → 工具服务器（服务端把自身 + 这个子命令当成工具子进程）
//   其余子命令 → 原样交给 @de/cli（run-case / run-cases / check / fixtures / replay）

export interface AppDeps {
  /** 启动服务端并托管前端；直到进程退出才返回 */
  serve: () => Promise<void>;
  /** 以 stdio 方式运行工具服务器 */
  mcpServer: () => Promise<void>;
  /** 初始化工作区（释放嵌入资源、写默认 settings.json），返回工作区路径 */
  initWorkspace: () => Promise<string>;
  /** 转交给 CLI，返回它的退出码 */
  cli: (argv: string[]) => Promise<number>;
}

export async function runApp(argv: string[], deps: AppDeps): Promise<number> {
  const [command] = argv;
  if (command === "init") {
    console.log(`工作区已就绪：${await deps.initWorkspace()}`);
    return 0;
  }
  if (command === "mcp-server") {
    await deps.mcpServer();
    return 0;
  }
  if (command !== undefined) return deps.cli(argv);
  await deps.serve();
  return 0;
}
