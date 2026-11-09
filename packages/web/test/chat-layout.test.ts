import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const css = (name: string) => readFileSync(join(import.meta.dir, "../src/styles", name), "utf8");

/** 取出某个选择器的第一条规则的声明块；找不到返回 undefined。 */
function ruleBody(source: string, selector: string): string | undefined {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\>]/g, "\\$&");
  const match = new RegExp(`(?:^|\\})\\s*${escaped}\\s*\\{([^}]*)\\}`, "m").exec(source);
  return match?.[1];
}

describe("对话流布局：流程卡片不能被压扁", () => {
  // .card 设了 overflow:hidden，作为 flex 列容器 .chat-body 的子项时 min-height:auto 会变成 0，
  // 内容一多就被 flex-shrink 压成一条。直接子项一律不收缩，高度超出由 .chat-body 自己滚动。
  test(".chat-body 的直接子项不收缩", () => {
    expect(ruleBody(css("chat.css"), ".chat-body > *")).toMatch(/flex-shrink:\s*0/);
  });

  test(".chat-body 仍然是可滚动的列容器（设置、员工配置、知识库页也用它）", () => {
    const body = ruleBody(css("chat.css"), ".chat-body") ?? "";
    expect(body).toMatch(/overflow:\s*auto/);
    expect(body).toMatch(/flex-direction:\s*column/);
  });

  test("步骤行列表的子项也不收缩（max-height 的 flex 列）", () => {
    expect(ruleBody(css("chat.css"), ".step-rows > *")).toMatch(/flex-shrink:\s*0/);
  });

  test("卡片本身保持 overflow:hidden（左侧色条靠它裁切），所以收缩保护必须在容器侧", () => {
    expect(ruleBody(css("cards.css"), ".card")).toMatch(/overflow:\s*hidden/);
  });
});
