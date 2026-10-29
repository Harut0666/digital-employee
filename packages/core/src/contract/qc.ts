// 质检契约（docs/design.md §10.5、§4.1）。

export const BUILTIN_QC_CHECKS = [
  "structure",
  "traceability",
  "anomaly_coverage",
  "quote_integrity",
  "contradiction_coverage",
  "forbidden_terms",
  "length",
  "feedback_response",
] as const;
export type BuiltinQcCheck = (typeof BUILTIN_QC_CHECKS)[number];

export const QC_TEMPLATES = [
  "forbidden_terms",
  "required_terms",
  "section_must_contain",
  "max_items",
  "max_pages",
  "number_range",
] as const;
export type QcTemplate = (typeof QC_TEMPLATES)[number];

/** 模板的中文名（配置页的“从模板新增规则”菜单用）。 */
export const QC_TEMPLATE_LABELS: Record<QcTemplate, string> = {
  forbidden_terms: "禁用词",
  required_terms: "必含词",
  section_must_contain: "章节必含内容",
  max_items: "条数上限",
  max_pages: "篇幅上限",
  number_range: "数值范围",
};

export interface QcCheckResult {
  /** 内置检查名，或自定义规则的 id */
  id: string;
  label: string;
  passed: boolean;
  /** 不通过时给出的具体问题，会作为修正提示交给判断步骤 */
  issues: string[];
  /** 不通过时应重跑的步骤；为空表示无法自动修正，直接转人工 */
  fixStep?: string;
}
