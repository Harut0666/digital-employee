package contract

// 设置文档 v2（settings.ts）。settings.json 是设置唯一数据来源：
// version=2，revision 乐观锁，未知顶层键原样保留（SettingsDocument.Extra）。

const SettingsVersion = 2

// 枚举取值。
var (
	ThinkingLevels    = []string{"off", "minimal", "low", "medium", "high", "xhigh"}
	Themes            = []string{"light", "dark", "system"}
	TrajectoryDetails = []string{"compact", "standard", "detailed"}
	CollapseTimings   = []string{"completion", "next-input"}
	EnterBehaviors    = []string{"send", "newline"}
	LinkOpenings      = []string{"in-app", "system"}
	LogLevels         = []string{"error", "warn", "info", "debug"}
	SandboxModes      = []string{"read-only", "workspace-write", "danger-full-access"}
	ApprovalPolicies  = []string{"ask", "never"}
	DefaultRoles      = []string{"initiator", "reviewer", "operator"}
)

// 范围常量（与 settings.ts 一致）。
const (
	FontSizeMin            = 12
	FontSizeMax            = 18
	StepTimeoutMinMS       = 1_000
	StepTimeoutMaxMS       = 3_600_000
	ModelRetriesMax        = 10
	RetryBackoffMax        = 10
	RetryBackoffItemMaxMS  = 600_000
	QcAutofixMax           = 10
	ContextWindowMin       = 1_000
	ContextWindowMax       = 4_000_000
	MaxTokensMin           = 1
	MaxTokensMax           = 2_000_000
	ConnectionTimeoutMinMS = 1_000
	ConnectionTimeoutMaxMS = 3_600_000
	ConnectionRetriesMax   = 10
)

// ModelOptions 每个模型单独的高级参数。
type ModelOptions struct {
	ContextWindow          *int     `json:"contextWindow,omitempty"`
	MaxTokens              *int     `json:"maxTokens,omitempty"`
	ReasoningEfforts       []string `json:"reasoningEfforts,omitempty"`
	DefaultReasoningEffort *string  `json:"defaultReasoningEffort,omitempty"`
}

// UiSettings 外观与交互。
type UiSettings struct {
	Theme            string `json:"theme"`
	FontSize         int    `json:"fontSize"`
	TrajectoryDetail string `json:"trajectoryDetail"`
	CollapseTiming   string `json:"collapseTiming"`
	EnterBehavior    string `json:"enterBehavior"`
	LinkOpening      string `json:"linkOpening"`
}

// ExecutionSettings 执行与预算（作为默认值）。
type ExecutionSettings struct {
	StepTimeoutMS   int   `json:"stepTimeoutMs"`
	ModelMaxRetries int   `json:"modelMaxRetries"`
	RetryBackoffMS  []int `json:"retryBackoffMs"`
	QcAutofix       int   `json:"qcAutofix"`
}

// DiagnosticsSettings 诊断。
type DiagnosticsSettings struct {
	LogLevel string `json:"logLevel"`
}

// PermissionSettings 权限与审批。
type PermissionSettings struct {
	SandboxMode    string `json:"sandboxMode"`
	ApprovalPolicy string `json:"approvalPolicy"`
	DefaultRole    string `json:"defaultRole"`
}

// DemoSettings 演示与调试。
type DemoSettings struct {
	ToolFaults          map[string]string `json:"toolFaults"`
	CorruptTraceability bool              `json:"corruptTraceability"`
	CrashOnNextStep     bool              `json:"crashOnNextStep"`
}

// AppSettings 四个分区 + 版本/锁。
type AppSettings struct {
	Version     int                 `json:"version"`
	Revision    int                 `json:"revision"`
	UI          UiSettings          `json:"ui"`
	Execution   ExecutionSettings   `json:"execution"`
	Diagnostics DiagnosticsSettings `json:"diagnostics"`
	Permissions PermissionSettings  `json:"permissions"`
}

// SettingsDocument 完整设置文档。
type SettingsDocument struct {
	AppSettings
	Connections []ModelConnection `json:"connections"`
	// LastModel 最近一次在会话里选的模型（Spec 0006「会话模型」）；nil 表示还没选过。
	// 取代了旧文档里的 defaultModel：新会话沿用这个模型。
	LastModel *ModelRef `json:"lastModel,omitempty"`
	// DefaultEmployee 新会话优先提议的员工；空串表示没设。
	DefaultEmployee string       `json:"defaultEmployee,omitempty"`
	Demo            DemoSettings `json:"demo"`
	// Extra 未识别的顶层键：读时保留，写时原样写回。
	Extra map[string]any `json:"-"`
}

// 默认值。
var (
	DefaultUISettings = UiSettings{
		Theme: "system", FontSize: 14, TrajectoryDetail: "standard",
		CollapseTiming: "completion", EnterBehavior: "send", LinkOpening: "in-app",
	}
	DefaultExecutionSettings = ExecutionSettings{
		StepTimeoutMS: 120_000, ModelMaxRetries: 2,
		RetryBackoffMS: []int{500, 1500, 4000}, QcAutofix: 3,
	}
	DefaultDiagnosticsSettings = DiagnosticsSettings{LogLevel: "info"}
	DefaultPermissionSettings  = PermissionSettings{
		SandboxMode: "workspace-write", ApprovalPolicy: "ask", DefaultRole: "initiator",
	}
)

// DefaultAppSettings 新文档（无 settings.json）用的默认值。
func DefaultAppSettings(revision int) AppSettings {
	return AppSettings{
		Version:     SettingsVersion,
		Revision:    revision,
		UI:          DefaultUISettings,
		Execution:   ExecutionSettings{StepTimeoutMS: 120_000, ModelMaxRetries: 2, RetryBackoffMS: []int{500, 1500, 4000}, QcAutofix: 3},
		Diagnostics: DefaultDiagnosticsSettings,
		Permissions: DefaultPermissionSettings,
	}
}
