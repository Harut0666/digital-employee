// 对话区的自动滚动判断：离底部 24px 以内算“在底部”（hooks/stick-to-bottom 用它判断人是不是往上翻了）。纯函数。

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
