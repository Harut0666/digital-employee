// 真实模型提供者：用 OpenAI 兼容的 /chat/completions 接口接 DeepSeek 等模型。
// 连接的 baseUrl、apiKey 来自本机设置文件（ModelSettings），不进仓库。
// 另提供“测试连接”：发一条极短请求，只报告可用性、延迟与错误（错误里不包含 API Key）。
//
// 移植自 packages/core/src/agent/real.ts（pi-ai 的 streamSimple 换成自带的最小 HTTP 客户端）。
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"digital-employee/internal/contract"
)

// 缺省上下文窗口 / 输出上限：没配高级参数时行为与 T20 之前一致。
const (
	DefaultContextWindow = 128_000
	DefaultMaxTokens     = 8_192
)

const (
	probeTimeoutMS = 15_000
	probeMaxTokens = 8
)

// declaresReasoning 这个模型是否声明了推理能力：配了默认档位或可用档位列表即视为支持。
func declaresReasoning(options *contract.ModelOptions) bool {
	if options == nil {
		return false
	}
	return options.DefaultReasoningEffort != nil || len(options.ReasoningEfforts) > 0
}

// RedactAPIKey 把错误信息里可能出现的 API Key 换成 ***，密钥永远不进响应、日志或错误信息。
func RedactAPIKey(text, apiKey string) string {
	if apiKey == "" {
		return text
	}
	return strings.ReplaceAll(text, apiKey, "***")
}

// RealProvider 真实模型提供者。
type RealProvider struct {
	settings contract.ModelSettings
}

// NewModelProvider 按设置创建真实模型提供者。
func NewModelProvider(settings contract.ModelSettings) *RealProvider {
	return &RealProvider{settings: settings}
}

// Kind 提供者类型。
func (p *RealProvider) Kind() string { return "real" }

// Resolve 取某个步骤的模型与 stream 函数。
func (p *RealProvider) Resolve(_ string, ref *contract.ModelRef) (*ResolvedStepModel, error) {
	connectionName := "default"
	modelID := "default"
	if ref != nil {
		if ref.Connection != "" {
			connectionName = ref.Connection
		}
		if ref.Model != "" {
			modelID = ref.Model
		}
	}
	if connectionName == "default" {
		connectionName = p.settings.DefaultConnection
	}
	var connection *contract.ModelConnection
	for index := range p.settings.Connections {
		if p.settings.Connections[index].Name == connectionName {
			connection = &p.settings.Connections[index]
			break
		}
	}
	if connection == nil {
		return nil, fmt.Errorf("未找到模型连接“%s”", connectionName)
	}
	if modelID == "default" {
		modelID = p.settings.DefaultModel
	}
	var options *contract.ModelOptions
	if connection.ModelOptions != nil {
		if value, ok := connection.ModelOptions[modelID]; ok {
			copied := value
			options = &copied
		}
	}

	// 档位：步骤 thinking → 连接上的模型默认档位 → off（未知值按 off）
	thinkingRef := (*string)(nil)
	if ref != nil {
		thinkingRef = ref.Thinking
	}
	if thinkingRef == nil && options != nil {
		thinkingRef = options.DefaultReasoningEffort
	}
	thinkingLevel := NormalizeThinkingLevel(thinkingRef)
	reasoning := thinkingLevel != contract.ThinkingOff || declaresReasoning(options)

	model := contractModelToAgent(*connection, modelID, reasoning, options)
	connectionCopy := *connection
	return &ResolvedStepModel{
		Model: model,
		Stream: func(ctx context.Context, model Model, request StreamRequest, streamOptions StreamOptions) (*AssistantMessage, error) {
			return streamOpenAICompatible(ctx, model, request, streamOptions, thinkingLevel)
		},
		APIKey:        connectionCopy.APIKey,
		ThinkingLevel: thinkingLevel,
		TimeoutMS:     connection.TimeoutMS,
		MaxRetries:    connection.MaxRetries,
	}, nil
}

