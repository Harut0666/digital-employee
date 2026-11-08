// 会话事件流：前台 Agent 的一轮回复边进行边推送（Spec 0006“前台 Agent 循环”）。
//
// 文字增量只推送不落盘；一轮结束时把完整消息写进会话日志。日志里写入的每条消息也推送一次，
// 所以流程卡片、程序说明和工具留痕都走同一条流。一轮开始前的历史不补发：断开重连的客户端
// 直接重读会话，再接着看新一轮。
package session

import (
	"strings"
	"sync"
)

// 会话事件的类型（SSE 的 event 名）。
const (
	EventTurnStart = "turn_start" // 一轮开始：{turn}
	EventText      = "text_delta" // 文字增量：{turn, text}
	EventToolStart = "tool_start" // 工具调用开始：{turn, id, tool, args}
	EventToolEnd   = "tool_end"   // 工具调用结束：{turn, id, tool, args, result|reason, isError}
	EventMessage   = "message"    // 写进会话日志的一条消息：{turn, message}
	EventTurnEnd   = "turn_end"   // 一轮结束：{turn, reason, error?}
)

// 一轮结束的原因。
const (
	TurnCompleted  = "completed"   // 说完了
	TurnStopped    = "stopped"     // 人点了停止
	TurnError      = "error"       // 模型不可用
	TurnRoundLimit = "round_limit" // 调用工具的次数到了上限
)

// AgentEvent 推给前端的一条会话事件。
type AgentEvent struct {
	// Turn 这一轮的编号：同一会话里从 1 开始递增；插话不另起一轮
	Turn   int64    `json:"turn"`
	Type   string   `json:"type"`
	Text   string   `json:"text,omitempty"`
	ID     string   `json:"id,omitempty"`
	Tool   string   `json:"tool,omitempty"`
	Args   any      `json:"args,omitempty"`
	Result any      `json:"result,omitempty"`
	Reason string   `json:"reason,omitempty"`
	IsErr  bool     `json:"isError,omitempty"`
	Error  string   `json:"error,omitempty"`
	Msg    *Message `json:"message,omitempty"`
}

// agentEventTypes 会话事件流的全部事件类型（前端按名字逐个监听）。
var agentEventTypes = []string{EventTurnStart, EventText, EventToolStart, EventToolEnd, EventMessage, EventTurnEnd}

// AgentEventTypes 会话事件流推送的全部事件类型。
func AgentEventTypes() []string { return append([]string{}, agentEventTypes...) }

// AgentTool 一轮里的一次工具调用现在走到哪了。
type AgentTool struct {
	ID     string `json:"id"`
	Tool   string `json:"tool"`
	Args   any    `json:"args,omitempty"`
	Result any    `json:"result,omitempty"`
	Reason string `json:"reason,omitempty"`
	IsErr  bool   `json:"isError,omitempty"`
	Ended  bool   `json:"ended"`
}

// AgentActivity 会话现在有没有一轮在跑，以及这一轮已经输出了什么。
type AgentActivity struct {
	Running bool        `json:"running"`
	Turn    int64       `json:"turn"`
	Text    string      `json:"text"`
	Tools   []AgentTool `json:"tools"`
	Cards   []Message   `json:"cards"`
}

// listener 一个订阅者。
type listener struct {
	queue  []AgentEvent
	notify chan struct{}
}

// bus 一个会话的订阅者。推送方持锁广播，订阅方取走自己的队列：慢的订阅者不会拖住 Agent。
type bus struct {
	mu   sync.Mutex
	subs map[*listener]bool
	// current 正在跑的那一轮的编号（0 表示没有）
	current int64
	// ended 最近结束的轮次和原因，job 用完就清掉
	ended  map[int64]string
	// textTurn 正在累积文字增量的轮次；text 这一轮已经说出的部分
	textTurn int64
	text     strings.Builder
	tools  []*AgentTool
	cards  []Message
}

func newBus() *bus {
	return &bus{subs: map[*listener]bool{}, ended: map[int64]string{}}
}

func (b *bus) publish(event AgentEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for sub := range b.subs {
		sub.queue = append(sub.queue, event)
		select {
		case sub.notify <- struct{}{}:
		default:
		}
	}
}

// subscribe 订阅之后的全部事件；cancel 之后不再收到。
func (b *bus) subscribe() (*listener, func()) {
	sub := &listener{notify: make(chan struct{}, 1)}
	b.mu.Lock()
	b.subs[sub] = true
	b.mu.Unlock()
	return sub, func() {
		b.mu.Lock()
		delete(b.subs, sub)
		b.mu.Unlock()
	}
}

// drain 取走排队的事件，并告诉订阅者队列空了。
func (sub *listener) drain() []AgentEvent {
	sub.queue = append([]AgentEvent{}, sub.queue...)
	out := sub.queue
	sub.queue = nil
	select {
	case <-sub.notify:
	default:
	}
	return out
}

// started 一轮开始。
func (b *bus) started(turn int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.current = turn
	b.textTurn = turn
	b.text.Reset()
	b.tools = nil
	b.cards = nil
	delete(b.ended, turn)
}

// textDelta 一段文字增量。
func (b *bus) textDelta(text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.text.WriteString(text)
}

// textDeltaSeen 这一轮是否已经推过文字增量。
func (b *bus) textDeltaSeen(turn int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.textTurn == turn && b.text.Len() > 0
}

// toolStarted 一个工具调用开始（记录在 activity 里，供后来订阅的人看到）。
func (b *bus) toolStarted(tool AgentTool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	copied := tool
	b.tools = append(b.tools, &copied)
}

// toolEnded 一个工具调用结束。
func (b *bus) toolEnded(id string, result any, reason string, isErr bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, tool := range b.tools {
		if tool.ID == id && !tool.Ended {
			tool.Ended = true
			tool.Result, tool.Reason, tool.IsErr = result, reason, isErr
		}
	}
}

// card 记下这一轮发出的卡片，供后来订阅的人看到。
func (b *bus) card(message Message) {
	if message.Card == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cards = append(b.cards, message)
}

// finished 一轮结束：推一条 turn_end，再清掉这一轮的现场（工具、卡片）。
func (b *bus) finished(turn int64, reason string) {
	b.publish(AgentEvent{Turn: turn, Type: EventTurnEnd, Reason: reason})
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.current == turn {
		b.current = 0
	}
	b.tools = nil
	b.cards = nil
}

// activity 这一轮现在的样子。
func (b *bus) activity() AgentActivity {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := AgentActivity{Running: b.current != 0, Turn: b.current, Text: b.text.String(), Tools: []AgentTool{}, Cards: []Message{}}
	for _, tool := range b.tools {
		out.Tools = append(out.Tools, *tool)
	}
	out.Cards = append(out.Cards, b.cards...)
	return out
}
