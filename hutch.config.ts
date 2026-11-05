// Hutch 工程配置：包管理器、常用任务与 Electrobun 版本（必须钉死精确版本）。
export default {
  packageManager: "bun",
  electrobun: {
    version: "2.0.6",
  },
  scripts: {
    install: ["hutch", "install"],
    dev: ["hutch", "electrobun", "dev", "--env=stable", "--watch"],
    build: ["bun", "run", "build:desktop"],
  },
};
