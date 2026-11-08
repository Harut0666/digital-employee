package contract

// 任务引擎契约（engine.ts）。服务端、CLI、测试都只通过 Engine 驱动任务。

type TaskID = string

// AttachmentInput 建任务/补充附件时的入参：引擎会把 sourcePath 复制进任务目录。
type AttachmentInput struct {
	Name       string `json:"name"`
	SourcePath string `json:"sourcePath"`
}

// TaskCommand 命令联合体。TS 是判别式联合，Go 用一个结构体承载，
// 前端发的 JSON 形状不变（只有 type 与相关字段）。
type TaskCommand struct {
	Type        string            `json:"type"`
	Form        map[string]any    `json:"form,omitempty"`
	Attachments []AttachmentInput `json:"attachments,omitempty"`
	Comment     string            `json:"comment,omitempty"`
	Sections    []string          `json:"sections,omitempty"`
}

const (
	CmdSubmit                 = "submit"
	CmdSupplement             = "supplement"
	CmdContinueWithoutMinutes = "continue_without_minutes"
	CmdContinueWithPartial    = "continue_with_partial"
	CmdRetry                  = "retry"
	CmdTerminate              = "terminate"
	CmdApprove                = "approve"
	CmdReturn                 = "return"
)

// CommandResult 命令结果：成功带快照，失败带业务错误码（HTTP 层据此定状态码）。
type CommandResult struct {
	OK       bool          `json:"ok"`
	Snapshot *TaskSnapshot `json:"snapshot,omitempty"`
	Error    string        `json:"error,omitempty"`
	Message  string        `json:"message,omitempty"`
}

// 业务错误码，与前端 CommandResult 的联合一致。
const (
	ErrIllegalTransition = "illegal_transition"
	ErrForbiddenRole     = "forbidden_role"
	ErrInvalidInput      = "invalid_input"
	ErrNotFound          = "not_found"
	// ErrModelNotConfigured 提交前检查不通过：默认模型连接没有可用的 Key。只由服务端返回，引擎不知道它。
	ErrModelNotConfigured = "model_not_configured"
)

// StepStatus 快照里的步骤状态。
type StepStatus struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Kind   string `json:"kind"`   // compute | judge | qc
	Status string `json:"status"` // pending | running | done | failed | skipped
	// Activity 给业务人员看的说法，如“正在撰写周报”，取自工作流配置
	Activity string `json:"activity,omitempty"`
}

const (
	StepPending = "pending"
	StepRunning = "running"
	StepDone    = "done"
	StepFailed  = "failed"
	StepSkipped = "skipped"
)

// DeliverableKind 交付物类型。
type DeliverableKind string

const (
	DeliverableDocx     DeliverableKind = "docx"
	DeliverableMD       DeliverableKind = "md"
	DeliverableQCRecord DeliverableKind = "qc_record"
)

// DeliverableRef 交付物引用。
type DeliverableRef struct {
	Kind   DeliverableKind `json:"kind"`
	Path   string          `json:"path"`
	SHA256 string          `json:"sha256"`
}

// Budget 预算用量。
type Budget struct {
	ModelCalls BudgetItem `json:"modelCalls"`
	ElapsedMs  BudgetItem `json:"elapsedMs"`
}

// BudgetItem 已用/上限。
type BudgetItem struct {
	Used  int64 `json:"used"`
	Limit int64 `json:"limit"`
}

// 操作按钮的样式。
const (
	ActionPrimary = "primary"
	ActionDefault = "default"
	ActionDanger  = "danger"
)

// TaskAction 当前可用的一个操作：由状态和暂停原因算出，不写入事件。
type TaskAction struct {
	Command string `json:"command"`
	Label   string `json:"label"`
	Roles   []Role `json:"roles"`
	Style   string `json:"style"`
}

// TaskSnapshot 任务快照：`GET /api/tasks/:id` 的响应体，前端据此渲染。
type TaskSnapshot struct {
	ID           TaskID           `json:"id"`
	Employee     string           `json:"employee"`
	ConfigHash   string           `json:"configHash"`
	State        TaskState        `json:"state"`
	Pause        *PauseReason     `json:"pause,omitempty"`
	Form         map[string]any   `json:"form"`
	Attachments  []AttachmentRef  `json:"attachments"`
	Steps        []StepStatus     `json:"steps"`
	Budget       Budget           `json:"budget"`
	ReworkRound  int              `json:"reworkRound"`
	QC           []QcCheckResult  `json:"qc,omitempty"`
	Deliverables []DeliverableRef `json:"deliverables"`
	LastSeq      int64            `json:"lastSeq"`
	// Actions 当前可用的操作，WaitingFor 在等哪些角色：都是读取时算出的投影
	Actions    []TaskAction `json:"actions"`
	WaitingFor []Role       `json:"waitingFor"`
	// Versions 历次送审的交付物，旧到新；最后一版就是 Deliverables
	Versions []DeliverableVersion `json:"versions"`
	// DownloadNames 当前交付物下载时的文件名（按类型），由服务端按员工配置的模板填写
	DownloadNames map[string]string `json:"downloadNames,omitempty"`
}

