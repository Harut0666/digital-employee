// 应用菜单：模板与动作分发都是纯函数，方便单测（不启动 Electrobun）。
// Windows 快捷键统一用 Control（Spec 0003）。
import type { ApplicationMenuItemConfig } from "electrobun/main";

export const MENU_ACTIONS = {
  openWorkspace: "open-workspace",
  reload: "reload",
  toggleDevTools: "toggle-devtools",
  about: "about",
  quit: "quit",
} as const;

export type MenuAction = (typeof MENU_ACTIONS)[keyof typeof MENU_ACTIONS];

/** 菜单动作要调用的宿主能力；由 main.ts 注入，测试里换成 spy。 */
export interface MenuActionHandlers {
  openWorkspace(): void;
  reload(): void;
  toggleDevTools(): void;
  showAbout(): void;
  quit(): void;
}

export interface MenuOptions {
  /** dev 构建（BuildConfig.getSync().isPackaged === false）才提供“开发者工具” */
  isDev: boolean;
}

/** 构建应用菜单：文件 / 视图 / 帮助 三个顶层菜单。 */
export function buildAppMenu(options: MenuOptions): ApplicationMenuItemConfig[] {
  const viewItems: ApplicationMenuItemConfig[] = [
    { type: "normal", label: "重新加载", action: MENU_ACTIONS.reload, accelerator: "Control+R" },
  ];
  if (options.isDev) {
    viewItems.push({
      type: "normal",
      label: "开发者工具",
      action: MENU_ACTIONS.toggleDevTools,
      accelerator: "Control+Shift+I",
    });
  }

  return [
    {
      type: "normal",
      label: "文件",
      submenu: [
        {
          type: "normal",
          label: "打开工作区文件夹",
          action: MENU_ACTIONS.openWorkspace,
          accelerator: "Control+O",
        },
        { type: "divider" },
        { type: "normal", label: "退出", action: MENU_ACTIONS.quit, accelerator: "Control+Q" },
      ],
    },
    { type: "normal", label: "视图", submenu: viewItems },
    {
      type: "normal",
      label: "帮助",
      submenu: [{ type: "normal", label: "关于", action: MENU_ACTIONS.about }],
    },
  ];
}

/** 菜单动作 → 宿主调用。返回是否被处理；非 dev 构建的开发者工具动作不执行。 */
export function dispatchMenuAction(
  action: string,
  handlers: MenuActionHandlers,
  options: MenuOptions,
): boolean {
  switch (action) {
    case MENU_ACTIONS.openWorkspace:
      handlers.openWorkspace();
      return true;
    case MENU_ACTIONS.reload:
      handlers.reload();
      return true;
    case MENU_ACTIONS.toggleDevTools:
      if (!options.isDev) return false;
      handlers.toggleDevTools();
      return true;
    case MENU_ACTIONS.about:
      handlers.showAbout();
      return true;
    case MENU_ACTIONS.quit:
      handlers.quit();
      return true;
    default:
      return false;
  }
}

/** 菜单里所有动作 id（测试与文档用）。 */
export function menuActionIds(menu: ApplicationMenuItemConfig[]): string[] {
  const ids: string[] = [];
  for (const item of menu) {
    if (item.action) ids.push(item.action);
    if (item.submenu) ids.push(...menuActionIds(item.submenu));
  }
  return ids;
}
