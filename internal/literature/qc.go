package literature

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"digital-employee/internal/contract"
)

// DefaultFixStep 质检不通过时默认重跑的步骤（workflow.qcFixSteps.write 可改）。
const DefaultFixStep = "write_brief"

// LengthMargin 篇幅留约 10% 余量，与周报一致。
const LengthMargin = 0.9

// QcInput 技术文献研判简报质检的输入。
type QcInput struct {
	Config        contract.LiteratureQC
	Draft         BriefDraft
	Stats         *Stats
	Scope         *ScopeResult
	Screen        *ScreenResult
	Rendered      *Rendered
	Lib           *Library
	PreviousDraft *BriefDraft
	Feedback      *contract.Feedback
	FixStep       string
}

func (in QcInput) fix() string {
	if in.FixStep != "" {
		return in.FixStep
	}
	return DefaultFixStep
}

func result(id, label string, issues []string, fixStep string) contract.QcCheckResult {
	if len(issues) == 0 {
		return contract.QcCheckResult{ID: id, Label: label, Passed: true, Issues: []string{}}
	}
	return contract.QcCheckResult{ID: id, Label: label, Passed: false, Issues: issues, FixStep: fixStep}
}

// sectionsNeedingCites 每句话都要有出处的章节；资料概况和风险与不确定性是对核对过程和局限的说明，不强求。
var sectionsNeedingCites = map[string]bool{"summary": true, "routes": true, "trend": true, "applicants": true, "leads": true}

// idPattern 专利编号和论文编号的形状。
var idPattern = regexp.MustCompile(`ZZ\d{10}[ABU]|LW\d{4}`)

var numberPattern = regexp.MustCompile(`\d+(?:\.\d+)?`)

// RunQC 按顺序做 7 项检查：结构完整、摘要角标、引用真实、数据溯源、禁用词、篇幅、意见响应（仅重做）。
func RunQC(in QcInput) []contract.QcCheckResult {
	cfg := in.Config
	d := contract.DefaultLiteratureQC()
	if cfg.SummaryMaxItems <= 0 {
		cfg.SummaryMaxItems = d.SummaryMaxItems
	}
	if cfg.MinCharts <= 0 {
		cfg.MinCharts = d.MinCharts
	}
	if cfg.MaxPages <= 0 {
		cfg.MaxPages = d.MaxPages
	}
	in.Config = cfg
	results := []contract.QcCheckResult{
		checkStructure(in),
		checkSummaryCited(in),
		checkCitesValid(in),
		checkNumbers(in),
		checkForbidden(in),
		checkLength(in),
	}
	if in.Feedback != nil {
		results = append(results, checkFeedback(in))
	}
	return results
}

func sectionByID(draft BriefDraft, id string) *BriefSection {
	for i := range draft.Sections {
		if draft.Sections[i].ID == id {
			return &draft.Sections[i]
		}
	}
	return nil
}

func headingOf(cfg contract.LiteratureQC, id string) string {
	for _, s := range cfg.Sections {
		if s.ID == id {
			return s.Heading
		}
	}
	return id
}

func checkStructure(in QcInput) contract.QcCheckResult {
	cfg := in.Config
	issues := []string{}
	draftIDs := []string{}
	for _, s := range in.Draft.Sections {
		draftIDs = append(draftIDs, s.ID)
	}
	missing := []string{}
	for _, s := range cfg.Sections {
		if sectionByID(in.Draft, s.ID) == nil {
			missing = append(missing, s.Heading)
		}
	}
	if len(missing) > 0 {
		issues = append(issues, "缺少章节："+strings.Join(missing, "、"))
	}
	last := -1
	for _, s := range cfg.Sections {
		at := indexOf(draftIDs, s.ID)
		if at < 0 {
			continue
		}
		if at < last {
			headings := []string{}
			for _, x := range cfg.Sections {
				headings = append(headings, x.Heading)
			}
			issues = append(issues, "章节顺序不正确，应为："+strings.Join(headings, " → "))
			break
		}
		last = at
	}
	if summary := sectionByID(in.Draft, "summary"); summary != nil {
		if len(summary.Claims) == 0 {
			issues = append(issues, "结论摘要是空的")
		}
		if len(summary.Claims) > cfg.SummaryMaxItems {
			issues = append(issues, fmt.Sprintf("结论摘要有 %d 条，不得超过 %d 条", len(summary.Claims), cfg.SummaryMaxItems))
		}
	}
	// 每条技术路线都要有“主要技术手段、代表性性能指标水平”的描述；数量和代表专利由程序列出
	if routes := sectionByID(in.Draft, "routes"); routes != nil && in.Stats != nil {
		described := map[string]bool{}
		for _, cl := range routes.Claims {
			described[strings.TrimSpace(cl.Route)] = true
		}
		for _, r := range in.Stats.Routes {
			if !described[r.Name] {
				issues = append(issues, fmt.Sprintf("技术路线「%s」缺少对主要技术手段和性能指标水平的描述（claims 的 route 要写这条路线的名字）", r.Name))
			}
		}
	}
	if in.Rendered != nil && in.Rendered.ChartCount < cfg.MinCharts {
		issues = append(issues, fmt.Sprintf("图表只有 %d 张，至少需要 %d 张", in.Rendered.ChartCount, cfg.MinCharts))
	}
	return result("structure", "结构完整", issues, in.fix())
}

