// Electrobun 主进程入口（由 Hutch 打包；构建期工具，不进 dependencies）。
// 顺序：单实例锁 → 工作区/服务端/工具客户端 → 窗口（先隐藏）→ 菜单 → 内容就绪后显示。
// 窗口只加载 http://127.0.0.1:<port>，前端零改动；关窗/退出先停 HTTP 与工具客户端，再退出。
import { ApplicationMenu, BrowserWindow, BuildConfig, PATHS, Screen, Utils } from "electrobun/main";
import Electrobun from "electrobun/main";
import { resolveWorkspace } from "@de/core";
import { startDesktopBackend, type DesktopBackend } from "./backend";
import { resolveDesktopAssetsDir } from "./paths";
import { acquireInstanceLock, releaseInstanceLock, type DesktopLock } from "./lock";
import { APP_TITLE } from "./brand";
import { defaultResourcesDir, formatAbout, readAboutInfo } from "./about";
import { buildAppMenu, dispatchMenuAction, type MenuActionHandlers } from "./menu";
import {
  buildWindowOptions,
  clampWindowSize,
  desktopShellScript,
  externalHttpUrl,
  navigationBlocked,
  navigationRules,
  navigationUrlFromEvent,
} from "./window";

interface DesktopContext {
  backend: DesktopBackend;
  workspaceDir: string;
  lock: DesktopLock;
}

// 引擎的“模拟崩溃”用 process.exit(1) 退出；Electrobun 已把 process.exit 改成走 before-quit。
// 这里记下请求的退出码，等收尾完成再用它真正退出，保持与 exe 一致的退出语义。
let requestedExitCode = 0;
{
  const sdkExit = process.exit;
  process.exit = ((code?: number) => {
    requestedExitCode = code ?? 0;
    return sdkExit(code);
  }) as typeof process.exit;
}

async function main(): Promise<void> {
  const workspaceDir = resolveWorkspace();
  const lock = acquireInstanceLock(workspaceDir);

  // 单实例：已有窗口在运行 → 原生提示后退出，不起第二份服务
  if (!lock) {
    await Utils.showMessageBox({
      type: "warning",
      title: APP_TITLE,
      message: "已经有一个窗口在运行",
      detail: `工作区：${workspaceDir}\n为避免重复启动服务、重复执行任务，本次启动已退出。`,
      buttons: ["退出"],
    });
    Utils.quit(0);
    return;
  }

  let backend: DesktopBackend | undefined;
  try {
    const assetsDir = resolveDesktopAssetsDir({ resourcesFolder: PATHS.RESOURCES_FOLDER, env: process.env });
    console.log(`[desktop] 资源目录：${assetsDir}`);
    backend = await startDesktopBackend({ assetsDir, workspaceDir });
    runDesktop({ backend, workspaceDir, lock });
  } catch (error) {
    if (backend) await backend.close().catch(() => {});
    releaseInstanceLock(workspaceDir, lock);
    throw error;
  }
}

