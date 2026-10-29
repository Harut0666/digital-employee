// design.md §10.2 的清洗与口径规则，用一份手工构造的小数据逐条验证。
import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import ExcelJS from "exceljs";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { WeeklyFacts } from "@de/core/contract";
import { connect, type Harness } from "./helpers";

type Row = [id: string, date: string, store: string, channel: string, product: string, qty: number, gross: number, disc: number, net: number, cost: number];

function days(from: string, n: number): string[] {
  const out: string[] = [];
  for (let i = 0; i < n; i++) out.push(new Date(Date.parse(from) + i * 86_400_000).toISOString().slice(0, 10));
  return out;
}
const rid = (store: string, date: string) => `R${date.replace(/-/g, "")}${store}-0001`;
const base = (store: string, date: string, net: number): Row => [rid(store, date), date, store, "堂食自提", "P1", 1, net, 0, net, net * 0.4];

function buildRows(): Row[] {
  const rows: Row[] = [];
  // A01：W36 每天 100，W37 每天 110（周合计 700 → 770，环比恰好 +10%）
  for (const d of days("2026-08-31", 7)) rows.push(base("A01", d, 100));
  for (const d of days("2026-09-07", 7)) rows.push(base("A01", d, 110));
  // A02（9/3 开业）：W36 从 9/3 起每天 50；W37 前 6 天每天 50（9/13 无数据），另有一笔退货
  for (const d of days("2026-09-03", 4)) rows.push(base("A02", d, 50));
  for (const d of days("2026-09-07", 6)) rows.push(base("A02", d, 50));
  // 数据质量
  rows.push(base("A01", "2026-09-08", 110)); // 完全重复
  rows.push([rid("A01", "2026-09-09"), "2026-09-09", "A01", "堂食自提", "P1", 1, 120, 0, 120, 48]); // ID 冲突
  rows.push(["R-UNK-STORE", "2026-09-10", "ZZ99", "堂食自提", "P1", 1, 30, 0, 30, 12]); // 未知门店
  rows.push(["R-UNK-PROD", "2026-09-10", "A01", "堂食自提", "P9", 1, 40, 0, 40, 16]); // 未知商品
  // 金额对不上：原价 120 − 折扣 5 ≠ 实收 110，以实收为准
  const mismatchAt = rows.findIndex((r) => r[0] === rid("A01", "2026-09-11"));
  rows[mismatchAt] = [rid("A01", "2026-09-11"), "2026-09-11", "A01", "堂食自提", "P1", 1, 120, 5, 110, 44];
  rows.push(["R-RETURN", "2026-09-12", "A02", "堂食自提", "P1", -1, -10, 0, -10, -4]); // 退货
  return rows;
}

async function buildXlsx(path: string, opts: { sheets?: boolean } = {}) {
  const wb = new ExcelJS.Workbook();
  const sales = wb.addWorksheet("销售明细");
  sales.addRow(["记录ID", "销售日期", "门店编码", "门店名称", "渠道", "商品编码", "商品名称", "销售数量", "原价金额", "折扣金额", "实收金额", "成本金额"]);
  for (const r of buildRows()) sales.addRow([r[0], r[1], r[2], r[2], r[3], r[4], r[4], r[5], r[6], r[7], r[8], r[9]]);
  if (opts.sheets !== false) {
    const st = wb.addWorksheet("门店信息");
    st.addRow(["门店编码", "门店名称", "区域", "城市", "店型", "面积(㎡)", "开业日期"]);
    st.addRow(["A01", "甲店", "华东", "上海", "商场店", 100, "2020-01-01"]);
    st.addRow(["A02", "乙店", "华东", "上海", "街边店", 80, "2026-09-03"]);
    const pr = wb.addWorksheet("商品信息");
    pr.addRow(["商品编码", "商品名称", "品类", "规格", "零售单价(元)", "单位成本(元)"]);
    pr.addRow(["P1", "拿铁", "饮品", "杯", 10, 4]);
    pr.addRow(["P2", "吐司", "面包", "条", 10, 4]);
    const tg = wb.addWorksheet("销售目标");
    tg.addRow(["区域", "年份", "周次", "周起始日", "周结束日", "实收目标(元)"]);
    tg.addRow(["华东", 2026, "W36", "2026-08-31", "2026-09-06", 1000]);
    tg.addRow(["华东", 2026, "W37", "2026-09-07", "2026-09-13", 1200]);
  }
  await wb.xlsx.writeFile(path);
}

let h: Harness;
let datasetPath: string;
beforeAll(async () => {
  h = await connect();
  const dir = mkdtempSync(join(tmpdir(), "de-rules-"));
  const xlsx = join(dir, "销售明细.xlsx");
  await buildXlsx(xlsx);
  datasetPath = (await h.call("load_sales", { salesPath: xlsx, outDir: dir })).datasetPath;
});
afterAll(async () => {
  await h.close();
});

