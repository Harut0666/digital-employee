// Package contract 是 Go 版唯一事实来源，逐字段移植自
// packages/core/src/contract/*.ts。JSON/YAML 字段名必须与 TS 完全一致：
// 前端 React 代码、工作区里已有的 settings.json / 员工配置 / 事件日志都按这些名字读写。
package contract

// TaskState 任务状态（states.ts）。
type TaskState string

const (
	StateDraft             TaskState = "draft"
	StateAdmission         TaskState = "admission"
	StateAwaitingInitiator TaskState = "awaiting_initiator"
	StateRunning           TaskState = "running"
	StateQC                TaskState = "qc"
	StateAwaitingOperator  TaskState = "awaiting_operator"
	StateAwaitingReview    TaskState = "awaiting_review"
	StateDelivered         TaskState = "delivered"
	StateTerminated        TaskState = "terminated"
)

// TaskStates 全部状态，顺序与前端的列表一致。
var TaskStates = []TaskState{
	StateDraft, StateAdmission, StateAwaitingInitiator, StateRunning, StateQC,
	StateAwaitingOperator, StateAwaitingReview, StateDelivered, StateTerminated,
}

// StateLabels 界面显示名。
var StateLabels = map[TaskState]string{
	StateDraft:             "草稿",
	StateAdmission:         "准入校验",
	StateAwaitingInitiator: "待发起人补充",
	StateRunning:           "执行中",
	StateQC:                "质检中",
	StateAwaitingOperator:  "待人工处理",
	StateAwaitingReview:    "待审核",
	StateDelivered:         "已交付",
	StateTerminated:        "已终止",
}

// IsTerminal 终态：已交付 / 已终止。
func IsTerminal(s TaskState) bool {
	return s == StateDelivered || s == StateTerminated
}

// IsPaused 暂停态：等外部动作才能继续。
func IsPaused(s TaskState) bool {
	return s == StateAwaitingInitiator || s == StateAwaitingOperator || s == StateAwaitingReview
}

// Role 角色。system 只出现在事件里，不能由 HTTP 头声明。
type Role string

const (
	RoleInitiator Role = "initiator"
	RoleReviewer  Role = "reviewer"
	RoleOperator  Role = "operator"
	RoleSystem    Role = "system"
)

// RoleLabels 界面显示名。
var RoleLabels = map[Role]string{
	RoleInitiator: "发起人",
	RoleReviewer:  "审核人",
	RoleOperator:  "操作员",
	RoleSystem:    "系统",
}

// TransitionTrigger 流转触发器。
type TransitionTrigger string

const (
	TriggerSubmit                 TransitionTrigger = "submit"
	TriggerAdmissionPassed        TransitionTrigger = "admission_passed"
	TriggerAdmissionFailed        TransitionTrigger = "admission_failed"
	TriggerSupplement             TransitionTrigger = "supplement"
	TriggerContinueWithoutMinutes TransitionTrigger = "continue_without_minutes"
	TriggerContinueWithPartial    TransitionTrigger = "continue_with_partial"
	TriggerTerminate              TransitionTrigger = "terminate"
	TriggerMaterialIssue          TransitionTrigger = "material_issue"
	TriggerStepsCompleted         TransitionTrigger = "steps_completed"
	TriggerLimitExceeded          TransitionTrigger = "limit_exceeded"
	TriggerToolFailed             TransitionTrigger = "tool_failed"
	TriggerModelUnavailable       TransitionTrigger = "model_unavailable"
	TriggerQcPassed               TransitionTrigger = "qc_passed"
	TriggerQcAutofix              TransitionTrigger = "qc_autofix"
	TriggerQcExhausted            TransitionTrigger = "qc_exhausted"
	TriggerRetry                  TransitionTrigger = "retry"
	TriggerApprove                TransitionTrigger = "approve"
	TriggerReturn                 TransitionTrigger = "return"
	TriggerNeedsHuman             TransitionTrigger = "needs_human"
)

// Transition 流转表的一行。
type Transition struct {
	From    TaskState
	To      TaskState
	Trigger TransitionTrigger
	Actor   Role
}

