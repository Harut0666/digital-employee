// 应用宿主（Spec 0006、ADR-0006）：整个进程只有一个引擎和一个服务端，设置、员工、知识库全局共用。
// /api/workspaces 在这里处理：列出、新建、重命名、取消登记；其余请求转给服务端。
//
// 工作区只是目录：新建、重命名、取消登记都不动引擎，也不需要“切换”。取消登记只删登记项，
// 目录里的文件、会话和任务都保留；之后它们的会话归入侧栏末尾的“未归属”分组（T38）。
package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"digital-employee/internal/contract"
	"digital-employee/internal/server"
	"digital-employee/internal/settings"
)

// workspaceHost 应用宿主：一个服务端、一份工作区登记。
type workspaceHost struct {
	options    Options
	env        map[string]string
	logger     *server.Logger
	repoRoot   string
	scriptFile string
	now        func() time.Time

	// single 单目录模式（DE_WORKSPACE / 命令行指定目录）：目录同时是应用目录和唯一的工作区，
	// 登记只读，不读写 workspaces.json
	single   bool
	singleID string
	// layout 启动时的布局（应用目录、单目录模式的工作区）
	layout   Layout
	registry *workspaceRegistry

	mu      sync.Mutex
	server  http.Handler
	runtime *workspaceRuntime
}

// workspaceRuntime 进程里唯一的引擎与工具。
type workspaceRuntime struct {
	engine   contract.Engine
	tool     contract.ToolClient
	ownsTool bool
}

// ServeHTTP /api/workspaces 的 POST 和 PATCH、DELETE 由宿主处理（登记），其余（含 GET 工作区列表和会话摘要）转给服务端。
func (h *workspaceHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimRight(r.URL.Path, "/")
	if r.Method != http.MethodOptions && r.Method != http.MethodGet && (path == "/api/workspaces" || strings.HasPrefix(path, "/api/workspaces/")) {
		setHostCORS(w)
		h.route(w, r, path)
		return
	}
	h.server.ServeHTTP(w, r)
}

// workspaces 登记过的工作区的快照（ID → 目录），服务端用来校验 workspaceId。
// 返回副本：服务端启动后登记还会变（取消登记），快照不受影响——会话和任务照样都在应用目录里。
func (h *workspaceHost) workspaces() []server.Workspace {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.single {
		return []server.Workspace{{ID: h.singleID, Path: h.singleDir()}}
	}
	return h.registry.dirs()
}

// singleDir 单目录模式的工作区目录（同时是应用目录）。
func (h *workspaceHost) singleDir() string {
	if h.layout.AppDir != "" {
		return h.layout.AppDir
	}
	return ResolveAppDir(h.env)
}

// nowTime 登记项的创建 / 更新时间。
func (h *workspaceHost) nowTime() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// stampLayout 登记项时间格式。
const stampLayout = "2006-01-02T15:04:05.000Z07:00"

func (h *workspaceHost) route(w http.ResponseWriter, r *http.Request, path string) {
	if path == "/api/workspaces" {
		switch r.Method {
		case http.MethodPost:
			h.handleCreate(w, r)
			return
		}
	}
	if rest, ok := strings.CutPrefix(path, "/api/workspaces/"); ok && rest != "" && !strings.Contains(rest, "/") {
		switch r.Method {
		case http.MethodPatch:
			h.handlePatch(w, r, rest)
			return
		case http.MethodDelete:
			h.handleDelete(w, rest)
			return
		}
	}
	if strings.HasSuffix(path, "/switch") {
		// Spec 0006 去掉了切换：所有工作区同时可用
		writeHostError(w, http.StatusNotFound, contract.ErrNotFound, "工作区不需要切换：所有工作区同时可用")
		return
	}
	writeHostError(w, http.StatusNotFound, contract.ErrNotFound, "路径不存在")
}

// errWorkspaceReadOnly 单目录模式：环境变量指定了目录，登记只读。
const errWorkspaceReadOnly = "workspace_read_only"

