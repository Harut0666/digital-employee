// 7 类内置质检（docs/design.md §10.5）。每项返回一个 QcCheckResult；不通过时给出 issues 和应重跑的 fixStep。

import type { QcCheckResult, QcInput } from "../contract";
import { renderSlots } from "./slots";
import { countSentences, entityNamesWithDigits, findBareNumbers, narrativePieces } from "./text";

/** 场景 A 的默认步骤 id；实际取值由引擎按 workflow.qcFixSteps 通过 QcInput.fixSteps 传入。 */
export const FIX_WRITE = "write_report";
export const FIX_CAUSES = "match_causes";

export const fixWrite = (input: QcInput): string => input.fixSteps?.write ?? FIX_WRITE;
export const fixCauses = (input: QcInput): string => input.fixSteps?.causes ?? FIX_CAUSES;

/** 篇幅留约 10% 余量（design.md §10.5）。 */
export const LENGTH_MARGIN = 0.9;

function result(id: string, label: string, issues: string[], fixStep: string): QcCheckResult {
  return issues.length === 0 ? { id, label, passed: true, issues: [] } : { id, label, passed: false, issues, fixStep };
}

export function checkStructure(input: QcInput): QcCheckResult {
  const { config, draft, rendered } = input;
  const cfg = config.builtin.structure;
  const issues: string[] = [];
  const draftIds = draft.sections.map((s) => s.id);

  const missing = cfg.sections.filter((s) => !draftIds.includes(s.id));
  if (missing.length > 0) issues.push(`缺少章节：${missing.map((s) => s.heading).join("、")}`);

  const present = cfg.sections.filter((s) => draftIds.includes(s.id)).map((s) => draftIds.indexOf(s.id));
  if (present.some((idx, i) => i > 0 && idx < (present[i - 1] ?? -1))) {
    issues.push(`章节顺序不正确，应为：${cfg.sections.map((s) => s.heading).join(" → ")}`);
  }

  // 约定：配置中的第一个章节是摘要，最后一个是下周关注。
  const summaryCfg = cfg.sections[0];
  const nextCfg = cfg.sections[cfg.sections.length - 1];
  const summary = draft.sections.find((s) => s.id === summaryCfg?.id);
  if (summary) {
    const n = countSentences([...summary.paragraphs, ...(summary.bullets ?? [])]);
    if (n > cfg.summaryMaxSentences) issues.push(`摘要有 ${n} 句，不得超过 ${cfg.summaryMaxSentences} 句`);
  }
  const next = draft.sections.find((s) => s.id === nextCfg?.id);
  if (next) {
    const items = next.bullets && next.bullets.length > 0 ? next.bullets.length : next.paragraphs.length;
    if (items > cfg.nextWeekMaxItems) issues.push(`下周关注有 ${items} 条，不得超过 ${cfg.nextWeekMaxItems} 条`);
  }

  // 图表数来自渲染结果；尚未渲染时无从判断，不算不通过。
  if (rendered && rendered.chartCount < cfg.minCharts) {
    issues.push(`图表只有 ${rendered.chartCount} 张，至少需要 ${cfg.minCharts} 张`);
  }
  return result("structure", "结构完整", issues, fixWrite(input));
}

export function checkTraceability(input: QcInput): QcCheckResult {
  const { draft, facts } = input;
  const issues: string[] = [];
  const unresolved = new Set<string>();

  const quotes = draft.causes.flatMap((c) => (c.quote?.trim() ? [c.quote.trim()] : []));
  const names = entityNamesWithDigits(facts);
  for (const piece of narrativePieces(draft)) {
    for (const path of renderSlots(piece.text, facts).unresolved) unresolved.add(path);
    for (const n of findBareNumbers(piece.text, quotes, names)) {
      issues.push(`「${piece.where}」出现未经槽位引用的数字：${n}，请改用 {{路径}} 槽位`);
    }
  }
  for (const path of unresolved) issues.push(`槽位无法对应到事实：{{${path}}}`);

  const regionSum = Object.values(facts.regions).reduce((sum, m) => sum + m.revenueCents, 0);
  if (regionSum !== facts.company.revenueCents) {
    issues.push(`区域合计（${regionSum} 分）不等于全公司实收（${facts.company.revenueCents} 分）`);
  }
  return result("traceability", "数据溯源", issues, fixWrite(input));
}

export function checkForbiddenTerms(input: QcInput): QcCheckResult {
  const { config, draft } = input;
  const issues: string[] = [];
  for (const piece of narrativePieces(draft)) {
    for (const term of config.builtin.forbidden_terms.terms) {
      if (piece.text.includes(term)) issues.push(`「${piece.where}」出现禁用指标“${term}”`);
    }
  }
  return result("forbidden_terms", "禁用指标", issues, fixWrite(input));
}

/** 异常覆盖：环比绝对值超过配置阈值的每条异常，都要在 causes 中有对应条目。 */
export function checkAnomalyCoverage(input: QcInput): QcCheckResult {
  const { config, draft, facts } = input;
  const threshold = config.builtin.anomaly_coverage.threshold;
  const covered = new Set(draft.causes.map((c) => c.anomalyId));
  const issues = facts.anomalies
    .filter((a) => Math.abs(a.wow) > threshold && !covered.has(a.id))
    .map((a) => `异常 ${a.id}（${a.name}）没有出现在“异常与原因”中`);
  return result("anomaly_coverage", "异常覆盖", issues, fixCauses(input));
}

