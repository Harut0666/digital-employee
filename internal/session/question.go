// ask_user_question：Agent 向发起人提结构化问题。
//
// 这一轮在工具调用处暂停，对话里出现“提问”卡片；发起人选完提交（POST /questions/{requestId}），
// 答案 {answers:[{id,selected[],custom?}]} 作为工具结果交给模型。只有发起人能答；停止按钮在等待时同样有效，
// 未决的提问记为“已取消”。等待、停止、答复竞争用的是权限确认的同一套机制（permission.go 的 awaitHuman / answerWait），
// 没有另抄一份。所有权限模式（含计划）都允许：它只是问人，不对工作区做任何事。
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

const (
	maxQuestions         = 4
	minOptions           = 2
	maxOptions           = 4
	maxCustomAnswer      = 2000
	kindQuestionAnswered = "question_answered"
)

// DecisionAnswered 提问卡片已答复后的 Decision（停止时是 cancelled）。
const DecisionAnswered = "answered"

// QuestionOption 一个选项。
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Question 一道问题。
type Question struct {
	ID          string           `json:"id"`
	Question    string           `json:"question"`
	Header      string           `json:"header,omitempty"`
	Options     []QuestionOption `json:"options"`
	MultiSelect bool             `json:"multiSelect,omitempty"`
}

// QuestionAnswer 一道问题的答案：选中的选项，加上“其他”自由输入。
type QuestionAnswer struct {
	ID       string   `json:"id"`
	Selected []string `json:"selected"`
	Custom   string   `json:"custom,omitempty"`
}

// parseQuestions 校验模型给的问题：最多 4 题，每题 2–4 个选项，id 不重复。
func parseQuestions(raw any) ([]Question, error) {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, toolFailure("invalid_questions", "questions 必须是非空数组")
	}
	if len(list) > maxQuestions {
		return nil, toolFailure("invalid_questions", "一次最多问 %d 个问题，收到 %d 个。把问题合并或分几次问", maxQuestions, len(list))
	}
	questions := make([]Question, 0, len(list))
	ids := map[string]bool{}
	for index, entry := range list {
		fields, ok := entry.(map[string]any)
		if !ok {
			return nil, toolFailure("invalid_questions", "第 %d 个问题不是对象", index+1)
		}
		question := Question{}
		question.ID = strings.TrimSpace(asString(fields["id"]))
		question.Question = strings.TrimSpace(asString(fields["question"]))
		question.Header = strings.TrimSpace(asString(fields["header"]))
		question.MultiSelect, _ = fields["multi_select"].(bool)
		switch {
		case question.ID == "":
			return nil, toolFailure("invalid_questions", "第 %d 个问题缺少 id", index+1)
		case ids[question.ID]:
			return nil, toolFailure("invalid_questions", "问题 id 重复：%s", question.ID)
		case question.Question == "":
			return nil, toolFailure("invalid_questions", "问题 %s 缺少 question", question.ID)
		}
		ids[question.ID] = true
		options, _ := fields["options"].([]any)
		if len(options) < minOptions || len(options) > maxOptions {
			return nil, toolFailure("invalid_questions", "问题 %s 要给 %d–%d 个选项（“其他”自由输入由界面另外提供），收到 %d 个", question.ID, minOptions, maxOptions, len(options))
		}
		labels := map[string]bool{}
		for _, rawOption := range options {
			optionFields, _ := rawOption.(map[string]any)
			option := QuestionOption{Label: strings.TrimSpace(asString(optionFields["label"])), Description: strings.TrimSpace(asString(optionFields["description"]))}
			if option.Label == "" {
				return nil, toolFailure("invalid_questions", "问题 %s 有一个选项缺少 label", question.ID)
			}
			if labels[option.Label] {
				return nil, toolFailure("invalid_questions", "问题 %s 的选项重复：%s", question.ID, option.Label)
			}
			labels[option.Label] = true
			question.Options = append(question.Options, option)
		}
		questions = append(questions, question)
	}
	return questions, nil
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}

