package agent_test

// 真实模型提供者的流式解析（OpenAI 兼容接口的 SSE）：文字增量、工具调用参数分片、
// 空 delta、[DONE]、中途断流。假服务按 SSE 分片输出，不联网。

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

// sseService 假的 OpenAI 兼容流式服务：按顺序把 frames 写出去，记下收到的请求体。
type sseService struct {
	server *httptest.Server
	mu     sync.Mutex
	bodies []string
}

func newSSEService(t *testing.T, handler func(w http.ResponseWriter, body string)) *sseService {
	t.Helper()
	service := &sseService{}
	service.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		service.mu.Lock()
		service.bodies = append(service.bodies, string(body))
		service.mu.Unlock()
		handler(w, string(body))
	}))
	t.Cleanup(service.server.Close)
	return service
}

// writeFrames 按 SSE 格式逐帧写出并冲刷。
func writeFrames(w http.ResponseWriter, frames ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, frame := range frames {
		_, _ = io.WriteString(w, "data: "+frame+"\n\n")
		w.(http.Flusher).Flush()
	}
}

// streamProvider 接假服务的真实模型提供者，解析前台 Agent 的模型。
func streamProvider(t *testing.T, url string) *agent.ResolvedStepModel {
	t.Helper()
	retries := 0
	provider := agent.NewModelProvider(contract.ModelSettings{
		Connections: []contract.ModelConnection{{
			Name: "fake", BaseURL: url, APIKey: "test-key", Models: []string{"m"}, MaxRetries: &retries,
		}},
		Default: contract.ModelRef{Connection: "fake", Model: "m"},
	})
	resolved, err := provider.Resolve(agent.FrontdeskStepID, nil)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func toolSpecs() []agent.ToolSpec {
	return []agent.ToolSpec{{
		Name: "query_task", Description: "查询任务",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
	}}
}

func TestRealStreamConcatenatesTextDeltas(t *testing.T) {
	service := newSSEService(t, func(w http.ResponseWriter, _ string) {
		writeFrames(w,
			`{"choices":[{"delta":{"role":"assistant","content":""}}]}`,
			`{"choices":[{"delta":{"content":"任务"}}]}`,
			// 空 delta（有些服务会推一条只有 role 或什么都没带的帧）
			`{"choices":[{"delta":{}}]}`,
			`{"choices":[{"delta":{"content":"在"}}]}`,
			// 内容为空串的帧也不应生成空的内容块
			`{"choices":[{"delta":{"content":""}}]}`,
			`{"choices":[{"delta":{"content":"等材料。"},"finish_reason":null}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`[DONE]`,
		)
	})
	resolved := streamProvider(t, service.server.URL)

	var deltas []string
	message, err := resolved.EventStream(context.Background(), resolved.Model, agent.StreamRequest{}, agent.EventStreamOptions{
		APIKey: resolved.APIKey, OnText: func(text string) { deltas = append(deltas, text) },
	})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Join(deltas, "|") != "任务|在|等材料。" {
		t.Fatalf("文字增量 = %q", deltas)
	}
	if message.StopReason != agent.StopStop || len(message.Content) != 1 || message.Content[0].Text != "任务在等材料。" {
		t.Fatalf("完整消息 = %+v", message)
	}
	if !strings.Contains(service.bodies[0], `"stream":true`) {
		t.Fatalf("流式请求应带 stream:true：%s", service.bodies[0])
	}
}

func TestRealStreamJoinsFragmentedToolArguments(t *testing.T) {
	service := newSSEService(t, func(w http.ResponseWriter, _ string) {
		writeFrames(w,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"query_task","arguments":""}}]}}]}`,
			// 参数按字符分片，中间的 JSON 不是合法片段
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"task"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"Id\":\"t-1\"}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`[DONE]`,
		)
	})
	resolved := streamProvider(t, service.server.URL)

	message, err := resolved.EventStream(context.Background(), resolved.Model,
		agent.StreamRequest{Tools: toolSpecs()}, agent.EventStreamOptions{APIKey: resolved.APIKey})
	if err != nil {
		t.Fatal(err)
	}

	if message.StopReason != agent.StopToolUse || len(message.Content) != 1 {
		t.Fatalf("完整消息 = %+v", message)
	}
	call := message.Content[0]
	if call.Type != agent.ContentToolCall || call.ID != "call_1" || call.Name != "query_task" || call.Arguments["taskId"] != "t-1" {
		t.Fatalf("工具调用 = %+v", call)
	}
}

func TestRealStreamKeepsEveryParallelToolCall(t *testing.T) {
	service := newSSEService(t, func(w http.ResponseWriter, _ string) {
		writeFrames(w,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"query_task","arguments":"{}"}},{"index":1,"id":"c2","type":"function","function":{"name":"propose_card","arguments":""}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"type\":\"pause\"}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`[DONE]`,
		)
	})
	resolved := streamProvider(t, service.server.URL)

	message, err := resolved.EventStream(context.Background(), resolved.Model, agent.StreamRequest{}, agent.EventStreamOptions{APIKey: resolved.APIKey})
	if err != nil {
		t.Fatal(err)
	}

	if len(message.Content) != 2 {
		t.Fatalf("内容块 = %+v", message.Content)
	}
	if message.Content[0].ID != "c1" || message.Content[1].ID != "c2" || message.Content[1].Arguments["type"] != "pause" {
		t.Fatalf("两个工具调用 = %+v", message.Content)
	}
}

