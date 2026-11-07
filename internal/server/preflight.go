// 提交前检查（Spec 0005）：发起任务时检查运行环境。检查放在服务端而不是引擎，
// 所以命令行和盲测不受影响；不通过时不写事件，也不进入准入校验。
package server

import (
	"fmt"
	"net/http"
	"strings"

	"digital-employee/internal/contract"
	"digital-employee/internal/settings"
)

// settingsEntry 让前端带人去设置页的入口。
type settingsEntry struct {
	Section string `json:"section"`
	Label   string `json:"label"`
}

// preflightResult GET /api/preflight 的响应；不通过时也是提交命令的响应。
type preflightResult struct {
	OK       bool           `json:"ok"`
	Error    string         `json:"error,omitempty"`
	Message  string         `json:"message,omitempty"`
	Settings *settingsEntry `json:"settings,omitempty"`
}

// preflight 判断默认连接是否有可用 Key。每次都读设置文件，保存 Key 后立即生效。
func (s *Server) preflight() preflightResult {
	if s.opts.Scripted {
		return preflightResult{OK: true}
	}
	models := settings.LoadModelSettings(s.opts.WorkspaceDir, s.settingsEnv())
	for _, connection := range models.Connections {
		if connection.Name == models.DefaultConnection && strings.TrimSpace(connection.APIKey) != "" {
			return preflightResult{OK: true}
		}
	}
	return preflightResult{
		Error:    contract.ErrModelNotConfigured,
		Message:  fmt.Sprintf("默认模型连接「%s」还没有配置 Key，任务无法运行。请先到设置里填写 Key。", models.DefaultConnection),
		Settings: &settingsEntry{Section: "models", Label: "去设置模型连接"},
	}
}

func (s *Server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.preflight())
}

// submitWouldProceed 这条提交命令引擎会不会接受。不合法的提交交给引擎拒绝并留痕，
// 否则审核人提交时看到的会是“去配 Key”，而不是越权提示。
func (s *Server) submitWouldProceed(taskID string, role contract.Role) bool {
	snapshot, found := s.engine().Get(taskID)
	if !found {
		return false
	}
	for _, action := range snapshot.Actions {
		if action.Command != contract.CmdSubmit {
			continue
		}
		for _, allowed := range action.Roles {
			if allowed == role {
				return true
			}
		}
	}
	return false
}
