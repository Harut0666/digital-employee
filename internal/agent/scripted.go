// 剧本式假模型：按步骤 id 依次返回 ScriptedTurn，不发任何网络请求。
// 移植自 packages/core/src/agent/scripted.ts。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"digital-employee/internal/contract"
)

// ScriptedToolCall 剧本里的一次工具调用。
type ScriptedToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// ScriptedTurn 剧本式假模型的一轮：要么输出文本，要么发起工具调用，要么报错，要么先延迟再继续。
type ScriptedTurn struct {
	Text      *string
	ToolCalls []ScriptedToolCall
	Error     *string
	DelayMS   *int
	Then      *ScriptedTurn
}

// UnmarshalJSON 按字段名解析一“轮”剧本（{text} / {toolCalls} / {error} / {delayMs, then}）。
func (t *ScriptedTurn) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if value, ok := raw["text"]; ok {
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return err
		}
		t.Text = &text
	}
	if value, ok := raw["error"]; ok {
		var message string
		if err := json.Unmarshal(value, &message); err != nil {
			return err
		}
		t.Error = &message
	}
	if value, ok := raw["toolCalls"]; ok {
		var calls []ScriptedToolCall
		if err := json.Unmarshal(value, &calls); err != nil {
			return err
		}
		t.ToolCalls = calls
	}
	if value, ok := raw["delayMs"]; ok {
		var delay int
		if err := json.Unmarshal(value, &delay); err != nil {
			return err
		}
		t.DelayMS = &delay
	}
	if value, ok := raw["then"]; ok {
		var next ScriptedTurn
		if err := json.Unmarshal(value, &next); err != nil {
			return err
		}
		t.Then = &next
	}
	return nil
}

// ModelScript 按步骤 id 分组的剧本；同一步骤被重跑时按顺序消费后续轮次。
type ModelScript map[string][]ScriptedTurn

// ScriptedRequest 假模型每次被调用时收到的请求，测试用来检查“模型看到了什么”。
type ScriptedRequest struct {
	StepID  string
	Request StreamRequest
	Options StreamOptions
}

// ScriptedProviderOptions 假模型的可选装置。
type ScriptedProviderOptions struct {
	// Model 覆盖模型字段，用来验证每模型参数真的传下去了
	Model *Model
	// TimeoutMS / MaxRetries 模拟连接级超时与重试次数
	TimeoutMS  *int
	MaxRetries *int
}

var scriptedModel = Model{
	ID: "scripted", Name: "Scripted model", API: "openai-completions", Provider: "scripted",
	BaseURL: "http://scripted.invalid", Reasoning: false,
	ContextWindow: 128_000, MaxTokens: 8_192,
}

// ScriptedProvider 剧本式模型提供者。
type ScriptedProvider struct {
	script   ModelScript
	options  ScriptedProviderOptions
	model    Model
	mu       sync.Mutex
	cursors  map[string]int
	requests []ScriptedRequest
	callSeq  int
}

// NewScriptedModelProvider 按剧本创建假模型提供者。
func NewScriptedModelProvider(script ModelScript, options ScriptedProviderOptions) *ScriptedProvider {
	model := scriptedModel
	if options.Model != nil {
		model = *options.Model
	}
	return &ScriptedProvider{
		script:  script,
		options: options,
		model:   model,
		cursors: map[string]int{},
	}
}

// Kind 提供者类型。
func (p *ScriptedProvider) Kind() string { return "scripted" }

// Requests 假模型收到的全部请求（测试用）。
func (p *ScriptedProvider) Requests() []ScriptedRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]ScriptedRequest(nil), p.requests...)
}

// Resolve 取某个步骤的模型与 stream 函数。
func (p *ScriptedProvider) Resolve(stepID string, ref *contract.ModelRef) (*ResolvedStepModel, error) {
	thinking := ""
	if ref != nil && ref.Thinking != nil {
		thinking = NormalizeThinkingLevel(ref.Thinking)
	} else {
		thinking = contract.ThinkingOff
	}
	return &ResolvedStepModel{
		Model:         p.model,
		Stream:        p.streamFor(stepID),
		ThinkingLevel: thinking,
		TimeoutMS:     p.options.TimeoutMS,
		MaxRetries:    p.options.MaxRetries,
	}, nil
}

func (p *ScriptedProvider) nextCallID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callSeq++
	return fmt.Sprintf("scripted_call_%d", p.callSeq)
}

func (p *ScriptedProvider) buildMessage(turn ScriptedTurn) AssistantMessage {
	message := NewAssistantMessage(p.model)
	switch {
	case turn.Error != nil:
		message.StopReason = StopError
		message.ErrorMessage = *turn.Error
	case turn.ToolCalls != nil:
		message.StopReason = StopToolUse
		blocks := make([]ContentBlock, 0, len(turn.ToolCalls))
		for _, call := range turn.ToolCalls {
			blocks = append(blocks, ToolCallBlock(p.nextCallID(), call.Name, call.Arguments))
		}
		message.Content = blocks
	case turn.Text != nil:
		message.Content = []ContentBlock{TextBlock(*turn.Text)}
	}
	return message
}

func (p *ScriptedProvider) streamFor(stepID string) StreamFunc {
	return func(ctx context.Context, model Model, request StreamRequest, options StreamOptions) (*AssistantMessage, error) {
		p.mu.Lock()
		p.requests = append(p.requests, ScriptedRequest{StepID: stepID, Request: request, Options: options})
		index := p.cursors[stepID]
		p.cursors[stepID] = index + 1
		var turn *ScriptedTurn
		if turns, ok := p.script[stepID]; ok && index < len(turns) {
			copied := turns[index]
			turn = &copied
		}
		p.mu.Unlock()

		if turn == nil {
			failed := FailedAssistantMessage(model, StopError,
				fmt.Sprintf("剧本已用完：步骤 %s 第 %d 轮没有预设响应", stepID, index+1))
			return &failed, nil
		}

		timer := time.NewTimer(0)
		if !timer.Stop() {
			<-timer.C
		}
		// 带延迟的一轮：等待期间若被中止，按 pi 约定以 aborted 结束。
		for turn != nil && turn.DelayMS != nil {
			delay := *turn.DelayMS
			if delay > 0 {
				timer.Reset(time.Duration(delay) * time.Millisecond)
				select {
				case <-ctx.Done():
					if !timer.Stop() {
						<-timer.C
					}
					aborted := FailedAssistantMessage(model, StopAborted, "请求已中止")
					return &aborted, nil
				case <-timer.C:
				}
			}
			turn = turn.Then
		}
		if turn == nil {
			failed := FailedAssistantMessage(model, StopError,
				fmt.Sprintf("剧本已用完：步骤 %s 第 %d 轮没有预设响应", stepID, index+1))
			return &failed, nil
		}
		message := p.buildMessage(*turn)
		return &message, nil
	}
}
