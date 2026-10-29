// MCP 工具服务器：注册场景 A 的工具，并提供故障注入。
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type {
  AdminSetFaultsInput,
  ComputeMetricsInput,
  FaultConfig,
  RenderReportInput,
  ToolName,
  ValidateSubmissionInput,
} from "@de/core/contract";
import * as S from "./schemas";
import { computeMetrics } from "./compute";
import { parseMinutesFile } from "./minutes";
import { renderReportDocx, type RenderOptions } from "./render";
import { loadSales } from "./sales/dataset";
import { validateSubmission } from "./validate";

export interface ToolsServerOptions extends RenderOptions {
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
    <A>(name: ToolName, run: (args: A) => Promise<object>) =>
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
        return { content: [{ type: "text", text: JSON.stringify(out) }], structuredContent: out as Record<string, unknown> };
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
    wrap("validate_submission", (a: ValidateSubmissionInput) => validateSubmission(a)) as never,
  );

  server.registerTool(
    "load_sales",
    {
      description: "读取销售明细（xlsx 或 csv），清洗后写成 JSON 数据集；文件名取内容哈希，已存在则直接返回。",
      inputSchema: S.loadSalesIn,
      outputSchema: S.loadSalesOut,
    },
    wrap("load_sales", (a: { salesPath: string; outDir: string }) => loadSales(a.salesPath, a.outDir)) as never,
  );

  server.registerTool(
    "compute_metrics",
    {
      description: "按周报口径计算指标与异常，输出 WeeklyFacts（金额为整数分，比例为小数）。",
      inputSchema: S.computeMetricsIn,
      outputSchema: S.computeMetricsOut,
    },
    wrap("compute_metrics", async (a: ComputeMetricsInput) => computeMetrics(a)) as never,
  );

  server.registerTool(
    "parse_minutes",
    {
      description: "解析周会纪要：从正文标题识别周次，切分章节与段落，段落 ID 形如 W44-p07。",
      inputSchema: S.parseMinutesIn,
      outputSchema: S.parseMinutesOut,
    },
    wrap("parse_minutes", async (a: { paths: string[] }) => ({
      minutes: await Promise.all(a.paths.map((p) => parseMinutesFile(p))),
    })) as never,
  );

  server.registerTool(
    "render_report_docx",
    {
      description: "生成 Word 周报（标题、章节、核心指标表、至少 2 张图）；同一幂等键的文件已存在则直接返回。",
      inputSchema: S.renderReportIn,
      outputSchema: S.renderReportOut,
    },
    wrap("render_report_docx", (a: RenderReportInput) =>
      renderReportDocx(a, { chartFonts: options.chartFonts, docxFont: options.docxFont }),
    ) as never,
  );

  server.registerTool(
    "web_search",
    { description: "联网搜索。不开放给任何员工，仅用于演示越权拦截。", inputSchema: S.webSearchIn },
    (async () => errorResult("web_search 未开放：该工具不对任何数字员工开放。")) as never,
  );

  // 管理工具：设置页经服务端下发故障开关。不放进任何员工的白名单，
  // 判断步骤若尝试调用会在 beforeToolCall 被拦截并留 tool_denied 事件。
  server.registerTool(
    "admin_set_faults",
    {
      description: "设置页下发的工具故障开关（管理工具，不开放给任何员工，只有服务端可以调用）。",
      inputSchema: S.adminSetFaultsIn,
      outputSchema: S.adminSetFaultsOut,
    },
    (async (args: AdminSetFaultsInput) => {
      // 管理工具自身不允许被注入故障，否则设置页就再也关不掉故障了
      const tools = { ...args.tools } as FaultConfig["tools"];
      delete tools.admin_set_faults;
      faults = { tools };
      const out = { ok: true, tools };
      return { content: [{ type: "text" as const, text: JSON.stringify(out) }], structuredContent: out as Record<string, unknown> };
    }) as never,
  );

  return {
    server,
    setFaults(next) {
      faults = next;
    },
  };
}
