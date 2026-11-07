import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { stripFileFields, uploadSlots } from "../src/view/upload-slots";

const DIR = join(import.meta.dir, "..", "..", "..", "employees", "weekly-report");
const schema = JSON.parse(readFileSync(join(DIR, "form.schema.json"), "utf8")) as Record<string, unknown>;
const ui = JSON.parse(readFileSync(join(DIR, "form.ui.json"), "utf8")) as Record<string, unknown>;

describe("上传项取自员工配置的 form.schema.json / form.ui.json 里的文件字段", () => {
  test("经营周报员工：销售明细（单个）与周会纪要（多个），标题、提示、可接受的文件类型都来自配置", () => {
    const slots = uploadSlots(schema, ui);
    expect(slots.map((s) => s.slot)).toEqual(["salesFile", "minutesFiles"]);
    expect(slots[0]).toMatchObject({ title: "销售明细", accept: ".xlsx,.csv", multiple: false });
    expect(slots[1]).toMatchObject({ title: "周会纪要", accept: ".docx", multiple: true });
    expect(slots[0]!.hint).toContain("xlsx");
  });

  test("上传项按界面配置的 ui:order 排列：服务端按字母序返回 Schema 字段时顺序也不乱", () => {
    const props = schema.properties as Record<string, unknown>;
    const alphabetical = { ...schema, properties: Object.fromEntries(Object.keys(props).sort().map((k) => [k, props[k]])) };
    expect(uploadSlots(alphabetical, ui).map((s) => s.slot)).toEqual(["salesFile", "minutesFiles"]);
  });

  test("没有文件字段的员工：给一个通用的资料上传项", () => {
    const slots = uploadSlots({ type: "object", properties: { topic: { type: "string" } } }, undefined);
    expect(slots.map((s) => s.slot)).toEqual(["other"]);
  });

  test("表单里不再渲染文件字段（由上传区负责），也不要求它们", () => {
    const stripped = stripFileFields({ ...schema, required: ["period", "salesFile"] }, ui);
    const props = Object.keys((stripped.schema.properties ?? {}) as Record<string, unknown>);
    expect(props).not.toContain("salesFile");
    expect(props).not.toContain("minutesFiles");
    expect(props).toContain("period");
    expect(stripped.schema.required).toEqual(["period"]);
    expect(stripped.uiSchema).not.toHaveProperty("salesFile");
    expect(stripped.uiSchema["ui:order"]).toEqual(["period", "remark", "allowMissingMinutes", "allowPartialPeriod"]);
  });
});
