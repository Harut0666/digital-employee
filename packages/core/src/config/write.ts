// 配置文件的“先校验、再生成写回文本”（docs/design.md §4.1）。
//
//   workflow.yaml / qc.yaml   用契约里的 zod 校验；结构化保存时把改动合进原有的 YAML Document，
//                             保留文件里的注释（表单页保存不该把注释洗掉）。
//   form.schema.json          用 ajv 编译校验（不是合法 JSON Schema 就拒绝），并检查每个 x-purpose。
//   schemas/*.json            判断步骤的输出 Schema，同样用 ajv 编译校验。
//   SKILL.md / frontdesk.md   由三个文本框 / 人设 + 工具勾选拼回原文（见 markdown.ts）。
//
// 校验失败时返回逐条中文错误，调用方不写任何文件。

import Ajv from "ajv";
import { isMap, isSeq, parse as parseYaml, parseDocument, stringify, type Document } from "yaml";
import { EmployeeWorkflowSchema, QcConfigSchema, isFrontdeskReadonlyTool, type EmployeeWorkflow } from "../contract";
import { workflowConsistencyProblems } from "./consistency";
import { normalizeConfigPath } from "./files";
import { validateFormSchemaPurposes } from "./form";
import { formatFrontdesk, joinSkill, type FrontdeskDoc, type SkillSections } from "./markdown";

const ajv = new Ajv({ allErrors: true, strict: false, allowUnionTypes: true });

export interface ConfigFileInput {
  /** 源文件视图：用户编辑的原文 */
  content?: string;
  /** 表单视图：界面上的结构化对象 */
  data?: unknown;
}

export type PrepareConfigResult = { ok: true; text: string } | { ok: false; errors: string[] };

type PickedInput =
  | { ok: true; kind: "content"; content: string }
  | { ok: true; kind: "data"; data: unknown }
  | { ok: false; errors: string[] };

/** 通过 pickInput 之后的输入（content 与 data 二选一，必居其一）。 */
type ValidInput = Extract<PickedInput, { ok: true }>;