// 中途断流：已经输出的部分保留，跳过的帧不影响它，消息以 error 结束，hint 说明断在哪里。
func TestRealStreamBrokenMidwayKeepsPartialText(t *testing.T) {
	service := newSSEService(t, func(w http.ResponseWriter, _ string) {
		writeFrames(w,
			`{"choices":[{"delta":{"content":"前一半"}}]}`,
			`{"choices":[{"delta":{"content":"后一半"}}]}`,
		)
	})
	resolved := streamProvider(t, service.server.URL)

	var deltas []string
	message, err := resolved.EventStream(context.Background(), resolved.Model, agent.StreamRequest{}, agent.EventStreamOptions{
		APIKey: resolved.APIKey, OnText: func(text string) { deltas = append(deltas, text) },
	})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Join(deltas, "") != "前一半后一半" {
		t.Fatalf("断流前已推送的增量 = %q", deltas)
	}
	if message.StopReason != agent.StopError || !strings.Contains(message.ErrorMessage, "中途断开") {
		t.Fatalf("断流应以 error 结束：%+v", message)
	}
	partial := agent.PartialText(message)
	if partial != "前一半后一半" {
		t.Fatalf("已输出的部分 = %q", partial)
	}
	if hint := agent.StreamErrorHint(message); !strings.Contains(hint, "中途断开") || !strings.Contains(hint, "已经输出的部分已保留") {
		t.Fatalf("断流说明 = %q", hint)
	}
	if agent.StreamErrorHint(&agent.AssistantMessage{StopReason: agent.StopStop}) != "" {
		t.Fatal("正常结束的消息不该有断流说明")
	}
}

// 客户端停止：上下文取消同样保留已输出的部分，hint 为空（不是出错）。
func TestRealStreamCancelKeepsPartialText(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	service := newSSEService(t, func(w http.ResponseWriter, _ string) {
		writeFrames(w, `{"choices":[{"delta":{"content":"说到一半"}}]}`)
		w.(http.Flusher).Flush()
		// 之后一直不再输出，直到测试取消
		<-ctx.Done()
	})
	resolved := streamProvider(t, service.server.URL)

	message, err := resolved.EventStream(ctx, resolved.Model, agent.StreamRequest{}, agent.EventStreamOptions{
		APIKey: resolved.APIKey,
		OnText: func(text string) {
			if text == "说到一半" {
				cancel()
			}
		},
	})
	cancel()
	if err != nil {
		t.Fatal(err)
	}

	if message.StopReason != agent.StopAborted || agent.PartialText(message) != "说到一半" {
		t.Fatalf("取消后的消息 = %+v", message)
	}
	if agent.StreamErrorHint(message) != "" {
		t.Fatalf("取消不是出错，不该有断流说明：%q", agent.StreamErrorHint(message))
	}
}

