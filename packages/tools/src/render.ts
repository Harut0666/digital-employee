// render_report_docx：用 docx 生成 Word（标题、章节、核心指标表、图表），按版式估算页数。
import {
  AlignmentType,
  BorderStyle,
  Document,
  HeadingLevel,
  ImageRun,
  Packer,
  Paragraph,
  ShadingType,
  Table,
  TableCell,
  TableRow,
  TextRun,
  WidthType,
} from "docx";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import type { RenderReportInput, RenderReportOutput, RenderedReport, WeeklyFacts } from "@de/core/contract";
import { CHART_SIZE, regionBarOption, renderChartPng, trendLineOption, type FontOptions } from "./charts";
import { fmtPct, fmtWow, fmtYuan } from "./format";

export interface RenderOptions {
  /** 图表字体（见 charts.ts）。思源黑体子集打包后在这里传入 */
  chartFonts?: FontOptions;
  /** Word 正文字体名；不嵌入字体，由阅读端系统提供，缺失时 Word 自动回退 */
  docxFont?: string;
}

const CHART_COUNT = 2;
const DEFAULT_DOCX_FONT = "Microsoft YaHei";

// ---- 页数估算（A4，2cm 页边距，五号字 10.5pt，1.5 倍行距）----
const PAGE_LINES = 46; // 每页可排的正文行数
const CHARS_PER_LINE = 45; // 每行汉字数
const CHART_LINES = 14; // 一张图（含图注）折合的行数

const textWidth = (s: string) => [...s].reduce((n, ch) => n + (ch.charCodeAt(0) < 0x2e80 ? 0.5 : 1), 0);
const linesOf = (s: string, indent = 0) => Math.max(1, Math.ceil((textWidth(s) + indent) / CHARS_PER_LINE));

export function estimatePages(report: RenderedReport, facts: WeeklyFacts): number {
  let lines = 3; // 标题与副标题
  const tableRows = 1 + 1 + Object.keys(facts.regions).length;
  for (const [i, s] of report.sections.entries()) {
    lines += 2.2 + 0.3; // 章节标题（三号/加粗）与段前后间距
    for (const p of s.paragraphs) lines += linesOf(p, 2) + 0.4;
    for (const b of s.bullets ?? []) lines += linesOf(b, 2) + 0.2;
    if (i === coreSectionIndex(report)) lines += tableRows * 1.6 + 2 + CHART_COUNT * CHART_LINES;
  }
  if (report.sections.length === 0) lines += tableRows * 1.6 + 2 + CHART_COUNT * CHART_LINES;
  return Math.max(1, Math.ceil(lines / PAGE_LINES));
}

/** 核心指标表与图表放在哪个章节之后：优先标题含“指标”的章节，否则第一章 */
function coreSectionIndex(report: RenderedReport): number {
  const i = report.sections.findIndex((s) => /指标|metrics/i.test(s.heading + s.id));
  return i >= 0 ? i : 0;
}

// ---- 文档构建 ----
const thin = { style: BorderStyle.SINGLE, size: 4, color: "BFBFBF" };
const borders = { top: thin, bottom: thin, left: thin, right: thin };

function cell(text: string, font: string, opts: { bold?: boolean; header?: boolean; right?: boolean; width: number }) {
  return new TableCell({
    width: { size: opts.width, type: WidthType.PERCENTAGE },
    borders,
    margins: { top: 50, bottom: 50, left: 100, right: 100 },
    shading: opts.header ? { type: ShadingType.CLEAR, fill: "EAF0FB", color: "auto" } : undefined,
    children: [
      new Paragraph({
        alignment: opts.right ? AlignmentType.RIGHT : AlignmentType.LEFT,
        children: [new TextRun({ text, bold: opts.bold || opts.header, size: 20, font: { eastAsia: font, ascii: font, hAnsi: font } })],
      }),
    ],
  });
}

function metricsTable(f: WeeklyFacts, font: string): Table {
  const widths = [20, 22, 18, 20, 20];
  const head = ["范围", "实收（元）", "环比", "毛利率", "目标达成率"];
  const rowOf = (name: string, m: WeeklyFacts["company"], bold = false) =>
    new TableRow({
      children: [
        cell(name, font, { bold, width: widths[0]! }),
        cell(fmtYuan(m.revenueCents), font, { bold, right: true, width: widths[1]! }),
        cell(fmtWow(m.wow), font, { bold, right: true, width: widths[2]! }),
        cell(fmtPct(m.grossMarginRate), font, { bold, right: true, width: widths[3]! }),
        cell(fmtPct(m.achievement), font, { bold, right: true, width: widths[4]! }),
      ],
    });
  return new Table({
    width: { size: 100, type: WidthType.PERCENTAGE },
    rows: [
      new TableRow({ tableHeader: true, children: head.map((t, i) => cell(t, font, { header: true, right: i > 0, width: widths[i]! })) }),
      rowOf("全公司", f.company, true),
      ...Object.entries(f.regions).map(([name, m]) => rowOf(name, m)),
    ],
  });
}

