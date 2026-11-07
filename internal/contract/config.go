package contract

// 员工配置契约（employee-config.ts）。一个员工一个目录，配置文件是唯一数据来源。
// 这些结构同时用于解析 YAML（工作区里的 employees/）与回写。

// EmployeeIDPattern 员工 id 即目录名：只允许小写字母、数字和连字符。
const EmployeeIDPattern = `^[a-z0-9-]+$`

// ModelRef 步骤用的模型引用。
type ModelRef struct {
	Connection string  `json:"connection" yaml:"connection"`
	Model      string  `json:"model" yaml:"model"`
	Thinking   *string `json:"thinking,omitempty" yaml:"thinking,omitempty"`
}

const (
	ThinkingOff     = "off"
	ThinkingMinimal = "minimal"
	ThinkingLow     = "low"
	ThinkingMedium  = "medium"
	ThinkingHigh    = "high"
	ThinkingXHigh   = "xhigh"
)

// StepLimits 判断步骤的上限；DurationSec 省略时由引擎按设置/内置 120 秒解析。
type StepLimits struct {
	Rounds      *int     `json:"rounds,omitempty" yaml:"rounds,omitempty"`
	DurationSec *float64 `json:"durationSec,omitempty" yaml:"durationSec,omitempty"`
}

// StepConfig 一个步骤。kind 决定哪些字段有意义：
//
//	compute: tool + input
//	judge:   prompt + input + tools + outputSchema + limits + model + rework + when
//	qc:      无
type StepConfig struct {
	ID    string `json:"id" yaml:"id"`
	Label string `json:"label" yaml:"label"`
	Kind  string `json:"kind" yaml:"kind"` // compute | judge | qc

	Tool   string         `json:"tool,omitempty" yaml:"tool,omitempty"`
	Prompt string         `json:"prompt,omitempty" yaml:"prompt,omitempty"`
	Input  map[string]any `json:"input,omitempty" yaml:"input,omitempty"`
	Tools  []string       `json:"tools,omitempty" yaml:"tools,omitempty"`

	OutputSchema string      `json:"outputSchema,omitempty" yaml:"outputSchema,omitempty"`
	Limits       *StepLimits `json:"limits,omitempty" yaml:"limits,omitempty"`
	Model        *ModelRef   `json:"model,omitempty" yaml:"model,omitempty"`
	Rework       bool        `json:"rework,omitempty" yaml:"rework,omitempty"`
	When         string      `json:"when,omitempty" yaml:"when,omitempty"` // always | rework
	Optional     bool        `json:"optional,omitempty" yaml:"optional,omitempty"`
}

const (
	StepKindCompute = "compute"
	StepKindJudge   = "judge"
	StepKindQC      = "qc"
)

// TaskLimits 任务级预算。
type TaskLimits struct {
	ModelCalls  int `json:"modelCalls" yaml:"modelCalls"`
	DurationSec int `json:"durationSec" yaml:"durationSec"`
}

// ToolRetryLimits 工具重试。BackoffMS 逐次使用，用尽后取最后一项。
type ToolRetryLimits struct {
	Attempts  int   `json:"attempts" yaml:"attempts"`
	BackoffMS []int `json:"backoffMs" yaml:"backoffMs"`
}

// WorkflowLimits workflow.limits。
type WorkflowLimits struct {
	Task      TaskLimits      `json:"task" yaml:"task"`
	ToolRetry ToolRetryLimits `json:"toolRetry" yaml:"toolRetry"`
	// QCAutofix 省略时由引擎按设置/内置 3 解析。
	QCAutofix *int `json:"qcAutofix,omitempty" yaml:"qcAutofix,omitempty"`
}

// QCSources 质检输入取自哪些步骤。
type QCSources struct {
	Draft    string `json:"draft" yaml:"draft"`
	Minutes  string `json:"minutes" yaml:"minutes"`
	Rendered string `json:"rendered" yaml:"rendered"`
}

// QCFixSteps 质检不通过时按问题类别指定应重跑的步骤。
type QCFixSteps struct {
	Write  string `json:"write,omitempty" yaml:"write,omitempty"`
	Causes string `json:"causes,omitempty" yaml:"causes,omitempty"`
}

// EmployeeWorkflow workflow.yaml 的结构。
type EmployeeWorkflow struct {
	ID            string         `json:"id" yaml:"id"`
	Name          string         `json:"name" yaml:"name"`
	Description   string         `json:"description,omitempty" yaml:"description,omitempty"`
	Limits        WorkflowLimits `json:"limits" yaml:"limits"`
	AdmissionStep string         `json:"admissionStep" yaml:"admissionStep"`
	FactsStep     string         `json:"factsStep,omitempty" yaml:"factsStep,omitempty"`
	QCSources     QCSources      `json:"qcSources" yaml:"qcSources"`
	QCFixSteps    QCFixSteps     `json:"qcFixSteps" yaml:"qcFixSteps"`
	Steps         []StepConfig   `json:"steps" yaml:"steps"`

	// DeliverableName 交付物下载名模板，如 经营周报_{year}年第{week}周；省略时用磁盘上的文件名。
	DeliverableName string `json:"deliverableName,omitempty" yaml:"deliverableName,omitempty"`
}

