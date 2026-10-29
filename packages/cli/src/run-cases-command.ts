// de run-cases <glob> [--scripted|--real] [--out <dir>]：
// 逐条执行用例，核对交付物的判定要点，最后写 summary.md。退出码等于未通过的条数。
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { basename, dirname, isAbsolute, join, relative, resolve } from "node:path";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import {
  createMcpToolClient,
  createModelProvider,
  createScriptedModelProvider,
  createTaskEngine,
  loadSettings,
  resolveWorkspace,
  type ModelProvider,
  type ModelScript,
  type ToolClient,
} from "@de/core";
import { createToolsServer } from "@de/tools";
import { parseCase, runCase, type CaseDef } from "./case";
import { resolveEmployeesDir } from "./paths";
import { evaluateChecks, readRunEvidence, type CheckOutcome } from "./checks";

export type RunMode = "scripted" | "real";

/** 一条用例的运行结果：状态类期望与判定要点合在一起算“未通过项”。 */
export interface CaseRunOutcome {
  caseId: string;
  title: string;
  passed: boolean;
  /** 未通过项：步骤/状态期望 + 判定要点 */
  failures: string[];
  checks: CheckOutcome[];
  modelCalls: number;
  durationMs: number;
  finalState?: string;
  /** 整条用例没跑起来时的原因（用例文件无效、找不到剧本、引擎抛错） */
  error?: string;
}

export interface RunCasesArgs {
  /** 用例文件的 glob，由命令行自己解析（Bun.Glob） */
  pattern: string;
  mode: RunMode;
  outDir?: string;
}

const toPosix = (path: string): string => path.replace(/\\/g, "/");

/** 用例名（去掉 .json）：A-01、A-01-rework、A-02 …… */
const caseNameOf = (file: string): string => basename(file, ".json");

/**
 * 解析 glob 找用例文件。排序按“编号 → 名字长度 → 名字”，所以 A-01 在 A-01-rework 前面，
 * A-01-rework 又在 A-02 前面，跟文档里的顺序一致。
 */
export function findCaseFiles(pattern: string): string[] {
  // 没有通配符就当作一个具体文件（Bun.Glob 只匹配带通配符的模式）
  if (!/[*?[\]{}]/.test(pattern)) {
    const file = resolve(process.cwd(), pattern);
    return existsSync(file) ? [file] : [];
  }
  const files = [...new Bun.Glob(toPosix(pattern)).scanSync({ cwd: process.cwd() })].map((path) => resolve(process.cwd(), path));
  return files
    .filter((file) => file.endsWith(".json"))
    .sort((a, b) => {
      const name = (file: string) => caseNameOf(file);
      const number = (file: string) => {
        const match = /(\d+)/.exec(name(file));
        return match ? Number(match[1]) : Number.POSITIVE_INFINITY;
      };
      return number(a) - number(b) || name(a).length - name(b).length || name(a).localeCompare(name(b));
    });
}

const formatDuration = (ms: number): string => `${(ms / 1000).toFixed(1)}s`;

const pad = (value: number, width = 2): string => String(value).padStart(width, "0");

const formatTime = (date: Date): string =>
  `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;

export interface SummaryMeta {
  mode: RunMode;
  /** 结果目录（写进汇总里，尽量用相对当前目录的路径） */
  outDir: string;
  generatedAt: Date;
}

/** 结果目录在汇总里尽量显示成相对路径，报告换台机器也能读。 */
function describeOutDir(dir: string): string {
  const rel = relative(process.cwd(), dir).replace(/\\/g, "/");
  const inside = rel !== "" && !rel.startsWith("..") && !isAbsolute(rel);
  return inside ? rel : dir.replace(/\\/g, "/");
}

/** 汇总 Markdown：每条用例的结果、未通过的 check、调用次数、用时。 */
export function buildSummary(outcomes: CaseRunOutcome[], meta: SummaryMeta): string {
  const failed = outcomes.filter((outcome) => !outcome.passed);
  const lines: string[] = [
    "# 自测用例运行结果",
    "",
    `- 模式：${meta.mode === "scripted" ? "scripted（剧本式假模型，不调用真实模型）" : "real（真实模型）"}`,
    `- 生成时间：${formatTime(meta.generatedAt)}`,
    `- 用例：${outcomes.length} 条，通过 ${outcomes.length - failed.length} 条，未通过 ${failed.length} 条`,
    "- 结果目录：" + `\`${toPosix(meta.outDir)}\`` + "（每条用例一个子目录，含 result.json、events.jsonl、workspace/）",
    "",
    "| 用例 | 标题 | 结果 | 核对 | 模型调用 | 用时 |",
    "|---|---|---|---|---|---|",
  ];
  for (const outcome of outcomes) {
    const passedChecks = outcome.checks.filter((check) => check.passed).length;
    const checksCell = outcome.checks.length === 0 ? "—" : `${passedChecks}/${outcome.checks.length}`;
    lines.push(
      `| ${outcome.caseId} | ${outcome.title} | ${outcome.passed ? "✅ 通过" : "❌ 未通过"} | ${checksCell} | ${outcome.modelCalls} | ${formatDuration(outcome.durationMs)} |`,
    );
  }
  if (failed.length === 0) {
    lines.push("", "全部用例通过。");
  } else {
    lines.push("", "## 未通过明细", "");
    for (const outcome of failed) {
      lines.push(`### ${outcome.caseId} · ${outcome.title}`, "");
      if (outcome.error) lines.push(`- 运行失败：${outcome.error}`);
      for (const failure of outcome.failures) lines.push(`- ${failure}`);
      lines.push("");
    }
  }
  return lines.join("\n") + "\n";
}

