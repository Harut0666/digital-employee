// 外观与交互偏好的运行时：读取设置文档 v2 的 `ui`、即时应用、写回 PATCH /api/settings/app。
//
// 分工：映射规则在 pages/settings/preferences.ts（纯函数、有单测）；这里只做 DOM / React 接线。
// 约定：
// - 读取失败回落到默认值，不阻塞页面渲染；
// - 保存先本地生效再写盘；写失败（含 409 冲突）回读服务端值并提示，不假装成功；
// - 主题只写 `data-theme` 与 `--font-size`，组件颜色由 antd 算法 + styles.css 变量负责。
import { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import { DEFAULT_UI_SETTINGS, type UiSettings } from "@de/core/contract";
import type { SettingsApi } from "../api/settings";
import {
  mergeUiSettings,
  planLinkOpen,
  resolveTheme,
  SYSTEM_DARK_QUERY,
  type LinkTarget,
  type ResolvedTheme,
} from "../pages/settings/preferences";

export interface UiPreferencesValue {
  ui: UiSettings;
  /** 解析后的实际主题（`system` 跟随系统） */
  resolvedTheme: ResolvedTheme;
  /** 设置读取完成；读取失败也算完成（此时是默认值） */
  loaded: boolean;
  saving: boolean;
  /** 最近一次保存/读取失败的说明；成功保存后清空 */
  error: string | undefined;
  /** 立即生效并写回设置文件；失败时回滚为服务端值 */
  save: (patch: Partial<UiSettings>) => Promise<void>;
  reload: () => Promise<void>;
}

const UiPreferencesContext = createContext<UiPreferencesValue | null>(null);

export function useUiPreferences(): UiPreferencesValue {
  const value = useContext(UiPreferencesContext);
  if (!value) throw new Error("UiPreferencesContext 未提供（UiPreferencesProvider 未包裹）");
  return value;
}

const toMessage = (error: unknown): string => (error instanceof Error ? error.message : "设置保存失败，请稍后重试");

// 桌面壳（Spec 0003）暂缓；约定存在 window.desktopShell.openExternal 时交给它打开，
// 这样以后接壳时链接行为不用改组件。
interface DesktopShell {
  openExternal?: (url: string) => void;
}

function desktopShell(): DesktopShell | undefined {
  if (typeof window === "undefined") return undefined;
  return (window as unknown as { desktopShell?: DesktopShell }).desktopShell;
}

export interface LinkAnchorProps {
  target?: LinkTarget;
  rel?: string;
  onClick?: (event: React.MouseEvent<HTMLElement>) => void;
}

/**
 * 链接打开方式：当前窗口（默认）直接跟随 href；系统浏览器用新窗口，
 * 桌面壳存在时交给壳。返回的是「按 URL 生成 a 标签属性」的函数，便于在列表里复用。
 */
export function useLinkProps(): (url: string | undefined) => LinkAnchorProps {
  const { ui } = useUiPreferences();
  return useCallback(
    (url: string | undefined) => {
      if (!url) return {};
      const plan = planLinkOpen(ui.linkOpening, desktopShell()?.openExternal !== undefined);
      if (plan.viaShell) {
        return {
          target: "_blank" as const,
          rel: "noreferrer",
          onClick: (event: React.MouseEvent<HTMLElement>) => {
            event.preventDefault();
            desktopShell()?.openExternal?.(url);
          },
        };
      }
      // 系统浏览器：显式 window.open（桌面壳存在时改走壳的 openExternal）
      if (plan.target === "_blank") {
        return {
          target: "_blank" as const,
          rel: "noreferrer",
          onClick: (event: React.MouseEvent<HTMLElement>) => {
            event.preventDefault();
            window.open(url, "_blank", "noopener,noreferrer");
          },
        };
      }
      return {};
    },
    [ui.linkOpening],
  );
}

// ---- 用户输入信号：轨迹「下次输入时折叠」用它收起刚跑完的一轮
type UserInputListener = () => void;
const inputListeners = new Set<UserInputListener>();

/** 输入框提交时广播一次「用户开始新的输入」。 */
export function notifyUserInput(): void {
  for (const listener of inputListeners) listener();
}

/** 订阅用户输入信号，返回取消函数。 */
export function onUserInput(listener: UserInputListener): () => void {
  inputListeners.add(listener);
  return () => {
    inputListeners.delete(listener);
  };
}

export function UiPreferencesProvider({ api, children }: { api: SettingsApi; children: React.ReactNode }) {
  const [ui, setUi] = useState<UiSettings>(DEFAULT_UI_SETTINGS);
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>();
  const [systemDark, setSystemDark] = useState(() =>
    typeof window !== "undefined" && typeof window.matchMedia === "function" ? window.matchMedia(SYSTEM_DARK_QUERY).matches : false,
  );
  const revisionRef = useRef(0);
  const uiRef = useRef(ui);
  uiRef.current = ui;
  // 连续改动（滑块、快速切换）按顺序写盘，避免 revision 互相踩
  const queueRef = useRef<Promise<void>>(Promise.resolve());
  const pendingRef = useRef(0);

  // 跟随系统：系统偏好变化时立即重算实际主题
  useEffect(() => {
    if (typeof window === "undefined" || typeof window.matchMedia !== "function") return;
    const query = window.matchMedia(SYSTEM_DARK_QUERY);
    const onChange = (event: MediaQueryListEvent) => setSystemDark(event.matches);
    setSystemDark(query.matches);
    query.addEventListener("change", onChange);
    return () => query.removeEventListener("change", onChange);
  }, []);

  const reload = useCallback(async () => {
    try {
      const view = await api.getAppSettings();
      revisionRef.current = view.revision;
      const next = mergeUiSettings(view.ui);
      uiRef.current = next;
      setUi(next);
      setError(undefined);
    } catch {
      // 读取失败：保持默认值，外观问题不阻塞页面
    } finally {
      setLoaded(true);
    }
  }, [api]);

  useEffect(() => {
    void reload();
  }, [reload]);

  const resolvedTheme = resolveTheme(ui.theme, systemDark);

  // 主题与字号落到 :root，styles.css 用变量接管颜色与正文/轨迹字号
  useEffect(() => {
    const root = document.documentElement;
    root.dataset.theme = resolvedTheme;
    root.style.setProperty("--font-size", `${ui.fontSize}px`);
  }, [resolvedTheme, ui.fontSize]);

  const save = useCallback(
    (patch: Partial<UiSettings>) => {
      const optimistic = mergeUiSettings({ ...uiRef.current, ...patch }, uiRef.current);
      uiRef.current = optimistic;
      setUi(optimistic); // 立即生效
      pendingRef.current += 1;
      setSaving(true);

      const run = queueRef.current.then(async () => {
        try {
          const view = await api.patchAppSettings({ revision: revisionRef.current, ui: patch });
          revisionRef.current = view.revision;
          const next = mergeUiSettings(view.ui);
          uiRef.current = next;
          setUi(next);
          setError(undefined);
        } catch (cause) {
          setError(toMessage(cause));
          // 写失败（含 revision 冲突）：回读服务端值，别让界面假装已保存
          try {
            const view = await api.getAppSettings();
            revisionRef.current = view.revision;
            const next = mergeUiSettings(view.ui);
            uiRef.current = next;
            setUi(next);
          } catch {
            // 连读都失败就保留本地值，error 已提示
          }
          throw cause;
        } finally {
          pendingRef.current -= 1;
          if (pendingRef.current <= 0) setSaving(false);
        }
      });
      queueRef.current = run.catch(() => {});
      return run;
    },
    [api],
  );

  const value: UiPreferencesValue = { ui, resolvedTheme, loaded, saving, error, save, reload };
  return <UiPreferencesContext.Provider value={value}>{children}</UiPreferencesContext.Provider>;
}
