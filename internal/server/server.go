// Package server 是 Go 版后端：net/http REST + SSE，契约与 Elysia 版逐字段一致，
// 前端 packages/web 零改动可用。
//
// 路由分成六块（前五块与 packages/server/src 对应）：
//   - /api/tasks、/api/employees、/api/samples：任务与员工（index.ts）
//   - /api/sessions：会话与前台 Agent（internal/session）
//   - /api/knowledge：知识库（internal/knowledge）
//   - /api/settings/*：设置中心（routes/settings.ts）
//   - /api/employees/:id/config/*：员工配置页与版本（routes/config.ts）
//   - 其余路径：托管 packages/web/dist，非 /api 找不到文件时回退 index.html（files.ts）
package server

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/knowledge"
	"digital-employee/internal/session"
)

// Options 服务端依赖。除 Engine 外都有默认实现，方便测试注入。
type Options struct {
	// Engine 任务引擎（唯一入口，契约见 internal/contract）。整个进程只有一个。
	Engine contract.Engine
	// AppDir 应用目录：设置、员工、知识库、配置版本、会话和任务都在这里，所有工作区共用；
	// 缺省 DE_WORKSPACE / DE_APP_DIR / %APPDATA%/digital-employee-app。
	AppDir string
	// Workspaces 登记过的工作区（ID 与目录，顺序即列样例时看哪个）。设置、员工、知识库不按工作区分，
	// 工作区目录只用来列样例。为空时是单目录模式：唯一的工作区就是 AppDir。
	// 与 WorkspaceDir 二选一（后者只指定单目录）。
	Workspaces []Workspace
	// ListWorkspaces 当前登记的工作区（宿主提供）；nil 时用启动时的快照 Workspaces。
	ListWorkspaces func() []Workspace
	// WorkspaceWritable 登记是否可改（新建、重命名、取消登记由宿主处理，这里只如实告诉界面）；
	// WorkspaceReadOnlyReason 只读时的原因，比如单目录模式由 DE_WORKSPACE 指定
	WorkspaceWritable       bool
	WorkspaceReadOnlyReason string
	// WorkspaceDir 单目录模式的工作区目录（同时是应用目录）；缺省 DE_WORKSPACE。
	WorkspaceDir string
	// EmployeesDir 员工配置目录（应用目录里的副本，所有工作区共用）。
	EmployeesDir string
	// DefaultEmployeesDir “恢复默认员工配置”的来源；缺省同 EmployeesDir。
	DefaultEmployeesDir string
	// WebDistDir 前端构建产物目录；为空时不托管静态资源。
	WebDistDir string
	// ToolClient 工具客户端，设置页下发故障开关用（可为 nil）。
	ToolClient contract.ToolClient
	// Faults 演示开关的运行时可变对象，与引擎共享同一个引用。
	Faults *contract.FaultHooks
	// OnSettingsSaved 设置保存后重建模型提供者，由启动入口提供。
	OnSettingsSaved func(contract.ModelSettings) error
	// LoadEmployee 员工配置加载器；缺省用本包的 LoadEmployeeConfig。
	LoadEmployee func(dir string) (*contract.EmployeeConfig, error)
	// ProbeConnection “测试连接”实现；缺省发真实 HTTP 请求。
	ProbeConnection func(conn contract.ModelConnection, model string) contract.ModelConnectionTest
	// ModelListTimeout “拉取模型”的超时；缺省 15 秒（测试里调小，避免等满）。
	ModelListTimeout time.Duration
	// OpenFolder 打开文件夹；缺省按平台调用 explorer/open/xdg-open。
	OpenFolder func(dir string) error
	// OpenFile 用系统编辑器打开文件；缺省按平台调用 notepad/open/xdg-open。
	OpenFile func(path string) error
	// Env 读取环境变量；nil 时读进程环境。
	Env map[string]string
	// Now 时钟（备份/诊断时间戳）；nil 时用 time.Now。
	Now func() time.Time
	// Logger 控制台日志；nil 时用默认 logger。
	Logger *Logger
	// AppVersion 诊断页显示的版本号。
	AppVersion string
	// Scripted 剧本模式：模型不走网络，提交前检查视为通过。
	Scripted bool
	// Model 取前台 Agent 当前可用的模型提供者；nil 时会话按模型不可用处理。
	Model func() agent.ModelProvider
	// single 由 New 推出：没有指定 Workspaces/ListWorkspaces 时的单目录模式（不值得从外面设）
	single bool
}