/** 装配窗口、菜单与生命周期；窗口创建失败由 main 的 catch 收尾。 */
function runDesktop(context: DesktopContext): void {
  const { backend, workspaceDir, lock } = context;
  const isDev = !BuildConfig.getSync().isPackaged;

  let allowQuit = false;
  let shuttingDown = false;

  // 关窗 / 退出：先关 HTTP（释放端口）与工具客户端，再删锁、退出。
  // Electrobun 的 before-quit 是同步的，先否决一次，异步收尾完成后再真正退出。
  const shutdown = async (code = 0): Promise<void> => {
    if (shuttingDown) return;
    shuttingDown = true;
    try {
      await backend.close();
    } catch (error) {
      console.error("[desktop] 关闭服务失败：", error);
    } finally {
      releaseInstanceLock(workspaceDir, lock);
      allowQuit = true;
      Utils.quit(code);
    }
  };

  Electrobun.events.on("before-quit", (event: unknown) => {
    if (allowQuit) return;
    (event as { response?: unknown }).response = { allow: false };
    void shutdown(requestedExitCode);
  });

  const display = Screen.getPrimaryDisplay();
  const workArea =
    display.workArea && display.workArea.width > 0 && display.workArea.height > 0
      ? display.workArea
      : display.bounds;
  const window = new BrowserWindow(buildWindowOptions({ url: backend.url, hidden: true, workArea }));

  // Electrobun 2.0.6 的 createWebview 会丢掉构造参数里的 navigationRules（Windows 实测），
  // 必须显式再设一次，外部链接才会在原生层被拦下。
  window.webview.setNavigationRules(navigationRules(new URL(backend.url).origin));

  const handlers: MenuActionHandlers = {
    openWorkspace: () => {
      Utils.openPath(workspaceDir);
    },
    reload: () => {
      window.webview.loadURL(backend.url);
    },
    toggleDevTools: () => {
      window.webview.openDevTools();
    },
    showAbout: () => {
      const info = readAboutInfo({ resourcesDir: defaultResourcesDir() });
      const body = formatAbout(info, { workspaceDir });
      void Utils.showMessageBox({
        type: "info",
        title: body.title,
        message: body.message,
        detail: body.detail,
        buttons: ["确定"],
      });
    },
    quit: () => {
      void shutdown();
    },
  };

  ApplicationMenu.on("application-menu-clicked", (event) => {
    const action = event?.data?.action;
    if (typeof action === "string") dispatchMenuAction(action, handlers, { isDev });
  });

  // 服务已就绪且页面内容加载完成后再显示窗口，避免白屏；
  // 菜单要挂在可见/激活的窗口上，所以在 show() 之后再设置一次。
  let visible = false;
  const showWindow = (): void => {
    if (visible) return;
    visible = true;
    window.show();
    ApplicationMenu.setApplicationMenu(buildAppMenu({ isDev }));
  };
  window.webview.on("dom-ready", () => {
    window.webview.executeJavascript(desktopShellScript());
    console.log("[desktop] 页面就绪，已注入桌面壳桥");
    showWindow();
  });
  setTimeout(showWindow, 5000);

  // 最小尺寸：Electrobun 2.0.6 没有原生 setMinimumSize，resize 后回正。
  // 显示前的初始化 resize（尺寸可能还是 0）不处理，避免把新窗口压到最小值。
  window.on("resize", (event: unknown) => {
    if (!visible) return;
    const data = (event as { data?: { width?: number; height?: number } }).data;
    if (typeof data?.width !== "number" || typeof data?.height !== "number") return;
    const size = clampWindowSize(data.width, data.height);
    if (size.changed) window.setSize(size.width, size.height);
  });

  // 外部链接交给系统浏览器，窗口不变成浏览器；本机地址之外的导航一律拦下
  window.webview.on("will-navigate", (event: unknown) => {
    const url = navigationUrlFromEvent(event);
    if (!url) return;
    const external = externalHttpUrl(url, backend.url);
    if (!external) return;
    // 导航已被原生规则拦下（allowed=false），这里只负责交给系统浏览器
    const opened = Utils.openExternal(external);
    console.log(`[desktop] 外部导航：${external}（导航已拦截=${navigationBlocked(event)}，系统浏览器=${opened}）`);
    if (!navigationBlocked(event)) window.webview.loadURL(backend.url);
  });
  window.webview.on("new-window-open", (event: unknown) => {
    const url = navigationUrlFromEvent(event);
    if (!url) return;
    const external = externalHttpUrl(url, backend.url);
    if (external) {
      const opened = Utils.openExternal(external);
      console.log(`[desktop] 新窗口请求：${external}（系统浏览器=${opened}）`);
    } else {
      window.webview.loadURL(url);
    }
  });

  window.on("close", () => {
    void shutdown();
  });

  console.log(
    `[desktop] 窗口已打开：${backend.url}（工作区 ${workspaceDir}${isDev ? "，开发构建" : ""}）`,
  );
}

main().catch(async (error: unknown) => {
  const message = error instanceof Error ? error.message : String(error);
  console.error(`[desktop] 启动失败：${message}`);
  try {
    await Utils.showMessageBox({
      type: "error",
      title: APP_TITLE,
      message: "应用启动失败",
      detail: message,
      buttons: ["退出"],
    });
  } finally {
    Utils.quit(1);
  }
});
