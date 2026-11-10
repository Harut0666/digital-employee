// 设置页后端（docs/design.md §14.1、Spec 0004），对应 packages/server/src/routes/settings.ts。
//
// 安全约定：
//   - API Key 永远不完整出现在任何响应里，只回末 4 位 + hasKey；Key 只来自设置里人填写的内容（Spec 0007）；
//   - 设置文档（v2）的所有写入都走 internal/settings 的统一入口，写入时 revision +1；
//   - 导出默认脱敏，诊断包永远是脱敏快照。
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/settings"
)

// faultableTools 可注入故障的工具（管理工具自身、web_search 与 query_task 不在其中）。
var faultableTools = []struct {
	Name  string
	Label string
}{
	{contract.ToolValidateSubmission, "表单与附件校验"},
	{contract.ToolLoadSales, "读取并清洗销售数据"},
	{contract.ToolComputeMetrics, "计算指标与异常"},
	{contract.ToolParseMinutes, "解析周会纪要"},
	{contract.ToolRenderReportDocx, "生成 Word 报告"},
}

var faultModes = map[string]bool{"timeout": true, "error": true, "invalid_output": true}

func (s *Server) settingsEnv() settings.Env {
	if s.opts.Env == nil {
		return nil
	}
	env := settings.Env{}
	for key, value := range s.opts.Env {
		env[key] = value
	}
	return env
}

func (s *Server) now() time.Time { return s.opts.Now() }

func (s *Server) audit(action, target, result string, r *http.Request, detail string) {
	role, ok := roleOf(r)
	if !ok {
		role = contract.Role(settings.Read(s.opts.AppDir).Permissions.DefaultRole)
	}
	appendAudit(s.opts.AppDir, s.log, AuditEntry{
		Role: string(role), Action: action, Target: target, Result: result, Detail: detail,
	})
}

// notifySettingsSaved 保存后重建模型提供者；失败返回说明（设置本身已写入）。
func (s *Server) notifySettingsSaved() (warning string) {
	if s.opts.OnSettingsSaved == nil {
		return ""
	}
	if err := s.opts.OnSettingsSaved(settings.LoadModelSettings(s.opts.AppDir)); err != nil {
		return err.Error()
	}
	return ""
}

// applyToolFaults 把故障开关下发给工具客户端（生产是进程内直连）。
func (s *Server) applyToolFaults(toolFaults map[string]string) (toolError string) {
	if s.opts.Faults != nil {
		s.opts.Faults.Tools = toolFaults
	}
	if s.opts.ToolClient == nil {
		return ""
	}
	args := map[string]any{"tools": toolFaults}
	result, err := s.opts.ToolClient.Call(contract.ToolAdminSetFaults, args)
	if err != nil {
		return err.Error()
	}
	if !result.OK {
		return result.Error
	}
	return ""
}

// validConnectionName 连接名作为 URL 路径段与配置里的引用名：不许为空、不许有路径分隔符或控制字符。
func validConnectionName(name string) bool {
	if name == "" || len(name) > 64 || strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if r == '/' || r == '\\' || r < 0x20 {
			return false
		}
	}
	return true
}

func isValidHTTPURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

// compactStamp 备份文件名用的紧凑时间戳（不含冒号、点，Windows 也能当文件名）。
func compactStamp(at time.Time) string {
	stamp := strings.NewReplacer("-", "", ":", "", ".", "").Replace(at.UTC().Format("2006-01-02T15:04:05.000Z"))
	if len(stamp) > 15 {
		stamp = stamp[:15]
	}
	return stamp
}

func uniquePath(parent, name string, isDir bool) string {
	ext := ""
	base := name
	if dot := strings.LastIndex(name, "."); dot > 0 && !isDir {
		base = name[:dot]
		ext = name[dot:]
	}
	candidate := filepath.Join(parent, name)
	for index := 2; ; index++ {
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
		candidate = filepath.Join(parent, fmt.Sprintf("%s-%d%s", base, index, ext))
	}
}

func employeeDirsOf(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{}
	}
	out := []string{}
	for _, entry := range entries {
		if entry.IsDir() && isFile(filepath.Join(dir, entry.Name(), "workflow.yaml")) {
			out = append(out, entry.Name())
		}
	}
	sort.Strings(out)
	return out
}

