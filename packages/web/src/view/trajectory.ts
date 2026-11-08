// 事件序列 → 轨迹视图。纯函数，不依赖 React 与网络。
// 轨迹是当前会话（任务）的执行过程记录：按轮次与步骤展示工具调用、模型轮次、质检、暂停与重做。
// 只读：不产生任何推进任务的按钮或动作；数据全部来自任务事件日志。

import type { TaskEvent, TaskState } from "@de/core/contract";
import type { StepDef } from "./session-view";

export type TraceItemKind = "step" | "tool" | "model" | "qc" | "state" | "notice";
export type TraceStatus = "pending" | "running" | "done" | "failed" | "skipped" | "info";

export interface TraceMeta {
  stepKind?: StepDef["kind"];
  /** 工具的第几次尝试 */
  attempt?: number;
  /** 模型是第几轮 */
  turn?: number;
  durationMs?: number;
  usage?: { input: number; output: number };
  passed?: boolean;
}

export interface TraceItem {
  id: string;
  kind: TraceItemKind;
  label: string;
  detail?: string;
  error?: string;
  status: TraceStatus;
  ts: string;
  meta?: TraceMeta;
  children?: TraceItem[];
}

export interface TraceRound {
  round: number;
  items: TraceItem[];
}

export interface TrajectorySummary {
  /** 工作流步骤总数 */
  steps: number;
  done: number;
  failed: number;
  toolCalls: number;
  toolFailures: number;
  modelCalls: number;
}

export interface TrajectoryView {
  state?: TaskState;
  /** 系统正在自动执行（准入 / 执行 / 质检） */
  active: boolean;
  currentRound: number;
  rounds: TraceRound[];
  summary: TrajectorySummary;
  /** 正在运行的步骤 */
  activeStepId?: string;
  lastSeq: number;
}

const ACTIVE_STATES: readonly TaskState[] = ["admission", "running", "qc"];

interface MutableRound extends TraceRound {
  byStep: Map<string, TraceItem>;
}

interface NoticeInput {
  kind: TraceItemKind;
  label: string;
  detail?: string;
  error?: string;
  status: TraceStatus;
  ts: string;
}