// Transitions 表外的流转一律拒绝，并写 transition_rejected 事件。
// 表只回答“这条流转是否合法”；某个暂停下谁能发起什么，由引擎的 availableActions 决定。
var Transitions = []Transition{
	{StateDraft, StateAdmission, TriggerSubmit, RoleInitiator},
	{StateDraft, StateTerminated, TriggerTerminate, RoleInitiator},
	{StateAdmission, StateRunning, TriggerAdmissionPassed, RoleSystem},
	{StateAdmission, StateAwaitingInitiator, TriggerAdmissionFailed, RoleSystem},
	{StateAdmission, StateAwaitingOperator, TriggerToolFailed, RoleSystem},
	{StateAdmission, StateTerminated, TriggerTerminate, RoleInitiator},
	{StateAwaitingInitiator, StateAdmission, TriggerSupplement, RoleInitiator},
	{StateAwaitingInitiator, StateAdmission, TriggerContinueWithoutMinutes, RoleInitiator},
	{StateAwaitingInitiator, StateAdmission, TriggerContinueWithPartial, RoleInitiator},
	{StateAwaitingInitiator, StateTerminated, TriggerTerminate, RoleInitiator},
	{StateRunning, StateAwaitingInitiator, TriggerMaterialIssue, RoleSystem},
	{StateRunning, StateQC, TriggerStepsCompleted, RoleSystem},
	{StateRunning, StateAwaitingOperator, TriggerLimitExceeded, RoleSystem},
	{StateRunning, StateAwaitingOperator, TriggerToolFailed, RoleSystem},
	{StateRunning, StateAwaitingOperator, TriggerModelUnavailable, RoleSystem},
	{StateRunning, StateAwaitingReview, TriggerNeedsHuman, RoleSystem},
	{StateRunning, StateTerminated, TriggerTerminate, RoleInitiator},
	{StateQC, StateAwaitingReview, TriggerQcPassed, RoleSystem},
	{StateQC, StateRunning, TriggerQcAutofix, RoleSystem},
	{StateQC, StateAwaitingOperator, TriggerQcExhausted, RoleSystem},
	{StateQC, StateTerminated, TriggerTerminate, RoleInitiator},
	{StateAwaitingOperator, StateRunning, TriggerRetry, RoleOperator},
	{StateAwaitingOperator, StateAdmission, TriggerRetry, RoleOperator},
	{StateAwaitingOperator, StateRunning, TriggerRetry, RoleInitiator},
	{StateAwaitingOperator, StateAdmission, TriggerRetry, RoleInitiator},
	{StateAwaitingOperator, StateTerminated, TriggerTerminate, RoleOperator},
	{StateAwaitingReview, StateDelivered, TriggerApprove, RoleReviewer},
	{StateAwaitingReview, StateRunning, TriggerReturn, RoleReviewer},
	{StateAwaitingReview, StateTerminated, TriggerTerminate, RoleReviewer},
}

// FindTransition 在流转表里查 from + trigger 的唯一一条（同一 from+trigger 只有一种目标）。
func FindTransition(from TaskState, trigger TransitionTrigger) (Transition, bool) {
	for _, t := range Transitions {
		if t.From == from && t.Trigger == trigger {
			return t, true
		}
	}
	return Transition{}, false
}

// PauseCode 暂停原因代码。
type PauseCode string

const (
	PauseMissingSales         PauseCode = "missing_sales"
	PauseMissingDimensions    PauseCode = "missing_dimensions"
	PausePeriodOutOfRange     PauseCode = "period_out_of_range"
	PausePeriodIncomplete     PauseCode = "period_incomplete"
	PauseMinutesMissing       PauseCode = "minutes_missing"
	PauseMinutesWeekMismatch  PauseCode = "minutes_week_mismatch"
	PauseInvalidInput         PauseCode = "invalid_input"
	PauseMissingList          PauseCode = "missing_list"
	PauseListDirection        PauseCode = "list_direction_mismatch"
	PauseInsufficientMaterial PauseCode = "insufficient_material"
	PauseMaterialIssue        PauseCode = "material_issue"
	PauseToolFailed           PauseCode = "tool_failed"
	PauseModelUnavailable     PauseCode = "model_unavailable"
	PauseLimitExceeded        PauseCode = "limit_exceeded"
	PauseQcExhausted          PauseCode = "qc_exhausted"
	PauseReworkNeedsHuman     PauseCode = "rework_needs_human"
	PauseAwaitingReview       PauseCode = "awaiting_review"
)

// PauseReason 暂停原因：给人看的中文说明 + 本暂停下允许发起的命令。
// Title、Resolution 只在快照里出现，读取时按 Code 和 Detail 生成，不写入事件；
// 工具名、失败代码等技术细节放在 Detail。
type PauseReason struct {
	Code       PauseCode      `json:"code" yaml:"code"`
	Title      string         `json:"title,omitempty" yaml:"title,omitempty"`
	Message    string         `json:"message" yaml:"message"`
	Resolution string         `json:"resolution,omitempty" yaml:"resolution,omitempty"`
	Options    []string       `json:"options,omitempty" yaml:"options,omitempty"`
	Detail     map[string]any `json:"detail,omitempty" yaml:"detail,omitempty"`
}
