// ISO 周与日期工具。日期一律用 "YYYY-MM-DD" 字符串，内部用 UTC 计算，避免时区漂移。
import type { IsoWeek, PeriodRange } from "@de/core/contract";

const DAY_MS = 86_400_000;

export function parseDate(s: string): number {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s);
  if (!m) throw new Error(`日期格式不正确：${s}`);
  return Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
}

export function formatDate(ms: number): string {
  return new Date(ms).toISOString().slice(0, 10);
}

export function addDays(date: string, days: number): string {
  return formatDate(parseDate(date) + days * DAY_MS);
}

/** 第 week 周的周一（ISO：包含 1 月 4 日的那一周为第 1 周） */
function mondayOf(year: number, week: number): number {
  const jan4 = Date.UTC(year, 0, 4);
  const dow = (new Date(jan4).getUTCDay() + 6) % 7; // 周一=0
  return jan4 - dow * DAY_MS + (week - 1) * 7 * DAY_MS;
}

export function periodRange(w: IsoWeek): PeriodRange {
  const monday = mondayOf(w.year, w.week);
  return { year: w.year, week: w.week, from: formatDate(monday), to: formatDate(monday + 6 * DAY_MS) };
}

export function isoWeekOf(date: string): IsoWeek {
  const ms = parseDate(date);
  const dow = (new Date(ms).getUTCDay() + 6) % 7;
  const thursday = ms - dow * DAY_MS + 3 * DAY_MS;
  const year = new Date(thursday).getUTCFullYear();
  const week = Math.floor((thursday - mondayOf(year, 1)) / (7 * DAY_MS)) + 1;
  return { year, week };
}

export function shiftWeek(w: IsoWeek, delta: number): IsoWeek {
  return isoWeekOf(addDays(periodRange(w).from, delta * 7));
}

export function weekKey(w: IsoWeek): string {
  return `${w.year}-W${String(w.week).padStart(2, "0")}`;
}

export function datesBetween(from: string, to: string): string[] {
  const out: string[] = [];
  for (let ms = parseDate(from); ms <= parseDate(to); ms += DAY_MS) out.push(formatDate(ms));
  return out;
}
