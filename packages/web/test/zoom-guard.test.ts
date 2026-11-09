// 禁用页面缩放：桌面窗口（--window，系统 WebView2）和浏览器里都不能用 Ctrl+滚轮、Ctrl+加减号/0、触控板捏合缩放。
// 字号请用“设置 → 外观”。判断该不该拦的是纯函数；installZoomGuard 只负责挂监听。
import { describe, expect, test } from "bun:test";
import { installZoomGuard, shouldBlockKey, shouldBlockWheel } from "../src/view/zoom-guard";

const key = (k: string, patch: Partial<Parameters<typeof shouldBlockKey>[0]> = {}) => ({ key: k, code: "", ctrlKey: true, metaKey: false, altKey: false, ...patch });

describe("shouldBlockKey：缩放快捷键", () => {
  test("Ctrl 加 + = - _ 0 都是缩放", () => {
    for (const k of ["+", "=", "-", "_", "0"]) expect(shouldBlockKey(key(k))).toBe(true);
  });
  test("Ctrl+Shift+= 也是放大（key 是 +）", () => {
    expect(shouldBlockKey(key("+", { shiftKey: true }))).toBe(true);
  });
  test("小键盘的加、减、0（看 code，key 在不同布局下可能不同）", () => {
    expect(shouldBlockKey(key("Unidentified", { code: "NumpadAdd" }))).toBe(true);
    expect(shouldBlockKey(key("Unidentified", { code: "NumpadSubtract" }))).toBe(true);
    expect(shouldBlockKey(key("Unidentified", { code: "Numpad0" }))).toBe(true);
  });
  test("Mac 上是 Cmd", () => {
    expect(shouldBlockKey(key("=", { ctrlKey: false, metaKey: true }))).toBe(true);
  });
  test("不带 Ctrl / Cmd 的加减号、0 是正常输入，不拦", () => {
    for (const k of ["+", "=", "-", "0"]) expect(shouldBlockKey(key(k, { ctrlKey: false }))).toBe(false);
  });
  test("Ctrl+C / V / A / F / R / P / F12 等正常功能不拦", () => {
    for (const k of ["c", "v", "x", "a", "z", "f", "r", "p", "Enter", "F12", "F5"]) expect(shouldBlockKey(key(k))).toBe(false);
  });
  test("AltGr（Windows 上是 Ctrl+Alt）打出来的字符不拦", () => {
    expect(shouldBlockKey(key("0", { altKey: true }))).toBe(false);
  });
});

describe("shouldBlockWheel：Ctrl（触控板捏合也是 ctrlKey 的滚轮）", () => {
  test("Ctrl+滚轮、Cmd+滚轮拦；普通滚轮不拦", () => {
    expect(shouldBlockWheel({ ctrlKey: true, metaKey: false })).toBe(true);
    expect(shouldBlockWheel({ ctrlKey: false, metaKey: true })).toBe(true);
    expect(shouldBlockWheel({ ctrlKey: false, metaKey: false })).toBe(false);
  });
});

/** 记下监听的假 target，再手动派发事件 */
function fakeTarget() {
  const listeners: { type: string; fn: (e: Event) => void; options: unknown }[] = [];
  return {
    listeners,
    addEventListener: (type: string, fn: (e: Event) => void, options?: unknown) => void listeners.push({ type, fn, options }),
    removeEventListener: (type: string, fn: (e: Event) => void) => {
      const at = listeners.findIndex((l) => l.type === type && l.fn === fn);
      if (at >= 0) listeners.splice(at, 1);
    },
    fire(type: string, init: Record<string, unknown> = {}) {
      const event = Object.assign(new Event(type, { cancelable: true }), init);
      for (const l of listeners.filter((l) => l.type === type)) l.fn(event);
      return event;
    },
  };
}

describe("installZoomGuard", () => {
  test("wheel 监听必须 passive:false，否则 preventDefault 不生效", () => {
    const target = fakeTarget();
    installZoomGuard(target);
    const wheel = target.listeners.find((l) => l.type === "wheel");
    expect(wheel?.options).toMatchObject({ passive: false });
  });
  test("Ctrl+滚轮被拦，普通滚轮放行", () => {
    const target = fakeTarget();
    installZoomGuard(target);
    expect(target.fire("wheel", { ctrlKey: true }).defaultPrevented).toBe(true);
    expect(target.fire("wheel", { ctrlKey: false }).defaultPrevented).toBe(false);
  });
  test("缩放快捷键被拦，复制粘贴放行", () => {
    const target = fakeTarget();
    installZoomGuard(target);
    expect(target.fire("keydown", { key: "=", ctrlKey: true }).defaultPrevented).toBe(true);
    expect(target.fire("keydown", { key: "0", ctrlKey: true }).defaultPrevented).toBe(true);
    expect(target.fire("keydown", { key: "c", ctrlKey: true }).defaultPrevented).toBe(false);
    expect(target.fire("keydown", { key: "F12" }).defaultPrevented).toBe(false);
  });
  test("Safari / WebKit 的捏合手势事件一律拦", () => {
    const target = fakeTarget();
    installZoomGuard(target);
    for (const type of ["gesturestart", "gesturechange", "gestureend"]) expect(target.fire(type).defaultPrevented).toBe(true);
  });
  test("返回的函数能撤掉全部监听", () => {
    const target = fakeTarget();
    const off = installZoomGuard(target);
    expect(target.listeners.length).toBeGreaterThan(0);
    off();
    expect(target.listeners).toEqual([]);
  });
});
