// 判定要点（`expect.checks`）的解析与核对，格式见 samples/cases/README.md。
//
// 输入是一次运行的产物目录：result.json（用例结果）、events.jsonl（任务事件，含事实产物路径与暂停历史）、
// 交付的 Word（用 mammoth 读正文与表格）。逐项给出通过或不通过及原因，不抛错——
// 写错的路径属于“这一项没核对通过”，不是程序异常。
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import mammoth from "mammoth";
import { SLOT_FORMATS, type PauseCode, type TaskState, type WeeklyFacts } from "@de/core/contract";

export type CheckKind = "numbers" | "mustContain" | "mustContainAny" | "mustNotContain" | "anomalies" | "pause";

/** 数字核对项。`path` 指向事实（facts:）或周报正文的表格单元（table:）。 */
export interface NumberCheck {
  path: string;
  /** 按展示口径填：金额为元、比例为百分数（-9.3 即 -9.3%）、计数为整数 */
  expected: number;
  /** 相对容差，缺省 0.005（±0.5%） */
  tolerance?: number;
}

export interface PauseCheck {
  code: PauseCode;
  /** message 里必须包含的片段 */
  messageContains?: string;
}

export interface CaseChecks {
  numbers?: NumberCheck[];
  mustContain?: string[];
  /** 每组是同一判定要点的一组等价说法，正文命中其中任意一处即通过 */
  mustContainAny?: string[][];
  mustNotContain?: string[];
  /** 事实里必须判出的异常 id，如 region:华东、store:HZ01 */
  anomalies?: string[];
  pause?: PauseCheck;
}

export interface CheckOutcome {
  kind: CheckKind;
  /** 数字项是 path，文本项是文本本身，异常项是异常 id，暂停项是暂停 code */
  target: string;
  passed: boolean;
  detail: string;
}

export interface DocxEvidence {
  path: string;
  /** 正文纯文本（表格单元按单元格边界拼接） */
  text: string;
  /** 按出现顺序解析出来的表格：[表][行][列] */
  tables: string[][][];
}

export interface PauseRecord {
  code: PauseCode;
  message: string;
}

export interface RunEvidence {
  resultDir: string;
  caseId: string;
  finalState: TaskState;
  pauseCode?: PauseCode;
  /** compute_metrics 的事实产物（没跑到该步骤时没有） */
  facts?: WeeklyFacts;
  /** 交付的 Word（暂停在准入、没有交付物时没有） */
  docx?: DocxEvidence;
  /** 这次运行出现过的暂停，按事件顺序 */
  pauses: PauseRecord[];
  /** 这次运行调用了模型几次（事件日志里 model_called 的条数） */
  modelCalls: number;
}

// ---- 格式校验 ----

/** 与 contract 的 `PauseCode` 保持一致；`satisfies` 保证这里不会写出契约里没有的 code。 */
const PAUSE_CODES = [
  "missing_sales",
  "missing_dimensions",
  "period_out_of_range",
  "period_incomplete",
  "minutes_missing",
  "minutes_week_mismatch",
  "invalid_input",
  "list_direction_mismatch",
  "insufficient_material",
  "tool_failed",
  "limit_exceeded",
  "qc_exhausted",
  "rework_needs_human",
  "awaiting_review",
] as const satisfies readonly PauseCode[];

const CHECK_KEYS = ["numbers", "mustContain", "mustContainAny", "mustNotContain", "anomalies", "pause"] as const;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function bad(where: string, problem: string): never {
  throw new Error(`${where}：${problem}`);
}

function nonEmptyStrings(value: unknown, where: string, field: string): string[] {
  if (!Array.isArray(value)) bad(where, `${field} 必须是数组`);
  return value.map((item, i) => {
    if (typeof item !== "string" || item.trim() === "") bad(where, `${field}[${i}] 必须是非空字符串`);
    return item;
  });
}

/** mustContainAny：[["说法 A", "说法 B"], …]，每组至少一个非空字符串。 */
function alternativeGroups(value: unknown, where: string, field: string): string[][] {
  if (!Array.isArray(value)) bad(where, `${field} 必须是数组，数组里每一项是一组等价说法`);
  return value.map((group, i) => {
    const at = `${field}[${i}]`;
    if (!Array.isArray(group)) bad(where, `${at} 必须是数组（同一判定要点的一组等价说法）`);
    if (group.length === 0) bad(where, `${at} 不能是空数组`);
    return nonEmptyStrings(group, where, at);
  });
}

