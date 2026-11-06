package contract

// TaskEvent 事件日志的一行（events.ts）。TS 里是 15 个判别式联合；
// Go 侧用一个宽结构体承载，字段名与 TS 完全一致，未用到的字段 omitempty 掉。
// 这样 SSE 推送、events.jsonl、前端解析三者都不用改。
//
// 注意：布尔与整数字段用指针，因为 0 / false 在契约里有意义
// （reworkRound: 0 是首轮，ok: false 是工具失败），omitempty 会把它们吞掉。
type TaskEvent struct {
	Seq    int64  `json:"seq"`
	TS     string `json:"ts"`
	TaskID string `json:"taskId"`
	Type   string `json:"type"`

	// task_created
	Employee    string          `json:"employee,omitempty"`
	ConfigHash  string          `json:"configHash,omitempty"`
	Form        map[string]any  `json:"form,omitempty"`
	Attachments []AttachmentRef `json:"attachments,omitempty"`

	// form_updated / review_decision 之外的角色
	Actor Role `json:"actor,omitempty"`

	// state_changed / transition_rejected
	From    TaskState         `json:"from,omitempty"`
	To      TaskState         `json:"to,omitempty"`
	Trigger TransitionTrigger `json:"trigger,omitempty"`
	Pause   *PauseReason      `json:"pause,omitempty"`
	Reason  string            `json:"reason,omitempty"`

	// step_started / step_completed / step_failed
	StepID       string `json:"stepId,omitempty"`
	Attempt      *int   `json:"attempt,omitempty"`
	ReworkRound  *int   `json:"reworkRound,omitempty"`
	OutputPath   string `json:"outputPath,omitempty"`
	OutputSHA256 string `json:"outputSha256,omitempty"`
	Error        string `json:"error,omitempty"`

	// tool_called / tool_result / tool_denied
	CallID string `json:"callId,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Args   any    `json:"args,omitempty"`
	OK     *bool  `json:"ok,omitempty"`
	// ResultSHA256 只在 tool_result.ok=true 时出现
	ResultSHA256 string `json:"resultSha256,omitempty"`

	// model_called
	Turn       *int        `json:"turn,omitempty"`
	Response   any         `json:"response,omitempty"`
	Usage      *EventUsage `json:"usage,omitempty"`
	DurationMS *int64      `json:"durationMs,omitempty"`

	// qc_result
	Passed *bool           `json:"passed,omitempty"`
	Checks []QcCheckResult `json:"checks,omitempty"`

	// review_decision
	Decision string   `json:"decision,omitempty"`
	Comment  string   `json:"comment,omitempty"`
	Sections []string `json:"sections,omitempty"`

	// deliverable_created
	Kind   string `json:"kind,omitempty"`
	Path   string `json:"path,omitempty"`
	SHA256 string `json:"sha256,omitempty"`

	// fault_injected
	Target string `json:"target,omitempty"`
	Mode   string `json:"mode,omitempty"`
}

// EventUsage 模型 token 用量。
type EventUsage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// 事件类型常量（前端 EVENT_TYPE_SET 里逐条订阅，名字必须一致）。
const (
	EventTaskCreated        = "task_created"
	EventFormUpdated        = "form_updated"
	EventStateChanged       = "state_changed"
	EventTransitionRejected = "transition_rejected"
	EventStepStarted        = "step_started"
	EventStepCompleted      = "step_completed"
	EventStepFailed         = "step_failed"
	EventToolCalled         = "tool_called"
	EventToolResult         = "tool_result"
	EventToolDenied         = "tool_denied"
	EventModelCalled        = "model_called"
	EventQCResult           = "qc_result"
	EventReviewDecision     = "review_decision"
	EventDeliverableCreated = "deliverable_created"
	EventFaultInjected      = "fault_injected"
)

// EventTypes 全部事件类型，SSE 为每条事件写具名 event:。
var EventTypes = []string{
	EventTaskCreated, EventFormUpdated, EventStateChanged, EventTransitionRejected,
	EventStepStarted, EventStepCompleted, EventStepFailed, EventToolCalled,
	EventToolResult, EventToolDenied, EventModelCalled, EventQCResult,
	EventReviewDecision, EventDeliverableCreated, EventFaultInjected,
}

func IntPtr(v int) *int       { return &v }
func Int64Ptr(v int64) *int64 { return &v }
func BoolPtr(v bool) *bool    { return &v }
func StringPtr(v string) *string { return &v }
