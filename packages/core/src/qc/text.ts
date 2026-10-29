// 草稿文本的收集与裸数字扫描。

import type { DraftReport, WeeklyFacts } from "../contract";
import { SLOT_PATTERN } from "./slots";

export interface TextPiece {
  /** 用于问题描述的位置，如 “本周摘要” */
  where: string;
  text: string;
}

/**
 * 叙述文本：标题、各章节的段落和要点、异常原因说明。
 * 不含原因摘录 quote（那是纪要原文，允许带数字）和修改说明。
 */
export function narrativePieces(draft: DraftReport): TextPiece[] {
  const pieces: TextPiece[] = [{ where: "标题", text: draft.title }];
  for (const section of draft.sections) {
    for (const p of section.paragraphs ?? []) pieces.push({ where: section.heading, text: p });
    for (const b of section.bullets ?? []) pieces.push({ where: section.heading, text: b });
  }
  for (const c of draft.causes) pieces.push({ where: `异常原因（${c.anomalyId}）`, text: c.cause });
  return pieces;
}

/**
 * 裸数字白名单：这些数字不是“事实”，允许直接出现在叙述里。扫描前先把它们抹掉。
 *  1. 标识符：字母开头且含数字，如门店编码 HZ01、周标签 W44、纪要段落 W44-p07
 *  2. ISO 日期与周：2026-10-26、2026-W44
 *  3. 年份：2026年
 *  4. 周次：第44周、近4周、4周
 *  5. 月日：10月26日、10 月 27 日、11月1日、10月、26日、1号
 *  5b. 斜杠日期：10/27、11/03（月/日）
 *  6. 排名范围：前5、前 5
 *  7. 行首序号：1. / 1、/ （1）
 *
 * 另有一类按上下文放行的数字（不在此列表里，见 maskQuotedSpans）：落在中文引号“…”或「…」内、
 * 且逐字出自某条原因的纪要摘录（causes[].quote）的片段。
 */
export const BARE_NUMBER_WHITELIST: readonly RegExp[] = [
  /[A-Za-z][A-Za-z0-9_-]*\d[A-Za-z0-9_-]*/g,
  /\d{4}-(?:W\d{1,2}|\d{1,2}-\d{1,2})/g,
  /\d{4}\s*年/g,
  /第?\s*\d+\s*周/g,
  /\d{1,2}\s*月(?:\s*\d{1,2}\s*[日号])?/g,
  /\d{1,2}\s*[日号]/g,
  /(?<![\d.])\d{1,2}\s*\/\s*\d{1,2}(?![\d.\/])/g,
  /前\s*\d+/g,
  /^\s*[（(]?\d+[)）.、]/g,
];

const BARE_NUMBER = /[+\-−]?\d[\d,]*(?:\.\d+)?\s*[%％]?/g;

const QUOTE_PAIRS: Record<string, string> = { "”": "“", "」": "「" };

/**
 * 把落在引号“…”「…」内、且内容逐字出自 quotes 之一的片段抹掉（替换为空格）。
 * 引号可以嵌套，如 「“第二杯半价”活动已于 10 月 25 日结束」；外层或内层任一层满足条件即抹掉。
 */
export function maskQuotedSpans(text: string, quotes: readonly string[]): string {
  if (quotes.length === 0) return text;
  const stack: { open: string; index: number }[] = [];
  const ranges: [number, number][] = [];
  for (let i = 0; i < text.length; i++) {
    const ch = text[i]!;
    if (ch === "“" || ch === "「") {
      stack.push({ open: ch, index: i });
    } else if (ch in QUOTE_PAIRS) {
      const top = stack[stack.length - 1];
      if (!top || top.open !== QUOTE_PAIRS[ch]) continue;
      stack.pop();
      const inner = text.slice(top.index + 1, i).trim();
      if (inner.length > 0 && quotes.some((q) => q.includes(inner))) ranges.push([top.index, i + 1]);
    }
  }
  let masked = text;
  for (const [from, to] of ranges) masked = masked.slice(0, from) + " ".repeat(to - from) + masked.slice(to);
  return masked;
}

/** 事实里名称带数字的实体（如单品“6寸生日蛋糕”），名称本身不是数字事实。长名称在前，避免被短名称截断。 */
export function entityNamesWithDigits(facts: WeeklyFacts): string[] {
  const names = [
    ...facts.topProducts.map((p) => p.name),
    ...facts.stores.map((s) => s.name),
    ...facts.categories.map((c) => c.category),
    ...Object.keys(facts.regions),
    ...facts.anomalies.map((a) => a.name),
  ];
  return [...new Set(names.filter((n) => /\d/.test(n)))].sort((a, b) => b.length - a.length);
}

/**
 * 返回文本中槽位之外、也不在白名单内的裸数字（原样片段）。
 * quotes 是各条原因的纪要摘录；引号内逐字出自它们的数字视为引用原文，不算裸数字。
 * entityNames 是事实里带数字的实体名称，出现时整体抹掉。
 */
export function findBareNumbers(text: string, quotes: readonly string[] = [], entityNames: readonly string[] = []): string[] {
  let masked = maskQuotedSpans(text, quotes).replace(SLOT_PATTERN, " ");
  for (const name of entityNames) masked = masked.split(name).join(" ");
  for (const pattern of BARE_NUMBER_WHITELIST) masked = masked.replace(pattern, " ");
  return [...masked.matchAll(BARE_NUMBER)].map((m) => m[0].trim());
}

/** 按句末标点切句，空句不计。 */
export function countSentences(paragraphs: string[]): number {
  return paragraphs.flatMap((p) => p.split(/[。！？!?]+/)).filter((s) => s.trim().length > 0).length;
}