function parseNumberCheck(raw: unknown, where: string, index: number): NumberCheck {
  const at = `${where}.numbers[${index}]`;
  if (!isRecord(raw)) bad(at, "必须是对象");
  const path = raw.path;
  if (typeof path !== "string") bad(at, "缺少 path");
  const [prefix, selector] = splitPath(path);
  if (prefix === "facts") {
    if (!selector) bad(at, `path 缺少事实路径：${JSON.stringify(path)}`);
  } else if (prefix === "table") {
    const slash = selector.indexOf("/");
    if (slash <= 0 || slash === selector.length - 1) bad(at, `table: 的 path 要写成 table:<行>/<列>：${JSON.stringify(path)}`);
  } else {
    bad(at, `path 必须以 facts: 或 table: 开头：${JSON.stringify(path)}`);
  }
  if (typeof raw.expected !== "number" || !Number.isFinite(raw.expected)) bad(at, "expected 必须是数字");
  if (raw.tolerance !== undefined && (typeof raw.tolerance !== "number" || !Number.isFinite(raw.tolerance) || raw.tolerance <= 0)) {
    bad(at, "tolerance 必须是正数");
  }
  return { path, expected: raw.expected, ...(raw.tolerance === undefined ? {} : { tolerance: raw.tolerance }) };
}

function parsePauseCheck(raw: unknown, where: string): PauseCheck {
  const at = `${where}.pause`;
  if (!isRecord(raw)) bad(at, "必须是对象");
  const { code, messageContains } = raw;
  if (typeof code !== "string" || !(PAUSE_CODES as readonly string[]).includes(code)) {
    bad(at, `code 必须是 ${PAUSE_CODES.join(" / ")} 之一：${JSON.stringify(code)}`);
  }
  if (messageContains !== undefined && (typeof messageContains !== "string" || messageContains === "")) {
    bad(at, "messageContains 必须是非空字符串");
  }
  return { code: code as PauseCode, ...(messageContains === undefined ? {} : { messageContains }) };
}

/** 校验并规范化 `expect.checks`；没有该段时返回空对象。格式错误直接抛错。 */
export function parseChecks(raw: unknown, where = "checks"): CaseChecks {
  if (raw === undefined || raw === null) return {};
  if (!isRecord(raw)) bad(where, "必须是对象");
  const unknown = Object.keys(raw).filter((key) => !(CHECK_KEYS as readonly string[]).includes(key));
  if (unknown.length > 0) bad(where, `有未知字段：${unknown.join("、")}（只支持 ${CHECK_KEYS.join(" / ")}）`);

  const checks: CaseChecks = {};
  if (raw.numbers !== undefined) {
    if (!Array.isArray(raw.numbers)) bad(where, "numbers 必须是数组");
    checks.numbers = raw.numbers.map((item, i) => parseNumberCheck(item, where, i));
  }
  if (raw.mustContain !== undefined) checks.mustContain = nonEmptyStrings(raw.mustContain, where, "mustContain");
  if (raw.mustContainAny !== undefined) checks.mustContainAny = alternativeGroups(raw.mustContainAny, where, "mustContainAny");
  if (raw.mustNotContain !== undefined) checks.mustNotContain = nonEmptyStrings(raw.mustNotContain, where, "mustNotContain");
  if (raw.anomalies !== undefined) checks.anomalies = nonEmptyStrings(raw.anomalies, where, "anomalies");
  if (raw.pause !== undefined) checks.pause = parsePauseCheck(raw.pause, where);
  return checks;
}

// ---- 读结果目录 ----

interface StoredEvent {
  type?: string;
  stepId?: string;
  outputPath?: string;
  pause?: { code?: string; message?: string };
}

interface StoredResult {
  caseId?: string;
  finalState?: TaskState;
  pauseCode?: PauseCode;
  deliverables?: { kind?: string; path?: string }[];
}

function readJsonFile(path: string): unknown {
  return JSON.parse(readFileSync(path, "utf8"));
}

function readEvents(path: string): StoredEvent[] {
  if (!existsSync(path)) return [];
  return readFileSync(path, "utf8")
    .split("\n")
    .filter((line) => line.trim() !== "")
    .map((line) => JSON.parse(line) as StoredEvent);
}

function looksLikeFacts(value: unknown): value is WeeklyFacts {
  return isRecord(value) && isRecord(value.company) && isRecord(value.regions) && isRecord(value.period);
}

/** 事实步骤的产物：事件日志里最后一个 compute_metrics 产物，且长得像事实。 */
function readFacts(events: StoredEvent[]): WeeklyFacts | undefined {
  for (let i = events.length - 1; i >= 0; i--) {
    const event = events[i]!;
    if (event.type !== "step_completed" || typeof event.outputPath !== "string") continue;
    if (!existsSync(event.outputPath)) continue;
    let value: unknown;
    try {
      value = readJsonFile(event.outputPath);
    } catch {
      continue;
    }
    if (looksLikeFacts(value)) return value;
  }
  return undefined;
}

