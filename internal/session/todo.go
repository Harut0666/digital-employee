// 待办清单（todo_write）：Agent 自己的计划状态，整表替换。
//
// 参考 DeepSeek Harness 的 todo 工具，不复制代码。清单写进会话日志（新的消息类型 todo_written，
// 旧版本的读取方认不得它也不会出错：它不改变会话的其它状态），会话视图带最新的 todos。
// 它不是对工作区的操作，所以所有权限模式（含计划）都允许。
package session

import (
	"fmt"
	"strings"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

// 待办的状态。
const (
	TodoPending    = "pending"
	TodoInProgress = "in_progress"
	TodoCompleted  = "completed"
)

// TodoItem 待办清单里的一项。
type TodoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

// parseTodos 校验模型给的清单：内容非空、不重复、状态是三种之一。
func parseTodos(raw any) ([]TodoItem, error) {
	list, ok := raw.([]any)
	if !ok {
		return nil, toolFailure("invalid_todos", "todos 必须是数组，每项是 {content, status}")
	}
	items := make([]TodoItem, 0, len(list))
	seen := map[string]bool{}
	for index, entry := range list {
		fields, ok := entry.(map[string]any)
		if !ok {
			return nil, toolFailure("invalid_todos", "第 %d 项不是对象，应是 {content, status}", index+1)
		}
		content, _ := fields["content"].(string)
		content = strings.TrimSpace(content)
		status, _ := fields["status"].(string)
		switch {
		case content == "":
			return nil, toolFailure("invalid_todos", "第 %d 项的 content 不能为空", index+1)
		case seen[content]:
			return nil, toolFailure("invalid_todos", "待办内容重复：%q。每一项内容要不同", content)
		case status != TodoPending && status != TodoInProgress && status != TodoCompleted:
			return nil, toolFailure("invalid_todos", "第 %d 项的 status 必须是 pending、in_progress 或 completed，收到 %q", index+1, status)
		}
		seen[content] = true
		items = append(items, TodoItem{Content: content, Status: status})
	}
	return items, nil
}

func todoCounts(items []TodoItem) (pending, active, done int) {
	for _, item := range items {
		switch item.Status {
		case TodoPending:
			pending++
		case TodoInProgress:
			active++
		case TodoCompleted:
			done++
		}
	}
	return
}

// todoTool todo_write。写会话日志要走这一轮的 loop（同时推送给事件流，卡片才能实时更新）。
func (loop *turnLoop) todoTool() tool {
	return tool{
		spec: agent.ToolSpec{
			Name: contract.ToolTodoWrite,
			Description: "记录并更新待办清单，用来规划多步骤的工作并展示进度；一两步就能做完的小事不用。每次调用整表替换：todos 是完整的清单。" +
				"开工前先为每个具体步骤列一项；做的时候把正在做的标成 in_progress，做完立刻标成 completed。这只是你自己的计划，不对工作区做任何事，所有权限模式都可以用。",
			Parameters: map[string]any{"type": "object", "required": []any{"todos"}, "properties": map[string]any{
				"todos": map[string]any{
					"type": "array", "description": "完整的待办清单，替换之前的清单",
					"items": map[string]any{"type": "object", "required": []any{"content", "status"}, "properties": map[string]any{
						"content": map[string]any{"type": "string", "description": "这一项要做什么，一句简短的祈使句"},
						"status":  map[string]any{"type": "string", "enum": []any{TodoPending, TodoInProgress, TodoCompleted}, "description": "pending 待做 | in_progress 进行中 | completed 已完成"},
					}},
				},
			}},
		},
		run: func(arguments map[string]any) (any, error) {
			items, err := parseTodos(arguments["todos"])
			if err != nil {
				return nil, err
			}
			if err := loop.write(Message{Kind: KindTodoWritten, Todos: items}); err != nil {
				return nil, err
			}
			pending, active, done := todoCounts(items)
			return map[string]any{
				"message": fmt.Sprintf("已更新待办：%d 项待做，%d 项进行中，%d 项已完成", pending, active, done),
				"todos":   items,
			}, nil
		},
	}
}
