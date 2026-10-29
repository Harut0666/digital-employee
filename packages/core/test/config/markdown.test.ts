// SKILL.md 三章节拆分与 frontdesk.md 只读工具段落：配置页表单写回文件的中间层。
import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { FRONTDESK_READONLY_TOOLS, isFrontdeskReadonlyTool } from "../../src/contract";
import { formatFrontdesk, joinSkill, parseFrontdesk, splitSkill } from "../../src/config/markdown";

const repoRoot = join(import.meta.dir, "..", "..", "..", "..");
const weeklySkill = readFileSync(join(repoRoot, "employees", "weekly-report", "SKILL.md"), "utf8");
const weeklyFrontdesk = readFileSync(join(repoRoot, "employees", "weekly-report", "frontdesk.md"), "utf8");
const miniSkill = readFileSync(join(import.meta.dir, "..", "fixtures", "employees", "mini", "SKILL.md"), "utf8");

describe("splitSkill / joinSkill：三个文本框写回 SKILL.md 的三个章节", () => {
  test("拆分后原样拼回，逐字节一致", () => {
    expect(joinSkill(splitSkill(weeklySkill), weeklySkill)).toBe(weeklySkill);
    expect(joinSkill(splitSkill(miniSkill), miniSkill)).toBe(miniSkill);
  });

  test("三个文本框分别是岗位职责、其余写作要求、注意事项", () => {
    const sections = splitSkill(weeklySkill);
    expect(sections.duties).toContain("青禾烘焙运营部");
    expect(sections.writing).toContain("## 最重要的规则");
    expect(sections.writing).toContain("## 周报怎么写");
    expect(sections.cautions).toContain("不得编造任何事实");
    // 三个文本框覆盖除标题外的全部正文，不丢内容
    for (const needle of ["### 1. 数字只写槽位", "### 2. 原因只能引用纪要原文", "## 重做时"]) {
      expect(`${sections.writing}${sections.cautions}`).toContain(needle);
    }
  });

  test("只改岗位职责时，其他章节原样保留", () => {
    const sections = splitSkill(weeklySkill);
    const next = joinSkill({ ...sections, duties: "新的岗位职责。\n" }, weeklySkill);
    expect(next).toContain("新的岗位职责。");
    expect(next).not.toContain("青禾烘焙运营部");
    expect(next).toContain("## 最重要的规则");
    expect(next).toContain("## 注意事项");
    expect(next.endsWith(sections.cautions)).toBe(true);
  });

  test("没有岗位职责章节的文件：三个文本框拼回原文；为空的新章节不会被加进去", () => {
    const sections = splitSkill(miniSkill);
    expect(sections.duties).toBe("");
    expect(sections.cautions).toBe("");
    expect(joinSkill({ writing: miniSkill, duties: "", cautions: "" }, miniSkill)).toBe(miniSkill);

    const withDuties = joinSkill({ ...sections, duties: "写周报。" }, miniSkill);
    expect(withDuties).toContain("## 岗位职责");
    expect(withDuties).toContain("写周报。");
    expect(withDuties).toContain("只用槽位引用数字");
  });
});

describe("frontdesk.md：人设 + 只读工具", () => {
  test("解析出只读工具并去掉该段落，拼回后逐字节一致", () => {
    const doc = parseFrontdesk(weeklyFrontdesk);
    expect(doc.tools).toEqual(["query_task"]);
    expect(doc.body).toContain("前台 Agent");
    expect(doc.body).not.toContain("开放给你的只读工具");
    expect(formatFrontdesk(doc)).toBe(weeklyFrontdesk);
  });

  test("没有该段落时按契约默认只读工具；保存时补上段落", () => {
    const doc = parseFrontdesk("# 人格\n\n说话简短。\n");
    expect(doc.tools).toEqual(FRONTDESK_READONLY_TOOLS.map((t) => t.name));
    const formatted = formatFrontdesk({ ...doc, tools: ["query_task"] });
    expect(formatted).toContain("## 开放给你的只读工具");
    expect(parseFrontdesk(formatted).body).toBe("# 人格\n\n说话简短。\n");
  });

  test("只读工具只能是契约里列出的那些", () => {
    for (const tool of FRONTDESK_READONLY_TOOLS) expect(isFrontdeskReadonlyTool(tool.name)).toBe(true);
    expect(isFrontdeskReadonlyTool("web_search")).toBe(false);
    expect(isFrontdeskReadonlyTool("fill_form_draft")).toBe(false);
  });
});
