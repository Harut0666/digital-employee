// 质检引擎：8 类内置检查 + 自定义规则，按 qc.yaml 的开关与顺序执行。
// 移植自 packages/core/src/qc/index.ts。
package qc

import "digital-employee/internal/contract"

// Engine 质检引擎。
type Engine struct{}

// NewEngine 创建质检引擎（无状态）。
func NewEngine() *Engine { return &Engine{} }

// Run 执行全部已启用的检查。顺序与 TS 版一致：8 类内置检查在前，自定义规则在后。
func (e *Engine) Run(input contract.QcInput) []contract.QcCheckResult {
	builtin := input.Config.Builtin
	custom := input.Config.Custom
	results := []contract.QcCheckResult{}

	if builtin.Structure.Enabled {
		results = append(results, CheckStructure(input))
	}
	if builtin.Traceability.Enabled {
		results = append(results, CheckTraceability(input))
	}
	if builtin.AnomalyCoverage.Enabled {
		results = append(results, CheckAnomalyCoverage(input))
	}
	if builtin.QuoteIntegrity.Enabled {
		results = append(results, CheckQuoteIntegrity(input))
	}
	if builtin.ContradictionCoverage.Enabled {
		results = append(results, CheckContradictionCoverage(input))
	}
	if builtin.ForbiddenTerms.Enabled {
		results = append(results, CheckForbiddenTerms(input))
	}
	if builtin.Length.Enabled {
		results = append(results, CheckLength(input))
	}
	if builtin.FeedbackResponse.Enabled {
		if result := CheckFeedbackResponse(input); result != nil {
			results = append(results, *result)
		}
	}
	for _, rule := range custom {
		if !rule.Enabled {
			continue
		}
		if result := RunCustomRule(rule, input); result != nil {
			results = append(results, *result)
		}
	}
	return results
}
