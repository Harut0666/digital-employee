package contract

// 工具契约（tools.ts）。工具输出的 JSON 形状是员工配置里 prompt/QC 的输入，
// 必须与 TS 版逐字段一致。

// 工具名常量（白名单也用它）。
const (
	ToolValidateSubmission  = "validate_submission"
	ToolLoadSales           = "load_sales"
	ToolComputeMetrics      = "compute_metrics"
	ToolDetectContradictions = "detect_contradictions"
	ToolParseMinutes        = "parse_minutes"
	ToolRenderReportDocx    = "render_report_docx"
	ToolQueryTask           = "query_task"
	ToolWebSearch           = "web_search"
	ToolAdminSetFaults      = "admin_set_faults"
)

// ToolNames 全部工具名。
var ToolNames = []string{
	ToolValidateSubmission, ToolLoadSales, ToolComputeMetrics, ToolDetectContradictions,
	ToolParseMinutes, ToolRenderReportDocx, ToolQueryTask, ToolWebSearch, ToolAdminSetFaults,
}

// AttachmentRef 工作区内的附件引用。
type AttachmentRef struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// ---- validate_submission ----

type ValidateSubmissionInput struct {
	Period             IsoWeek         `json:"period"`
	Attachments        []AttachmentRef `json:"attachments"`
	AllowMissingMinutes bool           `json:"allowMissingMinutes,omitempty"`
	AllowPartialPeriod  bool           `json:"allowPartialPeriod,omitempty"`
}

// MinutesDetection 纪要文件的周次识别结果。
type MinutesDetection struct {
	Path string  `json:"path"`
	Week *IsoWeek `json:"week"` // 识别不出为 null
}

// ValidateSubmissionOutput 准入校验结果。OK=false 时 Pause 给出最根本的一个原因。
type ValidateSubmissionOutput struct {
	OK         bool              `json:"ok"`
	Pause      *PauseReason      `json:"pause,omitempty"`
	Sales      *SalesFile        `json:"sales,omitempty"`
	Dimensions Dimensions        `json:"dimensions"`
	Minutes    []MinutesDetection `json:"minutes"`
	Coverage   *DateRange        `json:"coverage,omitempty"`
}

// SalesFile 识别出的销售明细文件。
type SalesFile struct {
	Path string `json:"path"`
	Kind string `json:"kind"` // xlsx | csv
}

// Dimensions 维表是否齐全。
type Dimensions struct {
	Stores   bool `json:"stores"`
	Products bool `json:"products"`
	Targets  bool `json:"targets"`
}

// DateRange 数据覆盖范围（只比较日期）。
type DateRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ---- load_sales ----

type LoadSalesInput struct {
	SalesPath string `json:"salesPath"`
	OutDir    string `json:"outDir"`
}

type LoadSalesOutput struct {
	DatasetPath string    `json:"datasetPath"`
	Rows        int       `json:"rows"`
	Coverage    DateRange `json:"coverage"`
}

// ---- compute_metrics ----

type ComputeMetricsInput struct {
	DatasetPath      string  `json:"datasetPath"`
	Period           IsoWeek `json:"period"`
	AnomalyThreshold float64 `json:"anomalyThreshold"`
}

// ---- detect_contradictions ----

type ClaimSubjectLevel string

const (
	ClaimCompany  ClaimSubjectLevel = "company"
	ClaimRegion   ClaimSubjectLevel = "region"
	ClaimStore    ClaimSubjectLevel = "store"
	ClaimCategory ClaimSubjectLevel = "category"
)

type ClaimSubject struct {
	Level ClaimSubjectLevel `json:"level"`
	Key   string            `json:"key"`
}

type ClaimDirection string

const (
	ClaimFlat ClaimDirection = "flat"
	ClaimUp   ClaimDirection = "up"
	ClaimDown ClaimDirection = "down"
)

// Claim 纪要里的一条可核验说法。
type Claim struct {
	Subject    ClaimSubject   `json:"subject"`
	Metric     string         `json:"metric"` // 目前只核验 wow
	Direction  ClaimDirection `json:"direction"`
	Magnitude  *float64       `json:"magnitude,omitempty"`
	SourceWeek IsoWeek        `json:"sourceWeek"`
	Quote      string         `json:"quote"`
}

