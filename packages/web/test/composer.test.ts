import { describe, expect, test } from "bun:test";
import { composerView, enterHint, STARTERS } from "../src/view/composer";

describe("输入区", () => {
  test("普通会话：可以输入，占位语说明能做什么", () => {
    const view = composerView({ readOnly: false, streaming: false, hasTask: false, canMention: false });
    expect(view.disabled).toBe(false);
    expect(view.notice).toBeUndefined();
    expect(view.placeholder).toBe("说说要做什么，或问问员工…");
  });

  test("已经有任务：问问这个任务", () => {
    expect(composerView({ readOnly: false, streaming: false, hasTask: true, canMention: false }).placeholder).toBe("问问这个任务…");
  });

  test("目录里有文件可选：提示 @", () => {
    expect(composerView({ readOnly: false, streaming: false, hasTask: false, canMention: true }).placeholder).toBe("说说要做什么，或问问员工…（输入 @ 选目录里的文件）");
  });

  test("正在说话：接着说会排进这一轮", () => {
    expect(composerView({ readOnly: false, streaming: true, hasTask: true, canMention: true }).placeholder).toBe("接着说…（会在下一步之前送达）");
  });

  test("只读会话：禁用，并给出明确的只读提示和出路", () => {
    const view = composerView({ readOnly: true, streaming: false, hasTask: true, canMention: false });
    expect(view.disabled).toBe(true);
    expect(view.placeholder).toBe("只读记录，不能发消息");
    expect(view.notice).toBe("这是旧任务的只读记录：可以查看和归档，不能发消息、换模型。要继续工作请开新对话。");
  });

  test("只读会话即使还在“说话”也不显示停止", () => {
    expect(composerView({ readOnly: true, streaming: true, hasTask: true, canMention: true }).showStop).toBe(false);
    expect(composerView({ readOnly: false, streaming: true, hasTask: true, canMention: true }).showStop).toBe(true);
  });
});

describe("新对话页的示例起点", () => {
  test("有出周报、载入公开样例、问能做什么三个起点", () => {
    expect(STARTERS.map((s) => s.key)).toEqual(["weekly", "sample", "ask"]);
  });

  test("“帮我出第 44 周的周报”是填进输入框而不是直接发送；“载入公开样例”走样例流程", () => {
    const weekly = STARTERS.find((s) => s.key === "weekly");
    expect(weekly).toMatchObject({ action: "fill", text: "帮我出第 44 周的周报" });
    expect(STARTERS.find((s) => s.key === "sample")).toMatchObject({ action: "sample" });
  });
});

describe("快捷键提示", () => {
  test("按回车行为偏好说明", () => {
    expect(enterHint("send")).toBe("Enter 发送 · Shift+Enter 换行");
    expect(enterHint("newline")).toBe("Ctrl+Enter 发送 · Enter 换行");
  });
});