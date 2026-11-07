// 模型调用错误的分类（Spec 0005“模型不可用”）。只有接口层的失败带类别；
// 模型输出不合规仍是判断步骤失败，不走这里。
package agent

import (
	"errors"
	"fmt"
	"net/http"

	"digital-employee/internal/contract"
)

// ModelCallError 模型接口拒绝请求或连不上。Message 已抹掉 API Key，可以写进事件。
type ModelCallError struct {
	Kind    contract.ModelErrorKind
	Message string
}

func (e *ModelCallError) Error() string { return e.Message }

func newModelCallError(kind contract.ModelErrorKind, apiKey, format string, args ...any) *ModelCallError {
	return &ModelCallError{Kind: kind, Message: RedactAPIKey(fmt.Sprintf(format, args...), apiKey)}
}

// modelErrorKindOf 按 HTTP 状态码分类。规格只分四类，其余的拒绝都归服务端错误。
func modelErrorKindOf(status int) contract.ModelErrorKind {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return contract.ModelErrorAuth
	case http.StatusPaymentRequired, http.StatusTooManyRequests:
		return contract.ModelErrorQuota
	}
	return contract.ModelErrorServer
}

// retriesCanHelp 认证失败时换多少次结果都一样，其余类别按配置重试。
func retriesCanHelp(err error) bool {
	var callErr *ModelCallError
	return !errors.As(err, &callErr) || callErr.Kind != contract.ModelErrorAuth
}
