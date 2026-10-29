// 工具的 zod 入参/出参声明。与 @de/core/contract 的类型保持一致（底部有类型断言）。
import { z } from "zod";
import type {
  AdminSetFaultsInput,
  AdminSetFaultsOutput,
  ComputeMetricsInput,
  LoadSalesInput,
  LoadSalesOutput,
  ParseMinutesInput,
  ParseMinutesOutput,
  RenderReportInput,
  RenderReportOutput,
  ValidateSubmissionInput,
  ValidateSubmissionOutput,
  WeeklyFacts,
} from "@de/core/contract";

const isoWeek = z.object({ year: z.number().int(), week: z.number().int().min(1).max(53) });
const dateRange = z.object({ from: z.string(), to: z.string() });
const attachment = z.object({ name: z.string(), path: z.string() });

// ---- validate_submission ----
export const validateSubmissionIn = {
  period: isoWeek,
  attachments: z.array(attachment),
  // 引擎把不存在的 $form 引用解析为 null（employee-config 契约），所以可选入参同时接受 null
  allowMissingMinutes: z.boolean().nullish(),
  allowPartialPeriod: z.boolean().nullish(),
};
export const validateSubmissionOut = {
  ok: z.boolean(),
  pause: z
    .object({
      code: z.string(),
      message: z.string(),
      detail: z.record(z.string(), z.unknown()).optional(),
    })
    .optional(),
  sales: z.object({ path: z.string(), kind: z.enum(["xlsx", "csv"]) }).optional(),
  dimensions: z.object({ stores: z.boolean(), products: z.boolean(), targets: z.boolean() }),
  minutes: z.array(z.object({ path: z.string(), week: isoWeek.nullable() })),
  coverage: dateRange.optional(),
};

// ---- load_sales ----
export const loadSalesIn = { salesPath: z.string(), outDir: z.string() };
export const loadSalesOut = { datasetPath: z.string(), rows: z.number().int(), coverage: dateRange };

// ---- compute_metrics ----
export const computeMetricsIn = {
  datasetPath: z.string(),
  period: isoWeek,
  anomalyThreshold: z.number().positive(),
};

const nullableNum = z.number().nullable();
const metric = z.object({
  revenueCents: z.number().int(),
  prevRevenueCents: z.number().int().nullable(),
  wow: nullableNum,
  grossMarginRate: nullableNum,
  targetCents: nullableNum,
  achievement: nullableNum,
  deliveryShare: nullableNum,
});
const storeType = z.enum(["商场店", "街边店", "社区店", "写字楼店"]);
const storeDay = z.object({ store: z.string(), date: z.string(), status: z.enum(["not_open", "no_data"]) });

export const computeMetricsOut = {
  period: z.object({ year: z.number().int(), week: z.number().int(), from: z.string(), to: z.string() }),
  dataCoverage: dateRange,
  company: metric,
  regions: z.record(z.string(), metric),
  stores: z.array(
    metric.extend({ code: z.string(), name: z.string(), region: z.string(), storeType }),
  ),
  categories: z.array(
    z.object({
      category: z.string(),
      revenueCents: z.number().int(),
      prevRevenueCents: z.number().int().nullable(),
      wow: nullableNum,
    }),
  ),
  topProducts: z.array(z.object({ code: z.string(), name: z.string(), revenueCents: z.number().int() })),
  anomalies: z.array(
    z.object({
      id: z.string(),
      level: z.enum(["region", "store"]),
      key: z.string(),
      name: z.string(),
      region: z.string(),
      storeType: storeType.optional(),
      wow: z.number(),
      revenueCents: z.number().int(),
      prevRevenueCents: z.number().int(),
      baseWeekHasMissingDays: z.boolean(),
    }),
  ),
  trend: z.array(
    z.object({
      week: isoWeek,
      companyCents: z.number().int(),
      regions: z.record(z.string(), z.number().int()),
    }),
  ),
  quality: z.object({
    duplicates: z.object({
      count: z.number().int(),
      revenueCents: z.number().int(),
      groups: z.array(
        z.object({
          store: z.string(),
          date: z.string(),
          count: z.number().int(),
          revenueCents: z.number().int(),
        }),
      ),
    }),
    idConflicts: z.object({ count: z.number().int(), recordIds: z.array(z.string()) }),
    amountMismatch: z.object({ count: z.number().int() }),
    unknownCodes: z.object({ count: z.number().int(), revenueCents: z.number().int() }),
    returns: z.object({ count: z.number().int(), revenueCents: z.number().int() }),
    storeDays: z.array(storeDay),
    prevWeekStoreDays: z.array(storeDay),
  }),
};

