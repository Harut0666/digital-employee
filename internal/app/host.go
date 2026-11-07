// 工作区宿主：持有当前工作区的引擎、工具和服务端，/api/workspaces 在这里处理，其余请求转给当前服务端。
package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"digital-employee/internal/contract"
	"digital-employee/internal/server"
	"digital-employee/internal/settings"
)

// workspaceRuntime 一个工作区上的引擎、工具和服务端。
type workspaceRuntime struct {
	dir      string
	engine   contract.Engine
	tool     contract.ToolClient
	ownsTool bool
	handler  http.Handler
}

// workspaceHost 当前工作区和工作区列表。
type workspaceHost struct {
	options    Options
	env        map[string]string
	logger     *server.Logger
	repoRoot   string
	scriptFile string
	// locked 环境变量指定了工作区：切换入口只读，不读写工作区列表
	locked   bool
	registry *workspaceRegistry

	// mu 串行化新建与切换
	mu      sync.Mutex
	current atomic.Pointer[workspaceRuntime]
}

// ServeHTTP /api/workspaces 由宿主处理，其余转给当前工作区的服务端。
func (h *workspaceHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimRight(r.URL.Path, "/")
	if r.Method != http.MethodOptions && (path == "/api/workspaces" || strings.HasPrefix(path, "/api/workspaces/")) {
		setHostCORS(w)
		h.route(w, r, path)
		return
	}
	h.current.Load().handler.ServeHTTP(w, r)
}

func (h *workspaceHost) route(w http.ResponseWriter, r *http.Request, path string) {
	if path == "/api/workspaces" {
		switch r.Method {
		case http.MethodGet:
			h.handleList(w)
			return
		case http.MethodPost:
			h.handleCreate(w, r)
			return
		}
	}
	if rest, ok := strings.CutPrefix(path, "/api/workspaces/"); ok && r.Method == http.MethodPost {
		if id, ok := strings.CutSuffix(rest, "/switch"); ok && id != "" && !strings.Contains(id, "/") {
			h.handleSwitch(w, id)
			return
		}
	}
	writeHostError(w, http.StatusNotFound, contract.ErrNotFound, "路径不存在")
}

const (
	// errWorkspaceReadOnly 环境变量指定了工作区，不能新建或切换
	errWorkspaceReadOnly = "workspace_read_only"
	// errWorkspaceBusy 有任务处于准入校验、执行中或质检中，不能切换
	errWorkspaceBusy = "workspace_busy"
)

// stopEngine 停下引擎；不支持停下的引擎（占位引擎）只检查有没有活跃任务。
func stopEngine(engine contract.Engine) error {
	if stopper, ok := engine.(contract.EngineStopper); ok {
		return stopper.Stop()
	}
	busy := []contract.TaskID{}
	for _, summary := range engine.List() {
		switch summary.State {
		case contract.StateAdmission, contract.StateRunning, contract.StateQC:
			busy = append(busy, summary.ID)
		}
	}
	if len(busy) > 0 {
		return &contract.EngineBusyError{TaskIDs: busy}
	}
	return nil
}

func (h *workspaceHost) handleCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeHostError(w, http.StatusBadRequest, contract.ErrInvalidInput, "请求体不是合法的 JSON")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.locked {
		writeHostError(w, http.StatusConflict, errWorkspaceReadOnly, h.listView().Reason)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || utf8.RuneCountInString(name) > 40 {
		writeHostError(w, http.StatusBadRequest, contract.ErrInvalidInput, "工作区名称不能为空，最多 40 个字")
		return
	}
	for _, item := range h.registry.Workspaces {
		if item.Name == name {
			writeHostError(w, http.StatusConflict, contract.ErrInvalidInput, fmt.Sprintf("已有名为“%s”的工作区", name))
			return
		}
	}
	id := newWorkspaceID()
	dir := strings.TrimSpace(body.Path)
	if dir == "" {
		dir = filepath.Join(filepath.Dir(h.registry.file), "workspaces", id)
	} else if !filepath.IsAbs(dir) {
		writeHostError(w, http.StatusBadRequest, contract.ErrInvalidInput, "工作区目录必须是绝对路径")
		return
	}
	dir = filepath.Clean(dir)
	if existing, found := h.registry.findPath(dir); found {
		writeHostError(w, http.StatusConflict, contract.ErrInvalidInput, fmt.Sprintf("这个目录已经是工作区“%s”", existing.Name))
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeHostError(w, http.StatusBadRequest, contract.ErrInvalidInput, fmt.Sprintf("无法创建工作区目录：%v", err))
		return
	}
	created := Workspace{ID: id, Name: name, Path: dir}
	h.registry.Workspaces = append(h.registry.Workspaces, created)
	if err := h.registry.save(); err != nil {
		h.registry.Workspaces = h.registry.Workspaces[:len(h.registry.Workspaces)-1]
		writeHostError(w, http.StatusInternalServerError, "internal", fmt.Sprintf("保存工作区列表失败：%v", err))
		return
	}
	writeHostJSON(w, http.StatusCreated, map[string]any{"ok": true, "workspace": created})
}

