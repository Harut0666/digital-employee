// T1 实现：把 MCP 客户端包装成 ToolClient（重试由引擎负责，这里只做单次调用与错误归一化）。
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import type { Transport } from "@modelcontextprotocol/sdk/shared/transport.js";
import type { ToolCallResult, ToolClient, ToolDescriptor } from "../contract";

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function textOf(content: unknown): string {
  if (!Array.isArray(content)) return "";
  return content
    .map((part) => (part && typeof part === "object" && (part as { type?: string }).type === "text" ? String((part as { text?: unknown }).text ?? "") : ""))
    .filter(Boolean)
    .join("\n");
}

export async function createMcpToolClient(transport: Transport): Promise<ToolClient> {
  const client = new Client({ name: "de-core", version: "0.0.0" });
  await client.connect(transport);

  async function listTools(): Promise<ToolDescriptor[]> {
    const tools: ToolDescriptor[] = [];
    let cursor: string | undefined;
    do {
      const page = await client.listTools(cursor ? { cursor } : undefined);
      for (const tool of page.tools) {
        tools.push({ name: tool.name, description: tool.description ?? "", inputSchema: tool.inputSchema });
      }
      cursor = page.nextCursor;
    } while (cursor);
    return tools;
  }

  // 先列一次工具：SDK 据此缓存输出 Schema，后续调用会校验 structuredContent（故障注入“返回非法数据”靠它识别）。
  await listTools().catch(() => undefined);

  return {
    listTools,
    async call(name, args, options): Promise<ToolCallResult> {
      try {
        const result = await client.callTool(
          { name, arguments: (args ?? {}) as Record<string, unknown> },
          undefined,
          options?.signal ? { signal: options.signal } : undefined,
        );
        const structured = result.structuredContent as Record<string, unknown> | undefined;
        if (result.isError) {
          return {
            ok: false,
            error: textOf(result.content) || `工具 ${name} 返回错误`,
            retryable: structured?.retryable !== false,
          };
        }
        if (structured === undefined || structured === null) {
          return { ok: false, error: `工具 ${name} 的返回缺少 structuredContent`, retryable: true };
        }
        return { ok: true, data: structured };
      } catch (error) {
        return { ok: false, error: errorMessage(error), retryable: true };
      }
    },
    close: () => client.close(),
  };
}