// Workspace 一个登记过的工作区：ID、名字与目录。
type Workspace struct {
	ID        string
	Name      string
	Path      string
	CreatedAt string
	UpdatedAt string
}

// Server HTTP 服务端。
type Server struct {
	opts     Options
	mux      *http.ServeMux
	log      *Logger
	sessions *session.Manager
	// knowledge 应用目录里的知识库，所有工作区共用
	knowledge *knowledge.Store
	// workspaces 登记过的工作区（ID → 目录）；单目录模式下只有一个。
	// 每次登记变化后由宿主调 SetWorkspaces 更新，写操作回摘要时用它取工作区名。
	workspaceMu sync.RWMutex
	workspaces  workspaceRegistry
}

// New 组装服务端（不监听；由调用方用 Handler 起 http.Server）。
func New(opts Options) *Server {
	s := &Server{opts: opts}
	s.resolveDefaults()
	s.knowledge = knowledge.Open(s.opts.AppDir)
	// 引擎写交付文件夹时按工作区 ID 找目录：宿主提供了 ListWorkspaces 就以它为准，登记变化马上生效。
	// 单目录模式（没有登记、只有一个目录同时是应用目录）按约定不写交付文件夹，不给引擎登记。
	if setter, ok := s.opts.Engine.(interface {
		SetWorkspaces(func(workspaceID string) (string, bool))
	}); ok {
		if s.singleWorkspace() {
			setter.SetWorkspaces(nil)
		} else {
			setter.SetWorkspaces(s.workspaceDir)
		}
	}
	s.sessions = s.newSessions()
	s.mux = http.NewServeMux()
	s.routes()
	return s
}

// Handler 返回带 CORS 与 SPA fallback 的 http.Handler。
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.serve(w, r)
	})
}

// Logger 暴露日志器（启动入口打印地址等用）。
func (s *Server) Logger() *Logger { return s.log }

// AppDir 应用目录。
func (s *Server) AppDir() string { return s.opts.AppDir }

func (s *Server) resolveDefaults() {
	env := s.env()
	if s.opts.AppDir == "" {
		s.opts.AppDir = resolveAppDir(env)
	}
	if s.opts.EmployeesDir == "" {
		s.opts.EmployeesDir = filepath.Join(s.opts.AppDir, "employees")
	}
	s.workspaces = workspaceRegistry{dirs: map[string]string{}, names: map[string]string{}}
	for _, item := range s.opts.Workspaces {
		if item.ID != "" && item.Path != "" {
			s.workspaces.dirs[item.ID] = item.Path
			s.workspaces.names[item.ID] = item.Name
		}
	}
	if !s.opts.WorkspaceWritable && s.opts.ListWorkspaces != nil {
		// 宿主管登记（能读到最新的登记），除非它说了只读（单目录模式）
		s.opts.WorkspaceWritable = s.opts.WorkspaceReadOnlyReason == ""
	}
	if len(s.workspaces.dirs) == 0 {
		// 单目录模式：唯一的工作区就是这个目录
		s.opts.single = true
		single := s.opts.WorkspaceDir
		if single == "" {
			single = s.opts.AppDir
		}
		s.workspaces.dirs[SingleWorkspaceID] = single
	}
	if s.opts.DefaultEmployeesDir == "" {
		s.opts.DefaultEmployeesDir = s.opts.EmployeesDir
	}
	if s.opts.LoadEmployee == nil {
		s.opts.LoadEmployee = LoadEmployeeConfig
	}
	if s.opts.Now == nil {
		s.opts.Now = time.Now
	}
	if s.opts.AppVersion == "" {
		s.opts.AppVersion = "0.1.0"
	}
	if s.opts.ProbeConnection == nil {
		s.opts.ProbeConnection = probeConnection
	}
	if s.opts.OpenFolder == nil {
		s.opts.OpenFolder = openFolderWithSystem
	}
	if s.opts.OpenFile == nil {
		s.opts.OpenFile = openFileWithSystem
	}
	s.log = s.opts.Logger
	if s.log == nil {
		s.log = NewLogger(LevelInfo, nil)
	}
}

// SingleWorkspaceID 单目录模式里唯一工作区的 ID。
const SingleWorkspaceID = "single"