func (h *workspaceHost) handleSwitch(w http.ResponseWriter, id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.locked {
		writeHostError(w, http.StatusConflict, errWorkspaceReadOnly, h.listView().Reason)
		return
	}
	target, found := h.registry.find(id)
	if !found {
		writeHostError(w, http.StatusNotFound, contract.ErrNotFound, fmt.Sprintf("工作区不存在：%s", id))
		return
	}
	if id == h.registry.Current {
		writeHostJSON(w, http.StatusOK, map[string]any{"ok": true, "current": target, "reload": false})
		return
	}
	old := h.current.Load()
	// 先停旧引擎：有任务在跑就拒绝，避免新旧引擎同时写入；停下后旧引擎不再写入
	if err := stopEngine(old.engine); err != nil {
		var busy *contract.EngineBusyError
		if errors.As(err, &busy) {
			writeHostJSON(w, http.StatusConflict, map[string]any{
				"ok": false, "error": errWorkspaceBusy, "taskIds": busy.TaskIDs,
				"message": "有任务正在准入校验、执行中或质检中，等它暂停或完成后再切换工作区。暂停中的任务不受影响。",
			})
			return
		}
		writeHostError(w, http.StatusInternalServerError, "internal", fmt.Sprintf("停止当前工作区失败：%v", err))
		return
	}
	next, err := h.build(target.Path, nil)
	if err != nil {
		// 新工作区打不开：重新打开原工作区，旧引擎已停，只能换一个新的
		if reopened, reopenErr := h.build(old.dir, nil); reopenErr == nil {
			h.current.Store(reopened)
			old.close()
			h.resume(reopened)
		} else {
			h.logger.Errorf("重新打开原工作区失败：%v", reopenErr)
		}
		writeHostError(w, http.StatusInternalServerError, "internal", fmt.Sprintf("打开工作区失败：%v", err))
		return
	}
	h.current.Store(next)
	old.close()
	h.registry.Current = id
	if err := h.registry.save(); err != nil {
		// 切换已生效，只是下次启动不会接着用它
		h.logger.Warnf("保存工作区列表失败：%v", err)
	}
	h.resume(next)
	h.logger.Infof("已切换到工作区 %s（%s）", target.Name, target.Path)
	writeHostJSON(w, http.StatusOK, map[string]any{"ok": true, "current": target, "reload": true})
}

// workspaceListView GET /api/workspaces 的响应。
type workspaceListView struct {
	Current    string      `json:"current"`
	ReadOnly   bool        `json:"readOnly"`
	Reason     string      `json:"reason,omitempty"`
	Workspaces []Workspace `json:"workspaces"`
}

func (h *workspaceHost) listView() workspaceListView {
	if h.locked {
		dir := h.current.Load().dir
		only := Workspace{ID: "env", Name: workspaceBaseName(dir), Path: dir}
		return workspaceListView{
			Current: only.ID, ReadOnly: true, Workspaces: []Workspace{only},
			Reason: "工作区由环境变量 DE_WORKSPACE 指定，不能在界面上切换",
		}
	}
	items := append([]Workspace{}, h.registry.Workspaces...)
	return workspaceListView{Current: h.registry.Current, Workspaces: items}
}

func (h *workspaceHost) handleList(w http.ResponseWriter) {
	h.mu.Lock()
	view := h.listView()
	h.mu.Unlock()
	writeHostJSON(w, http.StatusOK, view)
}

