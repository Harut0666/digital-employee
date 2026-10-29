// 表单字段用途（x-purpose）的读取与校验（docs/design.md §4.1）。
//
// form.schema.json 是普通 JSON Schema，字段上额外带一个 x-purpose：
//   writing_requirement  取值的标题 + 内容作为“发起人的写作要求”注入每个判断步骤（见 engine/index.ts）
//   param:<参数名>       绑定到程序已支持的参数，取值必须在 contract/form.ts 的 FORM_PARAMS 里
//
// 解析与保存校验放在一起，路由只在保存前调用 validateFormSchemaPurposes。

import { parseFormPurpose, type WritingRequirement } from "../contract";

type Schema = Record<string, unknown>;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** 空字符串、null、undefined、空数组、空对象都不算一条写作要求。 */
function hasValue(value: unknown): boolean {
  if (value === undefined || value === null || value === "") return false;
  if (Array.isArray(value)) return value.length > 0;
  if (typeof value === "object") return Object.keys(value as Record<string, unknown>).length > 0;
  return true;
}

function formatValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  return JSON.stringify(value);
}

function titleOf(schema: Schema, fallback: string): string {
  const title = schema["title"];
  return typeof title === "string" && title.trim() !== "" ? title : fallback;
}

function lastSegment(path: string): string {
  const parts = path.split(/\.|\[/).filter((p) => p !== "");
  return parts[parts.length - 1] ?? path;
}

function writingPurpose(schema: Schema): boolean {
  const parsed = parseFormPurpose(schema["x-purpose"]);
  return parsed.ok && parsed.purpose?.kind === "writing_requirement";
}

function collectFromSchema(schema: Schema, value: unknown, path: string, out: WritingRequirement[]): void {
  if (!hasValue(value)) return;

  // 数组字段：x-purpose 既可以标在数组字段上（整组算一条），也可以标在 items 上（逐项算一条）
  if (Array.isArray(value)) {
    const items = schema["items"];
    if (writingPurpose(schema)) {
      out.push({ field: path, title: titleOf(schema, lastSegment(path)), value, text: `${titleOf(schema, lastSegment(path))}：${formatValue(value)}` });
      return;
    }
    if (isRecord(items)) {
      value.forEach((item, index) => {
        const itemPath = `${path}[${index}]`;
        if (writingPurpose(items)) {
          if (!hasValue(item)) return;
          out.push({ field: itemPath, title: titleOf(items, lastSegment(path)), value: item, text: `${titleOf(items, lastSegment(path))}：${formatValue(item)}` });
          return;
        }
        collectFromSchema(items, item, itemPath, out);
      });
    }
    return;
  }

  if (writingPurpose(schema)) {
    const title = titleOf(schema, lastSegment(path));
    out.push({ field: path, title, value, text: `${title}：${formatValue(value)}` });
  }

  const properties = schema["properties"];
  if (isRecord(properties) && isRecord(value)) {
    for (const [key, child] of Object.entries(properties)) {
      if (!isRecord(child)) continue;
      collectFromSchema(child, value[key], path === "" ? key : `${path}.${key}`, out);
    }
  }
}

/** 收集所有 x-purpose: writing_requirement 字段的标题和取值（按 schema 中的顺序）。 */
export function collectWritingRequirements(formSchema: unknown, form: Record<string, unknown> | undefined): WritingRequirement[] {
  const out: WritingRequirement[] = [];
  if (!isRecord(formSchema)) return out;
  collectFromSchema(formSchema, form ?? {}, "", out);
  return out;
}

function checkPurpose(schema: unknown, path: string, errors: string[]): void {
  if (!isRecord(schema) || !Object.prototype.hasOwnProperty.call(schema, "x-purpose")) return;
  const parsed = parseFormPurpose(schema["x-purpose"]);
  if (!parsed.ok) errors.push(`${path}.x-purpose：${parsed.message}`);
}

/** 递归检查一个对象的 properties 以及子层级的 items。path 形如 properties.period.properties.year。 */
function validateProperties(schema: unknown, path: string, errors: string[]): void {
  if (!isRecord(schema)) return;
  const properties = schema["properties"];
  if (!isRecord(properties)) return;
  for (const [key, child] of Object.entries(properties)) {
    const childPath = `${path}.${key}`;
    checkPurpose(child, childPath, errors);
    if (!isRecord(child)) continue;
    validateProperties(child, `${childPath}.properties`, errors);
    const items = child["items"];
    if (isRecord(items)) {
      checkPurpose(items, `${childPath}.items`, errors);
      validateProperties(items, `${childPath}.items.properties`, errors);
    }
  }
}

/** 保存前的用途校验：返回逐条错误（空数组表示全部合法）。 */
export function validateFormSchemaPurposes(formSchema: unknown): string[] {
  const errors: string[] = [];
  if (!isRecord(formSchema)) return ["表单 Schema 必须是 JSON 对象"];
  checkPurpose(formSchema, "", errors);
  validateProperties(formSchema, "properties", errors);
  return errors;
}
