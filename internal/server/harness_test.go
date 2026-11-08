// 主接缝测试装置：HTTP 接口 + 真实引擎 + 真实工具 + 剧本模型，运行在临时工作区。
//
// 流转规则、可用操作等行为都从这里验证，测试只通过接口观察结果。
// 假引擎（server_test.go）继续保留，只用于 HTTP 细节。
package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/engine"
	"digital-employee/internal/server"
	"digital-employee/internal/settings"
	"digital-employee/internal/tools"
)

const harnessEmployee = "weekly-report"

// scriptedConnection 剧本模式下提交前检查用的连接：有 Key、有模型列表，但不发任何请求。
var scriptedConnection = contract.ModelConnection{
	Name: "scripted", BaseURL: "http://127.0.0.1:9/v1", APIKey: "sk-scripted-not-used", Models: []string{"scripted-model"},
}

// repoPath 仓库内的路径（测试的工作目录是 internal/server）。
func repoPath(parts ...string) string {
	return filepath.Join(append([]string{"..", ".."}, parts...)...)
}

// caseScript 读 samples/cases/scripts 下的剧本。
func caseScript(t *testing.T, name string) agent.ModelScript {
	t.Helper()
	raw, err := os.ReadFile(repoPath("samples", "cases", "scripts", name+".json"))
	if err != nil {
		t.Fatalf("读取剧本 %s 失败：%v", name, err)
	}
	script := agent.ModelScript{}
	if err := json.Unmarshal(raw, &script); err != nil {
		t.Fatalf("解析剧本 %s 失败：%v", name, err)
	}
	return script
}

// upload 一份要上传的附件。
type upload struct {
	name    string
	content []byte
}

func sampleFile(t *testing.T, name string) upload {
	t.Helper()
	content, err := os.ReadFile(repoPath("samples", "scenario-a", name))
	if err != nil {
		t.Fatalf("读取样例 %s 失败：%v", name, err)
	}
	return upload{name: name, content: content}
}

// a01Files 公开样例：第 44 周的销售明细和周会纪要。
func a01Files(t *testing.T) []upload {
	t.Helper()
	return []upload{sampleFile(t, "销售明细.xlsx"), sampleFile(t, "周会纪要_2026W44.docx")}
}

type harnessConfig struct {
	workflowEdits [][2]string
	connection    *contract.ModelConnection
	// connections 多个连接（会话模型要在它们之间选）；与 connection 二选一
	connections []contract.ModelConnection
	// scriptedModel 剧本模式下也配一条能通过提交前检查的连接（只在主人设里用）
	scriptedModel bool
	env           map[string]string
	wrapModel     func(agent.ModelProvider) agent.ModelProvider
	wrapTools     func(contract.ToolClient) contract.ToolClient
	qc            qcRunner
	crashAtStep   string
	// extraEmployees 经营周报员工之外，再复制进工作区的员工
	extraEmployees []string
	// appMode 应用目录模式：工作区是单独一个目录，应用目录在应用目录下的 app/
	appMode bool
}

// withAppMode 应用目录模式：工作区目录只是工作区（设置、员工、会话和任务都在应用目录里），
// 用来验证工作区目录里有什么、没有什么。
func withAppMode() harnessOption {
	return func(config *harnessConfig) { config.appMode = true }
}

// withEmployee 再复制一个仓库里的员工进工作区（比如知识库问答员工）。
func withEmployee(id string) harnessOption {
	return func(config *harnessConfig) { config.extraEmployees = append(config.extraEmployees, id) }
}

// qcRunner 质检引擎的形状，测试用它包住真实质检。
type qcRunner interface {
	Run(input contract.QcInput) []contract.QcCheckResult
}

// harnessOption 装置选项。
type harnessOption func(*harnessConfig)

// withWorkflowEdit 改工作区里员工配置 workflow.yaml 的一段文本（比如调低预算）。
func withWorkflowEdit(old, replacement string) harnessOption {
	return func(config *harnessConfig) {
		config.workflowEdits = append(config.workflowEdits, [2]string{old, replacement})
	}
}

// withModelConnection 不用剧本：引擎走真实模型提供者，这条连接写进工作区设置并设为默认。
func withModelConnection(connection contract.ModelConnection) harnessOption {
	return func(config *harnessConfig) { config.connection = &connection }
}

