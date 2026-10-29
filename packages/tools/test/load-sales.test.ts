import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { existsSync, mkdtempSync, readdirSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { connect, sample, type Harness } from "./helpers";

let h: Harness;
beforeAll(async () => {
  h = await connect();
});
afterAll(async () => {
  await h.close();
});

describe("load_sales（接缝 2）", () => {
  test("读 xlsx：写出数据集文件，返回行数与覆盖范围", async () => {
    const outDir = mkdtempSync(join(tmpdir(), "de-ds-"));
    const r = await h.call("load_sales", { salesPath: sample("销售明细.xlsx"), outDir });
    expect(r.datasetPath.startsWith(outDir)).toBe(true);
    expect(existsSync(r.datasetPath)).toBe(true);
    expect(r.rows).toBe(44820);
    expect(r.coverage).toEqual({ from: "2026-08-31", to: "2026-11-01" });
  });

  test("幂等：同一输入再调用一次，返回同一文件且不重写、不新增文件", async () => {
    const outDir = mkdtempSync(join(tmpdir(), "de-ds-"));
    const a = await h.call("load_sales", { salesPath: sample("销售明细.xlsx"), outDir });
    const mtime = statSync(a.datasetPath).mtimeMs;
    const b = await h.call("load_sales", { salesPath: sample("销售明细.xlsx"), outDir });
    expect(b).toEqual(a);
    expect(statSync(b.datasetPath).mtimeMs).toBe(mtime);
    expect(readdirSync(outDir)).toHaveLength(1);
  });

  test("读 csv（UTF-8 带 BOM）：行数与覆盖范围与 xlsx 一致", async () => {
    const outDir = mkdtempSync(join(tmpdir(), "de-ds-"));
    const r = await h.call("load_sales", { salesPath: sample("销售明细.csv"), outDir });
    expect(r.rows).toBe(44820);
    expect(r.coverage).toEqual({ from: "2026-08-31", to: "2026-11-01" });
  });

  test("文件不存在时返回 isError", async () => {
    const outDir = mkdtempSync(join(tmpdir(), "de-ds-"));
    const r = await h.raw("load_sales", { salesPath: sample("不存在.xlsx"), outDir });
    expect(r.isError).toBe(true);
  });
});
