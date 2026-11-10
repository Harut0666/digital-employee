import { describe, expect, test } from "bun:test";
import { FOLLOW_THRESHOLD, isNearBottom } from "../src/view/scroll-follow";

describe("自动滚动", () => {
  const at = (scrollTop: number) => ({ scrollTop, scrollHeight: 1000, clientHeight: 400 });

  test("距底部 24px 以内算在底部，再远就算离开", () => {
    expect(FOLLOW_THRESHOLD).toBe(24);
    expect(isNearBottom(at(600))).toBe(true);
    expect(isNearBottom(at(576))).toBe(true);
    expect(isNearBottom(at(575))).toBe(false);
    expect(isNearBottom(at(0))).toBe(false);
  });

  test("内容比窗口短（没有滚动条）时一直算在底部", () => {
    expect(isNearBottom({ scrollTop: 0, scrollHeight: 300, clientHeight: 400 })).toBe(true);
  });
});