function fail(...errors: string[]): { ok: false; errors: string[] } {
  return { ok: false, errors };
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function pickInput(input: ConfigFileInput): PickedInput {
  const hasContent = input.content !== undefined;
  const hasData = input.data !== undefined;
  if (hasContent && hasData) return fail("content 与 data 只能二选一");
  if (hasContent) {
    if (typeof input.content !== "string") return fail("content 必须是字符串");
    return { ok: true, kind: "content", content: input.content };
  }
  if (hasData) return { ok: true, kind: "data", data: input.data };
  return fail("缺少 content 或 data");
}

interface ZodIssue {
  path: (string | number | symbol)[];
  message: string;
}

function zodErrors(file: string, issues: ZodIssue[]): string[] {
  return issues.map((issue) => `${file}：${issue.path.length > 0 ? issue.path.map(String).join(".") : "（整体）"} ${issue.message}`);
}

function parseJsonText(file: string, text: string): { ok: true; value: unknown } | { ok: false; errors: string[] } {
  try {
    return { ok: true, value: JSON.parse(text) };
  } catch (error) {
    return { ok: false, errors: [`${file} 不是合法的 JSON：${(error as Error).message}`] };
  }
}

function compileSchema(file: string, schema: unknown): string[] {
  try {
    ajv.compile(schema as object);
    return [];
  } catch (error) {
    return [`${file} 不是可编译的 JSON Schema：${(error as Error).message}`];
  }
}

function stringifyJson(value: unknown): string {
  return `${JSON.stringify(value, null, 2)}\n`;
}

// ---------------------------------------------------------------- YAML：保留注释

/** 把结构化数据合进已有的 YAML Document：只替换变化的节点，注释与格式尽量保留。 */
function applyYamlData(currentText: string, data: unknown): string {
  const doc = parseDocument(currentText);
  if (isPlainObject(data) && isMap(doc.contents)) mergeNode(doc, doc.contents, data, []);
  // 原文不是映射（损坏或被清空）时整份重写；yaml 的 Parsed 类型比 createNode 的返回更窄，这里只能断言
  else doc.contents = doc.createNode(data) as typeof doc.contents;
  return doc.toString();
}

function mergeNode(doc: Document, node: unknown, data: unknown, path: (string | number)[]): void {
  if (isMap(node) && isPlainObject(data)) {
    const keys = node.items.map((pair) => String((pair.key as { value?: unknown } | null)?.value ?? "")).filter((key) => key !== "");
    for (const key of keys) if (!(key in data)) doc.deleteIn([...path, key]);
    for (const [key, value] of Object.entries(data)) {
      const pair = node.items.find((p) => String((p.key as { value?: unknown } | null)?.value ?? "") === key);
      const child = pair?.value;
      if (child !== undefined && child !== null) {
        if ((isMap(child) && isPlainObject(value)) || (isSeq(child) && Array.isArray(value))) {
          mergeNode(doc, child, value, [...path, key]);
          continue;
        }
      }
      doc.setIn([...path, key], value);
    }
    return;
  }
  if (isSeq(node) && Array.isArray(data) && node.items.length === data.length) {
    const mergable = node.items.every((item, index) => {
      const value = data[index];
      return (isMap(item) && isPlainObject(value)) || (isSeq(item) && Array.isArray(value));
    });
    if (mergable) {
      node.items.forEach((item, index) => mergeNode(doc, item, data[index], [...path, index]));
      return;
    }
  }
  if (path.length === 0) doc.contents = doc.createNode(data);
  else doc.setIn(path, data);
}

function isSkillSections(value: unknown): value is SkillSections {
  if (!isPlainObject(value)) return false;
  return ["duties", "writing", "cautions"].every((key) => typeof value[key] === "string");
}

function isFrontdeskDoc(value: unknown): value is FrontdeskDoc {
  if (!isPlainObject(value)) return false;
  return typeof value.body === "string" && Array.isArray(value.tools) && value.tools.every((t) => typeof t === "string");
}

// ---------------------------------------------------------------- 分文件校验

function prepareWorkflow(input: ValidInput, currentText: string | undefined): PrepareConfigResult {
  let data: unknown;
  if (input.kind === "content") {
    try {
      data = parseYaml(input.content);
    } catch (error) {
      return fail(`workflow.yaml 不是合法的 YAML：${(error as Error).message}`);
    }
  } else {
    data = input.data;
  }
  const parsed = EmployeeWorkflowSchema.safeParse(data);
  if (!parsed.success) return fail(...zodErrors("workflow.yaml", parsed.error.issues));
  const problems = workflowConsistencyProblems(parsed.data as EmployeeWorkflow);
  if (problems.length > 0) return fail(...problems);
  if (input.kind === "content") return { ok: true, text: input.content };
  return { ok: true, text: currentText ? applyYamlData(currentText, data) : `${stringify(data)}` };
}

function prepareQc(input: ValidInput, currentText: string | undefined): PrepareConfigResult {
  let data: unknown;
  if (input.kind === "content") {
    try {
      data = parseYaml(input.content);
    } catch (error) {
      return fail(`qc.yaml 不是合法的 YAML：${(error as Error).message}`);
    }
  } else {
    data = input.data;
  }
  const parsed = QcConfigSchema.safeParse(data);
  if (!parsed.success) return fail(...zodErrors("qc.yaml", parsed.error.issues));
  if (input.kind === "content") return { ok: true, text: input.content };
  return { ok: true, text: currentText ? applyYamlData(currentText, data) : `${stringify(data)}` };
}

function prepareFormSchema(input: ValidInput, file: "form.schema.json" | "form.ui.json"): PrepareConfigResult {
  let data: unknown;
  if (input.kind === "content") {
    const parsed = parseJsonText(file, input.content);
    if (!parsed.ok) return fail(...parsed.errors);
    data = parsed.value;
  } else {
    data = input.data;
  }
  if (!isPlainObject(data)) return fail(`${file} 必须是 JSON 对象`);
  const errors: string[] = [];
  if (file === "form.schema.json") {
    errors.push(...compileSchema(file, data), ...validateFormSchemaPurposes(data));
  }
  if (errors.length > 0) return fail(...errors);
  return { ok: true, text: input.kind === "content" ? input.content : stringifyJson(data) };
}

function prepareOutputSchema(input: ValidInput, file: string): PrepareConfigResult {
  let data: unknown;
  if (input.kind === "content") {
    const parsed = parseJsonText(file, input.content);
    if (!parsed.ok) return fail(...parsed.errors);
    data = parsed.value;
  } else {
    data = input.data;
  }
  if (!isPlainObject(data)) return fail(`${file} 必须是 JSON 对象`);
  const errors = compileSchema(file, data);
  if (errors.length > 0) return fail(...errors);
  return { ok: true, text: input.kind === "content" ? input.content : stringifyJson(data) };
}

/**
 * 校验一次配置写入并给出应当落盘的文本。
 * @param relPath      相对员工目录的路径，必须在白名单内
 * @param input        原文（源文件视图）或结构化数据（表单视图）
 * @param currentText  当前文件内容；文件不存在时为 undefined
 */
export function prepareConfigText(relPath: unknown, input: ConfigFileInput, currentText: string | undefined): PrepareConfigResult {
  const path = normalizeConfigPath(relPath);
  if (!path) return fail(`不允许写入配置文件：${String(relPath)}`);
  const picked = pickInput(input);
  if (!picked.ok) return picked;

  if (path === "workflow.yaml") return prepareWorkflow(picked, currentText);
  if (path === "qc.yaml") return prepareQc(picked, currentText);
  if (path === "form.schema.json" || path === "form.ui.json") return prepareFormSchema(picked, path);

  if (path === "SKILL.md") {
    if (picked.kind === "content") return { ok: true, text: picked.content };
    if (!isSkillSections(picked.data)) return fail("SKILL.md 需要 duties / writing / cautions 三个字符串");
    return { ok: true, text: joinSkill(picked.data, currentText ?? "") };
  }

  if (path === "frontdesk.md") {
    if (picked.kind === "content") return { ok: true, text: picked.content };
    if (!isFrontdeskDoc(picked.data)) return fail("frontdesk.md 需要 body（字符串）和 tools（字符串数组）");
    const bad = picked.data.tools.filter((tool) => !isFrontdeskReadonlyTool(tool));
    if (bad.length > 0) return fail(`前台 Agent 不能开放这些工具：${bad.join("、")}（只读工具由契约列出）`);
    return { ok: true, text: formatFrontdesk(picked.data) };
  }

  if (path.startsWith("prompts/")) {
    if (picked.kind === "data") return fail("提示词文件只接受原文 content");
    return { ok: true, text: picked.content };
  }

  return prepareOutputSchema(picked, path);
}
