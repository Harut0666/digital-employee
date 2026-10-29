// 清洗后的数据集：load_sales 产出的 JSON 文件格式，以及读写它的函数。
//
// 清洗规则（design.md §10.2）：
// - 记录ID 重复：只保留第一条，其余记入 removed；内容与首条不一致的标为 conflict
// - 实收 ≠ 原价 − 折扣：保留，以实收为准，标 mismatch（compute_metrics 按周报告条数）
// - 门店或商品编码在维表里查不到：保留但标 unknown，不计入任何合计
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, renameSync, statSync, writeFileSync } from "node:fs";
import { basename, join } from "node:path";
import { coverageOf, readSalesSource, type ProductInfo, type RawSale, type StoreInfo, type TargetInfo } from "./source";

const DATASET_VERSION = 1;

export interface SaleRow {
  id: string;
  date: string;
  store: string;
  channel: string;
  product: string;
  qty: number;
  grossCents: number;
  discountCents: number;
  netCents: number;
  costCents: number;
  /** 门店或商品编码查不到 */
  unknown?: true;
  /** 实收 ≠ 原价 − 折扣 */
  mismatch?: true;
}

export interface RemovedRow {
  id: string;
  store: string;
  date: string;
  netCents: number;
  /** true：与首条内容不一致（ID 冲突）；false：完全重复 */
  conflict: boolean;
}

export interface Dataset {
  version: number;
  source: { name: string; kind: "xlsx" | "csv"; sha256: string };
  rawRows: number;
  coverage: { from: string; to: string };
  rows: SaleRow[];
  removed: RemovedRow[];
  stores: StoreInfo[] | null;
  products: ProductInfo[] | null;
  targets: TargetInfo[] | null;
}

const sig = (r: RawSale) =>
  [r.date, r.store, r.channel, r.product, r.qty, r.grossCents, r.discountCents, r.netCents, r.costCents].join("|");

export function cleanSales(raw: RawSale[], stores: StoreInfo[] | null, products: ProductInfo[] | null) {
  const storeSet = stores ? new Set(stores.map((s) => s.code)) : null;
  const productSet = products ? new Set(products.map((p) => p.code)) : null;
  const first = new Map<string, string>();
  const rows: SaleRow[] = [];
  const removed: RemovedRow[] = [];
  for (const r of raw) {
    const seen = first.get(r.id);
    if (seen !== undefined) {
      removed.push({ id: r.id, store: r.store, date: r.date, netCents: r.netCents, conflict: seen !== sig(r) });
      continue;
    }
    first.set(r.id, sig(r));
    const row: SaleRow = {
      id: r.id,
      date: r.date,
      store: r.store,
      channel: r.channel,
      product: r.product,
      qty: r.qty,
      grossCents: r.grossCents,
      discountCents: r.discountCents,
      netCents: r.netCents,
      costCents: r.costCents,
    };
    if ((storeSet && !storeSet.has(r.store)) || (productSet && !productSet.has(r.product))) row.unknown = true;
    if (r.netCents !== r.grossCents - r.discountCents) row.mismatch = true;
    rows.push(row);
  }
  return { rows, removed };
}

/** 读取并清洗销售数据，写成内容哈希命名的 JSON；文件已存在则直接复用 */
export async function loadSales(salesPath: string, outDir: string) {
  const bytes = readFileSync(salesPath);
  const sha256 = createHash("sha256").update(bytes).digest("hex");
  const datasetPath = join(outDir, `sales-${sha256.slice(0, 32)}-v${DATASET_VERSION}.json`);
  if (existsSync(datasetPath)) {
    const ds = readDataset(datasetPath);
    return { datasetPath, rows: ds.rawRows, coverage: ds.coverage };
  }
  const src = await readSalesSource(salesPath);
  const coverage = coverageOf(src.sales);
  if (!coverage) throw new Error("销售明细中没有可识别的销售日期");
  const { rows, removed } = cleanSales(src.sales, src.stores, src.products);
  const ds: Dataset = {
    version: DATASET_VERSION,
    source: { name: basename(salesPath), kind: src.kind, sha256 },
    rawRows: src.sales.length,
    coverage,
    rows,
    removed,
    stores: src.stores,
    products: src.products,
    targets: src.targets,
  };
  mkdirSync(outDir, { recursive: true });
  const tmp = `${datasetPath}.${process.pid}.tmp`;
  writeFileSync(tmp, JSON.stringify(ds));
  renameSync(tmp, datasetPath);
  return { datasetPath, rows: ds.rawRows, coverage };
}

const cache = new Map<string, Dataset>();

export function readDataset(path: string): Dataset {
  const st = statSync(path);
  const key = `${path}|${st.size}|${st.mtimeMs}`;
  const hit = cache.get(key);
  if (hit) return hit;
  const ds = JSON.parse(readFileSync(path, "utf8")) as Dataset;
  if (ds.version !== DATASET_VERSION || !Array.isArray(ds.rows)) throw new Error(`数据集文件格式不正确：${path}`);
  cache.clear();
  cache.set(key, ds);
  return ds;
}
