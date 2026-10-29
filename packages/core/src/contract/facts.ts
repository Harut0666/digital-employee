// 场景 A 的事实（docs/design.md §9、§10）。金额一律为整数“分”，比例为小数（0.123 = 12.3%）。
// 交付物中的每个数字都必须来自这里，模型只能用槽位引用（ADR-0002）。

export interface IsoWeek {
  year: number;
  week: number;
}

export interface PeriodRange extends IsoWeek {
  /** YYYY-MM-DD，周一 */
  from: string;
  /** YYYY-MM-DD，周日 */
  to: string;
}

export interface Metric {
  revenueCents: number;
  /** 上周实收；无上周数据时为 null（如第 36 周） */
  prevRevenueCents: number | null;
  /** 环比，未取整；不可比时为 null */
  wow: number | null;
  grossMarginRate: number | null;
  targetCents: number | null;
  achievement: number | null;
  deliveryShare: number | null;
}

export type StoreType = "商场店" | "街边店" | "社区店" | "写字楼店";

export interface StoreMetric extends Metric {
  code: string;
  name: string;
  region: string;
  storeType: StoreType;
}

export interface CategoryMetric {
  category: string;
  revenueCents: number;
  prevRevenueCents: number | null;
  wow: number | null;
}

export interface Anomaly {
  /** 稳定 ID，如 "region:华东"、"store:HZ01" */
  id: string;
  level: "region" | "store";
  key: string;
  name: string;
  region: string;
  storeType?: StoreType;
  wow: number;
  revenueCents: number;
  prevRevenueCents: number;
  /** 上周存在数据缺失日，环比基数偏低 */
  baseWeekHasMissingDays: boolean;
}

export interface StoreDay {
  store: string;
  date: string;
  /** not_open：早于开业日；no_data：应营业但无数据（停业或缺失由判断步骤结合纪要再定） */
  status: "not_open" | "no_data";
}

export interface DataQualityReport {
  duplicates: { count: number; revenueCents: number; groups: { store: string; date: string; count: number; revenueCents: number }[] };
  idConflicts: { count: number; recordIds: string[] };
  amountMismatch: { count: number };
  unknownCodes: { count: number; revenueCents: number };
  returns: { count: number; revenueCents: number };
  storeDays: StoreDay[];
  /** 上周（环比基数周）的无数据门店日 */
  prevWeekStoreDays: StoreDay[];
}

export interface WeeklyFacts {
  period: PeriodRange;
  dataCoverage: { from: string; to: string };
  company: Metric;
  regions: Record<string, Metric>;
  stores: StoreMetric[];
  categories: CategoryMetric[];
  topProducts: { code: string; name: string; revenueCents: number }[];
  anomalies: Anomaly[];
  /** 近 4 周全公司与各区域实收，供趋势图 */
  trend: { week: IsoWeek; companyCents: number; regions: Record<string, number> }[];
  quality: DataQualityReport;
}

// 槽位语法：{{path}}，path 为 WeeklyFacts 上的点路径，数组用业务键取值：
//   {{company.revenue}}  {{company.wow}}  {{regions.华东.achievement}}
//   {{stores.HZ01.wow}}  {{categories.饮品.wow}}  {{quality.returns.count}}
// 渲染规则由字段名后缀决定，见 SLOT_FORMATS。
export type SlotFormat = "yuan" | "percent1" | "count";

export const SLOT_FORMATS: Record<string, SlotFormat> = {
  revenue: "yuan",
  revenueCents: "yuan",
  prevRevenueCents: "yuan",
  targetCents: "yuan",
  wow: "percent1",
  grossMarginRate: "percent1",
  achievement: "percent1",
  deliveryShare: "percent1",
  count: "count",
};
