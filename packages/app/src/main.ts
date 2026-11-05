#!/usr/bin/env bun
// 单文件 exe 的入口（design.md §14）：
//   无参数      → 准备工作区、启动服务端、打开浏览器（默认 http://localhost:8787）
//   init        → 只准备工作区（冒烟脚本用）
//   mcp-server  → 工具服务器（服务端把自身 + 这个子命令当工具子进程）
//   其余子命令  → 转交 @de/cli（run-case / run-cases / check / fixtures / replay）
import { resolveWorkspace } from "@de/core";
import { runCli } from "@de/cli";
import { runStdioServer } from "@de/tools";
import resvgWasm from "@resvg/resvg-wasm/index_bg.wasm" with { type: "file" };
import { runApp } from "./dispatch";
import { EMBEDDED_BUNDLES } from "./embedded";
import { serveApp } from "./serve";
import { prepareWorkspace } from "./workspace";

const argv = process.argv.slice(2);

const code = await runApp(argv, {
  serve: async () => {
    await serveApp({ source: { kind: "archive", bundles: EMBEDDED_BUNDLES } });
    // 服务端会一直运行，直到收到 SIGINT/SIGTERM（startServer 里注册的收尾）
    await new Promise<never>(() => {});
  },
  mcpServer: async () => {
    // exe 里 wasm 由 Bun 的 `with { type: "file" }` 嵌入，这里拿到的是可读路径
    await runStdioServer({ resvgWasm });
  },
  initWorkspace: async () => {
    const prepared = await prepareWorkspace({
      workspaceDir: resolveWorkspace(),
      source: { kind: "archive", bundles: EMBEDDED_BUNDLES },
    });
    return prepared.workspaceDir;
  },
  cli: (args) => runCli(args),
});

// mcp-server 会一直运行直到连接关闭；其余命令跑完即退出
if (argv[0] !== "mcp-server") process.exit(code);
