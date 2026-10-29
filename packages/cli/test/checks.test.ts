// 判定要点核对器（checks）：格式校验 + 逐项核对。
// 核对器的输入是一次运行的结果目录，所以测试里用真实渲染器造一份交付 docx，再拼出结果目录。
import { describe, expect, test } from "bun:test";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { evaluateChecks, parseChecks, readRunEvidence, type CaseChecks, type CheckOutcome } from "../src/checks";
import { makeRunDir, mutateDocx } from "./checks-fixture";
import type { RenderedReport } from "@de/core/contract";

const tmp = (): string => mkdtempSync(join(tmpdir(), "de-checks-"));

/** 复用同一份真实交付物，避免每个用例都重渲染 Word。 */
let shared: { dir: string; docxPath: string } | undefined;

async function runDir(): Promise<{ dir: string; docxPath: string }> {
  if (!shared) {
    const dir = tmp();
    const made = await makeRunDir(dir);
    shared = { dir, docxPath: made.docxPath };
  }
  return shared;
}

async function evaluate(checks: CaseChecks) {
  const { dir } = await runDir();
  return evaluateChecks(await readRunEvidence(dir), checks);
}

const failing = (outcomes: CheckOutcome[]): CheckOutcome[] => outcomes.filter((outcome) => !outcome.passed);

describe("parseChecks", () => {
  test("接受合法格式并按需保留字段", () => {
    const checks = parseChecks({
      numbers: [{ path: "table:全公司/实收（元）", expected: 100000, tolerance: 0.001 }],
      mustContain: ["以数据为准"],
      mustNotContain: ["客单价"],
      anomalies: ["region:华东"],
      pause: { code: "missing_sales", messageContains: "销售明细" },
    });
    expect(checks.numbers).toHaveLength(1);
    expect(checks.pause?.code).toBe("missing_sales");
  });

  test("空对象与 undefined 都是空核对项", () => {
    expect(parseChecks({})).toEqual({});
    expect(parseChecks(undefined)).toEqual({});
  });

  test("mustContainAny 接受“多组等价说法”，每组是一个至少一处的候选集合", () => {
    const checks = parseChecks({ mustContainAny: [["不是数据缺失", "不计为数据缺失"], ["外卖占比 31.8%"]] });
    expect(checks.mustContainAny).toEqual([["不是数据缺失", "不计为数据缺失"], ["外卖占比 31.8%"]]);
  });

  test.each([
    ["未知字段", { number: [] }, /未知/],
    ["numbers 不是数组", { numbers: "x" }, /numbers/],
    ["expected 不是数字", { numbers: [{ path: "facts:company.revenueCents", expected: "一百" }] }, /expected/],
    ["path 前缀不认识", { numbers: [{ path: "doc:xxx", expected: 1 }] }, /path/],
    ["path 缺表格单元", { numbers: [{ path: "table:全公司", expected: 1 }] }, /path/],
    ["tolerance 不是数字", { numbers: [{ path: "facts:company.revenueCents", expected: 1, tolerance: "1" }] }, /tolerance/],
    ["mustContain 含非字符串", { mustContain: [1] }, /mustContain/],
    ["mustContainAny 不是数组的数组", { mustContainAny: ["不是数据缺失"] }, /mustContainAny/],
    ["mustContainAny 有一组是空数组", { mustContainAny: [[]] }, /mustContainAny/],
    ["mustContainAny 组内没有字符串", { mustContainAny: [["不是数据缺失", 1]] }, /mustContainAny/],
    ["pause 缺 code", { pause: { messageContains: "x" } }, /pause/],
    ["pause 的 code 不认识", { pause: { code: "whatever" } }, /pause/],
  ])("格式错误直接报错：%s", (_label, raw, pattern) => {
    expect(() => parseChecks(raw)).toThrow(pattern);
  });
});

