// T36 存储主接缝：应用目录 + 工作区 ID。
//
// 设置、员工、知识库在应用目录（DE_APP_DIR）里，所有工作区共用；
// 会话和任务在应用目录的 sessions/、tasks/ 下，各自记下所属工作区的 ID；
// 设置了 DE_WORKSPACE（或启动时指定工作区）时是单目录模式：这个目录同时是应用目录和唯一的工作区。
package server_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/engine"
	"digital-employee/internal/server"
	"digital-employee/internal/session"
	"digital-employee/internal/settings"
)

// startServer 起一个测试服务端，返回地址。
func startServer(t *testing.T, srv *server.Server) string {
	t.Helper()
	httpServer := httptest.NewServer(srv.Handler())
	t.Cleanup(httpServer.Close)
	return httpServer.URL
}

// appWire 应用目录模式下的接线：应用目录、员工配置副本、两个工作区目录。
type appWire struct {
	appDir    string
	employees string
	workA     string
	workB     string
	// workspaces 传给服务端的工作区表（ID 与目录）
	workspaces []server.Workspace
}

// tryCreateSession POST /api/sessions，不判断结果。body 为 nil 时请求体为空。
func (h *harness) tryCreateSession(body map[string]any) (int, []byte) {
	h.t.Helper()
	if body == nil {
		return h.send(http.MethodPost, "/api/sessions", contract.RoleInitiator, "", nil)
	}
	return h.postJSON("/api/sessions", contract.RoleInitiator, body)
}

// sessionsList GET /api/sessions（可以带过滤参数）。
func (h *harness) sessionsList(query string) []session.View {
	h.t.Helper()
	status, data := h.send(http.MethodGet, "/api/sessions"+query, "", "", nil)
	if status != http.StatusOK {
		h.t.Fatalf("读取会话列表失败：%d %s", status, data)
	}
	return decode[[]session.View](h.t, data)
}

// createSessionIn 在某个工作区里开一个新会话。
func (h *harness) createSessionIn(workspaceID string) session.View {
	h.t.Helper()
	status, data := h.tryCreateSession(map[string]any{"workspaceId": workspaceID})
	if status != http.StatusCreated {
		h.t.Fatalf("建会话失败：%d %s", status, data)
	}
	return decode[session.View](h.t, data)
}

// newAppWire 用主接缝的临时目录搭一套应用目录：员工配置复制到应用目录的 employees/。
func newAppWire(t *testing.T, h *harness) *appWire {
	t.Helper()
	appDir := t.TempDir()
	employees := filepath.Join(appDir, "employees")
	copyEmployee(t, filepath.Join(h.employees, harnessEmployee), filepath.Join(employees, harnessEmployee))
	workA, workB := t.TempDir(), t.TempDir()
	return &appWire{
		appDir: appDir, employees: employees, workA: workA, workB: workB,
		workspaces: []server.Workspace{{ID: "ws-a", Path: workA}, {ID: "ws-b", Path: workB}},
	}
}

// serve 在应用目录上起一个服务端；同一个引擎、同一个会话模块，工作区之间不再重建。
func (w *appWire) serve(t *testing.T, h *harness, eng *engine.Engine, workspaceDir string, env map[string]string) *server.Server {
	t.Helper()
	full := map[string]string{"DE_APP_DIR": w.appDir}
	for key, value := range env {
		full[key] = value
	}
	return server.New(server.Options{
		Engine:       eng,
		WorkspaceDir: workspaceDir,
		Workspaces:   w.workspaces,
		AppDir:       w.appDir,
		EmployeesDir: w.employees,
		ToolClient:   h.registry,
		Faults:       h.faults,
		Model:        func() agent.ModelProvider { return h.model },
		Env:          full,
		Logger:       server.NewLogger(server.LevelError, func(string, string) {}),
	})
}

