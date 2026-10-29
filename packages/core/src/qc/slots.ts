// 槽位渲染（ADR-0002、contract/facts.ts）：把 {{path}} 替换为格式化后的事实值。

import { SLOT_FORMATS, type RenderSlots, type SlotFormat, type WeeklyFacts } from "../contract";

/** 匹配 {{path}}，path 内不含花括号，允许首尾空格。 */
export const SLOT_PATTERN = /\{\{\s*([^{}]+?)\s*\}\}/g;

/** 字段别名：叙述里写 revenue，事实里叫 revenueCents。 */
const FIELD_ALIASES: Record<string, string> = { revenue: "revenueCents" };

/** 数组元素的业务键，按顺序尝试：stores→code、categories→category、anomalies→id/key。 */
const ARRAY_KEYS = ["code", "category", "id", "key", "name"] as const;

export interface ResolvedSlot {
  format: SlotFormat;
  /** 事实上的原始值（金额为分，比例为小数）；null 表示事实存在但不可得 */
  value: number | null;
  /** 路径最后一段，用于区分环比与其他占比 */
  field: string;
}

/** 找出文本中的所有槽位路径（按出现顺序，含重复）。 */
export function extractSlotPaths(text: string): string[] {
  return [...text.matchAll(SLOT_PATTERN)].map((m) => (m[1] ?? "").trim());
}

/** 按路径在事实上取值；无法解析（路径不存在、不是数字、字段没有渲染格式）返回 undefined。 */
export function resolveSlot(path: string, facts: WeeklyFacts): ResolvedSlot | undefined {
  const segments = path.split(".");
  const last = segments[segments.length - 1];
  if (last === undefined || segments.length < 2) return undefined;
  const field = FIELD_ALIASES[last] ?? last;
  const format = SLOT_FORMATS[last] ?? SLOT_FORMATS[field];
  if (format === undefined) return undefined;

  let node: unknown = facts;
  for (const segment of segments.slice(0, -1)) {
    node = step(node, segment);
    if (node === undefined) return undefined;
  }
  if (typeof node !== "object" || node === null || Array.isArray(node)) return undefined;
  const value = (node as Record<string, unknown>)[field];
  if (value === null) return { format, value: null, field };
  if (typeof value !== "number" || !Number.isFinite(value)) return undefined;
  return { format, value, field };
}

function step(node: unknown, segment: string): unknown {
  if (Array.isArray(node)) {
    return node.find((item) => typeof item === "object" && item !== null && ARRAY_KEYS.some((k) => (item as Record<string, unknown>)[k] === segment));
  }
  if (typeof node === "object" && node !== null && Object.hasOwn(node, segment)) {
    return (node as Record<string, unknown>)[segment];
  }
  return undefined;
}

/** 四舍五入（远离零），先收敛浮点误差，避免 0.1235*1000 这类边界值被舍错。 */
function roundHalfAway(x: number): number {
  const fixed = Number(Math.abs(x).toPrecision(12));
  return Math.sign(x) * Math.round(fixed);
}

function groupThousands(n: number): string {
  return Math.abs(n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

/** 展示用数值：金额为元（取整），比例为百分数（一位小数），计数为整数。供 number_range 规则按展示口径比较。 */
export function displayValue(slot: ResolvedSlot): number | null {
  if (slot.value === null) return null;
  switch (slot.format) {
    case "yuan":
      return roundHalfAway(slot.value / 100);
    case "percent1":
      return roundHalfAway(slot.value * 1000) / 10;
    case "count":
      return Math.round(slot.value);
  }
}

function formatSlot(slot: ResolvedSlot): string {
  if (slot.value === null) return slot.field === "wow" ? "不可比（无上周数据）" : "暂无";
  const shown = displayValue(slot) ?? 0;
  const sign = shown < 0 ? "-" : "";
  switch (slot.format) {
    case "yuan":
      return `${sign}${groupThousands(shown)} 元`;
    case "percent1": {
      // 只有环比是“变化量”，正数带加号；占比类（毛利率、达成率…）不带。
      const plus = slot.field === "wow" && shown > 0 ? "+" : "";
      return `${plus}${sign}${Math.abs(shown).toFixed(1)}%`;
    }
    case "count":
      return `${sign}${groupThousands(shown)}`;
  }
}

export const renderSlots: RenderSlots = (text, facts) => {
  const unresolved: string[] = [];
  const rendered = text.replace(SLOT_PATTERN, (whole, raw: string) => {
    const path = raw.trim();
    const slot = resolveSlot(path, facts);
    if (slot === undefined) {
      if (!unresolved.includes(path)) unresolved.push(path);
      return whole;
    }
    return formatSlot(slot);
  });
  return { text: rendered, unresolved };
};
