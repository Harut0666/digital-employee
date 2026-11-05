// 危险操作的审计留痕（Spec 0004「审批的诚实边界」「危险操作」、T23）。
//
// 约定：
//   - 追加写 `<workspace>/audit.jsonl`，一行一条 JSON，字段顺序固定：
//     time（UTC ISO 8601）、role、action、target、result；
//   - 绝不写请求里的值：detail 只放服务端生成的说明，且统一过一遍脱敏；
//     即使说明里意外带出 Key（例如底层错误），也不会出现明文；
//   - 读路径跳过损坏行而不是整体失败（手改或写一半的行不影响其余记录）；
//   - 审计写失败不能让业务操作失败：调用方负责 try/catch。
import { appendFileSync, existsSync, mkdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";

/** 审计文件名（工作区根目录）。 */
export const AUDIT_FILE_NAME = "audit.jsonl";

/** 目前会写审计的操作（新操作在这里加一项，测试与文档同源）。 */
export const AUDIT_ACTIONS = [
  "reset-employees",
  "import-settings",
  "export-settings-with-keys",
  "set-sandbox-mode",
  "set-approval-policy",
  "simulate-crash",
] as const;
export type AuditAction = (typeof AUDIT_ACTIONS)[number];

/** 结果：前端取消不发请求，因此服务端只可能知道成功与失败。 */
export const AUDIT_RESULTS = ["success", "failure"] as const;
export type AuditResult = (typeof AUDIT_RESULTS)[number];

export interface AuditInput {
  time: Date;
  /** 操作角色（请求头 x-role；缺失时用设置里的 defaultRole） */
  role: string;
  action: AuditAction;
  /** 操作目标：文件、目录或权限字段名 */
  target: string;
  result: AuditResult;
  /** 可选说明；写盘前脱敏 */
  detail?: string;
}

/** 磁盘上的一行（已归一化、已脱敏）。 */
export interface AuditEntry {
  time: string;
  role: string;
  action: string;
  target: string;
  result: AuditResult;
  detail?: string;
}

/** 明文密钥的兜底打码规则：宁可多抹，不可漏写。 */
const SECRET_PATTERNS: readonly (readonly [RegExp, string])[] = [
  // {"apiKey":"sk-..."} 这类 JSON 片段
  [/"apiKey"\s*:\s*"[^"]*"/gi, '"apiKey":"***"'],
  // apiKey=sk-... 这类查询串 / 日志片段
  [/(apiKey=)[A-Za-z0-9._-]+/gi, "$1***"],
  // sk- 开头的密钥（DeepSeek 等）
  [/sk-[A-Za-z0-9._-]{4,}/g, "sk-***"],
  // Authorization: Bearer <token>
  [/(Bearer)\s+[A-Za-z0-9._~+/=-]+/gi, "$1 ***"],
];

/** 抹掉文本里的密钥形态（审计行与错误说明共用）。 */
export function redactAuditText(text: string): string {
  let out = text;
  for (const [pattern, replacement] of SECRET_PATTERNS) out = out.replace(pattern, replacement);
  return out;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isAuditResult(value: unknown): value is AuditResult {
  return typeof value === "string" && (AUDIT_RESULTS as readonly string[]).includes(value);
}

/** 纯函数：一条审计记录 → 一行 JSON 文本（末尾带换行）。字段顺序固定，便于人工比对。 */
export function formatAuditLine(input: AuditInput): string {
  const entry: AuditEntry = {
    time: input.time.toISOString(),
    role: redactAuditText(input.role),
    action: input.action,
    target: redactAuditText(input.target),
    result: input.result,
  };
  const detail = input.detail?.trim();
  if (detail) entry.detail = redactAuditText(detail);
  return `${JSON.stringify(entry)}\n`;
}

/** 纯函数：解析审计文本；坏 JSON / 缺字段 / 未知结果的行直接跳过。 */
export function parseAuditEntries(text: string): AuditEntry[] {
  const entries: AuditEntry[] = [];
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    if (line === "") continue;
    let value: unknown;
    try {
      value = JSON.parse(line);
    } catch {
      continue;
    }
    if (!isRecord(value)) continue;
    const { time, role, action, target, result, detail } = value;
    if (typeof time !== "string" || Number.isNaN(new Date(time).getTime())) continue;
    if (typeof role !== "string" || role === "") continue;
    if (typeof action !== "string" || action === "") continue;
    if (typeof target !== "string" || target === "") continue;
    if (!isAuditResult(result)) continue;
    const entry: AuditEntry = { time, role, action, target, result };
    if (typeof detail === "string") entry.detail = detail;
    entries.push(entry);
  }
  return entries;
}

/** 追加一条审计记录，返回写入的那一条。目录不存在时先建出来。 */
export function appendAuditEntry(workspace: string, input: AuditInput): AuditEntry {
  const file = join(workspace, AUDIT_FILE_NAME);
  mkdirSync(dirname(file), { recursive: true });
  const line = formatAuditLine(input);
  appendFileSync(file, line, "utf8");
  return JSON.parse(line.trimEnd()) as AuditEntry;
}

/** 读全部审计记录（按写入顺序）。文件不存在返回空数组；损坏行跳过。 */
export function readAuditEntries(workspace: string): AuditEntry[] {
  const file = join(workspace, AUDIT_FILE_NAME);
  if (!existsSync(file)) return [];
  try {
    return parseAuditEntries(readFileSync(file, "utf8"));
  } catch {
    return [];
  }
}
