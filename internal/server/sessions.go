// 会话接口：人和前台 Agent 的对话，业务都在 internal/session。
package server

import (
	"net/http"
	"path/filepath"

	"digital-employee/internal/contract"
	"digital-employee/internal/knowledge"
	"digital-employee/internal/session"
	"digital-employee/internal/settings"
)

// sessionErrorStatus 会话错误码 → HTTP 状态码（通用错误码走 statusOf）。
var sessionErrorStatus = map[string]int{
	session.ErrSessionHasTask:  http.StatusConflict,
	session.ErrSessionReadOnly: http.StatusConflict,
	session.ErrSessionBusy:     http.StatusConflict,
	session.ErrEmployeeMissing: http.StatusConflict,
	// 必需项不齐：会话的现状不允许开工
	session.ErrChecklistIncomplete: http.StatusConflict,
}

func writeSessionError(w http.ResponseWriter, err error) {
	code, message := session.CodeOf(err)
	if code == "" {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	if code == contract.ErrModelNotConfigured {
		writeJSON(w, statusOf(code), preflightResult{Error: code, Message: message, Settings: &modelSettingsEntry})
		return
	}
	status, ok := sessionErrorStatus[code]
	if !ok {
		status = statusOf(code)
	}
	writeAPIError(w, status, apiError(code, message))
}

// newSessions 组装会话模块：员工和设置都按需读，配置改动立即生效。
func (s *Server) newSessions() *session.Manager {
	return session.New(session.Deps{
		WorkspaceDir: s.opts.WorkspaceDir,
		Engine:       s.opts.Engine,
		Model:        s.opts.Model,
		Employees: func() ([]session.Employee, error) {
			listed, err := s.listEmployees()
			if err != nil {
				return nil, err
			}
			out := make([]session.Employee, 0, len(listed))
			for _, item := range listed {
				out = append(out, session.Employee{ID: item.ID, Name: item.Name, Description: item.Description})
			}
			return out, nil
		},
		LoadEmployee: func(id string) (*contract.EmployeeConfig, error) {
			return s.opts.LoadEmployee(filepath.Join(s.opts.EmployeesDir, id))
		},
		DefaultEmployee: func() string { return settings.Read(s.opts.WorkspaceDir).DefaultEmployee },
		Preflight: func() error {
			if result := s.preflight(); !result.OK {
				return &session.Error{Code: result.Error, Message: result.Message}
			}
			return nil
		},
		Execution: func() *contract.ExecutionSettings {
			execution := settings.Read(s.opts.WorkspaceDir).Execution
			return &execution
		},
		Knowledge: knowledge.WithTools(s.knowledge, nil),
		Filing:    filingSource{server: s},
		Now:       s.opts.Now,
	})
}

// sessionRoute 会话接口都要用到引擎，引擎没就绪时统一报错。
func (s *Server) sessionRoute(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.engine() == nil {
			writeJSON(w, http.StatusInternalServerError, internalError("任务引擎未就绪"))
			return
		}
		handler(w, r)
	}
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	views, err := s.sessions.List()
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if _, ok := roleOf(r); !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	view, err := s.sessions.Create()
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	role, ok := roleOf(r)
	if !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	body, err := readBodyJSON(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, err.Error()))
		return
	}
	text, _ := body["text"].(string)
	reply, err := s.sessions.Send(r.Context(), r.PathValue("id"), role, text)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

func (s *Server) handleConfirmEmployee(w http.ResponseWriter, r *http.Request) {
	role, ok := roleOf(r)
	if !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	body, err := readBodyJSON(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, err.Error()))
		return
	}
	employee, _ := body["employee"].(string)
	reply, err := s.sessions.Confirm(r.Context(), r.PathValue("id"), role, employee)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

func (s *Server) handleSessionAttachments(w http.ResponseWriter, r *http.Request) {
	role, ok := roleOf(r)
	if !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	if !isMultipart(r) {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "材料只能通过 multipart 上传"))
		return
	}
	form, err := readMultipart(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "multipart 请求解析失败"))
		return
	}
	uploads, err := saveUploads(form.File["attachments"])
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	defer uploads.cleanup()
	reply, err := s.sessions.AddAttachments(r.PathValue("id"), role, uploads.attachments)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

func (s *Server) handleSubmitSession(w http.ResponseWriter, r *http.Request) {
	role, ok := roleOf(r)
	if !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	body, err := readBodyJSON(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, err.Error()))
		return
	}
	var form map[string]any
	if raw, present := body["form"]; present {
		if form, ok = raw.(map[string]any); !ok {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "form 必须是 JSON 对象"))
			return
		}
	}
	reply, err := s.sessions.Submit(r.PathValue("id"), role, form)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

// handleSkipChecklistItem 发起人在清单上确认跳过一项。
func (s *Server) handleSkipChecklistItem(w http.ResponseWriter, r *http.Request) {
	role, ok := roleOf(r)
	if !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	reply, err := s.sessions.SkipItem(r.PathValue("id"), role, r.PathValue("item"))
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

// handleDraftFiling 入库卡片上请模型起草描述；模型不可用时回一条说明，由人手写。
func (s *Server) handleDraftFiling(w http.ResponseWriter, r *http.Request) {
	role, ok := roleOf(r)
	if !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	reply, err := s.sessions.DraftFiling(r.Context(), r.PathValue("id"), role)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	view, err := s.sessions.Get(r.PathValue("id"))
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
