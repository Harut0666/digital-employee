// compute_metrics：按《周报格式要求》口径从清洗后的数据集算出 WeeklyFacts。金额全程为整数“分”。
import type {
  Anomaly,
  CategoryMetric,
  ComputeMetricsInput,
  DataQualityReport,
  IsoWeek,
  Metric,
  StoreDay,
  StoreMetric,
  StoreType,
  WeeklyFacts,
} from "@de/core/contract";
import { readDataset, type Dataset } from "./sales/dataset";
import { datesBetween, periodRange, shiftWeek } from "./week";

interface Totals {
  rev: number;
  cost: number;
  delivery: number;
  hasData: boolean;
}
const zero = (): Totals => ({ rev: 0, cost: 0, delivery: 0, hasData: false });

interface WeekAgg {
  byStore: Map<string, Totals>;
  byProduct: Map<string, number>;
  /** 本周有哪些“门店-日期”存在有效数据 */
  storeDates: Set<string>;
  hasData: boolean;
}

/** 汇总 [from, to] 的有效记录（已去重；编码查不到的不计入任何合计） */
function aggregate(ds: Dataset, from: string, to: string): WeekAgg {
  const agg: WeekAgg = { byStore: new Map(), byProduct: new Map(), storeDates: new Set(), hasData: false };
  for (const r of ds.rows) {
    if (r.unknown || r.date < from || r.date > to) continue;
    let t = agg.byStore.get(r.store);
    if (!t) agg.byStore.set(r.store, (t = zero()));
    t.rev += r.netCents;
    t.cost += r.costCents;
    if (r.channel === "外卖") t.delivery += r.netCents;
    t.hasData = true;
    agg.byProduct.set(r.product, (agg.byProduct.get(r.product) ?? 0) + r.netCents);
    agg.storeDates.add(`${r.store}|${r.date}`);
    agg.hasData = true;
  }
  return agg;
}

function sumTotals(list: (Totals | undefined)[]): Totals {
  const t = zero();
  for (const x of list) {
    if (!x) continue;
    t.rev += x.rev;
    t.cost += x.cost;
    t.delivery += x.delivery;
    t.hasData ||= x.hasData;
  }
  return t;
}

function metric(cur: Totals, prev: Totals, prevExists: boolean, targetCents: number | null): Metric {
  const hasRev = cur.rev > 0;
  const prevRevenueCents = prevExists ? prev.rev : null;
  return {
    revenueCents: cur.rev,
    prevRevenueCents,
    wow: prevRevenueCents !== null && prevRevenueCents !== 0 ? (cur.rev - prevRevenueCents) / prevRevenueCents : null,
    grossMarginRate: hasRev ? (cur.rev - cur.cost) / cur.rev : null,
    targetCents,
    achievement: targetCents ? cur.rev / targetCents : null,
    deliveryShare: hasRev ? cur.delivery / cur.rev : null,
  };
}

function storeDays(ds: Dataset, agg: WeekAgg, from: string, to: string): StoreDay[] {
  // 只判断数据覆盖范围内的日期；覆盖范围之外不是“无数据”，而是“超出范围”
  const lo = from > ds.coverage.from ? from : ds.coverage.from;
  const hi = to < ds.coverage.to ? to : ds.coverage.to;
  if (lo > hi) return [];
  const out: StoreDay[] = [];
  for (const s of ds.stores ?? []) {
    for (const d of datesBetween(lo, hi)) {
      if (agg.storeDates.has(`${s.code}|${d}`)) continue;
      out.push({ store: s.code, date: d, status: d < s.openDate ? "not_open" : "no_data" });
    }
  }
  return out;
}

