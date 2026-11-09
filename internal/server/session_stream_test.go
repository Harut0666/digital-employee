// 前台 Agent 的流式循环：发消息立即 202，回复走会话事件流，可以停止，运行中也可以插话（Spec 0006、T40）。
// 全部从主接缝验证：真实引擎、真实工具、剧本模型，不联网。
package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

// agentEvent 会话事件流里的一条事件。
type agentEvent struct {
	Type   string              `json:"type"`
	Turn   int64               `json:"turn,omitempty"`
	Seq    int64               `json:"seq,omitempty"`
	Text   string              `json:"text,omitempty"`
	Tool   string              `json:"tool,omitempty"`
	ID     string              `json:"id,omitempty"`
	Args   any                 `json:"args,omitempty"`
	Reason string              `json:"reason,omitempty"`
	Result any                 `json:"result,omitempty"`
	IsErr  bool                `json:"isError,omitempty"`
	Msg    *session.Message    `json:"message,omitempty"`
	Detail *contract.TaskEvent `json:"detail,omitempty"`
}

// agentStream 订阅会话事件流：后台读事件放进通道，同时记下收到的每条事件。
type agentStream struct {
	t       *testing.T
	session string
	events  chan agentEvent
	mu      sync.Mutex
	seen    []agentEvent
	done    chan struct{}
	// started 读的协程已经开始收事件
	started chan struct{}
	cancel  func()
}

func (s *agentStream) close() {
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(stopTimeout):
	}
}

// openStream 建立会话事件流；失败时以测试失败收场。
func (h *harness) openStream(id string) *agentStream {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.baseURL+"/api/sessions/"+id+"/stream", nil)
	if err != nil {
		h.t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		cancel()
		h.t.Fatal(err)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.Contains(contentType, "text/event-stream") {
		cancel()
		response.Body.Close()
		h.t.Fatalf("会话事件流的 Content-Type = %q", contentType)
	}
	stream := &agentStream{
		t: h.t, session: id, events: make(chan agentEvent, 256), done: make(chan struct{}),
		// 订阅之后服务端才开始补发和推送：读的协程起来之前事件先攒在这里，一条都不丢
		started: make(chan struct{}),
		cancel:  func() { cancel(); response.Body.Close() },
	}
	go func() {
		defer close(stream.done)
		close(stream.started)
		defer close(stream.events)
		reader := bufio.NewReader(response.Body)
		name := ""
		data := strings.Builder{}
		for {
			line, readErr := reader.ReadString('\n')
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "" && data.Len() > 0:
				event := agentEvent{Type: name}
				if err := json.Unmarshal([]byte(data.String()), &event); err == nil {
					stream.mu.Lock()
					stream.seen = append(stream.seen, event)
					stream.mu.Unlock()

					// 先登记再交给等待方：等待方收到事件时，seen 里一定已经有它

					select {
					case stream.events <- event:
					case <-stream.done:
						return
					}
				}
				name, data = "", strings.Builder{}
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data.WriteString(strings.TrimPrefix(line, "data: "))
			}
			if readErr != nil {
				return
			}
		}
	}()
	return stream
}

// next 等下一条事件，超时算测试失败。
func (s *agentStream) next() agentEvent {
	s.t.Helper()
	// 读的协程起来之前不收事件：这样发消息之后再调用 next 也能从这一轮的第一条读起
	<-s.started

	select {
	case event, ok := <-s.events:
		if !ok {
			s.t.Fatal("事件流在结束之前断开了")
		}
		return event
	case <-time.After(stopTimeout):
		s.t.Fatal("等不到下一条事件")
	}
	return agentEvent{}
}

// turnEvents 收集到 turn_end 为止的事件；超时算测试失败。
func (s *agentStream) turnEvents() []agentEvent {
	s.t.Helper()
	out := []agentEvent{}
	for {
		event := s.next()
		out = append(out, event)
		if event.Type == session.EventTurnEnd {
			return out
		}
	}
}

// eventsOf 到目前为止收到的全部事件。
func (s *agentStream) eventsOf() []agentEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]agentEvent{}, s.seen...)
}

// sayAsync 发一条消息，要求接口立即返回 202（带这一轮的编号），不等回复。
func (h *harness) sayAsync(sessionID string, role contract.Role, text string) int64 {
	h.t.Helper()
	status, data := h.postJSON("/api/sessions/"+sessionID+"/messages", role, map[string]any{"text": text})
	if status != http.StatusAccepted {
		h.t.Fatalf("发消息应立即返回 202：%d %s", status, data)
	}
	accepted := decode[struct {
		OK   bool  `json:"ok"`
		Turn int64 `json:"turn"`
	}](h.t, data)
	if !accepted.OK || accepted.Turn <= 0 {
		h.t.Fatalf("202 响应 = %s", data)
	}
	return accepted.Turn
}

