// 命令可用性：由契约里的状态流转表 TRANSITIONS 推导，按钮是否可点完全跟着它走，
// 与后端的校验同源（后端仍是最终裁判）。

import { TRANSITIONS } from "@de/core/contract";
import type { PauseReason, Role, TaskCommand, TaskState } from "@de/core/contract";

export type CommandType = TaskCommand["type"];

/** 命令 → 流转表里的触发器。目前两者同名。 */
const COMMAND_TRIGGERS: Record<CommandType, string> = {
  submit: "submit",
  supplement: "supplement",
  continue_without_minutes: "continue_without_minutes",
  continue_with_partial: "continue_with_partial",
  retry: "retry",
  terminate: "terminate",
  approve: "approve",
  return: "return",
};

export function availableCommands(state: TaskState, role: Role, pause?: Pick<PauseReason, "options">): CommandType[] {
  const out = new Set<CommandType>();
  for (const t of TRANSITIONS) {
    if (t.from !== state || t.actor !== role) continue;
    for (const [cmd, trigger] of Object.entries(COMMAND_TRIGGERS) as [CommandType, string][]) {
      if (trigger === t.trigger) out.add(cmd);
    }
  }
  const all = [...out];
  return pause ? all.filter((c) => pause.options.includes(c)) : all;
}

export function isCommandAllowed(cmd: CommandType, state: TaskState, role: Role, pause?: Pick<PauseReason, "options">): boolean {
  return availableCommands(state, role, pause).includes(cmd);
}

export const ROLE_LABELS: Record<Exclude<Role, "system">, string> = {
  initiator: "发起人",
  reviewer: "审核人",
  operator: "操作员",
};