// copyEmployeeConfigs 把默认员工配置复制到目标目录：只复制含 workflow.yaml 的员工目录。
func copyEmployeeConfigs(source, target string) ([]string, error) {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return nil, err
	}
	copied := []string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		from := filepath.Join(source, entry.Name())
		if !isFile(filepath.Join(from, "workflow.yaml")) {
			continue
		}
		if err := copyDir(from, filepath.Join(target, entry.Name())); err != nil {
			return copied, err
		}
		copied = append(copied, entry.Name())
	}
	sort.Strings(copied)
	return copied, nil
}

// copyDir 递归复制目录。
func copyDir(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o644)
	})
}

// backupSettingsFile 导入 / 恢复前把当前 settings.json 备份到 config-versions/settings/<时间戳>.json。
func (s *Server) backupSettingsFile() *string {
	file := settings.Path(s.opts.AppDir)
	if !isFile(file) {
		return nil
	}
	dir := filepath.Join(s.opts.AppDir, "config-versions", "settings")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	target := uniquePath(dir, compactStamp(s.now())+".json", false)
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return nil
	}
	return &target
}

// ---- 诊断包 ----

type diagnosticsEvent struct {
	Seq  int64  `json:"seq"`
	Type string `json:"type"`
	TS   string `json:"ts"`
}

type diagnosticsTaskSummary struct {
	TaskID    string             `json:"taskId"`
	Employee  string             `json:"employee"`
	State     string             `json:"state"`
	UpdatedAt string             `json:"updatedAt"`
	Events    []diagnosticsEvent `json:"events"`
}

type diagnosticsInfo struct {
	OK          bool                     `json:"ok"`
	GeneratedAt string                   `json:"generatedAt"`
	App         diagnosticsApp           `json:"app"`
	Runtime     diagnosticsRuntime       `json:"runtime"`
	Workspace   diagnosticsWorkspace     `json:"workspace"`
	Settings    settings.AppView         `json:"settings"`
	Tasks       []diagnosticsTaskSummary `json:"tasks"`
}

type diagnosticsApp struct {
	Version         string `json:"version"`
	SettingsVersion int    `json:"settingsVersion"`
}

// diagnosticsRuntime 运行环境。字段名 bun 是旧契约遗留，现在装的是 Go 版本号（界面按 go 开头识别）。
type diagnosticsRuntime struct {
	Bun      string `json:"bun"`
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
}

type diagnosticsWorkspace struct {
	WorkspaceDir       string `json:"workspaceDir"`
	EmployeesDir       string `json:"employeesDir"`
	ConfigVersionsDir  string `json:"configVersionsDir"`
	SettingsFile       string `json:"settingsFile"`
	SettingsFileExists bool   `json:"settingsFileExists"`
}

// recentTaskSummaries 最近几个任务各自最后几条事件（给人看，不写回任务数据）。
func (s *Server) recentTaskSummaries(limit, eventsPerTask int) []diagnosticsTaskSummary {
	out := []diagnosticsTaskSummary{}
	if s.engine() == nil {
		return out
	}
	summaries := s.engine().List()
	sort.SliceStable(summaries, func(i, j int) bool { return summaries[i].UpdatedAt > summaries[j].UpdatedAt })
	if len(summaries) > limit {
		summaries = summaries[:limit]
	}
	for _, summary := range summaries {
		events := []contract.TaskEvent{}
		if fetched, err := s.engine().Events(summary.ID, 0); err == nil {
			events = fetched
		}
		if len(events) > eventsPerTask {
			events = events[len(events)-eventsPerTask:]
		}
		items := make([]diagnosticsEvent, 0, len(events))
		for _, event := range events {
			items = append(items, diagnosticsEvent{Seq: event.Seq, Type: event.Type, TS: event.TS})
		}
		out = append(out, diagnosticsTaskSummary{
			TaskID: summary.ID, Employee: summary.Employee, State: string(summary.State),
			UpdatedAt: summary.UpdatedAt, Events: items,
		})
	}
	return out
}

func (s *Server) diagnosticsInfo() diagnosticsInfo {
	doc := settings.Read(s.opts.AppDir)
	info := diagnosticsInfo{
		OK:          true,
		GeneratedAt: s.now().UTC().Format(time.RFC3339Nano),
		App: diagnosticsApp{
			Version:         s.opts.AppVersion,
			SettingsVersion: doc.Version,
		},
		Runtime: diagnosticsRuntime{
			Bun: runtime.Version(), Platform: runtime.GOOS, Arch: runtime.GOARCH,
		},
		Workspace: diagnosticsWorkspace{
			WorkspaceDir:       s.opts.AppDir,
			EmployeesDir:       s.opts.EmployeesDir,
			ConfigVersionsDir:  filepath.Join(s.opts.AppDir, "config-versions"),
			SettingsFile:       settings.Path(s.opts.AppDir),
			SettingsFileExists: isFile(settings.Path(s.opts.AppDir)),
		},
		Settings: settings.View(doc, s.settingsEnv()),
		Tasks:    s.recentTaskSummaries(3, 5),
	}
	return info
}

