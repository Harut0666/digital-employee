// de run-cases：批量跑用例 + 核对判定要点，汇总成 summary.md，退出码等于未通过条数。
// 端到端只跑 A-01 一条（完整跑一条要 4~5 秒）；汇总格式与失败路径用假的运行结果覆盖。
import { describe, expect, test } from "bun:test";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { buildSummary, findCaseFiles, runCasesCommand, type CaseRunOutcome } from "../src/run-cases-command";

const CASES_DIR = resolve(import.meta.dir, "..", "..", "..", "samples", "cases");
/** Windows 路径转成 Bun.Glob 认识的写法（glob 统一用 /） */
const globPath = (path: string): string => path.replace(/\\/g, "/");
const tmp = (): string => mkdtempSync(join(tmpdir(), "de-runcases-"));

const PASS: CaseRunOutcome = {
  caseId: "A-01",
  title: "正常任务（含纪要与数据不一致）",
  passed: true,
  failures: [],
  checks: [
    { kind: "numbers", target: "table:全公司/实收（元）", passed: true, detail: "实际 573635（期望 573635）" },
    { kind: "mustNotContain", target: "客单价", passed: true, detail: "正文里没有" },
  ],
  modelCalls: 2,
  durationMs: 4930,
  finalState: "delivered",
};

const FAIL: CaseRunOutcome = {
  caseId: "A-02",
  title: "正常任务（含重复记录）",
  passed: false,
  failures: ["核对未通过：numbers table:全公司/实收（元）：实际 600000，期望 632486（偏差 5.13%，容差 0.5%）"],
  checks: [
    { kind: "numbers", target: "table:全公司/实收（元）", passed: false, detail: "实际 600000，期望 632486（偏差 5.13%，容差 0.5%）" },
    { kind: "mustContain", target: "29 条记录", passed: true, detail: "正文里有" },
  ],
  modelCalls: 2,
  durationMs: 5120,
  finalState: "delivered",
};

describe("findCaseFiles", () => {
  test("glob 由命令行自己解析，Windows 下也能用；按用例编号排序", () => {
    const files = findCaseFiles(globPath(join(CASES_DIR, "A-*.json")));
    expect(files.map((f) => f.split(/[\\/]/).at(-1))).toEqual([
      "A-01.json",
      "A-01-rework.json",
      "A-02.json",
      "A-03.json",
      "A-04.json",
      "A-05.json",
    ]);
  });

  test("A 系列排在 S 系列前面（报告里先公开样例、后自测用例），组内按编号", () => {
    const names = findCaseFiles(globPath(join(CASES_DIR, "*.json"))).map((f) => f.split(/[\\/]/).at(-1));
    expect(names.slice(0, 6)).toEqual([
      "A-01.json",
      "A-01-rework.json",
      "A-02.json",
      "A-03.json",
      "A-04.json",
      "A-05.json",
    ]);
    expect(names[6]).toBe("S-01.json");
    expect(names.at(-1)).toBe("S-24.json");
    expect(names).toHaveLength(30);
  });

  test("没匹配到文件时返回空数组（由命令报错）", () => {
    expect(findCaseFiles(globPath(join(tmp(), "*.json")))).toEqual([]);
  });
});

describe("buildSummary", () => {
  test("逐条列出结果、核对通过数、模型调用次数与用时，并汇总总数", () => {
    const md = buildSummary([PASS, FAIL], { mode: "scripted", outDir: "out/cases", generatedAt: new Date(2026, 9, 29, 15, 4, 5) });
    expect(md).toContain("# 自测用例运行结果");
    expect(md).toContain("scripted");
    expect(md).toContain("用例：2 条，通过 1 条，未通过 1 条");
    expect(md).toContain("2026-10-29 15:04:05");
    // 每条用例一行：结果、核对、调用次数、用时
    expect(md).toContain("| A-01 | 正常任务（含纪要与数据不一致） | ✅ 通过 | 2/2 | 2 | 4.9s |");
    expect(md).toContain("| A-02 | 正常任务（含重复记录） | ❌ 未通过 | 1/2 | 2 | 5.1s |");
    // 未通过明细要带出具体是哪一项 check、实际值与期望值
    expect(md).toContain("### A-02 · 正常任务（含重复记录）");
    expect(md).toContain("table:全公司/实收（元）");
    expect(md).toContain("600000");
    expect(md).toContain("632486");
  });

  test("没有 checks 的用例，核对一栏写“—”而不是 0/0", () => {
    const md = buildSummary(
      [{ ...PASS, caseId: "A-99", checks: [] }],
      { mode: "scripted", outDir: "out/cases", generatedAt: new Date(2026, 9, 29) },
    );
    expect(md).toContain("| A-99 | 正常任务（含纪要与数据不一致） | ✅ 通过 | — | 2 |");
  });

  test("运行失败（没跑起来）也记一条，并写清原因", () => {
    const md = buildSummary([{ caseId: "X-99", title: "X-99", passed: false, failures: [], checks: [], modelCalls: 0, durationMs: 0, error: "用例文件无效：缺少 employee" }], {
      mode: "scripted",
      outDir: "out/cases",
      generatedAt: new Date(2026, 9, 29),
    });
    expect(md).toContain("❌ 未通过");
    expect(md).toContain("缺少 employee");
  });
});

describe("runCasesCommand", () => {
  test("跑通 A-01：退出码 0，写出 summary.md 与逐条结果目录", async () => {
    const out = join(tmp(), "cases");
    const code = await runCasesCommand({ pattern: globPath(join(CASES_DIR, "A-01.json")), mode: "scripted", outDir: out });
    expect(code).toBe(0);
    const summary = readFileSync(join(out, "summary.md"), "utf8");
    expect(summary).toContain("| A-01 |");
    expect(summary).toContain("✅ 通过");
    expect(summary).toContain("用例：1 条，通过 1 条，未通过 0 条");
    // 判定要点真的核对了（A-01 的 checks 共 48 项，全部通过）
    expect(summary).toContain("48/48");
    // 逐条结果目录：result.json 与完整事件日志
    expect(existsSync(join(out, "A-01", "result.json"))).toBe(true);
    expect(existsSync(join(out, "A-01", "events.jsonl"))).toBe(true);
  }, 120_000);

  test("用例文件跑不起来时：计入未通过条数并写进汇总（退出码=未通过条数）", async () => {
    const dir = tmp();
    writeFileSync(join(dir, "bad.json"), JSON.stringify({ id: "X-99", steps: [] }));
    const out = join(dir, "out");
    const code = await runCasesCommand({ pattern: globPath(join(dir, "*.json")), mode: "scripted", outDir: out });
    expect(code).toBe(1);
    const summary = readFileSync(join(out, "summary.md"), "utf8");
    expect(summary).toContain("X-99");
    expect(summary).toContain("employee");
  }, 60_000);

  test("glob 没匹配到用例时报错，而不是静默通过", async () => {
    const out = join(tmp(), "out");
    mkdirSync(out, { recursive: true });
    await expect(runCasesCommand({ pattern: globPath(join(tmp(), "none-*.json")), mode: "scripted", outDir: out })).rejects.toThrow(/没有匹配到/);
  });
});
