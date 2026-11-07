import { describe, expect, test } from "bun:test";
import { formatWeekRange, isoWeekOf } from "../src/view/iso-week";

describe("ISO 周", () => {
  test("周次显示成周一到周日的日期范围", () => {
    expect(formatWeekRange(2026, 44)).toBe("10 月 26 日至 11 月 1 日");
    expect(formatWeekRange(2026, 36)).toBe("8 月 31 日至 9 月 6 日");
  });

  test("跨年的周写出年份", () => {
    expect(formatWeekRange(2026, 1)).toBe("2025 年 12 月 29 日至 2026 年 1 月 4 日");
  });

  test("选中任意一天得到它所在的 ISO 周，年末几天可能属于下一年的第 1 周", () => {
    expect(isoWeekOf(new Date(2026, 10, 1))).toEqual({ year: 2026, week: 44 });
    expect(isoWeekOf(new Date(2026, 9, 26))).toEqual({ year: 2026, week: 44 });
    expect(isoWeekOf(new Date(2025, 11, 29))).toEqual({ year: 2026, week: 1 });
    expect(isoWeekOf(new Date(2027, 0, 1))).toEqual({ year: 2026, week: 53 });
  });
});
