import { describe, expect, test } from "bun:test";
import type { AgentToolView } from "../src/api/types";
import { errorLine, formatDuration, formatInput, formatOutput, lineDelta, toolStatus, toolStyle, toolSummary, turnSummary } from "../src/view/tool-rows";

const tool = (patch: Partial<AgentToolView>): AgentToolView => ({ id: "c1", tool: "read_file", ended: true, ...patch });

describe("工具名 → 图标和动词", () => {
  test("已知工具有动词标题", () => {
    expect(toolStyle("read_file").verb).toBe("读取文件");
    expect(toolStyle("write_file").verb).toBe("写入文件");
    expect(toolStyle("edit_file").verb).toBe("修改文件");
    expect(toolStyle("list_files").verb).toBe("列出文件");
  });

  test("即将加入的工具也有映射：glob、grep、shell、todo_write、ask_user_question", () => {
    expect(toolStyle("glob").verb).toBe("查找文件");
    expect(toolStyle("grep").verb).toBe("搜索");
    expect(toolStyle("shell").verb).toBe("运行命令");
    expect(toolStyle("todo_write").verb).toBe("更新待办");
    expect(toolStyle("ask_user_question").verb).toBe("提问");
  });

  test("未知工具不报错：通用图标，用原名显示", () => {
    expect(toolStyle("brand_new_tool")).toEqual({ verb: "brand_new_tool", icon: "tool" });
    expect(toolStyle("").verb).toBe("工具");
  });
});

describe("摘要", () => {
  test("文件类取相对路径，反斜杠换成斜杠，去掉 ./", () => {
    expect(toolSummary(tool({ args: { path: "./notes\\周会.md" } }))).toBe("notes/周会.md");
    expect(toolSummary(tool({ args: { file_path: "a/b.md" } }))).toBe("a/b.md");
  });

  test("绝对路径只留末尾几段", () => {
    expect(toolSummary(tool({ args: { path: "D:\\工作区\\交付\\2026\\周报.docx" } }))).toBe("…/交付/2026/周报.docx");
  });

  test("命令取命令说明或命令本身，搜索取搜索词", () => {
    expect(toolSummary(tool({ tool: "shell", args: { command: "bun test", description: "跑测试" } }))).toBe("跑测试");
    expect(toolSummary(tool({ tool: "shell", args: { command: "bun test" } }))).toBe("bun test");
    expect(toolSummary(tool({ tool: "grep", args: { pattern: "TODO" } }))).toBe("TODO");
    expect(toolSummary(tool({ tool: "glob", args: { pattern: "**/*.md" } }))).toBe("**/*.md");
  });

  test("没有认得的参数时留空，过长的截断", () => {
    expect(toolSummary(tool({ tool: "x", args: {} }))).toBe("");
    expect(toolSummary(tool({ tool: "x" }))).toBe("");
    expect(toolSummary(tool({ tool: "shell", args: { command: "x".repeat(300) } })).length).toBeLessThanOrEqual(120);
  });
});

describe("写入类的行数", () => {
  test("write_file：写入的行数全算新增", () => {
    expect(lineDelta(tool({ tool: "write_file", args: { path: "a", content: "一\n二\n三" } }))).toEqual({ added: 3, removed: 0 });
    expect(lineDelta(tool({ tool: "write_file", args: { path: "a", content: "一\n二\n" } }))).toEqual({ added: 2, removed: 0 });
  });

  test("edit_file：数差异里的 + 和 - 行（不数 +++ / ---）", () => {
    const diff = "--- a\n+++ a\n@@\n-旧一\n-旧二\n+新一\n 不变";
    expect(lineDelta(tool({ tool: "edit_file", args: { path: "a" }, result: { diff } }))).toEqual({ added: 1, removed: 2 });
  });

  test("edit_file 没有差异时按新旧文本的行数估", () => {
    expect(lineDelta(tool({ tool: "edit_file", args: { path: "a", old_string: "甲\n乙", new_string: "丙" } }))).toEqual({ added: 1, removed: 2 });
  });

  test("别的工具、没写成功的没有行数", () => {
    expect(lineDelta(tool({ tool: "read_file", args: { path: "a" } }))).toBeUndefined();
    expect(lineDelta(tool({ tool: "write_file", args: { path: "a", content: "x" }, isErr: true }))).toBeUndefined();
    expect(lineDelta(tool({ tool: "write_file", args: { path: "a", content: "x" }, ended: false }))).toBeUndefined();
  });
});

describe("状态用样式表达", () => {
  test("没结束：准备（还没有参数）或运行中，都是闪烁；结束：完成或出错", () => {
    expect(toolStatus(tool({ ended: false, args: undefined }))).toBe("pending");
    expect(toolStatus(tool({ ended: false, args: { path: "a" } }))).toBe("running");
    expect(toolStatus(tool({ ended: true }))).toBe("done");
    expect(toolStatus(tool({ ended: true, isErr: true }))).toBe("error");
  });

  test("出错时显示结果首行（原因优先）", () => {
    expect(errorLine(tool({ isErr: true, reason: "没有这个文件\n第二行" }))).toBe("没有这个文件");
    expect(errorLine(tool({ isErr: true, result: { message: "读不了" } }))).toBe("读不了");
    expect(errorLine(tool({ isErr: true, result: "出错了\n详情" }))).toBe("出错了");
    expect(errorLine(tool({ isErr: false, reason: "x" }))).toBe("");
  });
});

describe("展开体：输入和输出", () => {
  test("输入是缩进的参数 JSON，没有参数时说明", () => {
    expect(formatInput(tool({ args: { path: "a.md" } }))).toBe('{\n  "path": "a.md"\n}');
    expect(formatInput(tool({ args: undefined }))).toBe("（没有参数）");
  });

  test("输出：字符串原样，对象缩进成 JSON；过长截断并标出", () => {
    expect(formatOutput(tool({ result: "你好" }))).toEqual({ text: "你好", truncated: false, full: "你好" });
    expect(formatOutput(tool({ result: { ok: true } })).text).toBe('{\n  "ok": true\n}');
    const long = formatOutput(tool({ result: "行\n".repeat(500) }), 100);
    expect(long.truncated).toBe(true);
    expect(long.text.length).toBeLessThan(long.full.length);
  });

  test("没结束时没有输出；出错时输出原因", () => {
    expect(formatOutput(tool({ ended: false })).text).toBe("");
    expect(formatOutput(tool({ isErr: true, reason: "被拒绝" })).text).toBe("被拒绝");
  });
});

describe("一轮过程的收尾", () => {
  test("用时：秒、分秒", () => {
    expect(formatDuration(800)).toBe("1 秒");
    expect(formatDuration(12_400)).toBe("12 秒");
    expect(formatDuration(65_000)).toBe("1 分 05 秒");
    expect(formatDuration(3_600_000)).toBe("60 分 00 秒");
  });

  test("已完成 · 用时 X；没有时间戳就不写用时；出错数一并写出", () => {
    expect(turnSummary([tool({}), tool({ id: "c2" })], 1000, 13_000)).toBe("已完成 · 用时 12 秒 · 2 步");
    expect(turnSummary([tool({})], undefined, undefined)).toBe("已完成 · 1 步");
    expect(turnSummary([tool({}), tool({ id: "c2", isErr: true })], 0, 5000)).toBe("已完成 · 用时 5 秒 · 2 步，1 步出错");
  });
});
