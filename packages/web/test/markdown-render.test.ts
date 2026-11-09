// 渲染冒烟：用服务端渲染看 Markdown 组件的输出（偏好上下文换成固定的链接属性）。
import { describe, expect, mock, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { createElement } from "react";

mock.module("../src/components/Preferences", () => ({ useLinkProps: () => () => ({}) }));
const { Markdown } = await import("../src/components/Markdown");

const html = (text: string, streaming = false) => renderToStaticMarkup(createElement(Markdown, { text, streaming }));

describe("Markdown 组件（服务端渲染冒烟）", () => {
  test("GFM：表格、任务列表、删除线", () => {
    const out = html("| a | b |\n|---|---|\n| 1 | 2 |\n\n- [x] 完成\n- [ ] 待办\n\n~~删~~");
    expect(out).toContain("<table>");
    expect(out).toContain('type="checkbox"');
    expect(out).toContain("<del>");
  });

  test("代码块有语言标签和复制按钮，行内代码用 code", () => {
    const out = html("```ts\nconst a = 1;\n```\n\n用 `x` 表示");
    expect(out).toContain("md-code-lang");
    expect(out).toContain("ts");
    expect(out).toContain("复制");
    expect(out).toContain("<code>x</code>");
  });

  test("外链新窗口且带 noopener", () => {
    const out = html("[官网](https://example.com)");
    expect(out).toContain('target="_blank"');
    expect(out).toContain("noopener");
  });

  test("不渲染原始 HTML，也不放行 javascript: 链接", () => {
    const out = html('<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\n[点我](javascript:alert(1))');
    expect(out).not.toContain("<script");
    expect(out).not.toContain("<img");
    expect(out).not.toContain("javascript:");
  });

  test("流式：未闭合的围栏和粗体也能渲染成完整结构", () => {
    const out = html("```py\nprint(1)\n```\n\n先说明一下**重点", true);
    expect(out).toContain("<strong>");
    expect(out).toContain("md-code");
    expect(out).toContain("md-live");
    expect(html("```py\nprint(1)", true)).toContain("md-code");
  });
});
