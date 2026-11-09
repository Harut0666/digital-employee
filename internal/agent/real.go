// 真实模型提供者：用 OpenAI 兼容的 /chat/completions 接口接 DeepSeek 等模型。
// 连接的 baseUrl、apiKey 来自本机设置文件（ModelSettings），不进仓库。
// 另提供“测试连接”：发一条极短请求，只报告可用性、延迟与错误（错误里不包含 API Key）。
//
// 移植自 packages/core/src/agent/real.ts（pi-ai 的 streamSimple 换成自带的最小 HTTP 客户端）。
package agent

import (
	"bufio"
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

// Resolve 取某个步骤的模型与 stream 函数。ref 里的 connection / model 为 "default" 或空时按缺省处理：
// 两者都缺省时用设置的缺省模型（会话模型，Spec 0006）；
// 只写了一半时补另一半——model 为空取这个连接列出的第一个模型，connection 为空取缺省连接的连接。
func (p *RealProvider) Resolve(_ string, ref *contract.ModelRef) (*ResolvedStepModel, error) {
	connectionName := p.settings.Default.Connection
	modelID := p.settings.Default.Model
	pinnedConnection, pinnedModel := "", ""
	if ref != nil {
		if ref.Connection != "" && ref.Connection != "default" {
			pinnedConnection = ref.Connection
		}
		if ref.Model != "" && ref.Model != "default" {
			pinnedModel = ref.Model
		}
	}
	switch {
	case pinnedConnection != "":
		connectionName = pinnedConnection
	case pinnedModel != "":
		connectionName = p.settings.Default.Connection
	}
	if connectionName == "" {
		return nil, fmt.Errorf("还没有可用的模型：请到设置里填写模型的 Key")
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
	switch {
	case pinnedModel != "":
		modelID = pinnedModel
	case pinnedConnection != "" && len(connection.Models) > 0:
		modelID = connection.Models[0]
	}
	if modelID == "" {
		return nil, fmt.Errorf("模型连接“%s”还没有可用的模型", connectionName)
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
		Model:      model,
		Connection: connection.Name,
		ModelID:    modelID,
		Stream: func(ctx context.Context, model Model, request StreamRequest, streamOptions StreamOptions) (*AssistantMessage, error) {
			return streamOpenAICompatible(ctx, model, request, streamOptions, thinkingLevel)
		},
		StreamEvent: func(ctx context.Context, model Model, request StreamRequest, streamOptions EventStreamOptions) (*AssistantMessage, error) {
			return streamOpenAICompatibleEvents(ctx, model, request, streamOptions, thinkingLevel)
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
	// Stream 判断步骤为 false（一次取完整响应），前台 Agent 为 true（逐字推送）
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

// openAIBody 组装请求体；stream 决定用一次性响应还是 SSE。
func openAIBody(model Model, request StreamRequest, options StreamOptions, thinkingLevel string, stream bool) ([]byte, error) {
	body := openAIChatRequest{
		Model:     model.ID,
		Stream:    stream,
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
	return json.Marshal(body)
}

// callTimeout 一次调用的超时：选项里的优先，否则步骤内置值。
func callTimeout(options StreamOptions) time.Duration {
	timeout := time.Duration(BuiltinStepTimeoutMS) * time.Millisecond
	if options.TimeoutMS != nil && *options.TimeoutMS > 0 {
		timeout = time.Duration(*options.TimeoutMS) * time.Millisecond
	}
	return timeout
}

// attemptsOf 一次调用最多试几次（1 + 重试次数）。
func attemptsOf(options StreamOptions) int {
	if options.MaxRetries > 0 {
		return 1 + options.MaxRetries
	}
	return 1
}

func streamOpenAICompatible(ctx context.Context, model Model, request StreamRequest, options StreamOptions, thinkingLevel string) (*AssistantMessage, error) {
	raw, err := openAIBody(model, request, options, thinkingLevel, false)
	if err != nil {
		return nil, err
	}
	timeout := callTimeout(options)
	var lastError error
	for attempt := 1; attempt <= attemptsOf(options); attempt++ {
		response, err := postChatCompletion(ctx, model, raw, options.APIKey, timeout)
		if err != nil {
			lastError = err
			if ctx.Err() != nil || !retriesCanHelp(err) {
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
		return nil, newModelCallError(contract.ModelErrorNetwork, apiKey, "%v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{}
	response, err := client.Do(request)
	if err != nil {
		return nil, newModelCallError(contract.ModelErrorNetwork, apiKey, "%v", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, newModelCallError(contract.ModelErrorNetwork, apiKey, "%v", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, newModelCallError(modelErrorKindOf(response.StatusCode), apiKey,
			"模型接口返回 %d：%s", response.StatusCode, truncate(string(raw), 500))
	}
	var parsed openAIChatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, newModelCallError(contract.ModelErrorServer, apiKey, "模型响应不是合法的 JSON：%v", err)
	}
	if parsed.Error.Message != "" {
		return nil, newModelCallError(contract.ModelErrorServer, apiKey, "模型接口报错：%s", parsed.Error.Message)
	}
	return &parsed, nil
}

// openAIStreamAnswer 把一次性返回的完整回答拆成“流”：响应体是整段 JSON（服务不支持流式，或回了错误体）。
// 按增量逐段交出文字，调用方看到的效果和真流式一样。
type openAIStreamAnswer struct {
	answer openAIChatResponse
	raw    []byte
}

// streamAsEvents 逐段交出文字以后，按整条消息收尾。
func (a *openAIStreamAnswer) streamAsEvents(model Model, onText func(string)) *AssistantMessage {
	message := responseToAssistantMessage(model, &a.answer)
	for _, block := range message.Content {
		if block.Type == ContentText && block.Text != "" && onText != nil {
			onText(block.Text)
		}
	}
	return &message
}

// answerFromJSONBody 处理“请求了流式、200 却回了一段 JSON”：不支持流式的服务会直接给完整响应，
// 有的服务在 200 里回错误体，还有的直接给逐行 JSON 帧。三条路都走通，调用方看到的仍是同一条消息。
func answerFromJSONBody(model Model, raw []byte, apiKey string, onText func(string)) (*AssistantMessage, error) {
	var probe struct {
		Choices []map[string]json.RawMessage `json:"choices"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	whole := json.Unmarshal(raw, &probe) == nil
	if whole {
		if probe.Error.Message != "" {
			return nil, newModelCallError(contract.ModelErrorServer, apiKey, "模型接口报错：%s", probe.Error.Message)
		}
		for _, choice := range probe.Choices {
			if _, ok := choice["message"]; ok {
				answer := openAIStreamAnswer{}
				if err := json.Unmarshal(raw, &answer.answer); err != nil {
					return nil, newModelCallError(contract.ModelErrorServer, apiKey, "模型响应不是合法的 JSON：%v", err)
				}
				answer.raw = raw
				return answer.streamAsEvents(model, onText), nil
			}
		}
	}
	// 逐行 JSON 帧（有的服务不写 data: 前缀）：收齐以后按一条完整的回答交回
	parser := newSSEParser(model, onText)
	for _, line := range strings.Split(string(raw), "\n") {
		frame := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "data:"))
		if frame == "" {
			continue
		}
		if parser.feed(frame) {
			break
		}
	}
	return parser.done(model), nil
}

// streamOpenAICompatibleEvents 前台 Agent 的流式调用：文字增量边到边回调，工具调用参数拼齐。
//
// 只在还没有任何输出时才重试（连不上、接口拒绝请求）：已经开始输出之后重试会让人看到两遍开头。
// 中途断流不算 Go 错误，按“以 error 结束、保留已输出部分”返回，由调用方写进会话日志。
func streamOpenAICompatibleEvents(ctx context.Context, model Model, request StreamRequest, options EventStreamOptions, thinkingLevel string) (*AssistantMessage, error) {
	raw, err := openAIBody(model, request, options.StreamOptions, thinkingLevel, true)
	if err != nil {
		return nil, err
	}
	timeout := callTimeout(options.StreamOptions)
	var lastError error
	for attempt := 1; attempt <= attemptsOf(options.StreamOptions); attempt++ {
		message, err := postChatCompletionStream(ctx, model, raw, options, timeout)
		if err == nil {
			return message, nil
		}
		lastError = err
		if ctx.Err() != nil || !retriesCanHelp(err) {
			break
		}
	}
	if lastError == nil {
		lastError = fmt.Errorf("模型调用失败")
	}
	return nil, lastError
}

// postSSE 发请求并返回 SSE 响应；状态码不对时返回错误（由调用方决定重试）。
// 正文开头的几行先看一眼：有的服务在 200 里回一个错误体，或者整段回答（不支持流式）。
// 两种情况都从看过的地方接着读，正文一个字节不丢。
func postSSE(ctx context.Context, model Model, body []byte, apiKey string, timeout time.Duration, onOpen func()) (*http.Response, context.CancelFunc, error) {
	callCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	url := strings.TrimRight(model.BaseURL, "/") + "/chat/completions"
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, nil, newModelCallError(contract.ModelErrorNetwork, apiKey, "%v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	response, err := (&http.Client{}).Do(request)
	if err != nil {
		cancel()
		return nil, nil, newModelCallError(contract.ModelErrorNetwork, apiKey, "%v", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		response.Body.Close()
		cancel()
		return nil, nil, newModelCallError(modelErrorKindOf(response.StatusCode), apiKey,
			"模型接口返回 %d：%s", response.StatusCode, truncate(string(raw), 500))
	}
	response.Body = &bufferedBody{Reader: bufio.NewReader(response.Body), closer: response.Body}
	if onOpen != nil {
		onOpen()
	}
	return response, cancel, nil
}

// bufferedBody 正文前面多包一层缓冲：看过开头的几行之后还能接着读。
type bufferedBody struct {
	*bufio.Reader
	closer io.Closer
}

func (b *bufferedBody) Close() error { return b.closer.Close() }
// SSE 分片里出现的字段（只取用得到的部分）。
type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				// Index 并行工具调用的序号；有的服务不带，按出现顺序追加
				Index    *int   `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *openAIStreamChunkUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// streamCall 一个正在拼的工具调用。
type streamCall struct {
	index int
	id    string
	name  string
	args  strings.Builder
}

// sseParser 把 SSE 的 data 帧拼成一条完整的助手消息。
type sseParser struct {
	message   AssistantMessage
	text      strings.Builder
	calls     []*streamCall
	finish    string
	usage     *openAIStreamChunkUsage
	onText    func(string)
	lastError string
}

// openAIStreamChunkUsage 流式分片里的用量。
type openAIStreamChunkUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func newSSEParser(model Model, onText func(string)) *sseParser {
	return &sseParser{message: NewAssistantMessage(model), onText: onText}
}

// feed 处理一帧；返回 true 表示收到了 [DONE]。
func (p *sseParser) feed(data string) bool {
	data = strings.TrimSpace(data)
	if data == "" {
		return false
	}
	if data == "[DONE]" {
		return true
	}
	var chunk openAIStreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		// 不认识的分片不影响已经收到的内容
		return false
	}
	if chunk.Error != nil && chunk.Error.Message != "" {
		p.lastError = chunk.Error.Message
	}
	if chunk.Usage != nil {
		p.usage = chunk.Usage
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			p.text.WriteString(choice.Delta.Content)
			if p.onText != nil {
				p.onText(choice.Delta.Content)
			}
		}
		for _, fragment := range choice.Delta.ToolCalls {
			p.call(fragment.Index, fragment.ID, fragment.Function.Name, fragment.Function.Arguments)
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			p.finish = *choice.FinishReason
		}
	}
	return false
}

// call 收下一段工具调用：同名同序号的后续分片追加参数。
func (p *sseParser) call(index *int, id, name, arguments string) {
	var target *streamCall
	if index != nil {
		for _, existing := range p.calls {
			if existing.index == *index {
				target = existing
			}
		}
	}
	if target == nil && index == nil && len(p.calls) > 0 {
		// 不带序号的增量：属于上一个调用
		last := p.calls[len(p.calls)-1]
		if last.id == id || id == "" {
			target = last
		}
	}
	if target == nil {
		next := 0
		if index != nil {
			next = *index
		} else if len(p.calls) > 0 {
			next = p.calls[len(p.calls)-1].index + 1
		}
		target = &streamCall{index: next}
		p.calls = append(p.calls, target)
	}
	if id != "" {
		target.id = id
	}
	if name != "" {
		target.name = name
	}
	target.args.WriteString(arguments)
}

// done 拼成完整的助手消息：文字在前，工具调用在后（与一次性响应一致）。
func (p *sseParser) done(model Model) *AssistantMessage {
	message := p.message
	message.Model = model.ID
	blocks := make([]ContentBlock, 0, len(p.calls)+1)
	if p.text.Len() > 0 {
		blocks = append(blocks, TextBlock(p.text.String()))
	}
	toolCalls := 0
	for _, call := range p.calls {
		if call.name == "" {
			continue
		}
		toolCalls++
		blocks = append(blocks, ToolCallBlock(call.id, call.name, parseArguments(call.args.String())))
	}
	message.Content = blocks
	if p.usage != nil {
		message.Usage.Input = p.usage.PromptTokens
		message.Usage.Output = p.usage.CompletionTokens
		message.Usage.TotalTokens = p.usage.TotalTokens
		if message.Usage.TotalTokens == 0 {
			message.Usage.TotalTokens = message.Usage.Input + message.Usage.Output
		}
		message.Usage.CacheRead = p.usage.PromptTokensDetails.CachedTokens
	}
	switch {
	case toolCalls > 0 || p.finish == "tool_calls":
		message.StopReason = StopToolUse
	default:
		message.StopReason = StopStop
	}
	return &message
}

// parseArguments 工具参数是整段 JSON 时解析成对象；拆坏的按原文保留，交给模型自己纠正。
func parseArguments(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
		return parsed
	}
	return map[string]any{"_raw": raw}
}

// postChatCompletionStream 发一次流式请求并读完事件流。
// 已开始输出之后不再重试：断流、取消都以消息（而不是 error）结束。
func postChatCompletionStream(ctx context.Context, model Model, body []byte, options EventStreamOptions, timeout time.Duration) (*AssistantMessage, error) {
	response, cancel, err := postSSE(ctx, model, body, options.APIKey, timeout, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		response.Body.Close()
		cancel()
	}()

	reader := bufio.NewReaderSize(response.Body, 64<<10)
	head := strings.TrimLeft(string(mustPeek(reader, 512)), " \r\n\t")
	// 正文开头不是 data: 行也不是 JSON：有的服务回的是 200 里的错误体
	if !strings.HasPrefix(head, "data:") && !strings.HasPrefix(head, "{") {
		raw, readErr := io.ReadAll(io.LimitReader(reader, 8<<20))
		if readErr != nil {
			return nil, newModelCallError(contract.ModelErrorNetwork, options.APIKey, "%v", readErr)
		}
		return nil, newModelCallError(contract.ModelErrorServer, options.APIKey, "模型接口没有返回事件流：%s", truncate(string(raw), 500))
	}
	// 正文是一段完整的 JSON：服务不支持流式（请求里的 stream 被忽略），或者 200 里回了错误体。
	// 整段读完再判断，不要当成事件流逐帧喂。
	if strings.HasPrefix(head, "{") {
		raw, readErr := io.ReadAll(io.LimitReader(reader, 8<<20))
		if readErr != nil {
			return nil, newModelCallError(contract.ModelErrorNetwork, options.APIKey, "%v", readErr)
		}
		return answerFromJSONBody(model, raw, options.APIKey, options.OnText)
	}
	// 标准事件流：一行一个 data: 帧

	parser := newSSEParser(model, options.OnText)
	for {
		line, readErr := reader.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		// 标准的事件流每帧一行 data:；有的服务直接给 JSON 行，也认
		frame := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "data:"))
		if (strings.HasPrefix(strings.TrimSpace(line), "data:") || strings.HasPrefix(frame, "{")) && parser.feed(frame) {
			return parser.done(model), nil
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return interruptedMessage(model, parser), nil
			}
			// 没收到 [DONE] 就断了：已经说出口的不作废，但这一轮算没说完
			message := interruptedMessage(model, parser)
			message.StopReason = StopError
			message.ErrorMessage = "连接中途断开"
			if parser.lastError != "" {
				message.ErrorMessage = parser.lastError
			}
			return message, nil
		}
	}
}
// mustPeek 看一眼正文开头（有多少算多少，不看满也不当错误）：正文太短、读不到都照常往下读。
func mustPeek(reader *bufio.Reader, size int) []byte {
	peeked, _ := reader.Peek(size)
	return peeked
}

// interruptedMessage 取消或断流时返回：已输出的部分原样保留，以 aborted 结束。
func interruptedMessage(model Model, parser *sseParser) *AssistantMessage {
	message := parser.done(model)
	message.StopReason = StopAborted
	return message
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
