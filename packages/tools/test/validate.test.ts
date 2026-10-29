import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { att, connect, sample, type Harness } from "./helpers";

let h: Harness;
beforeAll(async () => {
  h = await connect();
});
afterAll(async () => {
  await h.close();
});

const validate = (period: { year: number; week: number }, files: string[], extra: Record<string, unknown> = {}) =>
  h.call("validate_submission", { period, attachments: files.map(att), ...extra });

describe("validate_submission（接缝 2）", () => {
  test("A-01：W44 + xlsx + W44 纪要 → 通过，并识别数据与纪要", async () => {
    const r = await validate({ year: 2026, week: 44 }, ["销售明细.xlsx", "周会纪要_2026W44.docx"]);
    expect(r.ok).toBe(true);
    expect(r.pause).toBeUndefined();
    expect(r.sales).toEqual({ path: sample("销售明细.xlsx"), kind: "xlsx" });
    expect(r.dimensions).toEqual({ stores: true, products: true, targets: true });
    expect(r.minutes).toEqual([{ path: sample("周会纪要_2026W44.docx"), week: { year: 2026, week: 44 } }]);
    expect(r.coverage).toEqual({ from: "2026-08-31", to: "2026-11-01" });
  });

  test("A-03：没有销售明细 → missing_sales", async () => {
    const r = await validate({ year: 2026, week: 44 }, ["周会纪要_2026W44.docx"]);
    expect(r.ok).toBe(false);
    expect(r.pause.code).toBe("missing_sales");
    expect(r.pause.message).toContain("销售明细");
  });

  test("A-05：W46 超出数据范围 → period_out_of_range，并给出实际覆盖日期", async () => {
    const r = await validate({ year: 2026, week: 46 }, ["销售明细.xlsx"]);
    expect(r.ok).toBe(false);
    expect(r.pause.code).toBe("period_out_of_range");
    expect(r.coverage).toEqual({ from: "2026-08-31", to: "2026-11-01" });
    expect(r.pause.message).toContain("2026-08-31");
    expect(r.pause.message).toContain("2026-11-01");
  });

  test("只传 CSV → missing_dimensions，说明缺门店信息和销售目标", async () => {
    const r = await validate({ year: 2026, week: 44 }, ["销售明细.csv", "周会纪要_2026W44.docx"]);
    expect(r.ok).toBe(false);
    expect(r.pause.code).toBe("missing_dimensions");
    expect(r.sales.kind).toBe("csv");
    expect(r.pause.message).toContain("门店信息");
    expect(r.pause.message).toContain("销售目标");
  });

  test("统计 W44 只传 W43 纪要 → minutes_week_mismatch；选择无纪要继续后通过", async () => {
    const files = ["销售明细.xlsx", "周会纪要_2026W43.docx"];
    const r = await validate({ year: 2026, week: 44 }, files);
    expect(r.pause.code).toBe("minutes_week_mismatch");
    expect(r.minutes[0].week).toEqual({ year: 2026, week: 43 });
    const r2 = await validate({ year: 2026, week: 44 }, files, { allowMissingMinutes: true });
    expect(r2.ok).toBe(true);
  });

  test("完全没有纪要 → minutes_missing", async () => {
    const r = await validate({ year: 2026, week: 44 }, ["销售明细.xlsx"]);
    expect(r.pause.code).toBe("minutes_missing");
  });

  test("纪要周次按正文标题识别，不看文件名", async () => {
    const dir = mkdtempSync(join(tmpdir(), "de-min-"));
    const renamed = join(dir, "随便什么名字.docx");
    writeFileSync(renamed, readFileSync(sample("周会纪要_2026W44.docx")));
    const r = await h.call("validate_submission", {
      period: { year: 2026, week: 44 },
      attachments: [att("销售明细.xlsx"), { name: "周会纪要_2026W43.docx", path: renamed }],
    });
    expect(r.ok).toBe(true);
    expect(r.minutes[0].week).toEqual({ year: 2026, week: 44 });
  });

  test("数据在周中截止 → period_incomplete；选择按已有数据继续后进入后续检查", async () => {
    const dir = mkdtempSync(join(tmpdir(), "de-csv-"));
    const lines = readFileSync(sample("销售明细.csv"), "utf8").split(/\r?\n/);
    const kept = lines.filter((l, i) => i === 0 || (l.split(",")[1] ?? "") <= "2026-10-29");
    const csv = join(dir, "销售明细.csv");
    writeFileSync(csv, kept.join("\n"));
    const attachments = [{ name: "销售明细.csv", path: csv }, att("周会纪要_2026W44.docx")];
    const r = await h.call("validate_submission", { period: { year: 2026, week: 44 }, attachments });
    expect(r.pause.code).toBe("period_incomplete");
    expect(r.coverage).toEqual({ from: "2026-08-31", to: "2026-10-29" });
    const r2 = await h.call("validate_submission", {
      period: { year: 2026, week: 44 },
      attachments,
      allowPartialPeriod: true,
    });
    expect(r2.pause.code).toBe("missing_dimensions");
  });
});
