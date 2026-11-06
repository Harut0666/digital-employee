package contract

// 判断步骤与质检的拆分接口（judgment.ts / qc.ts / draft 形状）。

// QcCheckResult 一条质检结果。
type QcCheckResult struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Passed bool   `json:"passed"`
	// Issues 不通过时的具体问题，会作为修正提示交给判断步骤
	Issues []string `json:"issues"`
	// FixStep 不通过时应重跑的步骤；为空表示无法自动修正，直接转人工
	FixStep string `json:"fixStep,omitempty"`
}

// DraftCause 原因匹配结果。
type DraftCause struct {
	AnomalyID  string   `json:"anomalyId"`
	Cause      string   `json:"cause"`
	Level      string   `json:"level"` // store | store_type | region | company | unknown
	SourceWeek *IsoWeek `json:"sourceWeek,omitempty"`
	Quote      string   `json:"quote,omitempty"`
}

// RevisionNote 重做时的修改说明。
type RevisionNote struct {
	Comment string   `json:"comment"`
	Sections []string `json:"sections"`
	Change  string   `json:"change"`
}

// DraftReport 判断步骤写出的报告草稿：叙述里只含槽位 {{path}}，不含裸数字。
type DraftReport struct {
	Title      string            `json:"title"`
	Sections   []RenderedSection `json:"sections"`
	Causes     []DraftCause      `json:"causes"`
	RevisionNotes []RevisionNote `json:"revisionNotes,omitempty"`
	ContradictionRefs []string   `json:"contradictionRefs,omitempty"`
}

// RenderedStats 渲染后的统计（render_report_docx 的返回）。
type RenderedStats struct {
	EstimatedPages float64 `json:"estimatedPages"`
	ChartCount     int     `json:"chartCount"`
}

// Feedback 审核退回意见。
type Feedback struct {
	Comment  string   `json:"comment"`
	Sections []string `json:"sections,omitempty"`
}

// QcInput 质检输入。
type QcInput struct {
	Config        QCConfig                `json:"config"`
	Draft         DraftReport             `json:"draft"`
	Facts         WeeklyFacts             `json:"facts"`
	Minutes       ParseMinutesOutput      `json:"minutes"`
	Contradictions []DetectedContradiction `json:"contradictions,omitempty"`
	Rendered      *RenderedStats          `json:"rendered,omitempty"`
	PreviousDraft *DraftReport            `json:"previousDraft,omitempty"`
	Feedback      *Feedback               `json:"feedback,omitempty"`
	FixSteps      *QCFixSteps             `json:"fixSteps,omitempty"`
}

// JudgmentFailure 判断步骤失败原因。
type JudgmentFailure string

const (
	FailStepRounds    JudgmentFailure = "step_rounds"
	FailStepDuration  JudgmentFailure = "step_duration"
	FailTaskCalls     JudgmentFailure = "task_calls"
	FailTaskDuration  JudgmentFailure = "task_duration"
	FailModelError    JudgmentFailure = "model_error"
	FailNoOutput      JudgmentFailure = "no_output"
	FailInvalidOutput JudgmentFailure = "invalid_output"
)

// JudgmentResult 判断步骤的结果。
type JudgmentResult struct {
	OK         bool
	Output     any
	Failure    JudgmentFailure
	Message    string
	ModelCalls int
}

// ModelTurn 一次模型调用的产物（写 model_called 事件、断点恢复时按日志重放）。
type ModelTurn struct {
	Turn       int             `json:"turn"`
	Response   any             `json:"response"`
	Usage      *EventUsage     `json:"usage,omitempty"`
	DurationMS int64           `json:"durationMs"`
}

// 槽位渲染：把 {{path}} 替换为格式化后的事实值；Unresolved 为无法解析的槽位。
type SlotRenderResult struct {
	Text       string
	Unresolved []string
}