// ---- 处理函数 ----

// 设置、员工、知识库和配置版本都在应用目录里，所有工作区共用（Spec 0006）。

func (s *Server) handleSettingsConnections(w http.ResponseWriter, r *http.Request) {
	view := settings.View(settings.Read(s.opts.AppDir), s.settingsEnv())
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"lastModel":   view.LastModel,
		"connections": view.Connections,
	})
}

// parsedAdvanced 连接卡片“高级”区的三字段：缺省 = 保留原值；显式 null = 清除；值 = 校验后写入。
type parsedAdvanced struct {
	modelOptions map[string]contract.ModelOptions
	clearOptions bool
	timeoutMS    *int
	clearTimeout bool
	maxRetries   *int
	clearRetries bool
}

func (s *Server) handleSaveConnection(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validConnectionName(name) {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "连接名不能为空，且不能包含路径分隔符或控制字符"))
		return
	}
	fields, err := readBodyJSON(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, err.Error()))
		return
	}

	doc := settings.Read(s.opts.AppDir)
	existingIndex := -1
	for index := range doc.Connections {
		if doc.Connections[index].Name == name {
			existingIndex = index
			break
		}
	}
	var existing *contract.ModelConnection
	if existingIndex >= 0 {
		existing = &doc.Connections[existingIndex]
	}

	baseURL := existingURL(existing)
	if raw, present := fields["baseUrl"]; present {
		text, ok := raw.(string)
		if !ok || !isValidHTTPURL(text) {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "Base URL 必须是 http(s) 地址"))
			return
		}
		baseURL = strings.TrimSpace(text)
	} else if existing == nil {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "新增连接必须填写 Base URL"))
		return
	}

	var apiKey string
	if existing != nil {
		apiKey = existing.APIKey
	}
	if raw, present := fields["apiKey"]; present {
		text, ok := raw.(string)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "apiKey 必须是字符串"))
			return
		}
		if text != "" {
			apiKey = text
		}
	}

	var models []string
	if raw, present := fields["models"]; present {
		parsed, ok := parseModelsField(raw)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "models 必须是非空字符串数组"))
			return
		}
		models = parsed
	} else if existing != nil {
		models = append([]string{}, existing.Models...)
	} else {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "models 必须是非空字符串数组"))
		return
	}

	advanced, message := parseAdvancedFields(fields)
	if message != "" {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, message))
		return
	}

	next := contract.ModelConnection{Name: name, BaseURL: baseURL, APIKey: apiKey, Models: models}
	if existing != nil {
		next.ModelOptions = existing.ModelOptions
		next.TimeoutMS = existing.TimeoutMS
		next.MaxRetries = existing.MaxRetries
	}
	if advanced.modelOptions != nil {
		next.ModelOptions = advanced.modelOptions
	}
	if advanced.clearOptions {
		next.ModelOptions = nil
	}
	if advanced.timeoutMS != nil {
		next.TimeoutMS = advanced.timeoutMS
	}
	if advanced.clearTimeout {
		next.TimeoutMS = nil
	}
	if advanced.maxRetries != nil {
		next.MaxRetries = advanced.maxRetries
	}
	if advanced.clearRetries {
		next.MaxRetries = nil
	}

	updated, err := settings.Update(s.opts.AppDir, func(doc *contract.SettingsDocument) {
		if existingIndex >= 0 {
			doc.Connections[existingIndex] = next
		} else {
			doc.Connections = append(doc.Connections, next)
		}
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	warning := s.notifySettingsSaved()

	view := settings.View(updated, s.settingsEnv())
	var connection *settings.ConnectionView
	for index := range view.Connections {
		if view.Connections[index].Name == name {
			connection = &view.Connections[index]
			break
		}
	}
	if connection == nil && len(view.Connections) > 0 {
		connection = &view.Connections[0]
	}
	response := map[string]any{"ok": true, "connection": connection}
	if warning != "" {
		response["warning"] = warning
	}
	writeJSON(w, http.StatusOK, response)
}

func existingURL(existing *contract.ModelConnection) string {
	if existing == nil {
		return ""
	}
	return existing.BaseURL
}

// parseModelsField 模型列表：逐项 trim、不许为空、去重。
func parseModelsField(raw any) ([]string, bool) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	seen := map[string]bool{}
	models := []string{}
	for _, item := range arr {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, false
		}
		if !seen[text] {
			seen[text] = true
			models = append(models, text)
		}
	}
	return models, true
}

