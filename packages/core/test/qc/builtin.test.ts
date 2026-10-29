import { describe, expect, test } from "bun:test";
import type { DraftReport, QcCheckResult } from "../../src/contract";
import { createQcEngine } from "../../src/qc";
import { makeDraft, makeFacts, makeInput, makeQcConfig } from "./fixtures";

const engine = createQcEngine();

function run(overrides: Parameters<typeof makeInput>[0] = {}): QcCheckResult[] {
  return engine.run(makeInput(overrides));
}

function get(results: QcCheckResult[], id: string): QcCheckResult {
  const found = results.find((r) => r.id === id);
  if (!found) throw new Error(`no result for ${id}: ${results.map((r) => r.id).join(",")}`);
  return found;
}

function withSection(draft: DraftReport, id: string, patch: Partial<DraftReport["sections"][number]>): DraftReport {
  return { ...draft, sections: draft.sections.map((s) => (s.id === id ? { ...s, ...patch } : s)) };
}

describe("质检引擎：整体", () => {
  test("合格草稿：6 项内置检查全部通过（意见响应仅重做时检查）", () => {
    const results = run();
    expect(results.map((r) => r.id)).toEqual(["structure", "traceability", "anomaly_coverage", "quote_integrity", "forbidden_terms", "length"]);
    for (const r of results) {
      expect(r.issues).toEqual([]);
      expect(r.passed).toBe(true);
      expect(r.fixStep).toBeUndefined();
      expect(r.label.length).toBeGreaterThan(0);
    }
  });

  test("enabled=false 的检查不出现", () => {
    const config = makeQcConfig();
    config.builtin.length.enabled = false;
    config.builtin.forbidden_terms.enabled = false;
    const ids = run({ config }).map((r) => r.id);
    expect(ids).not.toContain("length");
    expect(ids).not.toContain("forbidden_terms");
  });
});

describe("结构完整 structure", () => {
  test("缺少章节 → 不通过，重跑 write_report", () => {
    const draft = makeDraft();
    draft.sections = draft.sections.filter((s) => s.id !== "quality");
    const r = get(run({ draft }), "structure");
    expect(r.passed).toBe(false);
    expect(r.fixStep).toBe("write_report");
    expect(r.issues.join("\n")).toContain("数据质量说明");
  });

  test("章节顺序错误 → 不通过", () => {
    const draft = makeDraft();
    const [a, b, ...rest] = draft.sections;
    draft.sections = [b!, a!, ...rest];
    const r = get(run({ draft }), "structure");
    expect(r.passed).toBe(false);
    expect(r.issues.join("\n")).toContain("顺序");
  });

  test("摘要超过 5 句 → 不通过", () => {
    const draft = withSection(makeDraft(), "summary", { paragraphs: ["一。二。三！四？五。", "六。"] });
    const r = get(run({ draft }), "structure");
    expect(r.passed).toBe(false);
    expect(r.issues.join("\n")).toContain("摘要");
  });

  test("摘要恰好 5 句 → 通过", () => {
    const draft = withSection(makeDraft(), "summary", { paragraphs: ["一。二。三！四？五。"] });
    expect(get(run({ draft }), "structure").passed).toBe(true);
  });

  test("下周关注超过 5 条 → 不通过", () => {
    const draft = withSection(makeDraft(), "next_week", { bullets: ["a", "b", "c", "d", "e", "f"] });
    const r = get(run({ draft }), "structure");
    expect(r.passed).toBe(false);
    expect(r.issues.join("\n")).toContain("下周关注");
  });

  test("图表少于 2 张 → 不通过；未提供渲染统计时不判断", () => {
    const bad = get(run({ rendered: { estimatedPages: 2, chartCount: 1 } }), "structure");
    expect(bad.passed).toBe(false);
    expect(bad.issues.join("\n")).toContain("图表");
    expect(get(run({ rendered: undefined }), "structure").passed).toBe(true);
  });
});

