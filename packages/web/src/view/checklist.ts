// 开工清单的显示：对话顶部、状态机下方。开工前展开，开工后收成一行。
// 勾选全由后端判定（internal/session/checklist.go），这里只决定怎么显示；质检结果用同样的行样式。

import type { QcCheckResult } from "@de/core/contract";
import type { SessionChecklist } from "../api/types";

/** 一行的语气：已备齐/通过、还缺、未通过、已跳过 */
export type CheckTone = "ok" | "todo" | "bad" | "skip";

export interface CheckRow {
  key: string;
  label: string;
  tone: CheckTone;
  stateText: string;
  /** 补充说明（质检的问题） */
  detail?: string;
  /** 有值时这一行能点跳过，按钮上写这个 */
  skipLabel?: string;
}

export interface ChecklistBar {
  mode: "expanded" | "collapsed";
  headline: string;
  ready: boolean;
  rows: CheckRow[];
}

export function checklistBar(checklist: SessionChecklist, { started }: { started: boolean }): ChecklistBar {
  const total = checklist.items.length;
  const done = checklist.items.filter((i) => i.status === "done").length;
  const skipped = checklist.items.filter((i) => i.status === "skipped").length;
  let headline = `开工清单 ${done}/${total} 已备齐`;
  if (skipped > 0) headline += ` · 跳过 ${skipped} 项`;
  if (!started && checklist.missing.length > 0) headline += ` · 还缺：${checklist.missing.join("、")}`;

  const rows = checklist.items.map((item): CheckRow => {
    const row: CheckRow = { key: item.id, label: item.label, tone: "ok", stateText: "已备齐" };
    if (item.status === "skipped") return { ...row, tone: "skip", stateText: item.skipLabel ? `已跳过（${item.skipLabel}）` : "已跳过" };
    if (item.status === "missing") {
      const optional = !item.required;
      return {
        ...row,
        tone: "todo",
        stateText: optional ? "可跳过" : "还缺",
        skipLabel: optional && !started ? (item.skipLabel ?? "跳过") : undefined,
      };
    }
    return row;
  });
  return { mode: started ? "collapsed" : "expanded", headline, ready: checklist.ready, rows };
}

export function qcRows(checks: readonly QcCheckResult[]): CheckRow[] {
  return checks.map((c) => ({
    key: c.id,
    label: c.label,
    tone: c.passed ? "ok" : "bad",
    stateText: c.passed ? "通过" : "未通过",
    detail: c.passed || c.issues.length === 0 ? undefined : c.issues.join("；"),
  }));
}