async function buildDocx(input: RenderReportInput, opts: RenderOptions): Promise<Uint8Array> {
  const { report, facts } = input;
  const font = opts.docxFont ?? DEFAULT_DOCX_FONT;
  const fontSpec = { eastAsia: font, ascii: font, hAnsi: font };
  const run = (text: string, extra: { bold?: boolean; size?: number } = {}) => new TextRun({ text, font: fontSpec, size: 21, ...extra });

  const [bar, trend] = await Promise.all([
    renderChartPng(regionBarOption(facts), opts.chartFonts),
    renderChartPng(trendLineOption(facts), opts.chartFonts),
  ]);
  // 图宽 16cm 左右（约 600px @96dpi），按图表比例定高
  const imgW = 600;
  const imgH = Math.round((imgW * CHART_SIZE.height) / CHART_SIZE.width);
  const image = (data: Uint8Array, name: string) =>
    new Paragraph({
      alignment: AlignmentType.CENTER,
      spacing: { before: 60, after: 60 },
      children: [
        new ImageRun({
          type: "png",
          data,
          transformation: { width: imgW, height: imgH },
          altText: { name, title: name, description: name },
        }),
      ],
    });

  const core = coreSectionIndex(report);
  const children: (Paragraph | Table)[] = [
    new Paragraph({
      heading: HeadingLevel.TITLE,
      alignment: AlignmentType.CENTER,
      spacing: { after: 160 },
      children: [run(report.title, { bold: true, size: 36 })],
    }),
  ];
  const addCore = () => {
    children.push(
      new Paragraph({ spacing: { before: 120, after: 80 }, children: [run("核心指标", { bold: true, size: 24 })] }),
      metricsTable(facts, font),
      image(bar, "各区域实收对比图"),
      image(trend, "近 4 周实收趋势图"),
    );
  };
  report.sections.forEach((s, i) => {
    children.push(
      new Paragraph({
        heading: HeadingLevel.HEADING_1,
        spacing: { before: 200, after: 80 },
        children: [run(s.heading, { bold: true, size: 28 })],
      }),
    );
    for (const p of s.paragraphs) {
      children.push(new Paragraph({ spacing: { after: 80, line: 360 }, indent: { firstLine: 420 }, children: [run(p)] }));
    }
    for (const b of s.bullets ?? []) {
      children.push(new Paragraph({ bullet: { level: 0 }, spacing: { after: 40, line: 340 }, children: [run(b)] }));
    }
    if (i === core) addCore();
  });
  if (report.sections.length === 0) addCore();

  const doc = new Document({
    creator: "数字员工",
    title: report.title,
    styles: { default: { document: { run: { font: fontSpec, size: 21 } } } },
    sections: [
      {
        properties: { page: { size: { width: 11906, height: 16838 }, margin: { top: 1134, bottom: 1134, left: 1134, right: 1134 } } },
        children,
      },
    ],
  });
  return new Uint8Array(await Packer.toBuffer(doc));
}

const safeName = (key: string) => key.replace(/[^\p{L}\p{N}_.-]+/gu, "_").slice(0, 60);

export async function renderReportDocx(input: RenderReportInput, opts: RenderOptions = {}): Promise<RenderReportOutput> {
  const hash = createHash("sha256").update(input.idempotencyKey).digest("hex").slice(0, 12);
  const docxPath = join(input.outDir, `report-${safeName(input.idempotencyKey)}-${hash}.docx`);
  const estimatedPages = estimatePages(input.report, input.facts);
  // 幂等：同键文件已存在则直接返回，重试不产生重复文件
  if (!existsSync(docxPath)) {
    const bytes = await buildDocx(input, opts);
    mkdirSync(input.outDir, { recursive: true });
    const tmp = `${docxPath}.${process.pid}.tmp`;
    writeFileSync(tmp, bytes);
    renameSync(tmp, docxPath);
  }
  const sha256 = createHash("sha256").update(readFileSync(docxPath)).digest("hex");
  return { docxPath, sha256, estimatedPages, chartCount: CHART_COUNT };
}
