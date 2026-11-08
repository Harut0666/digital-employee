// 剧本式假模型：按步骤 id 依次返回 ScriptedTurn，不发任何网络请求。
// 移植自 packages/core/src/agent/scripted.ts。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
//
// ChunkSize 流式输出时每片几个字（按字符算，缺省 0 表示一次说完），方便测试逐字显示。
type ScriptedTurn struct {
	Text      *string
	ToolCalls []ScriptedToolCall
	Error     *string
	DelayMS   *int
	Then      *ScriptedTurn
	ChunkSize int
	// Chunks 流式输出时逐片给出的文字（比 ChunkSize 更细的写法）；给了就以它为准，拼起来应与 Text 相同
	Chunks []string
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
	if value, ok := raw["chunks"]; ok {
		var pieces []string
		if err := json.Unmarshal(value, &pieces); err != nil {
			return err
		}
		t.Chunks = pieces
	}
	if value, ok := raw["chunkSize"]; ok {
		var size int
		if err := json.Unmarshal(value, &size); err != nil {
			return err
		}
		t.ChunkSize = size
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

// SetScript 换一份剧本，游标从 0 开始（测试用：路径要等工作区建好才定得下来）。
func (p *ScriptedProvider) SetScript(script ModelScript) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.script = script
	p.cursors = map[string]int{}
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
		StreamEvent:   p.streamEventsFor(stepID),
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
	// 先说话再调工具的轮次：文字和工具调用同时出现在这一条消息里
	if turn.Text != nil && turn.ToolCalls != nil {
		message.StopReason = StopToolUse
		message.Content = append(message.Content, TextBlock(*turn.Text))
		for _, call := range turn.ToolCalls {
			message.Content = append(message.Content, ToolCallBlock(p.nextCallID(), call.Name, call.Arguments))
		}
		return message
	}
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

// streamEventsFor 流式版本：文字按 ChunkSize 分片逐段回调，与真实提供者的增量推送一致。
func (p *ScriptedProvider) streamEventsFor(stepID string) EventStreamFunc {
	return func(ctx context.Context, model Model, request StreamRequest, options EventStreamOptions) (*AssistantMessage, error) {
		run := p.streamFor(stepID)
		message, err := run(ctx, model, request, options.StreamOptions)

		if err != nil || message == nil || message.StopReason == StopError || message.StopReason == StopAborted {
			// 报错、中止的轮次不推文字
			return message, err
		}
		// 文字先逐片推出（工具调用轮次也常有半句话），工具调用随整条消息一起交给调用方
		text := PartialText(message)

		pieces, preset := p.chunksOf(stepID)
		if !preset {
			pieces = splitText(text, p.chunkSizeOf(stepID))
		}
		written := strings.Builder{}
		for _, piece := range pieces {
			if options.OnText != nil {

				options.OnText(piece)
			}
			written.WriteString(piece)
			// 每片之后都可能是停止：已经说出的部分留在消息里
			if ctx.Err() != nil {
				partial := NewAssistantMessage(model)
				partial.StopReason = StopAborted
				partial.Content = []ContentBlock{TextBlock(written.String())}
				return &partial, nil
			}
		}
		return message, nil
	}
}

// chunksOf 最近一轮剧本逐片给出的文字（没有写时 preset 为 false）。
func (p *ScriptedProvider) chunksOf(stepID string) ([]string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	index := p.cursors[stepID] - 1
	if turns, ok := p.script[stepID]; ok && index >= 0 && index < len(turns) && len(turns[index].Chunks) > 0 {
		return turns[index].Chunks, true
	}
	return nil, false
}

// chunkSizeOf 最近一轮剧本的分片大小。
func (p *ScriptedProvider) chunkSizeOf(stepID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	index := p.cursors[stepID] - 1
	if turns, ok := p.script[stepID]; ok && index >= 0 && index < len(turns) {
		return turns[index].ChunkSize
	}
	return 0
}

// splitText 按字符把文字切成若干片；size 不大于 0 时整段一片。
func splitText(text string, size int) []string {
	if text == "" {
		return nil
	}
	runes := []rune(text)
	if size <= 0 {
		size = len(runes)
	}
	pieces := make([]string, 0, (len(runes)+size-1)/size)
	for start := 0; start < len(runes); start += size {
		end := min(start+size, len(runes))
		pieces = append(pieces, string(runes[start:end]))
	}
	return pieces
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
