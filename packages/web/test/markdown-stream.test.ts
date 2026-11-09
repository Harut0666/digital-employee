// 流式 Markdown：渲染前把说到一半的结构临时补全，按块切分让稳定的前面块不重渲染。
import { describe, expect, test } from "bun:test";
import { closeOpenMarkdown, splitBlocks } from "../src/view/markdown-stream";

describe("closeOpenMarkdown：代码围栏", () => {
  test("没有未闭合结构的文本原样返回", () => {
    expect(closeOpenMarkdown("")).toBe("");
    expect(closeOpenMarkdown("你好，世界")).toBe("你好，世界");
    expect(closeOpenMarkdown("```\nx\n```\n后续")).toBe("```\nx\n```\n后续");
  });

  test("未闭合的围栏补上闭合围栏（结尾有没有换行都行）", () => {
    expect(closeOpenMarkdown("```ts\nconst a = 1")).toBe("```ts\nconst a = 1\n```");
    expect(closeOpenMarkdown("```ts\nconst a = 1\n")).toBe("```ts\nconst a = 1\n```");
    expect(closeOpenMarkdown("~~~\ncode")).toBe("~~~\ncode\n~~~");
  });

  test("围栏里的反引号不算：内层较短的围栏不会闭合外层", () => {
    expect(closeOpenMarkdown("````md\n```js\nx\n```\n")).toBe("````md\n```js\nx\n```\n````");
    // 围栏里奇数个行内反引号不补
    expect(closeOpenMarkdown("```\na ` b")).toBe("```\na ` b\n```");
  });

  test("围栏闭合行说到一半（只写了前两个反引号）：先去掉，等写完", () => {
    expect(closeOpenMarkdown("```\nx\n``")).toBe("```\nx\n```");
    expect(closeOpenMarkdown("段落\n``")).toBe("段落\n");
  });
});

describe("closeOpenMarkdown：行内", () => {
  test("奇数个行内反引号补上，闭合了的不动", () => {
    expect(closeOpenMarkdown("用 `foo")).toBe("用 `foo`");
    expect(closeOpenMarkdown("``a ` b")).toBe("``a ` b``");
    expect(closeOpenMarkdown("`a` and `b`")).toBe("`a` and `b`");
  });

  test("刚写下的开头反引号后面还没有内容：先去掉", () => {
    expect(closeOpenMarkdown("用 `")).toBe("用 ");
  });

  test("粗体、斜体、删除线补闭合", () => {
    expect(closeOpenMarkdown("这是**重要")).toBe("这是**重要**");
    expect(closeOpenMarkdown("*斜体")).toBe("*斜体*");
    expect(closeOpenMarkdown("~~删掉")).toBe("~~删掉~~");
    expect(closeOpenMarkdown("**粗体**后面")).toBe("**粗体**后面");
  });

  test("嵌套按相反顺序闭合；代码里的星号不算", () => {
    expect(closeOpenMarkdown("**粗 *斜")).toBe("**粗 *斜***");
    expect(closeOpenMarkdown("`a**b`")).toBe("`a**b`");
    expect(closeOpenMarkdown("**粗 `code")).toBe("**粗 `code`**");
  });

  test("闭合标记写了一半：去掉半截再补全", () => {
    expect(closeOpenMarkdown("**粗体*")).toBe("**粗体**");
  });

  test("普通星号、列表符号、蛇形命名、转义不当成强调", () => {
    expect(closeOpenMarkdown("2 * 3")).toBe("2 * 3");
    expect(closeOpenMarkdown("* 条目")).toBe("* 条目");
    expect(closeOpenMarkdown("- 条目")).toBe("- 条目");
    expect(closeOpenMarkdown("snake_case_name")).toBe("snake_case_name");
    expect(closeOpenMarkdown("a \\*b")).toBe("a \\*b");
  });

  test("只看最后一段：前面段落里没配对的标记不影响", () => {
    expect(closeOpenMarkdown("a * b\n\n**x")).toBe("a * b\n\n**x**");
    expect(closeOpenMarkdown("**未配对\n\n后面的段落")).toBe("**未配对\n\n后面的段落");
  });

  test("链接写到一半：先显示文字，等地址写完再变成链接", () => {
    expect(closeOpenMarkdown("看[文档](http://exa")).toBe("看文档");
    expect(closeOpenMarkdown("看![图](http://exa")).toBe("看图");
    expect(closeOpenMarkdown("[文档](http://a.com)")).toBe("[文档](http://a.com)");
    expect(closeOpenMarkdown("看[文档")).toBe("看[文档");
  });
});

describe("closeOpenMarkdown：列表和表格半行", () => {
  test("表格分隔行说到一半：补齐列数，不让整张表塌回段落", () => {
    expect(closeOpenMarkdown("| a | b |\n|---|")).toBe("| a | b |\n|---|---|");
    expect(closeOpenMarkdown("| a | b | c |\n|--")).toBe("| a | b | c |\n|--|---|---|");
    expect(closeOpenMarkdown("| a | b |\n|---|---|\n| 1 |")).toBe("| a | b |\n|---|---|\n| 1 |");
  });

  test("段落下一行只有 - 或 =：会被当成标题下划线，先去掉", () => {
    expect(closeOpenMarkdown("标题\n-")).toBe("标题\n");
    expect(closeOpenMarkdown("标题\n--")).toBe("标题\n");
    expect(closeOpenMarkdown("标题\n\n-")).toBe("标题\n\n-");
  });

  test("列表半行原样保留", () => {
    expect(closeOpenMarkdown("- 一\n- 二\n- ")).toBe("- 一\n- 二\n- ");
    expect(closeOpenMarkdown("1. 一\n2")).toBe("1. 一\n2");
  });
});

describe("splitBlocks：按块切分", () => {
  test("空行分块，围栏里的空行不分", () => {
    expect(splitBlocks("甲\n\n乙")).toEqual(["甲", "乙"]);
    expect(splitBlocks("```\na\n\nb\n```\n\n后")).toEqual(["```\na\n\nb\n```", "后"]);
  });

  test("同一个列表中间有空行不拆开", () => {
    expect(splitBlocks("- 一\n\n- 二\n\n段落")).toEqual(["- 一\n\n- 二", "段落"]);
    expect(splitBlocks("1. 一\n\n   缩进内容\n\n2. 二")).toEqual(["1. 一\n\n   缩进内容\n\n2. 二"]);
  });

  test("前面的块在文字增长时字符串不变（稳定块可以 memo）", () => {
    const a = splitBlocks("甲\n\n乙\n\n丙");
    const b = splitBlocks("甲\n\n乙\n\n丙丁戊");
    expect(b.slice(0, 2)).toEqual(a.slice(0, 2));
    expect(b[2]).toBe("丙丁戊");
  });

  test("空文本没有块", () => {
    expect(splitBlocks("")).toEqual([]);
    expect(splitBlocks("\n\n")).toEqual([]);
  });
});
