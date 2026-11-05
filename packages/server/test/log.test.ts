// 接缝 D（服务端纯函数）：日志级别过滤。
// 级别按 error < warn < info < debug 递增；配置的级别是“阈值”，低于等于它的消息才输出。
import { describe, expect, test } from "bun:test";
import { createLogger, shouldLog } from "../src/log";

describe("shouldLog", () => {
  test("info 阈值：error / warn / info 输出，debug 不输出", () => {
    expect(shouldLog("info", "error")).toBe(true);
    expect(shouldLog("info", "warn")).toBe(true);
    expect(shouldLog("info", "info")).toBe(true);
    expect(shouldLog("info", "debug")).toBe(false);
  });

  test("error 阈值只输出 error", () => {
    expect(shouldLog("error", "error")).toBe(true);
    expect(shouldLog("error", "warn")).toBe(false);
    expect(shouldLog("error", "info")).toBe(false);
    expect(shouldLog("error", "debug")).toBe(false);
  });

  test("debug 阈值输出全部级别", () => {
    for (const level of ["error", "warn", "info", "debug"] as const) {
      expect(shouldLog("debug", level)).toBe(true);
    }
  });
});

describe("createLogger", () => {
  function collector() {
    const lines: { level: string; line: string }[] = [];
    return { lines, sink: (level: string, line: string) => lines.push({ level, line }) };
  }

  test("默认 info：debug 被过滤掉，其它照常写 sink", () => {
    const { lines, sink } = collector();
    const logger = createLogger({ sink });
    logger.debug("细节");
    logger.info("启动");
    logger.warn("注意");
    logger.error("出错");
    expect(lines.map((l) => l.level)).toEqual(["info", "warn", "error"]);
    expect(lines[0]!.line).toBe("启动");
    // 非 info 的行带级别前缀，方便在控制台里区分
    expect(lines[1]!.line).toContain("[warn]");
    expect(lines[2]!.line).toContain("[error]");
  });

  test("debug 级别：debug 行也写出来，并带 [debug] 前缀", () => {
    const { lines, sink } = collector();
    const logger = createLogger({ level: "debug", sink });
    logger.debug("工作区就绪");
    expect(lines).toEqual([{ level: "debug", line: "[debug] 工作区就绪" }]);
  });

  test("setLevel 立即改变过滤（设置页改级别后不必重启服务端进程）", () => {
    const { lines, sink } = collector();
    const logger = createLogger({ level: "error", sink });
    logger.info("被过滤");
    expect(logger.level()).toBe("error");
    logger.setLevel("debug");
    logger.info("现在可见");
    logger.debug("细节也可见");
    expect(logger.level()).toBe("debug");
    expect(lines.map((l) => l.level)).toEqual(["info", "debug"]);
  });
});