// SectionSpec 章节定义（顺序即周报章节顺序）。
type SectionSpec struct {
	ID      string `json:"id" yaml:"id"`
	Heading string `json:"heading" yaml:"heading"`
}

// StructureQC 结构完整。
type StructureQC struct {
	Enabled             bool          `json:"enabled" yaml:"enabled"`
	Sections            []SectionSpec `json:"sections" yaml:"sections"`
	SummaryMaxSentences int           `json:"summaryMaxSentences" yaml:"summaryMaxSentences"`
	NextWeekMaxItems    int           `json:"nextWeekMaxItems" yaml:"nextWeekMaxItems"`
	MinCharts           int           `json:"minCharts" yaml:"minCharts"`
}

// EnabledQC 只带开关的检查。
type EnabledQC struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

// AnomalyCoverageQC 异常覆盖。
type AnomalyCoverageQC struct {
	Enabled   bool    `json:"enabled" yaml:"enabled"`
	Threshold float64 `json:"threshold" yaml:"threshold"`
}

// ForbiddenTermsQC 禁用指标。
type ForbiddenTermsQC struct {
	Enabled bool     `json:"enabled" yaml:"enabled"`
	Terms   []string `json:"terms" yaml:"terms"`
}

// LengthQC 篇幅。
type LengthQC struct {
	Enabled  bool    `json:"enabled" yaml:"enabled"`
	MaxPages float64 `json:"maxPages" yaml:"maxPages"`
}

// BuiltinQC 内置质检（只能调参数，不能删除）。
type BuiltinQC struct {
	Structure             StructureQC       `json:"structure" yaml:"structure"`
	Traceability          EnabledQC         `json:"traceability" yaml:"traceability"`
	AnomalyCoverage       AnomalyCoverageQC `json:"anomaly_coverage" yaml:"anomaly_coverage"`
	QuoteIntegrity        EnabledQC         `json:"quote_integrity" yaml:"quote_integrity"`
	ContradictionCoverage EnabledQC         `json:"contradiction_coverage" yaml:"contradiction_coverage"`
	ForbiddenTerms        ForbiddenTermsQC  `json:"forbidden_terms" yaml:"forbidden_terms"`
	Length                LengthQC          `json:"length" yaml:"length"`
	FeedbackResponse      EnabledQC         `json:"feedback_response" yaml:"feedback_response"`
}

// CustomRule 自定义质检规则。
type CustomRule struct {
	ID       string         `json:"id" yaml:"id"`
	Label    string         `json:"label" yaml:"label"`
	Template string         `json:"template" yaml:"template"`
	Enabled  bool           `json:"enabled" yaml:"enabled"`
	Params   map[string]any `json:"params" yaml:"params"`
}

// QCConfig qc.yaml 的结构。
type QCConfig struct {
	Builtin       BuiltinQC               `json:"builtin" yaml:"builtin"`
	Contradiction ContradictionThresholds `json:"contradiction" yaml:"contradiction"`
	Custom        []CustomRule            `json:"custom" yaml:"custom"`
}

// EmployeeConfig 加载后的完整员工配置。
type EmployeeConfig struct {
	ID            string                    `json:"id"`
	Dir           string                    `json:"dir"`
	Hash          string                    `json:"hash"`
	Skill         string                    `json:"skill"`
	Frontdesk     string                    `json:"frontdesk"`
	Workflow      EmployeeWorkflow          `json:"workflow"`
	QC            QCConfig                  `json:"qc"`
	FormSchema    map[string]any            `json:"formSchema"`
	FormUI        map[string]any            `json:"formUi,omitempty"`
	Prompts       map[string]string         `json:"prompts"`
	OutputSchemas map[string]map[string]any `json:"outputSchemas"`
}

// 质检模板名。
const (
	TemplateForbiddenTerms     = "forbidden_terms"
	TemplateRequiredTerms      = "required_terms"
	TemplateSectionMustContain = "section_must_contain"
	TemplateMaxItems           = "max_items"
	TemplateMaxPages           = "max_pages"
	TemplateNumberRange        = "number_range"
)

// 表单字段用途（form.ts）。
const (
	FormPurposeWritingRequirement = "writing_requirement"
	FormPurposeParamPrefix        = "param:"
)

// FormParams 程序已支持的表单参数。
var FormParams = map[string]string{
	"period":              "统计周期",
	"allowMissingMinutes": "无纪要继续",
	"allowPartialPeriod":  "按已有数据继续",
}

// FrontdeskReadonlyTools 开放给前台 Agent 的只读工具。
var FrontdeskReadonlyTools = []string{"query_task"}

// FrontdeskFixedTools 前台 Agent 固定具备的草稿类工具。
var FrontdeskFixedTools = []string{"fill_form_draft", "propose_card"}
