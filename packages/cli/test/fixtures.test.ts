// de fixtures：从公开样例生成变异数据。测试用一份小的合成 xlsx 验证变异逻辑与确定性；
// 另外核对入库的 samples/fixtures 与 manifest 一致（生成器改了就要重跑 de fixtures）。
import { describe, expect, test } from "bun:test";
import { existsSync, mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import ExcelJS from "exceljs";
import JSZip from "jszip";
import { CORRUPT_BYTES, FIXTURES, generateFixtures, writeWorkbookDeterministic, type FixtureManifest } from "../src/fixtures";

const REPO_ROOT = resolve(import.meta.dir, "..", "..", "..");
const tmp = (): string => mkdtempSync(join(tmpdir(), "de-fixtures-"));

const SALES_HEADER = ["记录ID", "销售日期", "门店编码", "门店名称", "渠道", "商品编码", "商品名称", "销售数量", "原价金额", "折扣金额", "实收金额", "成本金额"];

const saleRow = (id: string, date: string, store: string, storeName: string, product: string, productName: string, qty: number, gross: number, discount: number, net: number, cost: number) =>
  [id, date, store, storeName, "堂食自提", product, productName, qty, gross, discount, net, cost];

/** 与公开样例同构的小数据包：4 个工作表、3 家店、2 个商品、跨第 44 周。 */
async function makeSource(dir: string): Promise<string> {
  const wb = new ExcelJS.Workbook();
  const sales = wb.addWorksheet("销售明细");
  sales.addRow(SALES_HEADER);
  sales.addRow(saleRow("R20261026SH01-0001", "2026-10-26", "SH01", "上海徐汇店", "B01", "原味吐司", 10, 100, 5, 95, 40));
  sales.addRow(saleRow("R20261027SH01-0002", "2026-10-27", "SH01", "上海徐汇店", "B02", "全麦吐司", 8, 120, 6, 114, 48));
  sales.addRow(saleRow("R20261027HZ01-0006", "2026-10-27", "HZ01", "杭州湖滨店", "B02", "全麦吐司", 4, 60, 0, 60, 24));
  sales.addRow(saleRow("R20261027SZ01-0007", "2026-10-27", "SZ01", "深圳南山店", "B01", "原味吐司", 7, 70, 2, 68, 28));
  sales.addRow(saleRow("R20261028HZ01-0003", "2026-10-28", "HZ01", "杭州湖滨店", "B01", "原味吐司", 12, 120, 0, 120, 48));
  sales.addRow(saleRow("R20261029SZ01-0004", "2026-10-29", "SZ01", "深圳南山店", "B02", "全麦吐司", 5, 75, 3, 72, 30));
  sales.addRow(saleRow("R20261101SZ01-0005", "2026-11-01", "SZ01", "深圳南山店", "B01", "原味吐司", 6, 60, 0, 60, 24));
  const stores = wb.addWorksheet("门店信息");
  stores.addRow(["门店编码", "门店名称", "区域", "城市", "店型", "面积(㎡)", "开业日期"]);
  stores.addRow(["SH01", "上海徐汇店", "华东", "上海", "商场店", 120, "2021-05-01"]);
  stores.addRow(["HZ01", "杭州湖滨店", "华东", "杭州", "商场店", 100, "2021-06-01"]);
  stores.addRow(["SZ01", "深圳南山店", "华南", "深圳", "写字楼店", 90, "2021-07-01"]);
  const products = wb.addWorksheet("商品信息");
  products.addRow(["商品编码", "商品名称", "品类", "规格", "零售单价(元)", "单位成本(元)"]);
  products.addRow(["B01", "原味吐司", "面包", "400g", 10, 4]);
  products.addRow(["B02", "全麦吐司", "面包", "400g", 15, 6]);
  const targets = wb.addWorksheet("销售目标");
  targets.addRow(["区域", "年份", "周次", "周起始日", "周结束日", "实收目标(元)"]);
  targets.addRow(["华东", 2026, "W44", "2026-10-26", "2026-11-01", 5000]);
  targets.addRow(["华南", 2026, "W44", "2026-10-26", "2026-11-01", 3000]);
  const path = join(dir, "销售明细.xlsx");
  await writeWorkbookDeterministic(wb, path);
  return path;
}

async function readBack(path: string): Promise<ExcelJS.Workbook> {
  const wb = new ExcelJS.Workbook();
  await wb.xlsx.readFile(path);
  return wb;
}

const headerOf = (ws: ExcelJS.Worksheet): string[] => ((ws.getRow(1).values as unknown[]) ?? []).slice(1).map((v) => String(v ?? ""));

const datesOf = (ws: ExcelJS.Worksheet): string[] => {
  const out: string[] = [];
  for (let i = 2; i <= ws.rowCount; i++) {
    const value = ws.getRow(i).getCell(2).value;
    if (value === null || value === undefined) continue;
    out.push(value instanceof Date ? value.toISOString().slice(0, 10) : String(value).slice(0, 10));
  }
  return out;
};

/** 按文件名读回一份夹具。 */
const fixturePath = (dir: string, key: string): string => join(dir, `${key}.xlsx`);

describe("FIXTURES", () => {
  test("至少覆盖周中截止、缺工作表、缺列、损坏、未知编码、ID 冲突", () => {
    const keys = FIXTURES.map((fixture) => fixture.key);
    expect(keys).toContain("销售明细-第44周周中截止");
    expect(keys).toContain("销售明细-缺销售目标表");
    expect(keys).toContain("销售明细-缺实收金额列");
    expect(keys).toContain("销售明细-损坏");
    expect(keys).toContain("销售明细-含未知门店编码");
    expect(keys).toContain("销售明细-记录ID冲突");
    // 每个夹具都要有说明，说明里的重点是“变异了什么”
    for (const fixture of FIXTURES) expect(fixture.description.length).toBeGreaterThan(5);
  });
});

describe("generateFixtures（合成小数据包）", () => {
  test("生成 6 份夹具 + manifest，且每种变异都真的生效", async () => {
    const dir = tmp();
    const source = await makeSource(dir);
    const out = join(dir, "fixtures");
    const manifest = await generateFixtures({ sourcePath: source, outDir: out, repoRoot: REPO_ROOT });

    expect(manifest.source).toBe("销售明细.xlsx");
    expect(manifest.fixtures.map((f) => f.file)).toEqual(FIXTURES.map((f) => `${f.key}.xlsx`));
    for (const fixture of FIXTURES) expect(existsSync(fixturePath(out, fixture.key))).toBe(true);
    expect(existsSync(join(out, "manifest.json"))).toBe(true);

    // 周中截止：10/28 之后没有数据
    const cut = await readBack(fixturePath(out, "销售明细-第44周周中截止"));
    const cutDates = datesOf(cut.getWorksheet("销售明细")!);
    expect(Math.max(...cutDates.map((d) => Number(d.replace(/-/g, ""))))).toBe(20261028);
    expect(cutDates).toHaveLength(5);

    // 缺“销售目标”工作表
    const noTargets = await readBack(fixturePath(out, "销售明细-缺销售目标表"));
    expect(noTargets.worksheets.map((ws) => ws.name)).toEqual(["销售明细", "门店信息", "商品信息"]);

    // 缺“实收金额”列
    const noNet = await readBack(fixturePath(out, "销售明细-缺实收金额列"));
    expect(headerOf(noNet.getWorksheet("销售明细")!)).not.toContain("实收金额");
    expect(headerOf(noNet.getWorksheet("销售明细")!)).toContain("原价金额");

    // 损坏的 xlsx：读不出来
    const broken = readFileSync(fixturePath(out, "销售明细-损坏"));
    expect(broken.length).toBeLessThanOrEqual(CORRUPT_BYTES);
    const bad = new ExcelJS.Workbook();
    await expect(bad.xlsx.load(broken as unknown as ArrayBuffer)).rejects.toThrow();

    // 未知门店编码：出现门店信息表里没有的编码
    const unknown = await readBack(fixturePath(out, "销售明细-含未知门店编码"));
    const unknownStores = new Set<string>();
    const unknownWs = unknown.getWorksheet("销售明细")!;
    for (let i = 2; i <= unknownWs.rowCount; i++) unknownStores.add(String(unknownWs.getRow(i).getCell(3).value ?? ""));
    expect([...unknownStores].some((code) => !["SH01", "HZ01", "SZ01"].includes(code))).toBe(true);

    // 记录 ID 冲突：同一记录 ID 出现两次，且内容不同
    const conflict = await readBack(fixturePath(out, "销售明细-记录ID冲突"));
    const conflictWs = conflict.getWorksheet("销售明细")!;
    const seen = new Map<string, string>();
    let conflicts = 0;
    for (let i = 2; i <= conflictWs.rowCount; i++) {
      const row = conflictWs.getRow(i);
      const id = String(row.getCell(1).value ?? "");
      const signature = [2, 3, 6, 11].map((c) => String(row.getCell(c).value ?? "")).join("|");
      const previous = seen.get(id);
      if (previous !== undefined && previous !== signature) conflicts += 1;
      seen.set(id, signature);
    }
    expect(conflicts).toBe(3);
  }, 60_000);

  test("写 xlsx 时把 zip 里每个条目（含自动补的目录条目）的时间戳固定下来", async () => {
    const dir = tmp();
    const wb = new ExcelJS.Workbook();
    wb.addWorksheet("a").addRow(["x"]);
    const path = join(dir, "wb.xlsx");
    const bytes = await writeWorkbookDeterministic(wb, path);
    const zip = await JSZip.loadAsync(bytes);
    for (const [name, entry] of Object.entries(zip.files)) {
      expect({ name, date: entry.date.toISOString() }).toEqual({ name, date: "2026-01-01T00:00:00.000Z" });
    }
  });

  test("确定性：同一份源数据两次生成，字节完全一致（可以重复运行，入库不产生噪音）", async () => {
    const dir = tmp();
    const source = await makeSource(dir);
    const first = await generateFixtures({ sourcePath: source, outDir: join(dir, "a"), repoRoot: REPO_ROOT });
    const second = await generateFixtures({ sourcePath: source, outDir: join(dir, "b"), repoRoot: REPO_ROOT });
    expect(second).toEqual(first);
    for (const fixture of FIXTURES) {
      const left = readFileSync(fixturePath(join(dir, "a"), fixture.key));
      const right = readFileSync(fixturePath(join(dir, "b"), fixture.key));
      expect(left.equals(right)).toBe(true);
    }
    expect(readFileSync(join(dir, "a", "manifest.json"), "utf8")).toBe(readFileSync(join(dir, "b", "manifest.json"), "utf8"));
  }, 60_000);

  test("manifest 记下每个夹具的大小、哈希与结构，方便核对入库的生成物", async () => {
    const dir = tmp();
    const source = await makeSource(dir);
    const manifest = await generateFixtures({ sourcePath: source, outDir: join(dir, "fixtures"), repoRoot: REPO_ROOT });
    const cut = manifest.fixtures.find((f) => f.file === "销售明细-第44周周中截止.xlsx")!;
    expect(cut.sheets).toEqual(["销售明细", "门店信息", "商品信息", "销售目标"]);
    expect(cut.salesRows).toBe(5);
    expect(cut.coverage).toEqual({ from: "2026-10-26", to: "2026-10-28" });
    expect(cut.bytes).toBeGreaterThan(0);
    expect(cut.sha256).toMatch(/^[0-9a-f]{64}$/);
    const noNet = manifest.fixtures.find((f) => f.file === "销售明细-缺实收金额列.xlsx")!;
    expect(noNet.salesColumns).not.toContain("实收金额");
  }, 60_000);
});

describe("入库的 samples/fixtures", () => {
  const dir = join(REPO_ROOT, "samples", "fixtures");
  const manifestPath = join(dir, "manifest.json");

  test("manifest 与生成物一致（改了生成器就要重跑 de fixtures）", () => {
    expect(existsSync(manifestPath)).toBe(true);
    const manifest = JSON.parse(readFileSync(manifestPath, "utf8")) as FixtureManifest;
    expect(manifest.fixtures.map((f) => f.file)).toEqual(FIXTURES.map((f) => `${f.key}.xlsx`));
    for (const entry of manifest.fixtures) {
      const path = join(dir, entry.file);
      expect(existsSync(path)).toBe(true);
      const bytes = readFileSync(path);
      expect(bytes.length).toBe(entry.bytes);
      expect(Bun.CryptoHasher.hash("sha256", bytes, "hex")).toBe(entry.sha256);
    }
  });
});
