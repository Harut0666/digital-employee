// de replay：读取 <workspace>/tasks/<taskId>/events.jsonl，按 seq 顺序逐行输出。
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import type { TaskEvent } from "@de/core/contract";

export function eventsPath(workspace: string, taskId: string): string {
  return join(workspace, "tasks", taskId, "events.jsonl");
}

/** 读事件日志。崩溃时可能留下写了一半的最后一行，解析失败的行直接跳过。 */
export function readEvents(workspace: string, taskId: string): TaskEvent[] {
  const file = eventsPath(workspace, taskId);
  if (!existsSync(file)) throw new Error(`找不到任务 ${taskId} 的事件日志：${file}`);
  const events: TaskEvent[] = [];
  for (const line of readFileSync(file, "utf8").split(/\r?\n/)) {
    if (line.trim() === "") continue;
    try {
      events.push(JSON.parse(line) as TaskEvent);
    } catch {
      /* 半行，跳过 */
    }
  }
  return events.sort((a, b) => a.seq - b.seq);
}

export function summarize(e: TaskEvent): string {
  switch (e.type) {
    case "model_switched":
      return `模型切换：${e.fromModel ? `${e.fromModel.connection} / ${e.fromModel.model} → ` : ""}${e.model.connection} / ${e.model.model}`;
    case "task_created":
      return `员工 ${e.employee}，配置 ${e.configHash.slice(0, 8)}，附件 ${e.attachments.length} 个`;
    case "form_updated":
      return `表单更新（${e.actor}），附件 ${e.attachments.length} 个`;
    case "state_changed":
      return `${e.from} -> ${e.to}（${e.trigger}，${e.actor}${e.pause ? `，暂停原因 ${e.pause.code}` : ""}）`;
    case "transition_rejected":
      return `拒绝 ${e.trigger}：状态 ${e.from}，角色 ${e.actor}，原因 ${e.reason}`;
    case "step_started":
      return `${e.stepId}（第 ${e.attempt} 次尝试，重做轮次 ${e.reworkRound}）`;
    case "step_completed":
      return `${e.stepId} -> ${e.outputPath}`;
    case "step_failed":
      return `${e.stepId}：${e.error}`;
    case "tool_called":
      return `${e.stepId} 调用 ${e.tool}（第 ${e.attempt} 次，callId ${e.callId}）`;
    case "tool_result":
      return `callId ${e.callId} ${e.ok ? "成功" : `失败：${e.error ?? ""}`}`;
    case "tool_denied":
      return `${e.stepId} 调用 ${e.tool} 被拒绝：${e.reason}`;
    case "model_called":
      return `${e.stepId} 第 ${e.turn} 轮，用时 ${e.durationMs}ms${e.modelName ? `（${e.connection ?? ""} / ${e.modelName}）` : ""}`;
    case "qc_result":
      return `第 ${e.attempt} 次质检${e.passed ? "通过" : "未通过"}，共 ${e.checks.length} 项`;
    case "review_decision":
      return `${e.decision === "approve" ? "通过" : "退回"}${e.comment ? `：${e.comment}` : ""}`;
    case "deliverable_created":
      return `${e.kind} ${e.path}`;
    case "deliverable_filed":
      return e.filedPath ? `交付物已复制到 ${e.filedPath}` : `交付物没复制到工作区目录：${e.filedError ?? e.filedReason ?? ""}`;
    case "model_delta":
      // 增量不落盘，重放日志里不会出现；列在这里是为了让事件类型的 switch 保持穷尽
      return `${e.stepId} 第 ${e.turn} 轮增量：${e.text}`;
    case "fault_injected":
      return `${e.target} ${e.mode}`;
    default: {
      const { seq: _s, ts: _t, taskId: _i, type: _y, ...rest } = e as Record<string, unknown>;
      return JSON.stringify(rest);
    }
  }
}

export function formatEvent(e: TaskEvent): string {
  return `#${String(e.seq).padStart(3, "0")}  ${e.ts}  ${e.type.padEnd(20)}  ${summarize(e)}`;
}

export function replayLines(workspace: string, taskId: string): string[] {
  return readEvents(workspace, taskId).map(formatEvent);
}
