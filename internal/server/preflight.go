// 提交前检查（Spec 0005、Spec 0006“会话模型”）：发起任务时检查运行环境。检查放在服务端而不是引擎，
// 所以命令行和盲测不受影响；不通过时不写事件，也不进入准入校验。
//
// 检查的是会话模型：它所在的连接有 Key，并且模型在连接的模型列表里。
package server

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"digital-employee/internal/contract"
	"digital-employee/internal/session"
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

// effectiveModels 设置页与提交前检查看到的同一份模型设置：连接与 Key 都来自设置文件（Spec 0007）。
func (s *Server) effectiveModels() contract.ModelSettings {
	return settings.LoadModelSettings(s.opts.AppDir)
}

// sessionModelReady 会话模型能不能用：连接在、连接有 Key、模型在连接的模型列表里（没配模型列表时只看 Key）。
func sessionModelReady(models contract.ModelSettings, ref contract.ModelRef) bool {
	if ref.Connection == "" {
		return false
	}
	connection, found := settings.FindConnection(models.Connections, ref.Connection)
	return found && strings.TrimSpace(connection.APIKey) != "" && (ref.Model == "" || len(connection.Models) == 0 || slices.Contains(connection.Models, ref.Model))
}

// checkModel 按会话模型检查（Spec 0006「模型调用」）：连接在、连接有 Key、模型在连接的模型列表里。
// 没选过模型时检查缺省模型（第一个有 Key 的连接的第一个模型）。
func checkModel(models contract.ModelSettings, ref contract.ModelRef) preflightResult {
	if ref.Connection == "" || ref.Model == "" {
		ref = models.Default
	}
	if ref.Connection == "" {
		return checkFirstConnection(models)
	}
	connection, found := settings.FindConnection(models.Connections, ref.Connection)
	switch {
	case !found:
		return modelNotReady(fmt.Sprintf("会话用的模型连接「%s」已不存在，任务无法运行。请重新选择模型。", ref.Connection))
	case strings.TrimSpace(connection.APIKey) == "":
		return modelNotReady(fmt.Sprintf("模型连接「%s」还没有配置 Key，任务无法运行。请先到设置里填写 Key。", connection.Name))
	case ref.Model != "" && len(connection.Models) > 0 && !slices.Contains(connection.Models, ref.Model):
		return modelNotReady(fmt.Sprintf("模型「%s」已不在连接「%s」的模型列表里，任务无法运行。请重新选择模型。", ref.Model, connection.Name))
	}
	return preflightResult{OK: true}
}

// checkFirstConnection 还没选过模型、也没有有 Key 的连接：没有任何连接时提示去添加，
// 有连接但都不可用时指出第一个连接缺 Key。
func checkFirstConnection(models contract.ModelSettings) preflightResult {
	if len(models.Connections) == 0 {
		return modelNotReady("还没有配置模型连接。任务需要模型来写判断步骤，请先到设置里添加一个连接、填写 Key 并选择模型。")
	}
	return modelNotReady(fmt.Sprintf("模型连接「%s」还没有配置 Key，任务无法运行。请先到设置里填写 Key。", models.Connections[0].Name))
}

func modelNotReady(message string) preflightResult {
	return preflightResult{Error: contract.ErrModelNotConfigured, Message: message, Settings: &modelSettingsEntry}
}

// modelSettingsEntry 模型没配好时带人去的设置入口。
var modelSettingsEntry = settingsEntry{Section: "models", Label: "去设置模型连接"}

// modelSettingsEntryOf 提交前检查的结果 → 会话模块返回的设置入口。
func modelSettingsEntryOf(result preflightResult) *session.SettingsEntry {
	if result.Settings == nil {
		return nil
	}
	return &session.SettingsEntry{Section: result.Settings.Section, Label: result.Settings.Label}
}

// taskModel 任务当前使用的会话模型（发起事件给出，之后随“模型已切换”更新）。
func (s *Server) taskModel(taskID string) (contract.ModelRef, bool) {
	snapshot, found := s.engine().Get(taskID)
	if !found || snapshot.Model == nil {
		return contract.ModelRef{}, false
	}
	return *snapshot.Model, true
}

// preflight 检查会话模型：taskID 非空时用任务的会话模型，否则用设置里最近选的模型。
// 模型为空时按第一个有 Key 的连接检查（新会话也沿用它）；连接没配模型列表时只看连接有没有 Key。
// 每次都读设置文件，保存 Key 后立即生效。演示模式（剧本模型）不连真实接口，视为通过。
func (s *Server) preflight(taskID string) preflightResult {
	if s.opts.Scripted {
		return preflightResult{OK: true}
	}
	models := s.effectiveModels()
	ref, ok := contract.ModelRef{}, false
	if taskID != "" {
		ref, ok = s.taskModel(taskID)
	}
	if !ok {
		// 设置里还没选过模型：新会话沿用缺省模型（第一个有 Key 的连接的第一个模型）
		ref = models.Default
	}
	return checkModel(models, ref)
}

// checkSessionModel 提交前的会话模型检查（新会话提交时）：会话模型所在的连接有 Key、模型在列表里。
// 会话里没选过模型时：没有任何连接就提示去添加连接；有连接但都不可用就指出缺 Key 的连接
// （新会话沿用它）；选过的模型不可用（设置页保存后连接被删、模型被去掉）时拦下，带上设置入口，
// 让人在输入框右下角换一个模型。
func (s *Server) checkSessionModel(ref contract.ModelRef) error {
	models := s.effectiveModels()
	if ref.Connection == "" {
		if models.Default.Connection != "" && models.Default.Model != "" {
			return nil
		}
		result := checkFirstConnection(models)
		return &session.Error{Code: result.Error, Message: result.Message, Settings: modelSettingsEntryOf(result)}
	}
	if sessionModelReady(models, ref) {
		return nil
	}
	result := checkModel(models, ref)
	return &session.Error{Code: result.Error, Message: result.Message, Settings: modelSettingsEntryOf(result)}
}

// handlePreflight 提交前检查：给了 taskId 时检查任务的会话模型（严格：模型不可用就拦），
// 否则按新会话检查（会话里还没选过模型时有 Code 连接就能开始）。
func (s *Server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(r.URL.Query().Get("taskId"))
	if taskID != "" {
		writeJSON(w, http.StatusOK, s.preflight(taskID))
		return
	}
	writeJSON(w, http.StatusOK, s.sessionPreflight())
}

// sessionPreflight 新会话的上限检查：会话模型（还没选过时是缺省模型）可用才通过。
func (s *Server) sessionPreflight() preflightResult {
	if s.opts.Scripted {
		return preflightResult{OK: true}
	}
	if err := s.checkSessionModel(s.effectiveModels().Default); err != nil {
		var typed *session.Error
		if errors.As(err, &typed) {
			return preflightResult{Error: typed.Code, Message: typed.Message, Settings: settingsEntryOfSession(typed.Settings)}
		}
	}
	return preflightResult{OK: true}
}

// settingsEntryOfSession 会话模块给出的设置入口 → 提交前检查响应里的设置入口。
func settingsEntryOfSession(entry *session.SettingsEntry) *settingsEntry {
	if entry == nil {
		return nil
	}
	return &settingsEntry{Section: entry.Section, Label: entry.Label}
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
