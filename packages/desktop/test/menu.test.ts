// 菜单模板与动作分发的纯函数测试：五项动作、dev 构建才出现“开发者工具”、快捷键用 Control。
import { describe, expect, test } from "bun:test";
import {
  MENU_ACTIONS,
  buildAppMenu,
  dispatchMenuAction,
  menuActionIds,
  type MenuActionHandlers,
} from "../src/menu";

function spyHandlers(): { handlers: MenuActionHandlers; calls: string[] } {
  const calls: string[] = [];
  return {
    calls,
    handlers: {
      openWorkspace: () => calls.push("openWorkspace"),
      reload: () => calls.push("reload"),
      toggleDevTools: () => calls.push("toggleDevTools"),
      showAbout: () => calls.push("showAbout"),
      quit: () => calls.push("quit"),
    },
  };
}

describe("应用菜单模板", () => {
  test("五项动作齐全（打开工作区 / 重新加载 / 开发者工具 / 关于 / 退出）", () => {
    expect(menuActionIds(buildAppMenu({ isDev: true })).sort()).toEqual(
      [
        MENU_ACTIONS.openWorkspace,
        MENU_ACTIONS.reload,
        MENU_ACTIONS.toggleDevTools,
        MENU_ACTIONS.about,
        MENU_ACTIONS.quit,
      ].sort(),
    );
  });

  test("非 dev 构建不出现“开发者工具”", () => {
    const ids = menuActionIds(buildAppMenu({ isDev: false }));
    expect(ids).not.toContain(MENU_ACTIONS.toggleDevTools);
    expect(ids).toHaveLength(4);
  });

  test("Windows 快捷键用 Control，标签固定", () => {
    const menu = buildAppMenu({ isDev: true });
    const items = menu.flatMap((item) => item.submenu ?? []);
    const byAction = new Map(items.filter((item) => item.action).map((item) => [item.action, item]));
    expect(byAction.get(MENU_ACTIONS.openWorkspace)?.label).toBe("打开工作区文件夹");
    expect(byAction.get(MENU_ACTIONS.openWorkspace)?.accelerator).toBe("Control+O");
    expect(byAction.get(MENU_ACTIONS.reload)?.accelerator).toBe("Control+R");
    expect(byAction.get(MENU_ACTIONS.toggleDevTools)?.accelerator).toBe("Control+Shift+I");
    expect(byAction.get(MENU_ACTIONS.quit)?.accelerator).toBe("Control+Q");
    expect(byAction.get(MENU_ACTIONS.about)?.label).toBe("关于");
    for (const item of items) {
      if (item.accelerator) expect(item.accelerator.startsWith("Control+")).toBe(true);
    }
  });

  test("顶层是文件 / 视图 / 帮助三个菜单，子菜单最多一层", () => {
    const menu = buildAppMenu({ isDev: true });
    expect(menu.map((item) => item.label)).toEqual(["文件", "视图", "帮助"]);
    for (const item of menu) {
      expect(Array.isArray(item.submenu)).toBe(true);
      for (const child of item.submenu ?? []) expect(child.submenu).toBeUndefined();
    }
  });
});

describe("菜单动作分发", () => {
  test("每个动作只调用对应处理器", () => {
    const cases: Array<[string, string]> = [
      [MENU_ACTIONS.openWorkspace, "openWorkspace"],
      [MENU_ACTIONS.reload, "reload"],
      [MENU_ACTIONS.toggleDevTools, "toggleDevTools"],
      [MENU_ACTIONS.about, "showAbout"],
      [MENU_ACTIONS.quit, "quit"],
    ];
    for (const [action, expected] of cases) {
      const { handlers, calls } = spyHandlers();
      expect(dispatchMenuAction(action, handlers, { isDev: true })).toBe(true);
      expect(calls).toEqual([expected]);
    }
  });

  test("非 dev 构建的“开发者工具”不执行", () => {
    const { handlers, calls } = spyHandlers();
    expect(dispatchMenuAction(MENU_ACTIONS.toggleDevTools, handlers, { isDev: false })).toBe(false);
    expect(calls).toEqual([]);
  });

  test("未知动作被忽略", () => {
    const { handlers, calls } = spyHandlers();
    expect(dispatchMenuAction("does-not-exist", handlers, { isDev: true })).toBe(false);
    expect(calls).toEqual([]);
  });
});
