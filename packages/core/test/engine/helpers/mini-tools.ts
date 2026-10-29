// 测试内自建的小型 MCP 服务器：几个假工具，行为可由测试控制。
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import { z } from "zod";
import { createMcpToolClient } from "../../../src/mcp";
import type { ToolClient } from "../../../src/contract";

export type ToolHandler = (args: Record<string, unknown>) => unknown | Promise<unknown>;

/** 抛出它会让工具返回 isError（structuredContent 里可带 retryable）。 */
export class ToolFailure extends Error {
  constructor(
    message: string,
    readonly retryable = true,
  ) {
    super(message);
  }
}

export interface MiniTools {
  client: ToolClient;
  /** 每次调用的记录（工具名 + 入参） */
  calls: { tool: string; args: Record<string, unknown> }[];
  /** 覆盖某个工具的行为；传 undefined 恢复默认 */
  setHandler(tool: string, handler: ToolHandler | undefined): void;
  /** 让某个工具连续失败 n 次（n=Infinity 为一直失败） */
  failTimes(tool: string, n: number, retryable?: boolean): void;
  callCount(tool: string): number;
}

const TOOL_NAMES = ["validate_submission", "load_sales", "compute_metrics", "render_report_docx", "web_search"] as const;

function defaults(tool: string, args: Record<string, unknown>): unknown {
  switch (tool) {
    case "validate_submission":
      if (args.sales !== true) {
        return { ok: false, pause: { code: "missing_sales", message: "缺少销售明细，请上传销售明细后再提交。" } };
      }
      return { ok: true };
    case "load_sales":
      return { rows: Number(args.n ?? 1) };
    case "compute_metrics":
      return { revenueCents: 100_00, source: args.data };
    case "render_report_docx": {
      // 幂等：同一个键只生成一个文件
      const docxPath = `${String(args.outDir)}/deliverables-src/${String(args.idempotencyKey ?? "report")}.docx`;
      mkdirSync(dirname(docxPath), { recursive: true });
      writeFileSync(docxPath, `docx:${JSON.stringify(args.report)}`);
      return { docxPath, report: args.report };
    }
    default:
      throw new ToolFailure("未开放");
  }
}

export async function createMiniTools(): Promise<MiniTools> {
  const calls: MiniTools["calls"] = [];
  const handlers = new Map<string, ToolHandler>();
  const failures = new Map<string, { remaining: number; retryable: boolean }>();

  const server = new McpServer({ name: "mini-tools", version: "0.0.0" });
  for (const name of TOOL_NAMES) {
    server.registerTool(
      name,
      { description: `测试工具 ${name}`, inputSchema: z.object({}).passthrough() },
      async (args: Record<string, unknown>) => {
        calls.push({ tool: name, args });
        try {
          const failure = failures.get(name);
          if (failure && failure.remaining > 0) {
            failure.remaining -= 1;
            throw new ToolFailure(`${name} 模拟故障`, failure.retryable);
          }
          const data = await (handlers.get(name) ?? ((a) => defaults(name, a)))(args);
          return { content: [{ type: "text" as const, text: JSON.stringify(data) }], structuredContent: data as Record<string, unknown> };
        } catch (error) {
          const retryable = error instanceof ToolFailure ? error.retryable : true;
          return {
            isError: true,
            content: [{ type: "text" as const, text: (error as Error).message }],
            structuredContent: { retryable },
          };
        }
      },
    );
  }

  const [clientSide, serverSide] = InMemoryTransport.createLinkedPair();
  await server.connect(serverSide);
  const client = await createMcpToolClient(clientSide);

  return {
    client,
    calls,
    setHandler: (tool, handler) => {
      if (handler) handlers.set(tool, handler);
      else handlers.delete(tool);
    },
    failTimes: (tool, n, retryable = true) => failures.set(tool, { remaining: n, retryable }),
    callCount: (tool) => calls.filter((c) => c.tool === tool).length,
  };
}
