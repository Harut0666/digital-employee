// 前台 Agent 的多轮循环。
//
// 它和判断步骤共用模型提供者和剧本模型，但没有一次性提交：模型每轮要么调工具，要么说话，
// 说话就结束这条回复。工具轮次有上限，对话不占任务预算。
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

// MaxToolRounds 单条回复的工具轮次上限，超过就停下说明。
const MaxToolRounds = 20

const roundLimitText = "这条回复调用工具的次数已到上限，我先停在这里。请换个说法，或直接在卡片上操作。"

// taskReader Agent 能看到的全部任务接口：只读。工具里拿不到 Engine，也就发不出任务命令。
type taskReader interface {
	Get(taskID contract.TaskID) (*contract.TaskSnapshot, bool)
}

// errModelUnavailable 模型调用失败，说明已写入会话日志。
var errModelUnavailable = errors.New("模型不可用")

// denial 越权调用：拒绝，并写入会话日志。
type denial struct{ reason string }

func (d *denial) Error() string { return d.reason }

func denied(format string, args ...any) error {
	return &denial{reason: fmt.Sprintf(format, args...)}
}

// tool 这一轮开放给模型的一个工具。
type tool struct {
	spec agent.ToolSpec
	run  func(arguments map[string]any) (any, error)
}

// turn 一条回复的处理过程。
type turn struct {
	manager *Manager
	current *record
	role    contract.Role
	tasks   taskReader
	// out 这条回复写入的全部消息
	out []Message
	// cards 回复说完之后才发出的卡片
	cards []Card
	// proposed 模型提议了员工
	proposed bool
	// drafted 模型改了表单草稿
	drafted bool
}

func (t *turn) write(message Message) error {
	written, err := t.manager.store.append(t.current.id, message)
	if err != nil {
		return err
	}
	t.current.apply(written)
	t.out = append(t.out, written)
	return nil
}

// flushCards 发出排队的卡片。
func (t *turn) flushCards() error {
	for index := range t.cards {
		if err := t.write(Message{Kind: KindCard, Card: &t.cards[index]}); err != nil {
			return err
		}
	}
	t.cards = nil
	return nil
}

// history 发给模型的对话：只有人和 Agent 说的话，卡片和留痕是界面消息。
func (t *turn) history() []agent.Message {
	messages := []agent.Message{}
	for _, message := range t.current.messages {
		switch message.Kind {
		case KindUser:
			messages = append(messages, textMessage("user", message.Text))
		case KindAssistant:
			messages = append(messages, textMessage("assistant", message.Text))
		}
	}
	return messages
}

func textMessage(role, text string) agent.Message {
	return agent.Message{Role: role, Content: []agent.ContentBlock{agent.TextBlock(text)}}
}

// resolveModel 取前台 Agent 的模型；失败写入会话日志。
func (t *turn) resolveModel() (*agent.ResolvedStepModel, error) {
	fail := func(reason string) (*agent.ResolvedStepModel, error) {
		if err := t.write(Message{Kind: KindModelFailed, Reason: reason}); err != nil {
			return nil, err
		}
		return nil, errModelUnavailable
	}
	var provider agent.ModelProvider
	if t.manager.deps.Model != nil {
		provider = t.manager.deps.Model()
	}
	if provider == nil {
		return fail("没有可用的模型提供者")
	}
	resolved, err := provider.Resolve(agent.FrontdeskStepID, nil)
	if err != nil {
		return fail(err.Error())
	}
	if provider.Kind() == "real" && resolved.APIKey == "" {
		return fail("默认模型连接没有配置 API Key")
	}
	return resolved, nil
}

// converse 跑一条回复，返回模型最后说的话。
func (t *turn) converse(ctx context.Context, system string, conversation []agent.Message, tools []tool) (string, error) {
	resolved, err := t.resolveModel()
	if err != nil {
		return "", err
	}
	var execution *contract.ExecutionSettings
	if t.manager.deps.Execution != nil {
		execution = t.manager.deps.Execution()
	}
	specs := make([]agent.ToolSpec, 0, len(tools))
	open := map[string]tool{}
	for _, item := range tools {
		specs = append(specs, item.spec)
		open[item.spec.Name] = item
	}
	messages := append([]agent.Message{textMessage("system", system)}, conversation...)

	for rounds := 0; ; rounds++ {
		produced, err := resolved.Stream(ctx, resolved.Model, agent.StreamRequest{Messages: messages, Tools: specs}, agent.StreamOptions{
			APIKey:     resolved.APIKey,
			MaxTokens:  resolved.Model.MaxTokens,
			MaxRetries: agent.ResolveModelMaxRetries(resolved.MaxRetries, execution),
			TimeoutMS:  resolved.TimeoutMS,
		})
		if err == nil && (produced.StopReason == agent.StopError || produced.StopReason == agent.StopAborted) {
			err = errors.New(produced.ErrorMessage)
		}
		if err != nil {
			if writeErr := t.write(Message{Kind: KindModelFailed, Reason: agent.RedactAPIKey(err.Error(), resolved.APIKey)}); writeErr != nil {
				return "", writeErr
			}
			return "", errModelUnavailable
		}

		text := strings.Builder{}
		calls := []agent.ContentBlock{}
		for _, block := range produced.Content {
			switch block.Type {
			case agent.ContentText:
				text.WriteString(block.Text)
			case agent.ContentToolCall:
				calls = append(calls, block)
			}
		}
		if len(calls) == 0 {
			return strings.TrimSpace(text.String()), nil
		}
		if rounds >= MaxToolRounds {
			return roundLimitText, nil
		}

		messages = append(messages, agent.Message{Role: "assistant", Content: produced.Content})
		for _, call := range calls {
			result, err := t.call(open, call)
			if err != nil {
				return "", err
			}
			// 一条结果一条消息：OpenAI 兼容接口要求每个工具调用都有自己的 tool 消息
			messages = append(messages, agent.Message{Role: "tool", Content: []agent.ContentBlock{result}})
		}
	}
}

