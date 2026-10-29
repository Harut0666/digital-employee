// de fixtures：从公开样例 samples/scenario-a/销售明细.xlsx 生成变异数据，放到 samples/fixtures/。
//
// 变异只能由脚本生成，保证可复现：同样的输入连续跑两次，连字节都一样（zip 条目的时间戳与文档属性都固定）。
// 生成物入库；manifest.json 记下每份夹具的大小、sha256 与结构，改动生成器后重跑即可，入库的旧文件会被覆盖。
import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { basename, dirname, isAbsolute, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import ExcelJS from "exceljs";
import JSZip from "jszip";

/** 写 xlsx 时统一使用的时间戳：exceljs/JSZip 默认取“现在”，会让同样的数据每次写出不同字节。 */
const FIXED_DATE = new Date("2026-01-01T00:00:00.000Z");

/** 损坏夹具保留的字节数上限：zip 尾部的中央目录被截掉，xlsx 读取器会报 “can't find end of central directory”。 */
export const CORRUPT_BYTES = 64 * 1024;

/** 截断的字节数：大文件留前 64KB，小文件（如测试用的合成数据包）留一半，保证一定读不出来。 */
function truncateForCorruption(bytes: Buffer): Buffer {
  return bytes.subarray(0, Math.min(CORRUPT_BYTES, Math.floor(bytes.length / 2)));
}

const SHEETS = { sales: "销售明细", stores: "门店信息", products: "商品信息", targets: "销售目标" } as const;

const sha256 = (bytes: Buffer): string => createHash("sha256").update(bytes).digest("hex");

export interface FixtureSpec {
  /** 文件名（不含 .xlsx） */
  key: string;
  /** 变异了什么、用来测什么 */
  description: string;
  /** 在工作簿上做变异 */
  mutate?: (workbook: ExcelJS.Workbook) => void;
  /** 或者：直接截断源文件字节（损坏文件夹具，不做工作簿变异） */
  corrupt?: boolean;
}

export const FIXTURES: FixtureSpec[] = [
  {
    key: "销售明细-第44周周中截止",
    description: "删掉 2026-10-28 之后的销售记录，第 44 周数据在周中截止，用来测“周期不完整”暂停",
    mutate: (workbook) => cutSalesAfter(requireSheet(workbook, SHEETS.sales), "2026-10-28"),
  },
  {
    key: "销售明细-缺销售目标表",
    description: "删掉“销售目标”工作表，用来测缺维表暂停（目标达成率算不出来）",
    mutate: (workbook) => dropSheet(workbook, SHEETS.targets),
  },
  {
    key: "销售明细-缺实收金额列",
    description: "销售明细少“实收金额”一列，用来测缺列（实收全部读成 0）",
    mutate: (workbook) => dropColumn(requireSheet(workbook, SHEETS.sales), "实收金额"),
  },
  {
    key: "销售明细-损坏",
    description: "截掉尾部（前 64KB）的损坏 xlsx，用来测损坏文件（读不出来 → 准入不通过）",
    corrupt: true,
  },
  {
    key: "销售明细-含未知门店编码",
    description: "追加 3 行门店编码 XX99（门店信息表里没有）的销售记录，用来测编码不认识（不计入合计）",
    mutate: (workbook) => appendUnknownStoreRows(requireSheet(workbook, SHEETS.sales), { date: "2026-10-28", count: 3 }),
  },
  {
    key: "销售明细-记录ID冲突",
    description: "追加 3 行与 10/27 已有记录同 ID、实收金额不同的记录，用来测记录 ID 冲突（去重时记为冲突、不计入合计）",
    mutate: (workbook) => appendIdConflictRows(requireSheet(workbook, SHEETS.sales), { count: 3, amountDelta: 5, date: "2026-10-27" }),
  },
];

// ---- 读表小工具 ----

function requireSheet(workbook: ExcelJS.Workbook, name: string): ExcelJS.Worksheet {
  const sheet = workbook.getWorksheet(name);
  if (!sheet) throw new Error(`源数据里没有“${name}”工作表，夹具生成中止`);
  return sheet;
}

/** 表头文字（第 1 行，去空） */
function headerOf(sheet: ExcelJS.Worksheet): string[] {
  const values = (sheet.getRow(1).values as unknown[]) ?? [];
  return values.slice(1).map((value) => String(value ?? "").trim());
}

/** 按表头文字找列号（1 开始）；找不到就报错，避免生成“看起来像夹具”的废数据。 */
function columnOf(sheet: ExcelJS.Worksheet, header: string): number {
  const index = headerOf(sheet).indexOf(header);
  if (index < 0) throw new Error(`“${sheet.name}”表里没有“${header}”列，夹具生成中止`);
  return index + 1;
}

const dateOf = (value: ExcelJS.CellValue): string | null => {
  if (value === null || value === undefined) return null;
  if (value instanceof Date) return value.toISOString().slice(0, 10);
  const text = String(value).trim().slice(0, 10);
  return /^\d{4}-\d{2}-\d{2}$/.test(text) ? text : null;
};

const round2 = (value: number): number => Math.round(value * 100) / 100;

// ---- 变异 ----

/** 删掉“销售日期”晚于 cutoff 的行；返回删掉的行数。删完是空表也允许（那说明夹具没意义，由调用方判断）。 */
export function cutSalesAfter(sheet: ExcelJS.Worksheet, cutoff: string): number {
  const column = columnOf(sheet, "销售日期");
  const targets: number[] = [];
  for (let row = 2; row <= sheet.rowCount; row++) {
    const date = dateOf(sheet.getRow(row).getCell(column).value);
    if (date !== null && date > cutoff) targets.push(row);
  }
  if (targets.length === 0) throw new Error(`没有 ${cutoff} 之后的销售记录可删：源数据不是预期的范围`);
  // 逐行删（从后往前）：exceljs 的 spliceRows 一次删多行会留下尾部残行，一次删一行才是安全的；
  // 数据按日期排序，被删的是尾部一段，删除本身是常数开销。
  for (let i = targets.length - 1; i >= 0; i--) sheet.spliceRows(targets[i]!, 1);
  return targets.length;
}

/** 删掉一个工作表（比如“销售目标”）。 */
export function dropSheet(workbook: ExcelJS.Workbook, name: string): void {
  const sheet = requireSheet(workbook, name);
  workbook.removeWorksheet(sheet.id);
}

/** 删掉一列（按表头文字定位）。 */
export function dropColumn(sheet: ExcelJS.Worksheet, header: string): void {
  sheet.spliceColumns(columnOf(sheet, header), 1);
}

/**
 * 追加若干行“门店编码在门店信息表里查不到”的记录：其余字段照抄第一行数据，只换记录 ID、日期与门店。
 * 目的：让清洗步骤把它们标成 unknown（不计入任何合计），数据质量说明里报出数量。
 */
export function appendUnknownStoreRows(
  sheet: ExcelJS.Worksheet,
  options: { date: string; count: number; storeCode?: string; storeName?: string },
): string[] {
  const storeCode = options.storeCode ?? "XX99";
  const storeName = options.storeName ?? "未知门店";
  const storeColumn = columnOf(sheet, "门店编码");
  const idColumn = columnOf(sheet, "记录ID");
  const dateColumn = columnOf(sheet, "销售日期");
  const idIndex = idColumn - 1;
  const dateIndex = dateColumn - 1;
  const storeIndex = storeColumn - 1;
  for (let row = 2; row <= sheet.rowCount; row++) {
    if (String(sheet.getRow(row).getCell(storeColumn).value ?? "") === storeCode) {
      throw new Error(`门店编码 ${storeCode} 在源数据里已经存在，不能用来造“未知编码”夹具`);
    }
  }
  const header = headerOf(sheet);
  const template = sheet.getRow(2);
  const templateValues = header.map((_, column) => template.getCell(column + 1).value);
  const nameColumn = header.indexOf("门店名称");
  const datePart = options.date.replace(/-/g, "");
  const ids: string[] = [];
  for (let i = 0; i < options.count; i++) {
    const id = `R${datePart}${storeCode}-90${String(i + 1).padStart(2, "0")}`;
    const values = [...templateValues];
    values[idIndex] = id;
    values[dateIndex] = options.date;
    values[storeIndex] = storeCode;
    if (nameColumn >= 0) values[nameColumn] = storeName;
    sheet.addRow(values);
    ids.push(id);
  }
  return ids;
}

/**
 * 追加若干行“与已有记录同 ID、实收金额不同”的记录：清洗时被当作 ID 冲突丢掉，
 * 用来测数据质量说明里的“记录 ID 冲突”与去重逻辑。默认取 date 当天的记录，
 * 这样按那天所在的周统计时，冲突才会落进统计周期里（否则 compute_metrics 统计不到）。
 */
export function appendIdConflictRows(sheet: ExcelJS.Worksheet, options: { count: number; amountDelta: number; date: string }): string[] {
  const netColumn = columnOf(sheet, "实收金额");
  const idColumn = columnOf(sheet, "记录ID");
  const dateColumn = columnOf(sheet, "销售日期");
  const sameDay: number[] = [];
  for (let row = 2; row <= sheet.rowCount && sameDay.length < options.count; row++) {
    if (dateOf(sheet.getRow(row).getCell(dateColumn).value) === options.date) sameDay.push(row);
  }
  if (sameDay.length < options.count) throw new Error(`“销售明细”表里 ${options.date} 的记录不足 ${options.count} 条，无法造 ID 冲突夹具`);
  const ids: string[] = [];
  for (const row of sameDay) {
    const source = sheet.getRow(row);
    const values = headerOf(sheet).map((_, column) => source.getCell(column + 1).value);
    const net = Number(values[netColumn - 1] ?? 0);
    values[netColumn - 1] = round2(net + options.amountDelta);
    sheet.addRow(values);
    ids.push(String(values[idColumn - 1] ?? ""));
  }
  return ids;
}

// ---- 确定性写出 ----

/**
 * 写 xlsx 并固定时间戳：exceljs 写出的 zip 条目时间取自 `new Date()`，文档属性也带创建时间，
 * 不处理的话同一份数据两次生成字节不同，入库会一直产生噪音。这里统一重打包。
 */
export async function writeWorkbookDeterministic(workbook: ExcelJS.Workbook, path: string): Promise<Buffer> {
  workbook.created = FIXED_DATE;
  workbook.modified = FIXED_DATE;
  const raw = Buffer.from((await workbook.xlsx.writeBuffer()) as ArrayBuffer);
  const source = await JSZip.loadAsync(raw);
  const out = new JSZip();
  for (const name of Object.keys(source.files).sort()) {
    const file = source.files[name]!;
    if (file.dir) continue;
    out.file(name, await file.async("nodebuffer"), { date: FIXED_DATE });
  }
  // JSZip 会为路径自动补目录条目（如 _rels/），这些条目的日期默认取当前时间，同样要固定下来，
  // 否则同一份数据隔几分钟生成就会得到不同字节。
  for (const entry of Object.values(out.files)) entry.date = FIXED_DATE;
  const bytes = await out.generateAsync({ type: "nodebuffer", compression: "DEFLATE" });
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, bytes);
  return bytes;
}

