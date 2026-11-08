// 流式输出：前台 Agent 的回复逐字推送（Spec 0006“前台 Agent 循环”）。
//
// EventStream 和 Stream 发同样的请求，区别只在取结果的方式：流式时文字增量边到边回调
// （不落盘），工具调用参数可能被服务端拆成几片，拼齐后再交给调用方；断流或取消时已输出的部分
// 仍留在返回的消息里，由调用方决定怎么写进会话日志。
package agent

import (
	"context"
	"strings"
)

// EventStreamFunc 流式调用一次模型。OnText 每收到一段文字增量回调一次；取消 ctx 即停止。
type EventStreamFunc func(ctx context.Context, model Model, request StreamRequest, options EventStreamOptions) (*AssistantMessage, error)

// EventStreamOptions 判断步骤之外的流式调用选项：底下的参数与 StreamOptions 相同，多一个文字增量回调。
type EventStreamOptions struct {
	StreamOptions
	OnText func(text string)
}

// EventStream 向提供者要一个流式调用；提供者不单独给时，退回到一次性调用（调用方只有判断步骤和旧测试，不需要逐字）。
func (r *ResolvedStepModel) EventStream(ctx context.Context, model Model, request StreamRequest, options EventStreamOptions) (*AssistantMessage, error) {
	if r.StreamEvent != nil {
		return r.StreamEvent(ctx, model, request, options)
	}
	return r.Stream(ctx, model, request, options.StreamOptions)
}

// PartialText 消息里已经输出的全部文字。
func PartialText(message *AssistantMessage) string {
	if message == nil {
		return ""
	}
	text := strings.Builder{}
	for _, block := range message.Content {
		if block.Type == ContentText {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}

// InterruptedSuffix 停止时在已输出的部分后面补的一句。
const InterruptedSuffix = "（已中断）"

// StreamErrorHint 断流时说明断在哪里；正常结束或取消（aborted）时为空。
func StreamErrorHint(message *AssistantMessage) string {
	if message == nil || message.StopReason != StopError {
		return ""
	}
	hint := "回复中途断开"
	if PartialText(message) != "" {
		hint += "，已经输出的部分已保留"
	}
	return hint + "：" + message.ErrorMessage
}