// DeliverableVersion 一版交付物：每次质检通过送审产出一版，退回重做产出下一版。
type DeliverableVersion struct {
	Version     int              `json:"version"`
	ReworkRound int              `json:"reworkRound"`
	CreatedAt   string           `json:"createdAt"`
	Files       []DeliverableRef `json:"files"`
	// Feedback 引出这一版的退回意见；第 1 版没有
	Feedback *Feedback `json:"feedback,omitempty"`
	// RevisionNotes 这一版的修改说明，取自写作步骤的结构化输出
	RevisionNotes []RevisionNote `json:"revisionNotes,omitempty"`
}

// TaskSummary 任务列表项。
type TaskSummary struct {
	ID        TaskID    `json:"id"`
	Employee  string    `json:"employee"`
	Title     string    `json:"title"`
	State     TaskState `json:"state"`
	UpdatedAt string    `json:"updatedAt"`
	// StatusText 给人看的状态，WaitingFor 在等哪些角色：都是读取时算出的投影
	StatusText string `json:"statusText"`
	WaitingFor []Role `json:"waitingFor"`
	// WorkspaceID 任务属于哪个工作区（取自发起事件；旧任务为空）
	WorkspaceID string `json:"workspaceId,omitempty"`
	// Error 事件日志损坏时的说明（任务仍以最后已知状态出现）
	Error string `json:"error,omitempty"`
}

// ToolDescriptor 工具描述（MCP tools/list 移植）。
type ToolDescriptor struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// MaterialIssue 工具失败是因为材料本身有问题，不是工具故障：重试没有用，要发起人补充。
type MaterialIssue struct {
	// Path 有问题的附件；发起人补充时它会被移除
	Path string `json:"path"`
	// Message 给发起人看的说明
	Message string `json:"message"`
}

// ToolCallResult 工具调用结果。失败时 Retryable 决定是否按配置重试。
type ToolCallResult struct {
	OK            bool           `json:"ok"`
	Data          any            `json:"data,omitempty"`
	Error         string         `json:"error,omitempty"`
	Retryable     bool           `json:"retryable,omitempty"`
	MaterialIssue *MaterialIssue `json:"materialIssue,omitempty"`
}

// ToolClient 工具客户端：生产走进程内直连，CLI 测试也走同一路径。
// Go 单二进制里不再起 MCP stdio 子进程；接口保留，方便以后接外部 MCP server。
type ToolClient interface {
	ListTools() ([]ToolDescriptor, error)
	Call(name string, args any) (ToolCallResult, error)
	Close() error
}

// ModelConnection 模型连接（settings.json 的 connections[]）。
type ModelConnection struct {
	Name         string                  `json:"name"`
	BaseURL      string                  `json:"baseUrl"`
	APIKey       string                  `json:"apiKey"`
	Models       []string                `json:"models"`
	ModelOptions map[string]ModelOptions `json:"modelOptions,omitempty"`
	TimeoutMS    *int                    `json:"timeoutMs,omitempty"`
	MaxRetries   *int                    `json:"maxRetries,omitempty"`
}

// ModelSettings 模型设置。
type ModelSettings struct {
	Connections       []ModelConnection `json:"connections"`
	DefaultConnection string            `json:"defaultConnection"`
	DefaultModel      string            `json:"defaultModel"`
}

// ModelConnectionTest “测试连接”的结果。
type ModelConnectionTest struct {
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latencyMs"`
	Model     string `json:"model"`
	Error     string `json:"error,omitempty"`
}

// FaultHooks 运行时可变故障开关（演示与调试），服务端与引擎共享同一对象。
type FaultHooks struct {
	CrashAtStep         string            `json:"-"`
	CrashOnNextStep     bool              `json:"crashOnNextStep"`
	CorruptTraceability bool              `json:"corruptTraceability"`
	Tools               map[string]string `json:"tools,omitempty"`
}

// Engine 任务引擎：HTTP 层、CLI、测试共用的唯一入口。
type Engine interface {
	Create(input CreateTaskInput) (*TaskSnapshot, error)
	Command(taskID TaskID, cmd TaskCommand, actor Role) CommandResult
	Get(taskID TaskID) (*TaskSnapshot, bool)
	List() []TaskSummary
	Events(taskID TaskID, afterSeq int64) ([]TaskEvent, error)
	Subscribe(taskID TaskID, listener func(TaskEvent)) (cancel func())
	WaitForSettled(taskID TaskID) (*TaskSnapshot, error)
	ResumeAll() error
}

// EngineStopper 能停下的引擎（进程退出时用）：停下后不再接受命令，也不再写入。
// 有任务处于准入校验、执行中或质检中时拒绝停下，返回 *EngineBusyError，引擎照常工作。
type EngineStopper interface {
	Stop() error
}

// EngineBusyError 有任务处于准入校验、执行中或质检中。
type EngineBusyError struct {
	TaskIDs []TaskID
}

func (e *EngineBusyError) Error() string {
	return "有任务正在准入校验、执行中或质检中"
}

// CreateTaskInput 建草稿任务的入参。WorkspaceID 写进“发起”事件，说明任务属于哪个工作区。
type CreateTaskInput struct {
	Employee    string            `json:"employee"`
	Form        map[string]any    `json:"form"`
	Attachments []AttachmentInput `json:"attachments"`
	Actor       Role              `json:"actor"`
	WorkspaceID string            `json:"workspaceId,omitempty"`
}
