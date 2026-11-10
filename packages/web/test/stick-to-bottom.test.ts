import { describe, expect, test } from "bun:test";
import { BOTTOM_SLACK, nearBottom } from "../src/hooks/stick-to-bottom";

describe("nearBottom：对话区是否在底部", () => {
  test("正好在底部", () => {
    expect(nearBottom({ scrollHeight: 590, clientHeight: 371, scrollTop: 219 })).toBe(true);
  });

  test("差一点点（取整、滚动条）也算在底部", () => {
    expect(nearBottom({ scrollHeight: 590, clientHeight: 371, scrollTop: 219 - BOTTOM_SLACK })).toBe(true);
  });

  test("明显往上翻了就不算", () => {
    expect(nearBottom({ scrollHeight: 590, clientHeight: 371, scrollTop: 21 })).toBe(false);
  });

  test("内容不足一屏时总是在底部", () => {
    expect(nearBottom({ scrollHeight: 200, clientHeight: 371, scrollTop: 0 })).toBe(true);
  });
});
