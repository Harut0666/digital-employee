// 会话与任务一对多（Spec 0007）：一个会话先后运行多个任务，最新的一个是“当前任务”。
// 纯函数，不依赖 React 与网络。

import type { SessionSummary } from "../api/types";

/** 会话里按开工顺序的全部任务；旧后端只给 taskId 时读作一个任务。 */
export function taskIdsOf(session: Pick<SessionSummary, "taskId" | "taskIds">): string[] {
  if (session.taskIds && session.taskIds.length > 0) return session.taskIds;
  return session.taskId ? [session.taskId] : [];
}

/** 当前任务：列表里最新的一个；会话还没有任务时为空。 */
export function latestTaskId(session: Pick<SessionSummary, "taskId" | "taskIds">): string | undefined {
  return taskIdsOf(session).at(-1);
}

/** 早先的任务（都已结束）：对话里按它们各自的时间点显示步骤行和交付物。 */
export function pastTaskIds(session: Pick<SessionSummary, "taskId" | "taskIds">): string[] {
  return taskIdsOf(session).slice(0, -1);
}
