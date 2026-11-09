// 开放给前台 Agent 的只读查询工具（query_task、知识库）和表单字段辅助。这里的工具不碰任务状态；
// 开工只有 run_employee 一条路（employee_form.go），它经过权限关口和发起人确认。
package session

import (
	"errors"
	"fmt"
	"slices"

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
			Description: "只读查询本会话的任务：状态、暂停原因、步骤、检验记录、交付物，以及谁能点哪些按钮。回答任务问题前先调用它；默认查最新的任务，也可以用 taskId 查本会话里更早的任务。",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"taskId": map[string]any{"type": "string", "description": "任务 ID；不填就查会话里最新的任务。会话先后运行过多个任务时，用它查更早的那个"},
			}},
		},
		run: func(arguments map[string]any) (any, error) {
			// 不指定时查会话的当前任务（最新的一个）；也可以查本会话里更早的任务
			taskID := t.current.taskID
			if asked, present := arguments["taskId"]; present {
				if id, _ := asked.(string); id != taskID {
					if !slices.Contains(t.current.taskIDs, id) {
						return nil, denied("只能查询本会话的任务。")
					}
					taskID = id
				}
			}
			snapshot, found := t.tasks.Get(taskID)
			if !found {
				return nil, fmt.Errorf("任务不存在：%s", taskID)
			}
			return taskFacts(snapshot), nil
		},
	}
}

// knowledgeTools 勾选了的知识库工具。读取结果由 call 写进会话日志。
func (t *turn) knowledgeTools(ticked map[string]bool) ([]tool, error) {
	client := t.manager.deps.Knowledge
	if client == nil {
		return nil, nil
	}
	descriptors, err := client.ListTools()
	if err != nil {
		return nil, err
	}
	tools := []tool{}
	for _, descriptor := range descriptors {
		if !ticked[descriptor.Name] {
			continue
		}
		name := descriptor.Name
		parameters, _ := descriptor.InputSchema.(map[string]any)
		tools = append(tools, tool{
			spec: agent.ToolSpec{Name: name, Description: descriptor.Description, Parameters: parameters},
			run: func(arguments map[string]any) (any, error) {
				result, err := client.Call(name, arguments)
				if err != nil {
					return nil, err
				}
				if !result.OK {
					return nil, errors.New(result.Error)
				}
				return result.Data, nil
			},
		})
	}
	return tools, nil
}