// parseAdvancedFields 校验连接高级参数：缺省保留、null 清除、有值写入。
func parseAdvancedFields(fields map[string]any) (parsedAdvanced, string) {
	var out parsedAdvanced
	if raw, present := fields["modelOptions"]; present {
		if raw == nil {
			out.clearOptions = true
		} else {
			options, ok := raw.(map[string]any)
			if !ok {
				return out, "modelOptions 必须是 模型 id → 高级参数 的对象"
			}
			result := map[string]contract.ModelOptions{}
			for modelID, value := range options {
				if strings.TrimSpace(modelID) != modelID || modelID == "" {
					return out, "modelOptions 的模型 id 不能为空，也不能有首尾空格"
				}
				optionMap, ok := value.(map[string]any)
				if !ok {
					return out, fmt.Sprintf("模型 %s 的高级参数不合法", modelID)
				}
				option, message := parseModelOptions(optionMap)
				if message != "" {
					return out, fmt.Sprintf("模型 %s 的高级参数不合法（%s）", modelID, message)
				}
				result[modelID] = option
			}
			out.modelOptions = result
		}
	}
	if raw, present := fields["timeoutMs"]; present {
		if raw == nil {
			out.clearTimeout = true
		} else {
			n, ok := jsonInt(raw)
			if !ok || n < contract.ConnectionTimeoutMinMS || n > contract.ConnectionTimeoutMaxMS {
				return out, fmt.Sprintf("timeoutMs 必须是 %d–%d 之间的整数", contract.ConnectionTimeoutMinMS, contract.ConnectionTimeoutMaxMS)
			}
			out.timeoutMS = &n
		}
	}
	if raw, present := fields["maxRetries"]; present {
		if raw == nil {
			out.clearRetries = true
		} else {
			n, ok := jsonInt(raw)
			if !ok || n < 0 || n > contract.ConnectionRetriesMax {
				return out, fmt.Sprintf("maxRetries 必须是 0–%d 之间的整数", contract.ConnectionRetriesMax)
			}
			out.maxRetries = &n
		}
	}
	return out, ""
}

func jsonInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if n != float64(int(n)) {
			return 0, false
		}
		return int(n), true
	case int:
		return n, true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

func parseModelOptions(m map[string]any) (contract.ModelOptions, string) {
	out := contract.ModelOptions{}
	for key, value := range m {
		switch key {
		case "contextWindow":
			n, ok := jsonInt(value)
			if !ok || n < contract.ContextWindowMin || n > contract.ContextWindowMax {
				return out, fmt.Sprintf("contextWindow 必须是 %d–%d 的整数", contract.ContextWindowMin, contract.ContextWindowMax)
			}
			out.ContextWindow = &n
		case "maxTokens":
			n, ok := jsonInt(value)
			if !ok || n < contract.MaxTokensMin || n > contract.MaxTokensMax {
				return out, fmt.Sprintf("maxTokens 必须是 %d–%d 的整数", contract.MaxTokensMin, contract.MaxTokensMax)
			}
			out.MaxTokens = &n
		case "reasoningEfforts":
			arr, ok := value.([]any)
			if !ok || len(arr) > len(contract.ThinkingLevels) {
				return out, "reasoningEfforts 必须是推理档位数组"
			}
			efforts := []string{}
			for _, item := range arr {
				text, ok := item.(string)
				if !ok || !containsStringValue(contract.ThinkingLevels, text) {
					return out, "reasoningEfforts 必须是推理档位数组"
				}
				efforts = append(efforts, text)
			}
			out.ReasoningEfforts = efforts
		case "defaultReasoningEffort":
			text, ok := value.(string)
			if !ok || !containsStringValue(contract.ThinkingLevels, text) {
				return out, "defaultReasoningEffort 必须是推理档位"
			}
			out.DefaultReasoningEffort = &text
		default:
			return out, fmt.Sprintf("%s 不是可识别的高级参数", key)
		}
	}
	return out, ""
}

