// 工作区切换：从 HTTP 接口验证，配真实引擎、真实工具和剧本模型，工作区和应用级目录都是临时目录。
package app_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"digital-employee/internal/agent"
	"digital-employee/internal/app"
	"digital-employee/internal/contract"
	"digital-employee/internal/engine"
	"digital-employee/internal/tools"
)

// switchApp 一个运行中的应用。
type switchApp struct {
	t       *testing.T
	baseURL string
	running *app.Running
	// engines 按创建顺序记下的引擎（每次打开工作区建一个）
	engines []contract.Engine
}

type switchAppConfig struct {
	env  map[string]string
	gate *toolGate
}

type switchAppOption func(*switchAppConfig)

// withAppEnv 应用看到的环境变量（DE_APP_DIR 由装置补上）。
func withAppEnv(env map[string]string) switchAppOption {
	return func(config *switchAppConfig) { config.env = env }
}

// freePort 找一个空闲端口。
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

// startSwitchApp 在 workspace 上启动应用，应用级目录是 appDir。
func startSwitchApp(t *testing.T, appDir, workspace string, options ...switchAppOption) *switchApp {
	t.Helper()
	config := switchAppConfig{}
	for _, option := range options {
		option(&config)
	}
	env := map[string]string{}
	for key, value := range config.env {
		env[key] = value
	}
	env["DE_APP_DIR"] = appDir
	a := &switchApp{t: t}
	var mu sync.Mutex
	running, err := app.Start(app.Options{
		RepoRoot:     filepath.Join("..", ".."),
		WorkspaceDir: workspace,
		Port:         freePort(t),
		PortAttempts: 1,
		Hostname:     "127.0.0.1",
		NoOpen:       true,
		Env:          env,
		LogLine:      func(string) {},
		ScriptFile:   "scripted",
		ToolFactory: func() (contract.ToolClient, error) {
			var client contract.ToolClient = tools.NewRegistry()
			if config.gate != nil {
				client = gatedTools{ToolClient: client, gate: config.gate}
			}
			return client, nil
		},
		EngineFactory: func(deps app.EngineDeps) (contract.Engine, error) {
			created, err := engine.New(engine.Deps{
				WorkspaceDir: deps.WorkspaceDir,
				EmployeesDir: deps.EmployeesDir,
				ToolClient:   deps.ToolClient,
				Model:        agent.NewScriptedModelProvider(agent.ModelScript{}, agent.ScriptedProviderOptions{}),
				Faults:       deps.Faults,
			})
			if err == nil {
				mu.Lock()
				a.engines = append(a.engines, created)
				mu.Unlock()
			}
			return created, err
		},
	})
	if err != nil {
		t.Fatalf("启动应用失败：%v", err)
	}
	t.Cleanup(func() { _ = running.Close() })
	a.baseURL, a.running = running.URL, running
	return a
}

// call 发一个 JSON 请求，返回状态码和响应体。
func (a *switchApp) call(method, path string, body any) (int, []byte) {
	a.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			a.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, a.baseURL+path, reader)
	if err != nil {
		a.t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("x-role", string(contract.RoleOperator))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		a.t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		a.t.Fatal(err)
	}
	return response.StatusCode, data
}

func decodeAs[T any](t *testing.T, data []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("响应不是预期的 JSON：%v\n%s", err, data)
	}
	return value
}

// workspaceItem 和 workspaceList 是 /api/workspaces 的响应形状。
type workspaceItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type workspaceList struct {
	Current    string          `json:"current"`
	ReadOnly   bool            `json:"readOnly"`
	Reason     string          `json:"reason"`
	Workspaces []workspaceItem `json:"workspaces"`
}

// workspaces GET /api/workspaces。
func (a *switchApp) workspaces() workspaceList {
	a.t.Helper()
	status, data := a.call(http.MethodGet, "/api/workspaces", nil)
	if status != http.StatusOK {
		a.t.Fatalf("读取工作区列表失败：%d %s", status, data)
	}
	return decodeAs[workspaceList](a.t, data)
}

func TestWorkspaceListShowsStartingWorkspaceAsCurrent(t *testing.T) {
	appDir := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "华东区")
	a := startSwitchApp(t, appDir, workspace)

	list := a.workspaces()
	if list.ReadOnly {
		t.Fatalf("没有用环境变量指定工作区时不应只读：%+v", list)
	}
	if len(list.Workspaces) != 1 {
		t.Fatalf("应只有启动时的工作区：%+v", list)
	}
	only := list.Workspaces[0]
	if only.Name != "华东区" || only.Path != workspace || list.Current != only.ID {
		t.Fatalf("启动时的工作区应是当前工作区，名称取目录名：%+v", list)
	}
}

