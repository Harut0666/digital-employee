// 会话接口：人和前台 Agent 的对话，业务都在 internal/session。
package server

import (
	"net/http"
	"path/filepath"
	"strings"

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
	// 工作区没有登记过，也查不到它的目录
	session.ErrWorkspaceUnknown: http.StatusNotFound,
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
		Dir:          s.opts.AppDir,
		Engine:       s.opts.Engine,
		Model:        s.opts.Model,
		Workspace:    s.workspaceRegistered,
		WorkspaceDir: s.workspaceDir,
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
		DefaultEmployee: func() string { return settings.Read(s.opts.AppDir).DefaultEmployee },
		Preflight: func() error {
			if result := s.preflight(); !result.OK {
				return &session.Error{Code: result.Error, Message: result.Message}
			}
			return nil
		},
		Execution: func() *contract.ExecutionSettings {
			execution := settings.Read(s.opts.AppDir).Execution
			return &execution
		},
		Knowledge: knowledge.WithTools(s.knowledge, nil),
		// 前台 Agent 的 read_file 复用工具客户端的文件解析
		Tools:  s.opts.ToolClient,
		Filing: filingSource{server: s},
		Now:    s.opts.Now,
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

// handleListSessions 列出会话；?workspaceId= 只列这个工作区的会话（不带时列全部工作区）。
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspaceId"))
	if workspaceID != "" && !s.workspaceRegistered(workspaceID) {
		writeSessionError(w, &session.Error{Code: session.ErrWorkspaceUnknown, Message: "工作区不存在：" + workspaceID})
		return
	}
	views, err := s.sessions.List(workspaceID)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views)
}

// handleCreateSession 在某个工作区里开新会话：请求体必须带 workspaceId（单目录模式可以省略）。
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if _, ok := roleOf(r); !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	body, err := readBodyJSON(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, err.Error()))
		return
	}
	workspaceID := ""
	if raw, present := body["workspaceId"]; present {
		text, ok := raw.(string)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "workspaceId 必须是字符串"))
			return
		}
		workspaceID = strings.TrimSpace(text)
	}
	if workspaceID == "" {
		workspaceID = s.onlyWorkspace()
		if workspaceID == "" {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "新建会话要指定工作区：请求体缺少 workspaceId"))
			return
		}
	}
	view, err := s.sessions.Create(workspaceID)
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
	id := r.PathValue("id")
	workspaceID, err := s.sessions.WorkspaceOf(id)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	reply, err := s.sessions.AddAttachments(id, role, uploads.attachments)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	// 对话里上传的文件同时写一份到 <工作区目录>/材料/，重名加序号；写不进去不影响这次上传，只记一条说明
	if err := s.sessions.SaveWorkspaceMaterials(workspaceID, uploads.attachments); err != nil {
		s.logf("warn", "上传的材料写进工作区目录失败：%v", err)
	}
	writeJSON(w, http.StatusOK, reply)
}

// handleListFolderFiles GET /api/sessions/{id}/folder-files：@ 选择用的目录文件列表。
func (s *Server) handleListFolderFiles(w http.ResponseWriter, r *http.Request) {
	// 只读列表：任何角色都能看目录里有什么；确认（POST）才只有发起人能做
	if _, ok := roleOf(r); !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	listing, err := s.sessions.FolderFiles(r.PathValue("id"))
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, listing)
}

// handleUseFolderFiles POST /api/sessions/{id}/folder-files：人确认（或 @ 选择）目录里的文件，复制进会话材料。
func (s *Server) handleUseFolderFiles(w http.ResponseWriter, r *http.Request) {
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
	raw, present := body["paths"]
	if !present {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "缺少 paths：要用的目录文件路径列表"))
		return
	}
	items, ok := raw.([]any)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "paths 必须是字符串数组"))
		return
	}
	paths := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "paths 必须是字符串数组"))
			return
		}
		paths = append(paths, text)
	}
	reply, err := s.sessions.UseFolderFiles(r.PathValue("id"), role, paths)
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