// askUserQuestionTool ask_user_question。
func (loop *turnLoop) askUserQuestionTool() tool {
	option := map[string]any{"type": "object", "required": []any{"label"}, "properties": map[string]any{
		"label":       map[string]any{"type": "string", "description": "简短的选项名；如果你推荐某一项，把它放第一个并在名字后加“（推荐）”"},
		"description": map[string]any{"type": "string", "description": "一句话说明这个选项的影响或取舍"},
	}}
	return tool{
		spec: agent.ToolSpec{
			Name: contract.ToolAskUserQuestion,
			Description: "向发起人提结构化问题，在需要确认、选择或缺少信息时用。最多 4 个问题，每题 2–4 个选项（界面会另外提供“其他”自由输入）。" +
				"这一轮会暂停，等发起人选完提交，答案作为工具结果交给你；发起人点停止则视为取消。所有权限模式都可以用。",
			Parameters: map[string]any{"type": "object", "required": []any{"questions"}, "properties": map[string]any{
				"questions": map[string]any{"type": "array", "description": "要问的问题（最多 4 个）", "items": map[string]any{
					"type": "object", "required": []any{"id", "question", "options"}, "properties": map[string]any{
						"id":           map[string]any{"type": "string", "description": "这个问题的稳定 id，答案里原样带回"},
						"question":     map[string]any{"type": "string", "description": "具体要问的问题"},
						"header":       map[string]any{"type": "string", "description": "问题的简短标题，如“确认”“选统计周期”"},
						"options":      map[string]any{"type": "array", "description": "2–4 个选项", "items": option},
						"multi_select": map[string]any{"type": "boolean", "description": "是否可以多选，默认 false"},
					}}},
			}},
		},
		runCtx: func(ctx context.Context, arguments map[string]any) (any, error) {
			// 只有发起人能答：别的角色触发的这一轮里提问没有人能回答，直接让 Agent 把问题写进回复
			current, err := loop.job.manager.load(loop.job.session)
			if err != nil {
				return nil, err
			}
			if speaker := speakerRole(current, loop.job.role); speaker != contract.RoleInitiator {
				return nil, denied("现在和你说话的是%s，只有发起人能回答提问卡。请把要问的话直接写在回复里。", contract.RoleLabels[speaker])
			}
			questions, err := parseQuestions(arguments["questions"])
			if err != nil {
				return nil, err
			}
			payload, cancelled, err := loop.awaitHuman(ctx, kindQuestionAnswered, contract.ToolAskUserQuestion, Card{Type: CardQuestion, Questions: questions})
			if err != nil {
				return nil, err
			}
			if cancelled {
				return nil, errTurnCancelled
			}
			var answers []QuestionAnswer
			if err := json.Unmarshal([]byte(payload), &answers); err != nil {
				return nil, fmt.Errorf("读不懂提问的答案：%v", err)
			}
			return map[string]any{"answers": answers}, nil
		},
	}
}

