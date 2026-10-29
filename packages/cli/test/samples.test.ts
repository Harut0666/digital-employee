import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { parseCase } from "../src/case";

const CASES_DIR = join(import.meta.dir, "..", "..", "..", "samples", "cases");
const FILES = readdirSync(CASES_DIR)
  .filter((f) => /^A-0[1-5]\.json$/.test(f))
  .sort();
/** 自测用例集：A-0x（公开样例）+ A-01-rework + S-xx（自测） */
const ALL_CASES = readdirSync(CASES_DIR)
  .filter((f) => /^(A-|S-).*\.json$/.test(f))
  .sort();
const S_CASES = ALL_CASES.filter((f) => f.startsWith("S-"));
const CATEGORIES = ["正常", "边界", "缺材料", "异常", "流程"];
/** scripted 剧本只允许给判断步骤准备响应 */
const SCRIPT_STEPS = new Set(["interpret_feedback", "match_causes", "write_report"]);

const parse = (file: string) => parseCase(JSON.parse(readFileSync(join(CASES_DIR, file), "utf8")), CASES_DIR, file);

describe("samples/cases", () => {
  test("A-01 through A-05 are all present", () => {
    expect(FILES).toEqual(["A-01.json", "A-02.json", "A-03.json", "A-04.json", "A-05.json"]);
  });

  test("自测用例集：S-xx 至少 24 条，正常/边界/缺材料/异常/流程五类都有", () => {
    expect(S_CASES.length).toBeGreaterThanOrEqual(24);
    const titles = S_CASES.map((f) => parse(f).title);
    for (const category of CATEGORIES) expect(titles.some((title) => title.startsWith(category))).toBe(true);
    // id 与文件名一致，免得报告里对不上
    for (const file of S_CASES) expect(parse(file).id).toBe(file.replace(".json", ""));
  });

  test.each(ALL_CASES)("%s parses and every referenced attachment exists", (file) => {
    const def = parse(file);
    expect(def.id).toBe(file.replace(".json", ""));
    const all = [
      ...def.attachments,
      ...def.steps.flatMap((s) => (s.command.type === "supplement" ? (s.command.attachments ?? []) : [])),
    ];
    for (const a of all) expect(existsSync(a.sourcePath)).toBe(true);
  });

  test.each(ALL_CASES)("%s 都有配套剧本，剧本只含判断步骤的提交", (file) => {
    const id = file.replace(".json", "");
    const scriptPath = join(CASES_DIR, "scripts", `${id}.json`);
    expect(existsSync(scriptPath)).toBe(true);
    const script = JSON.parse(readFileSync(scriptPath, "utf8")) as Record<string, { toolCalls?: { name: string }[] }[]>;
    for (const [step, turns] of Object.entries(script)) {
      expect(SCRIPT_STEPS.has(step)).toBe(true);
      expect(turns.length).toBeGreaterThan(0);
      for (const turn of turns) expect(turn.toolCalls?.some((c) => c.name === "submit_result")).toBe(true);
    }
  });

  test("S 用例都写了判定要点（expect.checks），格式已由 parseCase 校验", () => {
    for (const file of S_CASES) {
      const checks = parse(file).expect?.checks;
      expect(checks).toBeDefined();
      const total =
        (checks?.numbers?.length ?? 0) +
        (checks?.mustContain?.length ?? 0) +
        (checks?.mustNotContain?.length ?? 0) +
        (checks?.anomalies?.length ?? 0) +
        (checks?.pause ? 1 : 0);
      expect(total).toBeGreaterThan(0);
    }
  });

  test("A-03 pauses on missing_sales and then supplements the workbook; A-05 stops at period_out_of_range", () => {
    const a03 = parse("A-03.json");
    expect(a03.steps[0]!.expect?.pauseCode).toBe("missing_sales");
    expect(a03.steps[1]!.command.type).toBe("supplement");
    const a05 = parse("A-05.json");
    expect(a05.expect?.pauseCode).toBe("period_out_of_range");
    expect(a05.form).toEqual({ period: { year: 2026, week: 46 } });
  });
});
