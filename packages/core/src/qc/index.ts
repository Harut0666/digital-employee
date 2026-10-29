// T6 实现：质检引擎与槽位渲染。
import type { QcCheckResult, QcEngine, QcInput } from "../contract";
import {
  checkAnomalyCoverage,
  checkContradictionCoverage,
  checkFeedbackResponse,
  checkForbiddenTerms,
  checkLength,
  checkQuoteIntegrity,
  checkStructure,
  checkTraceability,
} from "./builtin";
import { runCustomRule } from "./custom";

export { renderSlots } from "./slots";

export function createQcEngine(): QcEngine {
  return {
    run(input: QcInput): QcCheckResult[] {
      const { builtin, custom } = input.config;
      const results: (QcCheckResult | undefined)[] = [
        builtin.structure.enabled ? checkStructure(input) : undefined,
        builtin.traceability.enabled ? checkTraceability(input) : undefined,
        builtin.anomaly_coverage.enabled ? checkAnomalyCoverage(input) : undefined,
        builtin.quote_integrity.enabled ? checkQuoteIntegrity(input) : undefined,
        builtin.contradiction_coverage.enabled ? checkContradictionCoverage(input) : undefined,
        builtin.forbidden_terms.enabled ? checkForbiddenTerms(input) : undefined,
        builtin.length.enabled ? checkLength(input) : undefined,
        builtin.feedback_response.enabled ? checkFeedbackResponse(input) : undefined,
        ...custom.filter((rule) => rule.enabled).map((rule) => runCustomRule(rule, input)),
      ];
      return results.filter((r): r is QcCheckResult => r !== undefined);
    },
  };
}
