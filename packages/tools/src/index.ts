// T2 实现：MCP 工具服务器。
export { createToolsServer, type ToolsServer, type ToolsServerOptions } from "./server";

import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { createToolsServer } from "./server";

/** 以 stdio 方式运行工具服务器（exe 的 mcp-server 子命令） */
export async function runStdioServer(): Promise<void> {
  const { server } = createToolsServer();
  await server.connect(new StdioServerTransport());
}
