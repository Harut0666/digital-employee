import { describe, expect, test } from "bun:test";
import type { DraftReport, QcCheckResult, QcConfig } from "../../src/contract";
import { createQcEngine } from "../../src/qc";
import { makeDraft, makeInput, makeQcConfig } from "./fixtures";

const engine = createQcEngine();

function run(overrides: Parameters<typeof makeInput>[0] = {}): QcCheckResult[] {
  return engine.run(makeInput(overrides));
}
function get(results: QcCheckResult[], id: string): QcCheckResult {
  const found = results.find((r) => r.id === id);
  if (!found) throw new Error(`no result for ${id}: ${results.map((r) => r.id).join(",")}`);
  return found;
}

describe("异常覆盖 anomaly_coverage", () => {
  test("检出的每条异常都有原因条目 → 通过", () => {
    expect(get(run(), "anomaly_coverage").passed).toBe(true);
  });

  test("漏掉一条异常 → 不通过，重跑 match_causes", () => {
    const draft = makeDraft();
    draft.causes = draft.causes.filter((c) => c.anomalyId !== "store:HZ01");
    const r = get(run({ draft }), "anomaly_coverage");
    expect(r.passed).toBe(false);
    expect(r.fixStep).toBe("match_causes");
    expect(r.issues.join("\n")).toContain("store:HZ01");
  });

  test("环比绝对值未超过配置阈值的异常不要求覆盖", () => {
    const config = makeQcConfig();
    config.builtin.anomaly_coverage.threshold = 0.15; // 华东 -12% 不再算异常，HZ01 -16.67% 仍算
    const draft = makeDraft();
    draft.causes = draft.causes.filter((c) => c.anomalyId !== "region:华东");
    expect(get(run({ config, draft }), "anomaly_coverage").passed).toBe(true);
  });
});

describe("引用真实 quote_integrity", () => {
  test("摘录是所注周次纪要某段的连续子串 → 通过", () => {
    expect(get(run(), "quote_integrity").passed).toBe(true);
  });

  test("摘录不在纪要原文中 → 不通过，重跑 match_causes", () => {
    const draft = makeDraft();
    draft.causes[0]!.quote = "华东区因台风停业三天";
    const r = get(run({ draft }), "quote_integrity");
    expect(r.passed).toBe(false);
    expect(r.fixStep).toBe("match_causes");
    expect(r.issues.join("\n")).toContain("region:华东");
  });

  test("摘录出自别的周次的纪要 → 不通过", () => {
    const draft = makeDraft();
    draft.causes[0]!.sourceWeek = { year: 2026, week: 43 };
    const r = get(run({ draft }), "quote_integrity");
    expect(r.passed).toBe(false);
    expect(r.issues.join("\n")).toContain("第 43 周");
  });

  test("有来源层级却没有摘录 → 不通过；原因待核实（unknown）无需摘录", () => {
    const draft = makeDraft();
    delete draft.causes[0]!.quote;
    expect(get(run({ draft }), "quote_integrity").passed).toBe(false);
    const ok = makeDraft();
    expect(get(run({ draft: ok }), "quote_integrity").passed).toBe(true);
  });
});

describe("意见响应 feedback_response（仅重做时检查）", () => {
  const previous = makeDraft();
  const feedback = { comment: "摘要太长，请精简", sections: ["summary"] };

  function revised(overrides: Partial<DraftReport> = {}): DraftReport {
    const d = makeDraft({
      revisionNotes: [{ comment: "摘要太长，请精简", sections: ["summary"], change: "摘要由 3 句压缩为 1 句" }],
      ...overrides,
    });
    d.sections = d.sections.map((s) => (s.id === "summary" ? { ...s, paragraphs: ["本周实收 {{company.revenue}}。"] } : s));
    return d;
  }

  test("非重做时不出现该项", () => {
    expect(run().map((r) => r.id)).not.toContain("feedback_response");
  });

  test("修改说明覆盖退回意见且对应章节确实改了 → 通过", () => {
    const r = get(run({ draft: revised(), previousDraft: previous, feedback }), "feedback_response");
    expect(r.issues).toEqual([]);
    expect(r.passed).toBe(true);
  });

  test("没有修改说明 → 不通过，重跑 write_report", () => {
    const draft = revised();
    delete draft.revisionNotes;
    const r = get(run({ draft, previousDraft: previous, feedback }), "feedback_response");
    expect(r.passed).toBe(false);
    expect(r.fixStep).toBe("write_report");
    expect(r.issues.join("\n")).toContain("修改说明");
  });

  test("说明声称改了但对应章节内容没变 → 不通过", () => {
    const draft = makeDraft({ revisionNotes: [{ comment: "摘要太长，请精简", sections: ["summary"], change: "已精简" }] });
    const r = get(run({ draft, previousDraft: previous, feedback }), "feedback_response");
    expect(r.passed).toBe(false);
    expect(r.issues.join("\n")).toContain("summary");
  });

  test("说明没有提到被要求修改的章节 → 不通过", () => {
    const draft = revised({ revisionNotes: [{ comment: "摘要太长，请精简", sections: ["metrics"], change: "已改" }] });
    const r = get(run({ draft, previousDraft: previous, feedback }), "feedback_response");
    expect(r.passed).toBe(false);
  });

  test("意见未指定章节时，至少要有一个章节确实改了", () => {
    const fb = { comment: "整体再精炼" };
    const noChange = makeDraft({ revisionNotes: [{ comment: "整体再精炼", sections: [], change: "已改" }] });
    expect(get(run({ draft: noChange, previousDraft: previous, feedback: fb }), "feedback_response").passed).toBe(false);
    const changed = revised({ revisionNotes: [{ comment: "整体再精炼", sections: ["summary"], change: "精简摘要" }] });
    expect(get(run({ draft: changed, previousDraft: previous, feedback: fb }), "feedback_response").passed).toBe(true);
  });
});