func (s *Server) handleDeleteConnection(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	doc := settings.Read(s.opts.AppDir)
	index := -1
	for i := range doc.Connections {
		if doc.Connections[i].Name == name {
			index = i
			break
		}
	}
	if index < 0 {
		writeAPIError(w, http.StatusNotFound, apiError(contract.ErrNotFound, fmt.Sprintf("连接不存在：%s", name)))
		return
	}
	updated, err := settings.Update(s.opts.AppDir, func(doc *contract.SettingsDocument) {
		doc.Connections = append(doc.Connections[:index], doc.Connections[index+1:]...)
		// 最近选的模型在被删掉的连接上：清掉，缺省模型回到第一个有 Key 的连接
		if doc.LastModel != nil && doc.LastModel.Connection == name {
			doc.LastModel = nil
		}
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	warning := s.notifySettingsSaved()
	response := map[string]any{"ok": true, "connections": settings.View(updated, s.settingsEnv()).Connections}
	if warning != "" {
		response["warning"] = warning
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	effective := s.effectiveModels()
	connection, found := settings.FindConnection(effective.Connections, name)
	if !found {
		writeAPIError(w, http.StatusNotFound, apiError(contract.ErrNotFound, fmt.Sprintf("连接不存在：%s", name)))
		return
	}
	body, _ := readBodyJSON(r)
	requested := ""
	if value, ok := body["model"].(string); ok && strings.TrimSpace(value) != "" {
		requested = strings.TrimSpace(value)
	}
	model := requested
	if model == "" && len(connection.Models) > 0 {
		model = connection.Models[0]
	}
	if connection.APIKey == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "latencyMs": 0, "model": model, "error": "未配置 API Key，无法测试连接"})
		return
	}
	result := s.opts.ProbeConnection(connection, model)
	response := map[string]any{
		"ok": result.OK, "latencyMs": result.LatencyMS, "model": result.Model,
	}
	if response["model"] == "" {
		response["model"] = model
	}
	if result.Error != "" {
		response["error"] = redactAPIKey(result.Error, connection.APIKey)
	}
	writeJSON(w, http.StatusOK, response)
}

// handleFetchModels 拉取某个连接的模型列表（Spec 0006「模型列表与会话模型」）。
// 请求体可带未保存的 baseUrl、apiKey，方便新建连接时先拉取；连接不存在但给了 baseUrl 也能用。
// 只读：结果不写设置，由人勾选后随连接保存；失败返回原因，Key 已脱敏。
func (s *Server) handleFetchModels(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	fields, err := readBodyJSON(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, err.Error()))
		return
	}

	// 已有连接按设置里保存的 Key 拉取（Key 只来自设置文件）
	effective := s.effectiveModels()
	var existing *contract.ModelConnection
	for index := range effective.Connections {
		if effective.Connections[index].Name == name {
			connection := effective.Connections[index]
			existing = &connection
			break
		}
	}

	baseURL := existingURL(existing)
	if raw, present := fields["baseUrl"]; present {
		text, ok := raw.(string)
		if !ok || !isValidHTTPURL(text) {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "Base URL 必须是 http(s) 地址"))
			return
		}
		baseURL = strings.TrimSpace(text)
	} else if existing == nil {
		writeAPIError(w, http.StatusNotFound, apiError(contract.ErrNotFound, fmt.Sprintf("连接不存在：%s（新建连接时请先填写 Base URL）", name)))
		return
	}

	apiKey := ""
	if existing != nil {
		apiKey = existing.APIKey
	}
	if raw, present := fields["apiKey"]; present {
		text, ok := raw.(string)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "apiKey 必须是字符串"))
			return
		}
		if text != "" {
			apiKey = text
		}
	}

	timeout := agent.ModelListTimeout
	if s.opts.ModelListTimeout > 0 {
		timeout = s.opts.ModelListTimeout
	}
	models, err := agent.FetchModels(r.Context(), baseURL, apiKey, timeout)
	if err != nil {
		s.log.Debugf("拉取模型失败：连接 %s，%s", name, err.Error())
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "models": models})
}

func (s *Server) handleGetAppSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, settings.View(settings.Read(s.opts.AppDir), s.settingsEnv()))
}

// dangerousIntentOf 请求体是否意图切到危险权限（成功/失败都留痕）。
func dangerousIntentOf(body map[string]any) (sandbox, approval bool) {
	permissions, ok := body["permissions"].(map[string]any)
	if !ok {
		return false, false
	}
	sandboxMode, _ := permissions["sandboxMode"].(string)
	approvalPolicy, _ := permissions["approvalPolicy"].(string)
	return sandboxMode == "danger-full-access", approvalPolicy == "never"
}

