import { describe, expect, test } from "bun:test";
import { renderSlots } from "../../src/qc";
import { makeFacts } from "./fixtures";

describe("renderSlots", () => {
  const facts = makeFacts();

  test("环比渲染为带符号的一位小数百分比", () => {
    expect(renderSlots("{{regions.华东.wow}}", facts)).toEqual({ text: "-12.0%", unresolved: [] });
  });

  test("正数环比带加号", () => {
    expect(renderSlots("环比 {{regions.华北.wow}}", facts).text).toBe("环比 +30.9%");
  });

  test("金额按分取整到元并加千分位", () => {
    expect(renderSlots("{{company.revenue}}", facts)).toEqual({ text: "573,635 元", unresolved: [] });
  });

  test("数组按业务键取值：门店编码、品类名", () => {
    expect(renderSlots("{{stores.HZ01.wow}} / {{categories.饮品.wow}}", facts).text).toBe("-16.7% / +11.1%");
  });

  test("计数类字段渲染为整数", () => {
    expect(renderSlots("{{quality.returns.count}} 笔", facts).text).toBe("12 笔");
  });

  test("区域×品类毛利率：本周与上周都渲染为一位小数百分数", () => {
    expect(
      renderSlots("华南饮品毛利率由 {{regionCategories.华南.饮品.prevGrossMarginRate}} 降至 {{regionCategories.华南.饮品.grossMarginRate}}", facts).text,
    ).toBe("华南饮品毛利率由 70.4% 降至 60.5%");
  });

  test("退货明细可按门店编码或商品编码取：份数与金额", () => {
    expect(renderSlots("{{quality.returns.groups.BJ01.count}} 份 / {{quality.returns.groups.BJ01.revenue}}", facts).text).toBe("15 份 / -330 元");
    expect(renderSlots("{{quality.returns.groups.C01.count}} 份", facts).text).toBe("15 份");
  });

  test("未知路径进入 unresolved，原文保留", () => {
    const out = renderSlots("A {{regions.西南.wow}} B {{company.nope}} C {{regions.西南.wow}}", facts);
    expect(out.text).toBe("A {{regions.西南.wow}} B {{company.nope}} C {{regions.西南.wow}}");
    expect(out.unresolved).toEqual(["regions.西南.wow", "company.nope"]);
  });

  test("没有渲染格式的字段视为无法解析", () => {
    expect(renderSlots("{{period.week}}", facts).unresolved).toEqual(["period.week"]);
  });

  test("环比为 null 时写明不可比，其余 null 写暂无", () => {
    const f = makeFacts({ company: { ...facts.company, wow: null, prevRevenueCents: null, grossMarginRate: null } });
    expect(renderSlots("{{company.wow}}", f).text).toBe("不可比（无上周数据）");
    expect(renderSlots("{{company.grossMarginRate}}", f).text).toBe("暂无");
  });

  test("占比类字段不带加号，舍入按四舍五入", () => {
    expect(renderSlots("{{company.achievement}}", facts).text).toBe("95.3%");
    const f = makeFacts({ company: { ...facts.company, wow: 0.1235 } });
    expect(renderSlots("{{company.wow}}", f).text).toBe("+12.4%");
  });

  test("槽位内允许首尾空格", () => {
    expect(renderSlots("{{ company.revenue }}", facts).text).toBe("573,635 元");
  });
});