// validateAnswers 提交的答案必须逐题对应、选项必须存在、单选只能选一个。
func validateAnswers(questions []Question, answers []QuestionAnswer) ([]QuestionAnswer, error) {
	byID := map[string]Question{}
	for _, question := range questions {
		byID[question.ID] = question
	}
	got := map[string]QuestionAnswer{}
	for _, answer := range answers {
		question, known := byID[answer.ID]
		if !known {
			return nil, failure(contract.ErrInvalidInput, "没有这个问题：%s", answer.ID)
		}
		if _, dup := got[answer.ID]; dup {
			return nil, failure(contract.ErrInvalidInput, "问题 %s 答了不止一次", answer.ID)
		}
		valid := map[string]bool{}
		for _, option := range question.Options {
			valid[option.Label] = true
		}
		selected := []string{}
		seen := map[string]bool{}
		for _, label := range answer.Selected {
			if !valid[label] {
				return nil, failure(contract.ErrInvalidInput, "问题 %s 没有这个选项：%s", answer.ID, label)
			}
			if !seen[label] {
				seen[label] = true
				selected = append(selected, label)
			}
		}
		custom := strings.TrimSpace(answer.Custom)
		if len([]rune(custom)) > maxCustomAnswer {
			return nil, failure(contract.ErrInvalidInput, "问题 %s 的自由输入太长（最多 %d 字）", answer.ID, maxCustomAnswer)
		}
		if len(selected) > 1 && !question.MultiSelect {
			return nil, failure(contract.ErrInvalidInput, "问题 %s 是单选，只能选一个", answer.ID)
		}
		if len(selected) == 0 && custom == "" {
			return nil, failure(contract.ErrInvalidInput, "问题 %s 还没有回答：请选一个选项，或在“其他”里写下你的回答", answer.ID)
		}
		got[answer.ID] = QuestionAnswer{ID: answer.ID, Selected: selected, Custom: custom}
	}
	out := make([]QuestionAnswer, 0, len(questions))
	for _, question := range questions {
		answer, ok := got[question.ID]
		if !ok {
			return nil, failure(contract.ErrInvalidInput, "问题 %s 还没有回答", question.ID)
		}
		out = append(out, answer)
	}
	return out, nil
}

// questionCardOf 会话日志里某张提问卡。
func questionCardOf(current *record, requestID string) (Card, bool) {
	for _, message := range current.messages {
		if message.Card != nil && message.Card.Type == CardQuestion && message.Card.RequestID == requestID {
			return *message.Card, true
		}
	}
	return Card{}, false
}

// AnswerQuestion 发起人在提问卡上提交答案。不占这一轮的写锁：这一轮正停在这张卡上等着。
func (m *Manager) AnswerQuestion(id string, role contract.Role, requestID string, answers []QuestionAnswer) (*Reply, error) {
	if role != contract.RoleInitiator {
		return nil, failure(contract.ErrForbiddenRole, "只有发起人可以回答提问")
	}
	current, err := m.load(id)
	if err != nil {
		return nil, err
	}
	card, found := questionCardOf(current, requestID)
	if !found {
		return nil, failure(contract.ErrNotFound, "没有这张提问：%s", requestID)
	}
	checked, err := validateAnswers(card.Questions, answers)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(checked)
	if err != nil {
		return nil, err
	}
	return m.answerWait(id, role, requestID,
		func(*record) bool { return true },
		func(wait *permissionWait) Message {
			return Message{Kind: kindQuestionAnswered, RequestID: requestID, Decision: DecisionAnswered, Action: wait.action, Answers: checked}
		}, string(raw))
}

// answersRecorded 日志里某张提问卡的答案；没有答复时为 nil。
func answersRecorded(current *record, requestID string) []QuestionAnswer {
	for _, message := range current.messages {
		if message.Kind == kindQuestionAnswered && message.RequestID == requestID {
			return message.Answers
		}
	}
	return nil
}

// projectQuestions 给提问卡配上现在的状态：已答复（带答案）、仍在等、或已取消（停止，或进程重启后没人在等）。
func (m *Manager) projectQuestions(messages []Message, current *record) []Message {
	if current == nil {
		return messages
	}
	decided := map[string]Message{}
	for _, message := range current.messages {
		if message.Kind == kindQuestionAnswered {
			decided[message.RequestID] = message
		}
	}
	for index := range messages {
		card := messages[index].Card
		if card == nil || card.Type != CardQuestion {
			continue
		}
		copied := *card
		copied.Pending, copied.Decision, copied.Answers = false, "", nil
		if message, found := decided[copied.RequestID]; found {
			copied.Decision, copied.Answers = message.Decision, message.Answers
		} else if m.waiting(copied.RequestID, current.id) {
			copied.Pending = true
		} else {
			copied.Decision = DecisionCancelled
		}
		messages[index].Card = &copied
	}
	return messages
}