// withModelConnections 不用剧本：引擎走真实模型提供者，这几条连接写进设置（第一个是默认连接）。
func withModelConnections(connections ...contract.ModelConnection) harnessOption {
	return func(config *harnessConfig) { config.connections = connections }
}

// withScriptedModel 剧本模式下也配一条能通过提交前检查的连接（会话里要提交任务时用）。
func withScriptedModel() harnessOption {
	return func(config *harnessConfig) { config.scriptedModel = true }
}

// withEnv 注入服务端看到的环境变量。
func withEnv(env map[string]string) harnessOption {
	return func(config *harnessConfig) { config.env = env }
}

// withModelWrapper 包一层模型提供者，比如控制模型调用的时机。
func withModelWrapper(wrap func(agent.ModelProvider) agent.ModelProvider) harnessOption {
	return func(config *harnessConfig) { config.wrapModel = wrap }
}

// withModel 包住剧本模型（比如让某次调用停住）。
func withModel(wrap func(agent.ModelProvider) agent.ModelProvider) harnessOption {
	return func(config *harnessConfig) { config.wrapModel = wrap }
}

// withTools 包住真实工具（比如让某次调用停住）。
func withTools(wrap func(contract.ToolClient) contract.ToolClient) harnessOption {
	return func(config *harnessConfig) { config.wrapTools = wrap }
}

// withQC 换掉质检引擎。
func withQC(qc qcRunner) harnessOption {
	return func(config *harnessConfig) { config.qc = qc }
}

// withCrashAt 模拟进程在某个步骤崩溃。
func withCrashAt(step string) harnessOption {
	return func(config *harnessConfig) { config.crashAtStep = step }
}

// harness 一个运行中的主接缝。
type harness struct {
	t        *testing.T
	baseURL  string
	engine   *engine.Engine
	provider *agent.ScriptedProvider
	// frontdesk 不为 nil 时前台 Agent 改用它，比如接本地假服务的真实提供者
	frontdesk agent.ModelProvider
	// model 引擎用的模型，前台 Agent 默认也用它
	model     agent.ModelProvider
	scripted  bool
	env       map[string]string
	registry  contract.ToolClient
	faults    *contract.FaultHooks
	workspace string
	employees string
	// appDir 应用目录模式下的应用目录（单目录模式下是空串，应用目录就是工作区目录）
	appDir string
	// workspaces 应用目录模式下登记的工作区
	workspaces []server.Workspace
	// app 切到应用目录模式时的工作区接线：应用目录、员工副本、两个登记的工作区
	app *appWire
}

// setFrontdeskScript 换一份前台 Agent 的剧本（路径要等工作区建好才定得下来的测试用）。
func (h *harness) setFrontdeskScript(script agent.ModelScript) {
	h.provider.SetScript(script)
}

// appDirs 应用目录。
func (h *harness) appDirs() string {
	if h.appDir != "" {
		return h.appDir
	}
	return h.workspace
}

