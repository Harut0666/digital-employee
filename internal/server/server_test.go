package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"digital-employee/internal/contract"
	"digital-employee/internal/settings"
)

// fakeEngine 测试用内存引擎：submit → awaiting_review，approve → delivered（带 docx 交付物）。
type fakeEngine struct {
	mu              sync.Mutex
	tasks           map[string]*contract.TaskSnapshot
	events          map[string][]contract.TaskEvent
	subs            map[string][]func(contract.TaskEvent)
	order           []string
	deliverablePath string
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{
		tasks: map[string]*contract.TaskSnapshot{}, events: map[string][]contract.TaskEvent{},
		subs: map[string][]func(contract.TaskEvent){},
	}
}

func (e *fakeEngine) emitLocked(id string, event contract.TaskEvent) {
	task := e.tasks[id]
	task.LastSeq++
	event.Seq = task.LastSeq
	event.TS = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	event.TaskID = id
	e.events[id] = append(e.events[id], event)
	for _, listener := range e.subs[id] {
		listener(event)
	}
}

func (e *fakeEngine) Create(input contract.CreateTaskInput) (*contract.TaskSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	id := fmt.Sprintf("task-%d", len(e.order)+1)
	form := input.Form
	if form == nil {
		form = map[string]any{}
	}
	attachments := []contract.AttachmentRef{}
	for _, attachment := range input.Attachments {
		attachments = append(attachments, contract.AttachmentRef{Name: attachment.Name, Path: attachment.SourcePath})
	}
	snapshot := contract.TaskSnapshot{
		ID: id, Employee: input.Employee, ConfigHash: "hash", State: contract.StateDraft,
		Form: form, Attachments: attachments, Steps: []contract.StepStatus{},
		Deliverables: []contract.DeliverableRef{},
	}
	e.tasks[id] = &snapshot
	e.order = append(e.order, id)
	e.emitLocked(id, contract.TaskEvent{Type: contract.EventTaskCreated, Employee: input.Employee, Form: form, WorkspaceID: input.WorkspaceID})
	copied := e.tasks[id]
	result := *copied
	return &result, nil
}

func (e *fakeEngine) Command(id contract.TaskID, command contract.TaskCommand, actor contract.Role) contract.CommandResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	task, ok := e.tasks[id]
	if !ok {
		return contract.CommandResult{OK: false, Error: contract.ErrNotFound, Message: "任务不存在"}
	}
	switch {
	case task.State == contract.StateDraft && command.Type == contract.CmdSubmit:
		e.emitLocked(id, contract.TaskEvent{Type: contract.EventStateChanged, From: contract.StateDraft, To: contract.StateAwaitingReview, Trigger: contract.TriggerSubmit, Actor: actor})
		task.State = contract.StateAwaitingReview
		pause := &contract.PauseReason{Code: contract.PauseAwaitingReview, Message: "等待审核"}
		task.Pause = pause
		copied := *task
		return contract.CommandResult{OK: true, Snapshot: &copied}
	case task.State == contract.StateAwaitingReview && command.Type == contract.CmdApprove:
		e.emitLocked(id, contract.TaskEvent{Type: contract.EventStateChanged, From: contract.StateAwaitingReview, To: contract.StateDelivered, Trigger: contract.TriggerApprove, Actor: actor})
		task.State = contract.StateDelivered
		task.Pause = nil
		if e.deliverablePath != "" {
			task.Deliverables = []contract.DeliverableRef{{Kind: contract.DeliverableDocx, Path: e.deliverablePath, SHA256: "sha"}}
			e.emitLocked(id, contract.TaskEvent{Type: contract.EventDeliverableCreated, Kind: string(contract.DeliverableDocx), Path: e.deliverablePath, SHA256: "sha"})
		}
		copied := *task
		return contract.CommandResult{OK: true, Snapshot: &copied}
	default:
		return contract.CommandResult{OK: false, Error: contract.ErrIllegalTransition, Message: "非法流转"}
	}
}

