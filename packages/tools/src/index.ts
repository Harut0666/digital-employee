// T2 实现：MCP 工具服务器。
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { FaultConfig } from "@de/core/contract";

export interface ToolsServerOptions {
  /** 故障注入（演示与测试用），可在运行时通过 setFaults 修改 */
  faults?: FaultConfig;
}

export interface ToolsServer {
  server: McpServer;
  setFaults(faults: FaultConfig): void;
}

export function createToolsServer(_options?: ToolsServerOptions): ToolsServer {
  throw new Error("not implemented (T2)");
}

/** 以 stdio 方式运行工具服务器（exe 的 mcp-server 子命令） */
export async function runStdioServer(): Promise<void> {
  throw new Error("not implemented (T2)");
}