describe("数据溯源 traceability", () => {
  test("正文直接写 “环比 +12%” → 抓到裸数字，重跑 write_report", () => {
    const draft = withSection(makeDraft(), "summary", { paragraphs: ["本周实收环比 +12%，表现良好。"] });
    const r = get(run({ draft }), "traceability");
    expect(r.passed).toBe(false);
    expect(r.fixStep).toBe("write_report");
    expect(r.issues.join("\n")).toContain("+12%");
  });

  test("金额、计数的裸数字同样被抓到", () => {
    const draft = withSection(makeDraft(), "quality", { paragraphs: ["共 12 笔退货，合计 573,635 元。"] });
    const r = get(run({ draft }), "traceability");
    expect(r.passed).toBe(false);
    const text = r.issues.join("\n");
    expect(text).toContain("12");
    expect(text).toContain("573,635");
  });

  test("无法解析的槽位 → 不通过", () => {
    const draft = withSection(makeDraft(), "summary", { paragraphs: ["西南区环比 {{regions.西南.wow}}。"] });
    const r = get(run({ draft }), "traceability");
    expect(r.passed).toBe(false);
    expect(r.issues.join("\n")).toContain("regions.西南.wow");
  });

  test("白名单：周次、年份、日期、门店编码、前 N 不算裸数字", () => {
    const draft = withSection(makeDraft(), "summary", {
      paragraphs: ["2026年第44周（10月26日至11月1日，W44）门店 HZ01 的前5单品，近4周趋势。 2026-10-26。"],
    });
    const r = get(run({ draft }), "traceability");
    expect(r.issues).toEqual([]);
    expect(r.passed).toBe(true);
  });

  test("白名单：带空格的日期写法（10 月 27 日、10/27、11/03–11/06）不算裸数字", () => {
    const draft = withSection(makeDraft(), "next_week", {
      paragraphs: [],
      bullets: ["10 月 27 日至 29 日停业；方案 10/27 前提交，复盘 11/03–11/06 完成。", "10月30日（周五）恢复，11 月 2 日上市。"],
    });
    const r = get(run({ draft }), "traceability");
    expect(r.issues).toEqual([]);
  });

  test("白名单：引号「」或“”内、逐字出自 causes[].quote 的数字不算裸数字", () => {
    const draft = makeDraft();
    draft.causes[0]!.quote = "手工台账记录当日实收约 5,100 元，另有 15 份退货";
    draft.causes[0]!.cause = "纪要称「当日实收约 5,100 元」，且“另有 15 份退货”";
    const r = get(run({ draft }), "traceability");
    expect(r.issues).toEqual([]);
    expect(r.passed).toBe(true);
  });

  test("引号内的数字必须真的出自 quote；引号外的数字、编造的引文仍被抓到", () => {
    const draft = makeDraft();
    draft.causes[0]!.quote = "手工台账记录当日实收约 5,100 元";
    draft.causes[0]!.cause = "纪要称「当日实收约 9,999 元」，实际下降 15%";
    const r = get(run({ draft }), "traceability");
    expect(r.passed).toBe(false);
    const text = r.issues.join("\n");
    expect(text).toContain("9,999");
    expect(text).toContain("15%");
    expect(text).not.toContain("5,100");
  });

  test("嵌套引号：「“第二杯半价”活动已于 10 月 25 日结束」整句出自 quote 即可", () => {
    const draft = makeDraft();
    draft.causes[0]!.quote = "“第二杯半价”活动已于 10 月 25 日结束。陈某表示本周基本持平";
    draft.causes[0]!.cause = "纪要称「“第二杯半价”活动已于 10 月 25 日结束」，销售量 120 杯";
    const r = get(run({ draft }), "traceability");
    expect(r.passed).toBe(false);
    expect(r.issues.join("\n")).toContain("120");
    expect(r.issues.join("\n")).not.toContain("25");
  });

  test("白名单：事实里的实体名称（如单品“6寸生日蛋糕”）自带的数字不算裸数字；事实里没有的仍被抓到", () => {
    const base = makeFacts();
    const facts = makeFacts({ topProducts: [...base.topProducts, { code: "C04", name: "6寸生日蛋糕", revenueCents: 3_165_959 }] });
    const ok = withSection(makeDraft(), "categories", { paragraphs: [], bullets: ["前 5 名单品：6寸生日蛋糕 {{topProducts.C04.revenue}}。"] });
    expect(get(run({ draft: ok, facts }), "traceability").passed).toBe(true);
    const bad = withSection(makeDraft(), "categories", { paragraphs: [], bullets: ["8寸生日蛋糕 {{topProducts.C04.revenue}} 卖得很好。"] });
    const r = get(run({ draft: bad, facts }), "traceability");
    expect(r.passed).toBe(false);
    expect(r.issues.join("\n")).toContain("8");
  });

  test("槽位内部的数字不算裸数字；原因摘录（quote）里的数字不扫描", () => {
    const draft = makeDraft();
    draft.causes[0]!.quote = "华东区受连续降雨影响";
    const withNum = withSection(draft, "summary", { paragraphs: ["{{stores.HZ01.wow}} 与 {{company.revenue}}。"] });
    expect(get(run({ draft: withNum }), "traceability").passed).toBe(true);
  });

  test("原因说明 cause 中的裸数字同样被抓到", () => {
    const draft = makeDraft();
    draft.causes[0]!.cause = "客流下降 15%";
    expect(get(run({ draft }), "traceability").passed).toBe(false);
  });

  test("区域合计不等于全公司 → 不通过", () => {
    const base = makeFacts();
    const facts = makeFacts({ regions: { ...base.regions, 华北: { ...base.regions["华北"]!, revenueCents: 1 } } });
    const r = get(run({ facts }), "traceability");
    expect(r.passed).toBe(false);
    expect(r.issues.join("\n")).toContain("区域合计");
  });
});

describe("禁用指标 forbidden_terms", () => {
  test("出现 “同比” → 不通过，重跑 write_report", () => {
    const draft = withSection(makeDraft(), "summary", { paragraphs: ["本周同比增长 {{company.wow}}。"] });
    const r = get(run({ draft }), "forbidden_terms");
    expect(r.passed).toBe(false);
    expect(r.fixStep).toBe("write_report");
    expect(r.issues.join("\n")).toContain("同比");
  });

  test("要点和原因说明里的禁用词也会被发现", () => {
    const draft = makeDraft();
    draft.causes[1]!.cause = "客单价下降";
    const r = get(run({ draft }), "forbidden_terms");
    expect(r.passed).toBe(false);
    expect(r.issues.join("\n")).toContain("客单价");
  });
});

describe("篇幅 length", () => {
  test("估算 2.8 页超过 3 页的 90%（留 10% 余量）→ 不通过，要求压缩", () => {
    const r = get(run({ rendered: { estimatedPages: 2.8, chartCount: 2 } }), "length");
    expect(r.passed).toBe(false);
    expect(r.fixStep).toBe("write_report");
    expect(r.issues.join("\n")).toContain("压缩");
  });

  test("估算 2.7 页 → 通过", () => {
    expect(get(run({ rendered: { estimatedPages: 2.7, chartCount: 2 } }), "length").passed).toBe(true);
  });
});