func (s *Server) auditDangerousFailures(sandbox, approval bool, r *http.Request, detail string) {
	if sandbox {
		s.audit("set-sandbox-mode", "permissions.sandboxMode", "failure", r, detail)
	}
	if approval {
		s.audit("set-approval-policy", "permissions.approvalPolicy", "failure", r, detail)
	}
}

func conflictBody(revision int) map[string]any {
	return map[string]any{
		"ok": false, "error": "revision_conflict", "revision": revision,
		"message": "设置已被修改，请刷新后重试",
	}
}

func (s *Server) handlePatchAppSettings(w http.ResponseWriter, r *http.Request) {
	body, err := readBodyJSON(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, err.Error()))
		return
	}
	sandboxIntent, approvalIntent := dangerousIntentOf(body)
	patch, err := settings.ParsePatch(body)
	if err != nil {
		s.auditDangerousFailures(sandboxIntent, approvalIntent, r, err.Error())
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, err.Error()))
		return
	}
	if patch.DefaultEmployee != nil && *patch.DefaultEmployee != "" && !s.employeeExists(*patch.DefaultEmployee) {
		message := "设置值不合法（defaultEmployee：员工不存在）"
		s.auditDangerousFailures(sandboxIntent, approvalIntent, r, message)
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, message))
		return
	}
	current := settings.Read(s.opts.AppDir)
	if patch.Revision != current.Revision {
		message := "设置已被修改，请刷新后重试"
		s.auditDangerousFailures(sandboxIntent, approvalIntent, r, message)
		writeJSON(w, http.StatusConflict, conflictBody(current.Revision))
		return
	}
	written, err := settings.Update(s.opts.AppDir, func(doc *contract.SettingsDocument) {
		_ = settings.ApplyPatch(doc, patch)
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	s.log.SetLevel(written.Diagnostics.LogLevel)
	if sandboxIntent {
		s.audit("set-sandbox-mode", "permissions.sandboxMode", "success", r, "sandboxMode="+written.Permissions.SandboxMode)
	}
	if approvalIntent {
		s.audit("set-approval-policy", "permissions.approvalPolicy", "success", r, "approvalPolicy="+written.Permissions.ApprovalPolicy)
	}
	s.log.Debugf("设置已保存：revision %d", written.Revision)
	writeJSON(w, http.StatusOK, settings.View(written, s.settingsEnv()))
}

func (s *Server) handleSetRole(w http.ResponseWriter, r *http.Request) {
	body, err := readBodyJSON(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, err.Error()))
		return
	}
	role, ok := body["role"].(string)
	if !ok || !containsStringValue(contract.DefaultRoles, role) {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "设置值不合法（role 必须是 initiator / reviewer / operator）"))
		return
	}
	current := settings.Read(s.opts.AppDir)
	if rawRevision, present := body["revision"]; present {
		revision, ok := jsonInt(rawRevision)
		if !ok || revision < 0 {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "设置值不合法（revision 必须是非负整数）"))
			return
		}
		if revision != current.Revision {
			writeJSON(w, http.StatusConflict, conflictBody(current.Revision))
			return
		}
	}
	written, err := settings.Update(s.opts.AppDir, func(doc *contract.SettingsDocument) {
		doc.Permissions.DefaultRole = role
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "revision": written.Revision, "permissions": written.Permissions,
	})
}