describe("readRunEvidence", () => {
  test("从结果目录读出交付 docx 文本、事实、暂停历史", async () => {
    const { dir } = await runDir();
    const evidence = await readRunEvidence(dir);
    expect(evidence.caseId).toBe("X-01");
    expect(evidence.finalState).toBe("delivered");
    // 正文：mammoth 转出来的文本能读到章节、段落与表格单元
    expect(evidence.docx?.text).toContain("本周摘要");
    expect(evidence.docx?.text).toContain("100,000");
    // 表格按行列解析成二维数组
    expect(evidence.docx?.tables[0]?.[0]).toEqual(["范围", "实收（元）", "环比", "毛利率", "目标达成率"]);
    expect(evidence.docx?.tables[0]?.[1]).toEqual(["全公司", "100,000", "-20.0%", "60.0%", "83.3%"]);
    // 事实取自事件日志里 compute_metrics 的产物
    expect(evidence.facts?.company.revenueCents).toBe(10_000_000);
    // 本次运行出现过的暂停（先是缺材料，后是待审核）
    expect(evidence.pauses.map((p) => p.code)).toEqual(["missing_sales", "awaiting_review"]);
    // 模型调用次数（汇总里的“调用次数”）
    expect(evidence.modelCalls).toBe(2);
  });

  test("结果目录缺 result.json 时抛错，不静默通过", async () => {
    const dir = tmp();
    await expect(readRunEvidence(dir)).rejects.toThrow(/result\.json/);
  });
});

describe("numbers", () => {
  test("facts: 路径按展示口径换算后比较（金额元、比例百分数、计数整数）", async () => {
    const outcomes = await evaluate({
      numbers: [
        { path: "facts:company.revenueCents", expected: 100000 },
        { path: "facts:company.wow", expected: -20.0 },
        { path: "facts:company.deliveryShare", expected: 31.8 },
        { path: "facts:regions.华东.achievement", expected: 85.7 },
        { path: "facts:quality.returns.count", expected: 11 },
        { path: "facts:quality.returns.revenueCents", expected: -260 },
        { path: "facts:topProducts.D02.revenueCents", expected: 20000 },
      ],
    });
    expect(failing(outcomes)).toEqual([]);
    expect(outcomes).toHaveLength(7);
  });

  test("table: 路径从周报正文的表格单元读数字（行按首列、列按表头匹配）", async () => {
    const outcomes = await evaluate({
      numbers: [
        { path: "table:全公司/实收（元）", expected: 100000 },
        { path: "table:华南/目标达成率", expected: 80.0 },
        { path: "table:华东/环比", expected: -20 },
      ],
    });
    expect(failing(outcomes)).toEqual([]);
  });

  test("facts: 路径能穿过退货明细数组（按门店编码取）与区域品类毛利率", async () => {
    const outcomes = await evaluate({
      numbers: [
        { path: "facts:quality.returns.groups.HZ01.count", expected: 5 },
        { path: "facts:quality.returns.groups.HZ01.revenueCents", expected: -50 },
        { path: "facts:regionCategories.华南.饮品.prevGrossMarginRate", expected: 70.4 },
        { path: "facts:regionCategories.华南.饮品.grossMarginRate", expected: 60.5 },
      ],
    });
    expect(failing(outcomes)).toEqual([]);
    expect(outcomes).toHaveLength(4);
  });

  test("默认容差 ±0.5%：差一点点算通过，差得多判不通过并说明实际值", async () => {
    const within = await evaluate({ numbers: [{ path: "table:全公司/实收（元）", expected: 100100 }] });
    expect(failing(within)).toEqual([]);
    const beyond = await evaluate({ numbers: [{ path: "table:全公司/实收（元）", expected: 110000 }] });
    expect(failing(beyond)).toHaveLength(1);
    expect(beyond[0]!.detail).toContain("100000");
    expect(beyond[0]!.detail).toContain("110000");
  });

  test("tolerance 可覆盖默认值", async () => {
    const outcomes = await evaluate({ numbers: [{ path: "facts:company.wow", expected: -20.5, tolerance: 0.001 }] });
    expect(failing(outcomes)).toHaveLength(1);
  });

  test("路径写错时给出原因而不是抛错：未知事实字段、缺行、缺列、单元格不是数字", async () => {
    const outcomes = await evaluate({
      numbers: [
        { path: "facts:company.nope", expected: 1 },
        { path: "table:不存在/实收（元）", expected: 1 },
        { path: "table:全公司/不存在的列", expected: 1 },
      ],
    });
    expect(outcomes.map((o) => o.passed)).toEqual([false, false, false]);
    expect(outcomes[0]!.detail).toContain("company.nope");
    expect(outcomes[1]!.detail).toContain("不存在");
    expect(outcomes[2]!.detail).toContain("不存在的列");
  });

  test("没有事实数据时 facts: 检查不通过（不是静默跳过）", async () => {
    const outcomes = await evaluateChecks(
      { resultDir: "/none", caseId: "X", finalState: "delivered", pauses: [], docx: undefined, facts: undefined, modelCalls: 0 },
      { numbers: [{ path: "facts:company.revenueCents", expected: 1 }] },
    );
    expect(outcomes[0]!.passed).toBe(false);
    expect(outcomes[0]!.detail).toContain("事实");
  });
});

