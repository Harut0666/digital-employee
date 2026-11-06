// 模型消息形状。Go 侧不再使用 pi-agent-core，但事件日志里的 model_called.response
// 必须与 TS 版（pi-ai 的 AssistantMessage）形状一致，前端与断点重放都按这个形状读。
package agent

import "time"

// Model 一个可用的模型（对应 pi-ai 的 Model）。
type Model struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	API           string `json:"api"`
	Provider      string `json:"provider"`
	BaseURL       string `json:"baseUrl,omitempty"`
	Reasoning     bool   `json:"reasoning"`
	ContextWindow int    `json:"contextWindow"`
	MaxTokens     int    `json:"maxTokens"`
}

// Cost token 成本（本地模型恒为 0，只为形状兼容）。
type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

// Usage token 用量。
type Usage struct {
	Input       int  `json:"input"`
	Output      int  `json:"output"`
	CacheRead   int  `json:"cacheRead"`
	CacheWrite  int  `json:"cacheWrite"`
	TotalTokens int  `json:"totalTokens"`
	Cost        Cost `json:"cost"`
}

// 消息内容块的类型。
const (
	ContentText       = "text"
	ContentToolCall   = "toolCall"
	ContentToolResult = "toolResult"
)

// 结束原因（与 pi-ai 一致）。
const (
	StopStop    = "stop"
	StopToolUse = "toolUse"
	StopError   = "error"
	StopAborted = "aborted"
)

// ContentBlock 一条消息内容：文本、工具调用或工具结果。
type ContentBlock struct {
	Type string `json:"type"`
	// text
	Text string `json:"text,omitempty"`
	// toolCall
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
	// toolResult
	ToolCallID string `json:"toolCallId,omitempty"`
	IsError    bool   `json:"isError,omitempty"`
}

// AssistantMessage 一次模型调用的完整产物（写进 model_called.response，断点恢复时原样重放）。
type AssistantMessage struct {
	Role         string         `json:"role"`
	Content      []ContentBlock `json:"content"`
	API          string         `json:"api"`
	Provider     string         `json:"provider"`
	Model        string         `json:"model"`
	Usage        Usage          `json:"usage"`
	StopReason   string         `json:"stopReason"`
	ErrorMessage string         `json:"errorMessage,omitempty"`
	Timestamp    int64          `json:"timestamp"`
}

// Message 对话里的一条消息（发给模型的上下文）。
type Message struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// NewAssistantMessage 一份空的助手消息（带模型信息与零用量）。
func NewAssistantMessage(model Model) AssistantMessage {
	return AssistantMessage{
		Role:       "assistant",
		Content:    []ContentBlock{},
		API:        model.API,
		Provider:   model.Provider,
		Model:      model.ID,
		StopReason: StopStop,
		Timestamp:  time.Now().UnixMilli(),
	}
}

// TextBlock 一段文本内容。
func TextBlock(text string) ContentBlock { return ContentBlock{Type: ContentText, Text: text} }

// ToolCallBlock 一次工具调用。
func ToolCallBlock(id, name string, args map[string]any) ContentBlock {
	if args == nil {
		args = map[string]any{}
	}
	return ContentBlock{Type: ContentToolCall, ID: id, Name: name, Arguments: args}
}

// ToolResultBlock 工具执行结果。
func ToolResultBlock(callID, text string, isError bool) ContentBlock {
	return ContentBlock{Type: ContentToolResult, ToolCallID: callID, Text: text, IsError: isError}
}

// DecodeAssistantMessage 把重放的 JSON（map 或字节）还原成 AssistantMessage。
func DecodeAssistantMessage(value any) (AssistantMessage, bool) {
	raw, err := marshalJSON(value)
	if err != nil {
		return AssistantMessage{}, false
	}
	var message AssistantMessage
	if err := unmarshalJSON(raw, &message); err != nil {
		return AssistantMessage{}, false
	}
	return message, true
}

// FailedAssistantMessage 以错误结束的消息（与 pi 的 failedStream 语义一致）。
func FailedAssistantMessage(model Model, reason, errorMessage string) AssistantMessage {
	message := NewAssistantMessage(model)
	message.StopReason = reason
	message.ErrorMessage = errorMessage
	return message
}
