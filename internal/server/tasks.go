// 任务相关端点（对应 packages/server/src/index.ts 的 /api/tasks 组）。
package server

import (
	"fmt"
	"net/http"

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
	var files []*multipartFileHeader
	if isMultipart(r) {
		form, err := readMultipart(r)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "multipart 请求解析失败"))
			return
		}
		employee, _ = formValue(form, "employee")
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
	writeJSON(w, http.StatusOK, snapshot)
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
		if check := s.preflight(); !check.OK {
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
	var ref *contract.DeliverableRef
	for index := range snapshot.Deliverables {
		if string(snapshot.Deliverables[index].Kind) == kind {
			ref = &snapshot.Deliverables[index]
			break
		}
	}
	if ref != nil && downloadResponse(w, r, ref.Path) {
		return
	}
	writeAPIError(w, http.StatusNotFound, apiError(contract.ErrNotFound, fmt.Sprintf("没有可下载的交付物：%s", kind)))
}