const ENTITIES: [RegExp, string][] = [
  [/&nbsp;/g, " "],
  [/&lt;/g, "<"],
  [/&gt;/g, ">"],
  [/&quot;/g, '"'],
  [/&#39;/g, "'"],
  [/&amp;/g, "&"],
];

function htmlToText(html: string): string {
  let text = html.replace(/<[^>]+>/g, " ");
  for (const [pattern, replacement] of ENTITIES) text = text.replace(pattern, replacement);
  return text.replace(/\s+/g, " ").trim();
}

/** mammoth 的 HTML 转成 [表][行][列]：表格单元里可能有段落标签，去掉后按文本比较。 */
function parseTables(html: string): string[][][] {
  const tables: string[][][] = [];
  for (const table of html.matchAll(/<table[^>]*>([\s\S]*?)<\/table>/g)) {
    const rows: string[][] = [];
    for (const row of (table[1] ?? "").matchAll(/<tr[^>]*>([\s\S]*?)<\/tr>/g)) {
      const cells = [...(row[1] ?? "").matchAll(/<t[dh][^>]*>([\s\S]*?)<\/t[dh]>/g)].map((cell) => htmlToText(cell[1] ?? ""));
      rows.push(cells);
    }
    tables.push(rows);
  }
  return tables;
}

async function readDocx(path: string): Promise<DocxEvidence> {
  const [raw, html] = await Promise.all([mammoth.extractRawText({ path }), mammoth.convertToHtml({ path })]);
  return { path, text: raw.value, tables: parseTables(html.value) };
}

/** 读一次运行的产物目录。缺 result.json 说明目录不对，直接报错。 */
export async function readRunEvidence(resultDir: string): Promise<RunEvidence> {
  const resultPath = join(resultDir, "result.json");
  if (!existsSync(resultPath)) throw new Error(`结果目录里没有 result.json：${resultDir}`);
  const result = readJsonFile(resultPath) as StoredResult;
  const events = readEvents(join(resultDir, "events.jsonl"));
  const pauses: PauseRecord[] = [];
  for (const event of events) {
    if (event.type !== "state_changed" || !event.pause?.code) continue;
    pauses.push({ code: event.pause.code as PauseCode, message: event.pause.message ?? "" });
  }
  const docxRef = [...(result.deliverables ?? [])].reverse().find((d) => d.kind === "docx");
  const docx = docxRef?.path && existsSync(docxRef.path) ? await readDocx(docxRef.path) : undefined;
  return {
    resultDir,
    caseId: result.caseId ?? "",
    finalState: (result.finalState ?? "draft") as TaskState,
    ...(result.pauseCode === undefined ? {} : { pauseCode: result.pauseCode }),
    facts: readFacts(events),
    ...(docx === undefined ? {} : { docx }),
    pauses,
    modelCalls: events.filter((event) => event.type === "model_called").length,
  };
}

// ---- 核对 ----

const DEFAULT_TOLERANCE = 0.005;

type Resolved = { value: number } | { error: string };

const fmtNum = (value: number): string => String(Math.round(value * 100) / 100);

const squash = (text: string): string => text.replace(/\s+/g, "");

const normalizeCell = (text: string): string => text.replace(/\s+/g, "").trim();

function splitPath(path: string): [string, string] {
  const colon = path.indexOf(":");
  if (colon < 0) return ["", path];
  return [path.slice(0, colon), path.slice(colon + 1)];
}

/** 事实字段名决定展示口径（与槽位渲染一致）：分 → 元，比例 → 百分数。 */
function toDisplay(value: number, leaf: string): number | undefined {
  switch (SLOT_FORMATS[leaf]) {
    case "yuan":
      return value / 100;
    case "percent1":
      return value * 100;
    case "count":
      return value;
    default:
      return undefined;
  }
}

function resolveFactPath(facts: WeeklyFacts | undefined, dotted: string): Resolved {
  if (!facts) return { error: "这次运行没有事实数据（没跑到计算步骤）" };
  const segments = dotted.split(".");
  let current: unknown = facts;
  for (const [i, segment] of segments.entries()) {
    const trail = segments.slice(0, i + 1).join(".");
    if (Array.isArray(current)) {
      const found = current.find(
        (item) =>
          isRecord(item) &&
          // 数组的业务键与槽位渲染一致（qc/slots.ts 的 ARRAY_KEYS）；退货明细按 store/product 取
          [item.code, item.key, item.id, item.name, item.category, item.store, item.product].some((key) => key === segment),
      );
      if (found === undefined) return { error: `事实里没有 ${trail}` };
      current = found;
    } else if (isRecord(current)) {
      if (!(segment in current)) return { error: `事实里没有 ${trail}` };
      current = current[segment];
    } else {
      return { error: `事实里没有 ${trail}` };
    }
  }
  if (typeof current !== "number" || !Number.isFinite(current)) return { error: `事实 ${dotted} 不是数字` };
  const leaf = segments.at(-1)!;
  const display = toDisplay(current, leaf);
  if (display === undefined) return { error: `不知道字段 ${leaf} 的展示口径（不在 SLOT_FORMATS 里），请在 path 上换一个字段` };
  return { value: display };
}

/** 单元格文本转数字：去掉千分位与百分号，解析不了就返回 undefined。 */
function parseCellNumber(text: string): number | undefined {
  const cleaned = text.replace(/[\s,，]/g, "").replace(/[%％]$/, "");
  if (!/^[+-]?\d+(?:\.\d+)?$/.test(cleaned)) return undefined;
  const value = Number(cleaned);
  return Number.isFinite(value) ? value : undefined;
}

function resolveTableCell(docx: DocxEvidence | undefined, selector: string): Resolved {
  if (!docx) return { error: `没有交付的 Word（docx），无法读取表格单元 ${selector}` };
  const slash = selector.indexOf("/");
  const rowLabel = selector.slice(0, slash).trim();
  const columnLabel = selector.slice(slash + 1).trim();
  const reasons: string[] = [];
  for (const [index, table] of docx.tables.entries()) {
    const header = table[0] ?? [];
    const column = header.findIndex((cell) => normalizeCell(cell) === normalizeCell(columnLabel));
    if (column < 0) {
      reasons.push(`第 ${index + 1} 个表没有「${columnLabel}」列`);
      continue;
    }
    const rowIndex = table.findIndex((cells, i) => i > 0 && normalizeCell(cells[0] ?? "") === normalizeCell(rowLabel));
    if (rowIndex < 0) {
      reasons.push(`第 ${index + 1} 个表没有「${rowLabel}」行`);
      continue;
    }
    const cell = table[rowIndex]?.[column] ?? "";
    const number = parseCellNumber(cell);
    if (number === undefined) return { error: `表格单元「${rowLabel} / ${columnLabel}」不是数字：${cell || "空"}` };
    return { value: number };
  }
  if (docx.tables.length === 0) return { error: `周报里没有表格，无法读取表格单元 ${selector}` };
  return { error: `周报里找不到表格单元「${selector}」：${reasons.join("；")}` };
}

function resolveNumber(evidence: RunEvidence, path: string): Resolved {
  const [prefix, selector] = splitPath(path);
  if (prefix === "facts") return resolveFactPath(evidence.facts, selector);
  if (prefix === "table") return resolveTableCell(evidence.docx, selector);
  return { error: `不认识的 path 前缀：${JSON.stringify(prefix)}` };
}

function checkNumber(evidence: RunEvidence, check: NumberCheck): CheckOutcome {
  const resolved = resolveNumber(evidence, check.path);
  if ("error" in resolved) return { kind: "numbers", target: check.path, passed: false, detail: resolved.error };
  const tolerance = check.tolerance ?? DEFAULT_TOLERANCE;
  const diff = Math.abs(resolved.value - check.expected);
  const allowed = Math.abs(check.expected) * tolerance;
  if (diff <= allowed + 1e-9) {
    return { kind: "numbers", target: check.path, passed: true, detail: `实际 ${fmtNum(resolved.value)}（期望 ${fmtNum(check.expected)}）` };
  }
  const relative = check.expected === 0 ? "偏差无法按比例衡量" : `偏差 ${((diff / Math.abs(check.expected)) * 100).toFixed(2)}%`;
  return {
    kind: "numbers",
    target: check.path,
    passed: false,
    detail: `实际 ${fmtNum(resolved.value)}，期望 ${fmtNum(check.expected)}（${relative}，容差 ${(tolerance * 100).toFixed(1)}%）`,
  };
}

function checkMustContain(evidence: RunEvidence, text: string): CheckOutcome {
  if (!evidence.docx) return { kind: "mustContain", target: text, passed: false, detail: "没有交付的 Word（docx），无法核对正文" };
  const hit = squash(evidence.docx.text).includes(squash(text));
  return { kind: "mustContain", target: text, passed: hit, detail: hit ? "正文里有" : `正文里没有「${text}」` };
}

function checkMustContainAny(evidence: RunEvidence, alternatives: string[]): CheckOutcome {
  const target = alternatives.join(" / ");
  if (!evidence.docx) return { kind: "mustContainAny", target, passed: false, detail: "没有交付的 Word（docx），无法核对正文" };
  const body = squash(evidence.docx.text);
  const hit = alternatives.find((alternative) => body.includes(squash(alternative)));
  return hit === undefined
    ? { kind: "mustContainAny", target, passed: false, detail: `正文里没有以下任一说法：「${alternatives.join("」「")}」` }
    : { kind: "mustContainAny", target, passed: true, detail: `正文里有「${hit}」` };
}

function checkMustNotContain(evidence: RunEvidence, text: string): CheckOutcome {
  // 没有交付 Word 就没有正文，也就没有违规文本：这一项按通过
  if (!evidence.docx) return { kind: "mustNotContain", target: text, passed: true, detail: "没有交付的 Word，视为正文里没有" };
  const hit = squash(evidence.docx.text).includes(squash(text));
  return { kind: "mustNotContain", target: text, passed: !hit, detail: hit ? `正文里出现了「${text}」` : "正文里没有" };
}

function checkAnomaly(evidence: RunEvidence, id: string): CheckOutcome {
  if (!evidence.facts) return { kind: "anomalies", target: id, passed: false, detail: "这次运行没有事实数据（没跑到计算步骤）" };
  const ids = evidence.facts.anomalies.map((anomaly) => anomaly.id);
  return ids.includes(id)
    ? { kind: "anomalies", target: id, passed: true, detail: "已判出" }
    : { kind: "anomalies", target: id, passed: false, detail: `没有判出 ${id}（事实里的异常：${ids.join("、") || "无"}）` };
}

function checkPause(evidence: RunEvidence, pause: PauseCheck): CheckOutcome {
  const found = [...evidence.pauses].reverse().find((record) => record.code === pause.code);
  if (!found) {
    const seen = evidence.pauses.map((record) => record.code);
    return {
      kind: "pause",
      target: pause.code,
      passed: false,
      detail: `这次运行没有 ${pause.code} 暂停（出现过：${seen.join("、") || "无"}）`,
    };
  }
  if (pause.messageContains !== undefined && !squash(found.message).includes(squash(pause.messageContains))) {
    return {
      kind: "pause",
      target: pause.code,
      passed: false,
      detail: `${pause.code} 的提示里没有「${pause.messageContains}」，实际提示：${found.message}`,
    };
  }
  return { kind: "pause", target: pause.code, passed: true, detail: `出现过 ${pause.code} 暂停` };
}

/** 逐项核对，顺序与 checks 里各类的书写顺序一致。 */
export function evaluateChecks(evidence: RunEvidence, checks: CaseChecks): CheckOutcome[] {
  const outcomes: CheckOutcome[] = [];
  for (const check of checks.numbers ?? []) outcomes.push(checkNumber(evidence, check));
  for (const text of checks.mustContain ?? []) outcomes.push(checkMustContain(evidence, text));
  for (const alternatives of checks.mustContainAny ?? []) outcomes.push(checkMustContainAny(evidence, alternatives));
  for (const text of checks.mustNotContain ?? []) outcomes.push(checkMustNotContain(evidence, text));
  for (const id of checks.anomalies ?? []) outcomes.push(checkAnomaly(evidence, id));
  if (checks.pause) outcomes.push(checkPause(evidence, checks.pause));
  return outcomes;
}

export interface CheckReport {
  passed: boolean;
  outcomes: CheckOutcome[];
}

/** 读结果目录并核对；有未通过项时 passed=false。 */
export async function checkRun(resultDir: string, checks: CaseChecks): Promise<CheckReport> {
  const outcomes = evaluateChecks(await readRunEvidence(resultDir), checks);
  return { passed: outcomes.every((outcome) => outcome.passed), outcomes };
}

/** 从用例文件（或单独的 `{"checks": {...}}` 文件）里读 `checks`。 */
export function readChecksFromCaseFile(file: string): CaseChecks {
  if (!existsSync(file)) throw new Error(`找不到用例文件：${file}`);
  const raw = readJsonFile(file);
  if (!isRecord(raw)) throw new Error(`${file} 不是合法的用例文件：顶层必须是对象`);
  const expect = isRecord(raw.expect) ? raw.expect : undefined;
  const checks = raw.checks !== undefined ? raw.checks : expect?.checks;
  if (checks === undefined) throw new Error(`${file} 里没有 checks 段（expect.checks 或顶层 checks）`);
  return parseChecks(checks, `${file} 的 checks`);
}