// resolveAppDir 应用目录：DE_WORKSPACE（单目录模式）> DE_APP_DIR > %APPDATA%/digital-employee-app。
func resolveAppDir(env map[string]string) string {
	if dir := env["DE_WORKSPACE"]; dir != "" {
		return dir
	}
	if dir := env["DE_APP_DIR"]; dir != "" {
		return dir
	}
	appData := env["APPDATA"]
	if appData == "" {
		if home, err := os.UserHomeDir(); err == nil {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
	}
	if appData == "" {
		appData = "."
	}
	return filepath.Join(appData, "digital-employee-app")
}

func (s *Server) env() map[string]string {
	if s.opts.Env != nil {
		return s.opts.Env
	}
	return nil
}

// envValue 读注入的环境变量；没有注入时读进程环境。
func (s *Server) envValue(key string) string {
	if s.opts.Env != nil {
		if value, ok := s.opts.Env[key]; ok {
			return value
		}
		return ""
	}
	return os.Getenv(key)
}

func setCORS(w http.ResponseWriter) {
	header := w.Header()
	header.Set("Access-Control-Allow-Origin", "*")
	header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	header.Set("Access-Control-Allow-Headers", "Content-Type, x-role, Last-Event-ID")
	header.Set("Access-Control-Max-Age", "86400")
}

// registeredWorkspaces 登记过的工作区（ID 与目录）。
//
// 启动时传进来的是快照；宿主还提供了 ListWorkspaces 时以它为准：新建的工作区马上可用，
// 取消登记的工作区不再接受新会话（它们已有的会话和任务照旧）。
func (s *Server) registeredWorkspaces() []Workspace {
	if s.opts.ListWorkspaces != nil {
		return s.opts.ListWorkspaces()
	}
	s.workspaceMu.RLock()
	defer s.workspaceMu.RUnlock()
	out := make([]Workspace, 0, len(s.workspaces.dirs))
	for id, dir := range s.workspaces.dirs {
		out = append(out, Workspace{ID: id, Name: workspaceBaseName(dir), Path: dir})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// workspaceRegistry 服务端看到的工作区登记。
type workspaceRegistry struct {
	// dirs 工作区 ID → 目录
	dirs map[string]string
	// names 工作区 ID → 名字：取消登记后名字也留着，未归属的会话仍显示它原来的工作区名
	names map[string]string
}

// SetWorkspaces 宿主登记变化后调用：服务端按最新的这份校验工作区、给会话配工作区名。
func (s *Server) SetWorkspaces(items []Workspace) {
	next := workspaceRegistry{dirs: make(map[string]string, len(items)), names: map[string]string{}}
	s.workspaceMu.Lock()
	for _, item := range items {
		if item.ID == "" || item.Path == "" {
			continue
		}
		next.dirs[item.ID] = item.Path
		next.names[item.ID] = item.Name
	}
	s.workspaces.dirs = next.dirs
	for id, name := range next.names {
		s.workspaces.names[id] = name
	}
	s.workspaceMu.Unlock()
}

// workspaceBaseName 目录名，作为名字缺省时的显示名。
func workspaceBaseName(dir string) string {
	name := filepath.Base(filepath.Clean(dir))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return dir
	}
	return name
}

// workspaceDirs 工作区 ID → 目录，按 ID 排序（列表顺序稳定）。
func (s *Server) workspaceDirs() []struct{ ID, Dir string } {
	items := s.registeredWorkspaces()
	out := make([]struct{ ID, Dir string }, 0, len(items))
	for _, item := range items {
		out = append(out, struct{ ID, Dir string }{ID: item.ID, Dir: item.Path})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// onlyWorkspace 只有登记着一个工作区时返回它的 ID；否则返回空串。
func (s *Server) onlyWorkspace() string {
	items := s.registeredWorkspaces()
	if len(items) == 1 {
		return items[0].ID
	}
	return ""
}

// singleWorkspace 单目录模式：目录同时是应用目录和唯一的工作区（Spec 0006）。
func (s *Server) singleWorkspace() bool { return s.opts.single }

// workspaceDir 工作区 ID 对应的目录（写交付文件夹、前台 Agent 读目录文件、上传写进“材料”用）；
// 没有登记时找不到。单目录模式（没传登记、只有一个唯一工作区）返回 WorkspaceDir（没有时用应用目录）。
func (s *Server) workspaceDir(id string) (string, bool) {
	items := s.registeredWorkspaces()
	for _, item := range items {
		if item.ID == id && item.Path != "" {
			return item.Path, true
		}
	}
	if len(items) > 0 || id != SingleWorkspaceID {
		return "", false
	}
	if s.opts.WorkspaceDir != "" {
		return s.opts.WorkspaceDir, true
	}
	if s.opts.AppDir != "" {
		return s.opts.AppDir, true
	}
	return "", false
}

// workspaceRegistered 工作区 ID 是否登记过。
func (s *Server) workspaceRegistered(id string) bool {
	for _, item := range s.registeredWorkspaces() {
		if item.ID == id {
			return true
		}
	}
	return false
}

// workspaceSampleDir 公开样例放在哪里：单目录模式看工作区目录（同时是应用目录），应用目录模式看应用目录。
// 工作区目录里不再释放样例（T41），样例和评测用例只放在应用目录。
func (s *Server) workspaceSampleDir() string {
	if s.opts.WorkspaceDir != "" {
		return s.opts.WorkspaceDir
	}
	return s.opts.AppDir
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	// 容忍尾部斜杠（Elysia 的 prefix 路由 /api/settings 与 /api/settings/ 等价）
	if path := r.URL.Path; len(path) > 1 && strings.HasSuffix(path, "/") {
		clone := r.Clone(r.Context())
		url := *r.URL
		url.Path = strings.TrimRight(path, "/")
		clone.URL = &url
		r = clone
	}
	s.mux.ServeHTTP(w, r)
}

// routes 注册全部端点。
func (s *Server) routes() {
	m := s.mux

	// ---- 任务与员工 ----
	m.HandleFunc("GET /api/employees", s.handleListEmployees)
	m.HandleFunc("GET /api/preflight", s.handlePreflight)
	m.HandleFunc("GET /api/tasks", s.handleListTasks)
	m.HandleFunc("POST /api/tasks", s.handleCreateTask)
	m.HandleFunc("GET /api/tasks/{id}", s.handleGetTask)
	m.HandleFunc("POST /api/tasks/{id}/commands", s.handleCommand)
	m.HandleFunc("GET /api/tasks/{id}/events", s.handleEvents)
	m.HandleFunc("GET /api/tasks/{id}/stream", s.handleStream)
	m.HandleFunc("GET /api/tasks/{id}/deliverables/{kind}", s.handleDeliverable)

	// ---- 公开样例 ----
	m.HandleFunc("GET /api/samples", s.handleListSamples)
	m.HandleFunc("POST /api/samples/{id}/tasks", s.handleCreateSampleTask)

	// ---- 会话 ----
	m.HandleFunc("GET /api/sessions", s.sessionRoute(s.handleListSessions))
	m.HandleFunc("POST /api/sessions", s.sessionRoute(s.handleCreateSession))
	m.HandleFunc("GET /api/sessions/{id}", s.sessionRoute(s.handleGetSession))
	m.HandleFunc("PATCH /api/sessions/{id}", s.sessionRoute(s.handlePatchSession))
	m.HandleFunc("PUT /api/sessions/{id}/model", s.sessionRoute(s.handleSelectSessionModel))
	m.HandleFunc("PUT /api/sessions/{id}/permission-mode", s.sessionRoute(s.handleSetPermissionMode))
	m.HandleFunc("POST /api/sessions/{id}/permissions/{requestId}", s.sessionRoute(s.handleDecidePermission))
	m.HandleFunc("POST /api/sessions/{id}/messages", s.sessionRoute(s.handleSendMessage))
	m.HandleFunc("GET /api/sessions/{id}/stream", s.sessionRoute(s.handleSessionStream))
	m.HandleFunc("GET /api/sessions/{id}/activity", s.sessionRoute(s.handleSessionActivity))
	m.HandleFunc("POST /api/sessions/{id}/stop", s.sessionRoute(s.handleStopTurn))
	m.HandleFunc("POST /api/sessions/{id}/employee", s.sessionRoute(s.handleConfirmEmployee))
	m.HandleFunc("POST /api/sessions/{id}/attachments", s.sessionRoute(s.handleSessionAttachments))
	m.HandleFunc("GET /api/sessions/{id}/folder-files", s.sessionRoute(s.handleListFolderFiles))
	m.HandleFunc("POST /api/sessions/{id}/folder-files", s.sessionRoute(s.handleUseFolderFiles))
	m.HandleFunc("POST /api/sessions/{id}/submit", s.sessionRoute(s.handleSubmitSession))
	m.HandleFunc("POST /api/sessions/{id}/checklist/{item}/skip", s.sessionRoute(s.handleSkipChecklistItem))
	m.HandleFunc("POST /api/sessions/{id}/filing-draft", s.sessionRoute(s.handleDraftFiling))

	// ---- 工作区与记录（T38；新建、重命名、取消登记由宿主处理） ----
	m.HandleFunc("GET /api/workspaces", s.handleListWorkspaces)
	m.HandleFunc("POST /api/workspaces/{id}/open", s.handleOpenWorkspaceFolder)
	m.HandleFunc("GET /api/records", s.handleRecords)

	// ---- 知识库 ----
	m.HandleFunc("GET /api/knowledge", s.handleListKnowledge)
	m.HandleFunc("POST /api/knowledge", s.handleFileKnowledge)
	m.HandleFunc("GET /api/knowledge/{id}", s.handleReadKnowledge)
	m.HandleFunc("DELETE /api/knowledge/{id}", s.handleDeleteKnowledge)

	// ---- 设置中心 ----
	m.HandleFunc("GET /api/settings", s.handleSettingsConnections)
	m.HandleFunc("GET /api/settings/models", s.handleModelOptions)
	m.HandleFunc("PUT /api/settings/connections/{name}", s.handleSaveConnection)
	m.HandleFunc("DELETE /api/settings/connections/{name}", s.handleDeleteConnection)
	m.HandleFunc("POST /api/settings/connections/{name}/test", s.handleTestConnection)
	m.HandleFunc("POST /api/settings/connections/{name}/models", s.handleFetchModels)
	m.HandleFunc("GET /api/settings/app", s.handleGetAppSettings)
	m.HandleFunc("PATCH /api/settings/app", s.handlePatchAppSettings)
	m.HandleFunc("PUT /api/settings/role", s.handleSetRole)
	m.HandleFunc("GET /api/settings/export", s.handleExportSettings)
	m.HandleFunc("POST /api/settings/import", s.handleImportSettings)
	m.HandleFunc("GET /api/settings/diagnostics", s.handleDiagnostics)
	m.HandleFunc("POST /api/settings/diagnostics/export", s.handleExportDiagnostics)
	m.HandleFunc("POST /api/settings/diagnostics/open", s.handleOpenDiagnosticsFolder)
	m.HandleFunc("POST /api/settings/open-file", s.handleOpenSettingsFile)
	m.HandleFunc("GET /api/settings/demo", s.handleGetDemo)
	m.HandleFunc("PUT /api/settings/demo", s.handlePutDemo)
	m.HandleFunc("GET /api/settings/workspace", s.handleGetWorkspace)
	m.HandleFunc("POST /api/settings/workspace/open", s.handleOpenWorkspace)
	m.HandleFunc("POST /api/settings/workspace/reset-employees", s.handleResetEmployees)

	// ---- 员工配置页 ----
	m.HandleFunc("GET /api/employees/{id}/config", s.handleGetEmployeeConfig)
	m.HandleFunc("GET /api/employees/{id}/config/versions", s.handleListConfigVersions)
	m.HandleFunc("GET /api/employees/{id}/config/versions/{version}/diff", s.handleDiffConfigVersion)
	m.HandleFunc("POST /api/employees/{id}/config/versions/{version}/restore", s.handleRestoreConfigVersion)
	m.HandleFunc("PUT /api/employees/{id}/config/{path...}", s.handleSaveConfigFile)

	// ---- 静态资源与 /api 兜底 ----
	m.HandleFunc("/", s.handleRoot)
}

// handleRoot 托管前端构建产物；/api 下的未知路径返回 JSON 404。
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/api" || strings.HasPrefix(path, "/api/") {
		writeAPIError(w, http.StatusNotFound, apiError("not_found", "路径不存在"))
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeAPIError(w, http.StatusNotFound, apiError("not_found", "路径不存在"))
		return
	}
	if s.opts.WebDistDir == "" {
		writeAPIError(w, http.StatusNotFound, apiError("not_found", "路径不存在"))
		return
	}
	if serveStatic(w, r, s.opts.WebDistDir) {
		return
	}
	writeAPIError(w, http.StatusNotFound, apiError("not_found", "路径不存在"))
}

// logf 便捷日志。
func (s *Server) logf(level string, format string, args ...any) {
	switch level {
	case "error":
		s.log.Errorf(format, args...)
	case "warn":
		s.log.Warnf(format, args...)
	case "debug":
		s.log.Debugf(format, args...)
	default:
		s.log.Infof(format, args...)
	}
}

var _ = log.Println
