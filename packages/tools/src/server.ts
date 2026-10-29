// MCP 工具服务器：注册场景 A 的工具，并提供故障注入。
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { FaultConfig, ToolName } from "@de/core/contract";
import * as S from "./schemas";
import { validateSubmission } from "./validate";

export interface ToolsServerOptions {
  /** 故障注入（演示与测试用），可在运行时通过 setFaults 修改 */
  faults?: FaultConfig;
  /** timeout 故障模式下，工具卡住多久后才报超时（毫秒，默认 30000；测试里调小） */
  faultTimeoutMs?: number;
}

export interface ToolsServer {
  server: McpServer;
  setFaults(faults: FaultConfig): void;
}

type ToolResult = {
  content: { type: "text"; text: string }[];
  structuredContent?: Record<string, unknown>;
  isError?: boolean;
};

const errorResult = (text: string): ToolResult => ({ content: [{ type: "text", text }], isError: true });

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const t = setTimeout(resolve, ms);
    signal?.addEventListener("abort", () => (clearTimeout(t), resolve()), { once: true });
  });
}

export function createToolsServer(options: ToolsServerOptions = {}): ToolsServer {
  let faults: FaultConfig = options.faults ?? { tools: {} };
  const server = new McpServer({ name: "de-tools", version: "0.1.0" });

  /** 统一包装：故障注入 + 异常转中文 isError + 结果放入 structuredContent */
  const wrap =
    <A>(name: ToolName, run: (args: A) => Promise<Record<string, unknown>>) =>
    async (args: A, extra: { signal?: AbortSignal }): Promise<ToolResult> => {
      const mode = faults.tools[name];
      if (mode === "error") return errorResult(`工具 ${name} 执行失败（故障注入：报错）`);
      if (mode === "timeout") {
        await sleep(options.faultTimeoutMs ?? 30_000, extra.signal);
        return errorResult(`工具 ${name} 调用超时（故障注入：超时）`);
      }
      if (mode === "invalid_output") {
        // 返回不符合 outputSchema 的数据；服务端校验会把它拦成 isError
        return { content: [{ type: "text", text: "<<非法输出>>" }], structuredContent: { invalid: true } };
      }
      try {
        const out = await run(args);
        return { content: [{ type: "text", text: JSON.stringify(out) }], structuredContent: out };
      } catch (e) {
        return errorResult(`工具 ${name} 执行失败：${e instanceof Error ? e.message : String(e)}`);
      }
    };

  server.registerTool(
    "validate_submission",
    {
      description: "表单与附件校验：按固定顺序只报最根本的一个原因（超出范围、周次不完整、缺销售明细、缺维表、纪要缺失或周次不符）。",
      inputSchema: S.validateSubmissionIn,
      outputSchema: S.validateSubmissionOut,
    },
    wrap("validate_submission", (a) => validateSubmission(a as never) as never) as never,
  );

  server.registerTool(
    "web_search",
    { description: "联网搜索。不开放给任何员工，仅用于演示越权拦截。", inputSchema: S.webSearchIn },
    (async () => errorResult("web_search 未开放：该工具不对任何数字员工开放。")) as never,
  );

  return {
    server,
    setFaults(next) {
      faults = next;
    },
  };
}
