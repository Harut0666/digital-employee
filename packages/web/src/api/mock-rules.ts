// 模拟数据模式里的“后端规则”：可用操作、等待角色、暂停说明。
// 它只替模拟适配器扮演后端（对应 Go 的 internal/engine/actions.go 和 pause_text.go），视图层不许引用。

import { PAUSED_STATES, STATE_LABELS, type TaskState } from "@de/core/contract";
import type { CommandType, PauseView, TaskAction, UiRole } from "./types";

const COMMAND_LABELS: Record<CommandType, string> = {
  submit: "提交",
  supplement: "补充材料",
  continue_without_minutes: "无纪要继续",
  continue_with_partial: "按已有数据继续",
  retry: "重试",
  terminate: "终止",
  approve: "通过",
  return: "退回",
};

const action = (command: CommandType, style: TaskAction["style"], ...roles: UiRole[]): TaskAction => ({
  command,
  label: COMMAND_LABELS[command],
  roles,
  style,
});

export function actionsOf(state: TaskState, code: PauseView["code"] | undefined): TaskAction[] {
  switch (state) {
    case "draft":
      return [action("submit", "primary", "initiator"), { ...action("terminate", "danger", "initiator"), label: "放弃草稿" }];
    case "awaiting_initiator": {
      const continues: TaskAction[] = [];
      if (code === "minutes_missing" || code === "minutes_week_mismatch") continues.push(action("continue_without_minutes", "default", "initiator"));
      if (code === "period_incomplete") continues.push(action("continue_with_partial", "default", "initiator"));
      return [action("supplement", "primary", "initiator"), ...continues, action("terminate", "danger", "initiator")];
    }
    case "awaiting_operator": {
      const initiatorRetries = code === "tool_failed" || code === "model_unavailable";
      return [action("retry", "primary", ...(initiatorRetries ? (["initiator", "operator"] as const) : (["operator"] as const))), action("terminate", "danger", "operator")];
    }
    case "awaiting_review":
      return [action("approve", "primary", "reviewer"), action("return", "default", "reviewer"), action("terminate", "danger", "reviewer")];
    default:
      return [];
  }
}

export function waitingForOf(state: TaskState, actions: readonly TaskAction[]): UiRole[] {
  if (state !== "draft" && !PAUSED_STATES.includes(state)) return [];
  return (["initiator", "reviewer", "operator"] as const).filter((role) => actions.some((a) => a.roles.includes(role)));
}

const PAUSE_TEXT: Partial<Record<PauseView["code"], [title: string, resolution: string]>> = {
  missing_sales: ["缺少销售明细", "请上传销售明细后继续。"],
  missing_dimensions: ["缺少维表", "请上传完整的销售明细文件后继续。"],
  minutes_missing: ["缺少本周纪要", "可以补充纪要，或选择无纪要继续。"],
  tool_failed: ["工具调用失败", "可以重试；问题解决不了时由操作员终止任务。"],
  model_unavailable: ["模型不可用", "请到设置里检查模型 Key 后重试；问题解决不了时由操作员终止任务。"],
  rework_needs_human: ["退回意见超出自动处理范围", "可以修改意见后重新退回，也可以直接通过或终止任务。"],
  awaiting_review: ["待审核", "请审核交付物，决定通过或退回。"],
};

export function pauseTextOf(state: TaskState, code: PauseView["code"]): { title: string; resolution: string } {
  const [title, resolution] = PAUSE_TEXT[code] ?? [STATE_LABELS[state], "请按说明处理。"];
  return { title, resolution };
}
