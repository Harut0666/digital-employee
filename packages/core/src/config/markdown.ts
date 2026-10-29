// 配置页与配置文件之间的中间层（docs/design.md §4.1）：
//   SKILL.md     岗位说明页的三个文本框 ↔ “岗位职责 / 其余写作要求 / 注意事项”三个区域
//   frontdesk.md 前台 Agent 页的人设文本框 + 只读工具勾选 ↔ markdown 正文 + 末尾工具段落
//
// 两个函数都按“在原文上原地替换”的方式工作：没有改动的区域逐字节保留，
// 这样保存一次配置不会因为重新排版而制造无意义的差异。

import {
  FRONTDESK_READONLY_TOOLS,
  isFrontdeskReadonlyTool,
  type FrontdeskDoc,
  type FrontdeskReadonlyToolName,
  type SkillSections,
} from "../contract";

export type { FrontdeskDoc, SkillSections };

const DUTIES_HEADING = "岗位职责";
const CAUTIONS_HEADING = "注意事项";
export const FRONTDESK_TOOLS_HEADING = "开放给你的只读工具";

function lineText(line: string): string {
  return line.replace(/\r$/, "");
}

function headingName(line: string): string | undefined {
  const trimmed = lineText(line).trimEnd();
  if (!/^##\s+/.test(trimmed)) return undefined;
  return trimmed.replace(/^##\s+/, "").trim();
}

function findHeading(lines: string[], name: string): number {
  return lines.findIndex((line) => headingName(line) === name);
}

function nextHeading(lines: string[], from: number): number {
  for (let i = from; i < lines.length; i++) if (headingName(lines[i]!) !== undefined) return i;
  return lines.length;
}

/** SKILL.md 三个可编辑区域的内容（类型定义在 contract/config-api.ts）。 */
export function splitSkill(markdown: string): SkillSections {
  const lines = markdown.split("\n");
  const dutiesIdx = findHeading(lines, DUTIES_HEADING);
  const cautionsIdx = findHeading(lines, CAUTIONS_HEADING);

  let duties = "";
  let writingStart = 0;
  if (dutiesIdx >= 0) {
    const dutiesEnd = nextHeading(lines, dutiesIdx + 1);
    duties = lines.slice(dutiesIdx + 1, dutiesEnd).join("\n");
    writingStart = dutiesEnd;
  }
  const writingEnd = cautionsIdx >= 0 && cautionsIdx >= writingStart ? cautionsIdx : lines.length;
  const writing = lines.slice(writingStart, writingEnd).join("\n");
  const cautions = cautionsIdx >= 0 ? lines.slice(cautionsIdx + 1).join("\n") : "";
  return { duties, writing, cautions };
}

function trimTrailingEmpty(lines: string[]): void {
  while (lines.length > 0 && lines[lines.length - 1]!.trim() === "") lines.pop();
}

/**
 * 把三个文本框拼回 SKILL.md。原文里有某个章节标题时按原位置替换内容；
 * 原文没有该章节、而文本框里写了内容时，把它追加到文件末尾。
 */
export function joinSkill(sections: SkillSections, original: string): string {
  const lines = original.split("\n");
  const dutiesIdx = findHeading(lines, DUTIES_HEADING);
  const cautionsIdx = findHeading(lines, CAUTIONS_HEADING);
  const hasDuties = dutiesIdx >= 0;
  const hasCautions = cautionsIdx >= 0;

  const out: string[] = [];
  if (hasDuties) out.push(...lines.slice(0, dutiesIdx + 1));
  out.push(...sections.writing.split("\n"));
  if (hasDuties) {
    // 写作要求插在“## 岗位职责”之后；岗位职责内容先放进去
    out.splice(dutiesIdx + 1, 0, ...sections.duties.split("\n"));
  } else if (sections.duties.trim() !== "") {
    trimTrailingEmpty(out);
    out.push("", `## ${DUTIES_HEADING}`, "", ...sections.duties.replace(/\s+$/, "").split("\n"), "");
  }
  if (hasCautions) {
    out.push(...lines.slice(cautionsIdx, cautionsIdx + 1), ...sections.cautions.split("\n"));
  } else if (sections.cautions.trim() !== "") {
    trimTrailingEmpty(out);
    out.push("", `## ${CAUTIONS_HEADING}`, "", ...sections.cautions.replace(/\s+$/, "").split("\n"), "");
  }
  return out.join("\n");
}

// ---------------------------------------------------------------- frontdesk.md

const DEFAULT_FRONTDESK_TOOLS = FRONTDESK_READONLY_TOOLS.map((t) => t.name);

function normalizeBody(lines: string[]): string {
  const text = lines.join("\n").replace(/\s+$/, "");
  return text === "" ? "" : `${text}\n`;
}

function toolLabel(name: string): string {
  return FRONTDESK_READONLY_TOOLS.find((t) => t.name === name)?.label ?? name;
}

export function parseFrontdesk(text: string): FrontdeskDoc {
  const lines = text.split("\n");
  const idx = findHeading(lines, FRONTDESK_TOOLS_HEADING);
  if (idx < 0) return { body: normalizeBody(lines), tools: [...DEFAULT_FRONTDESK_TOOLS] };

  const end = nextHeading(lines, idx + 1);
  const tools: FrontdeskReadonlyToolName[] = [];
  for (const line of lines.slice(idx + 1, end)) {
    const match = /^\s*[-*]\s+([a-z_][a-z0-9_]*)/.exec(lineText(line));
    if (!match) continue;
    const name = match[1]!;
    if (isFrontdeskReadonlyTool(name)) tools.push(name);
  }
  const body = normalizeBody([...lines.slice(0, idx), ...lines.slice(end)]);
  return { body, tools: tools.length > 0 ? tools : [...DEFAULT_FRONTDESK_TOOLS] };
}

export function formatFrontdesk(doc: FrontdeskDoc): string {
  const body = doc.body.replace(/\s+$/, "");
  const tools = doc.tools.length > 0 ? doc.tools : [...DEFAULT_FRONTDESK_TOOLS];
  const list = tools.map((name) => `- ${name}：${toolLabel(name)}`).join("\n");
  return `${body === "" ? "" : `${body}\n\n`}## ${FRONTDESK_TOOLS_HEADING}\n\n${list}\n`;
}
