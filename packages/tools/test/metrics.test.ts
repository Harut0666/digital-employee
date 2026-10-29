// 公开样例 A-01 / A-02 / A-04 判定要点中的数字（来自 samples/scenario-a/公开样例用例.md）。
import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { WeeklyFacts } from "@de/core/contract";
import { connect, pct1, sample, yuan, type Harness } from "./helpers";

let h: Harness;
let datasetPath: string;
const facts: Record<number, WeeklyFacts> = {};

beforeAll(async () => {
  h = await connect();
  const outDir = mkdtempSync(join(tmpdir(), "de-metrics-"));
  datasetPath = (await h.call("load_sales", { salesPath: sample("销售明细.xlsx"), outDir })).datasetPath;
  for (const week of [44, 43, 42]) {
    facts[week] = await h.call("compute_metrics", {
      datasetPath,
      period: { year: 2026, week },
      anomalyThreshold: 0.1,
    });
  }
});
afterAll(async () => {
  await h.close();
});

/** 金额允许 ±0.5% */
const near = (actualCents: number, expectedYuan: number) => {
  expect(Math.abs(yuan(actualCents) - expectedYuan)).toBeLessThanOrEqual(Math.max(1, expectedYuan * 0.005));
};

interface Expected {
  revenue: number;
  wow: number;
  margin: number;
  delivery?: number;
}
function checkMetric(m: WeeklyFacts["company"], e: Expected & { achievement?: number }) {
  near(m.revenueCents, e.revenue);
  expect(pct1(m.wow)).toBe(e.wow);
  if (e.margin !== undefined) expect(pct1(m.grossMarginRate)).toBe(e.margin);
  if (e.delivery !== undefined) expect(pct1(m.deliveryShare)).toBe(e.delivery);
  if (e.achievement !== undefined) expect(pct1(m.achievement)).toBe(e.achievement);
}
const storeWow = (f: WeeklyFacts, code: string) => pct1(f.stores.find((s) => s.code === code)!.wow);
const anomalyStores = (f: WeeklyFacts) =>
  f.anomalies
    .filter((a) => a.level === "store")
    .map((a) => a.key)
    .sort();
const anomalyRegions = (f: WeeklyFacts) =>
  f.anomalies
    .filter((a) => a.level === "region")
    .map((a) => a.key)
    .sort();

describe("A-01 · 2026 年第 44 周", () => {
  const f = () => facts[44]!;
  test("周期与数据覆盖", () => {
    expect(f().period).toEqual({ year: 2026, week: 44, from: "2026-10-26", to: "2026-11-01" });
    expect(f().dataCoverage).toEqual({ from: "2026-08-31", to: "2026-11-01" });
  });
  test("全公司", () => {
    checkMetric(f().company, { revenue: 573635, wow: -9.3, margin: 61.1, delivery: 31.8 });
  });
  test("四个区域", () => {
    checkMetric(f().regions["华东"]!, { revenue: 197618, wow: -12.0, margin: undefined as never, achievement: 84.1 });
    checkMetric(f().regions["华南"]!, { revenue: 177306, wow: -12.7, margin: undefined as never, achievement: 92.3 });
    checkMetric(f().regions["华北"]!, { revenue: 119712, wow: -3.3, margin: undefined as never, achievement: 92.1 });
    checkMetric(f().regions["西南"]!, { revenue: 78998, wow: -2.6, margin: undefined as never, achievement: 96.3 });
  });
  test("超过 ±10% 的区域与门店", () => {
    expect(anomalyRegions(f())).toEqual(["华东", "华南"]);
    expect(anomalyStores(f())).toEqual(["HZ01", "SH02", "SZ01", "SZ02"]);
    expect(storeWow(f(), "HZ01")).toBe(-38.3);
    expect(storeWow(f(), "SH02")).toBe(-17.9);
    expect(storeWow(f(), "SZ01")).toBe(-15.3);
    expect(storeWow(f(), "SZ02")).toBe(-17.3);
  });
  test("前 5 单品", () => {
    expect(f().topProducts.map((p) => p.name)).toEqual(["拿铁", "生椰拿铁", "美式咖啡", "6寸生日蛋糕", "草莓奶油切块"]);
  });
  test("退货 11 笔、合计 -260 元；杭州湖滨店 10/27-10/29 无数据", () => {
    expect(f().quality.returns.count).toBe(11);
    expect(yuan(f().quality.returns.revenueCents)).toBe(-260);
    const hz = f().quality.storeDays.filter((d) => d.store === "HZ01");
    expect(hz).toEqual([
      { store: "HZ01", date: "2026-10-27", status: "no_data" },
      { store: "HZ01", date: "2026-10-28", status: "no_data" },
      { store: "HZ01", date: "2026-10-29", status: "no_data" },
    ]);
  });
  test("近 4 周趋势：W41 到 W44，末项为本周", () => {
    expect(f().trend.map((t) => t.week.week)).toEqual([41, 42, 43, 44]);
    expect(f().trend[3]!.companyCents).toBe(f().company.revenueCents);
    expect(Object.keys(f().trend[3]!.regions).sort()).toEqual(["华东", "华北", "华南", "西南"].sort());
  });
});