// ContradictionThresholds 矛盾判定阈值（qc.yaml 的 contradiction 节）。
type ContradictionThresholds struct {
	FlatTolerance      float64 `json:"flatTolerance" yaml:"flatTolerance"`
	DirectionMin       float64 `json:"directionMin" yaml:"directionMin"`
	MagnitudeTolerance float64 `json:"magnitudeTolerance" yaml:"magnitudeTolerance"`
}

type DetectContradictionsInput struct {
	Claims     []Claim                 `json:"claims"`
	Facts      WeeklyFacts             `json:"facts"`
	Thresholds ContradictionThresholds `json:"thresholds"`
}

type ContradictionRule string

const (
	RuleFlat      ContradictionRule = "flat"
	RuleDirection ContradictionRule = "direction"
	RuleMagnitude ContradictionRule = "magnitude"
)

// DetectedSubject 矛盾针对的对象，Name 是写进“异常与原因”章节时的展示名。
type DetectedSubject struct {
	Level ClaimSubjectLevel `json:"level"`
	Key   string            `json:"key"`
	Name  string            `json:"name"`
}

// DetectedContradiction 程序判出的一条矛盾。
type DetectedContradiction struct {
	ID         string            `json:"id"`
	Subject    DetectedSubject   `json:"subject"`
	Claim      DetectedClaim     `json:"claim"`
	ActualWow  float64           `json:"actualWow"`
	Rule       ContradictionRule `json:"rule"`
	SourceWeek IsoWeek           `json:"sourceWeek"`
	Quote      string            `json:"quote"`
}

// DetectedClaim 矛盾里的“纪要说法”。
type DetectedClaim struct {
	Direction ClaimDirection `json:"direction"`
	Magnitude *float64       `json:"magnitude,omitempty"`
}

type DetectContradictionsOutput struct {
	Contradictions []DetectedContradiction `json:"contradictions"`
	SkippedCount   int                     `json:"skippedCount"`
}

// ---- parse_minutes ----

type ParseMinutesInput struct {
	Paths []string `json:"paths"`
}

type MinutesParagraph struct {
	ID      string `json:"id"`
	Section string `json:"section,omitempty"`
	Text    string `json:"text"`
}

type ParsedMinutes struct {
	Path       string             `json:"path"`
	Week       *IsoWeek           `json:"week"`
	Title      string             `json:"title"`
	Paragraphs []MinutesParagraph `json:"paragraphs"`
}

type ParseMinutesOutput struct {
	Minutes []ParsedMinutes `json:"minutes"`
}

// ---- render_report_docx ----

type RenderReportInput struct {
	Report         RenderedReport `json:"report"`
	Facts          WeeklyFacts    `json:"facts"`
	OutDir         string         `json:"outDir"`
	IdempotencyKey string         `json:"idempotencyKey"`
}

// RenderedReport 已用事实渲染完槽位的报告内容。
type RenderedReport struct {
	Title    string              `json:"title"`
	Sections []RenderedSection   `json:"sections"`
}

type RenderedSection struct {
	ID         string   `json:"id"`
	Heading    string   `json:"heading"`
	Paragraphs []string `json:"paragraphs,omitempty"`
	Bullets    []string `json:"bullets,omitempty"`
}

type RenderReportOutput struct {
	DocxPath      string `json:"docxPath"`
	SHA256        string `json:"sha256"`
	EstimatedPages float64 `json:"estimatedPages"`
	ChartCount    int    `json:"chartCount"`
}

// ---- 故障注入 ----

type FaultMode string

const (
	FaultTimeout       FaultMode = "timeout"
	FaultError         FaultMode = "error"
	FaultInvalidOutput FaultMode = "invalid_output"
)

type FaultConfig struct {
	Tools map[string]FaultMode `json:"tools"`
}

type AdminSetFaultsInput struct {
	Tools map[string]FaultMode `json:"tools"`
}

type AdminSetFaultsOutput struct {
	OK    bool                `json:"ok"`
	Tools map[string]FaultMode `json:"tools"`
}
