package tools

// 场景 B（技术文献研判员工）的工具：薄薄一层，把工作流里的入参解码后交给 internal/literature。
// 工具全部只读本地资料库，不联网（design.md §11.1）。

import (
	"fmt"
	"path/filepath"
	"strings"

	"digital-employee/internal/contract"
	"digital-employee/internal/literature"
	"digital-employee/internal/render"
)

// fontPathsOf 图表字体候选路径：注册表创建时可用 SetChartFontPaths 指定，空则按系统默认找。
func (r *Registry) chartFonts() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.chartFontPaths
}

// SetChartFontPaths 指定图表中文字体候选路径（场景 B 的图表用）。
func (r *Registry) SetChartFontPaths(paths []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chartFontPaths = paths
}

type screenToolInput struct {
	Direction   string `json:"direction"`
	ListPath    string `json:"listPath"`
	ListName    string `json:"listName"`
	EmployeeDir string `json:"employeeDir"`
}

// screenToolOutput 清单核对的输出：核对结果加上 ok / pause（清单里一件能在库中查到的都没有时暂停）。
type screenToolOutput struct {
	OK    bool             `json:"ok"`
	Pause *literature.Gate `json:"pause,omitempty"`
	*literature.ScreenResult
}

type scopeToolInput struct {
	Screen           *literature.ScreenResult `json:"screen"`
	Relevance        []literature.Relevance   `json:"relevance"`
	Config           *contract.LiteratureQC   `json:"config"`
	AllowSmallSample bool                     `json:"allowSmallSample"`
}

type statsToolInput struct {
	Direction   string                      `json:"direction"`
	Included    []literature.Invention      `json:"included"`
	Assignments []literature.RouteAssignment `json:"assignments"`
	Config      *contract.LiteratureQC      `json:"config"`
}

type searchToolInput struct {
	Direction   string   `json:"direction"`
	Queries     []string `json:"queries"`
	Scope       string   `json:"scope"`
	TopK        int      `json:"topK"`
	Exclude     []string `json:"exclude"`
	EmployeeDir string   `json:"employeeDir"`
}

type briefToolInput struct {
	Direction      string                   `json:"direction"`
	Draft          literature.BriefDraft    `json:"draft"`
	Stats          *literature.Stats        `json:"stats"`
	Scope          *literature.ScopeResult  `json:"scope"`
	Screen         *literature.ScreenResult `json:"screen"`
	Config         *contract.LiteratureQC   `json:"config"`
	OutDir         string                   `json:"outDir"`
	IdempotencyKey string                   `json:"idempotencyKey"`
	EmployeeDir    string                   `json:"employeeDir"`
}

func literatureConfig(c *contract.LiteratureQC) contract.LiteratureQC {
	d := contract.DefaultLiteratureQC()
	if c == nil {
		return d
	}
	cfg := *c
	if cfg.SummaryMaxItems <= 0 {
		cfg.SummaryMaxItems = d.SummaryMaxItems
	}
	if cfg.MinCharts <= 0 {
		cfg.MinCharts = d.MinCharts
	}
	if cfg.MaxPages <= 0 {
		cfg.MaxPages = d.MaxPages
	}
	if cfg.MinIncluded <= 0 {
		cfg.MinIncluded = d.MinIncluded
	}
	if cfg.IrrelevantMaxShare <= 0 {
		cfg.IrrelevantMaxShare = d.IrrelevantMaxShare
	}
	if cfg.CutoffDate == "" {
		cfg.CutoffDate = d.CutoffDate
	}
	if cfg.IncompleteYears <= 0 {
		cfg.IncompleteYears = d.IncompleteYears
	}
	return cfg
}

