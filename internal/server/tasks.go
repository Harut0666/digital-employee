// 任务相关端点（对应 packages/server/src/index.ts 的 /api/tasks 组）。
package server

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"digital-employee/internal/contract"
)

func (s *Server) engine() contract.Engine { return s.opts.Engine }

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	if s.engine() == nil {
		writeJSON(w, http.StatusInternalServerError, internalError("任务引擎未就绪"))
		return
	}
	tasks := s.engine().List()
	if tasks == nil {
		tasks = []contract.TaskSummary{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	if s.engine() == nil {
		writeJSON(w, http.StatusInternalServerError, internalError("任务引擎未就绪"))
		return
	}
	role, ok := roleOf(r)
	if !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}

	var employee string
	var rawForm any = "{}"
	var workspaceID string
	var files []*multipartFileHeader
	if isMultipart(r) {
		form, err := readMultipart(r)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "multipart 请求解析失败"))
			return
		}
		employee, _ = formValue(form, "employee")
		workspaceID, _ = formValue(form, "workspaceId")
		if value, present := formValue(form, "form"); present {
			rawForm = value
		}
		files = form.File["attachments"]
	} else {
		body, err := readBodyJSON(r)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, err.Error()))
			return
		}
		if value, ok := body["employee"].(string); ok {
			employee = value
		}
		if value, ok := body["workspaceId"].(string); ok {
			workspaceID = value
		}
		if value, present := body["form"]; present {
			rawForm = value
		}
	}

	if employee == "" {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "缺少 employee"))
		return
	}
	if !IsEmployeeID(employee) {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "员工 id 不合法：只允许小写字母、数字和连字符"))
		return
	}
	parsed, ok := parseJSONField(rawForm)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "form 必须是 JSON 对象"))
		return
	}
	formValueMap, ok := parsed.(map[string]any)
	if !ok || formValueMap == nil {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "form 必须是 JSON 对象"))
		return
	}

	// 任务属于发起时所在的工作区：请求里的 workspaceId，缺省取唯一的工作区（单目录模式）
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		workspaceID = s.onlyWorkspace()
	}
	if !s.workspaceRegistered(workspaceID) {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "建任务要指定工作区：缺少有效的 workspaceId"))
		return
	}

	uploads, err := saveUploads(files)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	defer uploads.cleanup()

	snapshot, err := s.engine().Create(contract.CreateTaskInput{
		Employee:    employee,
		Form:        formValueMap,
		Attachments: uploads.attachments,
		Actor:       role,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	writeJSON(w, http.StatusCreated, snapshot)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	if s.engine() == nil {
		writeJSON(w, http.StatusInternalServerError, internalError("任务引擎未就绪"))
		return
	}
	id := r.PathValue("id")
	snapshot, found := s.engine().Get(id)
	if !found {
		writeAPIError(w, http.StatusNotFound, apiError(contract.ErrNotFound, fmt.Sprintf("任务不存在：%s", id)))
		return
	}
	writeJSON(w, http.StatusOK, s.withDownloadNames(snapshot))
}

// withDownloadNames 给快照配上下载名，界面显示的文件名和下载到的一致。
func (s *Server) withDownloadNames(snapshot *contract.TaskSnapshot) *contract.TaskSnapshot {
	if len(snapshot.Deliverables) == 0 {
		return snapshot
	}
	decorated := *snapshot
	decorated.DownloadNames = map[string]string{}
	for index := range snapshot.Deliverables {
		ref := &snapshot.Deliverables[index]
		decorated.DownloadNames[string(ref.Kind)] = s.deliverableDownloadName(snapshot, ref)
	}
	return &decorated
}

func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	if s.engine() == nil {
		writeJSON(w, http.StatusInternalServerError, internalError("任务引擎未就绪"))
		return
	}
	role, ok := roleOf(r)
	if !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	id := r.PathValue("id")

	var raw any
	var files []*multipartFileHeader
	if isMultipart(r) {
		form, err := readMultipart(r)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "multipart 请求解析失败"))
			return
		}
		rawCommand, present := formValue(form, "command")
		if !present {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "command 不是合法的 JSON"))
			return
		}
		parsed, ok := parseJSONField(rawCommand)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "command 不是合法的 JSON"))
			return
		}
		raw = parsed
		files = form.File["attachments"]
	} else {
		body, err := readBodyJSON(r)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, err.Error()))
			return
		}
		raw = body
	}

	command, message := parseCommand(raw)
	if message != "" {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, message))
		return
	}
	if command.Type == contract.CmdSubmit && s.submitWouldProceed(id, role) {
		if check := s.preflight(id); !check.OK {
			writeJSON(w, statusOf(check.Error), check)
			return
		}
	}
	uploads, err := saveUploads(files)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	defer uploads.cleanup()
	if command.Type == contract.CmdSupplement && len(uploads.attachments) > 0 {
		command.Attachments = append(command.Attachments, uploads.attachments...)
	}

	result := s.engine().Command(id, command, role)
	status := http.StatusOK
	if !result.OK {
		status = statusOf(result.Error)
	}
	writeJSON(w, status, result)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.engine() == nil {
		writeJSON(w, http.StatusInternalServerError, internalError("任务引擎未就绪"))
		return
	}
	id := r.PathValue("id")
	after, ok := parseAfter(r.URL.Query().Get("after"))
	if !ok {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "after 必须是非负整数"))
		return
	}
	if _, found := s.engine().Get(id); !found {
		writeAPIError(w, http.StatusNotFound, apiError(contract.ErrNotFound, fmt.Sprintf("任务不存在：%s", id)))
		return
	}
	events, err := s.engine().Events(id, after)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	if events == nil {
		events = []contract.TaskEvent{}
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) handleDeliverable(w http.ResponseWriter, r *http.Request) {
	if s.engine() == nil {
		writeJSON(w, http.StatusInternalServerError, internalError("任务引擎未就绪"))
		return
	}
	id := r.PathValue("id")
	kind := r.PathValue("kind")
	snapshot, found := s.engine().Get(id)
	if !found {
		writeAPIError(w, http.StatusNotFound, apiError(contract.ErrNotFound, fmt.Sprintf("任务不存在：%s", id)))
		return
	}
	files, suffix, ok := versionFiles(snapshot, r.URL.Query().Get("version"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, apiError(contract.ErrNotFound, fmt.Sprintf("没有这一版交付物：%s", r.URL.Query().Get("version"))))
		return
	}
	var ref *contract.DeliverableRef
	for index := range files {
		if string(files[index].Kind) == kind {
			ref = &files[index]
			break
		}
	}
	if ref != nil && downloadResponse(w, r, ref.Path, withVersionSuffix(s.deliverableDownloadName(snapshot, ref), suffix)) {
		return
	}
	writeAPIError(w, http.StatusNotFound, apiError(contract.ErrNotFound, fmt.Sprintf("没有可下载的交付物：%s", kind)))
}

// versionFiles 按 ?version= 取某一版的文件；不带版本号时取当前交付物。
// 旧版本的下载名要标出版本（suffix），免得和最新一版混在一起；最新一版不标。
func versionFiles(snapshot *contract.TaskSnapshot, raw string) ([]contract.DeliverableRef, string, bool) {
	if raw == "" {
		return snapshot.Deliverables, "", true
	}
	number, err := strconv.Atoi(raw)
	if err != nil || number < 1 || number > len(snapshot.Versions) {
		return nil, "", false
	}
	if number == len(snapshot.Versions) {
		return snapshot.Versions[number-1].Files, "", true
	}
	return snapshot.Versions[number-1].Files, fmt.Sprintf("_第%d版", number), true
}

func withVersionSuffix(name, suffix string) string {
	if suffix == "" {
		return name
	}
	ext := filepath.Ext(name)
	return strings.TrimSuffix(name, ext) + suffix + ext
}
