// ISO 周（周一至周日）和日期范围的换算，给表单里的周选择器用。
// 只按日历日期算，统一用 UTC 避免时区和夏令时干扰。

const DAY_MS = 86_400_000;

export interface IsoWeek {
  year: number;
  week: number;
}

/** 某个 ISO 周的周一（UTC 零点） */
export function isoWeekMonday(year: number, week: number): Date {
  // 1 月 4 日总在第 1 周
  const jan4 = Date.UTC(year, 0, 4);
  const weekday = (new Date(jan4).getUTCDay() + 6) % 7; // 周一 = 0
  return new Date(jan4 - weekday * DAY_MS + (week - 1) * 7 * DAY_MS);
}

/** 本地日期所在的 ISO 周 */
export function isoWeekOf(date: Date): IsoWeek {
  const day = Date.UTC(date.getFullYear(), date.getMonth(), date.getDate());
  const weekday = (new Date(day).getUTCDay() + 6) % 7;
  // 同一周的周四所在年份就是 ISO 年
  const thursday = new Date(day + (3 - weekday) * DAY_MS);
  const year = thursday.getUTCFullYear();
  const week = Math.floor((thursday.getTime() - isoWeekMonday(year, 1).getTime()) / (7 * DAY_MS)) + 1;
  return { year, week };
}

const md = (d: Date) => `${d.getUTCMonth() + 1} 月 ${d.getUTCDate()} 日`;

/** “10 月 26 日至 11 月 1 日”；跨年时写出年份 */
export function formatWeekRange(year: number, week: number): string {
  const monday = isoWeekMonday(year, week);
  const sunday = new Date(monday.getTime() + 6 * DAY_MS);
  if (monday.getUTCFullYear() !== sunday.getUTCFullYear() || monday.getUTCFullYear() !== year) {
    return `${monday.getUTCFullYear()} 年 ${md(monday)}至 ${sunday.getUTCFullYear()} 年 ${md(sunday)}`;
  }
  return `${md(monday)}至 ${md(sunday)}`;
}