describe("mustContain / mustNotContain", () => {
  test("必须出现的文本按忽略空白比较；不得出现的文本命中就判不通过", async () => {
    const outcomes = await evaluate({
      mustContain: ["本周全公司实收 100,000 元", "以数据为准", "外卖占比为 31.8%"],
      mustNotContain: ["客单价", "同比"],
    });
    expect(failing(outcomes)).toEqual([]);
    expect(outcomes).toHaveLength(5);
  });

  test("漏掉必须出现的内容 / 出现禁用词时，原因里带出文本与命中片段", async () => {
    const outcomes = await evaluate({
      mustContain: ["环比 +88.8%"],
      mustNotContain: ["杭州湖滨店"],
    });
    expect(outcomes[0]!.passed).toBe(false);
    expect(outcomes[0]!.detail).toContain("环比 +88.8%");
    expect(outcomes[1]!.passed).toBe(false);
    expect(outcomes[1]!.detail).toContain("杭州湖滨店");
  });

  test("没有交付 docx 时：mustContain 不通过，mustNotContain 通过（没有正文就没有违规文本）", async () => {
    const outcomes = await evaluateChecks(
      { resultDir: "/none", caseId: "X", finalState: "awaiting_initiator", pauses: [], docx: undefined, facts: undefined, modelCalls: 0 },
      { mustContain: ["本周摘要"], mustNotContain: ["第 46 周"] },
    );
    expect(outcomes[0]!.passed).toBe(false);
    expect(outcomes[0]!.detail).toContain("docx");
    expect(outcomes[1]!.passed).toBe(true);
  });
});

describe("mustContainAny", () => {
  test("每组至少命中一处等价说法即可；全部落空才判不通过并列出候选", async () => {
    const outcomes = await evaluate({
      mustContainAny: [
        ["不是数据缺失", "不计为数据缺失"],
        ["外卖占比 31.8%", "外卖占比为 31.8%"],
        ["退货 11 笔", "退货 11 笔，合计 -260 元"],
      ],
    });
    expect(failing(outcomes)).toEqual([]);
    expect(outcomes).toHaveLength(3);

    const missing = await evaluate({ mustContainAny: [["不算数据缺失"], ["重复记录不计入"]] });
    expect(failing(missing)).toHaveLength(2);
    expect(missing[0]!.target).toBe("不算数据缺失");
    // 原因里列出这一组尝试过的全部等价说法
    expect(missing[0]!.detail).toContain("不算数据缺失");
  });

  test("真实模型常见的等价说法算通过：A-01「不计为数据缺失」「外卖占比 31.8%」、A-02「已只保留第一条」", async () => {
    const report: RenderedReport = {
      title: "经营周报（2026 年第 44 周）",
      sections: [
        { id: "summary", heading: "本周摘要", paragraphs: ["本周全公司实收 100,000 元，环比 -20.0%。"] },
        { id: "metrics", heading: "核心指标", paragraphs: ["外卖占比 31.8%。"] },
        {
          id: "data_quality",
          heading: "数据质量说明",
          paragraphs: ["杭州湖滨店停业期间没有销售数据，不计为数据缺失。", "重复上传 29 条，涉及金额 5,629 元，已只保留第一条。"],
        },
      ],
    };
    const made = await makeRunDir(mkdtempSync(join(tmpdir(), "de-checks-real-")), { report });
    const outcomes = evaluateChecks(await readRunEvidence(made.dir), {
      mustContainAny: [
        ["不是数据缺失", "不计为数据缺失", "不算数据缺失"],
        ["外卖占比 31.8%", "外卖占比为 31.8%"],
        ["去重", "只保留第一条"],
        ["29 条记录", "重复上传 29 条", "29 条"],
      ],
    });
    expect(failing(outcomes)).toEqual([]);
  });
});

