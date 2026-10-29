// 表单字段用途（x-purpose）：契约常量与解析、写作要求收集、保存前的校验。
import { describe, expect, test } from "bun:test";
import {
  FORM_PARAM_NAMES,
  FORM_PURPOSE_WRITING_REQUIREMENT,
  formatFormPurpose,
  isFormParamName,
  parseFormPurpose,
} from "../../src/contract";
import { collectWritingRequirements, validateFormSchemaPurposes } from "../../src/config/form";

describe("x-purpose 契约", () => {
  test("writing_requirement 与 param:<已支持参数> 都能解析", () => {
    expect(parseFormPurpose(FORM_PURPOSE_WRITING_REQUIREMENT)).toEqual({ ok: true, purpose: { kind: "writing_requirement" } });
    expect(parseFormPurpose("param:period")).toEqual({ ok: true, purpose: { kind: "param", param: "period" } });
    expect(formatFormPurpose({ kind: "writing_requirement" })).toBe("writing_requirement");
    expect(formatFormPurpose({ kind: "param", param: "period" })).toBe("param:period");
    expect(formatFormPurpose(undefined)).toBeUndefined();
  });

  test("缺省（undefined / null / 空字符串）表示没有用途", () => {
    expect(parseFormPurpose(undefined)).toEqual({ ok: true });
    expect(parseFormPurpose(null)).toEqual({ ok: true });
    expect(parseFormPurpose("")).toEqual({ ok: true });
  });

  test("未知用途、未知参数、非字符串都给出逐条错误", () => {
    const unknownPurpose = parseFormPurpose("write_something");
    expect(unknownPurpose.ok).toBe(false);
    expect(!unknownPurpose.ok && unknownPurpose.message).toContain("writing_requirement");

    const unknownParam = parseFormPurpose("param:notAThing");
    expect(unknownParam.ok).toBe(false);
    expect(!unknownParam.ok && unknownParam.message).toContain("notAThing");
    expect(!unknownParam.ok && unknownParam.message).toContain("period");

    const notString = parseFormPurpose(42);
    expect(notString.ok).toBe(false);
  });

  test("已支持参数是契约里的常量，且包含统计周期", () => {
    expect(FORM_PARAM_NAMES).toContain("period");
    expect(isFormParamName("period")).toBe(true);
    expect(isFormParamName("nope")).toBe(false);
  });
});

describe("collectWritingRequirements：发起人的写作要求", () => {
  const schema = {
    type: "object",
    properties: {
      period: { type: "object", title: "统计周期", "x-purpose": "param:period" },
      focusRegion: { type: "string", title: "重点关注区域", "x-purpose": "writing_requirement" },
      remark: { type: "string", title: "备注", "x-purpose": "writing_requirement" },
      detail: {
        type: "object",
        title: "分组",
        properties: { tone: { type: "string", title: "语气", "x-purpose": "writing_requirement" } },
      },
    },
  };

  test("取标题和取值，跳过空值，顺序跟 schema 一致", () => {
    const requirements = collectWritingRequirements(schema, {
      period: { year: 2026, week: 44 },
      focusRegion: "华东",
      remark: "",
      detail: { tone: "平实" },
    });
    expect(requirements).toEqual([
      { field: "focusRegion", title: "重点关注区域", value: "华东", text: "重点关注区域：华东" },
      { field: "detail.tone", title: "语气", value: "平实", text: "语气：平实" },
    ]);
  });

  test("0 和 false 是有效取值；没有标用途的字段不收集", () => {
    const numeric = {
      type: "object",
      properties: {
        maxPages: { type: "integer", title: "篇幅上限", "x-purpose": "writing_requirement" },
        keepTable: { type: "boolean", title: "保留表格", "x-purpose": "writing_requirement" },
        others: { type: "string", title: "没标用途" },
      },
    };
    expect(collectWritingRequirements(numeric, { maxPages: 3, keepTable: false, others: "忽略" })).toEqual([
      { field: "maxPages", title: "篇幅上限", value: 3, text: "篇幅上限：3" },
      { field: "keepTable", title: "保留表格", value: false, text: "保留表格：false" },
    ]);
  });

  test("没有 title 时用字段名；x-purpose 在数组元素的 schema 上也生效", () => {
    const arraySchema = {
      type: "object",
      properties: {
        highlights: {
          type: "array",
          title: "要点",
          items: { type: "string", "x-purpose": "writing_requirement", title: "要点" },
        },
        plain: { type: "string", "x-purpose": "writing_requirement" },
      },
    };
    const requirements = collectWritingRequirements(arraySchema, { highlights: ["先写结论", "再写数据"], plain: "简短" });
    expect(requirements.map((r) => r.field)).toEqual(["highlights[0]", "highlights[1]", "plain"]);
    expect(requirements[0]).toEqual({ field: "highlights[0]", title: "要点", value: "先写结论", text: "要点：先写结论" });
    expect(requirements[2]).toEqual({ field: "plain", title: "plain", value: "简短", text: "plain：简短" });
  });

  test("没有写作要求字段时返回空数组", () => {
    expect(collectWritingRequirements({ type: "object", properties: { a: { type: "string" } } }, { a: "x" })).toEqual([]);
  });
});

describe("validateFormSchemaPurposes：保存前校验用途", () => {
  test("合法用途没有错误", () => {
    expect(
      validateFormSchemaPurposes({
        type: "object",
        properties: {
          period: { type: "object", "x-purpose": "param:period" },
          focusRegion: { type: "string", "x-purpose": "writing_requirement" },
        },
      }),
    ).toEqual([]);
  });

  test("未知用途、未知参数、嵌套字段的错误都带字段路径", () => {
    const errors = validateFormSchemaPurposes({
      type: "object",
      properties: {
        focusRegion: { type: "string", "x-purpose": "requirement" },
        detail: { type: "object", properties: { tone: { type: "string", "x-purpose": "param:whatever" } } },
      },
    });
    expect(errors).toHaveLength(2);
    expect(errors[0]).toContain("properties.focusRegion.x-purpose");
    expect(errors[0]).toContain("writing_requirement");
    expect(errors[1]).toContain("properties.detail.properties.tone.x-purpose");
    expect(errors[1]).toContain("whatever");
  });
});