func (s *Server) handleExportSettings(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("includeKeys")
	includeKeys := query == "true" || query == "1"
	filename := fmt.Sprintf("settings%s-%s.json", map[bool]string{true: "-with-keys", false: ""}[includeKeys], compactStamp(s.now()))
	doc := settings.Read(s.opts.AppDir)
	result := map[string]any{
		"ok":         true,
		"redacted":   !includeKeys,
		"filename":   filename,
		"document":   settings.Export(doc, includeKeys),
		"exportedAt": s.now().UTC().Format(time.RFC3339Nano),
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	if includeKeys {
		s.audit("export-settings-with-keys", filename, "success", r, "导出文件含明文 API Key（已二次确认）")
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleImportSettings(w http.ResponseWriter, r *http.Request) {
	body, err := readBodyJSON(r)
	if err != nil {
		s.audit("import-settings", "settings.json", "failure", r, err.Error())
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, err.Error()))
		return
	}
	candidate := settings.ImportBody(body)
	migrated, err := settings.ValidateImport(candidate)
	if err != nil {
		s.audit("import-settings", "settings.json", "failure", r, err.Error())
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, err.Error()))
		return
	}
	raw, ok := candidate.(map[string]any)
	if !ok {
		s.audit("import-settings", "settings.json", "failure", r, "设置文件必须是 JSON 对象")
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "设置文件必须是 JSON 对象"))
		return
	}
	current := settings.Read(s.opts.AppDir)
	backupPath := s.backupSettingsFile()
	imported := settings.Normalize(raw)
	// 导入的文档可能带着其它机器的 revision：以本机当前值 +1 继续，避免客户端旧 revision 误判
	imported.Revision = current.Revision
	written, err := settings.Write(s.opts.AppDir, imported)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	s.log.SetLevel(written.Diagnostics.LogLevel)
	if s.opts.Faults != nil {
		s.opts.Faults.CorruptTraceability = written.Demo.CorruptTraceability
		s.opts.Faults.CrashOnNextStep = written.Demo.CrashOnNextStep
	}
	toolError := s.applyToolFaults(written.Demo.ToolFaults)
	warning := s.notifySettingsSaved()
	result := map[string]any{
		"ok": true, "revision": written.Revision, "migrated": migrated,
	}
	if backupPath != nil {
		result["backupPath"] = *backupPath
	} else {
		result["backupPath"] = nil
	}
	if warning != "" {
		result["warning"] = warning
	}
	if toolError != "" {
		result["toolError"] = toolError
	}
	detail := fmt.Sprintf("revision=%d", written.Revision)
	if backupPath != nil {
		detail += "，旧文件已备份"
	}
	s.audit("import-settings", "settings.json", "success", r, detail)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.diagnosticsInfo())
}

func (s *Server) handleExportDiagnostics(w http.ResponseWriter, r *http.Request) {
	info := s.diagnosticsInfo()
	dir := filepath.Join(s.opts.AppDir, "diagnostics")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	path := uniquePath(dir, "diagnostic-"+compactStamp(s.now())+".json", false)
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": path, "generatedAt": info.GeneratedAt})
}

func (s *Server) handleOpenDiagnosticsFolder(w http.ResponseWriter, r *http.Request) {
	dir := filepath.Join(s.opts.AppDir, "diagnostics")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError("打开诊断目录失败："+err.Error()))
		return
	}
	if err := s.opts.OpenFolder(dir); err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError("打开诊断目录失败："+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": dir})
}

func (s *Server) handleOpenSettingsFile(w http.ResponseWriter, r *http.Request) {
	if _, err := settings.Ensure(s.opts.AppDir); err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	path := settings.Path(s.opts.AppDir)
	if err := s.opts.OpenFile(path); err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError("打开设置文件失败："+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": path})
}

func demoToolsView() []map[string]string {
	out := make([]map[string]string, 0, len(faultableTools))
	for _, tool := range faultableTools {
		out = append(out, map[string]string{"name": tool.Name, "label": tool.Label})
	}
	return out
}

func (s *Server) handleGetDemo(w http.ResponseWriter, r *http.Request) {
	demo := settings.LoadDemo(s.opts.AppDir)
	if demo.ToolFaults == nil {
		demo.ToolFaults = map[string]string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "toolFaults": demo.ToolFaults,
		"corruptTraceability": demo.CorruptTraceability,
		"crashOnNextStep":     demo.CrashOnNextStep,
		"tools":               demoToolsView(),
	})
}

func (s *Server) handlePutDemo(w http.ResponseWriter, r *http.Request) {
	body, err := readBodyJSON(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, err.Error()))
		return
	}
	patch := contract.DemoSettings{ToolFaults: map[string]string{}}
	setTools, setCorrupt, setCrash := false, false, false
	if raw, present := body["toolFaults"]; present {
		tools, ok := raw.(map[string]any)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "toolFaults 必须是 工具名 → timeout / error / invalid_output 的对象"))
			return
		}
		for tool, modeRaw := range tools {
			mode, ok := modeRaw.(string)
			if !ok || !faultModes[mode] || !isFaultableTool(tool) {
				writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "toolFaults 必须是 工具名 → timeout / error / invalid_output 的对象"))
				return
			}
			patch.ToolFaults[tool] = mode
		}
		setTools = true
	}
	for _, key := range []string{"corruptTraceability", "crashOnNextStep"} {
		raw, present := body[key]
		if !present {
			continue
		}
		value, ok := raw.(bool)
		if !ok {
			writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, key+" 必须是布尔值"))
			return
		}
		if key == "corruptTraceability" {
			patch.CorruptTraceability = value
			setCorrupt = true
		} else {
			patch.CrashOnNextStep = value
			setCrash = true
		}
	}
	demo, err := settings.SaveDemo(s.opts.AppDir, patch, setTools, setCorrupt, setCrash)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	if demo.ToolFaults == nil {
		demo.ToolFaults = map[string]string{}
	}
	if s.opts.Faults != nil {
		s.opts.Faults.CorruptTraceability = demo.CorruptTraceability
		s.opts.Faults.CrashOnNextStep = demo.CrashOnNextStep
	}
	if setCrash && demo.CrashOnNextStep {
		s.audit("simulate-crash", "demo.crashOnNextStep", "success", r, "下一次步骤开始后进程退出（一次性）")
	}
	toolError := s.applyToolFaults(demo.ToolFaults)
	response := map[string]any{"ok": true, "demo": demo, "tools": demoToolsView()}
	if toolError != "" {
		response["toolError"] = toolError
	}
	writeJSON(w, http.StatusOK, response)
}

