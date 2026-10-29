// 事件序列 → 会话视图状态。纯函数，不依赖 React 与网络。
// 前端对话流、右栏任务面板都由它的输出渲染；SSE 每来一条事件就重新推导一次。

import type { DeliverableRef, PauseReason, QcCheckResult, StepStatus, TaskEvent, TaskState } from "@de/core/contract";

/** 步骤的静态定义（标签、类型），事件里只有 stepId，需要由任务快照提供。 */
export interface StepDef {
  id: string;
  label: string;
  kind: StepStatus["kind"];
}

export interface StepView extends StepDef {
  status: StepStatus["status"];
  error?: string;
}

interface CardBase {
  /** 产生该卡片的事件序号，同时用作 React key */
  seq: number;
  ts: string;
}

export type ChatCard =
  | (CardBase & { kind: "user"; text: string })
  | (CardBase & { kind: "agent"; text: string })
  | (CardBase & { kind: "progress"; round: number; steps: StepView[] })
  | (CardBase & { kind: "pause"; state: TaskState; pause: PauseReason; active: boolean })
  | (CardBase & { kind: "qc"; attempt: number; passed: boolean; checks: QcCheckResult[] })
  | (CardBase & { kind: "review"; active: boolean; decision?: ReviewDecision })
  | (CardBase & { kind: "delivered"; deliverables: DeliverableRef[] })
  | (CardBase & { kind: "notice"; level: "info" | "warning"; text: string });

export interface ReviewDecision {
  decision: "approve" | "return";
  comment?: string;
  sections?: string[];
}

export interface QcView {
  attempt: number;
  passed: boolean;
  checks: QcCheckResult[];
}

export interface SessionView {
  state: TaskState | undefined;
  cards: ChatCard[];
  /** 当前（最新一轮）的步骤状态，右栏任务面板用 */
  steps: StepView[];
  /** 仍待处理的暂停原因 */
  pause: PauseReason | undefined;
  /** 最近一次质检结果 */
  qc: QcView | undefined;
  deliverables: DeliverableRef[];
  counts: { modelCalls: number; toolCalls: number; toolFailures: number };
  elapsedMs: number;
  lastSeq: number;
}

const USER_TRIGGER_TEXT: Partial<Record<string, string>> = {
  submit: "已提交表单，请开始处理",
  supplement: "已补充材料",
  continue_without_minutes: "无纪要继续",
  continue_with_partial: "按已有数据继续",
  retry: "重试",
};

export function deriveSessionView(events: readonly TaskEvent[], defs: readonly StepDef[] = []): SessionView {
  const cards: ChatCard[] = [];
  const counts = { modelCalls: 0, toolCalls: 0, toolFailures: 0 };
  const deliverables: DeliverableRef[] = [];
  let qc: QcView | undefined;
  let state: TaskState | undefined;
  let pause: PauseReason | undefined;

  // 每一轮（reworkRound）一张执行进度卡，卡内的步骤状态随事件更新。
  const rounds = new Map<number, { card: Extract<ChatCard, { kind: "progress" }>; byId: Map<string, StepView> }>();
  const defById = new Map(defs.map((d) => [d.id, d]));

  const roundOf = (round: number, ev: TaskEvent) => {
    let r = rounds.get(round);
    if (!r) {
      const steps: StepView[] = round === 0 ? defs.map((d) => ({ ...d, status: "pending" as const })) : [];
      r = { card: { kind: "progress", seq: ev.seq, ts: ev.ts, round, steps }, byId: new Map(steps.map((s) => [s.id, s])) };
      rounds.set(round, r);
      cards.push(r.card);
    }
    return r;
  };
  const stepIn = (round: number, stepId: string, ev: TaskEvent) => {
    const r = roundOf(round, ev);
    let s = r.byId.get(stepId);
    if (!s) {
      const def = defById.get(stepId) ?? { id: stepId, label: stepId, kind: "compute" as const };
      s = { ...def, status: "pending" };
      r.byId.set(stepId, s);
      r.card.steps.push(s);
    }
    return s;
  };
  let currentRound = 0;

  for (const ev of events) {
    switch (ev.type) {
      case "state_changed": {
        state = ev.to;
        const userText = USER_TRIGGER_TEXT[ev.trigger];
        if (userText) cards.push({ kind: "user", seq: ev.seq, ts: ev.ts, text: userText });
        if (ev.trigger === "admission_passed") {
          cards.push({ kind: "agent", seq: ev.seq, ts: ev.ts, text: "准入校验通过，开始执行" });
        }
        for (const c of cards) if (c.kind === "pause" || c.kind === "review") c.active = false;
        pause = ev.pause;
        if (ev.to === "awaiting_review") {
          cards.push({ kind: "review", seq: ev.seq, ts: ev.ts, active: true });
        } else if (ev.pause) {
          cards.push({ kind: "pause", seq: ev.seq, ts: ev.ts, state: ev.to, pause: ev.pause, active: true });
        }
        if (ev.to === "delivered") cards.push({ kind: "delivered", seq: ev.seq, ts: ev.ts, deliverables: [...deliverables] });
        if (ev.to === "terminated") cards.push({ kind: "notice", seq: ev.seq, ts: ev.ts, level: "info", text: "任务已终止" });
        break;
      }
      case "step_started": {
        currentRound = ev.reworkRound;
        stepIn(ev.reworkRound, ev.stepId, ev).status = "running";
        break;
      }
      case "step_completed": {
        const s = stepIn(ev.reworkRound, ev.stepId, ev);
        s.status = "done";
        delete s.error;
        break;
      }
      case "step_failed": {
        const s = stepIn(currentRound, ev.stepId, ev);
        s.status = "failed";
        s.error = ev.error;
        break;
      }
      case "qc_result":
        qc = { attempt: ev.attempt, passed: ev.passed, checks: ev.checks };
        cards.push({ kind: "qc", seq: ev.seq, ts: ev.ts, ...qc });
        break;
      case "deliverable_created":
        {
          const next = { kind: ev.kind, path: ev.path, sha256: ev.sha256 };
          const i = deliverables.findIndex((d) => d.kind === ev.kind);
          if (i >= 0) deliverables[i] = next;
          else deliverables.push(next);
        }
        break;
      case "review_decision": {
        for (let i = cards.length - 1; i >= 0; i--) {
          const c = cards[i];
          if (c?.kind === "review") {
            c.decision = { decision: ev.decision, comment: ev.comment, sections: ev.sections };
            break;
          }
        }
        break;
      }
      case "transition_rejected":
        cards.push({ kind: "notice", seq: ev.seq, ts: ev.ts, level: "warning", text: `操作被拒绝：${ev.reason}` });
        break;
      case "tool_called":
        counts.toolCalls += 1;
        break;
      case "tool_result":
        if (!ev.ok) counts.toolFailures += 1;
        break;
      case "model_called":
        counts.modelCalls += 1;
        break;
      default:
        break;
    }
  }

  const first = events[0];
  const last = events.at(-1);
  const latestRound = rounds.size === 0 ? undefined : rounds.get(Math.max(...rounds.keys()));
  return {
    state,
    cards,
    steps: latestRound?.card.steps ?? defs.map((d) => ({ ...d, status: "pending" as const })),
    pause,
    qc,
    deliverables,
    counts,
    elapsedMs: first && last ? Date.parse(last.ts) - Date.parse(first.ts) : 0,
    lastSeq: last?.seq ?? 0,
  };
}



