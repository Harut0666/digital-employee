// 读取销售数据源（xlsx 4 个工作表，或 csv 单表）。金额读入后立刻转成整数“分”。
import ExcelJS from "exceljs";
import Papa from "papaparse";
import { readFileSync, statSync } from "node:fs";
import { extname } from "node:path";

export interface RawSale {
  id: string;
  date: string;
  store: string;
  storeName: string;
  channel: string;
  product: string;
  productName: string;
  qty: number;
  grossCents: number;
  discountCents: number;
  netCents: number;
  costCents: number;
}
export interface StoreInfo {
  code: string;
  name: string;
  region: string;
  city: string;
  storeType: string;
  openDate: string;
}
export interface ProductInfo {
  code: string;
  name: string;
  category: string;
  priceCents: number;
  costCents: number;
}
export interface TargetInfo {
  region: string;
  year: number;
  week: number;
  targetCents: number;
}
export interface SalesSource {
  kind: "xlsx" | "csv";
  sales: RawSale[];
  stores: StoreInfo[] | null;
  products: ProductInfo[] | null;
  targets: TargetInfo[] | null;
}

/** 元 → 分，四舍五入（加极小量抵消二进制浮点误差，如 1.005） */
export const toCents = (v: number): number => Math.round(v * 100 + (v < 0 ? -1e-7 : 1e-7));

function str(v: unknown): string {
  if (v === null || v === undefined) return "";
  if (v instanceof Date) return v.toISOString().slice(0, 10);
  if (typeof v === "object") {
    const o = v as { text?: unknown; result?: unknown; richText?: { text: string }[] };
    if (o.richText) return o.richText.map((r) => r.text).join("");
    if (o.text !== undefined) return String(o.text);
    if (o.result !== undefined) return String(o.result);
  }
  return String(v).trim();
}
function num(v: unknown): number {
  if (typeof v === "number") return v;
  if (v && typeof v === "object" && "result" in v) return num((v as { result: unknown }).result);
  const n = Number(str(v));
  return Number.isFinite(n) ? n : 0;
}

function toSale(r: Record<string, unknown>): RawSale {
  return {
    id: str(r["记录ID"]),
    date: str(r["销售日期"]).slice(0, 10),
    store: str(r["门店编码"]),
    storeName: str(r["门店名称"]),
    channel: str(r["渠道"]),
    product: str(r["商品编码"]),
    productName: str(r["商品名称"]),
    qty: num(r["销售数量"]),
    grossCents: toCents(num(r["原价金额"])),
    discountCents: toCents(num(r["折扣金额"])),
    netCents: toCents(num(r["实收金额"])),
    costCents: toCents(num(r["成本金额"])),
  };
}

function sheetRows(ws: ExcelJS.Worksheet): Record<string, unknown>[] {
  const head = (ws.getRow(1).values as unknown[]).slice(1).map(str);
  const out: Record<string, unknown>[] = [];
  for (let i = 2; i <= ws.rowCount; i++) {
    const vals = ws.getRow(i).values as unknown[];
    if (!vals || vals.every((v) => v === null || v === undefined || v === "")) continue;
    const o: Record<string, unknown> = {};
    head.forEach((h, j) => (o[h] = vals[j + 1]));
    out.push(o);
  }
  return out;
}

const SHEETS = { sales: "销售明细", stores: "门店信息", products: "商品信息", targets: "销售目标" } as const;

async function readXlsx(path: string): Promise<SalesSource> {
  const wb = new ExcelJS.Workbook();
  await wb.xlsx.readFile(path);
  const salesWs = wb.getWorksheet(SHEETS.sales);
  if (!salesWs) throw new Error(`缺少“${SHEETS.sales}”工作表`);
  const storesWs = wb.getWorksheet(SHEETS.stores);
  const productsWs = wb.getWorksheet(SHEETS.products);
  const targetsWs = wb.getWorksheet(SHEETS.targets);
  return {
    kind: "xlsx",
    sales: sheetRows(salesWs).map(toSale),
    stores: storesWs
      ? sheetRows(storesWs).map((r) => ({
          code: str(r["门店编码"]),
          name: str(r["门店名称"]),
          region: str(r["区域"]),
          city: str(r["城市"]),
          storeType: str(r["店型"]),
          openDate: str(r["开业日期"]).slice(0, 10),
        }))
      : null,
    products: productsWs
      ? sheetRows(productsWs).map((r) => ({
          code: str(r["商品编码"]),
          name: str(r["商品名称"]),
          category: str(r["品类"]),
          priceCents: toCents(num(r["零售单价(元)"])),
          costCents: toCents(num(r["单位成本(元)"])),
        }))
      : null,
    targets: targetsWs
      ? sheetRows(targetsWs).map((r) => ({
          region: str(r["区域"]),
          year: num(r["年份"]),
          week: Number(/(\d+)/.exec(str(r["周次"]))?.[1] ?? 0),
          targetCents: toCents(num(r["实收目标(元)"])),
        }))
      : null,
  };
}

function readCsv(path: string): SalesSource {
  const text = readFileSync(path, "utf8").replace(/^﻿/, "");
  const parsed = Papa.parse<Record<string, unknown>>(text, { header: true, skipEmptyLines: true });
  if (!parsed.meta.fields?.includes("记录ID")) throw new Error("CSV 缺少“记录ID”等销售明细字段");
  return { kind: "csv", sales: parsed.data.map(toSale), stores: null, products: null, targets: null };
}

const cache = new Map<string, SalesSource>();

/** 同一文件（路径 + 大小 + 修改时间）在进程内只解析一次 */
export async function readSalesSource(path: string): Promise<SalesSource> {
  const st = statSync(path);
  const key = `${path}|${st.size}|${st.mtimeMs}`;
  const hit = cache.get(key);
  if (hit) return hit;
  const ext = extname(path).toLowerCase();
  const src = ext === ".csv" ? readCsv(path) : ext === ".xlsx" ? await readXlsx(path) : null;
  if (!src) throw new Error(`不支持的销售数据格式：${ext || path}`);
  cache.clear(); // 只缓存最近一份，避免内存占用
  cache.set(key, src);
  return src;
}

export function coverageOf(sales: RawSale[]): { from: string; to: string } | null {
  let from = "";
  let to = "";
  for (const s of sales) {
    if (!s.date) continue;
    if (!from || s.date < from) from = s.date;
    if (!to || s.date > to) to = s.date;
  }
  return from ? { from, to } : null;
}