func isFaultableTool(name string) bool {
	for _, tool := range faultableTools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func (s *Server) handleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                  true,
		"appDir":              s.opts.AppDir,
		"workspaceDir":        s.workspaceSampleDir(),
		"employeesDir":        s.opts.EmployeesDir,
		"defaultEmployeesDir": s.opts.DefaultEmployeesDir,
		"configVersionsDir":   filepath.Join(s.opts.AppDir, "config-versions"),
	})
}

// handleOpenWorkspace 在资源管理器里打开目录：?dir= 指定工作区目录，缺省打开应用目录。
func (s *Server) handleOpenWorkspace(w http.ResponseWriter, r *http.Request) {
	body, _ := readBodyJSON(r)
	dir, _ := body["dir"].(string)
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = s.opts.AppDir
	} else if !s.workspaceDirRegistered(dir) {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "只能打开登记过的工作区目录"))
		return
	}
	if err := s.opts.OpenFolder(dir); err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError("打开工作区失败："+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": dir})
}

// workspaceDirRegistered 这个目录是不是某个登记过的工作区。
func (s *Server) workspaceDirRegistered(dir string) bool {
	clean := filepath.Clean(dir)
	for _, item := range s.workspaceDirs() {
		if sameFilePath(item.Dir, clean) {
			return true
		}
	}
	return false
}

func (s *Server) handleResetEmployees(w http.ResponseWriter, r *http.Request) {
	destination, err := filepath.Abs(s.opts.EmployeesDir)
	if err != nil {
		destination = s.opts.EmployeesDir
	}
	source, err := filepath.Abs(s.opts.DefaultEmployeesDir)
	if err != nil {
		source = s.opts.DefaultEmployeesDir
	}
	info, statErr := os.Stat(source)
	if statErr != nil || !info.IsDir() {
		message := fmt.Sprintf("默认员工配置目录不存在：%s", source)
		s.audit("reset-employees", destination, "failure", r, message)
		writeJSON(w, http.StatusInternalServerError, internalError(message))
		return
	}
	var backupPath *string
	if _, statErr := os.Stat(destination); statErr == nil {
		versions := filepath.Join(s.opts.AppDir, "config-versions", "employees")
		if err := os.MkdirAll(versions, 0o755); err != nil {
			s.audit("reset-employees", destination, "failure", r, err.Error())
			writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
			return
		}
		stamp := strings.NewReplacer("-", "", ":", "", "T", "").Replace(s.now().UTC().Format("2006-01-02T15:04:05Z"))
		target := uniquePath(versions, stamp, true)
		if err := copyDir(destination, target); err != nil {
			s.audit("reset-employees", destination, "failure", r, err.Error())
			writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
			return
		}
		backupPath = &target
	}
	if destination != source {
		if err := os.RemoveAll(destination); err != nil {
			s.audit("reset-employees", destination, "failure", r, err.Error())
			writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
			return
		}
		if _, err := copyEmployeeConfigs(source, destination); err != nil {
			s.audit("reset-employees", destination, "failure", r, err.Error())
			writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
			return
		}
	}
	detail := "没有旧配置，直接创建默认配置"
	if backupPath != nil {
		detail = "旧配置已备份到 config-versions/employees/"
	}
	s.audit("reset-employees", destination, "success", r, detail)
	response := map[string]any{"ok": true, "employees": employeeDirsOf(destination)}
	if backupPath != nil {
		response["backupPath"] = *backupPath
	} else {
		response["backupPath"] = nil
	}
	writeJSON(w, http.StatusOK, response)
}

var _ = io.EOF
