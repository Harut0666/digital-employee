// 轨迹状态机：对话顶部的“发起、准入、执行、质检、审核、交付”六段，加一行业务话。纯函数。
// 阶段由任务状态决定；暂停和终止停在进入暂停前的那一段（取最后一次状态变化的 from）。
// 业务话取自工作流配置里步骤的 activity，工具名等技术细节只在轨迹详情里出现。

import type { TaskEvent, TaskState } from "@de/core/contract";
import type { StepDef } from "./session-view";

export type StageKey = "start" | "admission" | "running" | "qc" | "review" | "delivered";
export type StageStatus = "done" | "current" | "paused" | "stopped" | "pending";

export interface StageView {
  key: StageKey;
  label: string;
  status: StageStatus;
}

export interface StageLine {
  stages: StageView[];
  /** 现在在做什么，给业务人员看的一句话 */
  activity: string;
}

const STAGES: { key: StageKey; label: string }[] = [
  { key: "start", label: "发起" },
  { key: "admission", label: "准入" },
  { key: "running", label: "执行" },
  { key: "qc", label: "质检" },
  { key: "review", label: "审核" },
  { key: "delivered", label: "交付" },
];

const STAGE_OF: Partial<Record<TaskState, StageKey>> = {
  draft: "start",
  admission: "admission",
  running: "running",
  qc: "qc",
  awaiting_review: "review",
  delivered: "delivered",
};

const FIXED_ACTIVITY: Partial<Record<TaskState, string>> = {
  draft: "草稿，等待提交",
  delivered: "已交付",
  terminated: "已终止",
};

/** 正在运行的步骤：最后一次开始、之后没有结束的那个 */
function runningStep(events: readonly TaskEvent[]): string | undefined {
  let running: string | undefined;
  for (const ev of events) {
    if (ev.type === "step_started") running = ev.stepId;
    else if ((ev.type === "step_completed" || ev.type === "step_failed") && ev.stepId === running) running = undefined;
    else if (ev.type === "state_changed") running = undefined;
  }
  return running;
}

export function deriveStages(
  events: readonly TaskEvent[],
  steps: readonly (StepDef & { activity?: string })[],
  state: TaskState | undefined,
  pauseTitle?: string,
): StageLine {
  const current = state ?? "draft";
  let from: TaskState | undefined;
  for (const ev of events) if (ev.type === "state_changed") from = ev.from;

  // 暂停和终止停在进入前的阶段；没有记录时按发起算
  const halted = !STAGE_OF[current];
  const at = halted ? (from && STAGE_OF[from]) ?? "start" : STAGE_OF[current]!;
  const atIndex = STAGES.findIndex((s) => s.key === at);
  const status = (index: number): StageStatus => {
    if (current === "delivered") return "done";
    if (index < atIndex) return "done";
    if (index > atIndex) return "pending";
    if (current === "terminated") return "stopped";
    return halted ? "paused" : "current";
  };

  return {
    stages: STAGES.map((stage, index) => ({ ...stage, status: status(index) })),
    activity: activityOf(),
  };

  function activityOf(): string {
    const fixed = FIXED_ACTIVITY[current];
    if (fixed) return fixed;
    if (pauseTitle && (halted || current === "awaiting_review")) return pauseTitle;
    const stepId = runningStep(events);
    const step = steps.find((s) => s.id === stepId);
    if (step) return step.activity || step.label;
    if (current === "admission") return "正在检查材料";
    if (current === "qc") return "正在质检";
    if (current === "awaiting_review") return "等待审核";
    return halted ? "已暂停" : "正在处理";
  }
}
