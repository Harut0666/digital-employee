// 流程卡片：会话中由程序发出的结构化消息。
//
// 任务相关的卡片不写进会话日志，而是读取时从任务事件投影出来。所以任务推进时会话不用跟着写，
// 进程重启也不会漏卡片，旧任务的只读会话走的是同一段代码。按钮永远取自快照的 actions。
package session

import (
	"fmt"
	"sort"
	"time"

	"digital-employee/internal/contract"
)

// 卡片类型。
const (
	CardEmployeeConfirm = "employee_confirm"
	CardForm            = "form"
	CardPause           = "pause"
	CardQC              = "qc"
	CardReview          = "review"
	// CardFiling 入库确认：交付后由程序发出，模型起草描述，人确认后入库
	CardFiling = "knowledge_filing"
)

// 员工确认卡片的来源。
const (
	SourceDefault = "default" // 设置里的默认员工
	SourceModel   = "model"   // 模型提议
	SourceManual  = "manual"  // 判断不出，列出员工手选
)

// Card 一张流程卡片。字段按类型取用，内容都由程序填写。
type Card struct {
	Type string `json:"type"`

	// employee_confirm
	Proposed   string     `json:"proposed,omitempty"`
	Source     string     `json:"source,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	Candidates []Employee `json:"candidates,omitempty"`

	// form
	Employee    string         `json:"employee,omitempty"`
	Draft       map[string]any `json:"draft,omitempty"`
	Attachments []Attachment   `json:"attachments,omitempty"`
	// Submitted 已提交的表单，只读
	Submitted bool `json:"submitted,omitempty"`

	// pause / qc / review
	TaskID string `json:"taskId,omitempty"`
	// AtSeq 卡片发出时任务走到的事件序号：早于当前暂停的卡片已失效
	AtSeq int64                 `json:"atSeq,omitempty"`
	Pause *contract.PauseReason `json:"pause,omitempty"`
	// Actions 现在能点的按钮，读取时取自快照；卡片失效时为空
	Actions []contract.TaskAction `json:"actions,omitempty"`

	// qc
	Passed *bool                    `json:"passed,omitempty"`
	Checks []contract.QcCheckResult `json:"checks,omitempty"`

	// review：退回意见的草稿，人确认后才退回
	Comment  string   `json:"comment,omitempty"`
	Sections []string `json:"sections,omitempty"`

	// knowledge_filing：默认标题、模型起草的描述（人确认或修改后才入库）、已入库的条目 ID
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	// DraftFailed 模型起草失败，由人手写
	DraftFailed bool   `json:"draftFailed,omitempty"`
	Filed       string `json:"filed,omitempty"`
}

func confirmCard(employees []Employee, proposed, source, reason string) Card {
	return Card{Type: CardEmployeeConfirm, Proposed: proposed, Source: source, Reason: reason, Candidates: employees}
}

// projectEvent 一条任务事件在会话里的样子；不需要显示时返回 false。
func projectEvent(event contract.TaskEvent) (Message, bool) {
	message := Message{ID: fmt.Sprintf("t%d", event.Seq), Seq: event.Seq, TS: event.TS, Kind: KindCard}
	card := func(card Card) (Message, bool) {
		card.TaskID = event.TaskID
		card.AtSeq = event.Seq
		message.Card = &card
		return message, true
	}
	notice := func(text string) (Message, bool) {
		message.Kind = KindNotice
		message.Text = text
		return message, true
	}
	switch event.Type {
	case contract.EventTaskCreated:
		attachments := make([]Attachment, 0, len(event.Attachments))
		for _, attachment := range event.Attachments {
			attachments = append(attachments, Attachment{Name: attachment.Name})
		}
		return card(Card{Type: CardForm, Employee: event.Employee, Draft: event.Form, Attachments: attachments, Submitted: true})
	case contract.EventQCResult:
		return card(Card{Type: CardQC, Passed: event.Passed, Checks: event.Checks})
	case contract.EventReviewDecision:
		if event.Decision == string(contract.TriggerReturn) {
			return notice("审核人退回：" + event.Comment)
		}
		return notice("审核人已通过。")
	case contract.EventStateChanged:
		switch event.To {
		case contract.StateAwaitingInitiator, contract.StateAwaitingOperator:
			return card(Card{Type: CardPause, Pause: event.Pause})
		case contract.StateAwaitingReview:
			return card(Card{Type: CardReview, Pause: event.Pause})
		case contract.StateDelivered:
			return notice("任务已交付。")
		case contract.StateTerminated:
			return notice("任务已终止。")
		}
	}
	return Message{}, false
}

// withTask 把任务的卡片并进会话消息，按时间排好，并给还有效的卡片配上现在能点的按钮。
// current 是会话回放后的状态，带着入库描述的草稿；由旧任务推导出的只读会话没有，传 nil。
func (m *Manager) withTask(messages []Message, taskID string, current *record) ([]Message, error) {
	events, err := m.deps.Engine.Events(taskID, 0)
	if err != nil {
		return nil, err
	}
	merged := append([]Message{}, messages...)
	for _, event := range events {
		if message, ok := projectEvent(event); ok {
			merged = append(merged, message)
		}
		if event.Type == contract.EventStateChanged && event.To == contract.StateDelivered {
			if message, ok := m.filingMessage(event, current); ok {
				merged = append(merged, message)
			}
		}
	}
	sort.SliceStable(merged, func(i, j int) bool { return parseTime(merged[i].TS).Before(parseTime(merged[j].TS)) })
	return merged, m.attachActions(merged, taskID, events)
}

// filingMessage 交付之后的入库确认卡片：紧跟在“任务已交付”之后。
// 标题和是否已入库读取时从知识库取，描述草稿取自会话日志。没有可入库的交付物时不发。
func (m *Manager) filingMessage(delivered contract.TaskEvent, current *record) (Message, bool) {
	if m.deps.Filing == nil {
		return Message{}, false
	}
	offer, ok := m.deps.Filing.Offer(delivered.TaskID)
	if !ok {
		return Message{}, false
	}
	card := Card{Type: CardFiling, TaskID: delivered.TaskID, AtSeq: delivered.Seq, Title: offer.Title, Filed: offer.Filed}
	if current != nil {
		card.Description = current.filingDraft
		card.DraftFailed = current.filingFailed
	}
	return Message{
		ID: fmt.Sprintf("t%d-filing", delivered.Seq), Seq: delivered.Seq, TS: delivered.TS, Kind: KindCard, Card: &card,
	}, true
}

// attachActions 给当前暂停的卡片配上快照里的按钮和暂停说明；更早的卡片已失效，不配。
func (m *Manager) attachActions(messages []Message, taskID string, events []contract.TaskEvent) error {
	snapshot, found := m.deps.Engine.Get(taskID)
	if !found || !contract.IsPaused(snapshot.State) {
		return nil
	}
	opened := int64(0)
	for _, event := range events {
		if event.Type == contract.EventStateChanged {
			opened = event.Seq
		}
	}
	for _, message := range messages {
		card := message.Card
		if card != nil && card.TaskID == taskID && card.AtSeq >= opened && (card.Type == CardPause || card.Type == CardReview) {
			card.Actions = snapshot.Actions
			card.Pause = snapshot.Pause
		}
	}
	return nil
}

// parseTime 会话日志和任务事件的时间精度不同，要按时间比较，不能按字符串比较。
func parseTime(text string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