// newAppEngine 应用目录模式下的引擎：任务在应用目录的 tasks/。
func newAppEngine(w *appWire, h *harness) *engine.Engine {
	h.t.Helper()
	eng, err := engine.New(engine.Deps{
		Dir:          w.appDir,
		EmployeesDir: w.employees,
		ToolClient:   h.registry,
	})
	if err != nil {
		h.t.Fatalf("创建引擎失败：%v", err)
	}
	return eng
}

func TestWorkspacesShareOneSetOfSettings(t *testing.T) {
	// 没有剧本：前台 Agent 走真实模型提供者，只读设置，不发请求
	h := newHarness(t, agent.ModelScript{})
	w := newAppWire(t, h)
	if _, err := settings.Update(w.appDir, func(doc *contract.SettingsDocument) {
		doc.Connections = []contract.ModelConnection{{
			Name: "测试连接", BaseURL: "http://127.0.0.1:9/v1", APIKey: "sk-t36-test", Models: []string{"m1"},
		}}
		doc.DefaultConnection = "测试连接"
		doc.DefaultModel = "m1"
	}); err != nil {
		t.Fatalf("写应用目录设置失败：%v", err)
	}
	eng := newAppEngine(w, h)
	h.engine = eng

	// 在 A 的会话里提交前检查通过；B 里也一样（设置只有一份）
	for _, id := range []string{"ws-a", "ws-b"} {
		h.baseURL = startServer(t, w.serve(h.t, h, eng, w.workA, nil))
		created := h.createSessionIn(id)
		if created.WorkspaceID != id {
			t.Fatalf("会话应记下工作区 %s：%+v", id, created)
		}
		if !h.preflightOK() {
			t.Fatalf("工作区 %s 应共用应用目录里的设置，提交前检查应通过", id)
		}
	}
	// 设置文件只在应用目录里
	if _, err := os.Stat(filepath.Join(w.appDir, "settings.json")); err != nil {
		t.Fatalf("设置应在应用目录：%v", err)
	}
	if _, err := os.Stat(filepath.Join(w.workA, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("工作区目录里不应有设置：%v", err)
	}
}

// preflightOK GET /api/preflight 是否通过。
func (h *harness) preflightOK() bool {
	h.t.Helper()
	status, data := h.send(http.MethodGet, "/api/preflight", "", "", nil)
	if status != http.StatusOK {
		h.t.Fatalf("提交前检查失败：%d %s", status, data)
	}
	return decode[struct {
		OK bool `json:"ok"`
	}](h.t, data).OK
}

func TestSessionsAreFilteredByWorkspace(t *testing.T) {
	h := newHarness(t, agent.ModelScript{})
	w := newAppWire(t, h)
	eng := newAppEngine(w, h)
	h.engine = eng
	h.baseURL = startServer(t, w.serve(h.t, h, eng, w.workA, nil))

	inA := h.createSessionIn("ws-a")
	inB := h.createSessionIn("ws-b")

	onlyB := h.sessionsList("?workspaceId=ws-b")
	if len(onlyB) != 1 || onlyB[0].ID != inB.ID || onlyB[0].WorkspaceID != "ws-b" {
		t.Fatalf("workspaceId=ws-b 应只有 B 的会话：%+v", onlyB)
	}
	all := h.sessionsList("")
	seen := map[string]string{}
	for _, item := range all {
		seen[item.ID] = item.WorkspaceID
	}
	if seen[inA.ID] != "ws-a" || seen[inB.ID] != "ws-b" {
		t.Fatalf("不带过滤时应列出两个工作区的会话并各自带工作区：%+v", all)
	}
	// 会话在应用目录里，不写进工作区目录
	if _, err := os.Stat(filepath.Join(w.appDir, "sessions", inA.ID, "log.jsonl")); err != nil {
		t.Fatalf("会话日志应在应用目录里：%v", err)
	}
	if entries, err := os.ReadDir(w.workA); err == nil {
		for _, entry := range entries {
			if entry.Name() == "sessions" || entry.Name() == "tasks" {
				t.Fatalf("工作区目录里不应有 %s：%v", entry.Name(), err)
			}
		}
	}
}

func TestCreateSessionNeedsAWorkspace(t *testing.T) {
	h := newHarness(t, agent.ModelScript{})
	w := newAppWire(t, h)
	eng := newAppEngine(w, h)
	h.engine = eng
	h.baseURL = startServer(t, w.serve(h.t, h, eng, w.workA, nil))

	status, data := h.tryCreateSession(nil)
	if status != http.StatusBadRequest {
		t.Fatalf("空请求体（没有 workspaceId）应被拒绝：%d %s", status, data)
	}
	status, data = h.tryCreateSession(map[string]any{"name": "不算工作区"})
	if status != http.StatusBadRequest {
		t.Fatalf("没有 workspaceId 应被拒绝：%d %s", status, data)
	}
	status, data = h.tryCreateSession(map[string]any{"workspaceId": "ws-unknown"})
	if status != http.StatusNotFound {
		t.Fatalf("工作区不存在应返回 404：%d %s", status, data)
	}
	if sessions := h.sessionsList(""); len(sessions) != 0 {
		t.Fatalf("被拒绝的请求不应建会话：%+v", sessions)
	}
}

func TestTaskCreatedRecordsTheWorkspace(t *testing.T) {
	h := newHarness(t, frontdeskScript(modelSays("好的")))
	w := newAppWire(t, h)
	// 应用目录里配好一个带 Key 的连接：提交前检查读的是它，而不是主接缝目录里的设置
	if _, err := settings.Update(w.appDir, func(doc *contract.SettingsDocument) {
		doc.Connections = []contract.ModelConnection{{
			Name: "测试连接", BaseURL: "http://127.0.0.1:9/v1", APIKey: "sk-t36-test", Models: []string{"m1"},
		}}
		doc.DefaultConnection = "测试连接"
		doc.DefaultModel = "m1"
	}); err != nil {
		t.Fatalf("写应用目录设置失败：%v", err)
	}
	eng := newAppEngine(w, h)
	h.engine = eng
	h.baseURL = startServer(t, w.serve(h.t, h, eng, w.workB, nil))

	created := h.createSessionIn("ws-b")
	h.confirmed(created.ID, harnessEmployee)
	if status, data := h.uploadTo(created.ID, a01Files(t)); status != http.StatusOK {
		h.t.Fatalf("上传失败：%d %s", status, data)
	}
	reply := h.submit(created.ID, week44Form)
	taskID := reply.Session.TaskID
	if taskID == "" {
		t.Fatalf("提交后没有任务：%+v", reply.Session)
	}

	events := h.taskEvents(taskID)
	if len(events) == 0 || events[0].Type != contract.EventTaskCreated || events[0].WorkspaceID != "ws-b" {
		t.Fatalf("发起事件应记下工作区 ws-b：%+v", events)
	}
	detail := h.readSession(created.ID)
	if detail.WorkspaceID != "ws-b" {
		t.Fatalf("会话的工作区 = %q", detail.WorkspaceID)
	}
}

func TestSingleDirectoryModeTakesTheOnlyWorkspace(t *testing.T) {
	// 主接缝就是单目录模式：工作区目录同时是应用目录，不传 workspaceId 时取唯一的工作区
	h := newHarness(t, agent.ModelScript{})
	created := h.createSession()
	if created.WorkspaceID == "" {
		t.Fatalf("单目录模式的会话也要记下工作区：%+v", created)
	}
	only := h.sessionsList("?workspaceId=" + created.WorkspaceID)
	if len(only) != 1 || only[0].ID != created.ID {
		t.Fatalf("按唯一工作区过滤应列出这个会话：%+v", only)
	}
	// 任务也归这个唯一工作区
	if _, err := os.Stat(filepath.Join(h.workspace, "sessions", created.ID, "log.jsonl")); err != nil {
		t.Fatalf("单目录模式会话日志在目录里：%v", err)
	}
}
