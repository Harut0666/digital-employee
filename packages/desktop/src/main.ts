// Electrobun 主进程入口（由 Hutch 打包；构建期工具，不进 dependencies）。
// 顺序：资源根 → 后端（工作区 + 服务端 + 工具客户端）→ 原生窗口加载实际地址。
// 窗口只加载 http://127.0.0.1:<port>，前端零改动；关窗时先关服务端与工具客户端再退出。
import { BrowserWindow, PATHS, Utils } from "electrobun/main";
import Electrobun from "electrobun/main";
import { startDesktopBackend } from "./backend";
import { resolveDesktopAssetsDir } from "./paths";

async function main(): Promise<void> {
  const assetsDir = resolveDesktopAssetsDir({ resourcesFolder: PATHS.RESOURCES_FOLDER, env: process.env });
  console.log(`[desktop] 资源目录：${assetsDir}`);
  const backend = await startDesktopBackend({ assetsDir });

  let closing = false;
  const shutdown = async (): Promise<void> => {
    if (closing) return;
    closing = true;
    try {
      await backend.close();
    } catch (error) {
      console.error("[desktop] 关闭服务失败：", error);
    } finally {
      Utils.quit();
    }
  };

  const window = new BrowserWindow({
    title: "迷你数字员工",
    url: backend.url,
    frame: { width: 1280, height: 860 },
    sandbox: true,
  });

  // 关窗即退出；退出前先释放端口与工具客户端，不留后台服务
  window.on("close", () => {
    void shutdown();
  });
  Electrobun.events.on("before-quit", () => {
    void shutdown();
  });

  console.log(`[desktop] 窗口已打开：${backend.url}（工作区 ${backend.workspaceDir}）`);
}

main().catch(async (error: unknown) => {
  const message = error instanceof Error ? error.message : String(error);
  console.error(`[desktop] 启动失败：${message}`);
  try {
    await Utils.showMessageBox({
      type: "error",
      title: "迷你数字员工",
      message: "应用启动失败",
      detail: message,
      buttons: ["退出"],
    });
  } finally {
    Utils.quit(1);
  }
});
