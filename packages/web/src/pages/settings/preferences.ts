// 外观与交互偏好的纯函数（Spec 0004「关键约定」/ T21 接缝 D）。
//
// 设置页、App 主题接线、轨迹与输入框都从这里取映射，组件只负责把结果画出来；
// 这样主题解析、偏好合并、详细度映射这些容易出错的规则可以脱离 React 单测。
import {
  COLLAPSE_TIMINGS,
  DEFAULT_UI_SETTINGS,
  ENTER_BEHAVIORS,
  FONT_SIZE_MAX,
  FONT_SIZE_MIN,
  LINK_OPENINGS,
  THEMES,
  TRAJECTORY_DETAILS,
  type CollapseTiming,
  type EnterBehavior,
  type LinkOpening,
  type Theme,
  type TrajectoryDetail,
  type UiSettings,
} from "@de/core/contract";

export type ResolvedTheme = "light" | "dark";

/** 跟随系统时监听这条媒体查询（ticket T21「主题接线」）。 */
export const SYSTEM_DARK_QUERY = "(prefers-color-scheme: dark)";

/** 把「跟随系统」解析成实际主题；显式选择优先于系统偏好。 */
export function resolveTheme(theme: Theme | undefined, systemPrefersDark: boolean): ResolvedTheme {
  if (theme === "dark") return "dark";
  if (theme === "light") return "light";
  return systemPrefersDark ? "dark" : "light";
}

function oneOf<T extends string>(candidates: readonly T[], value: unknown, fallback: T): T {
  return typeof value === "string" && (candidates as readonly string[]).includes(value) ? (value as T) : fallback;
}

function clampFontSize(value: unknown, fallback: number): number {
  if (typeof value !== "number" || !Number.isFinite(value)) return fallback;
  return Math.min(FONT_SIZE_MAX, Math.max(FONT_SIZE_MIN, Math.round(value)));
}

/**
 * 把设置文件里的 `ui`（可能缺字段、可能被手改坏）合并到默认值上。
 * 读取失败或字段非法时回落默认值，不抛错——偏好问题不能阻塞页面渲染。
 */
export function mergeUiSettings(
  patch?: Partial<UiSettings> | null,
  base: UiSettings = DEFAULT_UI_SETTINGS,
): UiSettings {
  const source = patch ?? {};
  return {
    theme: oneOf<Theme>(THEMES, source.theme, base.theme),
    fontSize: clampFontSize(source.fontSize, base.fontSize),
    trajectoryDetail: oneOf<TrajectoryDetail>(TRAJECTORY_DETAILS, source.trajectoryDetail, base.trajectoryDetail),
    collapseTiming: oneOf<CollapseTiming>(COLLAPSE_TIMINGS, source.collapseTiming, base.collapseTiming),
    enterBehavior: oneOf<EnterBehavior>(ENTER_BEHAVIORS, source.enterBehavior, base.enterBehavior),
    linkOpening: oneOf<LinkOpening>(LINK_OPENINGS, source.linkOpening, base.linkOpening),
  };
}

/** 轨迹详细度 → 展示参数。 */
export interface TrajectoryDisplay {
  /** 非运行中的步骤是否默认展开子项（运行中始终展开） */
  autoExpandSteps: boolean;
  /** 是否显示耗时 / token / 第几次尝试等元信息 */
  showMeta: boolean;
  /** 是否显示每一步的细节摘要（错误始终显示） */
  showDetail: boolean;
}

const DISPLAY: Record<TrajectoryDetail, TrajectoryDisplay> = {
  compact: { autoExpandSteps: false, showMeta: false, showDetail: false },
  standard: { autoExpandSteps: false, showMeta: true, showDetail: true },
  detailed: { autoExpandSteps: true, showMeta: true, showDetail: true },
};

export function trajectoryDisplay(detail: TrajectoryDetail | undefined): TrajectoryDisplay {
  return DISPLAY[detail && detail in DISPLAY ? detail : DEFAULT_UI_SETTINGS.trajectoryDetail];
}

/** 某一步骤默认是否展开：运行中的步骤在任何详细度下都展开，跟随最新事件。 */
export function shouldAutoExpand(detail: TrajectoryDetail | undefined, status: string): boolean {
  return status === "running" || trajectoryDisplay(detail).autoExpandSteps;
}

/**
 * 一轮执行刚结束时是否保留轨迹展开。
 * `completion` 立即收起；`next-input` 留到下一次输入（由输入框发信号收起）。
 */
export function shouldStayOpenAfterRun(timing: CollapseTiming, wasActive: boolean, active: boolean): boolean {
  return timing === "next-input" && wasActive && !active;
}

export type EnterAction = "send" | "newline";

/** 回车动作：Ctrl/Cmd+Enter 始终发送，其余按「发送 / 换行」偏好决定。 */
export function enterAction(behavior: EnterBehavior, mods: { ctrl?: boolean; shift?: boolean } = {}): EnterAction {
  if (mods.ctrl) return "send";
  if (behavior === "newline") return "newline";
  return mods.shift ? "newline" : "send";
}

export type LinkTarget = "_self" | "_blank";

/** 「当前窗口」用 _self；「系统浏览器」用新窗口（桌面壳用 openExternal 交给系统浏览器）。 */
export function linkTarget(opening: LinkOpening | undefined): LinkTarget {
  return opening === "system" ? "_blank" : "_self";
}

export interface LinkOpenPlan {
  target: LinkTarget;
  /** true 表示应由桌面壳的 openExternal 处理（Spec 0003 暂缓，当前恒为 false） */
  viaShell: boolean;
}

export function planLinkOpen(opening: LinkOpening | undefined, hasDesktopShell: boolean): LinkOpenPlan {
  const target = linkTarget(opening);
  return { target, viaShell: target === "_blank" && hasDesktopShell };
}
