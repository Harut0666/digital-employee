// 会话与任务一对多（Spec 0007）：一个会话先后运行多个任务，同一时刻最多一个未结束。
// 会话的“当前任务”是任务列表里最新的一个；状态、状态文字、等待谁、状态点、未读都按它算。
package session

import (
	"digital-employee/internal/contract"
)

// openTask 会话里还没结束（非终止状态）的任务；没有时返回 nil。
// 只可能是最新的那个：开新任务之前会检查，所以更早的任务都已经结束。
func (m *Manager) openTask(current *record) *contract.TaskSnapshot {
	if current == nil || current.taskID == "" {
		return nil
	}
	snapshot, found := m.deps.Engine.Get(current.taskID)
	if !found || contract.IsTerminal(snapshot.State) {
		return nil
	}
	return snapshot
}

// refuseNewTask 会话里已有未结束的任务时拒绝再开新任务，错误说明原因。
// Submit 和以后的 run_employee 共用这一句，Agent 会把它转告给人。
func (m *Manager) refuseNewTask(current *record) error {
	open := m.openTask(current)
	if open == nil {
		return nil
	}
	return failure(ErrSessionHasTask, "这个会话里的任务 %s 还没结束（现在是“%s”），等它交付或终止后才能开新任务。",
		open.ID, contract.StateLabels[open.State])
}

// runNumberOf 任务是会话里的第几次运行（从 1 开始）；不在这个会话里时返回 0。
func runNumberOf(current *record, taskID string) int {
	for index, id := range current.taskIDs {
		if id == taskID {
			return index + 1
		}
	}
	return 0
}
