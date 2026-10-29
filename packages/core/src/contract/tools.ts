// 工具契约（docs/design.md §7）。工具服务器（@de/tools）实现，任务引擎（@de/core）通过 ToolClient 调用。
// 所有工具的结构化结果放在 MCP 的 structuredContent 中；失败时 isError=true，content[0].text 为中文错误说明。

import type { IsoWeek, WeeklyFacts } from "./facts";
import type { PauseReason } from "./states";

export const TOOL_NAMES = [
  "validate_submission",
  "load_sales",
  "compute_metrics",
  "detect_contradictions",
  "parse_minutes",
  "render_report_docx",
  "query_task",
  "web_search",
  "admin_set_faults",
] as const;
export type ToolName = (typeof TOOL_NAMES)[number];

export interface AttachmentRef {
  /** 原始文件名 */
  name: string;
  /** 工作区内的绝对路径 */
  path: string;
}

// ---- validate_submission ----
export interface ValidateSubmissionInput {
  period: IsoWeek;
  attachments: AttachmentRef[];
  /** 发起人已选择“无纪要继续” */
  allowMissingMinutes?: boolean;
  /** 发起人已选择“按已有数据继续” */
  allowPartialPeriod?: boolean;
}
export interface MinutesDetection {
  path: string;
  /** 从正文标题识别的周次，识别不出为 null */
  week: IsoWeek | null;
}
export interface ValidateSubmissionOutput {
  ok: boolean;
  /** ok=false 时给出最根本的一个原因（检查顺序见 design.md §10.1） */
  pause?: Omit<PauseReason, "options">;
  sales?: { path: string; kind: "xlsx" | "csv" };
  dimensions: { stores: boolean; products: boolean; targets: boolean };
  minutes: MinutesDetection[];
  coverage?: { from: string; to: string };
}

// ---- load_sales ----
export interface LoadSalesInput {
  salesPath: string;
  /** 工具把清洗后的数据集写入此目录，文件名由内容哈希决定（幂等） */
  outDir: string;
}
export interface LoadSalesOutput {
  datasetPath: string;
  rows: number;
  coverage: { from: string; to: string };
}

// ---- compute_metrics ----
export interface ComputeMetricsInput {
  datasetPath: string;
  period: IsoWeek;
  anomalyThreshold: number;
}
export type ComputeMetricsOutput = WeeklyFacts;

// ---- detect_contradictions ----
// 模型只负责把纪要里的可核验说法抽成固定格式（claims），是否构成矛盾由程序按阈值判定（docs/design.md §10.3）。

export type ClaimSubjectLevel = "company" | "region" | "store" | "category";
export interface ClaimSubject {
  level: ClaimSubjectLevel;
  /** 对象业务键：全公司写 company；区域写区域名；门店写门店编码；品类写品类名 */
  key: string;
}

export type ClaimDirection = "flat" | "up" | "down";

/** 纪要中的一条可核验说法，结构与 employees/weekly-report/schemas/match_causes.json 的 claims 一致。 */
export interface Claim {
  subject: ClaimSubject;
  /** 目前只核验实收环比 */
  metric: "wow";
  direction: ClaimDirection;
  /** 纪要给出的幅度，按小数（两成写 0.2，约七成写 0.7）；没给幅度时省略 */
  magnitude?: number;
  /** 说法出自哪一周的纪要 */
  sourceWeek: IsoWeek;
  /** 原文摘录，逐字取自所注周次纪要的连续片段 */
  quote: string;
}

/** 纪要矛盾的判定阈值，即 qc.yaml 的 contradiction 节。比例一律按小数（0.05 即 5%）。 */
export interface ContradictionThresholds {
  /** 说“持平”，但实际环比的绝对值大于此值，判为矛盾 */
  flatTolerance: number;
  /** 说的方向和实际相反，且实际环比的绝对值大于此值，判为矛盾 */
  directionMin: number;
  /** 给出的幅度与实际幅度相差超过此值（10 个百分点），判为矛盾 */
  magnitudeTolerance: number;
}

export interface DetectContradictionsInput {
  claims: Claim[];
  facts: WeeklyFacts;
  thresholds: ContradictionThresholds;
}

export type ContradictionRule = "flat" | "direction" | "magnitude";

export interface DetectedContradiction {
  /** 稳定 ID，如 "contradiction:region:华南"；质检按它核对草稿是否引用 */
  id: string;
  /** 说法针对的对象，name 是写进“异常与原因”章节时使用的展示名 */
  subject: ClaimSubject & { name: string };
  /** 纪要的说法（方向与幅度） */
  claim: { direction: ClaimDirection; magnitude?: number };
  /** 事实里的实际环比（未取整） */
  actualWow: number;
  rule: ContradictionRule;
  sourceWeek: IsoWeek;
  quote: string;
}

export interface DetectContradictionsOutput {
  contradictions: DetectedContradiction[];
  /** 对象在事实里找不到、或实际环比不可比而跳过的说法条数 */
  skippedCount: number;
}

// ---- parse_minutes ----
export interface ParseMinutesInput {
  paths: string[];
}
export interface MinutesParagraph {
  /** 稳定 ID，如 "W44-p07" */
  id: string;
  section?: string;
  text: string;
}
export interface ParseMinutesOutput {
  minutes: { path: string; week: IsoWeek | null; title: string; paragraphs: MinutesParagraph[] }[];
}

// ---- render_report_docx ----
export interface RenderReportInput {
  /** 已完成槽位渲染的报告内容 */
  report: RenderedReport;
  facts: WeeklyFacts;
  outDir: string;
  /** 幂等键：任务 + 步骤 + 输入哈希；同键文件已存在则直接返回 */
  idempotencyKey: string;
}
export interface RenderedReport {
  title: string;
  /** 段落；章节可能只有要点（如品类表现、下周关注），此时省略 paragraphs */
  sections: { id: string; heading: string; paragraphs?: string[]; bullets?: string[] }[];
}
export interface RenderReportOutput {
  docxPath: string;
  sha256: string;
  estimatedPages: number;
  chartCount: number;
}

// ---- 故障注入（design.md §14.1 演示与调试）----
export type FaultMode = "timeout" | "error" | "invalid_output";
export interface FaultConfig {
  /** 工具名 → 故障模式；未列出的工具正常 */
  tools: Partial<Record<ToolName, FaultMode>>;
}

// ---- admin_set_faults（管理工具，不开放给任何员工）----
// 由服务端在设置页下发到工具子进程；判断步骤若尝试调用，会被白名单拦截并留 tool_denied 事件。
export interface AdminSetFaultsInput {
  tools: Partial<Record<ToolName, FaultMode>>;
}
export interface AdminSetFaultsOutput {
  ok: boolean;
  tools: Partial<Record<ToolName, FaultMode>>;
}