describe("anomalies", () => {
  test("事实里必须判出列出的异常 id", async () => {
    const ok = await evaluate({ anomalies: ["region:华东"] });
    expect(failing(ok)).toEqual([]);
    const missing = await evaluate({ anomalies: ["region:华东", "store:HZ01"] });
    expect(failing(missing)).toHaveLength(1);
    expect(missing[1]!.detail).toContain("store:HZ01");
  });
});

describe("pause", () => {
  test("在本次运行的所有暂停里找期望的 code，并核对 message 片段", async () => {
    const ok = await evaluate({ pause: { code: "missing_sales", messageContains: "销售明细" } });
    expect(failing(ok)).toEqual([]);
    // 补材料后继续的用例，最终停在 awaiting_review，但中间那次 missing_sales 仍能核对到
    const later = await evaluate({ pause: { code: "awaiting_review" } });
    expect(failing(later)).toEqual([]);
  });

  test("暂停原因不符或提示里没有该片段时判不通过", async () => {
    const wrongCode = await evaluate({ pause: { code: "period_out_of_range" } });
    expect(wrongCode[0]!.passed).toBe(false);
    expect(wrongCode[0]!.detail).toContain("missing_sales");
    const wrongText = await evaluate({ pause: { code: "missing_sales", messageContains: "补充销售数据" } });
    expect(wrongText[0]!.passed).toBe(false);
  });
});

describe("核对器自身：已知正确的交付物全部通过，改错后对应项不通过", () => {
  const GOOD: CaseChecks = {
    numbers: [
      { path: "table:全公司/实收（元）", expected: 100000 },
      { path: "table:全公司/环比", expected: -20.0 },
      { path: "table:全公司/毛利率", expected: 60.0 },
      { path: "table:全公司/目标达成率", expected: 83.3 },
      { path: "table:华东/实收（元）", expected: 60000 },
      { path: "table:华南/目标达成率", expected: 80.0 },
      { path: "facts:company.deliveryShare", expected: 31.8 },
      { path: "facts:quality.returns.count", expected: 11 },
      { path: "facts:quality.returns.revenueCents", expected: -260 },
    ],
    mustContain: ["以数据为准", "基本持平", "商场消防改造停业三天", "不是数据缺失", "外卖占比为 31.8%"],
    mustNotContain: ["客单价", "同比"],
    anomalies: ["region:华东"],
    pause: { code: "missing_sales", messageContains: "销售明细" },
  };

  test("已知正确的交付物：全部通过", async () => {
    const outcomes = await evaluate(GOOD);
    expect(failing(outcomes)).toEqual([]);
    expect(outcomes).toHaveLength(9 + 5 + 2 + 1 + 1);
  });

  test("把交付物里的一个数字改错：只有对应的数字检查不通过", async () => {
    const good = await runDir();
    const bad = await makeRunDir(mkdtempSync(join(tmpdir(), "de-checks-bad-")));
    await mutateDocx(bad.docxPath, { replace: [["60,000", "66,000"]] });
    const outcomes = await evaluateChecks(await readRunEvidence(bad.dir), GOOD);
    const failed = failing(outcomes);
    expect(failed).toHaveLength(1);
    expect(failed[0]!.target).toBe("table:华东/实收（元）");
    expect(failed[0]!.detail).toContain("66000");
    // 原来那份没被改动，仍然全部通过
    expect(failing(await evaluateChecks(await readRunEvidence(good.dir), GOOD))).toEqual([]);
  });

  test("往正文里插入“客单价”：不得出现的文本检查不通过", async () => {
    const bad = await makeRunDir(mkdtempSync(join(tmpdir(), "de-checks-word-")));
    await mutateDocx(bad.docxPath, { insert: "客单价 12 元。" });
    const outcomes = await evaluateChecks(await readRunEvidence(bad.dir), GOOD);
    const failed = failing(outcomes);
    expect(failed.map((o) => o.target)).toEqual(["客单价"]);
    expect(failed[0]!.detail).toContain("客单价");
  });
});
