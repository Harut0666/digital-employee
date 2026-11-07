// 接手后开放给前台 Agent 的工具。这里的工具只产生草稿和提议，不碰任务状态。
package session

import (
	"fmt"
	"sort"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

// fieldWidget 表单界面配置里某个字段的控件。
func fieldWidget(formUI map[string]any, field string) string {
	options, _ := formUI[field].(map[string]any)
	widget, _ := options["ui:widget"].(string)
	return widget
}

// humanOnlyField 这个字段只能由人决定：隐藏字段是暂停卡片上的选择，文件字段要人上传。
func humanOnlyField(formUI map[string]any, field string) bool {
	switch fieldWidget(formUI, field) {
	case "hidden", "file", "files":
		return true
	}
	return false
}

// fillableFields 表单里 Agent 能写进草稿的字段及其 Schema。
func fillableFields(config *contract.EmployeeConfig) map[string]map[string]any {
	properties, _ := config.FormSchema["properties"].(map[string]any)
	fields := map[string]map[string]any{}
	for name, raw := range properties {
		schema, ok := raw.(map[string]any)
		if ok && !humanOnlyField(config.FormUI, name) {
			fields[name] = schema
		}
	}
	return fields
}

// taskFacts 给模型看的任务现状。都是快照里的内容，本地路径不给。
func taskFacts(snapshot *contract.TaskSnapshot) map[string]any {
	deliverables := make([]string, 0, len(snapshot.Deliverables))
	for _, deliverable := range snapshot.Deliverables {
		deliverables = append(deliverables, string(deliverable.Kind))
	}
	attachments := make([]string, 0, len(snapshot.Attachments))
	for _, attachment := range snapshot.Attachments {
		attachments = append(attachments, attachment.Name)
	}
	actions := make([]map[string]any, 0, len(snapshot.Actions))
	for _, action := range snapshot.Actions {
		actions = append(actions, map[string]any{"label": action.Label, "roles": action.Roles})
	}
	facts := map[string]any{
		"taskId":       snapshot.ID,
		"employee":     snapshot.Employee,
		"state":        snapshot.State,
		"stateLabel":   contract.StateLabels[snapshot.State],
		"steps":        snapshot.Steps,
		"form":         snapshot.Form,
		"attachments":  attachments,
		"reworkRound":  snapshot.ReworkRound,
		"qc":           snapshot.QC,
		"deliverables": deliverables,
		"waitingFor":   snapshot.WaitingFor,
		"cardButtons":  actions,
	}
	if pause := snapshot.Pause; pause != nil {
		facts["pause"] = map[string]any{
			"code": pause.Code, "title": pause.Title, "message": pause.Message, "resolution": pause.Resolution,
		}
	}
	return facts
}

func (t *turn) queryTaskTool() tool {
	return tool{
		spec: agent.ToolSpec{
			Name:        contract.ToolQueryTask,
			Description: "只读查询本会话的任务：状态、暂停原因、步骤、检验记录、交付物，以及谁能点哪些按钮。回答任务问题前先调用它。",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		},
		run: func(arguments map[string]any) (any, error) {
			if asked, present := arguments["taskId"]; present && asked != t.current.taskID {
				return nil, denied("只能查询本会话的任务。")
			}
			snapshot, found := t.tasks.Get(t.current.taskID)
			if !found {
				return nil, fmt.Errorf("任务不存在：%s", t.current.taskID)
			}
			return taskFacts(snapshot), nil
		},
	}
}

// proposeCardTool 提议一张卡片。Agent 只选类型，卡片内容和按钮都由程序按任务现状填写。
func (t *turn) proposeCardTool() tool {
	return tool{
		spec: agent.ToolSpec{
			Name:        "propose_card",
			Description: "把一张流程卡片发给人。按钮由程序决定，你只能选卡片类型。人说要退回时，用 review 卡片带上退回意见草稿，由审核人确认。",
			Parameters: map[string]any{
				"type":     "object",
				"required": []any{"type"},
				"properties": map[string]any{
					"type":     map[string]any{"type": "string", "enum": []any{CardForm, CardPause, CardQC, CardReview}},
					"comment":  map[string]any{"type": "string", "description": "review 卡片的退回意见草稿"},
					"sections": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "退回意见涉及的章节"},
				},
			},
		},
		run: func(arguments map[string]any) (any, error) {
			kind, _ := arguments["type"].(string)
			if kind == CardForm {
				if t.current.taskID != "" {
					return nil, fmt.Errorf("任务已经提交，不能再发表单卡片")
				}
				t.drafted = true
				return map[string]any{"sent": kind}, nil
			}
			if t.current.taskID == "" {
				return nil, fmt.Errorf("还没有任务，不能发 %s 卡片", kind)
			}
			snapshot, found := t.tasks.Get(t.current.taskID)
			if !found {
				return nil, fmt.Errorf("任务不存在：%s", t.current.taskID)
			}
			card := Card{Type: kind, TaskID: snapshot.ID, AtSeq: snapshot.LastSeq}
			switch kind {
			case CardPause:
				if snapshot.State != contract.StateAwaitingInitiator && snapshot.State != contract.StateAwaitingOperator {
					return nil, fmt.Errorf("任务现在没有暂停")
				}
				card.Pause = snapshot.Pause
			case CardQC:
				if len(snapshot.QC) == 0 {
					return nil, fmt.Errorf("任务还没有检验记录")
				}
				card.Checks = snapshot.QC
			case CardReview:
				if snapshot.State != contract.StateAwaitingReview {
					return nil, fmt.Errorf("任务现在不在待审核")
				}
				card.Pause = snapshot.Pause
				card.Comment, _ = arguments["comment"].(string)
				if sections, ok := arguments["sections"].([]any); ok {
					for _, section := range sections {
						if text, ok := section.(string); ok {
							card.Sections = append(card.Sections, text)
						}
					}
				}
			default:
				return nil, fmt.Errorf("没有这种卡片：%s", kind)
			}
			t.cards = append(t.cards, card)
			return map[string]any{"sent": kind}, nil
		},
	}
}

func (t *turn) fillFormDraftTool(config *contract.EmployeeConfig) tool {
	fields := fillableFields(config)
	properties := map[string]any{}
	for name, schema := range fields {
		properties[name] = schema
	}
	return tool{
		spec: agent.ToolSpec{
			Name:        "fill_form_draft",
			Description: "把人说的信息写进表单草稿，只传要改的字段。草稿要由发起人检查，并在表单卡片上点击提交才算数。",
			Parameters:  map[string]any{"type": "object", "properties": properties},
		},
		run: func(arguments map[string]any) (any, error) {
			names := make([]string, 0, len(arguments))
			for name := range arguments {
				names = append(names, name)
			}
			sort.Strings(names)
			next := map[string]any{}
			for name, value := range t.current.draft {
				next[name] = value
			}
			for _, name := range names {
				if humanOnlyField(config.FormUI, name) {
					return nil, denied("字段 %s 只能由人操作，不能写进草稿。", name)
				}
				schema, ok := fields[name]
				if !ok {
					return nil, fmt.Errorf("表单里没有字段 %s", name)
				}
				if errs := agent.NewValidator(schema).Validate(arguments[name]); len(errs) > 0 {
					return nil, fmt.Errorf("字段 %s 不合法：%s", name, agent.ErrorText(errs))
				}
				next[name] = arguments[name]
			}
			if err := t.write(Message{Kind: kindDraftSet, Draft: next}); err != nil {
				return nil, err
			}
			t.drafted = true
			return map[string]any{"draft": next}, nil
		},
	}
}
