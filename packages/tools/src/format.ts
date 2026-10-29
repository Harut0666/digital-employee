// 数字展示格式：金额取整数元带千分位，比例保留 1 位小数（四舍五入，远离零）。
const round1 = (x: number): number => (Math.sign(x) * Math.round(Math.abs(x) * 1000)) / 10;

export const fmtYuan = (cents: number): string =>
  ((Math.sign(cents) * Math.round(Math.abs(cents) / 100)) || 0).toLocaleString("en-US");

export const fmtPct = (x: number | null, empty = "—"): string => (x === null ? empty : `${(round1(x) || 0).toFixed(1)}%`);

/** 环比：带正负号，不可比时给出说明 */
export const fmtWow = (x: number | null): string => {
  if (x === null) return "不可比";
  const v = round1(x) || 0;
  return `${v > 0 ? "+" : ""}${v.toFixed(1)}%`;
};
