import type { DraftReport, Metric, QcConfig, QcInput, StoreMetric, WeeklyFacts } from "../../src/contract";

function metric(partial: Partial<Metric> & { revenueCents: number }): Metric {
  return {
    prevRevenueCents: null,
    wow: null,
    grossMarginRate: null,
    targetCents: null,
    achievement: null,
    deliveryShare: null,
    ...partial,
  };
}

function store(code: string, region: string, partial: Partial<StoreMetric> & { revenueCents: number }): StoreMetric {
  return { ...metric(partial), code, name: `${code}店`, region, storeType: "商场店", ...partial };
}

/** 全公司 573,635 元 = 华东 300,000 + 华北 273,635（元）。 */
export function makeFacts(overrides: Partial<WeeklyFacts> = {}): WeeklyFacts {
  return {
    period: { year: 2026, week: 44, from: "2026-10-26", to: "2026-11-01" },
    dataCoverage: { from: "2026-09-01", to: "2026-11-01" },
    company: metric({ revenueCents: 57_363_500, prevRevenueCents: 55_000_000, wow: 0.043, grossMarginRate: 0.352, achievement: 0.953 }),
    regions: {
      华东: metric({ revenueCents: 30_000_000, prevRevenueCents: 34_090_909, wow: -0.12, achievement: 0.9 }),
      华北: metric({ revenueCents: 27_363_500, prevRevenueCents: 20_909_091, wow: 0.3086, achievement: 1.02 }),
    },
    stores: [
      store("HZ01", "华东", { revenueCents: 5_000_000, prevRevenueCents: 6_000_000, wow: -0.1667 }),
      store("BJ02", "华北", { revenueCents: 7_000_000, prevRevenueCents: 6_800_000, wow: 0.0294 }),
    ],
    categories: [{ category: "饮品", revenueCents: 10_000_000, prevRevenueCents: 9_000_000, wow: 0.1111 }],
    topProducts: [{ code: "P001", name: "拿铁", revenueCents: 1_200_000 }],
    anomalies: [
      {
        id: "region:华东",
        level: "region",
        key: "华东",
        name: "华东",
        region: "华东",
        wow: -0.12,
        revenueCents: 30_000_000,
        prevRevenueCents: 34_090_909,
        baseWeekHasMissingDays: false,
      },
      {
        id: "store:HZ01",
        level: "store",
        key: "HZ01",
        name: "HZ01店",
        region: "华东",
        storeType: "商场店",
        wow: -0.1667,
        revenueCents: 5_000_000,
        prevRevenueCents: 6_000_000,
        baseWeekHasMissingDays: false,
      },
    ],
    trend: [],
    quality: {
      duplicates: { count: 2, revenueCents: 12_000, groups: [] },
      idConflicts: { count: 0, recordIds: [] },
      amountMismatch: { count: 0 },
      unknownCodes: { count: 1, revenueCents: 5_000 },
      returns: { count: 12, revenueCents: -80_000 },
      storeDays: [],
      prevWeekStoreDays: [],
    },
    ...overrides,
  };
}

export const SECTION_IDS = ["summary", "metrics", "categories", "anomalies", "quality", "next_week"] as const;

export function makeQcConfig(overrides: Partial<QcConfig> = {}): QcConfig {
  return {
    builtin: {
      structure: {
        enabled: true,
        sections: [
          { id: "summary", heading: "本周摘要" },
          { id: "metrics", heading: "核心指标" },
          { id: "categories", heading: "品类表现" },
          { id: "anomalies", heading: "异常与原因" },
          { id: "quality", heading: "数据质量说明" },
          { id: "next_week", heading: "下周关注事项" },
        ],
        summaryMaxSentences: 5,
        nextWeekMaxItems: 5,
        minCharts: 2,
      },
      traceability: { enabled: true },
      anomaly_coverage: { enabled: true, threshold: 0.1 },
      quote_integrity: { enabled: true },
      forbidden_terms: { enabled: true, terms: ["客单价", "同比"] },
      length: { enabled: true, maxPages: 3 },
      feedback_response: { enabled: true },
    },
    contradiction: { flatTolerance: 0.05, directionMin: 0.02, magnitudeTolerance: 0.1 },
    custom: [],
    ...overrides,
  };
}

/** 一份全部通过的草稿：叙述只含槽位。 */
export function makeDraft(overrides: Partial<DraftReport> = {}): DraftReport {
  return {
    title: "第44周经营周报",
    sections: [
      { id: "summary", heading: "本周摘要", paragraphs: ["本周实收 {{company.revenue}}，环比 {{company.wow}}。"] },
      { id: "metrics", heading: "核心指标", paragraphs: ["华东实收 {{regions.华东.revenue}}。"] },
      { id: "categories", heading: "品类表现", paragraphs: ["饮品环比 {{categories.饮品.wow}}。"] },
      {
        id: "anomalies",
        heading: "异常与原因",
        paragraphs: ["华东环比 {{regions.华东.wow}}，门店 HZ01 环比 {{stores.HZ01.wow}}。"],
      },
      { id: "quality", heading: "数据质量说明", paragraphs: ["退货 {{quality.returns.count}} 笔，已照常冲减。"] },
      { id: "next_week", heading: "下周关注事项", paragraphs: [], bullets: ["跟进华东恢复情况", "关注 HZ01 门店客流"] },
    ],
    causes: [
      {
        anomalyId: "region:华东",
        cause: "华东整体受天气影响",
        level: "region",
        sourceWeek: { year: 2026, week: 44 },
        quote: "华东区受连续降雨影响",
      },
      { anomalyId: "store:HZ01", cause: "原因待核实", level: "unknown" },
    ],
    ...overrides,
  };
}

export function makeInput(overrides: Partial<QcInput> = {}): QcInput {
  return {
    config: makeQcConfig(),
    draft: makeDraft(),
    facts: makeFacts(),
    minutes: {
      minutes: [
        {
          path: "w44.docx",
          week: { year: 2026, week: 44 },
          title: "2026年第44周周会纪要",
          paragraphs: [
            { id: "W44-p01", text: "本周总体平稳。" },
            { id: "W44-p02", text: "华东区受连续降雨影响，到店客流下降。" },
          ],
        },
      ],
    },
    rendered: { estimatedPages: 2, chartCount: 2 },
    ...overrides,
  };
}
