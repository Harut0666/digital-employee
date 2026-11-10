package contract

// 技术文献研判员工（场景 B，docs/design.md §11）用到的契约：质检档配置、专用工具名、专用暂停码。
// 周报员工不受影响：QCConfig.Profile 缺省为空，走原来的 8 类内置检查。

// QCProfileLiterature 质检档：技术文献研判简报（见 internal/literature）。
const QCProfileLiterature = "literature"

// 场景 B 的工具名。
const (
	// ToolValidateLiteratureSubmission 准入校验：技术方向与专利清单
	ToolValidateLiteratureSubmission = "validate_literature_submission"
	// ToolScreenPatentList 清单核对：去重、库中查不到、同族合并，并给出路线词表与相关性提示
	ToolScreenPatentList = "screen_patent_list"
	// ToolEvaluateScope 按模型的相关性判断算纳入范围；方向不符或资料不足时暂停
	ToolEvaluateScope = "evaluate_scope"
	// ToolComputeLiteratureStats 路线、申请趋势、主要申请人、转化线索的统计
	ToolComputeLiteratureStats = "compute_literature_stats"
	// ToolSearchLibrary 在论文库里按方向和路线检索佐证论文（BM25，只用本地资料库）
	ToolSearchLibrary = "search_library"
	// ToolRenderBriefMD 把简报草稿、统计和来源渲染成 Markdown，图表 PNG 放旁边的 assets 文件夹
	ToolRenderBriefMD = "render_brief_md"
)

// LiteratureTools 场景 B 的全部工具名（加入工具白名单）。
var LiteratureTools = []string{
	ToolValidateLiteratureSubmission, ToolScreenPatentList, ToolEvaluateScope,
	ToolComputeLiteratureStats, ToolSearchLibrary, ToolRenderBriefMD,
}

// LiteratureQC 技术文献研判简报的质检参数（qc.yaml 的 literature 节）。
type LiteratureQC struct {
	// Sections 简报章节（顺序即简报顺序）；“来源清单”由程序生成，不在这里
	Sections []SectionSpec `json:"sections" yaml:"sections"`
	// SummaryMaxItems 结论摘要最多几条，每条都要带引用角标
	SummaryMaxItems int `json:"summaryMaxItems" yaml:"summaryMaxItems"`
	// MinCharts 至少几张图表
	MinCharts int `json:"minCharts" yaml:"minCharts"`
	// MaxPages 正文不超过几页（按字数估算）
	MaxPages float64 `json:"maxPages" yaml:"maxPages"`
	// MinIncluded 纳入分析的发明少于这个数时暂停：0 件只能补充资料或终止，1 到 MinIncluded-1 件可选“按现有资料继续”
	MinIncluded int `json:"minIncluded" yaml:"minIncluded"`
	// IrrelevantMaxShare 与方向无关的发明超过有效条数的这个比例时暂停（清单与方向不符）
	IrrelevantMaxShare float64 `json:"irrelevantMaxShare" yaml:"irrelevantMaxShare"`
	// CutoffDate 资料库的数据截止日（YYYY-MM-DD），趋势里据此标注公开滞后
	CutoffDate string `json:"cutoffDate" yaml:"cutoffDate"`
	// IncompleteYears 截止年份及其前几年标注“因公开滞后不完整”
	IncompleteYears int `json:"incompleteYears" yaml:"incompleteYears"`
	// ForbiddenTerms 简报里不得出现的词
	ForbiddenTerms []string `json:"forbiddenTerms" yaml:"forbiddenTerms"`
}

// DefaultLiteratureQC 缺省值（qc.yaml 里省略的参数按它补）。
func DefaultLiteratureQC() LiteratureQC {
	return LiteratureQC{
		SummaryMaxItems:    5,
		MinCharts:          1,
		MaxPages:           5,
		MinIncluded:        5,
		IrrelevantMaxShare: 0.5,
		CutoffDate:         "2026-10-15",
		IncompleteYears:    2,
	}
}