func checkSummaryCited(in QcInput) contract.QcCheckResult {
	issues := []string{}
	if summary := sectionByID(in.Draft, "summary"); summary != nil {
		for i, cl := range summary.Claims {
			if len(nonEmpty(cl.Cites)) == 0 {
				issues = append(issues, fmt.Sprintf("结论摘要第 %d 条没有引用角标（cites 为空）", i+1))
			}
		}
	}
	return result("summary_cited", "摘要角标", issues, in.fix())
}

// removedIDs 被剔除、不得引用的编号：与方向无关的发明（库中查不到的本来就不在库里）。
func removedIDs(in QcInput) map[string]string {
	out := map[string]string{}
	if in.Scope != nil {
		for _, r := range in.Scope.Irrelevant {
			out[r.ID] = "与方向无关，已被剔除"
		}
	}
	return out
}

func checkCitesValid(in QcInput) contract.QcCheckResult {
	issues := []string{}
	removed := removedIDs(in)
	legitMentions := map[string]bool{}
	if in.Screen != nil {
		for _, e := range in.Screen.NotFound {
			legitMentions[e.ID] = true
		}
		for _, e := range in.Screen.Duplicates {
			legitMentions[e.ID] = true
		}
	}
	reported := map[string]bool{}
	for _, s := range in.Draft.Sections {
		for i, cl := range s.Claims {
			where := fmt.Sprintf("「%s」第 %d 句", headingOf(in.Config, s.ID), i+1)
			if sectionsNeedingCites[s.ID] && len(nonEmpty(cl.Cites)) == 0 && s.ID != "summary" {
				issues = append(issues, where+"没有引用角标（cites 为空）：每句话都要有出处")
			}
			for _, id := range nonEmpty(cl.Cites) {
				if in.Lib.Kind(id) == "" {
					key := "lib:" + id
					if !reported[key] {
						reported[key] = true
						issues = append(issues, fmt.Sprintf("%s引用的编号 %s 不在专利库或论文库中，不得编造编号", where, id))
					}
					continue
				}
				if why, bad := removed[id]; bad {
					key := "removed:" + id
					if !reported[key] {
						reported[key] = true
						issues = append(issues, fmt.Sprintf("%s引用了 %s，但它%s，不得作为依据", where, id, why))
					}
				}
			}
			// 正文里直接写出的编号也不能是库里没有的
			for _, id := range idPattern.FindAllString(cl.Text, -1) {
				if in.Lib.Kind(id) == "" && !legitMentions[id] {
					key := "text:" + id
					if !reported[key] {
						reported[key] = true
						issues = append(issues, fmt.Sprintf("%s的正文里出现了库中不存在的编号 %s", where, id))
					}
				}
			}
		}
	}
	return result("cite_valid", "引用真实", issues, in.fix())
}

// allowedNumbers 叙述里可以出现的数字：统计结果和核对数量里的，加上所引资料原文里的。
func allowedNumbers(in QcInput) map[string]bool {
	allowed := map[string]bool{}
	addFrom := func(text string) {
		for _, n := range numberPattern.FindAllString(text, -1) {
			allowed[normNumber(n)] = true
		}
	}
	if in.Stats != nil {
		raw, _ := json.Marshal(in.Stats)
		addFrom(string(raw))
	}
	if in.Scope != nil {
		raw, _ := json.Marshal(in.Scope.Counts)
		addFrom(string(raw))
	}
	if in.Screen != nil {
		addFrom(fmt.Sprintf("%d %d %d %d %d %d", in.Screen.Rows, len(in.Screen.Duplicates), len(in.Screen.NotFound), len(in.Screen.FamilyMerged), in.Screen.Library.Patents, in.Screen.Library.Papers))
	}
	return allowed
}