describe("A-02 · 2026 年第 43 周（含重复记录）", () => {
  const f = () => facts[43]!;
  test("全公司", () => {
    checkMetric(f().company, { revenue: 632486, wow: 4.4, margin: 60.1, delivery: 31.3 });
  });
  test("四个区域（华南为去重后 203,120 元）", () => {
    checkMetric(f().regions["华东"]!, { revenue: 224516, wow: -2.8, margin: undefined as never, achievement: 95.5 });
    checkMetric(f().regions["华南"]!, { revenue: 203120, wow: 19.0, margin: undefined as never, achievement: 105.8 });
    checkMetric(f().regions["华北"]!, { revenue: 123734, wow: -3.5, margin: undefined as never, achievement: 95.2 });
    checkMetric(f().regions["西南"]!, { revenue: 81116, wow: 6.6, margin: undefined as never, achievement: 98.9 });
  });
  test("南山店 10/21 的 29 条重复记录被剔除，合计 5,629 元", () => {
    const d = f().quality.duplicates;
    expect(d.count).toBe(29);
    expect(yuan(d.revenueCents)).toBe(5629);
    expect(d.groups).toEqual([{ store: "SZ01", date: "2026-10-21", count: 29, revenueCents: d.revenueCents }]);
  });
  test("超过 ±10% 的门店", () => {
    expect(anomalyStores(f())).toEqual(["CD02", "GZ01", "GZ02", "SZ01", "SZ02"]);
    expect(storeWow(f(), "GZ01")).toBe(15.0);
    expect(storeWow(f(), "GZ02")).toBe(30.1);
    expect(storeWow(f(), "SZ01")).toBe(17.4);
    expect(storeWow(f(), "SZ02")).toBe(17.2);
    expect(storeWow(f(), "CD02")).toBe(17.4);
  });
  test("海珠店环比偏高：上周 10/14 数据缺失，基数偏低（加分项）", () => {
    const gz02 = f().anomalies.find((a) => a.id === "store:GZ02")!;
    expect(gz02.baseWeekHasMissingDays).toBe(true);
    expect(f().anomalies.find((a) => a.id === "store:GZ01")!.baseWeekHasMissingDays).toBe(false);
    expect(f().quality.prevWeekStoreDays).toContainEqual({ store: "GZ02", date: "2026-10-14", status: "no_data" });
  });
  test("前 5 单品", () => {
    expect(f().topProducts.map((p) => p.name)).toEqual(["拿铁", "生椰拿铁", "美式咖啡", "6寸生日蛋糕", "8寸生日蛋糕"]);
  });
});

describe("A-04 · 2026 年第 42 周（数据缺失与退货）", () => {
  const f = () => facts[42]!;
  test("全公司", () => {
    checkMetric(f().company, { revenue: 605909, wow: -11.7, margin: 61.0, delivery: 31.5 });
  });
  test("四个区域", () => {
    checkMetric(f().regions["华东"]!, { revenue: 230947, wow: -15.8, margin: undefined as never, achievement: 98.3 });
    checkMetric(f().regions["华南"]!, { revenue: 170692, wow: -12.8, margin: undefined as never, achievement: 88.9 });
    checkMetric(f().regions["华北"]!, { revenue: 128159, wow: -5.6, margin: undefined as never, achievement: 98.6 });
    checkMetric(f().regions["西南"]!, { revenue: 76111, wow: -4.9, margin: undefined as never, achievement: 92.8 });
  });
  test("广州海珠店 10/14 是无数据门店日", () => {
    expect(f().quality.storeDays).toContainEqual({ store: "GZ02", date: "2026-10-14", status: "no_data" });
  });
  test("退货 15 笔、合计 -656 元", () => {
    expect(f().quality.returns.count).toBe(15);
    expect(yuan(f().quality.returns.revenueCents)).toBe(-656);
  });
});

describe("compute_metrics 其他", () => {
  test("第 36 周没有上周数据：环比为 null，趋势只有本周", async () => {
    const f = await h.call<WeeklyFacts>("compute_metrics", {
      datasetPath,
      period: { year: 2026, week: 36 },
      anomalyThreshold: 0.1,
    });
    expect(f.company.prevRevenueCents).toBeNull();
    expect(f.company.wow).toBeNull();
    expect(f.anomalies).toEqual([]);
    expect(f.trend.map((t) => t.week.week)).toEqual([36]);
  });

  test("超出数据覆盖范围的周期返回 isError", async () => {
    const r = await h.raw("compute_metrics", {
      datasetPath,
      period: { year: 2026, week: 46 },
      anomalyThreshold: 0.1,
    });
    expect(r.isError).toBe(true);
  });
});