// createDraftTask 发起人用公开样例建一个第 44 周的草稿任务，返回任务 ID。
func (a *switchApp) createDraftTask() string {
	a.t.Helper()
	buffer := &bytes.Buffer{}
	writer := multipart.NewWriter(buffer)
	_ = writer.WriteField("employee", "weekly-report")
	_ = writer.WriteField("form", `{"period":{"year":2026,"week":44}}`)
	for _, name := range []string{"销售明细.xlsx", "周会纪要_2026W44.docx"} {
		content, err := os.ReadFile(filepath.Join("..", "..", "samples", "scenario-a", name))
		if err != nil {
			a.t.Fatal(err)
		}
		part, err := writer.CreateFormFile("attachments", name)
		if err != nil {
			a.t.Fatal(err)
		}
		_, _ = part.Write(content)
	}
	if err := writer.Close(); err != nil {
		a.t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, a.baseURL+"/api/tasks", buffer)
	if err != nil {
		a.t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("x-role", string(contract.RoleInitiator))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		a.t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusCreated {
		a.t.Fatalf("建任务失败：%d %s", response.StatusCode, data)
	}
	return decodeAs[contract.TaskSnapshot](a.t, data).ID
}

// taskIDs GET /api/tasks 里的任务 ID。
func (a *switchApp) taskIDs() []string {
	a.t.Helper()
	status, data := a.call(http.MethodGet, "/api/tasks", nil)
	if status != http.StatusOK {
		a.t.Fatalf("读取任务列表失败：%d %s", status, data)
	}
	ids := []string{}
	for _, item := range decodeAs[[]contract.TaskSummary](a.t, data) {
		ids = append(ids, item.ID)
	}
	return ids
}

// employeeNames GET /api/employees 里的员工名称。
func (a *switchApp) employeeNames() []string {
	a.t.Helper()
	status, data := a.call(http.MethodGet, "/api/employees", nil)
	if status != http.StatusOK {
		a.t.Fatalf("读取员工失败：%d %s", status, data)
	}
	names := []string{}
	for _, item := range decodeAs[[]struct {
		Name string `json:"name"`
	}](a.t, data) {
		names = append(names, item.Name)
	}
	return names
}

// connectionNames GET /api/settings 里的模型连接名。
func (a *switchApp) connectionNames() []string {
	a.t.Helper()
	status, data := a.call(http.MethodGet, "/api/settings", nil)
	if status != http.StatusOK {
		a.t.Fatalf("读取设置失败：%d %s", status, data)
	}
	view := decodeAs[struct {
		Connections []struct {
			Name string `json:"name"`
		} `json:"connections"`
	}](a.t, data)
	names := []string{}
	for _, item := range view.Connections {
		names = append(names, item.Name)
	}
	return names
}

// createWorkspace POST /api/workspaces，要求成功。
func (a *switchApp) createWorkspace(name string) workspaceItem {
	a.t.Helper()
	status, data := a.call(http.MethodPost, "/api/workspaces", map[string]any{"name": name})
	if status != http.StatusCreated {
		a.t.Fatalf("新建工作区失败：%d %s", status, data)
	}
	return decodeAs[struct {
		Workspace workspaceItem `json:"workspace"`
	}](a.t, data).Workspace
}

// switchReply 切换接口的响应。
type switchReply struct {
	status  int
	OK      bool     `json:"ok"`
	Error   string   `json:"error"`
	Message string   `json:"message"`
	Reload  bool     `json:"reload"`
	TaskIDs []string `json:"taskIds"`
}

// trySwitch POST /api/workspaces/{id}/switch，不判断结果。
func (a *switchApp) trySwitch(id string) switchReply {
	a.t.Helper()
	status, data := a.call(http.MethodPost, "/api/workspaces/"+id+"/switch", map[string]any{})
	reply := decodeAs[switchReply](a.t, data)
	reply.status = status
	return reply
}

// switchTo 切换工作区，要求成功并通知前端重新加载。
func (a *switchApp) switchTo(id string) {
	a.t.Helper()
	reply := a.trySwitch(id)
	if reply.status != http.StatusOK || !reply.OK || !reply.Reload {
		a.t.Fatalf("切换工作区失败：%+v", reply)
	}
}

func TestSwitchingMovesSettingsEmployeesAndTasksToTheChosenWorkspace(t *testing.T) {
	appDir := t.TempDir()
	first := filepath.Join(t.TempDir(), "华东区")
	a := startSwitchApp(t, appDir, first)
	original := a.list0()

	// 旧工作区：一个草稿任务、一条自己的模型连接、改过名字的员工
	taskID := a.createDraftTask()
	status, data := a.call(http.MethodPut, "/api/settings/connections/华东专线", map[string]any{
		"baseUrl": "http://127.0.0.1:9/v1", "models": []string{"m1"},
	})
	if status != http.StatusOK {
		t.Fatalf("保存连接失败：%d %s", status, data)
	}
	workflow := filepath.Join(first, "employees", "weekly-report", "workflow.yaml")
	raw, err := os.ReadFile(workflow)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workflow, []byte(strings.Replace(string(raw), "name: 经营周报员工", "name: 华东周报员工", 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	created := a.createWorkspace("华南区")
	a.switchTo(created.ID)

	if list := a.workspaces(); list.Current != created.ID || len(list.Workspaces) != 2 {
		t.Fatalf("切换后当前工作区应是新建的：%+v", list)
	}
	if ids := a.taskIDs(); len(ids) != 0 {
		t.Fatalf("新工作区不应有任务：%v", ids)
	}
	if names := a.connectionNames(); slices.Contains(names, "华东专线") {
		t.Fatalf("新工作区的设置不应有旧工作区的连接：%v", names)
	}
	if names := a.employeeNames(); !slices.Equal(names, []string{"经营周报员工"}) {
		t.Fatalf("新工作区的员工应是默认配置：%v", names)
	}

	a.switchTo(original.ID)
	if ids := a.taskIDs(); !slices.Equal(ids, []string{taskID}) {
		t.Fatalf("切回来旧任务应还在：%v", ids)
	}
	if names := a.connectionNames(); !slices.Contains(names, "华东专线") {
		t.Fatalf("切回来旧连接应还在：%v", names)
	}
	if names := a.employeeNames(); !slices.Equal(names, []string{"华东周报员工"}) {
		t.Fatalf("切回来改过的员工应还在：%v", names)
	}
}

func TestWorkspaceListOutlivesTheProcess(t *testing.T) {
	appDir := t.TempDir()
	a := startSwitchApp(t, appDir, filepath.Join(t.TempDir(), "华东区"))
	created := a.createWorkspace("华南区")
	a.switchTo(created.ID)
	if err := a.running.Close(); err != nil {
		t.Fatal(err)
	}

	// 重启时不指定工作区：从应用级的工作区列表里接着用上次的
	restarted := startSwitchApp(t, appDir, "")
	list := restarted.workspaces()
	if list.Current != created.ID || len(list.Workspaces) != 2 {
		t.Fatalf("重启后应接着用上次切到的工作区：%+v", list)
	}
	for _, item := range list.Workspaces {
		if strings.HasPrefix(appDir, item.Path) {
			t.Fatalf("工作区列表文件不应落在任何工作区里：%s 在 %s 下", appDir, item.Path)
		}
	}
}

// list0 当前列表里的第一个工作区。
func (a *switchApp) list0() workspaceItem {
	a.t.Helper()
	return a.workspaces().Workspaces[0]
}

// toolGate 让某个工具的第一次调用停住，直到放行。
type toolGate struct {
	tool    string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newToolGate(tool string) *toolGate {
	return &toolGate{tool: tool, entered: make(chan struct{}), release: make(chan struct{})}
}

type gatedTools struct {
	contract.ToolClient
	gate *toolGate
}

func (g gatedTools) Call(name string, args any) (contract.ToolCallResult, error) {
	if name == g.gate.tool {
		first := false
		g.gate.once.Do(func() { first = true })
		if first {
			close(g.gate.entered)
			<-g.gate.release
		}
	}
	return g.ToolClient.Call(name, args)
}

// withToolGate 每个工作区的工具都经过这道闸。
func withToolGate(gate *toolGate) switchAppOption {
	return func(config *switchAppConfig) { config.gate = gate }
}

// submitTask 发起人提交任务。
func (a *switchApp) submitTask(taskID string) {
	a.t.Helper()
	request, err := http.NewRequest(http.MethodPost, a.baseURL+"/api/tasks/"+taskID+"/commands", strings.NewReader(`{"type":"submit"}`))
	if err != nil {
		a.t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-role", string(contract.RoleInitiator))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		a.t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(response.Body)
		a.t.Fatalf("提交任务失败：%d %s", response.StatusCode, data)
	}
}

// taskState GET /api/tasks/{id} 的状态。
func (a *switchApp) taskState(taskID string) contract.TaskState {
	a.t.Helper()
	status, data := a.call(http.MethodGet, "/api/tasks/"+taskID, nil)
	if status != http.StatusOK {
		a.t.Fatalf("读取任务失败：%d %s", status, data)
	}
	return decodeAs[contract.TaskSnapshot](a.t, data).State
}

// waitUntilIdle 等任务离开准入校验、执行中和质检中。
func (a *switchApp) waitUntilIdle(taskID string) contract.TaskState {
	a.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		state := a.taskState(taskID)
		if state != contract.StateAdmission && state != contract.StateRunning && state != contract.StateQC {
			return state
		}
		if time.Now().After(deadline) {
			a.t.Fatalf("任务一直停在 %s", state)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestSwitchIsRefusedWhileATaskIsRunning(t *testing.T) {
	gate := newToolGate("validate_submission")
	a := startSwitchApp(t, t.TempDir(), filepath.Join(t.TempDir(), "华东区"), withToolGate(gate))
	original := a.list0()
	created := a.createWorkspace("华南区")

	taskID := a.createDraftTask()
	a.submitTask(taskID)
	<-gate.entered // 任务停在准入校验的工具调用里

	reply := a.trySwitch(created.ID)
	if reply.status != http.StatusConflict || reply.Error != "workspace_busy" || !slices.Equal(reply.TaskIDs, []string{taskID}) {
		t.Fatalf("有任务在准入校验时应拒绝切换并指出是哪个任务：%+v", reply)
	}
	if !strings.Contains(reply.Message, "任务") {
		t.Fatalf("拒绝时应说明原因：%q", reply.Message)
	}
	if list := a.workspaces(); list.Current != original.ID {
		t.Fatalf("被拒绝后仍应留在原工作区：%+v", list)
	}

	close(gate.release)
	a.waitUntilIdle(taskID)
	// 原工作区的任务照常推进，没有被切换打断
	if ids := a.taskIDs(); !slices.Equal(ids, []string{taskID}) {
		t.Fatalf("原工作区的任务应还在：%v", ids)
	}
	a.switchTo(created.ID)
}

func TestOldEngineStopsWritingAfterSwitch(t *testing.T) {
	a := startSwitchApp(t, t.TempDir(), filepath.Join(t.TempDir(), "华东区"))
	original := a.list0()
	taskID := a.createDraftTask()
	eventsBefore := a.eventCount(taskID)
	oldEngine := a.engines[len(a.engines)-1]

	a.switchTo(a.createWorkspace("华南区").ID)

	// 还拿着旧引擎的调用方（比如切换前发出的请求）不能再写旧工作区
	if result := oldEngine.Command(taskID, contract.TaskCommand{Type: contract.CmdSubmit}, contract.RoleInitiator); result.OK {
		t.Fatalf("切换后旧引擎不应再接受命令：%+v", result)
	}
	if _, err := oldEngine.Create(contract.CreateTaskInput{Employee: "weekly-report", Actor: contract.RoleInitiator}); err == nil {
		t.Fatal("切换后旧引擎不应再建任务")
	}

	a.switchTo(original.ID)
	if state := a.taskState(taskID); state != contract.StateDraft {
		t.Fatalf("旧任务应仍是草稿：%s", state)
	}
	if after := a.eventCount(taskID); after != eventsBefore {
		t.Fatalf("旧引擎不应再写事件：切换前 %d 条，切回来 %d 条", eventsBefore, after)
	}
	if ids := a.taskIDs(); !slices.Equal(ids, []string{taskID}) {
		t.Fatalf("旧工作区不应多出任务：%v", ids)
	}
}

// eventCount GET /api/tasks/{id}/events 的事件数。
func (a *switchApp) eventCount(taskID string) int {
	a.t.Helper()
	status, data := a.call(http.MethodGet, "/api/tasks/"+taskID+"/events", nil)
	if status != http.StatusOK {
		a.t.Fatalf("读取事件失败：%d %s", status, data)
	}
	return len(decodeAs[[]json.RawMessage](a.t, data))
}

func TestWorkspaceFromEnvironmentMakesSwitchingReadOnly(t *testing.T) {
	pinned := filepath.Join(t.TempDir(), "演示")
	a := startSwitchApp(t, t.TempDir(), "", withAppEnv(map[string]string{"DE_WORKSPACE": pinned}))

	list := a.workspaces()
	if !list.ReadOnly || list.Reason == "" {
		t.Fatalf("环境变量指定工作区时切换入口应只读并说明原因：%+v", list)
	}
	if len(list.Workspaces) != 1 || list.Workspaces[0].Path != pinned || list.Workspaces[0].ID != list.Current {
		t.Fatalf("只读时只列出环境变量指定的工作区：%+v", list)
	}

	status, data := a.call(http.MethodPost, "/api/workspaces", map[string]any{"name": "华南区"})
	if status != http.StatusConflict || decodeAs[switchReply](t, data).Error != "workspace_read_only" {
		t.Fatalf("只读时不能新建工作区：%d %s", status, data)
	}
	if reply := a.trySwitch(list.Current); reply.status != http.StatusConflict || reply.Error != "workspace_read_only" {
		t.Fatalf("只读时不能切换工作区：%+v", reply)
	}
}
