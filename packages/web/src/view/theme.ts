// antd 的主题取值（ConfigProvider.theme.token / components）。纯函数。
//
// 色值与 styles/tokens.css 保持一致：antd 组件（按钮、输入框、弹层、表单）和自定义样式（侧栏、卡片）
// 看起来是同一套。算法（浅色 / 深色）仍由调用方选，这里只给 token。

import type { ThemeConfig } from "antd";

export type ThemeMode = "light" | "dark";

const FONT_FAMILY = '-apple-system, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif';

const LIGHT = {
  colorPrimary: "#1a7f67",
  colorInfo: "#2563d6",
  colorSuccess: "#157347",
  colorWarning: "#d4780f",
  colorError: "#c42b33",
  colorText: "#1a2230",
  colorTextSecondary: "#475161",
  colorTextTertiary: "#667180",
  colorTextQuaternary: "#8d97a5",
  colorBorder: "#d3d7de",
  colorBorderSecondary: "#e5e7eb",
  colorBgLayout: "#f3f4f6",
  colorBgContainer: "#ffffff",
  colorBgElevated: "#ffffff",
} as const;

const DARK = {
  colorPrimary: "#3fb596",
  colorInfo: "#5b93f5",
  colorSuccess: "#3cba7e",
  colorWarning: "#e89a3c",
  colorError: "#f0605f",
  colorText: "#e6ebf1",
  colorTextSecondary: "#b3bdc9",
  colorTextTertiary: "#8b98a7",
  colorTextQuaternary: "#677484",
  colorBorder: "#36404c",
  colorBorderSecondary: "#262e38",
  colorBgLayout: "#0e1216",
  colorBgContainer: "#161b22",
  colorBgElevated: "#1a2028",
} as const;

export function antdTheme(mode: ThemeMode, fontSize: number): Pick<ThemeConfig, "token" | "components"> {
  const palette = mode === "dark" ? DARK : LIGHT;
  return {
    token: {
      ...palette,
      fontFamily: FONT_FAMILY,
      fontSize,
      borderRadius: 8,
      borderRadiusLG: 12,
      borderRadiusSM: 6,
      controlHeight: 32,
    },
    components: {
      // 深色下主色是亮青绿，主按钮上用深色字才够对比度
      Button: { primaryColor: mode === "dark" ? "#06241d" : "#ffffff", fontWeight: 500 },
      Segmented: { trackBg: mode === "dark" ? "#1e252e" : "#eceef1" },
    },
  };
}
