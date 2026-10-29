// T1 实现：把 MCP 客户端包装成 ToolClient（重试由引擎负责，这里只做单次调用与错误归一化）。
import type { Transport } from "@modelcontextprotocol/sdk/shared/transport.js";
import type { ToolClient } from "../contract";

export async function createMcpToolClient(_transport: Transport): Promise<ToolClient> {
  throw new Error("not implemented (T1)");
}
