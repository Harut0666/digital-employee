// 禁用页面缩放。纯函数加一个挂监听的小函数，不依赖 React。
//
// 桌面窗口（--window）用系统 WebView2：MyGo 把 IsZoomControlEnabled 固定为开且不暴露设置，
// Ctrl+滚轮（触控板捏合在桌面浏览器里也是带 ctrlKey 的 wheel）和 Ctrl+加减号/0 都会缩放页面；
// 这是个桌面工作台，字号用“设置 → 外观”调，所以窗口和浏览器里都拦。

export interface KeyLike {
  key: string;
  code?: string;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey?: boolean;
  shiftKey?: boolean;
}

export interface WheelLike {
  ctrlKey: boolean;
  metaKey: boolean;
}

const ZOOM_KEYS = new Set(["+", "=", "-", "_", "0"]);
const ZOOM_CODES = new Set(["NumpadAdd", "NumpadSubtract", "Numpad0"]);

/** Ctrl（Mac 上 Cmd）加 + = - _ 0 或小键盘的加、减、0。AltGr 在 Windows 上是 Ctrl+Alt，它打出来的字符不拦。 */
export function shouldBlockKey(event: KeyLike): boolean {
  if (!(event.ctrlKey || event.metaKey) || event.altKey) return false;
  return ZOOM_KEYS.has(event.key) || ZOOM_CODES.has(event.code ?? "");
}

/** 带 Ctrl / Cmd 的滚轮（含触控板捏合）。 */
export function shouldBlockWheel(event: WheelLike): boolean {
  return event.ctrlKey || event.metaKey;
}

/** installZoomGuard 需要的最小接口（window 就满足；测试可以传假的） */
export interface ZoomGuardTarget {
  addEventListener(type: string, listener: (event: Event) => void, options?: AddEventListenerOptions | boolean): void;
  removeEventListener(type: string, listener: (event: Event) => void): void;
}

const GESTURES = ["gesturestart", "gesturechange", "gestureend"] as const;

/** 在 target（通常是 window）上拦掉所有缩放入口；返回撤销函数。 */
export function installZoomGuard(target: ZoomGuardTarget): () => void {
  const onWheel = (event: Event) => {
    if (shouldBlockWheel(event as unknown as WheelLike)) event.preventDefault();
  };
  const onKey = (event: Event) => {
    if (shouldBlockKey(event as unknown as KeyLike)) event.preventDefault();
  };
  const onGesture = (event: Event) => event.preventDefault();
  // wheel 默认是 passive 监听，preventDefault 不生效，必须显式 passive:false
  target.addEventListener("wheel", onWheel, { passive: false });
  target.addEventListener("keydown", onKey);
  for (const type of GESTURES) target.addEventListener(type, onGesture, { passive: false });
  return () => {
    target.removeEventListener("wheel", onWheel);
    target.removeEventListener("keydown", onKey);
    for (const type of GESTURES) target.removeEventListener(type, onGesture);
  };
}