func TestRealStreamRejectsErrorStatusBeforeStreaming(t *testing.T) {
	service := newSSEService(t, func(w http.ResponseWriter, _ string) {
		http.Error(w, "invalid key test-key", http.StatusUnauthorized)
	})
	resolved := streamProvider(t, service.server.URL)

	_, err := resolved.EventStream(context.Background(), resolved.Model, agent.StreamRequest{}, agent.EventStreamOptions{APIKey: resolved.APIKey})
	if err == nil || strings.Contains(err.Error(), "test-key") {
		t.Fatalf("认证失败应报错且抹掉 Key：%v", err)
	}
}

// 判断步骤仍走非流式：请求体里 stream 为 false。
func TestJudgmentStepsStayNonStreaming(t *testing.T) {
	service := newSSEService(t, func(w http.ResponseWriter, _ string) {
		_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`)
	})
	resolved := streamProvider(t, service.server.URL)

	message, err := resolved.Stream(context.Background(), resolved.Model, agent.StreamRequest{}, agent.StreamOptions{APIKey: resolved.APIKey})
	if err != nil || message.StopReason != agent.StopStop {
		t.Fatalf("非流式调用 = %+v / %v", message, err)
	}
	if !strings.Contains(service.bodies[0], `"stream":false`) {
		t.Fatalf("判断步骤应保持非流式：%s", service.bodies[0])
	}
}

// 剧本模型按分片输出：逐字推送，工具调用和报错轮次不推文字。
func TestScriptedEventStreamEmitsPieces(t *testing.T) {
	delay := 0
	turns := []agent.ScriptedTurn{
		{Text: textPointer("你好世界"), ChunkSize: 2},
		{DelayMS: &delay, Then: &agent.ScriptedTurn{ToolCalls: []agent.ScriptedToolCall{{Name: "query_task", Arguments: map[string]any{}}}}},
	}
	provider := agent.NewScriptedModelProvider(agent.ModelScript{"step": turns}, agent.ScriptedProviderOptions{})
	resolved, err := provider.Resolve("step", &contract.ModelRef{})
	if err != nil {
		t.Fatal(err)
	}

	var pieces []string
	message, err := resolved.EventStream(context.Background(), resolved.Model, agent.StreamRequest{}, agent.EventStreamOptions{
		OnText: func(text string) { pieces = append(pieces, text) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pieces, "|") != "你好|世界" {
		t.Fatalf("分片 = %q", pieces)
	}
	if message.Content[0].Text != "你好世界" || message.StopReason != agent.StopStop {
		t.Fatalf("完整消息 = %+v", message)
	}

	pieces = nil
	toolTurn, err := resolved.EventStream(context.Background(), resolved.Model, agent.StreamRequest{}, agent.EventStreamOptions{
		OnText: func(text string) { pieces = append(pieces, text) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pieces) != 0 || toolTurn.StopReason != agent.StopToolUse || len(toolTurn.Content) != 1 {
		t.Fatalf("工具轮次 = %+v / %q", toolTurn, pieces)
	}
}

// 分片时被取消：消息以 aborted 结束，带上已经输出的部分。
func TestScriptedEventStreamAbortKeepsPieces(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	provider := agent.NewScriptedModelProvider(agent.ModelScript{"step": []agent.ScriptedTurn{
		{Text: textPointer("逐字输出"), ChunkSize: 1},
	}}, agent.ScriptedProviderOptions{})
	resolved, _ := provider.Resolve("step", &contract.ModelRef{})

	message, err := resolved.EventStream(ctx, resolved.Model, agent.StreamRequest{}, agent.EventStreamOptions{
		OnText: func(text string) {
			if text == "逐" {
				cancel()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.StopReason != agent.StopAborted || agent.PartialText(message) != "逐" {
		t.Fatalf("取消后的消息 = %+v", message)
	}
}

func textPointer(text string) *string { return &text }

func intPointer(value int) *int { return &value }
