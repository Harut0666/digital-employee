// Hutch 工程配置：包管理器、常用任务与 Electrobun 版本（必须钉死精确版本）。
export default {
  packageManager: "bun",
  electrobun: {
    version: "2.0.6",
  },
  scripts: {
    install: ["hutch", "install"],
    // dev 与根 dev:desktop 同款：先准备前端产物与 resvg wasm，再起 Electrobun dev
    dev: ["bun", "scripts/dev-desktop.ts"],
    build: ["bun", "run", "build:desktop"],
  },
};