// waitTurn 等编号为 turn 的这一轮结束，返回它的事件。事件流要在发消息之前开好（openStream），
// 否则这一轮可能已经跑完，收不到它的事件。
func (h *harness) waitTurn(stream *agentStream, turn int64) []agentEvent {
	h.t.Helper()
	deadline := time.Now().Add(stopTimeout)
	for {
		if stream.ended(turn) {
			stream.mu.Lock()
			events := []agentEvent{}
			for _, event := range stream.seen {
				if event.Turn == turn {
					events = append(events, event)
				}
			}
			stream.mu.Unlock()
			return events
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("等不到第 %d 轮的结束：%+v", turn, stream.eventsOf())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// ended 编号为 turn 的这一轮是否已经有结束事件。
func (s *agentStream) ended(turn int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range s.seen {
		if event.Type == session.EventTurnEnd && event.Turn == turn {
			return true
		}
	}
	return false
}

// sessionBusy 这个会话现在有没有一轮在跑。
func (h *harness) sessionBusy(id string) bool {
	h.t.Helper()
	status, data := h.send(http.MethodGet, "/api/sessions/"+id, "", "", nil)
	if status != http.StatusOK {
		h.t.Fatalf("读会话失败：%d %s", status, data)
	}
	return decode[session.Detail](h.t, data).AgentRunning
}

// waitRequests 等前台 Agent 的模型调用次数达到 want，返回全部请求。
func (h *harness) waitRequests(want int) []agent.ScriptedRequest {
	h.t.Helper()
	deadline := time.Now().Add(stopTimeout)
	for {
		requests := frontdeskRequests(h)
		if len(requests) >= want {
			return requests
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("模型调用次数停在 %d，等不到 %d 次", len(requests), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// turnEnd 一轮结束事件。
type turnEnd struct {
	Reason string `json:"reason"`
}

func endOf(event agentEvent) turnEnd {
	var end turnEnd
	raw, _ := json.Marshal(event)
	_ = json.Unmarshal(raw, &end)
	return end
}

func eventTypes(events []agentEvent) string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		if event.Type == session.EventText {
			out = append(out, "text")
			continue
		}
		out = append(out, event.Type)
	}
	return strings.Join(out, " ")
}

// textsOf 推送的文字增量拼起来。
func textsOf(events []agentEvent) string {
	text := strings.Builder{}
	for _, event := range events {
		if event.Type == session.EventText {
			text.WriteString(event.Text)
		}
	}
	return text.String()
}

func intPtr(value int) *int { return &value }

// toolNamesOf 会话里某一类留痕的工具名，按写入顺序。
func toolNamesOf(messages []session.Message, kind string) []string {
	out := []string{}
	for _, message := range messages {
		if message.Kind == kind {
			out = append(out, message.Tool)
		}
	}
	return out
}

// lastAssistant 会话里最后一条 Agent 的回复。
func lastAssistant(messages []session.Message) string {
	text := ""
	for _, message := range messages {
		if message.Kind == session.KindAssistant {
			text = message.Text
		}
	}
	return text
}

// cancelledTools 会话留痕里记为“已取消”的工具。
func cancelledTools(messages []session.Message) []string {
	out := []string{}
	for _, message := range messages {
		if message.Kind == session.KindToolCalled && strings.Contains(fmt.Sprint(message.Result), "已取消") {
			out = append(out, message.Tool)
		}
	}
	return out
}

func TestMessageIsAcceptedThenStreamed(t *testing.T) {
	answer := "这项工作适合经营周报员工，请确认。"
	h := newHarness(t, frontdeskScript(
		agent.ScriptedTurn{ToolCalls: []agent.ScriptedToolCall{{Name: "propose_employee", Arguments: map[string]any{"employee": harnessEmployee}}}},
		agent.ScriptedTurn{Text: &answer, ChunkSize: 4},
	))
	created := h.createSession()

	stream := h.openStream(created.ID)
	defer stream.close()
	turn := h.sayAsync(created.ID, contract.RoleInitiator, "帮我出第 44 周的周报")
	events := h.waitTurn(stream, turn)
	if events == nil {
		t.Fatal("看不到这一轮的事件")
	}

	// 顺序：工具开始 → 工具结束 → 卡片 → 文字增量…… → 一轮结束
	types := eventTypes(events)
	if first, last := types[:strings.Index(types, " ")], types[strings.LastIndex(types, " ")+1:]; first != "turn_start" || last != "turn_end" || !strings.HasPrefix(types, "turn_start tool_start") || !strings.Contains(types, "tool_end") || strings.Count(types, "turn_start") != 1 {
		t.Fatalf("事件顺序 = %s", types)
	}
	if events[0].Turn != turn || events[len(events)-1].Turn != turn {
		t.Fatalf("一轮的事件应带编号 %d：%+v", turn, events)
	}
	of := func(kind string) []agentEvent {
		out := []agentEvent{}
		for _, event := range events {
			if event.Type == kind {
				out = append(out, event)
			}
		}
		return out
	}
	started, ended := of(session.EventToolStart), of(session.EventToolEnd)
	if len(started) != 1 || len(ended) != 1 || started[0].Tool != "propose_employee" || ended[0].Tool != "propose_employee" || ended[0].IsErr || started[0].ID != ended[0].ID {
		t.Fatalf("工具事件 = %+v / %+v", started, ended)
	}
	cards := []*session.Card{}
	for _, event := range of(session.EventMessage) {
		if event.Msg != nil && event.Msg.Card != nil {
			cards = append(cards, event.Msg.Card)
		}
	}
	if len(cards) != 1 || cards[0].Type != session.CardEmployeeConfirm || cards[0].Proposed != harnessEmployee {
		t.Fatalf("流程卡片 = %+v", cards)
	}
	if got := textsOf(events); strings.TrimSpace(got) != answer {
		t.Fatalf("文字增量 = %q", got)
	}
	if end := endOf(events[len(events)-1]); end.Reason != session.TurnCompleted {
		t.Fatalf("结束原因 = %q", end.Reason)
	}

	// 文字增量不落盘：一轮结束时才写完整消息
	detail := h.readSession(created.ID)
	if text := lastAssistant(detail.Messages); text != answer {
		t.Fatalf("一轮结束时写入的完整消息 = %q", text)
	}
	if calls := toolNamesOf(detail.Messages, session.KindToolCalled); fmt.Sprint(calls) != "[propose_employee]" {
		t.Fatalf("工具留痕 = %v", calls)
	}
}

// halfwayModel 前台 Agent 的第二轮说半句话就停在半路：测试说“继续”之后才交出工具调用。
// 和翻页一样，停在半路本身就是一次中断。
type halfwayModel struct {
	inner  agent.ModelProvider
	armed  atomic.Bool
	resume chan struct{}
}

func (m *halfwayModel) Kind() string { return m.inner.Kind() }

func (m *halfwayModel) Resolve(stepID string, ref *contract.ModelRef) (*agent.ResolvedStepModel, error) {
	resolved, err := m.inner.Resolve(stepID, ref)
	if err != nil || resolved.StreamEvent == nil {
		return resolved, err
	}
	inner := resolved.StreamEvent
	resolved.StreamEvent = func(ctx context.Context, model agent.Model, request agent.StreamRequest, options agent.EventStreamOptions) (*agent.AssistantMessage, error) {
		if !m.armed.CompareAndSwap(true, false) {
			return inner(ctx, model, request, options)
		}
		written := ""
		for _, piece := range []string{"接下来我", "再看看", "卡片，"} {
			if options.OnText != nil {
				options.OnText(piece)
			}
			written += piece
			if ctx.Err() != nil {
				partial := agent.NewAssistantMessage(model)
				partial.StopReason = agent.StopAborted
				partial.Content = []agent.ContentBlock{agent.TextBlock(written)}
				return &partial, nil
			}
		}
		select {
		case <-m.resume:
			return inner(ctx, model, request, options)
		case <-ctx.Done():
			partial := agent.NewAssistantMessage(model)
			partial.StopReason = agent.StopAborted
			partial.Content = []agent.ContentBlock{agent.TextBlock(written)}
			return &partial, nil
		}
	}
	return resolved, nil
}

// activity GET /api/sessions/:id/activity。
func (h *harness) activity(sessionID string) (session.AgentActivity, error) {
	status, data := h.send(http.MethodGet, "/api/sessions/"+sessionID+"/activity", "", "", nil)
	if status != http.StatusOK {
		return session.AgentActivity{}, fmt.Errorf("读会话活动失败：%d %s", status, data)
	}
	return decode[session.AgentActivity](h.t, data), nil
}

// sustained 收下事件的读取端：一直读到流结束，不让服务端因为没人收而卡在写事件上。
func (s *agentStream) sustained() {
	go func() {
		for {
			if _, ok := <-s.events; !ok {
				return
			}
		}
	}()
}

func TestStopKeepsWhatWasSaidAndCancelsTheRest(t *testing.T) {
	// 第二轮（说半句话、再调两个工具）由测试放行：说到半路时点停止，“已中断”保存的是这半句
	held := &halfwayModel{resume: make(chan struct{})}
	h := newHarness(t, frontdeskScript(
		modelSays("我先看看任务。"),
		agent.ScriptedTurn{
			Text: stringPointer("接下来我再看看卡片，"),
			ToolCalls: []agent.ScriptedToolCall{
				{Name: contract.ToolQueryTask, Arguments: map[string]any{}},
				{Name: "propose_card", Arguments: map[string]any{"type": session.CardPause}},
			},
		},
	), withModel(func(inner agent.ModelProvider) agent.ModelProvider {
		held.inner = inner
		return held
	}), withScriptedModel())
	opened, _ := pausedSession(h)
	stream := h.openStream(opened.ID)
	defer stream.close()
	turn := h.sayAsync(opened.ID, contract.RoleInitiator, "接下来怎么办")
	for event := stream.next(); event.Type != session.EventTurnEnd; event = stream.next() {
	}

	held.armed.Store(true)
	// 取消事件流会让服务端卡在写事件上，测试里另起一个读取端一直收
	stream.sustained()
	if status, data := h.postJSON("/api/sessions/"+opened.ID+"/messages", contract.RoleInitiator, map[string]any{"text": "接着说说"}); status != http.StatusAccepted {
		t.Fatalf("发消息：%d %s", status, data)
	}
	// 等到这半句话已经推送出来（会话里还没写下来），再点停止
	deadline := time.Now().Add(stopTimeout)
	for {
		activity, err := h.activity(opened.ID)
		if err == nil && strings.Contains(activity.Text, "卡片，") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("等不到半句话：%+v", activity)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if status, data := h.postJSON("/api/sessions/"+opened.ID+"/stop", contract.RoleInitiator, map[string]any{}); status != http.StatusOK {
		t.Fatalf("停止：%d %s", status, data)
	}

	deadline = time.Now().Add(stopTimeout)
	for h.sessionBusy(opened.ID) {
		if time.Now().After(deadline) {
			t.Fatal("停止之后会话应空闲")
		}
		time.Sleep(2 * time.Millisecond)
	}
	detail := h.readSession(opened.ID)
	text := lastAssistant(detail.Messages)
	if !strings.Contains(text, "接下来我") || !strings.Contains(text, "已中断") {
		t.Fatalf("已输出部分应以“已中断”保存：%q", text)
	}
	_ = turn
}
func TestStopRequiresARunningTurn(t *testing.T) {
	h := newHarness(t, nil)
	created := h.createSession()

	status, data := h.postJSON("/api/sessions/"+created.ID+"/stop", contract.RoleInitiator, map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("没有在回复时停止：%d %s", status, data)
	}
	reply := decode[struct {
		OK      bool  `json:"ok"`
		Stopped bool  `json:"stopped"`
		Turn    int64 `json:"turn"`
	}](t, data)
	if !reply.OK || reply.Stopped {
		t.Fatalf("没有在回复时应回 ok 但没停止：%s", data)
	}
	if status, data := h.postJSON("/api/sessions/s-missing/stop", contract.RoleInitiator, map[string]any{}); status != http.StatusNotFound {
		t.Fatalf("不存在的会话停止：%d %s", status, data)
	}
}

func TestBusySessionRefusesOtherWrites(t *testing.T) {
	h := newHarness(t, frontdeskScript(agent.ScriptedTurn{DelayMS: intPtr(200), Then: &agent.ScriptedTurn{Text: stringPointer("好的。")}}), withSerial())
	created := h.createSession()
	stream := h.openStream(created.ID)
	defer stream.close()
	turn := h.sayAsync(created.ID, contract.RoleInitiator, "出周报")
	h.waitRequests(1)
	if detail := h.readSession(created.ID); detail.Employee != "" || !detail.AgentRunning {
		t.Fatalf("回复期间的会话 = %+v", detail)
	}

	if status, data := h.postJSON("/api/sessions/"+created.ID+"/employee", contract.RoleInitiator, map[string]any{"employee": harnessEmployee}); status != http.StatusConflict || !strings.Contains(string(data), session.ErrSessionBusy) {
		t.Fatalf("回复期间确认员工：%d %s", status, data)
	}
	// 上传不用等回复
	if status, data := h.uploadTo(created.ID, []upload{{name: "a.csv", content: []byte("x")}}); status != http.StatusOK {
		t.Fatalf("回复期间上传：%d %s", status, data)
	}
	if events := h.waitTurn(stream, turn); events == nil {
		t.Fatal("看不到这一轮的事件")
	}
}