// ---- manifest ----

export interface FixtureEntry {
  file: string;
  description: string;
  bytes: number;
  sha256: string;
  /** 工作表名（按顺序） */
  sheets: string[];
  /** 销售明细的数据行数（不含表头） */
  salesRows: number;
  /** 销售明细的表头 */
  salesColumns: string[];
  /** 销售日期覆盖范围；损坏／没有销售明细时为 null */
  coverage: { from: string; to: string } | null;
}

export interface FixtureManifest {
  /** 源数据（相对仓库根目录；不在仓库内时只有文件名） */
  source: string;
  sourceSha256: string;
  /** 重新生成用的命令 */
  generatedBy: string;
  fixtures: FixtureEntry[];
}

export interface GenerateFixturesOptions {
  sourcePath: string;
  outDir: string;
  /** 用于把 sourcePath 记成相对路径；缺省按本包位置推仓库根目录 */
  repoRoot?: string;
}

function describeSource(sourcePath: string, repoRoot: string): string {
  const rel = relative(repoRoot, sourcePath);
  const inside = rel !== "" && !rel.startsWith("..") && !isAbsolute(rel);
  return (inside ? rel : basename(sourcePath)).replace(/\\/g, "/");
}

function statsOf(workbook: ExcelJS.Workbook): Omit<FixtureEntry, "file" | "description" | "bytes" | "sha256"> {
  const sheets = workbook.worksheets.map((sheet) => sheet.name);
  const sales = workbook.getWorksheet(SHEETS.sales);
  if (!sales) return { sheets, salesRows: 0, salesColumns: [], coverage: null };
  const dateColumn = headerOf(sales).indexOf("销售日期") + 1;
  let from = "";
  let to = "";
  let rows = 0;
  for (let row = 2; row <= sales.rowCount; row++) {
    rows += 1;
    if (dateColumn < 1) continue;
    const date = dateOf(sales.getRow(row).getCell(dateColumn).value);
    if (date === null) continue;
    if (from === "" || date < from) from = date;
    if (to === "" || date > to) to = date;
  }
  return { sheets, salesRows: rows, salesColumns: headerOf(sales), coverage: from === "" ? null : { from, to } };
}