// callLiterature 处理场景 B 的工具；不是 B 的工具返回 handled=false。
func (r *Registry) callLiterature(name string, args any) (result contract.ToolCallResult, handled bool) {
	switch name {
	case contract.ToolValidateLiteratureSubmission:
		var in literature.ValidateInput
		if err := decodeArgs(args, &in); err != nil {
			return decodeFailure(name, err), true
		}
		if mode, ok := r.faultOf(name); ok {
			return r.faultResult(name, mode), true
		}
		out, err := literature.Validate(in)
		if err != nil {
			return failure(name, err), true
		}
		return contract.ToolCallResult{OK: true, Data: out}, true

	case contract.ToolScreenPatentList:
		var in screenToolInput
		if err := decodeArgs(args, &in); err != nil {
			return decodeFailure(name, err), true
		}
		if mode, ok := r.faultOf(name); ok {
			return r.faultResult(name, mode), true
		}
		lib, err := literature.LoadLibrary(literature.LibraryDir(in.EmployeeDir))
		if err != nil {
			return failure(name, err), true
		}
		lex, err := literature.LoadLexicon(in.EmployeeDir)
		if err != nil {
			return failure(name, err), true
		}
		rows, err := literature.ReadList(in.ListPath)
		if err != nil {
			return failure(name, err), true
		}
		listName := in.ListName
		if listName == "" {
			listName = filepath.Base(in.ListPath)
		}
		screened := literature.Screen(rows, lib, lex, in.Direction, listName)
		out := screenToolOutput{OK: screened.Gate == nil, Pause: screened.Gate, ScreenResult: screened}
		return contract.ToolCallResult{OK: true, Data: out}, true

	case contract.ToolEvaluateScope:
		var in scopeToolInput
		if err := decodeArgs(args, &in); err != nil {
			return decodeFailure(name, err), true
		}
		if mode, ok := r.faultOf(name); ok {
			return r.faultResult(name, mode), true
		}
		if in.Screen == nil {
			return failure(name, fmt.Errorf("缺少清单核对结果")), true
		}
		out := literature.EvaluateScope(literature.ScopeInput{
			Screen: in.Screen, Relevance: in.Relevance, Config: literatureConfig(in.Config), AllowSmallSample: in.AllowSmallSample,
		})
		return contract.ToolCallResult{OK: true, Data: out}, true

	case contract.ToolComputeLiteratureStats:
		var in statsToolInput
		if err := decodeArgs(args, &in); err != nil {
			return decodeFailure(name, err), true
		}
		if mode, ok := r.faultOf(name); ok {
			return r.faultResult(name, mode), true
		}
		out := literature.ComputeStats(literature.StatsInput{
			Direction: in.Direction, Included: in.Included, Assignments: in.Assignments, Config: literatureConfig(in.Config),
		})
		return contract.ToolCallResult{OK: true, Data: out}, true

	case contract.ToolSearchLibrary:
		var in searchToolInput
		if err := decodeArgs(args, &in); err != nil {
			return decodeFailure(name, err), true
		}
		if mode, ok := r.faultOf(name); ok {
			return r.faultResult(name, mode), true
		}
		lib, err := literature.LoadLibrary(literature.LibraryDir(in.EmployeeDir))
		if err != nil {
			return failure(name, err), true
		}
		out := literature.Search(lib, literature.SearchInput{
			Direction: in.Direction, Queries: in.Queries, Scope: in.Scope, TopK: in.TopK, Exclude: in.Exclude,
		})
		return contract.ToolCallResult{OK: true, Data: out}, true

	case contract.ToolRenderBriefMD:
		var in briefToolInput
		if err := decodeArgs(args, &in); err != nil {
			return decodeFailure(name, err), true
		}
		if mode, ok := r.faultOf(name); ok {
			return r.faultResult(name, mode), true
		}
		if strings.TrimSpace(in.OutDir) == "" {
			return failure(name, fmt.Errorf("outDir 不能为空")), true
		}
		out, err := literature.RenderBrief(literature.RenderInput{
			Direction: in.Direction, Draft: in.Draft, Stats: in.Stats, Scope: in.Scope, Screen: in.Screen,
			Config: literatureConfig(in.Config), OutDir: in.OutDir, IdempotencyKey: in.IdempotencyKey,
			EmployeeDir: in.EmployeeDir, FontPaths: r.chartFonts(),
		})
		if err != nil {
			return failure(name, err), true
		}
		return contract.ToolCallResult{OK: true, Data: out}, true
	}
	return contract.ToolCallResult{}, false
}

var _ = render.BarChart{} // 图表由 literature.RenderBrief 经 render.RenderBarPNG 绘制