// call 执行一次工具调用，写入留痕，返回交给模型的结果。
func (t *turn) call(open map[string]tool, call agent.ContentBlock) (agent.ContentBlock, error) {
	item, ok := open[call.Name]
	if !ok {
		names := make([]string, 0, len(open))
		for name := range open {
			names = append(names, name)
		}
		sort.Strings(names)
		return t.deny(call, fmt.Sprintf("工具 %s 现在没有开放给前台 Agent。现在可用的工具：%s。", call.Name, strings.Join(names, "、")))
	}
	result, err := item.run(call.Arguments)
	var refused *denial
	if errors.As(err, &refused) {
		return t.deny(call, refused.reason)
	}
	if err != nil {
		if writeErr := t.write(Message{Kind: KindToolCalled, Tool: call.Name, Args: call.Arguments, Reason: err.Error()}); writeErr != nil {
			return agent.ContentBlock{}, writeErr
		}
		return agent.ToolResultBlock(call.ID, err.Error(), true), nil
	}
	if err := t.write(Message{Kind: KindToolCalled, Tool: call.Name, Args: call.Arguments, Result: result}); err != nil {
		return agent.ContentBlock{}, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return agent.ContentBlock{}, err
	}
	return agent.ToolResultBlock(call.ID, string(raw), false), nil
}

func (t *turn) deny(call agent.ContentBlock, reason string) (agent.ContentBlock, error) {
	if err := t.write(Message{Kind: KindToolDenied, Tool: call.Name, Args: call.Arguments, Reason: reason}); err != nil {
		return agent.ContentBlock{}, err
	}
	return agent.ToolResultBlock(call.ID, reason, true), nil
}

// ---------------------------------------------------------------- 接待

const receptionPrompt = `你是数字员工系统的前台 Agent，负责判断发起人要做的工作该由哪个员工接手。你说话平实、简短，用中文回答。

- 看懂要做什么后，调用 propose_employee 提议一个员工，并用一句话说明理由。提议之后由人确认，你不能替人确认，也不能自己开始工作。
- 只能提议下面列出的员工。没有合适的员工就如实说，不要硬选。
- 说不清要做什么时，问一句，不要猜。

## 可接手的员工
`

func receptionSystem(employees []Employee) string {
	lines := make([]string, 0, len(employees))
	for _, employee := range employees {
		lines = append(lines, fmt.Sprintf("- %s（id：%s）：%s", employee.Name, employee.ID, employee.Description))
	}
	return receptionPrompt + strings.Join(lines, "\n") + "\n"
}

func (t *turn) proposeEmployeeTool(employees []Employee) tool {
	ids := make([]any, 0, len(employees))
	for _, employee := range employees {
		ids = append(ids, employee.ID)
	}
	return tool{
		spec: agent.ToolSpec{
			Name:        "propose_employee",
			Description: "提议由哪个员工接手这项工作。提议后程序会发出确认卡片，由人确认。",
			Parameters: map[string]any{
				"type":     "object",
				"required": []any{"employee"},
				"properties": map[string]any{
					"employee": map[string]any{"type": "string", "enum": ids, "description": "员工 id"},
					"reason":   map[string]any{"type": "string", "description": "一句话说明为什么由这个员工接手"},
				},
			},
		},
		run: func(arguments map[string]any) (any, error) {
			id, _ := arguments["employee"].(string)
			reason, _ := arguments["reason"].(string)
			for _, employee := range employees {
				if employee.ID == id {
					t.proposed = true
					t.cards = append(t.cards, confirmCard(employees, id, SourceModel, reason))
					return map[string]any{"proposed": id}, nil
				}
			}
			return nil, fmt.Errorf("没有这个员工：%s", id)
		},
	}
}

// defaultEmployee 设置里的默认员工；没设或已不存在时返回空串。
func (t *turn) defaultEmployee(employees []Employee) string {
	if t.manager.deps.DefaultEmployee == nil {
		return ""
	}
	id := t.manager.deps.DefaultEmployee()
	for _, employee := range employees {
		if employee.ID == id {
			return id
		}
	}
	return ""
}

