// T2 实现：MCP 工具服务器。
export { createToolsServer, type ToolsServer, type ToolsServerOptions } from "./server";
export type { ResvgWasm } from "./charts";

import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { createToolsServer, type ToolsServerOptions } from "./server";

/**
 * 以 stdio 方式运行工具服务器（exe 的 mcp-server 子命令）。
 * resvg wasm 由入口注入：exe/开发入口用 Bun 文件导入，桌面版读随包拷贝的 wasm。
 */
export async function runStdioServer(options: ToolsServerOptions = {}): Promise<void> {
  const { server } = createToolsServer(options);
  await server.connect(new StdioServerTransport());
}
