// 可用操作：由任务状态和暂停原因算出，是“谁能做什么”的单一事实来源。
// 命令校验和快照共用它；流转表只负责合法流转。结果不写入事件，所以旧工作区的任务也能显示。
package engine

import (
	"fmt"
	"strings"

	"digital-employee/internal/contract"
)

// commandLabels 命令的中文名：按钮文字和拒绝提示共用。
var commandLabels = map[string]string{
	contract.CmdSubmit:                 "提交",
	contract.CmdSupplement:             "补充材料",
	contract.CmdContinueWithoutMinutes: "无纪要继续",
	contract.CmdContinueWithPartial:    "按已有数据继续",
	contract.CmdRetry:                  "重试",
	contract.CmdTerminate:              "终止",
	contract.CmdApprove:                "通过",
	contract.CmdReturn:                 "退回",
}

func taskAction(command, style string, roles ...contract.Role) contract.TaskAction {
	return contract.TaskAction{Command: command, Label: commandLabels[command], Roles: roles, Style: style}
}

// availableActions 某状态和暂停原因下的全部操作。
func availableActions(state contract.TaskState, pause *contract.PauseReason) []contract.TaskAction {
	code := contract.PauseCode("")
	if pause != nil {
		code = pause.Code
	}
	switch state {
	case contract.StateDraft:
		abandon := taskAction(contract.CmdTerminate, contract.ActionDanger, contract.RoleInitiator)
		abandon.Label = "放弃草稿"
		return []contract.TaskAction{
			taskAction(contract.CmdSubmit, contract.ActionPrimary, contract.RoleInitiator),
			abandon,
		}
	case contract.StateAwaitingInitiator:
		actions := []contract.TaskAction{taskAction(contract.CmdSupplement, contract.ActionPrimary, contract.RoleInitiator)}
		switch code {
		case contract.PauseMinutesMissing, contract.PauseMinutesWeekMismatch:
			actions = append(actions, taskAction(contract.CmdContinueWithoutMinutes, contract.ActionDefault, contract.RoleInitiator))
		case contract.PausePeriodIncomplete:
			actions = append(actions, taskAction(contract.CmdContinueWithPartial, contract.ActionDefault, contract.RoleInitiator))
		}
		return append(actions, taskAction(contract.CmdTerminate, contract.ActionDanger, contract.RoleInitiator))
	case contract.StateAwaitingOperator:
		// 超限和质检用尽是高风险决定，只由操作员处理；工具故障发起人也能重试
		retryRoles := []contract.Role{contract.RoleOperator}
		if code == contract.PauseToolFailed {
			retryRoles = []contract.Role{contract.RoleInitiator, contract.RoleOperator}
		}
		return []contract.TaskAction{
			taskAction(contract.CmdRetry, contract.ActionPrimary, retryRoles...),
			taskAction(contract.CmdTerminate, contract.ActionDanger, contract.RoleOperator),
		}
	case contract.StateAwaitingReview:
		return []contract.TaskAction{
			taskAction(contract.CmdApprove, contract.ActionPrimary, contract.RoleReviewer),
			taskAction(contract.CmdReturn, contract.ActionDefault, contract.RoleReviewer),
			taskAction(contract.CmdTerminate, contract.ActionDanger, contract.RoleReviewer),
		}
	}
	return []contract.TaskAction{}
}

func commandsOf(actions []contract.TaskAction) []string {
	commands := make([]string, 0, len(actions))
	for _, action := range actions {
		commands = append(commands, action.Command)
	}
	return commands
}

// pauseOptions 进入某暂停时写进事件的命令清单。
func pauseOptions(state contract.TaskState, code contract.PauseCode) []string {
	return commandsOf(availableActions(state, &contract.PauseReason{Code: code}))
}

func findAction(actions []contract.TaskAction, command string) (contract.TaskAction, bool) {
	for _, action := range actions {
		if action.Command == command {
			return action, true
		}
	}
	return contract.TaskAction{}, false
}

// illegalCommandReason 当前状态或暂停原因下没有这个操作。
func illegalCommandReason(state contract.TaskState, command string) string {
	return fmt.Sprintf("任务当前处于「%s」，不能执行「%s」。", contract.StateLabels[state], commandLabels[command])
}

// forbiddenRoleReason 操作存在，但不归这个角色。
func forbiddenRoleReason(state contract.TaskState, action contract.TaskAction, actor contract.Role) string {
	allowed := make([]string, 0, len(action.Roles))
	for _, role := range action.Roles {
		allowed = append(allowed, contract.RoleLabels[role])
	}
	return fmt.Sprintf("%s无权在「%s」执行「%s」，应由%s执行。",
		contract.RoleLabels[actor], contract.StateLabels[state], action.Label, strings.Join(allowed, "、"))
}

// requestRoles 能发起操作的角色，也是 waitingFor 的显示顺序。
var requestRoles = []contract.Role{contract.RoleInitiator, contract.RoleReviewer, contract.RoleOperator}

// waitingForRoles 任务在等哪些角色：草稿和暂停态下，有操作可做的角色。
func waitingForRoles(state contract.TaskState, actions []contract.TaskAction) []contract.Role {
	roles := []contract.Role{}
	if state != contract.StateDraft && !contract.IsPaused(state) {
		return roles
	}
	for _, role := range requestRoles {
		for _, action := range actions {
			if actionAllows(action, role) {
				roles = append(roles, role)
				break
			}
		}
	}
	return roles
}

func actionAllows(action contract.TaskAction, role contract.Role) bool {
	for _, allowed := range action.Roles {
		if allowed == role {
			return true
		}
	}
	return false
}
