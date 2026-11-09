// 会话事件流里哪些事件要让界面重读会话。纯函数。

import type { SessionAgentEvent } from "../api/types";

/**
 * - turn_start：用户的话在这一轮开始之前就写进了日志，重读让正式消息尽早进来（乐观显示的那条随之让位）
 * - turn_end：这一轮完整的回复、卡片这一刻才写下来
 * - 带流程卡片的消息：权限确认等卡片在这一轮进行中就要让人看到，一轮停在确认上不会有 turn_end
 */
export function reloadOnEvent(event: SessionAgentEvent): boolean {
  return event.type === "turn_start" || event.type === "turn_end" || (event.type === "message" && Boolean(event.message?.card));
}
