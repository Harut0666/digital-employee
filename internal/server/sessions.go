// 会话接口：人和前台 Agent 的对话，业务都在 internal/session。
package server

import (
	"encoding/json"
	"errors"
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
		entry := modelSettingsEntry
		var typed *session.Error
		if errors.As(err, &typed) && typed.Settings != nil {
			entry = settingsEntry{Section: typed.Settings.Section, Label: typed.Settings.Label}
		}
		writeJSON(w, statusOf(code), preflightResult{Error: code, Message: message, Settings: &entry})
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
		WorkspaceDir: func(id string) string {
			dir, ok := s.workspaceDir(id)
			if !ok {
				return ""
			}
			return dir
		},
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
		// 提交前检查按会话模型（Spec 0006“会话模型”）
		Preflight: s.checkSessionModel,
		// 会话里选过的模型、新会话沿用的缺省模型：每次都读设置文件（设置页保存后立即生效）
		ModelSettings: func() contract.ModelSettings { return s.effectiveModels() },
		SaveLastModel: func(ref contract.ModelRef) error {
			_, err := settings.Update(s.opts.AppDir, func(doc *contract.SettingsDocument) {
				copied := ref
				doc.LastModel = &copied
			})
			return err
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

// handleListSessions 列出会话；?workspaceId= 只列这个工作区的会话（不带时列全部工作区）；
// ?archived=1 连归档的会话一起列（默认不列）。
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	workspaceID := strings.TrimSpace(query.Get("workspaceId"))
	if workspaceID != "" && !s.workspaceRegistered(workspaceID) {
		writeSessionError(w, &session.Error{Code: session.ErrWorkspaceUnknown, Message: "工作区不存在：" + workspaceID})
		return
	}
	views, err := s.sessions.List(workspaceID, truthy(query.Get("archived")))
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, views)
}

// handlePatchSession 人改会话：重命名（title）、归档或取消归档（archived）。
func (s *Server) handlePatchSession(w http.ResponseWriter, r *http.Request) {
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
	id := r.PathValue("id")
	var changed bool
	if raw, present := body["title"]; present {
		title, ok := raw.(string)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "title 必须是字符串"))
			return
		}
		if _, err := s.sessions.Rename(id, role, title); err != nil {
			writeSessionError(w, err)
			return
		}
		changed = true
	}
	if raw, present := body["archived"]; present {
		archived, ok := raw.(bool)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "archived 必须是 true 或 false"))
			return
		}
		if _, err := s.sessions.Archive(id, role, archived); err != nil {
			writeSessionError(w, err)
			return
		}
		changed = true
	}
	if !changed {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "没有要改的内容：可以改 title 或 archived"))
		return
	}
	summary, err := s.sessions.SummaryOf(id, role)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session": summary, "statusDot": summary.StatusDot})
}

// handleRecords 侧栏“记录”tab：所有工作区里已开工的任务，按状态分组（每组都列出来，空的也列）。
func (s *Server) handleRecords(w http.ResponseWriter, r *http.Request) {
	role, ok := roleOf(r)
	if !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	// 状态点按读的人的角色现算（记录本身缓存的是任务和会话，不带角色）
	records := s.sessions.Records()
	for index := range records {
		if records[index].SessionID != "" {
			records[index].StatusDot = s.sessions.DotFor(records[index].SessionID, role)
		}
	}
	writeJSON(w, http.StatusOK, s.recordsView(records))
}

// handleListWorkspaces GET /api/workspaces：每个工作区连同它的会话摘要，末尾是“未归属”分组。
// ?archived=1 连归档的会话一起列。
func (s *Server) handleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	role, ok := roleOf(r)
	if !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	writeJSON(w, http.StatusOK, s.workspaceView(role, truthy(r.URL.Query().Get("archived")), true))
}

// handleOpenWorkspaceFolder POST /api/workspaces/{id}/open：在资源管理器里打开工作区目录。
func (s *Server) handleOpenWorkspaceFolder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.workspaceMu.Lock()
	s.workspaceMu.RLock()
	dir, found := s.workspaces.dirs[id]
	s.workspaceMu.RUnlock()
	if !found {
		writeAPIError(w, http.StatusNotFound, apiError(contract.ErrNotFound, "工作区不存在："+id))
		return
	}
	if err := s.opts.OpenFolder(dir); err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError("打开工作区失败："+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": dir})
}

// sessionSummaryOf 会话详情 → 侧栏里的摘要；同时返回按发起人算的状态点。
// 写操作之后回一份最新摘要，界面不用再读一次工作区列表。
// truthy 查询参数里表示“是”的写法。
func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes":
		return true
	}
	return false
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

// handleSelectSessionModel PUT /api/sessions/{id}/model：人在输入框右下角选模型。
// 写进会话日志（“选择模型”事件），同时更新设置的 lastModel；任务运行中再写一条“模型已切换”任务事件。
func (s *Server) handleSelectSessionModel(w http.ResponseWriter, r *http.Request) {
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
	connection, _ := body["connection"].(string)
	model, _ := body["model"].(string)
	reply, err := s.sessions.SelectModel(r.PathValue("id"), role, connection, model)
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

// handleModelOptions GET /api/settings/models：会话里可以选的模型（只列有 Key 的连接和它们列出的模型）。
func (s *Server) handleModelOptions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.sessions.ModelOptions())
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