// ---- parse_minutes ----
export const parseMinutesIn = { paths: z.array(z.string()) };
export const parseMinutesOut = {
  minutes: z.array(
    z.object({
      path: z.string(),
      week: isoWeek.nullable(),
      title: z.string(),
      paragraphs: z.array(z.object({ id: z.string(), section: z.string().optional(), text: z.string() })),
    }),
  ),
};

// ---- render_report_docx ----
export const renderReportIn = {
  report: z.object({
    title: z.string(),
    sections: z.array(
      z.object({
        id: z.string(),
        heading: z.string(),
        paragraphs: z.array(z.string()),
        bullets: z.array(z.string()).optional(),
      }),
    ),
  }),
  facts: z.object(computeMetricsOut),
  outDir: z.string(),
  idempotencyKey: z.string().min(1),
};
export const renderReportOut = {
  docxPath: z.string(),
  sha256: z.string(),
  estimatedPages: z.number(),
  chartCount: z.number().int(),
};

// ---- web_search（只注册，调用即报错）----
export const webSearchIn = { query: z.string() };

// ---- admin_set_faults（管理工具，服务端下发，不开放给任何员工）----
export const faultMode = z.enum(["timeout", "error", "invalid_output"]);
export const adminSetFaultsIn = { tools: z.record(z.string(), faultMode) };
export const adminSetFaultsOut = { ok: z.boolean(), tools: z.record(z.string(), faultMode) };

// ---- 与契约类型的一致性断言（编译期）----
type Eq<A, B> = [A] extends [B] ? ([B] extends [A] ? true : false) : false;
const _assert = <T extends true>(): T | undefined => undefined;
type In<S extends z.ZodRawShape> = z.infer<z.ZodObject<S>>;
_assert<Eq<In<typeof loadSalesIn>, LoadSalesInput>>();
_assert<Eq<In<typeof loadSalesOut>, LoadSalesOutput>>();
_assert<Eq<In<typeof parseMinutesIn>, ParseMinutesInput>>();
_assert<Eq<In<typeof renderReportOut>, RenderReportOutput>>();
_assert<Assignable<In<typeof adminSetFaultsIn>, AdminSetFaultsInput>>();
_assert<Assignable<In<typeof adminSetFaultsOut>, AdminSetFaultsOutput>>();
// 其余类型含可选字段/联合，只检查单向可赋值，保证工具产出能被契约类型接收
type Assignable<A, B> = [A] extends [B] ? true : false;
_assert<Assignable<ComputeMetricsInput, In<typeof computeMetricsIn>>>();
_assert<Assignable<ValidateSubmissionInput, In<typeof validateSubmissionIn>>>();
_assert<Assignable<In<typeof computeMetricsOut>, WeeklyFacts>>();
_assert<Assignable<WeeklyFacts, In<typeof computeMetricsOut>>>();
_assert<Assignable<ParseMinutesOutput, In<typeof parseMinutesOut>>>();
_assert<Assignable<RenderReportInput, In<typeof renderReportIn>>>();
_assert<Assignable<ValidateSubmissionOutput, In<typeof validateSubmissionOut>>>();
