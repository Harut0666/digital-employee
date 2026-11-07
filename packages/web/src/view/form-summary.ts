// 已提交的表单卡片：把表单内容按 Schema 和界面配置写成几行可读的文字。
// 文件字段由附件列表展示，隐藏字段是暂停卡片上的选择，都不列。

import { formatWeekRange } from "./iso-week";

type Json = Record<string, unknown>;

const isObject = (v: unknown): v is Json => typeof v === "object" && v !== null && !Array.isArray(v);

export interface SummaryRow {
  label: string;
  value: string;
}

function show(value: unknown, ui: Json): string {
  if (ui["ui:field"] === "isoWeek" && isObject(value) && typeof value.year === "number" && typeof value.week === "number") {
    return `${value.year} 年第 ${value.week} 周（${formatWeekRange(value.year, value.week)}）`;
  }
  if (typeof value === "boolean") return value ? "是" : "否";
  if (Array.isArray(value)) return value.map(String).join("、");
  if (isObject(value)) return JSON.stringify(value);
  return value === undefined || value === null ? "" : String(value);
}

export function formSummary(schema: Json, formUi: Json | undefined, form: Json): SummaryRow[] {
  const props = isObject(schema.properties) ? schema.properties : {};
  const order = Array.isArray(formUi?.["ui:order"]) ? (formUi["ui:order"] as string[]).filter((k) => k in props) : [];
  const names = [...order, ...Object.keys(props).filter((k) => !order.includes(k))];
  return names.flatMap((name) => {
    const ui = isObject(formUi?.[name]) ? (formUi[name] as Json) : {};
    if (["file", "files", "hidden"].includes(String(ui["ui:widget"]))) return [];
    const value = show(form[name], ui);
    if (value === "") return [];
    const field = isObject(props[name]) ? (props[name] as Json) : {};
    return [{ label: typeof field.title === "string" ? field.title : name, value }];
  });
}
