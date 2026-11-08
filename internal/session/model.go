// 会话模型（Spec 0006「模型列表与会话模型」）。
//
// 会话模型的取值顺序：会话里最后一次选择 → 设置的 lastModel → 第一个有 Key 的连接的第一个模型。
// 选择写进会话日志（“选择模型”事件），同时更新设置的 lastModel，新会话沿用它。
// 发起任务时把会话模型写进任务的“发起”事件；运行中改模型走 Engine.SetTaskModel，
// 写一条“模型已切换”任务事件，从下一个判断步骤开始生效。
package session

import (
	"slices"
	"strings"

	"digital-employee/internal/contract"
)

// ModelConnectionOption 会话里可以选的模型：一个有 Key 的连接和它列出的模型。
type ModelConnectionOption struct {
	Name   string   `json:"name"`
	Models []string `json:"models"`
}

// sameModel 两个模型引用是不是同一个。
func sameModel(ref *contract.ModelRef, connection, model string) bool {
	return ref != nil && ref.Connection == connection && ref.Model == model
}

// effectiveModel 会话当前用的模型（没选过时沿用设置的 lastModel）。
func (m *Manager) effectiveModel(current *record) contract.ModelRef {
	if current != nil && current.model != nil {
		return *current.model
	}
	return m.effectiveModelSettings().Default
}

// taskModel 会话绑定任务的当前模型：任务记下的会话模型（发起事件，之后随“模型已切换”更新）；
// 旧任务没记时用会话当前的模型。
func (m *Manager) taskModel(current *record) contract.ModelRef {
	if current.taskID != "" {
		if snapshot, found := m.deps.Engine.Get(current.taskID); found && snapshot.Model != nil {
			return *snapshot.Model
		}
	}
	return m.effectiveModel(current)
}

// currentModel 会话现在用的模型：绑定了任务时是任务的当前模型，否则是会话模型。
func (m *Manager) currentModel(current *record) *contract.ModelRef {
	model := m.effectiveModel(current)
	if current.taskID != "" {
		model = m.taskModel(current)
	}
	if model.Connection == "" && model.Model == "" {
		return nil
	}
	return &model
}

func (m *Manager) effectiveModelSettings() contract.ModelSettings {
	if m.deps.ModelSettings == nil {
		return contract.ModelSettings{}
	}
	return m.deps.ModelSettings()
}

// ModelOptions 会话里可选的模型：只列有 Key 的连接和它们列出的模型。
func (m *Manager) ModelOptions() []ModelConnectionOption {
	options := []ModelConnectionOption{}
	for _, connection := range m.effectiveModelSettings().Connections {
		if strings.TrimSpace(connection.APIKey) == "" || len(connection.Models) == 0 {
			continue
		}
		options = append(options, ModelConnectionOption{Name: connection.Name, Models: append([]string{}, connection.Models...)})
	}
	return options
}

// checkSelectable 只能选连接已列出的模型，连接还要有 Key。
func (m *Manager) checkSelectable(ref contract.ModelRef) error {
	connection := ""
	models := []string{}
	found := false
	for _, item := range m.effectiveModelSettings().Connections {
		if item.Name == ref.Connection {
			connection, models, found = strings.TrimSpace(item.APIKey), item.Models, true
			break
		}
	}
	switch {
	case !found:
		return failure(contract.ErrInvalidInput, "没有这个连接：%s", ref.Connection)
	case connection == "":
		return failure(contract.ErrInvalidInput, "连接「%s」还没有配置 Key，不能用它的模型", ref.Connection)
	case !slices.Contains(models, ref.Model):
		return failure(contract.ErrInvalidInput, "模型「%s」不在连接「%s」的模型列表里", ref.Model, ref.Connection)
	}
	return nil
}

// SelectModel 人在会话里选模型。写进会话日志，同时更新设置的 lastModel。
//
// 任务已在运行时，再写一条“模型已切换”任务事件（从下一个判断步骤生效，正在进行的调用不中断）；
// 任务已结束时只改会话记录，下一次发起的任务用它。
func (m *Manager) SelectModel(id string, role contract.Role, connection, model string) (*Reply, error) {
	if role != contract.RoleInitiator {
		return nil, failure(contract.ErrForbiddenRole, "只有发起人可以选模型")
	}
	ref := contract.ModelRef{Connection: strings.TrimSpace(connection), Model: strings.TrimSpace(model)}
	if ref.Connection == "" || ref.Model == "" {
		return nil, failure(contract.ErrInvalidInput, "选模型要同时给出连接和模型")
	}
	t, done, err := m.begin(id, role)
	if err != nil {
		return nil, err
	}
	defer done()
	if err := m.checkSelectable(ref); err != nil {
		return nil, err
	}
	if t.current.taskID != "" {
		if snapshot, found := m.deps.Engine.Get(t.current.taskID); found && contract.IsTerminal(snapshot.State) {
			return nil, failure(ErrSessionReadOnly, "任务已结束，模型不能再改；下一个任务请开新对话")
		}
	}
	if !sameModel(t.current.model, ref.Connection, ref.Model) {
		copied := ref
		if err := t.write(Message{Kind: kindModelSelected, Model: &copied, Selected: true}); err != nil {
			return nil, err
		}
		if err := m.saveLastModel(ref); err != nil {
			return nil, err
		}
		if t.current.taskID != "" {
			if switcher, ok := m.deps.Engine.(TaskModelSwitcher); ok {
				if err := switcher.SetTaskModel(t.current.taskID, ref); err != nil {
					return nil, err
				}
			}
			if err := t.write(Message{
				Kind:  KindNotice,
				Text:  "模型已切换为 " + ref.Connection + " / " + ref.Model + "，从下一个判断步骤开始生效。",
				Model: &copied,
			}); err != nil {
				return nil, err
			}
		}
	}
	return m.reply(t)
}

// TaskModelSwitcher 运行中换模型（引擎实现它）。
type TaskModelSwitcher interface {
	SetTaskModel(taskID string, ref contract.ModelRef) error
}

// saveLastModel 把这次选择记进设置；记不上不影响会话本身（会话日志已经写好了）。
func (m *Manager) saveLastModel(ref contract.ModelRef) error {
	if m.deps.SaveLastModel == nil {
		return nil
	}
	return m.deps.SaveLastModel(ref)
}
