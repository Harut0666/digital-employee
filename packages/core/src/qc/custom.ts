// 自定义规则的 6 种模板（contract/qc.ts 的 QC_TEMPLATES）。params 形状：
//   forbidden_terms      { terms: string[] }                      全文不得出现
//   required_terms       { terms: string[] }                      全文必须出现
//   section_must_contain { section: string, terms: string[] }    指定章节（id）必须出现
//   max_items            { section: string, max: number }         指定章节的条数上限（有要点按要点，否则按段落）
//   max_pages            { max: number }                          渲染后估算页数上限（不留余量）
//   number_range         { path: string, min?: number, max?: number }
//                        槽位路径指向的事实值须在区间内，按展示口径比较：金额为元、比例为百分数（95.3 表示 95.3%）、计数为整数
// 参数缺失或类型不对时返回“参数无效”，不带 fixStep（无法自动修正，转人工）。

import type { DraftReport, QcCheckResult, QcInput } from "../contract";
import { FIX_WRITE } from "./builtin";
import { displayValue, resolveSlot } from "./slots";
import { narrativePieces } from "./text";

type Rule = QcInput["config"]["custom"][number];

function fail(rule: Rule, issues: string[]): QcCheckResult {
  return { id: rule.id, label: rule.label, passed: false, issues, fixStep: FIX_WRITE };
}

/** 无法自动修正（参数有误、事实缺失等），不给 fixStep，由引擎转人工。 */
function failManual(rule: Rule, issues: string[]): QcCheckResult {
  return { id: rule.id, label: rule.label, passed: false, issues };
}

function pass(rule: Rule): QcCheckResult {
  return { id: rule.id, label: rule.label, passed: true, issues: [] };
}

function invalid(rule: Rule, what: string): QcCheckResult {
  return failManual(rule, [`自定义规则“${rule.label}”参数无效：${what}`]);
}

function strings(value: unknown): string[] | undefined {
  return Array.isArray(value) && value.length > 0 && value.every((v) => typeof v === "string") ? (value as string[]) : undefined;
}

function sectionText(draft: DraftReport, id: string): { heading: string; text: string } | undefined {
  const s = draft.sections.find((x) => x.id === id);
  return s ? { heading: s.heading, text: [s.heading, ...s.paragraphs, ...(s.bullets ?? [])].join("\n") } : undefined;
}

function fullText(draft: DraftReport): string {
  return narrativePieces(draft).map((p) => p.text).join("\n");
}

export function runCustomRule(rule: Rule, input: QcInput): QcCheckResult {
  const { draft, facts, rendered } = input;
  const p = rule.params;
  switch (rule.template) {
    case "forbidden_terms": {
      const terms = strings(p["terms"]);
      if (!terms) return invalid(rule, "terms 应为非空字符串数组");
      const text = fullText(draft);
      const hits = terms.filter((t) => text.includes(t));
      return hits.length === 0 ? pass(rule) : fail(rule, hits.map((t) => `出现禁止的表述“${t}”`));
    }
    case "required_terms": {
      const terms = strings(p["terms"]);
      if (!terms) return invalid(rule, "terms 应为非空字符串数组");
      const text = fullText(draft);
      const missing = terms.filter((t) => !text.includes(t));
      return missing.length === 0 ? pass(rule) : fail(rule, missing.map((t) => `缺少必须出现的表述“${t}”`));
    }
    case "section_must_contain": {
      const terms = strings(p["terms"]);
      const sectionId = p["section"];
      if (!terms || typeof sectionId !== "string") return invalid(rule, "需要 section（字符串）和 terms（非空字符串数组）");
      const section = sectionText(draft, sectionId);
      if (!section) return fail(rule, [`缺少章节 ${sectionId}`]);
      const missing = terms.filter((t) => !section.text.includes(t));
      return missing.length === 0 ? pass(rule) : fail(rule, missing.map((t) => `「${section.heading}」缺少“${t}”`));
    }
    case "max_items": {
      const sectionId = p["section"];
      const max = p["max"];
      if (typeof sectionId !== "string" || typeof max !== "number") return invalid(rule, "需要 section（字符串）和 max（数字）");
      const s = draft.sections.find((x) => x.id === sectionId);
      if (!s) return fail(rule, [`缺少章节 ${sectionId}`]);
      const n = s.bullets && s.bullets.length > 0 ? s.bullets.length : s.paragraphs.length;
      return n <= max ? pass(rule) : fail(rule, [`「${s.heading}」有 ${n} 条，不得超过 ${max} 条`]);
    }
    case "max_pages": {
      const max = p["max"];
      if (typeof max !== "number") return invalid(rule, "需要 max（数字）");
      if (!rendered) return pass(rule);
      return rendered.estimatedPages <= max ? pass(rule) : fail(rule, [`估算 ${rendered.estimatedPages} 页，超过 ${max} 页，请压缩篇幅`]);
    }
    case "number_range": {
      const path = p["path"];
      const min = p["min"];
      const max = p["max"];
      if (typeof path !== "string" || (min !== undefined && typeof min !== "number") || (max !== undefined && typeof max !== "number") || (min === undefined && max === undefined)) {
        return invalid(rule, "需要 path（字符串）以及 min / max 中至少一个数字");
      }
      const slot = resolveSlot(path, facts);
      if (!slot) return failManual(rule, [`无法在事实中找到 ${path}`]);
      const shown = displayValue(slot);
      if (shown === null) return failManual(rule, [`${path} 暂无数据，无法判断是否在区间内`]);
      const low = min !== undefined && shown < min;
      const high = max !== undefined && shown > max;
      return low || high ? failManual(rule, [`${path} 为 ${shown}，不在 [${min ?? "-∞"}, ${max ?? "+∞"}] 内`]) : pass(rule);
    }
  }
}
