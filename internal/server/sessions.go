// 会话接口：人和前台 Agent 的对话，业务都在 internal/session。
package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
		Dir:       s.opts.AppDir,
		Engine:    s.opts.Engine,
		Model:     s.opts.Model,
		Workspace: s.workspaceRegistered,
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
	// 立即返回 202：回复在后台流式跑，通过会话事件流推送（Spec 0006、T40）
	accepted, err := s.sessions.Send(r.PathValue("id"), role, text)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, accepted)
}

// handleStopTurn 停止前台 Agent 正在进行的这一轮。
func (s *Server) handleStopTurn(w http.ResponseWriter, r *http.Request) {
	if _, ok := roleOf(r); !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	result, err := s.sessions.Stop(r.PathValue("id"))
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleSessionActivity 会话现在有没有一轮在跑，以及这一轮已经输出的内容。
func (s *Server) handleSessionActivity(w http.ResponseWriter, r *http.Request) {
	activity, err := s.sessions.Activity(r.PathValue("id"))
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, activity)
}

// handleSessionStream 会话事件流：文字增量、工具调用开始和结束、写入的流程卡片、一轮结束。
// 先推这一轮已经输出的部分（来晚的客户端也能接着看），再推新事件；心跳注释防止代理断开。
func (s *Server) handleSessionStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, internalError("当前连接不支持流式响应"))
		return
	}
	id := r.PathValue("id")
	activity, err := s.sessions.Activity(id)
	if err != nil {
		writeSessionError(w, err)
		return
	}

	var mu sync.Mutex
	queue := []session.AgentEvent{}
	notify := make(chan struct{}, 1)
	closed := false
	wake := func() {
		select {
		case notify <- struct{}{}:
		default:
		}
	}
	cancel, err := s.sessions.Subscribe(id, func(events []session.AgentEvent) {
		mu.Lock()
		if !closed {
			queue = append(queue, events...)
		}
		mu.Unlock()
		wake()
	})
	if err != nil {
		writeSessionError(w, err)
		return
	}
	defer func() {
		mu.Lock()
		closed = true
		mu.Unlock()
		cancel()
	}()

	header := w.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-cache, no-transform")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	catchUp := encodeAgentEvents(sessionCatchUp(activity)...)

	if _, err := w.Write([]byte(catchUp)); err != nil {
		return
	}
	flusher.Flush()

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-notify:
			mu.Lock()
			batch := queue
			queue = nil
			mu.Unlock()
			if len(batch) > 0 {

				if _, err := w.Write([]byte(encodeAgentEvents(batch...))); err != nil {
					return
				}
				flusher.Flush()
			}
		case <-ticker.C:
			if _, err := w.Write([]byte(": keep-alive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// sessionCatchUp 新订阅者先看到的一轮现状：已经输出的文字、工具调用和卡片。
func sessionCatchUp(activity *session.AgentActivity) []session.AgentEvent {
	if activity == nil || activity.Turn == 0 {
		return nil
	}
	events := []session.AgentEvent{{Turn: activity.Turn, Type: session.EventTurnStart}}
	if activity.Text != "" {
		events = append(events, session.AgentEvent{Turn: activity.Turn, Type: session.EventText, Text: activity.Text})
	}
	for _, tool := range activity.Tools {
		events = append(events, session.AgentEvent{Turn: activity.Turn, Type: session.EventToolStart, ID: tool.ID, Tool: tool.Tool, Args: tool.Args})
		if tool.Ended {
			events = append(events, session.AgentEvent{
				Turn: activity.Turn, Type: session.EventToolEnd, ID: tool.ID, Tool: tool.Tool, Args: tool.Args,
				Result: tool.Result, Reason: tool.Reason, IsErr: tool.IsErr,
			})
		}
	}
	for index := range activity.Cards {
		message := activity.Cards[index]
		events = append(events, session.AgentEvent{Turn: activity.Turn, Type: session.EventMessage, Msg: &message})
	}
	return events
}

// encodeAgentEvents 会话事件 → SSE 帧（event 名就是类型）。
func encodeAgentEvents(events ...session.AgentEvent) string {
	frame := strings.Builder{}
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			continue
		}
		frame.WriteString("event: " + event.Type + "\ndata: " + string(data) + "\n\n")
	}
	return frame.String()
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
	reply, err := s.sessions.Confirm(r.PathValue("id"), role, employee)
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
