// 应用目录、工作区登记与启动：从 HTTP 接口验证，配真实引擎、真实工具和剧本模型，应用目录和工作区目录都是临时目录。
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
	"digital-employee/internal/session"
	"digital-employee/internal/tools"
)

// switchingApp 一个运行中的应用。
type switchingApp struct {
	t       *testing.T
	baseURL string
	running *app.Running
}

type switchingConfig struct {
	env map[string]string
}

// withAppEnv 应用看到的环境变量（DE_APP_DIR 由装置补上）。
func withAppEnv(env map[string]string) func(*switchingConfig) {
	return func(config *switchingConfig) {
		if config.env == nil {
			config.env = map[string]string{}
		}
		for key, value := range env {
			config.env[key] = value
		}
	}
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

// startApp 在应用目录 appDir 上启动应用；workspace 为空时由应用自己决定（首次启动建“我的工作区”）。
func startApp(t *testing.T, appDir, workspace string, options ...func(*switchingConfig)) *switchingApp {
	t.Helper()
	config := switchingConfig{}
	for _, option := range options {
		option(&config)
	}
	env := map[string]string{}
	for key, value := range config.env {
		env[key] = value
	}
	env["DE_APP_DIR"] = appDir
	a := &switchingApp{t: t}
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
			return tools.NewRegistry(), nil
		},
		EngineFactory: func(deps app.EngineDeps) (contract.Engine, error) {
			if deps.ApplicationDir == "" {
				return nil, &missingAppDirError{}
			}
			created, err := engine.New(engine.Deps{
				Dir:          deps.ApplicationDir,
				EmployeesDir: deps.EmployeesDir,
				ToolClient:   deps.ToolClient,
				Model:        agent.NewScriptedModelProvider(agent.ModelScript{}, agent.ScriptedProviderOptions{}),
				Faults:       deps.Faults,
				// 任务列表要读任务目录里的员工配置副本：应用目录模式下员工按应用目录的 employees/ 解析
				// （真实引擎按员工 id 取配置，这里只是为换目录提供一个同名副本）
				ConfigFallback: func(employeeID string) (string, bool) {
					dir := filepath.Join(deps.EmployeesDir, employeeID)
					if _, err := os.Stat(filepath.Join(dir, "workflow.yaml")); err != nil {
						return "", false
					}
					return dir, true
				},
			})
			if err == nil {
				mu.Lock()
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

type missingAppDirError struct{}

func (*missingAppDirError) Error() string { return "引擎没有拿到应用目录" }

// call 发一个 JSON 请求，返回状态码和响应体。
func (a *switchingApp) call(method, path string, body any) (int, []byte) {
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
	request.Header.Set("x-role", string(contract.RoleInitiator))
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

// registered 是 /api/workspaces 列表里的一项。
type registered struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	Missing     bool   `json:"missing"`
	MissingText string `json:"missingText"`
}

type workspaceList struct {
	ReadOnly   bool         `json:"readOnly"`
	Reason     string       `json:"reason"`
	Workspaces []registered `json:"workspaces"`
}

// workspaces GET /api/workspaces。
func (a *switchingApp) workspaces() workspaceList {
	a.t.Helper()
	status, data := a.call(http.MethodGet, "/api/workspaces", nil)
	if status != http.StatusOK {
		a.t.Fatalf("读取工作区列表失败：%d %s", status, data)
	}
	return decodeAs[workspaceList](a.t, data)
}

// byName 列表里叫这个名字的工作区。
func (a *switchingApp) byName(name string) registered {
	a.t.Helper()
	for _, item := range a.workspaces().Workspaces {
		if item.Name == name {
			return item
		}
	}
	a.t.Fatalf("工作区列表里没有“%s”：%+v", name, a.workspaces())
	return registered{}
}

// createWorkspace POST /api/workspaces，要求成功。
func (a *switchingApp) createWorkspace(name, path string) registered {
	a.t.Helper()
	status, data := a.call(http.MethodPost, "/api/workspaces", map[string]any{"name": name, "path": path})
	if status != http.StatusCreated {
		a.t.Fatalf("新建工作区失败：%d %s", status, data)
	}
	return decodeAs[struct {
		Workspace registered `json:"workspace"`
	}](a.t, data).Workspace
}

// createSessionIn POST /api/sessions。
func (a *switchingApp) createSessionIn(workspaceID string) session.View {
	a.t.Helper()
	status, data := a.call(http.MethodPost, "/api/sessions", map[string]any{"workspaceId": workspaceID})
	if status != http.StatusCreated {
		a.t.Fatalf("建会话失败：%d %s", status, data)
	}
	return decodeAs[session.View](a.t, data)
}

// sessions GET /api/sessions（可以带过滤参数）。
func (a *switchingApp) sessions(query string) []session.View {
	a.t.Helper()
	status, data := a.call(http.MethodGet, "/api/sessions"+query, nil)
	if status != http.StatusOK {
		a.t.Fatalf("读取会话失败：%d %s", status, data)
	}
	return decodeAs[[]session.View](a.t, data)
}

// sessionsWith 按工作区过滤的会话；返回 ID → 工作区。
func (a *switchingApp) sessionsWith(workspaceID string) map[string]string {
	a.t.Helper()
	out := map[string]string{}
	// 会话目录隔一会儿才重新读（T38 起列表带状态点）：新建的会话马上读不到，等一下
	deadline := time.Now().Add(5 * time.Second)
	for {
		out = map[string]string{}
		listed := a.sessions("?workspaceId=" + workspaceID)
		for _, item := range listed {
			out[item.ID] = item.WorkspaceID
		}
		if time.Now().After(deadline) {
			return out
		}
		if len(out) > 0 {
			return out
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitSessions 等到会话列表满足条件，最多等这么久（T38 起会话目录隔一会儿才重新读）。
func (a *switchingApp) waitSessions(query string, ok func([]session.View) bool) []session.View {
	a.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		list := a.sessions(query)
		if ok(list) {
			return list
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// createDraftTask 用公开样例建一个第 44 周的草稿任务，返回任务 ID。
func (a *switchingApp) createDraftTask(workspaceID string) string {
	a.t.Helper()
	buffer := &bytes.Buffer{}
	writer := multipart.NewWriter(buffer)
	_ = writer.WriteField("employee", "weekly-report")
	_ = writer.WriteField("form", `{"period":{"year":2026,"week":44}}`)
	_ = writer.WriteField("workspaceId", workspaceID)
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
func (a *switchingApp) taskIDs() []string {
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

// knowledgeTitles GET /api/knowledge 里的条目标题。
func (a *switchingApp) knowledgeTitles() []string {
	a.t.Helper()
	status, data := a.call(http.MethodGet, "/api/knowledge", nil)
	if status != http.StatusOK {
		a.t.Fatalf("读取知识库失败：%d %s", status, data)
	}
	titles := []string{}
	for _, item := range decodeAs[[]struct {
		Title string `json:"title"`
	}](a.t, data) {
		titles = append(titles, item.Title)
	}
	return titles
}

// employeeNames GET /api/employees 里的员工名称。
func (a *switchingApp) employeeNames() []string {
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
func (a *switchingApp) connectionNames() []string {
	a.t.Helper()
	status, data := a.call(http.MethodGet, "/api/settings", nil)
	if status != http.StatusOK {
		a.t.Fatalf("读取设置失败：%d %s", status, data)
	}
	view := decodeAs[struct {
		Connections []struct {
			Name   string `json:"name"`
			HasKey bool   `json:"hasKey"`
		} `json:"connections"`
	}](a.t, data)
	names := []string{}
	for _, item := range view.Connections {
		names = append(names, item.Name)
	}
	return names
}

// hasKey GET /api/settings 里某个连接有没有 Key。
func (a *switchingApp) hasKey(name string) bool {
	a.t.Helper()
	status, data := a.call(http.MethodGet, "/api/settings", nil)
	if status != http.StatusOK {
		a.t.Fatalf("读取设置失败：%d %s", status, data)
	}
	view := decodeAs[struct {
		Connections []struct {
			Name   string `json:"name"`
			HasKey bool   `json:"hasKey"`
		} `json:"connections"`
	}](a.t, data)
	for _, item := range view.Connections {
		if item.Name == name {
			return item.HasKey
		}
	}
	return false
}

// preflight GET /api/preflight 是否通过。
func (a *switchingApp) preflight() bool {
	a.t.Helper()
	status, data := a.call(http.MethodGet, "/api/preflight", nil)
	if status != http.StatusOK {
		a.t.Fatalf("提交前检查失败：%d %s", status, data)
	}
	return decodeAs[struct {
		OK bool `json:"ok"`
	}](a.t, data).OK
}

// hasFile 文件是否存在。
func hasFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func TestFirstStartCreatesMyWorkspaceInDocuments(t *testing.T) {
	appDir := t.TempDir()
	home := t.TempDir()
	a := startApp(t, appDir, "", withAppEnv(map[string]string{"USERPROFILE": home}))

	list := a.workspaces()
	if list.ReadOnly {
		t.Fatalf("没有用环境变量指定工作区时不是只读：%+v", list)
	}
	if len(list.Workspaces) != 1 {
		t.Fatalf("首次启动应建一个工作区：%+v", list)
	}
	only := list.Workspaces[0]
	want := filepath.Join(home, "Documents", "数字员工")
	if only.Name != "我的工作区" || !sameDir(only.Path, want) {
		t.Fatalf("首次启动的工作区 = %+v，目录应为 %s", only, want)
	}
	if !isDir(want) {
		t.Fatalf("工作区目录应已创建：%s", want)
	}
	if only.ID == "" || only.CreatedAt == "" || only.UpdatedAt == "" {
		t.Fatalf("登记项应有 ID、创建时间和更新时间：%+v", only)
	}
	// 全局内容放在应用目录：设置、员工、工作区列表；任务和会话也在这里
	for _, name := range []string{"settings.json", "employees", "workspaces.json", "tasks", "sessions"} {
		if _, err := os.Stat(filepath.Join(appDir, name)); err != nil {
			t.Fatalf("应用目录里应有 %s：%v", name, err)
		}
	}
	// 工作目录里只放用得上的东西：一个空的“交付”文件夹，不放样例，也没有应用数据
	entries, err := os.ReadDir(want)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !slices.Equal(names, []string{"交付"}) {
		t.Fatalf("工作目录里只应有“交付”文件夹：%v", names)
	}
	// 公开样例和评测用例只放在应用目录
	if !hasFile(filepath.Join(appDir, "samples", "cases", "A-01.json")) || !hasFile(filepath.Join(appDir, "samples", "scenario-a", "销售明细.xlsx")) {
		t.Fatal("应用目录里应有公开样例")
	}
}

// sampleList GET /api/samples 里的样例 ID。
func (a *switchingApp) sampleList() []string {
	a.t.Helper()
	status, data := a.call(http.MethodGet, "/api/samples", nil)
	if status != http.StatusOK {
		a.t.Fatalf("读取公开样例失败：%d %s", status, data)
	}
	ids := []string{}
	for _, item := range decodeAs[struct {
		Samples []struct {
			ID string `json:"id"`
		} `json:"samples"`
	}](a.t, data).Samples {
		ids = append(ids, item.ID)
	}
	return ids
}

// loadSample POST /api/samples/scenario-a/tasks。
func (a *switchingApp) loadSample(workspaceID string) (int, []byte) {
	a.t.Helper()
	return a.call(http.MethodPost, "/api/samples/scenario-a/tasks", map[string]any{"workspaceId": workspaceID})
}

func TestWorkspaceFolderHasNoSamplesAndPublicSampleStillLoads(t *testing.T) {
	// 假仓库：只有 employees/ 和 samples/，应用目录里的样例从这里释放
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "employees"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(repo, "samples", "cases", "A-01.json"), `{"employee":"weekly-report","form":{"period":{"year":2026,"week":44}}}`)
	writeTestFile(t, filepath.Join(repo, "samples", "scenario-a", "销售明细.xlsx"), "xlsx")
	writeTestFile(t, filepath.Join(repo, "samples", "scenario-a", "周会纪要_2026W44.docx"), "docx")

	appDir := t.TempDir()
	home := t.TempDir()
	env := withAppEnv(map[string]string{"USERPROFILE": home, "DE_REPO_ROOT": repo})
	a := startApp(t, appDir, "", env)

	// 新建的工作区目录里没有 samples/
	created := a.createWorkspace("华南区", filepath.Join(home, "华南区"))
	if isDir(filepath.Join(created.Path, "samples")) {
		t.Fatalf("新建的工作区目录里不应释放样例：%s", created.Path)
	}
	if !isDir(filepath.Join(created.Path, "交付")) {
		t.Fatalf("新建的工作区目录里应有“交付”文件夹：%s", created.Path)
	}
	mine := a.byName("我的工作区")
	if isDir(filepath.Join(mine.Path, "samples")) || !isDir(filepath.Join(mine.Path, "交付")) {
		t.Fatalf("“我的工作区”目录里只应有“交付”：%s", mine.Path)
	}
	// 载入公开样例：从应用目录取，不需要工作区里有 samples/
	if ids := a.sampleList(); !slices.Equal(ids, []string{"scenario-a"}) {
		t.Fatalf("公开样例列表 = %v", ids)
	}
	status, data := a.loadSample(created.ID)
	if status != http.StatusCreated {
		t.Fatalf("载入公开样例失败：%d %s", status, data)
	}
	snapshot := decodeAs[contract.TaskSnapshot](a.t, data)
	if len(snapshot.Attachments) != 2 || snapshot.Form == nil {
		t.Fatalf("样例任务 = %+v", snapshot)
	}
}

func TestSingleDirectoryModeKeepsSamples(t *testing.T) {
	// 单目录模式布局不变：目录同时是应用目录，样例照样释放到它的 samples/ 下
	pinned := filepath.Join(t.TempDir(), "演示")
	a := startApp(t, t.TempDir(), pinned)
	if !hasFile(filepath.Join(pinned, "samples", "cases", "A-01.json")) {
		t.Fatal("单目录模式下应释放公开用例")
	}
	if ids := a.sampleList(); !slices.Equal(ids, []string{"scenario-a"}) {
		t.Fatalf("单目录模式的公开样例列表 = %v", ids)
	}
}

func sameDir(a, b string) bool {
	left, errLeft := filepath.Abs(a)
	right, errRight := filepath.Abs(b)
	if errLeft != nil || errRight != nil {
		return a == b
	}
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func TestWorkspaceListOutlivesTheProcess(t *testing.T) {
	appDir := t.TempDir()
	home := t.TempDir()
	env := withAppEnv(map[string]string{"USERPROFILE": home})
	first := startApp(t, appDir, "", env)
	created := first.createWorkspace("华南区", filepath.Join(home, "华南区"))
	if err := first.running.Close(); err != nil {
		t.Fatal(err)
	}

	// 重启后接着用登记过的工作区，不重建“我的工作区”
	restarted := startApp(t, appDir, "", env)
	list := restarted.workspaces()
	if len(list.Workspaces) != 2 {
		t.Fatalf("重启后应还是两个工作区：%+v", list)
	}
	if got := restarted.byName("华南区"); got.ID != created.ID {
		t.Fatalf("登记项应原样保留：%+v", got)
	}
}

func TestWorkspacesShareSettingsAndSessionsStayInTheirWorkspace(t *testing.T) {
	// 没有剧本：前台 Agent 走真实模型提供者，只读设置，不发请求
	appDir := t.TempDir()
	home := t.TempDir()
	env := withAppEnv(map[string]string{"USERPROFILE": home})
	a := startApp(t, appDir, "", env)
	mine := a.workspaces().Workspaces[0]
	other := a.createWorkspace("华南区", "")

	inMine := a.createSessionIn(mine.ID)
	inOther := a.createSessionIn(other.ID)

	// 在 A 保存的连接就在应用目录的 settings.json 里，B 的提交前检查读同一份
	if status, data := a.call(http.MethodPut, "/api/settings/connections/华东专线", map[string]any{
		"baseUrl": "http://127.0.0.1:9/v1", "apiKey": "sk-t36-file", "models": []string{"m1"},
	}); status != http.StatusOK {
		t.Fatalf("保存连接失败：%d %s", status, data)
	}
	if !a.hasKey("华东专线") || !a.preflight() {
		t.Fatal("两个工作区应共用应用目录里的一份设置：任何一边的提交前检查都读到它")
	}
	if !hasFile(filepath.Join(appDir, "settings.json")) {
		t.Fatal("设置应在应用目录里")
	}

	// 会话目录隔一会儿才重新读（T38 起列表带状态点）：刚建的会话等一下才看得到
	onlyOther := a.waitSessions("?workspaceId="+other.ID, func(list []session.View) bool { return len(list) == 1 && list[0].ID == inOther.ID })
	if onlyOther == nil {
		t.Fatalf("B 下只应有 B 的会话：%+v", a.sessions("?workspaceId="+other.ID))
	}
	both := a.waitSessions("", func(list []session.View) bool {
		ids := []string{}
		for _, item := range list {
			ids = append(ids, item.ID)
		}
		return slices.Contains(ids, inMine.ID) && slices.Contains(ids, inOther.ID)
	})
	if both == nil {
		ids := []string{}
		for _, item := range a.sessions("") {
			ids = append(ids, item.ID)
		}
		t.Fatalf("不带过滤时应列出两个工作区的会话：%v", ids)
	}
}

func TestUnregisterKeepsSessionsTasksAndFiles(t *testing.T) {
	appDir := t.TempDir()
	home := t.TempDir()
	a := startApp(t, appDir, "", withAppEnv(map[string]string{"USERPROFILE": home}))
	mine := a.byName("我的工作区")
	other := a.createWorkspace("华南区", "")
	created := a.createSessionIn(mine.ID)
	taskID := a.createDraftTask(mine.ID)
	files, err := os.ReadDir(other.Path)
	if err != nil {
		t.Fatal(err)
	}
	before := []string{}
	for _, file := range files {
		before = append(before, file.Name())
	}

	status, data := a.call(http.MethodDelete, "/api/workspaces/"+other.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("取消登记失败：%d %s", status, data)
	}
	if list := a.workspaces().Workspaces; slices.ContainsFunc(list, func(item registered) bool { return item.ID == other.ID }) {
		t.Fatalf("取消登记后不应再列出这个工作区：%+v", list)
	}
	// 会话、任务和目录里的文件都还在
	found := false
	for _, item := range a.waitSessions("", func(list []session.View) bool { return slices.ContainsFunc(list, func(s session.View) bool { return s.ID == created.ID }) }) {
		if item.ID == created.ID {
			found = true
			if item.WorkspaceID != mine.ID {
				t.Fatalf("会话仍应记着原工作区：%+v", item)
			}
		}
	}
	if !found {
		t.Fatal("取消登记后会话应还在")
	}
	if !slices.Contains(a.taskIDs(), taskID) {
		t.Fatalf("取消登记后任务应还在：%v", a.taskIDs())
	}
	after, err := os.ReadDir(other.Path)
	if err != nil {
		t.Fatalf("工作区目录应还在：%v", err)
	}
	names := []string{}
	for _, file := range after {
		names = append(names, file.Name())
	}
	if !slices.Equal(before, names) {
		t.Fatalf("取消登记不应删目录里的文件：前 %v 后 %v", before, names)
	}
}

func TestMissingDirectoryIsReportedButRegistrationStays(t *testing.T) {
	appDir := t.TempDir()
	home := t.TempDir()
	a := startApp(t, appDir, "", withAppEnv(map[string]string{"USERPROFILE": home}))
	gone := filepath.Join(home, "已删除")
	created := a.createWorkspace("已删除", gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	// 登记保留，状态是“目录缺失”
	item := a.byName("已删除")
	if item.ID != created.ID || item.Path != created.Path {
		t.Fatalf("登记应保留：%+v", item)
	}
	if !item.Missing || item.MissingText != "目录缺失" {
		t.Fatalf("目录不存在时状态应是“目录缺失”：%+v", item)
	}
	// 目录缺失时不重建目录
	if err := a.running.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := startApp(t, appDir, "", withAppEnv(map[string]string{"USERPROFILE": home}))
	if item := restarted.byName("已删除"); !item.Missing || item.ID != created.ID {
		t.Fatalf("重启后登记应保留并标出目录缺失：%+v", item)
	}
	if isDir(gone) {
		t.Fatalf("目录缺失时启动不应重建目录：%s", gone)
	}
}

func TestEnvironmentWorkspaceIsReadOnly(t *testing.T) {
	pinned := filepath.Join(t.TempDir(), "演示")
	a := startApp(t, t.TempDir(), "", withAppEnv(map[string]string{"DE_WORKSPACE": pinned}))

	list := a.workspaces()
	if !list.ReadOnly || list.Reason == "" {
		t.Fatalf("环境变量指定工作区时应只读并说明原因：%+v", list)
	}
	if len(list.Workspaces) != 1 || !sameDir(list.Workspaces[0].Path, pinned) {
		t.Fatalf("只读时只列出环境变量指定的工作区：%+v", list)
	}
	if status, _ := a.call(http.MethodPost, "/api/workspaces", map[string]any{"name": "华南区", "path": pinned}); status != http.StatusConflict {
		t.Fatalf("只读时不能新建工作区：%d", status)
	}
	// 不传 workspaceId 时取唯一的工作区
	if created := a.createSessionIn(""); created.WorkspaceID == "" {
		t.Fatalf("单目录模式的会话要记下工作区：%+v", created)
	}
}