func normNumber(text string) string {
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return text
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func checkNumbers(in QcInput) contract.QcCheckResult {
	issues := []string{}
	base := allowedNumbers(in)
	for _, s := range in.Draft.Sections {
		for i, cl := range s.Claims {
			allowed := map[string]bool{}
			for k := range base {
				allowed[k] = true
			}
			// 所引资料原文里的数字（性能指标等）可以照写
			for _, id := range nonEmpty(cl.Cites) {
				if p, ok := in.Lib.Patents[id]; ok {
					for _, n := range numberPattern.FindAllString(p.Title+" "+p.Abstract+" "+p.Claim, -1) {
						allowed[normNumber(n)] = true
					}
				}
				if p, ok := in.Lib.Papers[id]; ok {
					for _, n := range numberPattern.FindAllString(p.Title+" "+p.Abstract, -1) {
						allowed[normNumber(n)] = true
					}
				}
			}
			text := idPattern.ReplaceAllString(cl.Text, "")
			bad := []string{}
			seen := map[string]bool{}
			for _, n := range numberPattern.FindAllString(text, -1) {
				norm := normNumber(n)
				if seen[norm] || allowed[norm] || smallNumber(norm) || isYear(norm) {
					continue
				}
				seen[norm] = true
				bad = append(bad, n)
			}
			if len(bad) > 0 {
				issues = append(issues, fmt.Sprintf("「%s」第 %d 句里的数字 %s 在统计结果和所引资料里都找不到，不得编造数据；请改用统计里的数字，或引用写有该数字的资料",
					headingOf(in.Config, s.ID), i+1, strings.Join(bad, "、")))
			}
		}
	}
	return result("numbers_traceable", "数据溯源", issues, in.fix())
}

// smallNumber 不超过 10 的整数（“3 条路线”“前 5 名”这类序数和个数）不查。
func smallNumber(norm string) bool {
	f, err := strconv.ParseFloat(norm, 64)
	return err == nil && f == float64(int(f)) && f >= 0 && f <= 10
}

func isYear(norm string) bool {
	f, err := strconv.ParseFloat(norm, 64)
	return err == nil && f == float64(int(f)) && f >= 1990 && f <= 2100
}

func checkForbidden(in QcInput) contract.QcCheckResult {
	issues := []string{}
	terms := in.Config.ForbiddenTerms
	check := func(where, text string) {
		for _, term := range terms {
			if term != "" && strings.Contains(text, term) {
				issues = append(issues, fmt.Sprintf("%s出现禁用词“%s”", where, term))
			}
		}
	}
	check("标题", in.Draft.Title)
	for _, s := range in.Draft.Sections {
		for i, cl := range s.Claims {
			check(fmt.Sprintf("「%s」第 %d 句", headingOf(in.Config, s.ID), i+1), cl.Text)
		}
	}
	return result("forbidden_terms", "禁用词", issues, in.fix())
}

func checkLength(in QcInput) contract.QcCheckResult {
	issues := []string{}
	if in.Rendered != nil {
		limit := in.Config.MaxPages * LengthMargin
		if in.Rendered.EstimatedPages > limit {
			issues = append(issues, fmt.Sprintf("按版式估算约 %.1f 页，超过上限 %.1f 页（%.0f 页留 10%% 余量），请压缩叙述", in.Rendered.EstimatedPages, limit, in.Config.MaxPages))
		}
	}
	return result("length", "篇幅", issues, in.fix())
}

func sectionText(s *BriefSection) string {
	if s == nil {
		return ""
	}
	parts := []string{}
	for _, cl := range s.Claims {
		parts = append(parts, cl.Text+"|"+strings.Join(nonEmpty(cl.Cites), ","))
	}
	return strings.Join(parts, "\n")
}

func checkFeedback(in QcInput) contract.QcCheckResult {
	issues := []string{}
	if len(in.Draft.RevisionNotes) == 0 {
		issues = append(issues, "重做时要输出修改说明（revisionNotes），逐条对应退回意见")
	}
	changedAny := false
	for _, s := range in.Draft.Sections {
		var prev *BriefSection
		if in.PreviousDraft != nil {
			prev = sectionByID(*in.PreviousDraft, s.ID)
		}
		if sectionText(&s) != sectionText(prev) {
			changedAny = true
		}
	}
	if in.PreviousDraft != nil && !changedAny {
		issues = append(issues, "和上一版相比没有任何章节发生变化，退回意见没有被采纳")
	}
	for i, note := range in.Draft.RevisionNotes {
		for _, ref := range note.Sections {
			id := ref
			for _, sec := range in.Config.Sections {
				if sec.Heading == ref {
					id = sec.ID
				}
			}
			cur := sectionByID(in.Draft, id)
			if cur == nil {
				issues = append(issues, fmt.Sprintf("修改说明第 %d 条指向的章节「%s」在简报里不存在", i+1, ref))
				continue
			}
			var prev *BriefSection
			if in.PreviousDraft != nil {
				prev = sectionByID(*in.PreviousDraft, id)
			}
			if in.PreviousDraft != nil && sectionText(cur) == sectionText(prev) {
				issues = append(issues, fmt.Sprintf("修改说明第 %d 条称改了「%s」，但这个章节和上一版没有变化", i+1, ref))
			}
		}
	}
	return result("feedback_response", "意见响应", issues, in.fix())
}

func nonEmpty(items []string) []string {
	out := []string{}
	for _, item := range items {
		if t := strings.TrimSpace(item); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func indexOf(items []string, want string) int {
	for i, item := range items {
		if item == want {
			return i
		}
	}
	return -1
}