// newHarness 组装真实引擎、真实工具和剧本模型，员工配置是临时工作区里的副本。
//
// 测试因此并行运行，一个测试只能调一次（t.Parallel 不能重复调用）。
func newHarness(t *testing.T, script agent.ModelScript, options ...harnessOption) *harness {
	t.Helper()
	config := harnessConfig{
		// 工具重试不等待，否则每次工具故障都要白等几秒
		workflowEdits: [][2]string{{"backoffMs: [1000, 2000, 4000]", "backoffMs: [0]"}},
	}
	for _, option := range options {
		option(&config)
	}

	workspace := t.TempDir()
	appDir := workspace
	if config.appMode {
		workspace = filepath.Join(t.TempDir(), "工作区")
		if err := os.MkdirAll(workspace, 0o755); err != nil {
			t.Fatal(err)
		}
		appDir = filepath.Join(t.TempDir(), "app")
	}
	employees := filepath.Join(appDir, "employees")
	copyEmployee(t, repoPath("employees", harnessEmployee), filepath.Join(employees, harnessEmployee))
	editWorkflow(t, filepath.Join(employees, harnessEmployee, "workflow.yaml"), config.workflowEdits)
	for _, id := range config.extraEmployees {
		copyEmployee(t, repoPath("employees", id), filepath.Join(employees, id))
	}

	env := config.env
	if env == nil {
		env = map[string]string{}
	}
	// 测试不读进程里真实的 DEEPSEEK_API_KEY（沙箱里 DSH 会设一个）：服务端按注入的 Env 读，
	// 没给就是这个空值（withEnv 注入的照样生效）
	env["DEEPSEEK_API_KEY"] = config.env["DEEPSEEK_API_KEY"]
	// Spec 0006：设置、员工、知识库在应用目录里。主接缝默认是单目录模式：工作区目录同时是应用目录。
	if config.appMode {
		env["DE_APP_DIR"] = appDir
	} else {
		env["DE_WORKSPACE"] = workspace
	}
	// 判断步骤和前台 Agent 共用一个剧本模型，前台 Agent 消费 agent.FrontdeskStepID 下的轮次
	provider := agent.NewScriptedModelProvider(script, agent.ScriptedProviderOptions{})
	var model agent.ModelProvider = provider
	connections := config.connections
	if config.connection != nil {
		connections = []contract.ModelConnection{*config.connection}
	}
	// 设置写在应用目录里（单目录模式下就是工作区目录）
	if len(connections) > 0 {
		if _, err := settings.Update(appDir, func(doc *contract.SettingsDocument) {
			doc.Connections = connections
			doc.LastModel = &contract.ModelRef{Connection: connections[0].Name, Model: connections[0].Models[0]}
		}); err != nil {
			t.Fatalf("写模型设置失败：%v", err)
		}
		model = agent.NewModelProvider(settings.LoadModelSettings(appDir, settings.Env(env)))
	} else if config.scriptedModel && config.connection == nil {
		// 剧本模式不走网络，但提交前检查照常检查会话模型（Spec 0006）：
		// 给一个能通过检查的连接（指向本机、不存在的端口），否则会话里提交不了任务。
		if _, err := settings.Update(appDir, func(doc *contract.SettingsDocument) {
			doc.Connections = []contract.ModelConnection{scriptedConnection}
			doc.LastModel = &contract.ModelRef{Connection: scriptedConnection.Name, Model: scriptedConnection.Models[0]}
		}); err != nil {
			t.Fatalf("写模型设置失败：%v", err)
		}
	} else if config.scriptedModel {
		// 指定了连接（比如没配 Key 的那个来测提交前检查）：引擎仍用剧本模型，设置按原样写
		if _, err := settings.Update(appDir, func(doc *contract.SettingsDocument) {
			doc.Connections = []contract.ModelConnection{*config.connection}
		}); err != nil {
			t.Fatalf("写模型设置失败：%v", err)
		}
	}
	if config.wrapModel != nil {
		model = config.wrapModel(model)
	}

	registry := tools.NewRegistry()
	t.Cleanup(func() { _ = registry.Close() })
	faults := &contract.FaultHooks{CrashAtStep: config.crashAtStep}
	var toolClient contract.ToolClient = registry
	if config.wrapTools != nil {
		toolClient = config.wrapTools(toolClient)
	}
	eng, err := engine.NewWithOptions(engine.Deps{
		Dir:          appDir,
		EmployeesDir: employees,
		ToolClient:   toolClient,
		Model:        model,
		Faults:       faults,
	}, engine.Options{QC: config.qc})
	if err != nil {
		t.Fatalf("创建引擎失败：%v", err)
	}
	h := &harness{
		t: t, engine: eng, provider: provider, model: model, scripted: config.connection == nil && len(config.connections) == 0, env: env,
		registry: registry, faults: faults, workspace: workspace, employees: employees,
	}
	if config.appMode {
		h.appDir = appDir
		h.workspaces = []server.Workspace{{ID: appWorkspaceID, Path: workspace}}
	}
	h.baseURL = h.serve()
	return h
}

// appWorkspaceID 应用目录模式里的工作区 ID。
const appWorkspaceID = "ws-app"

