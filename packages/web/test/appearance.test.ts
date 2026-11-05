// 接缝 D：外观与交互偏好的纯函数（Spec 0004「关键约定」/ T21）。
// 只断言外部可观察的映射结果：主题解析、偏好合并、轨迹展示参数、回车动作、链接目标。
import { describe, expect, test } from "bun:test";
import { DEFAULT_UI_SETTINGS } from "@de/core/contract";
import {
  enterAction,
  linkTarget,
  mergeUiSettings,
  planLinkOpen,
  resolveTheme,
  shouldAutoExpand,
  shouldStayOpenAfterRun,
  trajectoryDisplay,
} from "../src/pages/settings/preferences";

describe("resolveTheme", () => {
  test("跟随系统：按系统偏好落到浅色/深色", () => {
    expect(resolveTheme("system", true)).toBe("dark");
    expect(resolveTheme("system", false)).toBe("light");
  });

  test("显式选择优先于系统偏好", () => {
    expect(resolveTheme("dark", false)).toBe("dark");
    expect(resolveTheme("light", true)).toBe("light");
  });
});

describe("mergeUiSettings", () => {
  test("没有任何改动时等于契约里的默认值", () => {
    expect(mergeUiSettings()).toEqual(DEFAULT_UI_SETTINGS);
    expect(mergeUiSettings(undefined)).toEqual(DEFAULT_UI_SETTINGS);
  });

  test("只覆盖传入的字段，其余保持默认", () => {
    expect(mergeUiSettings({ theme: "dark" })).toEqual({ ...DEFAULT_UI_SETTINGS, theme: "dark" });
    expect(mergeUiSettings({ enterBehavior: "newline" })).toEqual({ ...DEFAULT_UI_SETTINGS, enterBehavior: "newline" });
  });

  test("字号夹在 12–18 且取整", () => {
    expect(mergeUiSettings({ fontSize: 99 }).fontSize).toBe(18);
    expect(mergeUiSettings({ fontSize: 3 }).fontSize).toBe(12);
    expect(mergeUiSettings({ fontSize: 15.6 }).fontSize).toBe(16);
    expect(mergeUiSettings({ fontSize: Number.NaN }).fontSize).toBe(DEFAULT_UI_SETTINGS.fontSize);
  });

  test("非法枚举值回落到默认值（文件被手改坏也不崩）", () => {
    const broken = { theme: "neon", trajectoryDetail: "verbose", enterBehavior: "space" } as unknown as Parameters<typeof mergeUiSettings>[0];
    expect(mergeUiSettings(broken)).toEqual(DEFAULT_UI_SETTINGS);
  });
});

describe("trajectoryDisplay", () => {
  test("简洁：不自动展开、不显示耗时/token 与细节", () => {
    expect(trajectoryDisplay("compact")).toEqual({ autoExpandSteps: false, showMeta: false, showDetail: false });
  });

  test("标准：显示信息量但默认折叠", () => {
    expect(trajectoryDisplay("standard")).toEqual({ autoExpandSteps: false, showMeta: true, showDetail: true });
  });

  test("详细：默认展开每一步并显示全部信息", () => {
    expect(trajectoryDisplay("detailed")).toEqual({ autoExpandSteps: true, showMeta: true, showDetail: true });
  });

  test("运行中的步骤在任何详细度下都自动展开", () => {
    expect(shouldAutoExpand("compact", "running")).toBe(true);
    expect(shouldAutoExpand("compact", "done")).toBe(false);
    expect(shouldAutoExpand("detailed", "done")).toBe(true);
  });
});

describe("shouldStayOpenAfterRun（折叠时机）", () => {
  test("完成即折叠：运行结束就收起", () => {
    expect(shouldStayOpenAfterRun("completion", true, false)).toBe(false);
  });

  test("下次输入时折叠：刚跑完的一轮先留着，等用户下次输入", () => {
    expect(shouldStayOpenAfterRun("next-input", true, false)).toBe(true);
  });

  test("本来就没在运行，或仍在运行，都不触发收起判断", () => {
    expect(shouldStayOpenAfterRun("next-input", false, false)).toBe(false);
    expect(shouldStayOpenAfterRun("next-input", true, true)).toBe(false);
  });
});

describe("enterAction", () => {
  test("发送模式：Enter 发送、Shift+Enter 换行", () => {
    expect(enterAction("send", {})).toBe("send");
    expect(enterAction("send", { shift: true })).toBe("newline");
  });

  test("换行模式：Enter 换行", () => {
    expect(enterAction("newline", {})).toBe("newline");
    expect(enterAction("newline", { shift: true })).toBe("newline");
  });

  test("Ctrl+Enter 始终发送（两种模式都一样）", () => {
    expect(enterAction("send", { ctrl: true })).toBe("send");
    expect(enterAction("newline", { ctrl: true })).toBe("send");
    expect(enterAction("newline", { ctrl: true, shift: true })).toBe("send");
  });
});

describe("链接打开方式", () => {
  test("当前窗口用 _self，系统浏览器用 _blank", () => {
    expect(linkTarget("in-app")).toBe("_self");
    expect(linkTarget("system")).toBe("_blank");
    expect(linkTarget(undefined)).toBe("_self");
  });

  test("桌面壳存在时交给壳（当前未实现），否则走浏览器新窗口", () => {
    expect(planLinkOpen("system", false)).toEqual({ target: "_blank", viaShell: false });
    expect(planLinkOpen("system", true)).toEqual({ target: "_blank", viaShell: true });
    expect(planLinkOpen("in-app", true)).toEqual({ target: "_self", viaShell: false });
  });
});