export function deriveTrajectory(events: readonly TaskEvent[], defs: readonly StepDef[] = []): TrajectoryView {
  const rounds = new Map<number, MutableRound>();
  const toolCalls = new Map<string, TraceItem>();
  const summary: TrajectorySummary = { steps: defs.length, done: 0, failed: 0, toolCalls: 0, toolFailures: 0, modelCalls: 0 };
  let state: TaskState | undefined;
  let currentRound = 0;
  let activeStepId: string | undefined;

  const ensureRound = (round: number): MutableRound => {
    let r = rounds.get(round);
    if (!r) {
      r = { round, items: [], byStep: new Map() };
      rounds.set(round, r);
      // 首轮先把工作流的全部步骤按定义顺序列出来（未开始的就是 pending）；重做轮只出现实际重跑的步骤
      if (round === 0) {
        for (const d of defs) {
          const item: TraceItem = {
            id: `r0:${d.id}`,
            kind: "step",
            label: d.label,
            status: "pending",
            ts: "",
            meta: { stepKind: d.kind },
            children: [],
          };
          r.items.push(item);
          r.byStep.set(d.id, item);
        }
      }
    }
    return r;
  };

  const stepItem = (round: number, stepId: string): TraceItem => {
    const r = ensureRound(round);
    let item = r.byStep.get(stepId);
    if (!item) {
      const def = defs.find((d) => d.id === stepId);
      item = { id: `r${round}:${stepId}`, kind: "step", label: def?.label ?? stepId, status: "pending", ts: "", meta: { stepKind: def?.kind }, children: [] };
      r.byStep.set(stepId, item);
      r.items.push(item);
    }
    return item;
  };

  const notice = (round: number, input: NoticeInput): void => {
    const r = ensureRound(round);
    r.items.push({ id: `r${round}:n${r.items.length}`, children: [], ...input });
  };

  for (const ev of events) {
    switch (ev.type) {
      case "state_changed": {
        state = ev.to;
        if (ev.trigger === "return") {
          currentRound += 1;
          ensureRound(currentRound);
          notice(currentRound, { kind: "state", label: "审核退回，开始重做", detail: ev.actor === "reviewer" ? "按退回意见重做，审核意见会作为判断步骤的修正上下文" : undefined, status: "info", ts: ev.ts });
        } else if (ev.trigger === "qc_autofix") {
          notice(currentRound, { kind: "state", label: "质检未通过，自动修正", detail: "重跑质检指出的步骤", status: "info", ts: ev.ts });
        } else if (ev.trigger === "retry") {
          notice(currentRound, { kind: "state", label: "重试，从断点继续", status: "info", ts: ev.ts });
        }
        if (ev.pause) notice(currentRound, { kind: "state", label: "任务暂停，等待处理", detail: ev.pause.message, status: "info", ts: ev.ts });
        if (ev.to === "delivered") notice(currentRound, { kind: "state", label: "已交付", status: "done", ts: ev.ts });
        if (ev.to === "terminated") notice(currentRound, { kind: "state", label: "任务已终止", status: "info", ts: ev.ts });
        break;
      }
      case "step_started": {
        currentRound = ev.reworkRound;
        const item = stepItem(ev.reworkRound, ev.stepId);
        item.status = "running";
        item.ts = ev.ts;
        item.error = undefined;
        activeStepId = ev.stepId;
        break;
      }
      case "step_completed": {
        const item = stepItem(ev.reworkRound, ev.stepId);
        item.status = "done";
        item.error = undefined;
        const dur = item.ts ? Date.parse(ev.ts) - Date.parse(item.ts) : 0;
        if (dur >= 0) item.meta = { ...item.meta, durationMs: dur };
        if (activeStepId === ev.stepId) activeStepId = undefined;
        break;
      }
      case "step_failed": {
        const item = stepItem(currentRound, ev.stepId);
        item.status = "failed";
        item.error = ev.error;
        if (activeStepId === ev.stepId) activeStepId = undefined;
        break;
      }
      case "tool_called": {
        const step = stepItem(currentRound, ev.stepId);
        const child: TraceItem = {
          id: `tool:${ev.callId}`,
          kind: "tool",
          label: ev.tool,
          status: "running",
          ts: ev.ts,
          meta: { attempt: ev.attempt },
          children: [],
        };
        step.children!.push(child);
        toolCalls.set(ev.callId, child);
        summary.toolCalls += 1;
        break;
      }
      case "tool_result": {
        const child = toolCalls.get(ev.callId);
        if (child) {
          child.status = ev.ok ? "done" : "failed";
          child.error = ev.ok ? undefined : ev.error;
          const dur = Date.parse(ev.ts) - Date.parse(child.ts);
          if (dur >= 0) child.meta = { ...child.meta, durationMs: dur };
        }
        if (!ev.ok) summary.toolFailures += 1;
        break;
      }
      case "tool_denied": {
        const step = stepItem(currentRound, ev.stepId);
        step.children!.push({ id: `denied:${ev.seq}`, kind: "tool", label: ev.tool, detail: "调用被拒绝", error: ev.reason, status: "failed", ts: ev.ts, children: [] });
        break;
      }
      case "model_called": {
        const step = stepItem(currentRound, ev.stepId);
        step.children!.push({
          id: `model:${ev.seq}`,
          kind: "model",
          label: `模型第 ${ev.turn} 轮`,
          // 这次调用用的连接和模型名（T39）；剧本模式没有
          detail: ev.connection && ev.modelName ? `${ev.connection} / ${ev.modelName}` : undefined,
          status: "done",
          ts: ev.ts,
          meta: { turn: ev.turn, durationMs: ev.durationMs, usage: ev.usage },
          children: [],
        });
        summary.modelCalls += 1;
        break;
      }
      case "model_switched":
        // 运行中换了模型：从下一个判断步骤开始生效（T39）
        notice(currentRound, {
          kind: "notice",
          label: `模型已切换：${ev.fromModel ? `${ev.fromModel.connection} / ${ev.fromModel.model} → ` : ""}${ev.model.connection} / ${ev.model.model}`,
          status: "info",
          ts: ev.ts,
        });
        break;
      case "qc_result": {
        const bad = ev.checks.filter((c) => !c.passed);
        ensureRound(currentRound).items.push({
          id: `qc:${ev.seq}`,
          kind: "qc",
          label: `质检第 ${ev.attempt} 次：${ev.passed ? "通过" : "未通过"}`,
          status: ev.passed ? "done" : "failed",
          ts: ev.ts,
          meta: { passed: ev.passed },
          children: bad.map((c) => ({ id: `qc:${ev.seq}:${c.id}`, kind: "qc" as const, label: c.label, detail: c.issues.join("；"), status: "failed" as const, ts: ev.ts })),
        });
        break;
      }
      case "fault_injected":
        notice(currentRound, { kind: "notice", label: `注入故障：${ev.target}（${ev.mode}）`, status: "info", ts: ev.ts });
        break;
      case "transition_rejected":
        notice(currentRound, { kind: "notice", label: "操作被拒绝", detail: ev.reason, status: "failed", ts: ev.ts });
        break;
      case "review_decision":
        notice(currentRound, {
          kind: "state",
          label: ev.decision === "approve" ? "审核通过" : "审核退回",
          detail: ev.comment,
          status: "info",
          ts: ev.ts,
        });
        break;
      default:
        break;
    }
  }

  const keys = [...rounds.keys()];
  const latest = keys.length > 0 ? rounds.get(Math.max(...keys)) : undefined;
  for (const item of latest?.items ?? []) {
    if (item.kind !== "step") continue;
    if (item.status === "done") summary.done += 1;
    else if (item.status === "failed") summary.failed += 1;
  }
  const last = events.at(-1);
  return {
    state,
    active: state ? ACTIVE_STATES.includes(state) : false,
    currentRound,
    rounds: [...rounds.values()].map((r) => ({ round: r.round, items: r.items })),
    summary,
    activeStepId,
    lastSeq: last?.seq ?? 0,
  };
}
