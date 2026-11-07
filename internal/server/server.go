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
	"strings"
	"time"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/knowledge"
	"digital-employee/internal/session"
)

// Options 服务端依赖。除 Engine 外都有默认实现，方便测试注入。
type Options struct {
	// Engine 任务引擎（唯一入口，契约见 internal/contract）。
	Engine contract.Engine
	// EmployeesDir 员工配置目录（工作区里的副本）。
	EmployeesDir string
	// WorkspaceDir 工作区根目录；缺省 DE_WORKSPACE / %APPDATA%/digital-employee。
	WorkspaceDir string
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
}

// Server HTTP 服务端。
type Server struct {
	opts     Options
	mux      *http.ServeMux
	log      *Logger
	sessions *session.Manager
	// knowledge 本工作区的知识库
	knowledge *knowledge.Store
}

// New 组装服务端（不监听；由调用方用 Handler 起 http.Server）。
func New(opts Options) *Server {
	s := &Server{opts: opts}
	s.resolveDefaults()
	s.knowledge = knowledge.Open(s.opts.WorkspaceDir)
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

// WorkspaceDir 工作区根目录。
func (s *Server) WorkspaceDir() string { return s.opts.WorkspaceDir }

func (s *Server) resolveDefaults() {
	env := s.env()
	if s.opts.WorkspaceDir == "" {
		s.opts.WorkspaceDir = resolveWorkspace(env)
	}
	if s.opts.EmployeesDir == "" {
		s.opts.EmployeesDir = filepath.Join(s.opts.WorkspaceDir, "employees")
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

// resolveWorkspace 工作区：DE_WORKSPACE > %APPDATA%/digital-employee。
func resolveWorkspace(env map[string]string) string {
	if dir := env["DE_WORKSPACE"]; dir != "" {
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
	return filepath.Join(appData, "digital-employee")
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
	m.HandleFunc("POST /api/sessions/{id}/messages", s.sessionRoute(s.handleSendMessage))
	m.HandleFunc("POST /api/sessions/{id}/employee", s.sessionRoute(s.handleConfirmEmployee))
	m.HandleFunc("POST /api/sessions/{id}/attachments", s.sessionRoute(s.handleSessionAttachments))
	m.HandleFunc("POST /api/sessions/{id}/submit", s.sessionRoute(s.handleSubmitSession))
	m.HandleFunc("POST /api/sessions/{id}/filing-draft", s.sessionRoute(s.handleDraftFiling))

	// ---- 知识库 ----
	m.HandleFunc("GET /api/knowledge", s.handleListKnowledge)
	m.HandleFunc("POST /api/knowledge", s.handleFileKnowledge)
	m.HandleFunc("GET /api/knowledge/{id}", s.handleReadKnowledge)
	m.HandleFunc("DELETE /api/knowledge/{id}", s.handleDeleteKnowledge)

	// ---- 设置中心 ----
	m.HandleFunc("GET /api/settings", s.handleSettingsConnections)
	m.HandleFunc("PUT /api/settings/connections/{name}", s.handleSaveConnection)
	m.HandleFunc("DELETE /api/settings/connections/{name}", s.handleDeleteConnection)
	m.HandleFunc("POST /api/settings/connections/{name}/test", s.handleTestConnection)
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