describe("自定义规则（6 种模板）", () => {
  function custom(rule: QcConfig["custom"][number]): QcConfig {
    return makeQcConfig({ custom: [rule] });
  }
  const base = { id: "c1", label: "自定义规则", enabled: true };

  test("未启用的规则不出现", () => {
    const config = custom({ ...base, enabled: false, template: "forbidden_terms", params: { terms: ["x"] } });
    expect(run({ config }).map((r) => r.id)).not.toContain("c1");
  });

  test("forbidden_terms：命中不通过 / 未命中通过", () => {
    const hit = custom({ ...base, template: "forbidden_terms", params: { terms: ["下降"] } });
    const draft = makeDraft();
    draft.sections[3]!.paragraphs = ["华东客流下降。"];
    const bad = get(run({ config: hit, draft }), "c1");
    expect(bad.passed).toBe(false);
    expect(bad.fixStep).toBe("write_report");
    expect(bad.label).toBe("自定义规则");
    expect(get(run({ config: custom({ ...base, template: "forbidden_terms", params: { terms: ["暴跌"] } }) }), "c1").passed).toBe(true);
  });

  test("required_terms：缺少必需词不通过", () => {
    const cfg = (terms: string[]) => custom({ ...base, template: "required_terms", params: { terms } });
    expect(get(run({ config: cfg(["环比"]) }), "c1").passed).toBe(true);
    const bad = get(run({ config: cfg(["风险提示"]) }), "c1");
    expect(bad.passed).toBe(false);
    expect(bad.issues.join("\n")).toContain("风险提示");
  });

  test("section_must_contain：指定章节必须包含词", () => {
    const cfg = (terms: string[]) => custom({ ...base, template: "section_must_contain", params: { section: "quality", terms } });
    expect(get(run({ config: cfg(["退货"]) }), "c1").passed).toBe(true);
    const bad = get(run({ config: cfg(["缺失"]) }), "c1");
    expect(bad.passed).toBe(false);
    expect(bad.issues.join("\n")).toContain("数据质量说明");
  });

  test("max_items：章节条数上限", () => {
    const cfg = (max: number) => custom({ ...base, template: "max_items", params: { section: "next_week", max } });
    expect(get(run({ config: cfg(2) }), "c1").passed).toBe(true);
    expect(get(run({ config: cfg(1) }), "c1").passed).toBe(false);
  });

  test("max_pages：页数上限，不留余量", () => {
    const cfg = (max: number) => custom({ ...base, template: "max_pages", params: { max } });
    expect(get(run({ config: cfg(2) }), "c1").passed).toBe(true);
    expect(get(run({ config: cfg(1.5) }), "c1").passed).toBe(false);
  });

  test("number_range：事实值（按展示口径：元 / 百分数）必须在区间内", () => {
    const cfg = (min: number, max: number) => custom({ ...base, template: "number_range", params: { path: "company.achievement", min, max } });
    expect(get(run({ config: cfg(90, 100) }), "c1").passed).toBe(true); // 95.3%
    const bad = get(run({ config: cfg(96, 120) }), "c1");
    expect(bad.passed).toBe(false);
    expect(bad.issues.join("\n")).toContain("company.achievement");
    const money = custom({ ...base, template: "number_range", params: { path: "company.revenue", min: 500000, max: 600000 } });
    expect(get(run({ config: money }), "c1").passed).toBe(true);
  });

  test("参数无效 → 不通过且无 fixStep（转人工）", () => {
    const r = get(run({ config: custom({ ...base, template: "max_items", params: { section: "next_week" } }) }), "c1");
    expect(r.passed).toBe(false);
    expect(r.fixStep).toBeUndefined();
    expect(r.issues.join("\n")).toContain("参数");
  });

  test("自定义规则不通过时的 fixStep 跟随 QcInput.fixSteps.write；参数无效仍不带 fixStep", () => {
    const config = custom({ ...base, template: "forbidden_terms", params: { terms: ["下降"] } });
    const draft = makeDraft();
    draft.sections[3]!.paragraphs = ["华东客流下降。"];
    const fixSteps = { write: "compose_report", causes: "link_causes" };
    const configured = get(run({ config, draft, fixSteps }), "c1");
    expect(configured.passed).toBe(false);
    expect(configured.fixStep).toBe("compose_report");
    // 没传 fixSteps 时沿用场景 A 的默认 id
    expect(get(run({ config, draft }), "c1").fixStep).toBe("write_report");
    // 参数无效（无 fixStep）不受配置影响，仍然转人工
    const broken = custom({ ...base, template: "forbidden_terms", params: { terms: [] } });
    const manual = get(run({ config: broken, draft, fixSteps }), "c1");
    expect(manual.passed).toBe(false);
    expect(manual.fixStep).toBeUndefined();
  });
});