function readJson(file: string): unknown {
  if (!existsSync(file)) throw new Error(`找不到文件：${file}`);
  try {
    return JSON.parse(readFileSync(file, "utf8"));
  } catch (err) {
    throw new Error(`${file} 不是合法的 JSON：${err instanceof Error ? err.message : String(err)}`);
  }
}

/** scripted 模式：剧本按用例文件名字放在同目录的 scripts/ 下（见 samples/cases/README.md）。 */
function findScript(caseFile: string, def: CaseDef): string {
  const dir = join(dirname(caseFile), "scripts");
  const candidates = [join(dir, `${caseNameOf(caseFile)}.json`), join(dir, `${def.id}.json`)];
  const found = candidates.find((path) => existsSync(path));
  if (!found) throw new Error(`找不到剧本：--scripted 模式需要 ${candidates[0]}（或用 --real 调真实模型）`);
  return found;
}

interface BatchContext {
  outBase: string;
  mode: RunMode;
  toolClient: ToolClient;
  employeesDir: string;
  realModel?: ModelProvider;
}

/** 用例文件还没解析成功时，尽量从原始 JSON 里取一个能显示的名字。 */
function labelOf(raw: unknown, file: string): { caseId: string; title: string } {
  const fallback = caseNameOf(file);
  if (typeof raw !== "object" || raw === null) return { caseId: fallback, title: fallback };
  const record = raw as Record<string, unknown>;
  const caseId = typeof record.id === "string" && record.id !== "" ? record.id : fallback;
  const title = typeof record.title === "string" && record.title !== "" ? record.title : caseId;
  return { caseId, title };
}

async function runOneCase(caseFile: string, context: BatchContext): Promise<CaseRunOutcome> {
  const started = performance.now();
  let raw: unknown;
  let def: CaseDef | undefined;
  try {
    raw = readJson(caseFile);
    def = parseCase(raw, dirname(caseFile), caseFile);
    const caseOutDir = join(context.outBase, def.id);
    // 每次跑都重建这条用例的结果目录，避免上一次的产物混进来
    rmSync(caseOutDir, { recursive: true, force: true });
    mkdirSync(caseOutDir, { recursive: true });
    const workspaceDir = process.env.DE_WORKSPACE ? join(resolve(process.env.DE_WORKSPACE), def.id) : join(caseOutDir, "workspace");
    mkdirSync(join(workspaceDir, "tasks"), { recursive: true });

    const model =
      context.mode === "scripted"
        ? createScriptedModelProvider(readJson(findScript(caseFile, def)) as ModelScript)
        : (context.realModel ??= createModelProvider(loadSettings(resolveWorkspace())));

    const engine = createTaskEngine({ workspaceDir, employeesDir: context.employeesDir, toolClient: context.toolClient, model });
    const result = await runCase(engine, def, { outDir: caseOutDir });
    const durationMs = performance.now() - started;

    const evidence = await readRunEvidence(caseOutDir);
    const checks = evaluateChecks(evidence, def.expect?.checks ?? {});
    const failures = [
      ...result.failures,
      ...checks.filter((check) => !check.passed).map((check) => `核对未通过：${check.kind} ${check.target}：${check.detail}`),
    ];
    return {
      caseId: def.id,
      title: def.title,
      passed: failures.length === 0,
      failures,
      checks,
      modelCalls: evidence.modelCalls,
      durationMs,
      finalState: result.finalState,
    };
  } catch (error) {
    const { caseId, title } = labelOf(raw, caseFile);
    return {
      caseId,
      title,
      passed: false,
      failures: [],
      checks: [],
      modelCalls: 0,
      durationMs: performance.now() - started,
      error: error instanceof Error ? error.message : String(error),
    };
  }
}

export async function runCasesCommand(args: RunCasesArgs): Promise<number> {
  const files = findCaseFiles(args.pattern);
  if (files.length === 0) throw new Error(`没有匹配到用例文件：${args.pattern}（glob 由命令行自己解析，例：\"samples/cases/*.json\"）`);

  const outBase = resolve(args.outDir ?? join("out", "cases"));
  mkdirSync(outBase, { recursive: true });
  const employeesDir = resolveEmployeesDir();

  // 一次批量共用一个进程内工具服务器
  const tools = createToolsServer();
  const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  await tools.server.connect(serverTransport);
  const toolClient = await createMcpToolClient(clientTransport);

  const outcomes: CaseRunOutcome[] = [];
  try {
    const context: BatchContext = { outBase, mode: args.mode, toolClient, employeesDir };
    for (const [index, file] of files.entries()) {
      console.log(`[${index + 1}/${files.length}] ${caseNameOf(file)}`);
      const outcome = await runOneCase(file, context);
      outcomes.push(outcome);
      const passedChecks = outcome.checks.filter((check) => check.passed).length;
      console.log(
        `  ${outcome.passed ? "通过" : "失败"}  核对 ${passedChecks}/${outcome.checks.length}  模型调用 ${outcome.modelCalls}  用时 ${formatDuration(outcome.durationMs)}`,
      );
      for (const failure of outcome.failures) console.log(`  - ${failure}`);
      if (outcome.error) console.log(`  - 运行失败：${outcome.error}`);
    }
  } finally {
    await toolClient.close();
  }

  const summaryPath = join(outBase, "summary.md");
  writeFileSync(summaryPath, buildSummary(outcomes, { mode: args.mode, outDir: describeOutDir(outBase), generatedAt: new Date() }));
  const failed = outcomes.filter((outcome) => !outcome.passed).length;
  console.log(`共 ${outcomes.length} 条用例：通过 ${outcomes.length - failed} 条，未通过 ${failed} 条`);
  console.log(`汇总已写入 ${summaryPath}`);
  // 退出码等于未通过的条数
  return failed;
}
