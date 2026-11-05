// 控制台日志的级别过滤（Spec 0004「诊断与关于」的日志级别）。
//
// 约定：`diagnostics.logLevel` 是阈值，按 error < warn < info < debug 递增；
// 阈值越高输出的行越多。启动时从 settings.json 读一次，设置页改级别后就地调整
// （不重启进程也生效；重启后同样按文件里的值生效）。
//
// 这里只做“包一层”：现有调用点保持原样，换成 logger.info / logger.warn 即可。
import type { LogLevel } from "@de/core/contract";

/** 每个级别的严重度：数字越小越严重。 */
const SEVERITY: Record<LogLevel, number> = { error: 0, warn: 1, info: 2, debug: 3 };

/** 配置阈值 `threshold` 下，`level` 的消息是否应该输出。 */
export function shouldLog(threshold: LogLevel, level: LogLevel): boolean {
  return SEVERITY[level] <= SEVERITY[threshold];
}

/** 一行日志的去处（测试注入假 sink，生产写 console）。 */
export type LogSink = (level: LogLevel, line: string) => void;

export interface Logger {
  level(): LogLevel;
  setLevel(level: LogLevel): void;
  error(message: string): void;
  warn(message: string): void;
  info(message: string): void;
  debug(message: string): void;
}

/** 默认 sink：error/warn 走对应的 console 方法，info/debug 走 console.log。 */
export const consoleSink: LogSink = (level, line) => {
  if (level === "error") console.error(line);
  else if (level === "warn") console.warn(line);
  else console.log(line);
};

export interface LoggerOptions {
  /** 阈值，缺省 info（与 DEFAULT_DIAGNOSTICS_SETTINGS 一致） */
  level?: LogLevel;
  sink?: LogSink;
}

export function createLogger(options: LoggerOptions = {}): Logger {
  let threshold: LogLevel = options.level ?? "info";
  const sink = options.sink ?? consoleSink;
  const emit = (level: LogLevel, message: string): void => {
    if (!shouldLog(threshold, level)) return;
    // info 行不带前缀，保持“服务已启动”这类既有输出干净；其余级别加前缀便于筛选
    sink(level, level === "info" ? message : `[${level}] ${message}`);
  };
  return {
    level: () => threshold,
    setLevel(level) {
      threshold = level;
    },
    error: (message) => emit("error", message),
    warn: (message) => emit("warn", message),
    info: (message) => emit("info", message),
    debug: (message) => emit("debug", message),
  };
}

/**
 * 进程内共享的控制台 logger：启动时按 settings.json 设级别，
 * 设置页保存 `diagnostics.logLevel` 后就地调整（路由与启动代码共用这一个）。
 */
export const consoleLogger = createLogger();

export function setConsoleLogLevel(level: LogLevel): void {
  consoleLogger.setLevel(level);
}
