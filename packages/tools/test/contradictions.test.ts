// detect_contradictions（接缝 2）：纪要矛盾完全由程序判定（docs/design.md §10.3）。
// 说法（claims）由测试手写，不依赖模型；事实来自公开样例的 compute_metrics 真实输出。
import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { Claim, DetectContradictionsOutput, ContradictionThresholds, WeeklyFacts } from "@de/core/contract";
import { connect, pct1, sample, type Harness } from "./helpers";

/** 与 employees/weekly-report/qc.yaml 的 contradiction 节一致（design.md §10.3：5%、2%、10 个百分点）。 */
const THRESHOLDS: ContradictionThresholds = { flatTolerance: 0.05, directionMin: 0.02, magnitudeTolerance: 0.1 };

let h: Harness;
let datasetPath: string;
const facts: Record<number, WeeklyFacts> = {};

beforeAll(async () => {
  h = await connect();
  const outDir = mkdtempSync(join(tmpdir(), "de-contradictions-"));
  datasetPath = (await h.call("load_sales", { salesPath: sample("销售明细.xlsx"), outDir })).datasetPath;
  for (const week of [44, 43, 42, 36]) {
    facts[week] = await h.call("compute_metrics", { datasetPath, period: { year: 2026, week }, anomalyThreshold: 0.1 });
  }
});
afterAll(async () => {
  await h.close();
});

/** 手写一条纪要说法：默认是 2026 年第 44 周纪要里的原话。 */
function claim(partial: Omit<Claim, "metric" | "sourceWeek" | "quote"> & Partial<Claim>): Claim {
  return {
    metric: "wow",
    sourceWeek: { year: 2026, week: 44 },
    quote: "（测试手写说法）",
    ...partial,
  };
}

function detect(week: number, claims: Claim[], thresholds: ContradictionThresholds = THRESHOLDS): Promise<DetectContradictionsOutput> {
  return h.call<DetectContradictionsOutput>("detect_contradictions", { claims, facts: facts[week], thresholds });
}

describe("A-01 · 2026 年第 44 周：纪要里的说法", () => {
  // A-01 纪要（本周）：华南基本持平；华东下滑较多；湖滨店停业三天；静安店对面竞品开业。
  const claims = (): Claim[] => [
    claim({
      subject: { level: "region", key: "华南" },
      direction: "flat",
      quote: "本周华南整体销售与上周基本持平",
    }),
    claim({ subject: { level: "region", key: "华东" }, direction: "down", quote: "本周华东受门店停业和竞品开业影响下滑较多" }),
    claim({ subject: { level: "store", key: "HZ01" }, direction: "down", quote: "杭州湖滨店 10 月 27 日至 29 日因商场消防改造停业三天" }),
    claim({ subject: { level: "store", key: "SH02" }, direction: "down", quote: "上海静安店对面竞品 10 月 24 日开业" }),
  ];

  test("判出且只判出一条矛盾：华南“基本持平”，实际环比 -12.7%", async () => {
    const out = await detect(44, claims());
    expect(out.skippedCount).toBe(0);
    expect(out.contradictions).toHaveLength(1);
    const [c] = out.contradictions;
    expect(c).toMatchObject({
      subject: { level: "region", key: "华南", name: "华南" },
      rule: "flat",
      claim: { direction: "flat" },
      quote: "本周华南整体销售与上周基本持平",
      sourceWeek: { year: 2026, week: 44 },
    });
    expect(pct1(c!.actualWow)).toBe(-12.7);
  });

  test("方向相反且超过 2%：华东实际 -12.0%，纪要说“上升”→ 判 direction", async () => {
    const out = await detect(44, [claim({ subject: { level: "region", key: "华东" }, direction: "up" })]);
    expect(out.contradictions.map((c) => [c.rule, c.id])).toEqual([["direction", "contradiction:region:华东"]]);
    expect(pct1(out.contradictions[0]!.actualWow)).toBe(-12.0);
  });

  test("持平但未超过 5%：华北实际 -3.3%，不判", async () => {
    expect((await detect(44, [claim({ subject: { level: "region", key: "华北" }, direction: "flat" })])).contradictions).toEqual([]);
  });

  test("方向相反：西南实际 -2.6%，“上升”仍判 direction（阈值是绝对值严格大于 2%）", async () => {
    const out = await detect(44, [claim({ subject: { level: "region", key: "西南" }, direction: "up" })]);
    expect(out.contradictions.map((c) => c.rule)).toEqual(["direction"]);
  });

  test("幅度偏差超过 10 个百分点：华东实际 -12.0%，纪要“下滑三成” → 判 magnitude", async () => {
    const out = await detect(44, [claim({ subject: { level: "region", key: "华东" }, direction: "down", magnitude: 0.3 })]);
    expect(out.contradictions.map((c) => [c.rule, c.claim.magnitude])).toEqual([["magnitude", 0.3]]);
    expect(pct1(out.contradictions[0]!.actualWow)).toBe(-12.0);
  });

  test("幅度偏差仍在 10 个百分点内：华东实际 -12.0%，纪要“下滑两成” → 不判", async () => {
    expect((await detect(44, [claim({ subject: { level: "region", key: "华东" }, direction: "down", magnitude: 0.2 })])).contradictions).toEqual([]);
  });

  test("门店说法用门店名写进周报：上海静安店“持平”，实际 -17.9%", async () => {
    const out = await detect(44, [claim({ subject: { level: "store", key: "SH02" }, direction: "flat" })]);
    expect(out.contradictions[0]).toMatchObject({
      id: "contradiction:store:SH02",
      subject: { level: "store", key: "SH02", name: "上海静安店" },
      rule: "flat",
    });
  });

  test("全公司说法：实际 -9.3%，“基本持平” → 判 flat", async () => {
    const out = await detect(44, [claim({ subject: { level: "company", key: "company" }, direction: "flat" })]);
    expect(out.contradictions[0]).toMatchObject({ id: "contradiction:company:company", subject: { name: "全公司" }, rule: "flat" });
  });

  test("同一对象的另一条说法用带序号的 ID，避免草稿引用时混淆", async () => {
    const out = await detect(44, [
      claim({ subject: { level: "region", key: "华南" }, direction: "flat" }),
      claim({ subject: { level: "region", key: "华南" }, direction: "up" }),
    ]);
    expect(out.contradictions.map((c) => c.id)).toEqual(["contradiction:region:华南", "contradiction:region:华南:2"]);
  });

  test("阈值由调用方给定：把 flatTolerance 放宽到 0.13，华南不再判为矛盾", async () => {
    const out = await detect(44, [claim({ subject: { level: "region", key: "华南" }, direction: "flat" })], {
      ...THRESHOLDS,
      flatTolerance: 0.13,
    });
    expect(out.contradictions).toEqual([]);
  });
});

