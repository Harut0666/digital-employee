// validate_submission：按 design.md §10.1 的顺序，只报最根本的一个原因。
import { extname } from "node:path";
import type {
  AttachmentRef,
  IsoWeek,
  MinutesDetection,
  ValidateSubmissionInput,
  ValidateSubmissionOutput,
} from "@de/core/contract";
import { parseMinutesFile } from "./minutes";
import { coverageOf, readSalesSource, type SalesSource } from "./sales/source";
import { periodRange } from "./week";

type Pause = NonNullable<ValidateSubmissionOutput["pause"]>;

const weekLabel = (w: IsoWeek) => `${w.year} 年第 ${w.week} 周`;

function pickSales(files: AttachmentRef[]): { ref: AttachmentRef; kind: "xlsx" | "csv" } | null {
  const kindOf = (f: AttachmentRef): "xlsx" | "csv" | null => {
    const ext = extname(f.name || f.path).toLowerCase();
    return ext === ".xlsx" ? "xlsx" : ext === ".csv" ? "csv" : null;
  };
  const cands = files.flatMap((ref) => {
    const kind = kindOf(ref);
    return kind ? [{ ref, kind }] : [];
  });
  // 同时给了 xlsx 与 csv 时优先 xlsx（含维表）
  return cands.find((c) => c.kind === "xlsx") ?? cands[0] ?? null;
}

function dimensionsOf(src: SalesSource | null) {
  return { stores: !!src?.stores?.length, products: !!src?.products?.length, targets: !!src?.targets?.length };
}

export async function validateSubmission(input: ValidateSubmissionInput): Promise<ValidateSubmissionOutput> {
  const { period, attachments } = input;
  const range = periodRange(period);

  // 纪要始终先识别：即使最终因别的原因暂停，也把识别结果交给调用方
  const minutes: MinutesDetection[] = [];
  for (const ref of attachments.filter((a) => extname(a.name || a.path).toLowerCase() === ".docx")) {
    try {
      const parsed = await parseMinutesFile(ref.path);
      minutes.push({ path: ref.path, week: parsed.week });
    } catch {
      minutes.push({ path: ref.path, week: null });
    }
  }

  const pick = pickSales(attachments);
  const fail = (pause: Pause, extra: Partial<ValidateSubmissionOutput> = {}): ValidateSubmissionOutput => ({
    ok: false,
    pause,
    dimensions: { stores: false, products: false, targets: false },
    minutes,
    ...extra,
  });

  // 3. 缺销售明细（没有数据就无从判断覆盖范围，所以先于 1、2 处理）
  if (!pick) {
    return fail({
      code: "missing_sales",
      message: "缺少销售明细：未找到 xlsx 或 csv 格式的销售数据，无法生成周报。请上传《销售明细.xlsx》后继续。",
      detail: { missing: ["销售明细"] },
    });
  }

  let src: SalesSource;
  try {
    src = await readSalesSource(pick.ref.path);
  } catch (e) {
    return fail({
      code: "invalid_input",
      message: `销售数据文件无法读取（${pick.ref.name}）：${(e as Error).message}。请确认文件未损坏且包含“销售明细”表后重新上传。`,
    });
  }
  const sales = { path: pick.ref.path, kind: pick.kind };
  const dimensions = dimensionsOf(src);
  const coverage = coverageOf(src.sales);
  const base = { sales, dimensions, minutes, ...(coverage ? { coverage } : {}) };
  if (!coverage) {
    return fail(
      { code: "invalid_input", message: `销售数据文件（${pick.ref.name}）中没有可识别的销售日期，请检查文件内容。` },
      base,
    );
  }

  // 1. 周次超出数据范围
  if (range.to < coverage.from || range.from > coverage.to) {
    return fail(
      {
        code: "period_out_of_range",
        message: `所选周期（${weekLabel(period)}，${range.from} 至 ${range.to}）超出销售数据覆盖范围：数据实际覆盖 ${coverage.from} 至 ${coverage.to}。请改选范围内的周次，或补充对应日期的销售数据。`,
        detail: { period: range, coverage },
      },
      base,
    );
  }

  // 2. 周次不完整（数据在周中开始或截止）
  if (!input.allowPartialPeriod && (coverage.to < range.to || coverage.from > range.from)) {
    const from = coverage.from > range.from ? coverage.from : range.from;
    const to = coverage.to < range.to ? coverage.to : range.to;
    return fail(
      {
        code: "period_incomplete",
        message: `${weekLabel(period)}（${range.from} 至 ${range.to}）的销售数据不完整：数据只覆盖 ${from} 至 ${to}。可补充缺失日期的数据，或选择“按已有数据继续”（周报首页将标注“数据不完整”）。`,
        detail: { period: range, coverage, available: { from, to } },
      },
      base,
    );
  }

  // 4. 缺维表（只传了 CSV，或 xlsx 缺工作表）
  const missingDims = [
    !dimensions.stores && "门店信息",
    !dimensions.products && "商品信息",
    !dimensions.targets && "销售目标",
  ].filter(Boolean) as string[];
  if (missingDims.length > 0) {
    const reason =
      pick.kind === "csv"
        ? "只上传了 CSV，其中不含门店信息、商品信息和销售目标"
        : "销售明细文件中缺少相应工作表";
    return fail(
      {
        code: "missing_dimensions",
        message: `缺少维表：${missingDims.join("、")}。${reason}，无法按区域、品类和目标达成率统计。请上传完整的《销售明细.xlsx》（含 4 个工作表）。`,
        detail: { missing: missingDims },
      },
      base,
    );
  }

  // 5. 缺本周纪要，或纪要周次不符
  const hasThisWeek = minutes.some((m) => m.week && m.week.year === period.year && m.week.week === period.week);
  if (!hasThisWeek && !input.allowMissingMinutes) {
    if (minutes.length === 0) {
      return fail(
        {
          code: "minutes_missing",
          message: `缺少 ${weekLabel(period)} 的周会纪要。可补充纪要，或选择“无纪要继续”（异常原因将一律写“原因待核实”）。`,
          detail: { period },
        },
        base,
      );
    }
    const found = minutes.map((m) => (m.week ? weekLabel(m.week) : "周次无法识别"));
    return fail(
      {
        code: "minutes_week_mismatch",
        message: `上传的纪要周次与统计周期不符：统计 ${weekLabel(period)}，但纪要标题显示为 ${found.join("、")}。请上传 ${weekLabel(period)} 的纪要，或选择“无纪要继续”。`,
        detail: { period, found: minutes.map((m) => m.week) },
      },
      base,
    );
  }

  return { ok: true, ...base };
}