/** 生成全部夹具与 manifest.json，返回 manifest。生成物字节可重复（同输入 → 同字节）。 */
export async function generateFixtures(options: GenerateFixturesOptions): Promise<FixtureManifest> {
  const repoRoot = options.repoRoot ?? fileURLToPath(new URL("../../..", import.meta.url));
  const sourceBytes = readFileSync(options.sourcePath);
  const sourceSha256 = sha256(sourceBytes);
  mkdirSync(options.outDir, { recursive: true });

  const fixtures: FixtureEntry[] = [];
  for (const spec of FIXTURES) {
    const path = join(options.outDir, `${spec.key}.xlsx`);
    let bytes: Buffer;
    let stats: Omit<FixtureEntry, "file" | "description" | "bytes" | "sha256">;
    if (spec.corrupt) {
      bytes = truncateForCorruption(sourceBytes);
      writeFileSync(path, bytes);
      stats = { sheets: [], salesRows: 0, salesColumns: [], coverage: null };
    } else {
      const workbook = new ExcelJS.Workbook();
      await workbook.xlsx.readFile(options.sourcePath);
      spec.mutate?.(workbook);
      bytes = await writeWorkbookDeterministic(workbook, path);
      stats = statsOf(workbook);
    }
    fixtures.push({ file: `${spec.key}.xlsx`, description: spec.description, bytes: bytes.length, sha256: sha256(bytes), ...stats });
  }

  const manifest: FixtureManifest = {
    source: describeSource(options.sourcePath, repoRoot),
    sourceSha256,
    generatedBy: "bun packages/cli/src/main.ts fixtures",
    fixtures,
  };
  writeFileSync(join(options.outDir, "manifest.json"), JSON.stringify(manifest, null, 2) + "\n");
  return manifest;
}
