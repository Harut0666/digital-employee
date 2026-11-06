// 执行参数的三级解析（Spec 0004「执行与预算」）。
//
// 每个值都由“更靠近步骤的配置”优先，其次才是设置文档里的默认值，最后是内置值：
//
//	步骤超时     step.limits.durationSec       → settings.execution.stepTimeoutMs  → 120s
//	模型重试     连接上的 maxRetries            → settings.execution.modelMaxRetries → 2 次
//	质检自动修正 workflow.limits.qcAutofix      → settings.execution.qcAutofix      → 3 次
//	工具退避     workflow.limits.toolRetry.backoffMs（非空）→ settings.execution.retryBackoffMs → [500,1500,4000]
//
// 移植自 packages/core/src/agent/execution.ts。
package agent

import "digital-employee/internal/contract"

// 内置步骤超时：设置文件缺失或没配时的兜底。
const BuiltinStepTimeoutMS = 120_000

// 内置模型重试次数（与 pi-ai 客户端默认值一致）。
const BuiltinModelMaxRetries = 2

// 内置质检自动修正上限（Spec 0004）。
const BuiltinQcAutofix = 3

// builtinRetryBackoffMS 内置工具重试退避。
var builtinRetryBackoffMS = []int{500, 1500, 4000}

func positiveNumber(value *float64) (float64, bool) {
	if value == nil || *value <= 0 {
		return 0, false
	}
	return *value, true
}

func nonNegativeInt(value *int) (int, bool) {
	if value == nil || *value < 0 {
		return 0, false
	}
	return *value, true
}

// ResolveStepTimeoutMS 步骤超时（毫秒）：步骤配置 > 设置默认 > 内置 120 秒；非法值视作没配置。
func ResolveStepTimeoutMS(stepTimeoutMS *float64, execution *contract.ExecutionSettings) int {
	if value, ok := positiveNumber(stepTimeoutMS); ok {
		return int(value)
	}
	if execution != nil && execution.StepTimeoutMS > 0 {
		return execution.StepTimeoutMS
	}
	return BuiltinStepTimeoutMS
}

// ResolveModelMaxRetries 模型调用重试次数：连接配置 > 设置默认 > 内置 2 次；0 表示不重试，是合法值。
func ResolveModelMaxRetries(connectionMaxRetries *int, execution *contract.ExecutionSettings) int {
	if value, ok := nonNegativeInt(connectionMaxRetries); ok {
		return value
	}
	if execution != nil && execution.ModelMaxRetries >= 0 {
		return execution.ModelMaxRetries
	}
	return BuiltinModelMaxRetries
}

// ResolveQcAutofix 质检自动修正上限：员工配置 > 设置默认 > 内置 3 次；0 表示不自动修正。
func ResolveQcAutofix(workflowQcAutofix *int, execution *contract.ExecutionSettings) int {
	if value, ok := nonNegativeInt(workflowQcAutofix); ok {
		return value
	}
	if execution != nil && execution.QcAutofix >= 0 {
		return execution.QcAutofix
	}
	return BuiltinQcAutofix
}

// ResolveToolRetryBackoffMS 工具重试退避（毫秒数组）：员工配置写了非空数组就以它为准；
// 空数组（或没写）时回落到设置默认值，最后是内置值。
func ResolveToolRetryBackoffMS(workflowBackoffMS []int, execution *contract.ExecutionSettings) []int {
	if len(workflowBackoffMS) > 0 {
		return append([]int(nil), workflowBackoffMS...)
	}
	if execution != nil && len(execution.RetryBackoffMS) > 0 {
		return append([]int(nil), execution.RetryBackoffMS...)
	}
	return append([]int(nil), builtinRetryBackoffMS...)
}