export function computeMetrics(input: ComputeMetricsInput): WeeklyFacts {
  const ds = readDataset(input.datasetPath);
  if (!ds.stores?.length || !ds.products?.length || !ds.targets?.length) {
    throw new Error("数据集缺少门店信息、商品信息或销售目标维表，无法按区域、品类和目标统计（只有 CSV 的数据集不能用于周报）");
  }
  const period = periodRange(input.period);
  if (period.to < ds.coverage.from || period.from > ds.coverage.to) {
    throw new Error(
      `统计周期 ${period.from} 至 ${period.to} 超出数据覆盖范围（${ds.coverage.from} 至 ${ds.coverage.to}）`,
    );
  }
  const prevWeek = shiftWeek(input.period, -1);
  const prevRange = periodRange(prevWeek);

  const cur = aggregate(ds, period.from, period.to);
  const prev = aggregate(ds, prevRange.from, prevRange.to);
  const prevExists = prev.hasData;

  const regionOrder: string[] = [];
  for (const s of ds.stores) if (!regionOrder.includes(s.region)) regionOrder.push(s.region);
  const storesOfRegion = (region: string) => ds.stores!.filter((s) => s.region === region);
  const targetOf = (region: string, w: IsoWeek): number | null =>
    ds.targets!.find((t) => t.region === region && t.year === w.year && t.week === w.week)?.targetCents ?? null;

  const regionTotals = (agg: WeekAgg, region: string) =>
    sumTotals(storesOfRegion(region).map((s) => agg.byStore.get(s.code)));

  // 区域与全公司
  const regions: Record<string, Metric> = {};
  const regionTargets: (number | null)[] = [];
  for (const region of regionOrder) {
    const target = targetOf(region, input.period);
    regionTargets.push(target);
    regions[region] = metric(regionTotals(cur, region), regionTotals(prev, region), prevExists, target);
  }
  const companyTarget = regionTargets.every((t) => t !== null)
    ? (regionTargets as number[]).reduce((a, b) => a + b, 0)
    : null;
  const sumAll = (agg: WeekAgg) => sumTotals(ds.stores!.map((s) => agg.byStore.get(s.code)));
  const company = metric(sumAll(cur), sumAll(prev), prevExists, companyTarget);

  // 门店
  const stores: StoreMetric[] = ds.stores.map((s) => {
    const c = cur.byStore.get(s.code) ?? zero();
    const p = prev.byStore.get(s.code) ?? zero();
    return {
      code: s.code,
      name: s.name,
      region: s.region,
      storeType: s.storeType as StoreType,
      ...metric(c, p, p.hasData, null),
    };
  });

  // 品类与单品
  const categoryOf = new Map(ds.products.map((p) => [p.code, p.category]));
  const categoryRev = (agg: WeekAgg) => {
    const m = new Map<string, number>();
    for (const [code, rev] of agg.byProduct) {
      const c = categoryOf.get(code);
      if (c) m.set(c, (m.get(c) ?? 0) + rev);
    }
    return m;
  };
  const curCat = categoryRev(cur);
  const prevCat = categoryRev(prev);
  const categories: CategoryMetric[] = [];
  for (const p of ds.products) {
    if (categories.some((c) => c.category === p.category)) continue;
    const revenueCents = curCat.get(p.category) ?? 0;
    const prevRevenueCents = prevExists ? (prevCat.get(p.category) ?? 0) : null;
    categories.push({
      category: p.category,
      revenueCents,
      prevRevenueCents,
      wow: prevRevenueCents ? (revenueCents - prevRevenueCents) / prevRevenueCents : null,
    });
  }
  const nameOf = new Map(ds.products.map((p) => [p.code, p.name]));
  const topProducts = [...cur.byProduct]
    .filter(([code]) => nameOf.has(code))
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .slice(0, 5)
    .map(([code, revenueCents]) => ({ code, name: nameOf.get(code)!, revenueCents }));

  // 数据质量
  const inPeriod = (d: string) => d >= period.from && d <= period.to;
  const removedIn = ds.removed.filter((r) => inPeriod(r.date));
  const dups = removedIn.filter((r) => !r.conflict);
  const groupMap = new Map<string, { store: string; date: string; count: number; revenueCents: number }>();
  for (const r of dups) {
    const k = `${r.date}|${r.store}`;
    const g = groupMap.get(k) ?? { store: r.store, date: r.date, count: 0, revenueCents: 0 };
    g.count += 1;
    g.revenueCents += r.netCents;
    groupMap.set(k, g);
  }
  const keptIn = ds.rows.filter((r) => inPeriod(r.date));
  const unknown = keptIn.filter((r) => r.unknown);
  const returns = keptIn.filter((r) => !r.unknown && r.qty < 0);
  const conflictIds = [...new Set(removedIn.filter((r) => r.conflict).map((r) => r.id))];
  const curDays = storeDays(ds, cur, period.from, period.to);
  const prevDays = storeDays(ds, prev, prevRange.from, prevRange.to);
  const quality: DataQualityReport = {
    duplicates: {
      count: dups.length,
      revenueCents: dups.reduce((a, r) => a + r.netCents, 0),
      groups: [...groupMap.values()].sort((a, b) => a.date.localeCompare(b.date) || a.store.localeCompare(b.store)),
    },
    idConflicts: { count: conflictIds.length, recordIds: conflictIds },
    amountMismatch: { count: keptIn.filter((r) => r.mismatch).length },
    unknownCodes: { count: unknown.length, revenueCents: unknown.reduce((a, r) => a + r.netCents, 0) },
    returns: { count: returns.length, revenueCents: returns.reduce((a, r) => a + r.netCents, 0) },
    storeDays: curDays,
    prevWeekStoreDays: prevDays,
  };

  // 异常：用未取整的环比，严格大于阈值
  const missingPrev = (pred: (store: string) => boolean) =>
    prevDays.some((d) => d.status === "no_data" && pred(d.store));
  const regionOfStore = new Map(ds.stores.map((s) => [s.code, s.region]));
  const anomalies: Anomaly[] = [];
  for (const region of regionOrder) {
    const m = regions[region]!;
    if (m.wow !== null && Math.abs(m.wow) > input.anomalyThreshold) {
      anomalies.push({
        id: `region:${region}`,
        level: "region",
        key: region,
        name: region,
        region,
        wow: m.wow,
        revenueCents: m.revenueCents,
        prevRevenueCents: m.prevRevenueCents!,
        baseWeekHasMissingDays: missingPrev((s) => regionOfStore.get(s) === region),
      });
    }
  }
  for (const s of stores) {
    if (s.wow !== null && Math.abs(s.wow) > input.anomalyThreshold) {
      anomalies.push({
        id: `store:${s.code}`,
        level: "store",
        key: s.code,
        name: s.name,
        region: s.region,
        storeType: s.storeType,
        wow: s.wow,
        revenueCents: s.revenueCents,
        prevRevenueCents: s.prevRevenueCents!,
        baseWeekHasMissingDays: missingPrev((code) => code === s.code),
      });
    }
  }

  // 近 4 周趋势（含本周；没有数据的周不画）
  const trend: WeeklyFacts["trend"] = [];
  for (let k = 3; k >= 0; k--) {
    const week = shiftWeek(input.period, -k);
    const r = periodRange(week);
    const agg = k === 0 ? cur : k === 1 ? prev : aggregate(ds, r.from, r.to);
    if (!agg.hasData) continue;
    trend.push({
      week,
      companyCents: sumAll(agg).rev,
      regions: Object.fromEntries(regionOrder.map((region) => [region, regionTotals(agg, region).rev])),
    });
  }

  return {
    period,
    dataCoverage: ds.coverage,
    company,
    regions,
    stores,
    categories,
    topProducts,
    anomalies,
    trend,
    quality,
  };
}