// serve 在同一个工作区上起一个服务端，返回地址。再调一次就是“重启后”的服务端。
func (h *harness) serve() string {
	h.t.Helper()
	srv := server.New(server.Options{
		Engine:       h.engine,
		AppDir:       h.appDirs(),
		Workspaces:   h.workspaces,
		WorkspaceDir: h.workspace,
		EmployeesDir: h.employees,
		ToolClient:   h.registry,
		Faults:       h.faults,
		Scripted:     h.scripted,
		Model: func() agent.ModelProvider {
			if h.frontdesk != nil {
				return h.frontdesk
			}
			return h.model
		},
		// 和生产一样：保存模型设置后重建真实模型提供者并交给引擎；剧本模式不换
		OnSettingsSaved: func(saved contract.ModelSettings) error {
			if h.scripted {
				return nil
			}
			h.model = agent.NewModelProvider(saved)
			h.engine.SetModelProvider(h.model)
			return nil
		},
		Env:    h.env,
		Logger: server.NewLogger(server.LevelError, func(string, string) {}),
	})
	httpServer := httptest.NewServer(srv.Handler())
	h.t.Cleanup(httpServer.Close)
	return httpServer.URL
}

func copyEmployee(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, rel)
		if info.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, content, 0o644)
	})
	if err != nil {
		t.Fatalf("复制员工配置失败：%v", err)
	}
}

