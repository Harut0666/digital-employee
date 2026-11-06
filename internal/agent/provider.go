// 模型提供者的内部形状（移植自 packages/core/src/agent/provider.ts）。
//
// 契约里的 ModelProvider 只有 kind；这里在其上加 resolve：判断步骤执行器按
// “步骤 id + 步骤配置里的模型引用”向提供者要一份可用的模型和 stream 函数。
package agent

import (
	"context"

	"digital-employee/internal/contract"
)

// ToolSpec 交给模型的工具描述。
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// StreamRequest 一次模型调用的请求。
type StreamRequest struct {
	Messages []Message
	Tools    []ToolSpec
}

// StreamOptions 一次模型调用的选项。
type StreamOptions struct {
	APIKey     string
	MaxTokens  int
	MaxRetries int
	TimeoutMS  *int
}

// StreamFunc 调一次模型，返回完整的助手消息（失败编码进消息的 stopReason，不抛异常）。
type StreamFunc func(ctx context.Context, model Model, request StreamRequest, options StreamOptions) (*AssistantMessage, error)

// ResolvedStepModel 一个步骤实际使用的模型与参数。
type ResolvedStepModel struct {
	Model         Model
	Stream        StreamFunc
	APIKey        string
	ThinkingLevel string
	TimeoutMS     *int
	MaxRetries    *int
}

// ModelProvider 模型提供者：真实模型与剧本式假模型都实现它。
type ModelProvider interface {
	// Kind "real" | "scripted"
	Kind() string
	// Resolve 取某个步骤可用的模型与 stream 函数。
	Resolve(stepID string, ref *contract.ModelRef) (*ResolvedStepModel, error)
}

// NormalizeThinkingLevel 推理档位归一化：只接受契约里的六个值，其它（手改坏值、旧文件）一律按 off。
func NormalizeThinkingLevel(value *string) string {
	if value == nil {
		return contract.ThinkingOff
	}
	for _, level := range contract.ThinkingLevels {
		if level == *value {
			return *value
		}
	}
	return contract.ThinkingOff
}

// EmptyUsage 全零用量。
func EmptyUsage() Usage {
	return Usage{
		Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0, TotalTokens: 0,
		Cost: Cost{},
	}
}
