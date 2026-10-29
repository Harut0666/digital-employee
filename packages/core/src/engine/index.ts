// T1 实现：任务引擎（状态机、事件日志、编排器、断点恢复）。
import type { EngineDeps, JudgmentRunner, QcEngine, TaskEngine } from "../contract";

/** 测试可注入的内部实现；生产环境不传，使用 ../agent 与 ../qc 的默认实现。 */
export interface EngineInternals {
  judgment?: JudgmentRunner;
  qc?: QcEngine;
}

export function createTaskEngine(_deps: EngineDeps, _internals?: EngineInternals): TaskEngine {
  throw new Error("not implemented (T1)");
}
