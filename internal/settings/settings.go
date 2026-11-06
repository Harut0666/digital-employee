// Package settings 实现 settings.json（设置文档 v2）的唯一读写入口。
//
// 移植自 packages/core/src/config/{settings-document,workspace}.ts：
//   - 读路径不写盘：v1 文档（无 version）只在内存里补默认值并视作 v2，迁移发生在首次写入时；
//   - 未识别的顶层键原样保留在 Extra 里，写入时再展开回去；
//   - revision 存在文档里，每次写入 +1，写入只经过 Write 一处；
//   - 密钥只经 View / Export 出去，默认脱敏（只回末 4 位 + hasKey + source）。
package settings

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"digital-employee/internal/contract"
)

// FileName 设置文件名（工作区根目录下）。
const FileName = "settings.json"

// 默认模型连接：没有任何 settings.json 时用它。
const (
	DefaultConnectionName = "deepseek"
	DefaultDefaultModel   = "deepseek-chat"
	DefaultBaseURL        = "https://api.deepseek.com"
)

// DefaultConnections 默认连接列表。
var DefaultConnections = []contract.ModelConnection{
	{Name: DefaultConnectionName, BaseURL: DefaultBaseURL, APIKey: "", Models: []string{DefaultDefaultModel}},
}

// knownTopLevelKeys 由本模块认识的顶层键；其余键进 Extra 原样保留。
var knownTopLevelKeys = map[string]bool{
	"version": true, "revision": true, "connections": true, "defaultConnection": true,
	"defaultModel": true, "demo": true, "ui": true, "execution": true,
	"diagnostics": true, "permissions": true,
}

var faultModes = map[string]bool{"timeout": true, "error": true, "invalid_output": true}

// Env 读取环境变量（服务端/CLI 可注入）；nil 时用进程环境。
type Env map[string]string

func (e Env) get(key string) string {
	if e != nil {
		return e[key]
	}
	return os.Getenv(key)
}

// Path settings.json 的完整路径。
func Path(workspace string) string { return filepath.Join(workspace, FileName) }