func (e *fakeEngine) Get(id contract.TaskID) (*contract.TaskSnapshot, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	task, ok := e.tasks[id]
	if !ok {
		return nil, false
	}
	copied := *task
	return &copied, true
}

func (e *fakeEngine) List() []contract.TaskSummary {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []contract.TaskSummary{}
	for index := len(e.order) - 1; index >= 0; index-- {
		task := e.tasks[e.order[index]]
		out = append(out, contract.TaskSummary{ID: task.ID, Employee: task.Employee, Title: task.Employee, State: task.State, UpdatedAt: "2026-01-01T00:00:00Z"})
	}
	return out
}

func (e *fakeEngine) Events(id contract.TaskID, after int64) ([]contract.TaskEvent, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.tasks[id]; !ok {
		return nil, fmt.Errorf("任务不存在：%s", id)
	}
	out := []contract.TaskEvent{}
	for _, event := range e.events[id] {
		if event.Seq > after {
			out = append(out, event)
		}
	}
	return out, nil
}

func (e *fakeEngine) Subscribe(id contract.TaskID, listener func(contract.TaskEvent)) func() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.subs[id] = append(e.subs[id], listener)
	index := len(e.subs[id]) - 1
	return func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		listeners := e.subs[id]
		for i := range listeners {
			if i == index {
				e.subs[id] = append(listeners[:i], listeners[i+1:]...)
				return
			}
		}
	}
}

func (e *fakeEngine) WaitForSettled(id contract.TaskID) (*contract.TaskSnapshot, error) {
	snapshot, ok := e.Get(id)
	if !ok {
		return nil, fmt.Errorf("任务不存在")
	}
	return snapshot, nil
}

func (e *fakeEngine) ResumeAll() error { return nil }

// ---- 测试辅助 ----

type testServer struct {
	server    *httptest.Server
	engine    *fakeEngine
	workspace string
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	workspace := t.TempDir()
	engine := newFakeEngine()
	srv := New(Options{
		Engine:       engine,
		AppDir:       workspace,
		WorkspaceDir: workspace,
		EmployeesDir: filepath.Join("..", "..", "employees"),
		WebDistDir:   "",
		Env:          map[string]string{},
		Logger:       NewLogger(LevelError, func(string, string) {}),
	})
	httpServer := httptest.NewServer(srv.Handler())
	t.Cleanup(httpServer.Close)
	return &testServer{server: httpServer, engine: engine, workspace: workspace}
}

func (s *testServer) request(t *testing.T, method, path string, body string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, s.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	// 像浏览器页面一样带同源标记（改变状态的接口要求它，见 origin.go）
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, data
}

func decodeJSON(t *testing.T, data []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("响应不是合法 JSON：%v（%s）", err, string(data))
	}
}

// ---- 测试 ----

func TestEmployeesEndpoint(t *testing.T) {
	ts := newTestServer(t)
	response, body := ts.request(t, http.MethodGet, "/api/employees", "", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d：%s", response.StatusCode, body)
	}
	var employees []map[string]any
	decodeJSON(t, body, &employees)
	found := false
	for _, employee := range employees {
		if employee["id"] == "weekly-report" {
			found = true
			if employee["name"] == "" || employee["formSchema"] == nil {
				t.Fatalf("员工信息不完整：%v", employee)
			}
		}
	}
	if !found {
		t.Fatalf("员工列表里没有 weekly-report：%s", body)
	}
}

