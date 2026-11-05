// 窗口层纯函数：标题/尺寸/居中、最小尺寸回正、外部链接判定、导航事件解析与前端桥注入。
import { describe, expect, test } from "bun:test";
import { APP_TITLE } from "../src/brand";
import {
  WINDOW_MIN_SIZE,
  WINDOW_SIZE,
  buildWindowOptions,
  centeredFrame,
  clampWindowSize,
  desktopShellScript,
  externalHttpUrl,
  navigationBlocked,
  navigationRules,
  navigationUrlFromEvent,
} from "../src/window";

const ORIGIN = "http://127.0.0.1:8787";
const URL = `${ORIGIN}/`;

describe("窗口参数", () => {
  test("标题、1280×860、居中（不传 x/y）、sandbox、默认隐藏", () => {
    const options = buildWindowOptions({ url: URL });
    expect(options.title).toBe(APP_TITLE);
    expect(options.url).toBe(URL);
    expect(options.frame).toEqual({ width: 1280, height: 860 });
    expect(options.frame?.x).toBeUndefined();
    expect(options.frame?.y).toBeUndefined();
    expect(options.sandbox).toBe(true);
    expect(options.hidden).toBe(true);
    expect(WINDOW_SIZE).toEqual({ width: 1280, height: 860 });
    expect(WINDOW_MIN_SIZE).toEqual({ width: 1024, height: 700 });
  });

  test("导航规则先拦 http/https，再放行本机地址（最后匹配的生效）", () => {
    const rules = navigationRules(ORIGIN);
    expect(rules).toEqual(["^http://*", "^https://*", ORIGIN, `${ORIGIN}/*`]);
    expect(buildWindowOptions({ url: URL }).navigationRules).toEqual(rules);
    // 末尾带斜杠的 origin 也能归一
    expect(navigationRules(`${ORIGIN}/`)).toEqual(rules);
  });

  test("传入主显示器工作区时显式居中（多显示器/DPI 下不依赖原生居中）", () => {
    expect(centeredFrame({ x: 0, y: 0, width: 1920, height: 1040 })).toEqual({ x: 320, y: 90 });
    expect(buildWindowOptions({ url: URL, workArea: { x: 0, y: 0, width: 1920, height: 1040 } }).frame).toEqual({
      x: 320,
      y: 90,
      width: 1280,
      height: 860,
    });
  });

  test("工作区比窗口小时贴左上，保证可见", () => {
    expect(centeredFrame({ x: 100, y: 50, width: 1200, height: 800 })).toEqual({ x: 100, y: 50 });
  });
});

describe("最小尺寸回正", () => {
  test("小于最小值时抬到 1024×700", () => {
    expect(clampWindowSize(800, 500)).toEqual({ width: 1024, height: 700, changed: true });
    expect(clampWindowSize(1024, 600)).toEqual({ width: 1024, height: 700, changed: true });
  });

  test("不小于最小值时原样返回，避免 resize 循环", () => {
    expect(clampWindowSize(1280, 860)).toEqual({ width: 1280, height: 860, changed: false });
  });

  test("取整后再比较", () => {
    expect(clampWindowSize(1024.4, 700.2)).toEqual({ width: 1024, height: 700, changed: true });
  });
});

describe("外部链接判定", () => {
  test("本机同源不算外部", () => {
    expect(externalHttpUrl(`${ORIGIN}/tasks/1`, ORIGIN)).toBeUndefined();
  });

  test("其他端口、其他主机的 http(s) 算外部", () => {
    expect(externalHttpUrl("http://127.0.0.1:9999/x", ORIGIN)).toBe("http://127.0.0.1:9999/x");
    expect(externalHttpUrl("https://example.com/a?b=1", ORIGIN)).toBe("https://example.com/a?b=1");
  });

  test("非 http(s) 与非法地址不处理", () => {
    expect(externalHttpUrl("mailto:a@b.c", ORIGIN)).toBeUndefined();
    expect(externalHttpUrl("not a url", ORIGIN)).toBeUndefined();
  });
});

describe("导航事件解析", () => {
  test("will-navigate 的 detail 是对象", () => {
    expect(
      navigationUrlFromEvent({ name: "will-navigate", data: { detail: { url: "https://a.com", allowed: false } } }),
    ).toBe("https://a.com");
    expect(
      navigationBlocked({ name: "will-navigate", data: { detail: { url: "https://a.com", allowed: false } } }),
    ).toBe(true);
  });

  test("will-navigate 的 detail 是 JSON 字符串（Electrobun 2.0.6 实测形状）", () => {
    const detail = JSON.stringify({ url: "https://example.com/?from=plain", allowed: false });
    expect(navigationUrlFromEvent({ name: "will-navigate", data: { detail } })).toBe(
      "https://example.com/?from=plain",
    );
    expect(navigationBlocked({ name: "will-navigate", data: { detail } })).toBe(true);
    const allowed = JSON.stringify({ url: "http://127.0.0.1:8787/", allowed: true });
    expect(navigationBlocked({ name: "will-navigate", data: { detail: allowed } })).toBe(false);
  });

  test("new-window-open 的 detail 可能是字符串", () => {
    expect(navigationUrlFromEvent({ name: "new-window-open", data: { detail: "https://b.com" } })).toBe("https://b.com");
  });

  test("缺字段 / 坏 JSON 返回 undefined", () => {
    expect(navigationUrlFromEvent({})).toBeUndefined();
    expect(navigationUrlFromEvent({ data: { detail: {} } })).toBeUndefined();
    expect(navigationUrlFromEvent({ data: { detail: "{bad" } })).toBe("{bad");
    expect(navigationBlocked({})).toBe(false);
    expect(navigationBlocked({ data: { detail: "{bad" } })).toBe(false);
  });
});

describe("前端桌面壳桥", () => {
  test("注入 window.desktopShell.openExternal，且可重复执行", () => {
    const script = desktopShellScript();
    expect(script).toContain("window.desktopShell");
    expect(script).toContain("openExternal");
    expect(script).toContain("window.location.href");
  });

  test("把 target=_blank 链接改成本窗口导航，避免 WebView2 新窗口请求被丢弃", () => {
    const script = desktopShellScript();
    expect(script).toContain("a[target=");
    expect(script).toContain("_blank");
    expect(script).toContain("preventDefault");
  });
});
