// 对话里任务的步骤行（Spec 0006“员工步骤在对话中的形式”、T43）。
//
// 每个步骤一行：业务说法、状态、耗时、所用模型。数据来自任务事件，加上步骤定义（快照里带着工作流配置的
// 业务话 activity）。纯函数，不依赖 React 与网络；点开这一步时沿用现有的轨迹详情，这里不产生任何动作。

import type { TaskEvent } from "@de/core/contract";
import type { StepDef } from "./session-view";

export type StepRowStatus = "pending" | "running" | "done" | "failed" | "skipped";

export interface StepRow {
  id: string;
  /** 业务说法：工作流配置里的 activity，没有时退回步骤标题 */
  label: string;
  kind: StepDef["kind"];
  status: StepRowStatus;
  /** 耗时：已完成/失败的是实际耗时，运行中的是到 nowMs 为止 */
  durationMs?: number;
  /** 所用模型（“连接 / 模型”），取这一步最后一次模型调用；剧本模式用会话模型 */
  model?: string;
  error?: string;
}

/** 步骤定义（快照里的步骤带 activity 和引擎给的权威状态）。 */
export interface StepRowDef extends StepDef {
  activity?: string;
  /** 引擎给的权威状态：可选步骤失败后被跳过，只有快照里看得到 */
  status?: StepRowStatus;
}

export interface StepRowsOptions {
  /** 当前时间：用来算运行中步骤的已用时间；不给就不显示 */
  nowMs?: number;
  /** 模型调用事件里没有模型名时（剧本模式）退回的会话模型 */
  fallbackModel?: string;
}

interface MutableRow extends StepRow {
  /** 步骤开始的时间，用来算耗时 */
  startMs?: number;
  /** 引擎说这一步被跳过了（可选步骤失败）：后续事件不再把它改成失败或完成 */
  skippedByEngine?: boolean;
}

/** 事件序列 → 步骤行。 */
export function deriveStepRows(
  events: readonly TaskEvent[],
  defs: readonly StepRowDef[] = [],
  options: StepRowsOptions = {},
): StepRow[] {
  const rows = new Map<string, MutableRow>();
  const order: string[] = [];

  const add = (row: MutableRow) => {
    rows.set(row.id, row);
    order.push(row.id);
    return row;
  };
  const ensure = (stepId: string): MutableRow => {
    const existing = rows.get(stepId);
    if (existing) return existing;
    // 事件里出现了快照还没给的步骤（快照还没刷新）：先按 id 占一行
    return add({ id: stepId, label: stepId, kind: "compute", status: "pending" });
  };

  for (const def of defs) {
    add({
      id: def.id,
      label: def.activity?.trim() || def.label,
      kind: def.kind,
      status: def.status ?? "pending",
      skippedByEngine: def.status === "skipped",
    });
  }

  for (const ev of events) {
    switch (ev.type) {
      case "step_started": {
        const row = ensure(ev.stepId);
        if (!row.skippedByEngine) {
          row.status = "running";
          row.error = undefined;
        }
        row.startMs = Date.parse(ev.ts);
        delete row.durationMs;
        break;
      }
      case "step_completed": {
        const row = ensure(ev.stepId);
        if (!row.skippedByEngine) {
          row.status = "done";
          row.error = undefined;
          if (row.startMs !== undefined) {
            const dur = Date.parse(ev.ts) - row.startMs;
            if (dur >= 0) row.durationMs = dur;
          }
        }
        break;
      }
      case "step_failed": {
        const row = ensure(ev.stepId);
        if (!row.skippedByEngine) {
          row.status = "failed";
          row.error = ev.error;
          if (row.startMs !== undefined) {
            const dur = Date.parse(ev.ts) - row.startMs;
            if (dur >= 0) row.durationMs = dur;
          }
        }
        break;
      }
      case "model_called": {
        const row = ensure(ev.stepId);
        // 这次调用用的连接和模型名（T39）；剧本模式没有，退回会话模型
        const label = ev.modelName ? (ev.connection ? `${ev.connection} / ${ev.modelName}` : ev.modelName) : options.fallbackModel;
        if (label) row.model = label;
        break;
      }
      default:
        break;
    }
  }

  return order.map((id) => {
    const row = rows.get(id)!;
    const duration =
      row.status === "running" && row.startMs !== undefined && options.nowMs !== undefined
        ? Math.max(0, options.nowMs - row.startMs)
        : row.durationMs;
    const out: StepRow = {
      id: row.id,
      label: row.label,
      kind: row.kind,
      status: row.skippedByEngine ? "skipped" : row.status,
      model: row.model,
      error: row.skippedByEngine ? undefined : row.error,
    };
    if (duration !== undefined) out.durationMs = duration;
    return out;
  });
}

export interface StepProgress {
  total: number;
  /** 已完成（含跳过的可选步骤）的步数 */
  done: number;
  failed: number;
  running: number;
  /** 一句话：已完成 3/7 步（1 步失败） */
  label: string;
  tone: "running" | "error" | "done" | "idle";
  /** 步骤都走完了：默认收起这张列表 */
  finished: boolean;
}

/** 步骤行 → 折叠头上的进度概述。 */
export function stepProgress(rows: readonly StepRow[]): StepProgress {
  const total = rows.length;
  const done = rows.filter((r) => r.status === "done" || r.status === "skipped").length;
  const failed = rows.filter((r) => r.status === "failed").length;
  const running = rows.filter((r) => r.status === "running").length;
  const finished = total > 0 && done === total;
  const tone = failed > 0 ? "error" : running > 0 ? "running" : finished ? "done" : "idle";
  return { total, done, failed, running, finished, tone, label: `已完成 ${done}/${total} 步${failed > 0 ? `，${failed} 步失败` : ""}` };
}