// route 按“默认员工 → 模型提议 → 列出员工手选”决定谁接手。
func (t *turn) route(ctx context.Context) error {
	employees, err := t.manager.deps.Employees()
	if err != nil {
		return err
	}
	if id := t.defaultEmployee(employees); id != "" {
		if err := t.write(Message{Kind: KindNotice, Text: "这项工作默认由下面的员工接手，请确认。"}); err != nil {
			return err
		}
		t.cards = append(t.cards, confirmCard(employees, id, SourceDefault, ""))
		return t.flushCards()
	}
	text, err := t.converse(ctx, receptionSystem(employees), t.history(), []tool{t.proposeEmployeeTool(employees)})
	unavailable := errors.Is(err, errModelUnavailable)
	if err != nil && !unavailable {
		return err
	}
	if unavailable {
		if err := t.write(Message{Kind: KindNotice, Text: "模型暂时不可用，我判断不了该由谁接手。请从列表里选择员工。"}); err != nil {
			return err
		}
	} else if text != "" {
		if err := t.write(Message{Kind: KindAssistant, Text: text}); err != nil {
			return err
		}
	}
	if !t.proposed {
		t.cards = append(t.cards, confirmCard(employees, "", SourceManual, ""))
	}
	return t.flushCards()
}

// ---------------------------------------------------------------- 接手之后

const handoverNote = "（系统提示）发起人已确认由你接手。请根据此前的对话把能确定的信息填进表单草稿；信息不足时问一句，不要猜。"

// assistantSystem 员工的 frontdesk.md 加上这个会话的现状。
func (t *turn) assistantSystem(config *contract.EmployeeConfig) string {
	lines := []string{
		strings.TrimSpace(config.Frontdesk), "", "## 当前会话", "",
		"- 正在和你说话的人：" + contract.RoleLabels[t.role],
	}
	if t.current.taskID != "" {
		lines = append(lines, "- 任务："+t.current.taskID)
	} else {
		lines = append(lines, "- 任务：还没有提交")
		lines = append(lines, "- 表单草稿："+compactJSON(t.current.draft))
		names := []string{}
		for _, attachment := range t.current.attachments {
			names = append(names, attachment.Name)
		}
		lines = append(lines, "- 已提供的材料："+strings.Join(names, "、"))
		lines = append(lines, checklistLines(config, t.current)...)
	}
	return strings.Join(lines, "\n") + "\n"
}

func compactJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(raw)
}

// assistantTools 这个阶段开放的工具：固定的草稿类工具，加上 frontdesk.md 勾选的只读工具。
func (t *turn) assistantTools(config *contract.EmployeeConfig) ([]tool, error) {
	ticked := map[string]bool{}
	for _, name := range contract.FrontdeskTools(config.Frontdesk) {
		ticked[name] = true
	}
	tools := []tool{}
	if t.current.taskID != "" {
		if ticked[contract.ToolQueryTask] {
			tools = append(tools, t.queryTaskTool())
		}
		tools = append(tools, t.proposeCardTool())
	} else {
		tools = append(tools, t.fillFormDraftTool(config), t.proposeCardTool())
	}
	knowledge, err := t.knowledgeTools(ticked)
	if err != nil {
		return nil, err
	}
	// 目录读取工具对所有员工都开放：工作区就是目录，Agent 要能看见里面有什么
	return append(append(tools, t.folderTools()...), knowledge...), nil
}

// unavailableText 模型不可用时程序自己说的话：任务的现状不需要模型也说得清。
func (t *turn) unavailableText() string {
	text := "模型暂时不可用，我现在回答不了。可以直接在卡片上操作。"
	if t.current.taskID == "" {
		return text
	}
	snapshot, found := t.tasks.Get(t.current.taskID)
	if !found {
		return text
	}
	text += "任务现在的状态是“" + contract.StateLabels[snapshot.State] + "”。"
	if snapshot.Pause != nil {
		text += snapshot.Pause.Message
	}
	return text
}

// assist 员工接手后的一条回复。
func (t *turn) assist(ctx context.Context, conversation []agent.Message) error {
	config, err := t.manager.deps.LoadEmployee(t.current.employee)
	if err != nil {
		return err
	}
	tools, err := t.assistantTools(config)
	if err != nil {
		return err
	}
	text, err := t.converse(ctx, t.assistantSystem(config), conversation, tools)
	switch {
	case errors.Is(err, errModelUnavailable):
		if err := t.write(Message{Kind: KindNotice, Text: t.unavailableText()}); err != nil {
			return err
		}
	case err != nil:
		return err
	case text != "":
		if err := t.write(Message{Kind: KindAssistant, Text: text}); err != nil {
			return err
		}
	}
	if t.drafted {
		t.cards = append(t.cards, t.formCard())
	}
	return nil
}

func (t *turn) formCard() Card {
	return Card{
		Type: CardForm, Employee: t.current.employee, Draft: t.current.draft,
		Attachments: append([]Attachment{}, t.current.attachments...),
	}
}