// build 在 dir 上准备工作区，建工具、引擎和服务端（不恢复任务，由调用方在接管请求后恢复）。
func (h *workspaceHost) build(dir string, faults *contract.FaultHooks) (*workspaceRuntime, error) {
	options := h.options
	env := h.env
	var prepared *PreparedWorkspace
	if !options.SkipPrepare {
		employeesOverride := options.EmployeesDir
		if employeesOverride == "" {
			employeesOverride = envValue(env, "DE_EMPLOYEES_DIR")
		}
		webOverride := options.WebDistDir
		if webOverride == "" {
			webOverride = envValue(env, "DE_WEB_DIST")
		}
		released, err := PrepareWorkspace(PrepareOptions{
			WorkspaceDir: dir,
			RepoRoot:     h.repoRoot,
			EmployeesDir: employeesOverride,
			WebDistDir:   webOverride,
		})
		if err != nil {
			return nil, err
		}
		prepared = released
	}

	employeesDir := options.EmployeesDir
	if employeesDir == "" && prepared != nil {
		employeesDir = prepared.EmployeesDir
	}
	if employeesDir == "" {
		employeesDir = envValue(env, "DE_EMPLOYEES_DIR")
	}
	if employeesDir == "" {
		employeesDir = filepath.Join(dir, "employees")
	}
	defaultEmployeesDir := filepath.Join(dir, runtimeDir, "default-employees")
	if prepared != nil && prepared.DefaultEmployeesDir != "" {
		defaultEmployeesDir = prepared.DefaultEmployeesDir
	}
	webDistDir := options.WebDistDir
	if webDistDir == "" && prepared != nil {
		webDistDir = prepared.WebDistDir
	}
	if webDistDir == "" {
		webDistDir = envValue(env, "DE_WEB_DIST")
	}
	if err := os.MkdirAll(filepath.Join(dir, "tasks"), 0o755); err != nil {
		return nil, err
	}

	// 演示开关持久化在 settings.json；运行时可变对象与引擎、设置路由共享。
	doc := settings.Read(dir)
	if faults == nil {
		faults = &contract.FaultHooks{}
	}
	faults.CorruptTraceability = doc.Demo.CorruptTraceability
	faults.CrashOnNextStep = doc.Demo.CrashOnNextStep
	if doc.Demo.ToolFaults != nil {
		faults.Tools = doc.Demo.ToolFaults
	}

	logger := h.logger
	var toolClient contract.ToolClient
	ownsTool := false
	if options.ToolFactory != nil {
		client, err := options.ToolFactory()
		if err != nil {
			return nil, fmt.Errorf("创建工具客户端失败：%w", err)
		}
		toolClient = client
		ownsTool = true
		if toolClient != nil {
			// 恢复上次演示留下的工具故障（设置页把开关持久化在 settings.json）
			if result, err := toolClient.Call(contract.ToolAdminSetFaults, map[string]any{"tools": doc.Demo.ToolFaults}); err != nil {
				logger.Warnf("工具故障开关恢复失败：%v", err)
			} else if !result.OK {
				logger.Warnf("工具故障开关恢复失败：%s", result.Error)
			}
		}
	}
	closeTool := func() {
		if ownsTool && toolClient != nil {
			_ = toolClient.Close()
		}
	}

	modelSettings := settings.LoadModelSettings(dir, settings.Env(env))
	var engine contract.Engine
	if options.EngineFactory != nil {
		created, err := options.EngineFactory(EngineDeps{
			WorkspaceDir: dir, EmployeesDir: employeesDir,
			ToolClient: toolClient, Faults: faults, ScriptFile: h.scriptFile,
			ModelSettings: modelSettings,
		})
		if err != nil {
			closeTool()
			return nil, fmt.Errorf("创建任务引擎失败：%w", err)
		}
		engine = created
	} else {
		logger.Warnf("使用占位任务引擎（internal/engine 尚未接入）：只提供 HTTP 契约联调，不执行真实步骤")
		engine = NewMockEngine(logger)
	}

	srv := server.New(server.Options{
		Engine:              engine,
		EmployeesDir:        employeesDir,
		WorkspaceDir:        dir,
		DefaultEmployeesDir: defaultEmployeesDir,
		WebDistDir:          webDistDir,
		ToolClient:          toolClient,
		Faults:              faults,
		OnSettingsSaved:     options.OnSettingsSaved,
		Model:               options.Model,
		LoadEmployee:        options.LoadEmployee,
		Env:                 env,
		Logger:              logger,
		AppVersion:          options.AppVersion,
		// 占位引擎同样不调模型
		Scripted: h.scriptFile != "" || options.EngineFactory == nil,
	})
	logger.Debugf("工作区 %s：员工目录 %s", dir, employeesDir)
	return &workspaceRuntime{dir: dir, engine: engine, tool: toolClient, ownsTool: ownsTool, handler: srv.Handler()}, nil
}

// resume 恢复当前工作区里未完成的任务。
func (h *workspaceHost) resume(runtime *workspaceRuntime) {
	if err := runtime.engine.ResumeAll(); err != nil {
		h.logger.Warnf("恢复未完成任务失败：%v", err)
	}
}

// close 关掉自己起的工具客户端。
func (runtime *workspaceRuntime) close() {
	if runtime.ownsTool && runtime.tool != nil {
		_ = runtime.tool.Close()
	}
}

func writeHostJSON(w http.ResponseWriter, status int, value any) {
	data, err := marshalIndentJSON(value)
	if err != nil {
		status = http.StatusInternalServerError
		data = []byte(`{"ok":false,"error":"internal","message":"响应序列化失败"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func setHostCORS(w http.ResponseWriter) {
	header := w.Header()
	header.Set("Access-Control-Allow-Origin", "*")
	header.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	header.Set("Access-Control-Allow-Headers", "Content-Type, x-role")
}

func writeHostError(w http.ResponseWriter, status int, code, message string) {
	writeHostJSON(w, status, map[string]any{"ok": false, "error": code, "message": message})
}