func TestTasksFlow(t *testing.T) {
	ts := newTestServer(t)

	response, body := ts.request(t, http.MethodGet, "/api/tasks", "", nil)
	if response.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("任务列表应为空数组：%d %s", response.StatusCode, body)
	}

	// 缺角色 → 403
	response, body = ts.request(t, http.MethodPost, "/api/tasks", `{"employee":"weekly-report","form":{}}`, nil)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("缺角色应为 403：%d %s", response.StatusCode, body)
	}
	var errorBody map[string]any
	decodeJSON(t, body, &errorBody)
	if errorBody["error"] != contract.ErrForbiddenRole {
		t.Fatalf("错误码不对：%v", errorBody)
	}

	// 角色 + 合法员工 → 201
	response, body = ts.request(t, http.MethodPost, "/api/tasks", `{"employee":"weekly-report","form":{"period":{"year":2026,"week":44}}}`, map[string]string{"x-role": "initiator"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("建任务应为 201：%d %s", response.StatusCode, body)
	}
	var snapshot contract.TaskSnapshot
	decodeJSON(t, body, &snapshot)
	if snapshot.ID == "" || snapshot.State != contract.StateDraft {
		t.Fatalf("快照不对：%+v", snapshot)
	}

	// 非法员工 id → 400
	response, body = ts.request(t, http.MethodPost, "/api/tasks", `{"employee":"Bad_ID","form":{}}`, map[string]string{"x-role": "initiator"})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法员工 id 应为 400：%d %s", response.StatusCode, body)
	}

	// 读任务 / 不存在的任务
	response, body = ts.request(t, http.MethodGet, "/api/tasks/"+snapshot.ID, "", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("读任务应为 200：%d %s", response.StatusCode, body)
	}
	response, _ = ts.request(t, http.MethodGet, "/api/tasks/nope", "", nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在的任务应为 404：%d", response.StatusCode)
	}

	// 命令：submit
	response, body = ts.request(t, http.MethodPost, "/api/tasks/"+snapshot.ID+"/commands", `{"type":"submit"}`, map[string]string{"x-role": "initiator"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("submit 应为 200：%d %s", response.StatusCode, body)
	}
	var result contract.CommandResult
	decodeJSON(t, body, &result)
	if !result.OK || result.Snapshot == nil || result.Snapshot.State != contract.StateAwaitingReview {
		t.Fatalf("submit 结果不对：%+v", result)
	}

	// 非法流转 → 409
	response, body = ts.request(t, http.MethodPost, "/api/tasks/"+snapshot.ID+"/commands", `{"type":"submit"}`, map[string]string{"x-role": "initiator"})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("非法流转应为 409：%d %s", response.StatusCode, body)
	}

	// 未知命令 → 400
	response, _ = ts.request(t, http.MethodPost, "/api/tasks/"+snapshot.ID+"/commands", `{"type":"explode"}`, map[string]string{"x-role": "initiator"})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("未知命令应为 400：%d", response.StatusCode)
	}

	// 事件
	response, body = ts.request(t, http.MethodGet, "/api/tasks/"+snapshot.ID+"/events?after=0", "", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("事件应为 200：%d %s", response.StatusCode, body)
	}
	var events []contract.TaskEvent
	decodeJSON(t, body, &events)
	if len(events) < 2 || events[0].Type != contract.EventTaskCreated {
		t.Fatalf("事件序列不对：%s", body)
	}
	response, _ = ts.request(t, http.MethodGet, "/api/tasks/"+snapshot.ID+"/events?after=x", "", nil)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法 after 应为 400：%d", response.StatusCode)
	}

	// 没有交付物 → 404
	response, body = ts.request(t, http.MethodGet, "/api/tasks/"+snapshot.ID+"/deliverables/docx", "", nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("没有交付物应为 404：%d %s", response.StatusCode, body)
	}

	// approve 后有 docx 交付物可下载
	deliverable := filepath.Join(ts.workspace, "report.docx")
	if err := os.WriteFile(deliverable, []byte("docx"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts.engine.mu.Lock()
	ts.engine.deliverablePath = deliverable
	ts.engine.mu.Unlock()
	response, _ = ts.request(t, http.MethodPost, "/api/tasks/"+snapshot.ID+"/commands", `{"type":"approve"}`, map[string]string{"x-role": "reviewer"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("approve 应为 200：%d", response.StatusCode)
	}
	response, body = ts.request(t, http.MethodGet, "/api/tasks/"+snapshot.ID+"/deliverables/docx", "", nil)
	if response.StatusCode != http.StatusOK || string(body) != "docx" {
		t.Fatalf("交付物下载不对：%d %s", response.StatusCode, body)
	}
	if disposition := response.Header.Get("Content-Disposition"); !strings.Contains(disposition, "attachment") {
		t.Fatalf("缺少下载头：%q", disposition)
	}
}

func TestStaticHostingAndFallback(t *testing.T) {
	workspace := t.TempDir()
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dist, "index.html"), []byte("<html>index</html>"), 0o644)
	os.WriteFile(filepath.Join(dist, "assets", "app.js"), []byte("console.log(1)"), 0o644)
	srv := New(Options{
		Engine: newFakeEngine(), AppDir: workspace, WorkspaceDir: workspace,
		EmployeesDir: filepath.Join("..", "..", "employees"), WebDistDir: dist,
		Env: map[string]string{}, Logger: NewLogger(LevelError, func(string, string) {}),
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	fetch := func(path string) (int, string, string) {
		response, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(body), response.Header.Get("Content-Type")
	}
	if status, body, _ := fetch("/"); status != 200 || !strings.Contains(body, "index") {
		t.Fatalf("首页不对：%d %s", status, body)
	}
	if status, body, _ := fetch("/tasks/abc"); status != 200 || !strings.Contains(body, "index") {
		t.Fatalf("SPA 回退不对：%d %s", status, body)
	}
	if status, _, contentType := fetch("/assets/app.js"); status != 200 || !strings.Contains(contentType, "javascript") {
		t.Fatalf("静态脚本不对：%d %s", status, contentType)
	}
	if status, body, _ := fetch("/assets/missing.js"); status != 404 || !strings.Contains(body, "not_found") {
		t.Fatalf("缺失资源不应回退 index：%d %s", status, body)
	}
	if status, body, _ := fetch("/api/nope"); status != 404 || !strings.Contains(body, "not_found") {
		t.Fatalf("/api 未知路径应为 JSON 404：%d %s", status, body)
	}
}

func TestSSEReplaysAndStreams(t *testing.T) {
	ts := newTestServer(t)
	_, body := ts.request(t, http.MethodPost, "/api/tasks", `{"employee":"weekly-report","form":{}}`, map[string]string{"x-role": "initiator"})
	var snapshot contract.TaskSnapshot
	decodeJSON(t, body, &snapshot)

	response, err := http.Get(ts.server.URL + "/api/tasks/" + snapshot.ID + "/stream?after=0")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if contentType := response.Header.Get("Content-Type"); !strings.Contains(contentType, "text/event-stream") {
		t.Fatalf("SSE Content-Type 不对：%q", contentType)
	}
	reader := bufio.NewReader(response.Body)
	// SSE 是长连接：读到第一个空行为止（不等待更多事件）
	lines := []string{}
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			lines = append(lines, line)
		}
		if err != nil {
			t.Fatalf("读 SSE 帧失败：%v（已读 %q）", err, strings.Join(lines, ""))
		}
		if line == "\n" {
			break
		}
	}
	frame := strings.Join(lines, "")
	if !strings.Contains(frame, "event: task_created") || !strings.Contains(frame, "id: 1") || !strings.Contains(frame, "data: ") {
		t.Fatalf("SSE 帧格式不对：%q", frame)
	}
}