func editWorkflow(t *testing.T, path string, edits [][2]string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, edit := range edits {
		if !strings.Contains(text, edit[0]) {
			t.Fatalf("workflow.yaml 里没有 %q", edit[0])
		}
		text = strings.ReplaceAll(text, edit[0], edit[1])
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// send 发一个请求，返回状态码和响应体。
func (h *harness) send(method, path string, role contract.Role, contentType string, body io.Reader) (int, []byte) {
	h.t.Helper()
	request, err := http.NewRequest(method, h.baseURL+path, body)
	if err != nil {
		h.t.Fatal(err)
	}
	if role != "" {
		request.Header.Set("x-role", string(role))
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		h.t.Fatal(err)
	}
	return response.StatusCode, data
}

func multipartBody(t *testing.T, fields map[string]string, files []upload) (string, io.Reader) {
	t.Helper()
	buffer := &bytes.Buffer{}
	writer := multipart.NewWriter(buffer)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range files {
		part, err := writer.CreateFormFile("attachments", file.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(file.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return writer.FormDataContentType(), buffer
}

// createTask 发起人建一个第 44 周的草稿任务。
func (h *harness) createTask(files []upload) contract.TaskSnapshot {
	h.t.Helper()
	contentType, body := multipartBody(h.t, map[string]string{
		"employee": harnessEmployee,
		"form":     `{"period":{"year":2026,"week":44}}`,
	}, files)
	status, data := h.send(http.MethodPost, "/api/tasks", contract.RoleInitiator, contentType, body)
	if status != http.StatusCreated {
		h.t.Fatalf("建任务失败：%d %s", status, data)
	}
	return decode[contract.TaskSnapshot](h.t, data)
}

// commandReply POST /commands 的响应。
type commandReply struct {
	status int
	contract.CommandResult
}

// tryCommand 发命令，不判断结果。
func (h *harness) tryCommand(taskID string, role contract.Role, command map[string]any) commandReply {
	h.t.Helper()
	raw, err := json.Marshal(command)
	if err != nil {
		h.t.Fatal(err)
	}
	status, data := h.send(http.MethodPost, "/api/tasks/"+taskID+"/commands", role, "application/json", bytes.NewReader(raw))
	return commandReply{status: status, CommandResult: decode[contract.CommandResult](h.t, data)}
}

// command 发命令，要求被接受，并等任务停稳。
func (h *harness) command(taskID string, role contract.Role, command map[string]any) contract.TaskSnapshot {
	h.t.Helper()
	reply := h.tryCommand(taskID, role, command)
	if !reply.OK {
		h.t.Fatalf("命令 %v 被拒绝：%d %s %s", command["type"], reply.status, reply.Error, reply.Message)
	}
	return h.settled(taskID)
}

// supplement 发起人带附件补充材料，并等任务停稳。
func (h *harness) supplement(taskID string, files []upload) contract.TaskSnapshot {
	h.t.Helper()
	contentType, body := multipartBody(h.t, map[string]string{"command": `{"type":"supplement"}`}, files)
	status, data := h.send(http.MethodPost, "/api/tasks/"+taskID+"/commands", contract.RoleInitiator, contentType, body)
	if status != http.StatusOK {
		h.t.Fatalf("supplement 被拒绝：%d %s", status, data)
	}
	return h.settled(taskID)
}

// settled 等任务停在暂停态或终态，再从接口读快照。
func (h *harness) settled(taskID string) contract.TaskSnapshot {
	h.t.Helper()
	if _, err := h.engine.WaitForSettled(taskID); err != nil {
		h.t.Fatalf("等待任务停稳失败：%v", err)
	}
	return h.snapshot(taskID)
}

// snapshot GET /api/tasks/:id。
func (h *harness) snapshot(taskID string) contract.TaskSnapshot {
	h.t.Helper()
	status, data := h.send(http.MethodGet, "/api/tasks/"+taskID, "", "", nil)
	if status != http.StatusOK {
		h.t.Fatalf("读取任务失败：%d %s", status, data)
	}
	return decode[contract.TaskSnapshot](h.t, data)
}

// summary GET /api/tasks 里某个任务的摘要。
func (h *harness) summary(taskID string) contract.TaskSummary {
	h.t.Helper()
	status, data := h.send(http.MethodGet, "/api/tasks", "", "", nil)
	if status != http.StatusOK {
		h.t.Fatalf("读取任务列表失败：%d %s", status, data)
	}
	for _, item := range decode[[]contract.TaskSummary](h.t, data) {
		if item.ID == taskID {
			return item
		}
	}
	h.t.Fatalf("任务列表里没有 %s", taskID)
	return contract.TaskSummary{}
}

// events GET /api/tasks/:id/events 的原始 JSON。
func (h *harness) eventsJSON(taskID string) string {
	h.t.Helper()
	status, data := h.send(http.MethodGet, "/api/tasks/"+taskID+"/events", "", "", nil)
	if status != http.StatusOK {
		h.t.Fatalf("读取事件失败：%d %s", status, data)
	}
	return string(data)
}

// setToolFaults 通过设置接口注入工具故障（传空表清除）。
func (h *harness) setToolFaults(faults map[string]string) {
	h.t.Helper()
	raw, err := json.Marshal(map[string]any{"toolFaults": faults})
	if err != nil {
		h.t.Fatal(err)
	}
	status, data := h.send(http.MethodPut, "/api/settings/demo", contract.RoleOperator, "application/json", bytes.NewReader(raw))
	if status != http.StatusOK {
		h.t.Fatalf("设置工具故障失败：%d %s", status, data)
	}
}

func decode[T any](t *testing.T, data []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("响应不是预期的 JSON：%v\n%s", err, data)
	}
	return value
}

// actionTable 把快照的 actions 写成“命令=角色,角色”的列表，便于和预期逐字比较。
func actionTable(snapshot contract.TaskSnapshot) []string {
	rows := make([]string, 0, len(snapshot.Actions))
	for _, action := range snapshot.Actions {
		roles := make([]string, 0, len(action.Roles))
		for _, role := range action.Roles {
			roles = append(roles, string(role))
		}
		rows = append(rows, action.Command+"="+strings.Join(roles, ","))
	}
	return rows
}

func expectActions(t *testing.T, snapshot contract.TaskSnapshot, want ...string) {
	t.Helper()
	got := actionTable(snapshot)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("状态 %s 的 actions = %v，want %v", snapshot.State, got, want)
	}
}

func expectWaitingFor(t *testing.T, snapshot contract.TaskSnapshot, want ...contract.Role) {
	t.Helper()
	if fmt.Sprint(snapshot.WaitingFor) != fmt.Sprint(want) {
		t.Fatalf("状态 %s 的 waitingFor = %v，want %v", snapshot.State, snapshot.WaitingFor, want)
	}
}

func expectState(t *testing.T, snapshot contract.TaskSnapshot, want contract.TaskState) {
	t.Helper()
	if snapshot.State != want {
		t.Fatalf("状态 = %s，want %s（暂停原因 %+v）", snapshot.State, want, snapshot.Pause)
	}
}