/** 引用真实：原文摘录必须是所注周次纪要中某一段的连续子串。 */
export function checkQuoteIntegrity(input: QcInput): QcCheckResult {
  const { draft, minutes } = input;
  const issues: string[] = [];
  for (const cause of draft.causes) {
    if (cause.level === "unknown") continue;
    const quote = cause.quote?.trim();
    if (!quote || !cause.sourceWeek) {
      issues.push(`${cause.anomalyId} 的原因层级为“${cause.level}”，必须同时给出来源周次和纪要原文摘录`);
      continue;
    }
    const { year, week } = cause.sourceWeek;
    const sources = minutes.minutes.filter((m) => m.week?.year === year && m.week.week === week);
    if (sources.length === 0) {
      issues.push(`${cause.anomalyId} 引用了 ${year} 年第 ${week} 周的纪要，但没有这一周的纪要`);
    } else if (!sources.some((m) => m.paragraphs.some((p) => p.text.includes(quote)))) {
      issues.push(`${cause.anomalyId} 的摘录“${quote}”不是 ${year} 年第 ${week} 周纪要原文的连续片段`);
    }
  }
  return result("quote_integrity", "引用真实", issues, fixCauses(input));
}

/** “异常与原因”章节：按场景 A 的约定先找 id=anomalies，再退化为标题含“异常”的章节。 */
function anomaliesSection(draft: QcInput["draft"]) {
  return draft.sections.find((s) => s.id === "anomalies") ?? draft.sections.find((s) => s.heading.includes("异常"));
}

/**
 * 纪要矛盾覆盖：程序判出的每一条矛盾，草稿都要用 contradictionRefs 引用，
 * 且“异常与原因”章节里出现矛盾对象名。
 */
export function checkContradictionCoverage(input: QcInput): QcCheckResult {
  const { draft, contradictions } = input;
  const issues: string[] = [];
  const refs = new Set(draft.contradictionRefs ?? []);
  const section = anomaliesSection(draft);
  const sectionText = section ? [section.heading, ...section.paragraphs, ...(section.bullets ?? [])].join("\n") : undefined;
  for (const contradiction of contradictions ?? []) {
    if (!refs.has(contradiction.id)) {
      issues.push(`纪要矛盾 ${contradiction.id}（${contradiction.subject.name}）没有写进“异常与原因”章节`);
    }
    // 缺章节由“结构完整”负责；这里只在章节存在时核对对象名，避免同一个问题报两遍。
    if (sectionText !== undefined && !sectionText.includes(contradiction.subject.name)) {
      issues.push(`“异常与原因”章节没有提到矛盾对象“${contradiction.subject.name}”`);
    }
  }
  return result("contradiction_coverage", "纪要矛盾覆盖", issues, fixWrite(input));
}

/** 意见响应（仅重做时）：修改说明覆盖退回意见，且被要求修改的章节内容确实变了。 */
export function checkFeedbackResponse(input: QcInput): QcCheckResult | undefined {
  const { draft, previousDraft, feedback } = input;
  if (!feedback) return undefined;
  const issues: string[] = [];
  const notes = draft.revisionNotes ?? [];
  const comment = feedback.comment.trim();
  const matching = notes.filter((n) => n.comment.trim() === comment || n.comment.includes(comment));
  if (matching.length === 0) {
    issues.push(`修改说明（revisionNotes）没有覆盖退回意见“${comment}”`);
  } else if (matching.every((n) => n.change.trim().length === 0)) {
    issues.push("修改说明没有写明具体改了什么");
  }

  const changed = (id: string): boolean | undefined => {
    if (!previousDraft) return undefined; // 没有上一版就无从比较
    const before = previousDraft.sections.find((s) => s.id === id);
    const after = draft.sections.find((s) => s.id === id);
    return JSON.stringify(before) !== JSON.stringify(after);
  };

  const required = feedback.sections ?? [];
  const mentioned = new Set(matching.flatMap((n) => n.sections));
  for (const id of required) {
    if (matching.length > 0 && !mentioned.has(id)) issues.push(`修改说明没有提到被要求修改的章节 ${id}`);
    if (changed(id) === false) issues.push(`章节 ${id} 的内容与上一版相同，没有按意见修改`);
  }
  if (required.length === 0 && previousDraft && !draft.sections.some((s) => changed(s.id))) {
    issues.push("所有章节都与上一版相同，没有按意见修改");
  }
  return result("feedback_response", "意见响应", issues, fixWrite(input));
}

export function checkLength(input: QcInput): QcCheckResult {
  const { config, rendered } = input;
  const issues: string[] = [];
  const limit = config.builtin.length.maxPages * LENGTH_MARGIN;
  if (rendered && rendered.estimatedPages > limit) {
    issues.push(`估算 ${rendered.estimatedPages} 页，超过上限 ${config.builtin.length.maxPages} 页（留 10% 余量即 ${limit} 页），请压缩篇幅`);
  }
  return result("length", "篇幅", issues, fixWrite(input));
}