describe("A-02 · 2026 年第 43 周：客流、口感不是矛盾", () => {
  test("华南“促销拉动明显”与实际 +19.0% 方向一致；客流、口感的说法对象在事实里找不到，跳过并计数", async () => {
    const out = await detect(43, [
      claim({ subject: { level: "region", key: "华南" }, direction: "up", sourceWeek: { year: 2026, week: 43 }, quote: "本周华南促销拉动明显" }),
      claim({ subject: { level: "region", key: "客流" }, direction: "flat", sourceWeek: { year: 2026, week: 43 } }),
      claim({ subject: { level: "category", key: "口感" }, direction: "down", sourceWeek: { year: 2026, week: 43 } }),
    ]);
    expect(out.contradictions).toEqual([]);
    expect(out.skippedCount).toBe(2);
  });
});

describe("A-04 · 2026 年第 42 周：数据缺失与退货", () => {
  test("区域整体下滑与说法方向一致；客流、口感不判；幅度在容差内不判", async () => {
    const out = await detect(42, [
      claim({ subject: { level: "region", key: "华东" }, direction: "down", magnitude: 0.1, sourceWeek: { year: 2026, week: 42 } }),
      claim({ subject: { level: "region", key: "华北" }, direction: "down", sourceWeek: { year: 2026, week: 42 } }),
      claim({ subject: { level: "region", key: "客流" }, direction: "flat", sourceWeek: { year: 2026, week: 42 } }),
      claim({ subject: { level: "category", key: "口感" }, direction: "up", sourceWeek: { year: 2026, week: 42 } }),
    ]);
    expect(out.contradictions).toEqual([]);
    expect(out.skippedCount).toBe(2);
  });
});

describe("detect_contradictions 其他", () => {
  test("实际环比不可比（第 36 周没有上周数据）的说法跳过并计数", async () => {
    const out = await detect(36, [claim({ subject: { level: "company", key: "company" }, direction: "flat" })]);
    expect(out.contradictions).toEqual([]);
    expect(out.skippedCount).toBe(1);
  });

  test("没有说法时返回空结果", async () => {
    expect(await detect(44, [])).toEqual({ contradictions: [], skippedCount: 0 });
  });
});

