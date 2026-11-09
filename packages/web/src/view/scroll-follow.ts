// 对话区的自动滚动：离底部 24px 以内就跟随到底，用户向上滚动就释放，回到底部再恢复。纯函数。

/** 离底部多近算“在底部” */
export const FOLLOW_THRESHOLD = 24;

export interface ScrollMetrics {
  scrollTop: number;
  scrollHeight: number;
  clientHeight: number;
}

/** 当前位置是否在底部附近（滚动事件里用它决定继续跟随还是释放）。 */
export function isNearBottom(m: ScrollMetrics, threshold: number = FOLLOW_THRESHOLD): boolean {
  return m.scrollHeight - m.scrollTop - m.clientHeight <= threshold;
}

/**
 * 内容变化后要不要滚到底、怎么滚。减少动态效果偏好下直接跳转；
 * 流式增长时也直接跳转（平滑滚动会跟不上逐字增长），只有新增一条内容时才平滑。
 */
export function followAction(input: { following: boolean; reducedMotion: boolean; grewOnly: boolean }): "none" | "jump" | "smooth" {
  if (!input.following) return "none";
  if (input.reducedMotion || input.grewOnly) return "jump";
  return "smooth";
}