// workspaceView GET /api/workspaces 里的一项。
type workspaceView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	CreatedAt   string `json:"createdAt,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
	Missing     bool   `json:"missing"`
	MissingText string `json:"missingText,omitempty"`
}

// workspaceListView GET /api/workspaces 的响应。
type workspaceListView struct {
	ReadOnly   bool            `json:"readOnly"`
	Reason     string          `json:"reason,omitempty"`
	Workspaces []workspaceView `json:"workspaces"`
}

func (w Workspace) view() workspaceView {
	out := workspaceView{ID: w.ID, Name: w.Name, Path: w.Path, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt}
	if !dirExists(w.Path) {
		// 目录不存在：登记保留，会话仍可查看
		out.Missing = true
		out.MissingText = "目录缺失"
	}
	return out
}

// readOnlyReason 登记只读的原因：单目录模式下工作区由环境变量指定；否则为空（可改）。
func (h *workspaceHost) readOnlyReason() string {
	if h.single {
		return "工作区由环境变量 DE_WORKSPACE 指定，不能在界面上添加或取消登记"
	}
	return ""
}

func (h *workspaceHost) listView() workspaceListView {
	if h.single {
		dir := h.singleDir()
		only := Workspace{ID: h.singleID, Name: workspaceBaseName(dir), Path: dir}
		return workspaceListView{
			ReadOnly: true, Workspaces: []workspaceView{only.view()},
			Reason: h.readOnlyReason(),
		}
	}
	items := make([]workspaceView, 0, len(h.registry.Workspaces))
	for _, item := range h.registry.Workspaces {
		items = append(items, item.view())
	}
	return workspaceListView{Workspaces: items}
}

func (h *workspaceHost) handleList(w http.ResponseWriter) {
	h.mu.Lock()
	view := h.listView()
	h.mu.Unlock()
	writeHostJSON(w, http.StatusOK, view)
}

// workspaceNameProblem 名称校验：不能为空，最多 40 个字。
func workspaceNameProblem(name string) string {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(strings.TrimSpace(name)) > 40 {
		return "工作区名称不能为空，最多 40 个字"
	}
	return ""
}

// ListWorkspaces 当前登记的工作区（新建、重命名、取消登记后，服务端拉到最新的这份）。
func (h *workspaceHost) ListWorkspaces() []server.Workspace { return h.workspaces() }

// handleCreate POST /api/workspaces：新建一个工作区（名称 + 目录）。名称允许重名，目录不能重复登记。
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
	if h.single {
		writeHostError(w, http.StatusConflict, errWorkspaceReadOnly, h.listView().Reason)
		return
	}
	name := strings.TrimSpace(body.Name)
	if problem := workspaceNameProblem(name); problem != "" {
		writeHostError(w, http.StatusBadRequest, contract.ErrInvalidInput, problem)
		return
	}
	dir := strings.TrimSpace(body.Path)
	if dir == "" {
		// 没给目录：在应用目录的 workspaces/ 下建一个
		dir = filepath.Join(filepath.Dir(h.registry.file), "workspaces", NewWorkspaceID())
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
	// 工作区目录里只放“交付”文件夹（样例在应用目录）；出错不影响登记
	if _, err := PrepareWorkspaceDir(dir); err != nil {
		h.logger.Warnf("准备工作区目录 %s 失败：%v", dir, err)
	}
	created := h.registry.add(name, dir, h.nowTime().UTC())
	if err := h.registry.save(); err != nil {
		h.registry.Workspaces = h.registry.Workspaces[:len(h.registry.Workspaces)-1]
		writeHostError(w, http.StatusInternalServerError, "internal", fmt.Sprintf("保存工作区登记失败：%v", err))
		return
	}
	h.logger.Infof("已添加工作区 %s（%s）", created.Name, created.Path)
	writeHostJSON(w, http.StatusCreated, map[string]any{"ok": true, "workspace": created.view()})
}

// handlePatch PATCH /api/workspaces/{id}：重命名。
func (h *workspaceHost) handlePatch(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeHostError(w, http.StatusBadRequest, contract.ErrInvalidInput, "请求体不是合法的 JSON")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.single {
		writeHostError(w, http.StatusConflict, errWorkspaceReadOnly, h.listView().Reason)
		return
	}
	index := -1
	for i := range h.registry.Workspaces {
		if h.registry.Workspaces[i].ID == id {
			index = i
		}
	}
	if index < 0 {
		writeHostError(w, http.StatusNotFound, contract.ErrNotFound, fmt.Sprintf("工作区不存在：%s", id))
		return
	}
	name := strings.TrimSpace(body.Name)
	if problem := workspaceNameProblem(name); problem != "" {
		writeHostError(w, http.StatusBadRequest, contract.ErrInvalidInput, problem)
		return
	}
	previous := h.registry.Workspaces[index]
	updated := previous
	updated.Name = name
	updated.UpdatedAt = h.nowTime().UTC().Format(stampLayout)
	h.registry.Workspaces[index] = updated
	if err := h.registry.save(); err != nil {
		h.registry.Workspaces[index] = previous
		writeHostError(w, http.StatusInternalServerError, "internal", fmt.Sprintf("保存工作区登记失败：%v", err))
		return
	}
	writeHostJSON(w, http.StatusOK, map[string]any{"ok": true, "workspace": updated.view()})
}

// handleDelete DELETE /api/workspaces/{id}：取消登记。只删登记项，目录里的文件、会话和任务都保留。
func (h *workspaceHost) handleDelete(w http.ResponseWriter, id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.single {
		writeHostError(w, http.StatusConflict, errWorkspaceReadOnly, h.listView().Reason)
		return
	}
	target, found := h.registry.find(id)
	if !found {
		writeHostError(w, http.StatusNotFound, contract.ErrNotFound, fmt.Sprintf("工作区不存在：%s", id))
		return
	}
	kept := make([]Workspace, 0, len(h.registry.Workspaces))
	for _, item := range h.registry.Workspaces {
		if item.ID != id {
			kept = append(kept, item)
		}
	}
	previous := h.registry.Workspaces
	h.registry.Workspaces = kept
	if err := h.registry.save(); err != nil {
		h.registry.Workspaces = previous
		writeHostError(w, http.StatusInternalServerError, "internal", fmt.Sprintf("保存工作区登记失败：%v", err))
		return
	}
	h.logger.Infof("已取消登记工作区 %s（%s），目录和记录都保留", target.Name, target.Path)
	writeHostJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

// prepare 启动前的一次性准备：释放资源、读工作区登记（应用目录模式下先跑迁移，再补“我的工作区”）。
func (h *workspaceHost) prepare(layout Layout, doc contract.SettingsDocument) (*PreparedWorkspace, error) {
	h.layout = layout
	if h.single && h.options.SkipPrepare {
		// 单目录模式的布局就是目录本身，不需要登记
		return nil, nil
	}
	if h.single {
		// 单目录模式：布局和以前一样，这个目录同时是应用目录和唯一的工作区；不读写登记
		return PrepareAppDir(PrepareOptions{AppDir: layout.AppDir, RepoRoot: h.repoRoot, EmployeesDir: h.employeesOverride(), WebDistDir: h.webDistOverride()})
	}

	// 一次性迁移：旧默认工作区和 T32 登记过的工作区的数据搬进应用目录（有标记文件，可重复运行）
	if _, err := Migrate(Options{Env: h.env, AppDir: layout.AppDir, Logger: h.logger}); err != nil {
		return nil, err
	}
	registry, err := LoadRegistry(layout.AppDir)
	if err != nil {
		return nil, err
	}
	h.registry = registry
	if len(registry.Workspaces) == 0 {
		// 首次启动：建“我的工作区”
		defaultDir := DefaultWorkspaceDir(h.env)
		if err := os.MkdirAll(defaultDir, 0o755); err != nil {
			return nil, err
		}
		registry.add("我的工作区", defaultDir, h.nowTime().UTC())
	}
	if err := registry.save(); err != nil {
		return nil, fmt.Errorf("保存工作区登记失败：%w", err)
	}
	// 登记的目录里补“交付”文件夹（不释放样例）；目录缺失的不重建、不补东西，登记保留，界面上标“目录缺失”。
	// 只补“我的工作区”：迁移来的其他工作区在启动时不往里面写东西（它们只是保留登记）。
	for _, item := range registry.Workspaces {
		if item.Name != myWorkspaceName {
			continue
		}
		if !dirExists(item.Path) {
			h.logger.Warnf("工作区“%s”的目录不存在：%s", item.Name, item.Path)
			continue
		}
		if h.options.SkipPrepare {
			continue
		}
		if _, err := PrepareWorkspaceDir(item.Path); err != nil {
			h.logger.Warnf("准备工作区目录 %s 失败：%v", item.Path, err)
		}
	}
	var prepared *PreparedWorkspace
	if h.options.SkipPrepare {
		return nil, nil
	}
	// 应用目录：员工配置、样例、前端产物、设置。已有设置保留（迁移来的设置也在这里）
	prepared, err = PrepareAppDir(PrepareOptions{
		AppDir: layout.AppDir, RepoRoot: h.repoRoot,
		EmployeesDir: h.employeesOverride(), WebDistDir: h.webDistOverride(),
	})
	if err != nil {
		return nil, err
	}
	return prepared, nil
}

// employeesOverride 已指定的员工目录（DE_EMPLOYEES_DIR）。
func (h *workspaceHost) employeesOverride() string {
	if h.options.EmployeesDir != "" {
		return h.options.EmployeesDir
	}
	return envValue(h.env, "DE_EMPLOYEES_DIR")
}

// webDistOverride 已指定的前端产物目录（DE_WEB_DIST）。
func (h *workspaceHost) webDistOverride() string {
	if h.options.WebDistDir != "" {
		return h.options.WebDistDir
	}
	return envValue(h.env, "DE_WEB_DIST")
}

// build 建工具、引擎和服务端（整个进程只有一个）：引擎按应用目录找设置、知识库和任务，
// 服务端按登记的工作区校验 workspaceId。
func (h *workspaceHost) build(layout Layout, prepared *PreparedWorkspace, faults *contract.FaultHooks) (*workspaceRuntime, error) {
	h.layout = layout
	options := h.options
	env := h.env
	appDir := layout.AppDir

	employeesDir := options.EmployeesDir
	if employeesDir == "" && prepared != nil {
		employeesDir = prepared.EmployeesDir
	}
	if employeesDir == "" {
		employeesDir = envValue(env, "DE_EMPLOYEES_DIR")
	}
	if employeesDir == "" {
		employeesDir = filepath.Join(appDir, "employees")
	}
	defaultEmployeesDir := filepath.Join(appDir, runtimeDir, "default-employees")
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
	if err := os.MkdirAll(filepath.Join(appDir, "tasks"), 0o755); err != nil {
		return nil, err
	}

	// 演示开关持久化在 settings.json；运行时可变对象与引擎、设置路由共享。
	doc := settings.Read(appDir)
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

	modelSettings := settings.LoadModelSettings(appDir, settings.Env(env))
	var engine contract.Engine
	if options.EngineFactory != nil {
		created, err := options.EngineFactory(EngineDeps{
			ApplicationDir: appDir, WorkspaceDir: layout.Workspace.Path,
			// 任务目录里的配置副本读不出来时按员工 id 找应用目录的 employees/（应用目录模式下设置和知识库也在这里）
			ConfigFallback: func(employeeID string) (string, bool) {
				dir := filepath.Join(employeesDir, employeeID)
				if !dirExists(dir) {
					return "", false
				}
				return dir, true
			},
			EmployeesDir: employeesDir,
			// 登记的工作区：每个版本送审时把交付物写进它的“交付”文件夹（T42）；单目录模式不写
			Workspaces: h.workspaces(),
			ToolClient: toolClient, Faults: faults, ScriptFile: h.scriptFile,
			ModelSettings: modelSettings,
		})
		if err != nil {
			closeTool()
			return nil, fmt.Errorf("创建任务引擎失败：%w", err)
		}
		engine = created
	} else {
		// 没有引擎工厂：占位引擎只提供 HTTP 契约联调
		logger.Warnf("使用占位任务引擎（internal/engine 尚未接入）：只提供 HTTP 契约联调，不执行真实步骤")
		engine = NewMockEngine(logger, layout.AppDir)
	}

	srv := server.New(server.Options{
		Engine: engine,
		AppDir: appDir,
		// 登记的工作区：服务端据此校验 workspaceId，新建的工作区马上可用
		Workspaces:          h.workspaces(),
		ListWorkspaces:      h.ListWorkspaces,
		WorkspaceWritable:   !h.single,
		WorkspaceReadOnlyReason: h.readOnlyReason(),
		WorkspaceDir:        layout.Workspace.Path,
		EmployeesDir:        employeesDir,
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
	h.server = srv.Handler()
	// 会话和任务都在应用目录的 sessions/、tasks/ 下：启动时就建好，首次启动也一眼看得到
	for _, dir := range []string{"sessions", "tasks"} {
		if err := os.MkdirAll(filepath.Join(appDir, dir), 0o755); err != nil {
			closeTool()
			return nil, err
		}
	}
	logger.Debugf("应用目录 %s：员工目录 %s", appDir, employeesDir)
	return &workspaceRuntime{engine: engine, tool: toolClient, ownsTool: ownsTool}, nil
}

// resume 恢复未完成的任务。
func (h *workspaceHost) resume(runtime *workspaceRuntime) {
	if err := runtime.engine.ResumeAll(); err != nil {
		h.logger.Warnf("恢复未完成任务失败：%v", err)
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
	header.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
	header.Set("Access-Control-Allow-Headers", "Content-Type, x-role")
}

func writeHostError(w http.ResponseWriter, status int, code, message string) {
	writeHostJSON(w, status, map[string]any{"ok": false, "error": code, "message": message})
}

// close 关掉自己起的工具客户端。
func (runtime *workspaceRuntime) close() {
	if runtime.ownsTool && runtime.tool != nil {
		_ = runtime.tool.Close()
	}
}