func contractModelToAgent(connection contract.ModelConnection, modelID string, reasoning bool, options *contract.ModelOptions) Model {
	contextWindow := DefaultContextWindow
	maxTokens := DefaultMaxTokens
	if options != nil {
		if options.ContextWindow != nil {
			contextWindow = *options.ContextWindow
		}
		if options.MaxTokens != nil {
			maxTokens = *options.MaxTokens
		}
	}
	return Model{
		ID: modelID, Name: modelID, API: "openai-completions", Provider: connection.Name,
		BaseURL: connection.BaseURL, Reasoning: reasoning,
		ContextWindow: contextWindow, MaxTokens: maxTokens,
	}
}

// ---------------------------------------------------------------- HTTP 客户端

type openAIChatRequest struct {
	Model string `json:"model"`
	// Stream 恒为 false：Go 版一次调用取完整响应，SSE 只用于把事件推给前端
	Stream    bool             `json:"stream"`
	Messages  []map[string]any `json:"messages"`
	Tools     []map[string]any `json:"tools,omitempty"`
	MaxTokens int              `json:"max_tokens,omitempty"`
	// ReasoningEffort 思考强度（OpenAI 兼容接口的通用写法）
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		TotalTokens         int `json:"total_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func streamOpenAICompatible(ctx context.Context, model Model, request StreamRequest, options StreamOptions, thinkingLevel string) (*AssistantMessage, error) {
	body := openAIChatRequest{
		Model:     model.ID,
		Stream:    false,
		MaxTokens: model.MaxTokens,
	}
	if options.MaxTokens > 0 {
		body.MaxTokens = options.MaxTokens
	}
	for _, message := range request.Messages {
		body.Messages = append(body.Messages, convertMessage(message))
	}
	for _, tool := range request.Tools {
		body.Tools = append(body.Tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.Parameters,
			},
		})
	}
	if thinkingLevel != "" && thinkingLevel != contract.ThinkingOff {
		body.ReasoningEffort = thinkingLevel
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	timeout := time.Duration(BuiltinStepTimeoutMS) * time.Millisecond
	if options.TimeoutMS != nil && *options.TimeoutMS > 0 {
		timeout = time.Duration(*options.TimeoutMS) * time.Millisecond
	}
	attempts := 1
	if options.MaxRetries > 0 {
		attempts += options.MaxRetries
	}

	var lastError error
	for attempt := 1; attempt <= attempts; attempt++ {
		response, err := postChatCompletion(ctx, model, raw, options.APIKey, timeout)
		if err != nil {
			lastError = err
			if ctx.Err() != nil {
				break
			}
			continue
		}
		message := responseToAssistantMessage(model, response)
		return &message, nil
	}
	if lastError == nil {
		lastError = fmt.Errorf("模型调用失败")
	}
	return nil, lastError
}

func postChatCompletion(ctx context.Context, model Model, body []byte, apiKey string, timeout time.Duration) (*openAIChatResponse, error) {
	callCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	url := strings.TrimRight(model.BaseURL, "/") + "/chat/completions"
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("模型接口返回 %d：%s", response.StatusCode, truncate(string(raw), 500))
	}
	var parsed openAIChatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("模型响应不是合法的 JSON：%v", err)
	}
	if parsed.Error.Message != "" {
		return nil, fmt.Errorf("模型接口报错：%s", parsed.Error.Message)
	}
	return &parsed, nil
}

func convertMessage(message Message) map[string]any {
	if message.Role == "assistant" {
		toolCalls := []map[string]any{}
		text := strings.Builder{}
		for _, block := range message.Content {
			switch block.Type {
			case ContentText:
				text.WriteString(block.Text)
			case ContentToolCall:
				arguments, _ := json.Marshal(block.Arguments)
				toolCalls = append(toolCalls, map[string]any{
					"id":   block.ID,
					"type": "function",
					"function": map[string]any{
						"name":      block.Name,
						"arguments": string(arguments),
					},
				})
			}
		}
		out := map[string]any{"role": "assistant", "content": text.String()}
		if len(toolCalls) > 0 {
			out["tool_calls"] = toolCalls
		}
		return out
	}
	if message.Role == "tool" {
		for _, block := range message.Content {
			if block.Type == ContentToolResult {
				return map[string]any{
					"role":         "tool",
					"tool_call_id": block.ToolCallID,
					"content":      block.Text,
				}
			}
		}
	}
	text := strings.Builder{}
	for _, block := range message.Content {
		if block.Type == ContentText {
			text.WriteString(block.Text)
		}
	}
	return map[string]any{"role": message.Role, "content": text.String()}
}

func responseToAssistantMessage(model Model, response *openAIChatResponse) AssistantMessage {
	message := NewAssistantMessage(model)
	message.Usage = EmptyUsage()
	message.Usage.Input = response.Usage.PromptTokens
	message.Usage.Output = response.Usage.CompletionTokens
	message.Usage.TotalTokens = response.Usage.TotalTokens
	if message.Usage.TotalTokens == 0 {
		message.Usage.TotalTokens = message.Usage.Input + message.Usage.Output
	}
	message.Usage.CacheRead = response.Usage.PromptTokensDetails.CachedTokens
	if len(response.Choices) == 0 {
		message.StopReason = StopError
		message.ErrorMessage = "模型没有返回任何结果"
		return message
	}
	choice := response.Choices[0]
	blocks := []ContentBlock{}
	if strings.TrimSpace(choice.Message.Content) != "" {
		blocks = append(blocks, TextBlock(choice.Message.Content))
	}
	for _, call := range choice.Message.ToolCalls {
		arguments := map[string]any{}
		if strings.TrimSpace(call.Function.Arguments) != "" {
			var parsed map[string]any
			if err := json.Unmarshal([]byte(call.Function.Arguments), &parsed); err == nil {
				arguments = parsed
			} else {
				arguments = map[string]any{"_raw": call.Function.Arguments}
			}
		}
		blocks = append(blocks, ToolCallBlock(call.ID, call.Function.Name, arguments))
	}
	message.Content = blocks
	if len(choice.Message.ToolCalls) > 0 || choice.FinishReason == "tool_calls" {
		message.StopReason = StopToolUse
	} else {
		message.StopReason = StopStop
	}
	return message
}

// TestModelConnection 测试连接：用该连接发一条极短的对话请求（最多 8 个 token），
// 返回是否可用与延迟。不做重试：测试连接要的是“现在能不能用”，重试会掩盖问题。
func TestModelConnection(connection contract.ModelConnection, modelID string, timeoutMS int) contract.ModelConnectionTest {
	started := time.Now()
	if modelID == "" {
		if len(connection.Models) > 0 {
			modelID = connection.Models[0]
		}
	}
	if modelID == "" {
		return contract.ModelConnectionTest{OK: false, LatencyMS: 0, Model: modelID, Error: "该连接没有配置模型"}
	}
	options := (*contract.ModelOptions)(nil)
	if connection.ModelOptions != nil {
		if value, ok := connection.ModelOptions[modelID]; ok {
			options = &value
		}
	}
	model := contractModelToAgent(connection, modelID, declaresReasoning(options), options)
	if timeoutMS <= 0 {
		timeoutMS = probeTimeoutMS
	}
	context, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	request := StreamRequest{Messages: []Message{{
		Role:    "user",
		Content: []ContentBlock{TextBlock("请只回复 OK")},
	}}}
	message, err := streamOpenAICompatible(context, model, request, StreamOptions{
		APIKey: connection.APIKey, MaxTokens: probeMaxTokens, MaxRetries: 0, TimeoutMS: &timeoutMS,
	}, NormalizeThinkingLevel(nil))
	latency := time.Since(started).Milliseconds()
	if err != nil {
		return contract.ModelConnectionTest{OK: false, LatencyMS: latency, Model: modelID, Error: RedactAPIKey(err.Error(), connection.APIKey)}
	}
	if message.StopReason == StopError || message.StopReason == StopAborted {
		return contract.ModelConnectionTest{OK: false, LatencyMS: latency, Model: modelID, Error: RedactAPIKey(message.ErrorMessage, connection.APIKey)}
	}
	return contract.ModelConnectionTest{OK: true, LatencyMS: latency, Model: modelID}
}

func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
