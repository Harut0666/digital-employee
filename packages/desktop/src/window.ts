// 窗口参数与窗口层纯函数（可单测；不依赖 Electrobun 运行时）：
// 尺寸/标题/居中、最小尺寸回正、外部导航判定、注入给前端的桌面壳桥。
import type { BrowserWindowOptions } from "electrobun/main";
import { APP_TITLE } from "./brand";

/** 默认窗口尺寸（Spec 0003）。 */
export const WINDOW_SIZE = { width: 1280, height: 860 } as const;

/** 最小窗口尺寸（Electrobun 2.0.6 没有 setMinimumSize，用 resize 回正实现）。 */
export const WINDOW_MIN_SIZE = { width: 1024, height: 700 } as const;

export interface ScreenRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface WindowOptionsInput {
  /** 窗口要加载的地址（服务端就绪后的实际地址） */
  url: string;
  /** 先隐藏，等内容 dom-ready 后再 show()，避免白屏 */
  hidden?: boolean;
  /** 主显示器工作区；传入时显式居中，避免原生居中在多显示器/DPI 下把窗口放偏 */
  workArea?: ScreenRect;
}

/** 在主显示器工作区里居中；尺寸大于工作区时贴左上，保证窗口可见。 */
export function centeredFrame(workArea: ScreenRect): { x: number; y: number } {
  const x = Math.round(workArea.x + Math.max(0, (workArea.width - WINDOW_SIZE.width) / 2));
  const y = Math.round(workArea.y + Math.max(0, (workArea.height - WINDOW_SIZE.height) / 2));
  return { x, y };
}

/** BrowserWindow 构造参数：不传 x/y 时交给原生居中，传了工作区就显式居中。 */
export function buildWindowOptions(input: WindowOptionsInput): BrowserWindowOptions {
  const frame: BrowserWindowOptions["frame"] = {
    width: WINDOW_SIZE.width,
    height: WINDOW_SIZE.height,
    ...(input.workArea ? centeredFrame(input.workArea) : {}),
  };
  return {
    title: APP_TITLE,
    url: input.url,
    frame,
    sandbox: true,
    hidden: input.hidden ?? true,
    navigationRules: navigationRules(new URL(input.url).origin),
  };
}

/**
 * 导航规则（Electrobun 语义：glob，`^` 开头是阻止规则，最后一条匹配的生效）。
 * 先拦掉所有 http/https，再放行本机服务地址：外部链接永远进不了这个窗口。
 */
export function navigationRules(origin: string): string[] {
  const base = origin.replace(/\/+$/, "");
  return ["^http://*", "^https://*", base, `${base}/*`];
}

/** 判断一个地址是否是本机服务之外的 http(s) 链接；是则返回它，否则 undefined。 */
export function externalHttpUrl(url: string, appOrigin: string): string | undefined {
  let parsed: URL;
  let app: URL;
  try {
    parsed = new URL(url);
    app = new URL(appOrigin);
  } catch {
    return undefined;
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return undefined;
  return parsed.origin === app.origin ? undefined : parsed.href;
}

/**
 * 窗口尺寸小于最小值时回正（Electrobun 2.0.6 没有原生 setMinimumSize）。
 * 返回要设置的新尺寸；不需要调整时 changed=false，避免 resize 循环。
 */
export function clampWindowSize(width: number, height: number): {
  width: number;
  height: number;
  changed: boolean;
} {
  const nextWidth = Math.max(Math.round(width), WINDOW_MIN_SIZE.width);
  const nextHeight = Math.max(Math.round(height), WINDOW_MIN_SIZE.height);
  return { width: nextWidth, height: nextHeight, changed: nextWidth !== width || nextHeight !== height };
}

/**
 * will-navigate 的 detail 在 Electrobun 2.0.6 里是 JSON 字符串（SDK 只为部分事件做了解析），
 * new-window-open 却是已解析对象；这里统一成对象再取 url/allowed。
 */
function parseDetail(detail: unknown): unknown {
  if (typeof detail !== "string") return detail;
  const value = detail.trim();
  if (!value.startsWith("{")) return detail;
  try {
    return JSON.parse(value);
  } catch {
    return detail;
  }
}

/** 从 webview 事件里取出被导航/被请求打开的地址（will-navigate 与 new-window-open 两种形状）。 */
export function navigationUrlFromEvent(event: unknown): string | undefined {
  const data = (event as { data?: { url?: unknown; detail?: unknown } } | undefined)?.data;
  if (!data) return undefined;
  if (typeof data.url === "string") return data.url;
  const detail = parseDetail(data.detail);
  if (typeof detail === "string") return detail;
  if (detail && typeof detail === "object" && typeof (detail as { url?: unknown }).url === "string") {
    return (detail as { url: string }).url;
  }
  return undefined;
}

/** will-navigate 是否已被原生导航规则拦下。 */
export function navigationBlocked(event: unknown): boolean {
  const detail = parseDetail((event as { data?: { detail?: unknown } } | undefined)?.data?.detail);
  return typeof detail === "object" && detail !== null && (detail as { allowed?: unknown }).allowed === false;
}

/**
 * 注入给前端的桌面壳桥：前端（Preferences.tsx）约定存在 window.desktopShell.openExternal
 * 时把「系统浏览器」偏好交给壳处理。这里用一次导航转交主进程：导航会被导航规则拦下，
 * 主进程在 will-navigate 里改用 Utils.openExternal 打开系统浏览器。
 * 同时把 target=_blank 的链接改成本窗口导航，避免 WebView2 新窗口请求被静默丢弃。
 */
export function desktopShellScript(): string {
  return [
    "(() => {",
    "  if (!window.desktopShell || !window.desktopShell.openExternal) {",
    "    window.desktopShell = {",
    "      openExternal(url) { window.location.href = String(url); },",
    "    };",
    "  }",
    "  if (window.__deDesktopLinks) return;",
    "  window.__deDesktopLinks = true;",
    "  window.addEventListener('click', (event) => {",
    "    const target = event.target;",
    "    const anchor = target && target.closest ? target.closest('a[target=\"_blank\"]') : null;",
    "    if (!anchor) return;",
    "    event.preventDefault();",
    "    window.location.href = anchor.href;",
    "  }, true);",
    "})();",
  ].join("\n");
}
