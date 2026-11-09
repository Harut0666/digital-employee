// 流程卡片：会话中由程序发出的结构化消息。
//
// 任务相关的卡片不写进会话日志，而是读取时从任务事件投影出来。所以任务推进时会话不用跟着写，
// 进程重启也不会漏卡片，旧任务的只读会话走的是同一段代码。按钮永远取自快照的 actions。
package session

import (
	"fmt"
	"os"
	"sort"
	"strings"
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
	// CardUseFile 使用目录文件：前台 Agent 提议把工作区目录里的文件用作材料，人确认后才复制进会话
	CardUseFile = "folder_file"
	// CardPermission 权限确认（T48）：权限模式要求先问人时，Agent 想做的动作和三个答复按钮
	CardPermission = "permission"
	// CardEmployeeForm 员工表单（T49）：run_employee 弹出的结构化表单，取代“确认员工”卡、开工清单卡和提交按钮
	CardEmployeeForm = "employee_form"
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

	// folder_file：目录里的文件（Path 相对工作区目录），提议理由用 Reason
	Path string `json:"path,omitempty"`
	Name string `json:"name,omitempty"`
	Size int64  `json:"size,omitempty"`
	// Confirmed 人已经确认过：材料已经在会话里
	Confirmed bool `json:"confirmed,omitempty"`

	// permission：RequestID 是答复时用的编号；Action 是动作类别，ActionLabel 是它的说法；
	// Tool 是 Agent 调的工具，Object 是动作的对象（文件路径、员工），Detail 是一句话说明。
	// Pending 还在等人答复；答复之后 Decision 是 allow_once、always_allow、deny 之一，停止时是 cancelled
	RequestID   string `json:"requestId,omitempty"`
	Action      string `json:"action,omitempty"`
	ActionLabel string `json:"actionLabel,omitempty"`
	Tool        string `json:"tool,omitempty"`
	Object      string `json:"object,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Pending     bool   `json:"pending,omitempty"`
	Decision    string `json:"decision,omitempty"`
	// Choices 等答复时的三个按钮；答复之后、取消之后没有
	Choices []PermissionChoice `json:"choices,omitempty"`

	// employee_form：Employee 是员工 ID，Draft 是 Agent 预填的表单字段（人在卡片上改了以后以人提交的为准），
	// Prefilled 是其中 Agent 预填的字段名，Materials 是 Agent 给的材料及来源；人在卡片上补传的材料不在这里，
	// 它们是会话材料。Reason 是 Agent 的一句话理由。RequestID、Pending、Decision、TaskID 同上：答复之后
	// Decision 是 confirmed、rejected 之一，停止时是 cancelled，确认开工后 TaskID 是创建的任务。
	// Standalone 表示不是 Agent 弹出的（载入公开样例）：没有一轮在等它，重启后仍可确认。
	EmployeeName string         `json:"employeeName,omitempty"`
	Prefilled    []string       `json:"prefilled,omitempty"`
	Materials    []FormMaterial `json:"materials,omitempty"`
	Standalone   bool           `json:"standalone,omitempty"`
	// Baseline 弹出卡片时会话里已有的材料文件名：之后新出现的（人在卡片上补传的）才算这张卡上补的材料
	Baseline []string `json:"baseline,omitempty"`
	// Checklist 这个员工的开工清单定义：卡片上随人改字段、补材料实时判定哪些已备齐、能不能确认（最终以后端为准）
	Checklist []contract.ChecklistItem `json:"checklist,omitempty"`
}

// 员工表单上一份材料的来源。
const (
	MaterialAttachment = "attachment" // 会话里已有的材料（上传的，或之前确认过的目录文件）
	MaterialFolder     = "folder"     // 工作区目录里的文件，确认开工时复制进任务
	MaterialSample     = "sample"     // 应用目录里的公开样例文件，确认开工时复制进任务
)

// FormMaterial 员工表单上 Agent 给的一份材料。
type FormMaterial struct {
	Name   string `json:"name"`
	Size   int64  `json:"size,omitempty"`
	Source string `json:"source"`
	// Path 目录文件在工作区目录里的相对路径（source=folder）
	Path string `json:"path,omitempty"`
}

// PermissionChoice 权限确认卡上的一个答复按钮：Decision 就是答复时提交的决定。
type PermissionChoice struct {
	Decision string `json:"decision"`
	Label    string `json:"label"`
	Style    string `json:"style"`
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

// withTasks 把会话里每个任务的卡片并进会话消息，按时间排好，并给还有效的卡片配上现在能点的按钮。
// 每个任务分别投影：先开的任务的开工位置、步骤相关的卡片和说明留在它们发生的时间点，不因后来有新任务而消失。
// current 是会话回放后的状态，带着入库描述的草稿；由旧任务推导出的只读会话没有，传 nil。
func (m *Manager) withTasks(messages []Message, taskIDs []string, current *record) ([]Message, error) {
	merged := append([]Message{}, messages...)
	for run, taskID := range taskIDs {
		events, err := m.deps.Engine.Events(taskID, 0)
		if err != nil {
			return nil, err
		}
		projected := []Message{}
		for _, event := range events {
			if message, ok := projectEvent(event); ok {
				projected = append(projected, message)
			}
			if event.Type == contract.EventStateChanged && event.To == contract.StateDelivered {
				if message, ok := m.filingMessage(event, current); ok {
					projected = append(projected, message)
				}
			}
		}
		// 每个任务的事件序号都从 1 开始：第二次运行起的消息 ID 加上运行次数，对话里的 ID 才不重复
		if run > 0 {
			for index := range projected {
				projected[index].ID = fmt.Sprintf("r%d-%s", run+1, projected[index].ID)
			}
		}
		merged = append(merged, projected...)
		if err := m.attachActions(merged, taskID, events); err != nil {
			return nil, err
		}
	}
	root := ""
	if current != nil {
		root = m.workspaceDir(current.workspaceID)
	}
	return orderMessages(attachFolderFileActions(merged, root)), nil
}

// orderMessages 按时间排好会话消息，再把一轮里已经说出的部分放回 Agent 的回复之后：
// 一轮的文字增量与这一轮的卡片、工具留痕、最后那条完整回复时间交错，但显示时它们都是那条回复的一部分。
func orderMessages(messages []Message) []Message {
	sort.SliceStable(messages, func(i, j int) bool { return parseTime(messages[i].TS).Before(parseTime(messages[j].TS)) })
	deltas := map[int64][]Message{}
	final := map[int64]bool{}
	for _, message := range messages {
		switch {
		case message.Kind == KindTurnText:
			deltas[message.Turn] = append(deltas[message.Turn], message)
		case message.Kind == KindAssistant && message.Turn != 0:
			final[message.Turn] = true
		}
	}
	// 已经写完完整回复的轮次：增量放在这条回复之后；还没写完的：放在原来的位置，边说边显示
	out := make([]Message, 0, len(messages))
	for _, message := range messages {
		if message.Kind != KindTurnText {
			out = append(out, message)
			continue
		}
		if !final[message.Turn] {
			out = append(out, message)
		}
	}
	if len(final) == 0 {
		return out
	}
	merged := make([]Message, 0, len(messages))
	for _, message := range out {
		merged = append(merged, message)
		if message.Kind == KindAssistant && message.Turn != 0 {
			merged = append(merged, deltas[message.Turn]...)
		}
	}
	return merged
}

// folder_file 按钮的命令名（与任务命令分开：它不是任务命令，只复制材料）。
const commandUseFolderFile = "use_folder_file"

// folderCardConfirmed 人已经确认过这张卡片：会话里记着“已把目录里的文件作为材料：…”里列出了它的路径。
func folderCardConfirmed(messages []Message, card Card) bool {
	for _, message := range messages {
		if message.Kind != KindUser {
			continue
		}
		for _, rel := range folderConfirmedPaths(message.Text) {
			if rel == card.Path {
				return true
			}
		}
	}
	return false
}

// folderConfirmedPaths 从确认说明里取出列出的路径（“已把目录里的文件作为材料：a、b/周报.docx”）。
func folderConfirmedPaths(text string) []string {
	const prefix = "已把目录里的文件作为材料："
	index := strings.Index(text, prefix)
	if index < 0 {
		return nil
	}
	out := []string{}
	for _, item := range strings.Split(text[index+len(prefix):], "、") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// attachFolderFileActions 给“使用目录文件”卡片配上确认按钮：文件还在目录里、人还没确认过才有。
// 人确认过（会话里记着这张卡片路径的 attachment_added）就不再给按钮。
func attachFolderFileActions(messages []Message, root string) []Message {
	if root == "" {
		return messages
	}
	pending := make([]int, 0, 2)
	for index := range messages {
		card := messages[index].Card
		if card == nil || card.Type != CardUseFile || card.Confirmed {
			continue
		}
		pending = append(pending, index)
	}
	for _, index := range pending {
		card := messages[index].Card
		if folderCardConfirmed(messages, *card) {
			card.Confirmed = true
			card.Actions = nil
			continue
		}
		target, _, err := resolveInWorkspace(root, card.Path)
		if err != nil {
			card.Actions = nil
			continue
		}
		if info, err := os.Stat(target); err != nil || !info.Mode().IsRegular() {
			card.Actions = nil
			continue
		}
		card.Actions = []contract.TaskAction{{
			Command: commandUseFolderFile, Label: "用这个文件", Roles: []contract.Role{contract.RoleInitiator}, Style: "primary",
		}}
	}
	return messages
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
		draft := current.filingDrafts[delivered.TaskID]
		card.Description = draft.text
		card.DraftFailed = draft.failed
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
