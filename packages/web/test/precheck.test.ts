import { describe, expect, test } from "bun:test";
import { computePrecheck } from "../src/view/precheck";

const f = (name: string) => ({ name });
const status = (items: ReturnType<typeof computePrecheck>) => Object.fromEntries(items.map((i) => [i.key, i.ok]));

describe("computePrecheck（前端侧的预检提示，最终以服务端准入校验为准）", () => {
  test("什么都没传：全部未就绪", () => {
    expect(status(computePrecheck({ period: { year: 2026, week: 44 }, sales: [], minutes: [] }))).toEqual({
      sales: false, stores: false, targets: false, period: true, minutes: false,
    });
  });

  test("只传 CSV：缺门店信息和销售目标", () => {
    expect(status(computePrecheck({ period: { year: 2026, week: 44 }, sales: [f("销售明细.csv")], minutes: [] }))).toMatchObject({
      sales: true, stores: false, targets: false,
    });
  });

  test("传 xlsx 工作簿：维表随工作簿提供；CSV 加同名维表文件也算就绪", () => {
    expect(status(computePrecheck({ period: { year: 2026, week: 44 }, sales: [f("销售明细.xlsx")], minutes: [f("周会纪要_2026W44.docx")] }))).toEqual({
      sales: true, stores: true, targets: true, period: true, minutes: true,
    });
    expect(status(computePrecheck({ period: { year: 2026, week: 44 }, sales: [f("销售明细.csv"), f("门店信息.csv"), f("销售目标.csv")], minutes: [] }))).toMatchObject({
      stores: true, targets: true,
    });
  });

  test("没选周次：周次项未就绪", () => {
    expect(status(computePrecheck({ period: undefined, sales: [], minutes: [] })).period).toBe(false);
  });
});
