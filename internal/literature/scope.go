package literature

import (
	"fmt"
	"sort"
	"strings"

	"digital-employee/internal/contract"
)

// Relevance 模型对一件发明是否属于这个方向的判断（judge_relevance 的输出项）。
type Relevance struct {
	ID       string `json:"id"`
	Relevant bool   `json:"relevant"`
	Reason   string `json:"reason"`
}

// ScopeInput evaluate_scope 的入参。
type ScopeInput struct {
	Screen    *ScreenResult `json:"screen"`
	Relevance []Relevance   `json:"relevance"`
	// Config 取自 qc.yaml 的 literature 节
	Config contract.LiteratureQC `json:"config"`
	// AllowSmallSample 发起人已确认“按现有资料继续”
	AllowSmallSample bool `json:"allowSmallSample"`
}

// Removed 一件被剔除的发明。
type Removed struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// Counts 清单核对的数量，写进资料概况。
type Counts struct {
	Rows         int `json:"rows"`
	Duplicates   int `json:"duplicates"`
	NotFound     int `json:"notFound"`
	FamilyMerged int `json:"familyMerged"` // 因同族合并少算的件数
	Inventions   int `json:"inventions"`
	Irrelevant   int `json:"irrelevant"`
	Included     int `json:"included"`
}

// ScopeResult 纳入范围。OK 为 false 时 Pause 说明为什么要停下来。
type ScopeResult struct {
	OK    bool   `json:"ok"`
	Pause *Gate  `json:"pause,omitempty"`
	Counts Counts `json:"counts"`
	// Included 纳入分析的发明
	Included []Invention `json:"included"`
	// Irrelevant 与方向无关被剔除的发明（带模型给的理由）
	Irrelevant []Removed `json:"irrelevant"`
	// SmallSample 纳入件数偏少但发起人已确认继续：风险与不确定性里要写明样本过少
	SmallSample bool `json:"smallSample,omitempty"`
}

// EvaluateScope 按模型的相关性判断算出纳入范围，并检查两个暂停条件（design.md §11.2）：
// 无关件超过有效条数的一半 → 清单与方向不符；纳入 0 件或偏少 → 资料不足。
func EvaluateScope(in ScopeInput) *ScopeResult {
	screen := in.Screen
	cfg := in.Config
	defaults := contract.DefaultLiteratureQC()
	if cfg.IrrelevantMaxShare <= 0 {
		cfg.IrrelevantMaxShare = defaults.IrrelevantMaxShare
	}
	if cfg.MinIncluded <= 0 {
		cfg.MinIncluded = defaults.MinIncluded
	}

	verdict := map[string]Relevance{}
	for _, item := range in.Relevance {
		verdict[strings.TrimSpace(item.ID)] = item
	}
	result := &ScopeResult{Included: []Invention{}, Irrelevant: []Removed{}}
	for _, inv := range screen.Inventions {
		v, judged := verdict[inv.ID]
		relevant := true
		reason := v.Reason
		if judged {
			relevant = v.Relevant
		} else if inv.HintRelevant == "no" {
			// 模型没给这一件的判断：按词表的提示处理，并写明
			relevant, reason = false, "模型未给出判断，按词表提示（IPC 分类不属于该方向）剔除"
		}
		if relevant {
			result.Included = append(result.Included, inv)
		} else {
			if strings.TrimSpace(reason) == "" {
				reason = "与技术方向无关"
			}
			result.Irrelevant = append(result.Irrelevant, Removed{ID: inv.ID, Title: inv.Title, Reason: reason})
		}
	}
	merged := 0
	for _, m := range screen.FamilyMerged {
		merged += len(m.Merged)
	}
	total := len(screen.Inventions)
	result.Counts = Counts{
		Rows: screen.Rows, Duplicates: len(screen.Duplicates), NotFound: len(screen.NotFound), FamilyMerged: merged,
		Inventions: total, Irrelevant: len(result.Irrelevant), Included: len(result.Included),
	}

	// 暂停条件 1：清单与方向不符
	if total > 0 && float64(len(result.Irrelevant)) > cfg.IrrelevantMaxShare*float64(total) {
		result.Pause = mismatchGate(screen, result, cfg)
		return result
	}
	// 暂停条件 2：资料不足
	included := len(result.Included)
	if included == 0 {
		result.Pause = &Gate{Code: "insufficient_material", Message: "资料不足，无法完成研判：清单里没有可纳入分析的专利。请补充资料后继续，或终止任务。",
			Detail: map[string]any{"included": 0}}
		return result
	}
	if included < cfg.MinIncluded {
		if !in.AllowSmallSample {
			result.Pause = &Gate{
				Code: "insufficient_material",
				Message: fmt.Sprintf("资料偏少：清单核对后只纳入 %d 项发明（少于 %d 项），样本过少，结论的可靠性有限。"+
					"可以补充资料，或选择“按现有资料继续”（简报会在“风险与不确定性”里写明样本过少）。", included, cfg.MinIncluded),
				Detail: map[string]any{"included": included, "minIncluded": cfg.MinIncluded},
			}
			return result
		}
		result.SmallSample = true
	}
	result.OK = true
	return result
}

func mismatchGate(screen *ScreenResult, result *ScopeResult, cfg contract.LiteratureQC) *Gate {
	reasons := map[string]int{}
	for _, r := range result.Irrelevant {
		reasons[r.Reason]++
	}
	type kv struct {
		Reason string
		N      int
	}
	top := []kv{}
	for reason, n := range reasons {
		top = append(top, kv{reason, n})
	}
	sort.SliceStable(top, func(a, b int) bool { return top[a].N > top[b].N || top[a].N == top[b].N && top[a].Reason < top[b].Reason })
	parts := []string{
		fmt.Sprintf("清单与填写的技术方向“%s”不符：%d 项发明中有 %d 项与该方向无关（超过一半）", screen.Direction, result.Counts.Inventions, result.Counts.Irrelevant),
	}
	if screen.ListTheme != nil && screen.ListTheme.Count > 0 {
		parts = append(parts, fmt.Sprintf("按 IPC 分类，清单中 %d/%d 件专利属于“%s”", screen.ListTheme.Count, screen.ListTheme.Total, screen.ListTheme.Direction))
	}
	if len(top) > 0 {
		parts = append(parts, "主要依据："+top[0].Reason)
	}
	parts = append(parts, "请修改技术方向，或换一份清单后继续；也可以终止任务")
	detail := map[string]any{
		"direction": screen.Direction, "inventions": result.Counts.Inventions, "irrelevant": result.Counts.Irrelevant,
	}
	if screen.ListTheme != nil {
		detail["listTheme"] = screen.ListTheme
	}
	return &Gate{Code: "list_direction_mismatch", Message: strings.Join(parts, "。"), Detail: detail}
}
