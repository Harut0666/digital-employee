import { describe, expect, test } from "bun:test";
import { formSummary } from "../src/view/form-summary";

const schema = {
  type: "object",
  properties: {
    period: { type: "object", title: "统计周期", properties: { year: { type: "integer" }, week: { type: "integer" } } },
    salesFile: { type: "string", title: "销售明细" },
    remark: { type: "string", title: "备注" },
    focus: { type: "string", title: "关注点" },
    allowMissingMinutes: { type: "boolean", title: "无纪要继续" },
  },
};
const ui = {
  "ui:order": ["period", "salesFile", "focus", "remark", "allowMissingMinutes"],
  period: { "ui:field": "isoWeek" },
  salesFile: { "ui:widget": "file" },
  allowMissingMinutes: { "ui:widget": "hidden" },
};

describe("formSummary", () => {
  test("按界面顺序列出填写的字段；周选择器的字段显示周次和日期范围；文件和隐藏字段不列", () => {
    const rows = formSummary(schema, ui, {
      period: { year: 2026, week: 44 },
      remark: "先写区域",
      focus: "",
      salesFile: "x.xlsx",
      allowMissingMinutes: true,
    });
    expect(rows).toEqual([
      { label: "统计周期", value: "2026 年第 44 周（10 月 26 日至 11 月 1 日）" },
      { label: "备注", value: "先写区域" },
    ]);
  });

  test("没有标题的字段用字段名；布尔值写是否", () => {
    expect(formSummary({ type: "object", properties: { urgent: { type: "boolean" } } }, undefined, { urgent: true })).toEqual([{ label: "urgent", value: "是" }]);
  });
});
