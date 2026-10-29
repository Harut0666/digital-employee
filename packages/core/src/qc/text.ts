// 草稿文本的收集与裸数字扫描。

import type { DraftReport } from "../contract";
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
    for (const p of section.paragraphs) pieces.push({ where: section.heading, text: p });
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
 *  5. 月日：10月26日、11月1日、10月、26日、1号
 *  6. 排名范围：前5、前 5
 *  7. 行首序号：1. / 1、/ （1）
 */
export const BARE_NUMBER_WHITELIST: readonly RegExp[] = [
  /[A-Za-z][A-Za-z0-9_-]*\d[A-Za-z0-9_-]*/g,
  /\d{4}-(?:W\d{1,2}|\d{1,2}-\d{1,2})/g,
  /\d{4}\s*年/g,
  /第?\s*\d+\s*周/g,
  /\d{1,2}\s*月(?:\s*\d{1,2}\s*[日号])?/g,
  /\d{1,2}\s*[日号]/g,
  /前\s*\d+/g,
  /^\s*[（(]?\d+[)）.、]/g,
];

const BARE_NUMBER = /[+\-−]?\d[\d,]*(?:\.\d+)?\s*[%％]?/g;

/** 返回文本中槽位之外、也不在白名单内的裸数字（原样片段）。 */
export function findBareNumbers(text: string): string[] {
  let masked = text.replace(SLOT_PATTERN, " ");
  for (const pattern of BARE_NUMBER_WHITELIST) masked = masked.replace(pattern, " ");
  return [...masked.matchAll(BARE_NUMBER)].map((m) => m[0].trim());
}

/** 按句末标点切句，空句不计。 */
export function countSentences(paragraphs: string[]): number {
  return paragraphs.flatMap((p) => p.split(/[。！？!?]+/)).filter((s) => s.trim().length > 0).length;
}