func TestSettingsAppRevisionLock(t *testing.T) {
	ts := newTestServer(t)
	response, body := ts.request(t, http.MethodGet, "/api/settings/app", "", nil)
	if response.StatusCode != 200 {
		t.Fatalf("GET /api/settings/app 失败：%d %s", response.StatusCode, body)
	}
	var view map[string]any
	decodeJSON(t, body, &view)
	if view["version"].(float64) != 2 || view["ok"] != true {
		t.Fatalf("设置视图不对：%s", body)
	}

	response, body = ts.request(t, http.MethodPatch, "/api/settings/app", `{"revision":0,"ui":{"theme":"dark"}}`, nil)
	if response.StatusCode != 200 {
		t.Fatalf("PATCH 应为 200：%d %s", response.StatusCode, body)
	}
	var patched map[string]any
	decodeJSON(t, body, &patched)
	if patched["revision"].(float64) != 1 {
		t.Fatalf("revision 应 +1：%s", body)
	}
	ui := patched["ui"].(map[string]any)
	if ui["theme"] != "dark" {
		t.Fatalf("patch 未生效：%s", body)
	}

	response, body = ts.request(t, http.MethodPatch, "/api/settings/app", `{"revision":0,"ui":{"theme":"light"}}`, nil)
	if response.StatusCode != 409 {
		t.Fatalf("旧 revision 应为 409：%d %s", response.StatusCode, body)
	}
	var conflict map[string]any
	decodeJSON(t, body, &conflict)
	if conflict["error"] != "revision_conflict" {
		t.Fatalf("冲突体不对：%s", body)
	}

	// settings.json 里未知顶层键应保留
	dir := ts.workspace
	if err := os.WriteFile(settings.Path(dir), []byte(`{"version":2,"revision":5,"future":"keep"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if written, err := settings.Update(dir, func(doc *contract.SettingsDocument) { doc.UI.Theme = "system" }); err != nil || written.Revision != 6 {
		t.Fatalf("update 保留未知键并 +1：%+v %v", written, err)
	}
}

func TestEmployeeConfigFlow(t *testing.T) {
	workspace := t.TempDir()
	// 复制仓库里的 weekly-report 到临时工作区，避免测试写仓库
	source := filepath.Join("..", "..", "employees", "weekly-report")
	target := filepath.Join(workspace, "employees", "weekly-report")
	if err := copyDir(source, target); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{
		Engine: newFakeEngine(), AppDir: workspace, WorkspaceDir: workspace,
		EmployeesDir: filepath.Join(workspace, "employees"),
		Env:          map[string]string{}, Logger: NewLogger(LevelError, func(string, string) {}),
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	get := func(path string) (int, []byte) {
		response, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, body
	}
	put := func(path, body string) (int, []byte) {
		request, _ := http.NewRequest(http.MethodPut, ts.URL+path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		return response.StatusCode, data
	}

	status, body := get("/api/employees/weekly-report/config")
	if status != 200 {
		t.Fatalf("GET config 失败：%d %s", status, body)
	}
	var config map[string]any
	decodeJSON(t, body, &config)
	if config["name"] != "经营周报员工" || config["config"] == nil {
		t.Fatalf("配置视图不对：%s", body)
	}
	// 配置哈希跟着员工配置走（唯一事实来源是 internal/config）；T44 改 interpret_feedback 措辞后已更新
	const goldenConfigHash = "3b95fb6248cda4508973337be0cef8b021c0197eb229f72eb515ed0954421512"
	if config["hash"] != goldenConfigHash {
		t.Fatalf("配置哈希与 TS 版不一致：%v", config["hash"])
	}
	files := config["files"].([]any)
	if len(files) < 5 {
		t.Fatalf("文件清单不完整：%s", body)
	}

	status, body = put("/api/employees/weekly-report/config/SKILL.md", `{"content":"# 改过\n"}`)
	if status != 200 {
		t.Fatalf("PUT config 失败：%d %s", status, body)
	}
	var saved map[string]any
	decodeJSON(t, body, &saved)
	if saved["version"] == nil {
		t.Fatalf("保存应返回版本：%s", body)
	}

	status, body = get("/api/employees/weekly-report/config/versions")
	if status != 200 {
		t.Fatalf("版本列表失败：%d %s", status, body)
	}
	var versions []map[string]any
	decodeJSON(t, body, &versions)
	if len(versions) != 1 {
		t.Fatalf("版本数应为 1：%s", body)
	}
	versionID := versions[0]["id"].(string)

	status, body = get("/api/employees/weekly-report/config/versions/" + versionID + "/diff")
	if status != 200 {
		t.Fatalf("diff 失败：%d %s", status, body)
	}
	var diff map[string]any
	decodeJSON(t, body, &diff)
	if diff["hasChanges"] != true {
		t.Fatalf("应检测到改动：%s", body)
	}

	// 结构化保存 workflow.yaml：应保留原文件注释（对应 TS 的 yaml Document 合并）
	status, body = get("/api/employees/weekly-report/config")
	var payload map[string]any
	decodeJSON(t, body, &payload)
	loaded, _ := payload["config"].(map[string]any)
	workflowData, err := json.Marshal(loaded["workflow"])
	if err != nil {
		t.Fatal(err)
	}
	status, body = put("/api/employees/weekly-report/config/workflow.yaml", `{"data":`+string(workflowData)+`}`)
	if status != 200 {
		t.Fatalf("结构化保存 workflow.yaml 失败：%d %s", status, body)
	}
	workflowText, _ := os.ReadFile(filepath.Join(target, "workflow.yaml"))
	if !strings.Contains(string(workflowText), "# 经营周报员工") {
		t.Fatalf("结构化保存应保留原文件注释：%s", string(workflowText)[:200])
	}

	// 不允许写白名单外的文件
	status, _ = put("/api/employees/weekly-report/config/prompts/evil.txt", `{"content":"x"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("非白名单文件应为 400：%d", status)
	}
}

func TestNormalizeConfigPath(t *testing.T) {
	legal := []string{"SKILL.md", "workflow.yaml", "qc.yaml", "form.schema.json", "form.ui.json", "frontdesk.md", "prompts/write_report.md", "schemas/write_report.json"}
	for _, path := range legal {
		if _, ok := normalizeConfigPath(path); !ok {
			t.Fatalf("合法路径被拒绝：%s", path)
		}
	}
	illegal := []string{"", "../settings.json", "/etc/passwd", `C:\Windows\win.ini`, "prompts/evil.txt", "prompts/../../x.md", "other/file.md", "SKILL.md/extra", "."}
	for _, path := range illegal {
		if _, ok := normalizeConfigPath(path); ok {
			t.Fatalf("非法路径被接受：%s", path)
		}
	}
}

func TestPrepareConfigTextValidation(t *testing.T) {
	content := "# 新岗位说明\n"
	text, errors := prepareConfigText("SKILL.md", configFileInput{Content: &content}, nil)
	if len(errors) > 0 || text != content {
		t.Fatalf("SKILL.md 原文应直接写入：%q %v", text, errors)
	}
	badWorkflow := "id: Bad_ID\nsteps: []\n"
	if _, errors := prepareConfigText("workflow.yaml", configFileInput{Content: &badWorkflow}, nil); len(errors) == 0 {
		t.Fatal("非法 workflow 应失败")
	}
	badJSON := "{not json"
	if _, errors := prepareConfigText("form.schema.json", configFileInput{Content: &badJSON}, nil); len(errors) == 0 {
		t.Fatal("非法 JSON 应失败")
	}
	badPurpose := `{"properties":{"x":{"type":"string","x-purpose":"param:unknown"}}}`
	if _, errors := prepareConfigText("form.schema.json", configFileInput{Content: &badPurpose}, nil); len(errors) == 0 {
		t.Fatal("不支持的 x-purpose 应失败")
	}
	if _, errors := prepareConfigText("prompts/a.md", configFileInput{Data: map[string]any{}, HasData: true}, nil); len(errors) == 0 {
		t.Fatal("提示词文件不接受 data")
	}
	// frontdesk 只能开放契约列出的只读工具
	if _, errors := prepareConfigText("frontdesk.md", configFileInput{Data: map[string]any{"body": "人设", "tools": []any{"shell"}}, HasData: true}, nil); len(errors) == 0 {
		t.Fatal("frontdesk 非法工具应失败")
	}
}

func TestOpenFolder(t *testing.T) {
	ts := newTestServer(t)
	// workspace 视图
	response, body := ts.request(t, http.MethodGet, "/api/settings/workspace", "", nil)
	if response.StatusCode != 200 {
		t.Fatalf("workspace 接口失败：%d %s", response.StatusCode, body)
	}
	var workspace map[string]any
	decodeJSON(t, body, &workspace)
	if workspace["workspaceDir"] != ts.workspace {
		t.Fatalf("workspaceDir 不对：%s", body)
	}
}

func TestMultipartCreateAndSupplement(t *testing.T) {
	ts := newTestServer(t)
	// 建任务：multipart 带一个附件
	boundary := "----test-boundary"
	fileContent := "hello-xlsx"
	body := strings.Join([]string{
		"--" + boundary,
		`Content-Disposition: form-data; name="employee"`,
		"",
		"weekly-report",
		"--" + boundary,
		`Content-Disposition: form-data; name="form"`,
		"",
		`{"period":{"year":2026,"week":44}}`,
		"--" + boundary,
		`Content-Disposition: form-data; name="attachments"; filename="数据.xlsx"`,
		"Content-Type: application/octet-stream",
		"",
		fileContent,
		"--" + boundary + "--",
		"",
	}, "\r\n")
	request, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/tasks", strings.NewReader(body))
	request.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	request.Header.Set("x-role", "initiator")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("multipart 建任务应为 201：%d %s", response.StatusCode, responseBody)
	}
	var snapshot contract.TaskSnapshot
	decodeJSON(t, responseBody, &snapshot)
	if len(snapshot.Attachments) != 1 || snapshot.Attachments[0].Name != "数据.xlsx" {
		t.Fatalf("附件落盘不对：%+v", snapshot.Attachments)
	}

	// multipart 命令：command 字段 + 新附件（解析成功即会被引擎受理/拒绝；这里用 submit）
	body = strings.Join([]string{
		"--" + boundary,
		`Content-Disposition: form-data; name="command"`,
		"",
		`{"type":"submit"}`,
		"--" + boundary,
		`Content-Disposition: form-data; name="attachments"; filename="纪要.docx"`,
		"Content-Type: application/octet-stream",
		"",
		"hello-docx",
		"--" + boundary + "--",
		"",
	}, "\r\n")
	request, _ = http.NewRequest(http.MethodPost, ts.server.URL+"/api/tasks/"+snapshot.ID+"/commands", strings.NewReader(body))
	request.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	request.Header.Set("x-role", "initiator")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	responseBody, _ = io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("multipart 命令应为 200：%d %s", response.StatusCode, responseBody)
	}
}

func TestSSELastEventIDPrefersHeader(t *testing.T) {
	ts := newTestServer(t)
	_, body := ts.request(t, http.MethodPost, "/api/tasks", `{"employee":"weekly-report","form":{}}`, map[string]string{"x-role": "initiator"})
	var snapshot contract.TaskSnapshot
	decodeJSON(t, body, &snapshot)
	ts.request(t, http.MethodPost, "/api/tasks/"+snapshot.ID+"/commands", `{"type":"submit"}`, map[string]string{"x-role": "initiator"})

	request, _ := http.NewRequest(http.MethodGet, ts.server.URL+"/api/tasks/"+snapshot.ID+"/stream?after=0", nil)
	request.Header.Set("Last-Event-ID", "1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(line) != "id: 2" {
		t.Fatalf("Last-Event-ID 应优先于 after（应从 seq2 续传）：%q", line)
	}
}

func TestSSEUnknownTask(t *testing.T) {
	ts := newTestServer(t)
	response, _ := ts.request(t, http.MethodGet, "/api/tasks/nope/stream", "", nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在任务的 SSE 应为 404：%d", response.StatusCode)
	}
}
