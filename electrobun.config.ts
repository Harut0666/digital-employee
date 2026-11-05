// Electrobun 打包配置（构建期由 Hutch 读取；Electrobun 不进 package.json 的 dependencies）。
// 主进程就是 Bun：服务端、任务引擎、工具服务器与窗口在同一个进程里。
// 资源映射与 scripts/build-desktop.ts 共用一份清单（scripts/desktop-resources.ts）。
import { desktopCopyMapping } from "./scripts/desktop-resources";

export default {
  app: {
    name: "Digital Employee",
    identifier: "dev.digital-employee.desktop",
    version: "0.1.0",
    description: "迷你数字员工桌面版",
  },
  build: {
    mainProcess: "bun",
    bun: {
      entrypoint: "packages/desktop/src/main.ts",
    },
    // 目标路径落在安装后的 Resources/app/ 下（spike 实测），运行时用 PATHS.RESOURCES_FOLDER + "app" 定位
    copy: desktopCopyMapping(),
    artifactFolder: "dist/desktop",
  },
  // 本地构建不生成增量补丁（Spec 0003：delta patch 不在范围内）
  release: {
    generatePatch: false,
  },
  runtime: {
    exitOnLastWindowClosed: true,
  },
};