func isRecord(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

// asInt JSON 数字（float64 / int / json.Number）→ 整数；非整数返回 false。
func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n {
			return 0, false
		}
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
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

func asNonNegativeInt(v any) (int, bool) {
	n, ok := asInt(v)
	return n, ok && n >= 0
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func stringIn(v any, allowed []string) (string, bool) {
	s, ok := v.(string)
	if !ok || !containsString(allowed, s) {
		return "", false
	}
	return s, true
}

// ---------------------------------------------------------------- 原始文件读写

// ReadRawObject 读 settings.json 的原始对象；文件不存在返回空对象，损坏时报错。
func ReadRawObject(workspace string) (map[string]any, error) {
	file := Path(workspace)
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("无法读取 %s：%w", file, err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("无法读取 %s：%v", file, err)
	}
	obj, ok := isRecord(v)
	if !ok {
		return nil, fmt.Errorf("无法读取 %s：顶层必须是 JSON 对象", file)
	}
	return obj, nil
}

func writeRaw(workspace string, value map[string]any) error {
	file := Path(workspace)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, append(data, '\n'), 0o644)
}

// ---------------------------------------------------------------- 归一化 / 迁移

func normalizeUI(v any) contract.UiSettings {
	out := contract.DefaultUISettings
	m, ok := isRecord(v)
	if !ok {
		return out
	}
	if s, ok := stringIn(m["theme"], contract.Themes); ok {
		out.Theme = s
	}
	if n, ok := asInt(m["fontSize"]); ok && n >= contract.FontSizeMin && n <= contract.FontSizeMax {
		out.FontSize = n
	}
	if s, ok := stringIn(m["trajectoryDetail"], contract.TrajectoryDetails); ok {
		out.TrajectoryDetail = s
	}
	if s, ok := stringIn(m["collapseTiming"], contract.CollapseTimings); ok {
		out.CollapseTiming = s
	}
	if s, ok := stringIn(m["enterBehavior"], contract.EnterBehaviors); ok {
		out.EnterBehavior = s
	}
	if s, ok := stringIn(m["linkOpening"], contract.LinkOpenings); ok {
		out.LinkOpening = s
	}
	return out
}

func normalizeIntList(v any) ([]int, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]int, 0, len(arr))
	for _, item := range arr {
		n, ok := asInt(item)
		if !ok {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

func normalizeExecution(v any) contract.ExecutionSettings {
	out := contract.DefaultExecutionSettings
	out.RetryBackoffMS = append([]int(nil), contract.DefaultExecutionSettings.RetryBackoffMS...)
	m, ok := isRecord(v)
	if !ok {
		return out
	}
	if n, ok := asInt(m["stepTimeoutMs"]); ok && n >= contract.StepTimeoutMinMS && n <= contract.StepTimeoutMaxMS {
		out.StepTimeoutMS = n
	}
	if n, ok := asInt(m["modelMaxRetries"]); ok && n >= 0 && n <= contract.ModelRetriesMax {
		out.ModelMaxRetries = n
	}
	if list, ok := normalizeIntList(m["retryBackoffMs"]); ok && len(list) <= contract.RetryBackoffMax {
		valid := true
		for _, item := range list {
			if item < 0 || item > contract.RetryBackoffItemMaxMS {
				valid = false
			}
		}
		if valid {
			out.RetryBackoffMS = list
		}
	}
	if n, ok := asInt(m["qcAutofix"]); ok && n >= 0 && n <= contract.QcAutofixMax {
		out.QcAutofix = n
	}
	return out
}

func normalizeDiagnostics(v any) contract.DiagnosticsSettings {
	out := contract.DefaultDiagnosticsSettings
	m, ok := isRecord(v)
	if !ok {
		return out
	}
	if s, ok := stringIn(m["logLevel"], contract.LogLevels); ok {
		out.LogLevel = s
	}
	return out
}

func normalizePermissions(v any) contract.PermissionSettings {
	out := contract.DefaultPermissionSettings
	m, ok := isRecord(v)
	if !ok {
		return out
	}
	if s, ok := stringIn(m["sandboxMode"], contract.SandboxModes); ok {
		out.SandboxMode = s
	}
	if s, ok := stringIn(m["approvalPolicy"], contract.ApprovalPolicies); ok {
		out.ApprovalPolicy = s
	}
	if s, ok := stringIn(m["defaultRole"], contract.DefaultRoles); ok {
		out.DefaultRole = s
	}
	return out
}

func normalizeModelOptions(v any) (map[string]contract.ModelOptions, bool) {
	m, ok := isRecord(v)
	if !ok {
		return nil, false
	}
	out := map[string]contract.ModelOptions{}
	for modelID, raw := range m {
		optsRaw, ok := isRecord(raw)
		if !ok || modelID == "" {
			continue
		}
		opts := contract.ModelOptions{}
		if n, ok := asInt(optsRaw["contextWindow"]); ok && n >= contract.ContextWindowMin && n <= contract.ContextWindowMax {
			opts.ContextWindow = &n
		}
		if n, ok := asInt(optsRaw["maxTokens"]); ok && n >= contract.MaxTokensMin && n <= contract.MaxTokensMax {
			opts.MaxTokens = &n
		}
		if arr, ok := optsRaw["reasoningEfforts"].([]any); ok && len(arr) <= len(contract.ThinkingLevels) {
			efforts := make([]string, 0, len(arr))
			valid := true
			for _, item := range arr {
				s, ok := stringIn(item, contract.ThinkingLevels)
				if !ok {
					valid = false
					break
				}
				efforts = append(efforts, s)
			}
			if valid {
				opts.ReasoningEfforts = efforts
			}
		}
		if s, ok := stringIn(optsRaw["defaultReasoningEffort"], contract.ThinkingLevels); ok {
			opts.DefaultReasoningEffort = &s
		}
		out[modelID] = opts
	}
	return out, true
}

func normalizeConnection(v any) (contract.ModelConnection, bool) {
	m, ok := isRecord(v)
	if !ok {
		return contract.ModelConnection{}, false
	}
	name, ok := m["name"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		return contract.ModelConnection{}, false
	}
	baseURL, ok := m["baseUrl"].(string)
	if !ok {
		return contract.ModelConnection{}, false
	}
	conn := contract.ModelConnection{Name: name, BaseURL: baseURL}
	if key, ok := m["apiKey"].(string); ok {
		conn.APIKey = key
	}
	if rawModels, ok := m["models"].([]any); ok {
		for _, item := range rawModels {
			if s, ok := item.(string); ok && s != "" {
				conn.Models = append(conn.Models, s)
			}
		}
	}
	if options, ok := normalizeModelOptions(m["modelOptions"]); ok {
		conn.ModelOptions = options
	}
	if n, ok := asInt(m["timeoutMs"]); ok && n >= contract.ConnectionTimeoutMinMS && n <= contract.ConnectionTimeoutMaxMS {
		conn.TimeoutMS = &n
	}
	if n, ok := asInt(m["maxRetries"]); ok && n >= 0 && n <= contract.ConnectionRetriesMax {
		conn.MaxRetries = &n
	}
	return conn, true
}

func normalizeToolFaults(v any) map[string]string {
	out := map[string]string{}
	m, ok := isRecord(v)
	if !ok {
		return out
	}
	for tool, raw := range m {
		if s, ok := raw.(string); ok && faultModes[s] {
			out[tool] = s
		}
	}
	return out
}

func normalizeDemo(v any) contract.DemoSettings {
	m, ok := isRecord(v)
	if !ok {
		return contract.DemoSettings{ToolFaults: map[string]string{}}
	}
	demo := contract.DemoSettings{ToolFaults: normalizeToolFaults(m["toolFaults"])}
	if b, ok := m["corruptTraceability"].(bool); ok {
		demo.CorruptTraceability = b
	}
	if b, ok := m["crashOnNextStep"].(bool); ok {
		demo.CrashOnNextStep = b
	}
	return demo
}

// Normalize 把任意形状的 settings.json 归一化成 v2 文档（v1 → v2 的迁移也在这里）。
func Normalize(raw map[string]any) contract.SettingsDocument {
	doc := contract.SettingsDocument{}
	doc.Version = contract.SettingsVersion
	if revision, ok := asNonNegativeInt(raw["revision"]); ok {
		doc.Revision = revision
	}
	doc.UI = normalizeUI(raw["ui"])
	doc.Execution = normalizeExecution(raw["execution"])
	doc.Diagnostics = normalizeDiagnostics(raw["diagnostics"])
	doc.Permissions = normalizePermissions(raw["permissions"])

	if rawList, ok := raw["connections"].([]any); ok {
		for _, item := range rawList {
			if conn, ok := normalizeConnection(item); ok {
				doc.Connections = append(doc.Connections, conn)
			}
		}
	} else {
		doc.Connections = cloneConnections(DefaultConnections)
	}
	if s, ok := raw["defaultConnection"].(string); ok {
		doc.DefaultConnection = s
	} else if len(doc.Connections) > 0 {
		doc.DefaultConnection = doc.Connections[0].Name
	} else {
		doc.DefaultConnection = DefaultConnectionName
	}
	if s, ok := raw["defaultModel"].(string); ok {
		doc.DefaultModel = s
	} else if len(doc.Connections) > 0 && len(doc.Connections[0].Models) > 0 {
		doc.DefaultModel = doc.Connections[0].Models[0]
	} else {
		doc.DefaultModel = DefaultDefaultModel
	}
	doc.Demo = normalizeDemo(raw["demo"])

	doc.Extra = map[string]any{}
	for key, value := range raw {
		if !knownTopLevelKeys[key] {
			doc.Extra[key] = value
		}
	}
	return doc
}

// Read 读设置文档；v1 文件会在内存里补成 v2，但不写盘（迁移只发生在写入时）。
func Read(workspace string) contract.SettingsDocument {
	raw, err := ReadRawObject(workspace)
	if err != nil {
		// 读路径从不因手改坏值而失败：损坏时退回默认文档（写回时才暴露问题）
		return Normalize(map[string]any{})
	}
	return Normalize(raw)
}

func cloneConnections(in []contract.ModelConnection) []contract.ModelConnection {
	out := make([]contract.ModelConnection, 0, len(in))
	for _, conn := range in {
		copied := conn
		copied.Models = append([]string(nil), conn.Models...)
		out = append(out, copied)
	}
	return out
}

// ---------------------------------------------------------------- 写

type connectionJSON struct {
	Name         string                  `json:"name"`
	BaseURL      string                  `json:"baseUrl"`
	APIKey       string                  `json:"apiKey"`
	Models       []string                `json:"models"`
	ModelOptions map[string]contract.ModelOptions `json:"modelOptions,omitempty"`
	TimeoutMS    *int                    `json:"timeoutMs,omitempty"`
	MaxRetries   *int                    `json:"maxRetries,omitempty"`
}

func serializeConnection(conn contract.ModelConnection) connectionJSON {
	out := connectionJSON{
		Name: conn.Name, BaseURL: conn.BaseURL, APIKey: conn.APIKey,
		Models: append([]string{}, conn.Models...),
	}
	if conn.ModelOptions != nil {
		out.ModelOptions = conn.ModelOptions
	}
	out.TimeoutMS = conn.TimeoutMS
	out.MaxRetries = conn.MaxRetries
	return out
}

// Serialize 文档 → 磁盘上的 JSON 对象（Extra 展开回顶层，保持“未知键原样保留”）。
func Serialize(doc contract.SettingsDocument) map[string]any {
	out := map[string]any{}
	for key, value := range doc.Extra {
		out[key] = value
	}
	connections := make([]connectionJSON, 0, len(doc.Connections))
	for _, conn := range doc.Connections {
		connections = append(connections, serializeConnection(conn))
	}
	out["version"] = contract.SettingsVersion
	out["revision"] = doc.Revision
	out["connections"] = connections
	out["defaultConnection"] = doc.DefaultConnection
	out["defaultModel"] = doc.DefaultModel
	demo := map[string]any{
		"toolFaults":          doc.Demo.ToolFaults,
		"corruptTraceability": doc.Demo.CorruptTraceability,
		"crashOnNextStep":     doc.Demo.CrashOnNextStep,
	}
	if demo["toolFaults"] == nil {
		demo["toolFaults"] = map[string]string{}
	}
	out["demo"] = demo
	out["ui"] = doc.UI
	execution := doc.Execution
	execution.RetryBackoffMS = append([]int{}, doc.Execution.RetryBackoffMS...)
	out["execution"] = execution
	out["diagnostics"] = doc.Diagnostics
	out["permissions"] = doc.Permissions
	return out
}

// Write 写设置文档：revision + 1 后落盘，返回写后的文档。
func Write(workspace string, doc contract.SettingsDocument) (contract.SettingsDocument, error) {
	next := doc
	next.Version = contract.SettingsVersion
	next.Revision = doc.Revision + 1
	if err := writeRaw(workspace, Serialize(next)); err != nil {
		return doc, err
	}
	return next, nil
}

// Update 读 → 改 → 写（唯一的日常写入入口）：每次调用 revision + 1。
func Update(workspace string, mutate func(*contract.SettingsDocument)) (contract.SettingsDocument, error) {
	doc := Read(workspace)
	mutate(&doc)
	return Write(workspace, doc)
}

// Ensure settings.json 不存在时写一份默认文档（“打开设置文件”前调用）。
func Ensure(workspace string) (contract.SettingsDocument, error) {
	if _, err := os.Stat(Path(workspace)); err == nil {
		return Read(workspace), nil
	}
	return Write(workspace, Normalize(map[string]any{}))
}

// ---------------------------------------------------------------- 运行期视图

// IsDeepSeekConnection 按名称或地址判断：只有它会用 DEEPSEEK_API_KEY 覆盖文件里的 Key。
func IsDeepSeekConnection(conn contract.ModelConnection) bool {
	return strings.EqualFold(conn.Name, DefaultConnectionName) || strings.Contains(conn.BaseURL, "deepseek.com")
}

// ModelSettingsOf 文档里的连接三件套。
func ModelSettingsOf(doc contract.SettingsDocument) contract.ModelSettings {
	return contract.ModelSettings{
		Connections:       cloneConnections(doc.Connections),
		DefaultConnection: doc.DefaultConnection,
		DefaultModel:      doc.DefaultModel,
	}
}

// LoadModelSettings 读 <workspace>/settings.json；DEEPSEEK_API_KEY 优先于文件里的 Key。
func LoadModelSettings(workspace string, env Env) contract.ModelSettings {
	ms := ModelSettingsOf(Read(workspace))
	key := env.get("DEEPSEEK_API_KEY")
	if key == "" {
		return ms
	}
	for i := range ms.Connections {
		if IsDeepSeekConnection(ms.Connections[i]) {
			ms.Connections[i].APIKey = key
		}
	}
	return ms
}

// LoadDemo 读取演示开关。
func LoadDemo(workspace string) contract.DemoSettings { return Read(workspace).Demo }

// SaveDemo 更新演示开关并写回 settings.json（revision +1），返回更新后的演示设置。
func SaveDemo(workspace string, patch contract.DemoSettings, setTools, setCorrupt, setCrash bool) (contract.DemoSettings, error) {
	doc, err := Update(workspace, func(doc *contract.SettingsDocument) {
		if setTools {
			tools := map[string]string{}
			for k, v := range patch.ToolFaults {
				tools[k] = v
			}
			doc.Demo.ToolFaults = tools
		}
		if setCorrupt {
			doc.Demo.CorruptTraceability = patch.CorruptTraceability
		}
		if setCrash {
			doc.Demo.CrashOnNextStep = patch.CrashOnNextStep
		}
	})
	if err != nil {
		return contract.DemoSettings{}, err
	}
	return doc.Demo, nil
}

// ConnectionView 一个连接的脱敏视图：没有 apiKey 字段，只有末 4 位与来源。
type ConnectionView struct {
	Name         string                           `json:"name"`
	BaseURL      string                           `json:"baseUrl"`
	Models       []string                         `json:"models"`
	ModelOptions map[string]contract.ModelOptions `json:"modelOptions,omitempty"`
	TimeoutMS    *int                             `json:"timeoutMs,omitempty"`
	MaxRetries   *int                             `json:"maxRetries,omitempty"`
	APIKeyLast4  *string                          `json:"apiKeyLast4"`
	HasKey       bool                             `json:"hasKey"`
	Source       string                           `json:"source"`
	IsDefault    bool                             `json:"isDefault"`
}

// AppView GET /api/settings/app 的响应（v2 文档视图）。
type AppView struct {
	OK                bool                       `json:"ok"`
	Version           int                        `json:"version"`
	Revision          int                        `json:"revision"`
	UI                contract.UiSettings        `json:"ui"`
	Execution         contract.ExecutionSettings `json:"execution"`
	Diagnostics       contract.DiagnosticsSettings `json:"diagnostics"`
	Permissions       contract.PermissionSettings `json:"permissions"`
	Connections       []ConnectionView           `json:"connections"`
	DefaultConnection string                     `json:"defaultConnection"`
	DefaultModel      string                     `json:"defaultModel"`
	Demo              contract.DemoSettings      `json:"demo"`
	Extra             map[string]any             `json:"extra"`
	EnvOverride       bool                       `json:"envOverride"`
}

func effectiveKey(conn contract.ModelConnection, env Env) (key, source string) {
	envKey := env.get("DEEPSEEK_API_KEY")
	if envKey != "" && IsDeepSeekConnection(conn) {
		return envKey, "env"
	}
	if conn.APIKey != "" {
		return conn.APIKey, "file"
	}
	return "", "none"
}

func connectionView(conn contract.ModelConnection, defaultConnection string, env Env) ConnectionView {
	key, source := effectiveKey(conn, env)
	view := ConnectionView{
		Name: conn.Name, BaseURL: conn.BaseURL,
		Models:    append([]string{}, conn.Models...),
		HasKey:    key != "",
		Source:    source,
		IsDefault: conn.Name == defaultConnection,
	}
	if view.Models == nil {
		view.Models = []string{}
	}
	if key != "" {
		last4 := key
		if len(last4) > 4 {
			last4 = last4[len(last4)-4:]
		}
		view.APIKeyLast4 = &last4
	}
	if conn.ModelOptions != nil {
		view.ModelOptions = conn.ModelOptions
	}
	view.TimeoutMS = conn.TimeoutMS
	view.MaxRetries = conn.MaxRetries
	return view
}

// View v2 文档的脱敏视图：密钥只出末 4 位 + hasKey + source。
func View(doc contract.SettingsDocument, env Env) AppView {
	connections := make([]ConnectionView, 0, len(doc.Connections))
	for _, conn := range doc.Connections {
		connections = append(connections, connectionView(conn, doc.DefaultConnection, env))
	}
	extra := map[string]any{}
	for key, value := range doc.Extra {
		extra[key] = value
	}
	execution := doc.Execution
	execution.RetryBackoffMS = append([]int{}, doc.Execution.RetryBackoffMS...)
	demo := doc.Demo
	toolFaults := map[string]string{}
	for k, v := range doc.Demo.ToolFaults {
		toolFaults[k] = v
	}
	demo.ToolFaults = toolFaults
	return AppView{
		OK: true, Version: doc.Version, Revision: doc.Revision,
		UI: doc.UI, Execution: execution, Diagnostics: doc.Diagnostics, Permissions: doc.Permissions,
		Connections: connections, DefaultConnection: doc.DefaultConnection, DefaultModel: doc.DefaultModel,
		Demo: demo, Extra: extra, EnvOverride: env.get("DEEPSEEK_API_KEY") != "",
	}
}

// Export 导出用的 JSON 对象：默认把 Key 抹成空串（形状仍可再导入）。
func Export(doc contract.SettingsDocument, includeKeys bool) map[string]any {
	raw := Serialize(doc)
	if includeKeys {
		return raw
	}
	connections := make([]connectionJSON, 0, len(doc.Connections))
	for _, conn := range doc.Connections {
		conn.APIKey = ""
		connections = append(connections, serializeConnection(conn))
	}
	raw["connections"] = connections
	return raw
}

// ---------------------------------------------------------------- 写入校验

// Patch PATCH /api/settings/app 的请求体（只含请求里真的出现的字段）。
type Patch struct {
	Revision    int
	UI          map[string]any
	Execution   map[string]any
	Diagnostics map[string]any
	Permissions map[string]any
}

func badValue(format string, args ...any) error {
	return fmt.Errorf("设置值不合法（%s）", fmt.Sprintf(format, args...))
}

func validateUIPatch(m map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for key, value := range m {
		switch key {
		case "theme":
			s, ok := stringIn(value, contract.Themes)
			if !ok {
				return nil, badValue("ui.theme：取值必须是 %s", strings.Join(contract.Themes, " / "))
			}
			out[key] = s
		case "fontSize":
			n, ok := asInt(value)
			if !ok || n < contract.FontSizeMin || n > contract.FontSizeMax {
				return nil, badValue("ui.fontSize：必须是 %d–%d 的整数", contract.FontSizeMin, contract.FontSizeMax)
			}
			out[key] = n
		case "trajectoryDetail":
			s, ok := stringIn(value, contract.TrajectoryDetails)
			if !ok {
				return nil, badValue("ui.trajectoryDetail：取值必须是 %s", strings.Join(contract.TrajectoryDetails, " / "))
			}
			out[key] = s
		case "collapseTiming":
			s, ok := stringIn(value, contract.CollapseTimings)
			if !ok {
				return nil, badValue("ui.collapseTiming：取值必须是 %s", strings.Join(contract.CollapseTimings, " / "))
			}
			out[key] = s
		case "enterBehavior":
			s, ok := stringIn(value, contract.EnterBehaviors)
			if !ok {
				return nil, badValue("ui.enterBehavior：取值必须是 %s", strings.Join(contract.EnterBehaviors, " / "))
			}
			out[key] = s
		case "linkOpening":
			s, ok := stringIn(value, contract.LinkOpenings)
			if !ok {
				return nil, badValue("ui.linkOpening：取值必须是 %s", strings.Join(contract.LinkOpenings, " / "))
			}
			out[key] = s
		default:
			return nil, badValue("ui.%s：未知字段", key)
		}
	}
	return out, nil
}

func validateExecutionPatch(m map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for key, value := range m {
		switch key {
		case "stepTimeoutMs":
			n, ok := asInt(value)
			if !ok || n < contract.StepTimeoutMinMS || n > contract.StepTimeoutMaxMS {
				return nil, badValue("execution.stepTimeoutMs：必须是 %d–%d 之间的整数", contract.StepTimeoutMinMS, contract.StepTimeoutMaxMS)
			}
			out[key] = n
		case "modelMaxRetries":
			n, ok := asInt(value)
			if !ok || n < 0 || n > contract.ModelRetriesMax {
				return nil, badValue("execution.modelMaxRetries：必须是 0–%d 的整数", contract.ModelRetriesMax)
			}
			out[key] = n
		case "retryBackoffMs":
			list, ok := normalizeIntList(value)
			if !ok || len(list) > contract.RetryBackoffMax {
				return nil, badValue("execution.retryBackoffMs：必须是整数数组，最多 %d 项", contract.RetryBackoffMax)
			}
			for _, item := range list {
				if item < 0 || item > contract.RetryBackoffItemMaxMS {
					return nil, badValue("execution.retryBackoffMs：每项必须是 0–%d 的整数", contract.RetryBackoffItemMaxMS)
				}
			}
			out[key] = list
		case "qcAutofix":
			n, ok := asInt(value)
			if !ok || n < 0 || n > contract.QcAutofixMax {
				return nil, badValue("execution.qcAutofix：必须是 0–%d 的整数", contract.QcAutofixMax)
			}
			out[key] = n
		default:
			return nil, badValue("execution.%s：未知字段", key)
		}
	}
	return out, nil
}

func validateDiagnosticsPatch(m map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for key, value := range m {
		switch key {
		case "logLevel":
			s, ok := stringIn(value, contract.LogLevels)
			if !ok {
				return nil, badValue("diagnostics.logLevel：取值必须是 %s", strings.Join(contract.LogLevels, " / "))
			}
			out[key] = s
		default:
			return nil, badValue("diagnostics.%s：未知字段", key)
		}
	}
	return out, nil
}

func validatePermissionsPatch(m map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for key, value := range m {
		switch key {
		case "sandboxMode":
			s, ok := stringIn(value, contract.SandboxModes)
			if !ok {
				return nil, badValue("permissions.sandboxMode：取值必须是 %s", strings.Join(contract.SandboxModes, " / "))
			}
			out[key] = s
		case "approvalPolicy":
			s, ok := stringIn(value, contract.ApprovalPolicies)
			if !ok {
				return nil, badValue("permissions.approvalPolicy：取值必须是 %s", strings.Join(contract.ApprovalPolicies, " / "))
			}
			out[key] = s
		case "defaultRole":
			s, ok := stringIn(value, contract.DefaultRoles)
			if !ok {
				return nil, badValue("permissions.defaultRole：取值必须是 %s", strings.Join(contract.DefaultRoles, " / "))
			}
			out[key] = s
		default:
			return nil, badValue("permissions.%s：未知字段", key)
		}
	}
	return out, nil
}

// ParsePatch 校验 PATCH /api/settings/app 的请求体；只保留请求里真的出现的字段。
func ParsePatch(body map[string]any) (*Patch, error) {
	revision, ok := asNonNegativeInt(body["revision"])
	if !ok {
		return nil, badValue("revision：必须是必填的非负整数")
	}
	patch := &Patch{Revision: revision}
	for key, value := range body {
		switch key {
		case "revision":
			continue
		case "ui", "execution", "diagnostics", "permissions":
			section, ok := isRecord(value)
			if !ok {
				return nil, badValue("%s：必须是对象", key)
			}
			var parsed map[string]any
			var err error
			switch key {
			case "ui":
				parsed, err = validateUIPatch(section)
			case "execution":
				parsed, err = validateExecutionPatch(section)
			case "diagnostics":
				parsed, err = validateDiagnosticsPatch(section)
			case "permissions":
				parsed, err = validatePermissionsPatch(section)
			}
			if err != nil {
				return nil, err
			}
			switch key {
			case "ui":
				patch.UI = parsed
			case "execution":
				patch.Execution = parsed
			case "diagnostics":
				patch.Diagnostics = parsed
			case "permissions":
				patch.Permissions = parsed
			}
		default:
			return nil, badValue("%s：未知字段", key)
		}
	}
	return patch, nil
}

func mergeInto(dst any, patch map[string]any) error {
	raw, err := json.Marshal(dst)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	for key, value := range patch {
		m[key] = value
	}
	merged, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(merged, dst)
}

// ApplyPatch 把 PATCH 的分区字段合并进文档（只碰传了的分区）。
func ApplyPatch(doc *contract.SettingsDocument, patch *Patch) error {
	var err error
	if patch.UI != nil {
		if err = mergeInto(&doc.UI, patch.UI); err != nil {
			return err
		}
	}
	if patch.Execution != nil {
		if err = mergeInto(&doc.Execution, patch.Execution); err != nil {
			return err
		}
		if doc.Execution.RetryBackoffMS == nil {
			doc.Execution.RetryBackoffMS = []int{}
		}
	}
	if patch.Diagnostics != nil {
		if err = mergeInto(&doc.Diagnostics, patch.Diagnostics); err != nil {
			return err
		}
	}
	if patch.Permissions != nil {
		if err = mergeInto(&doc.Permissions, patch.Permissions); err != nil {
			return err
		}
	}
	return nil
}

func validateConnectionInput(value any) error {
	m, ok := isRecord(value)
	if !ok {
		return fmt.Errorf("必须是对象")
	}
	name, ok := m["name"].(string)
	if !ok || len(name) == 0 || len(name) > 64 {
		return fmt.Errorf("name 必须是 1–64 个字符的字符串")
	}
	if _, ok := m["baseUrl"].(string); !ok {
		return fmt.Errorf("baseUrl 必须是字符串")
	}
	if raw, ok := m["apiKey"]; ok {
		if _, ok := raw.(string); !ok {
			return fmt.Errorf("apiKey 必须是字符串")
		}
	}
	if raw, ok := m["models"]; ok {
		arr, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("models 必须是字符串数组")
		}
		for _, item := range arr {
			if _, ok := item.(string); !ok {
				return fmt.Errorf("models 必须是字符串数组")
			}
		}
	}
	if raw, ok := m["modelOptions"]; ok {
		if _, ok := normalizeModelOptions(raw); !ok {
			return fmt.Errorf("modelOptions 必须是 模型 id → 高级参数 的对象")
		}
	}
	if raw, ok := m["timeoutMs"]; ok {
		n, ok := asInt(raw)
		if !ok || n < contract.ConnectionTimeoutMinMS || n > contract.ConnectionTimeoutMaxMS {
			return fmt.Errorf("timeoutMs 必须是 %d–%d 之间的整数", contract.ConnectionTimeoutMinMS, contract.ConnectionTimeoutMaxMS)
		}
	}
	if raw, ok := m["maxRetries"]; ok {
		n, ok := asInt(raw)
		if !ok || n < 0 || n > contract.ConnectionRetriesMax {
			return fmt.Errorf("maxRetries 必须是 0–%d 之间的整数", contract.ConnectionRetriesMax)
		}
	}
	for key := range m {
		switch key {
		case "name", "baseUrl", "apiKey", "models", "modelOptions", "timeoutMs", "maxRetries":
		default:
			return fmt.Errorf("%s：未知字段", key)
		}
	}
	return nil
}

// ValidateImport 导入文件校验：逐段检查枚举 / 范围 / 连接形状，v1 文件（无 version）允许并标记 migrated。
// 返回 error 时调用方必须 400 且不写盘。
func ValidateImport(value any) (migrated bool, err error) {
	doc, ok := isRecord(value)
	if !ok {
		return false, fmt.Errorf("设置文件必须是 JSON 对象")
	}
	if rawVersion, ok := doc["version"]; ok {
		n, ok := asInt(rawVersion)
		if !ok || (n != 1 && n != 2) {
			return false, fmt.Errorf("不支持的设置版本：%v", rawVersion)
		}
	}
	if raw, ok := doc["revision"]; ok {
		if _, ok := asNonNegativeInt(raw); !ok {
			return false, fmt.Errorf("revision 必须是非负整数")
		}
	}
	if raw, ok := doc["connections"]; ok {
		arr, ok := raw.([]any)
		if !ok {
			return false, fmt.Errorf("connections 必须是数组")
		}
		for index, item := range arr {
			if err := validateConnectionInput(item); err != nil {
				return false, fmt.Errorf("第 %d 个连接不合法（%v）", index+1, err)
			}
		}
	}
	for _, key := range []string{"defaultConnection", "defaultModel"} {
		if raw, ok := doc[key]; ok {
			if _, ok := raw.(string); !ok {
				return false, fmt.Errorf("%s 必须是字符串", key)
			}
		}
	}
	sections := []struct {
		key      string
		validate func(map[string]any) (map[string]any, error)
	}{
		{"ui", validateUIPatch},
		{"execution", validateExecutionPatch},
		{"diagnostics", validateDiagnosticsPatch},
		{"permissions", validatePermissionsPatch},
	}
	for _, section := range sections {
		raw, ok := doc[section.key]
		if !ok {
			continue
		}
		m, ok := isRecord(raw)
		if !ok {
			return false, fmt.Errorf("%s 必须是对象", section.key)
		}
		if _, err := section.validate(m); err != nil {
			return false, err
		}
	}
	if raw, ok := doc["demo"]; ok {
		m, ok := isRecord(raw)
		if !ok {
			return false, fmt.Errorf("demo 必须是对象")
		}
		for key, value := range m {
			switch key {
			case "toolFaults":
				tools, ok := isRecord(value)
				if !ok {
					return false, fmt.Errorf("demo.toolFaults 必须是对象")
				}
				for tool, mode := range tools {
					s, ok := mode.(string)
					if !ok || !faultModes[s] {
						return false, fmt.Errorf("demo.toolFaults.%s 必须是 timeout / error / invalid_output", tool)
					}
				}
			case "corruptTraceability", "crashOnNextStep":
				if _, ok := value.(bool); !ok {
					return false, fmt.Errorf("demo.%s 必须是布尔值", key)
				}
			default:
				return false, fmt.Errorf("demo.%s：未知字段", key)
			}
		}
	}
	version, hasVersion := doc["version"]
	if !hasVersion {
		return true, nil
	}
	n, _ := asInt(version)
	return n != contract.SettingsVersion, nil
}

// ImportBody 导入请求体既接受导出的信封（{document} / {settings}），也接受裸设置文档。
func ImportBody(body any) any {
	m, ok := isRecord(body)
	if !ok {
		return body
	}
	if doc, ok := isRecord(m["document"]); ok {
		return doc
	}
	if doc, ok := isRecord(m["settings"]); ok {
		return doc
	}
	return body
}