const compute = (week: number, anomalyThreshold = 0.1) =>
  h.call<WeeklyFacts>("compute_metrics", { datasetPath, period: { year: 2026, week }, anomalyThreshold });

describe("compute_metrics 口径规则", () => {
  test("重复记录只保留第一条；内容不一致的记 ID 冲突，也不计入合计", async () => {
    const f = await compute(37);
    expect(f.quality.duplicates).toEqual({
      count: 1,
      revenueCents: 11000,
      groups: [{ store: "A01", date: "2026-09-08", count: 1, revenueCents: 11000 }],
    });
    expect(f.quality.idConflicts).toEqual({ count: 1, recordIds: [rid("A01", "2026-09-09")] });
    // A01 周合计仍是 770 元，重复与冲突行都没有进入
    expect(f.stores.find((s) => s.code === "A01")!.revenueCents).toBe(77000);
  });

  test("实收 ≠ 原价 − 折扣：以实收为准，并报告条数", async () => {
    const f = await compute(37);
    expect(f.quality.amountMismatch.count).toBe(1);
    expect(f.stores.find((s) => s.code === "A01")!.revenueCents).toBe(77000);
  });

  test("门店或商品编码查不到：不计入任何合计，单独报告条数和金额", async () => {
    const f = await compute(37);
    expect(f.quality.unknownCodes).toEqual({ count: 2, revenueCents: 7000 });
    expect(f.company.revenueCents).toBe(77000 + 29000); // A01 + A02（A02 含 -10 元退货）
  });

  test("退货照常冲减并报告笔数和合计；具名退货按门店+日期+单品分组", async () => {
    const f = await compute(37);
    expect(f.quality.returns).toEqual({
      count: 1,
      revenueCents: -1000,
      groups: [
        { store: "A02", storeName: "乙店", date: "2026-09-12", product: "P1", productName: "拿铁", count: 1, revenueCents: -1000 },
      ],
    });
    expect(f.stores.find((s) => s.code === "A02")!.revenueCents).toBe(29000);
  });

  test("早于开业日是 not_open，应营业但无数据是 no_data；覆盖范围外的日期不计", async () => {
    const w36 = await compute(36);
    expect(w36.quality.storeDays).toEqual([
      { store: "A02", date: "2026-08-31", status: "not_open" },
      { store: "A02", date: "2026-09-01", status: "not_open" },
      { store: "A02", date: "2026-09-02", status: "not_open" },
    ]);
    const w37 = await compute(37);
    expect(w37.quality.storeDays).toEqual([{ store: "A02", date: "2026-09-13", status: "no_data" }]);
  });

  test("异常严格大于阈值：恰好 +10.0% 不算，阈值略低则算", async () => {
    const at = await compute(37, 0.1);
    expect(at.stores.find((s) => s.code === "A01")!.wow).toBe(0.1);
    expect(at.anomalies.some((a) => a.id === "store:A01")).toBe(false);
    const below = await compute(37, 0.099);
    expect(below.anomalies.some((a) => a.id === "store:A01")).toBe(true);
  });

  test("上周有缺数日（含未开业以外的 no_data）才标基数偏低", async () => {
    const f = await compute(37);
    // A02 环比 = 290 / 200 - 1；上周只有 not_open，没有 no_data
    const a02 = f.anomalies.find((a) => a.id === "store:A02")!;
    expect(a02.baseWeekHasMissingDays).toBe(false);
    expect(f.quality.prevWeekStoreDays.every((d) => d.status === "not_open")).toBe(true);
  });

  test("目标达成率、毛利率、外卖占比来自数据", async () => {
    const f = await compute(37);
    expect(f.regions["华东"]!.targetCents).toBe(120000);
    expect(f.company.targetCents).toBe(120000);
    expect(f.regions["华东"]!.achievement).toBeCloseTo(106000 / 120000, 10);
    expect(f.company.deliveryShare).toBe(0);
    expect(f.company.grossMarginRate).toBeCloseTo(0.6, 10);
  });

  test("品类：按商品表归类，环比基于上周", async () => {
    const f = await compute(37);
    const drinks = f.categories.find((c) => c.category === "饮品")!;
    expect(drinks.revenueCents).toBe(106000);
    expect(drinks.prevRevenueCents).toBe(90000);
    expect(f.categories.find((c) => c.category === "面包")!.revenueCents).toBe(0);
  });

  test("只有 CSV（没有维表）的数据集不能算指标", async () => {
    const dir = mkdtempSync(join(tmpdir(), "de-rules-csv-"));
    const xlsx = join(dir, "无维表.xlsx");
    await buildXlsx(xlsx, { sheets: false });
    const ds = await h.call("load_sales", { salesPath: xlsx, outDir: dir });
    const r = await h.raw("compute_metrics", {
      datasetPath: ds.datasetPath,
      period: { year: 2026, week: 37 },
      anomalyThreshold: 0.1,
    });
    expect(r.isError).toBe(true);
    expect(r.content[0].text).toContain("维表");
  });
});
