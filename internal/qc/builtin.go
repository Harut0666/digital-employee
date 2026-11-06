// 8 类内置质检（docs/design.md §10.5）。每项返回一个 QcCheckResult；
// 不通过时给出 issues 和应重跑的 fixStep。移植自 packages/core/src/qc/builtin.ts。
package qc

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"digital-employee/internal/contract"
)

// 场景 A 的默认步骤 id；实际取值由引擎按 workflow.qcFixSteps 通过 QcInput.FixSteps 传入。
const (
	FixWriteReport = "write_report"
	FixMatchCauses = "match_causes"
)

// LengthMargin 篇幅留约 10% 余量（design.md §10.5）。
const LengthMargin = 0.9

func fixWrite(input contract.QcInput) string {
	if input.FixSteps != nil && input.FixSteps.Write != "" {
		return input.FixSteps.Write
	}
	return FixWriteReport
}

func fixCauses(input contract.QcInput) string {
	if input.FixSteps != nil && input.FixSteps.Causes != "" {
		return input.FixSteps.Causes
	}
	return FixMatchCauses
}

func qcResult(id, label string, issues []string, fixStep string) contract.QcCheckResult {
	if len(issues) == 0 {
		return contract.QcCheckResult{ID: id, Label: label, Passed: true, Issues: []string{}}
	}
	return contract.QcCheckResult{ID: id, Label: label, Passed: false, Issues: issues, FixStep: fixStep}
}

// CheckStructure 结构完整：章节齐全且顺序正确，摘要、下周关注事项有数量上限，图表有下限。
func CheckStructure(input contract.QcInput) contract.QcCheckResult {
	cfg := input.Config.Builtin.Structure
	issues := []string{}
	draftIDs := make([]string, 0, len(input.Draft.Sections))
	for _, section := range input.Draft.Sections {
		draftIDs = append(draftIDs, section.ID)
	}

	missing := []string{}
	for _, section := range cfg.Sections {
		if !containsString(draftIDs, section.ID) {
			missing = append(missing, section.Heading)
		}
	}
	if len(missing) > 0 {
		issues = append(issues, "缺少章节："+strings.Join(missing, "、"))
	}

	present := []int{}
	for _, section := range cfg.Sections {
		if index := indexOf(draftIDs, section.ID); index >= 0 {
			present = append(present, index)
		}
	}
	for index := 1; index < len(present); index++ {
		if present[index] < present[index-1] {
			headings := make([]string, 0, len(cfg.Sections))
			for _, section := range cfg.Sections {
				headings = append(headings, section.Heading)
			}
			issues = append(issues, "章节顺序不正确，应为："+strings.Join(headings, " → "))
			break
		}
	}

	// 约定：配置中的第一个章节是摘要，最后一个是下周关注。
	var summaryHeading, nextHeading string
	if len(cfg.Sections) > 0 {
		summaryHeading = cfg.Sections[0].ID
		nextHeading = cfg.Sections[len(cfg.Sections)-1].ID
	}
	for _, section := range input.Draft.Sections {
		if section.ID != summaryHeading {
			continue
		}
		paragraphs := append(append([]string{}, section.Paragraphs...), section.Bullets...)
		count := CountSentences(paragraphs)
		if count > cfg.SummaryMaxSentences {
			issues = append(issues, fmt.Sprintf("摘要有 %d 句，不得超过 %d 句", count, cfg.SummaryMaxSentences))
		}
	}
	for _, section := range input.Draft.Sections {
		if section.ID != nextHeading {
			continue
		}
		items := len(section.Paragraphs)
		if len(section.Bullets) > 0 {
			items = len(section.Bullets)
		}
		if items > cfg.NextWeekMaxItems {
			issues = append(issues, fmt.Sprintf("下周关注有 %d 条，不得超过 %d 条", items, cfg.NextWeekMaxItems))
		}
	}

	// 图表数来自渲染结果；尚未渲染时无从判断，不算不通过。
	if input.Rendered != nil && input.Rendered.ChartCount < cfg.MinCharts {
		issues = append(issues, fmt.Sprintf("图表只有 %d 张，至少需要 %d 张", input.Rendered.ChartCount, cfg.MinCharts))
	}
	return qcResult("structure", "结构完整", issues, fixWrite(input))
}

// CheckTraceability 数据溯源：叙述里没有裸数字，槽位都能对上事实。
func CheckTraceability(input contract.QcInput) contract.QcCheckResult {
	issues := []string{}
	unresolved := map[string]bool{}
	unresolvedOrder := []string{}

	quotes := []string{}
	for _, cause := range input.Draft.Causes {
		if trimmed := strings.TrimSpace(cause.Quote); trimmed != "" {
			quotes = append(quotes, trimmed)
		}
	}
	names := EntityNamesWithDigits(input.Facts)
	slots := NewSlots(input.Facts)
	for _, piece := range NarrativePieces(input.Draft) {
		rendered := slots.Render(piece.Text)
		for _, path := range rendered.Unresolved {
			if !unresolved[path] {
				unresolved[path] = true
				unresolvedOrder = append(unresolvedOrder, path)
			}
		}
		for _, number := range FindBareNumbers(piece.Text, quotes, names) {
			issues = append(issues, fmt.Sprintf("「%s」出现未经槽位引用的数字：%s，请改用 {{路径}} 槽位", piece.Where, number))
		}
	}
	for _, path := range unresolvedOrder {
		issues = append(issues, "槽位无法对应到事实：{{"+path+"}}")
	}

	regionSum := int64(0)
	for _, metric := range input.Facts.Regions {
		regionSum += metric.RevenueCents
	}
	if regionSum != input.Facts.Company.RevenueCents {
		issues = append(issues, fmt.Sprintf("区域合计（%d 分）不等于全公司实收（%d 分）", regionSum, input.Facts.Company.RevenueCents))
	}
	return qcResult("traceability", "数据溯源", issues, fixWrite(input))
}

// CheckForbiddenTerms 禁用指标。
func CheckForbiddenTerms(input contract.QcInput) contract.QcCheckResult {
	issues := []string{}
	for _, piece := range NarrativePieces(input.Draft) {
		for _, term := range input.Config.Builtin.ForbiddenTerms.Terms {
			if strings.Contains(piece.Text, term) {
				issues = append(issues, fmt.Sprintf("「%s」出现禁用指标“%s”", piece.Where, term))
			}
		}
	}
	return qcResult("forbidden_terms", "禁用指标", issues, fixWrite(input))
}

// CheckAnomalyCoverage 异常覆盖：环比绝对值超过配置阈值的每条异常，都要在 causes 中有对应条目。
func CheckAnomalyCoverage(input contract.QcInput) contract.QcCheckResult {
	threshold := input.Config.Builtin.AnomalyCoverage.Threshold
	covered := map[string]bool{}
	for _, cause := range input.Draft.Causes {
		covered[cause.AnomalyID] = true
	}
	issues := []string{}
	for _, anomaly := range input.Facts.Anomalies {
		if math.Abs(anomaly.Wow) > threshold && !covered[anomaly.ID] {
			issues = append(issues, fmt.Sprintf("异常 %s（%s）没有出现在“异常与原因”中", anomaly.ID, anomaly.Name))
		}
	}
	return qcResult("anomaly_coverage", "异常覆盖", issues, fixCauses(input))
}

// CheckQuoteIntegrity 引用真实：原文摘录必须是所注周次纪要中某一段的连续子串。
func CheckQuoteIntegrity(input contract.QcInput) contract.QcCheckResult {
	issues := []string{}
	for _, cause := range input.Draft.Causes {
		if cause.Level == "unknown" {
			continue
		}
		quote := strings.TrimSpace(cause.Quote)
		if quote == "" || cause.SourceWeek == nil {
			issues = append(issues, fmt.Sprintf("%s 的原因层级为“%s”，必须同时给出来源周次和纪要原文摘录", cause.AnomalyID, cause.Level))
			continue
		}
		year := cause.SourceWeek.Year
		week := cause.SourceWeek.Week
		sources := []contract.ParsedMinutes{}
		for _, minutes := range input.Minutes.Minutes {
			if minutes.Week != nil && minutes.Week.Year == year && minutes.Week.Week == week {
				sources = append(sources, minutes)
			}
		}
		if len(sources) == 0 {
			issues = append(issues, fmt.Sprintf("%s 引用了 %d 年第 %d 周的纪要，但没有这一周的纪要", cause.AnomalyID, year, week))
			continue
		}
		found := false
		for _, minutes := range sources {
			for _, paragraph := range minutes.Paragraphs {
				if strings.Contains(paragraph.Text, quote) {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			issues = append(issues, fmt.Sprintf("%s 的摘录“%s”不是 %d 年第 %d 周纪要原文的连续片段", cause.AnomalyID, quote, year, week))
		}
	}
	return qcResult("quote_integrity", "引用真实", issues, fixCauses(input))
}

// anomaliesSection “异常与原因”章节：先找 id=anomalies，再退化为标题含“异常”的章节。
func anomaliesSection(draft contract.DraftReport) *contract.RenderedSection {
	for index := range draft.Sections {
		if draft.Sections[index].ID == "anomalies" {
			return &draft.Sections[index]
		}
	}
	for index := range draft.Sections {
		if strings.Contains(draft.Sections[index].Heading, "异常") {
			return &draft.Sections[index]
		}
	}
	return nil
}

// CheckContradictionCoverage 纪要矛盾覆盖：程序判出的每一条矛盾，草稿都要用 contradictionRefs 引用，
// 且“异常与原因”章节里出现矛盾对象名。
func CheckContradictionCoverage(input contract.QcInput) contract.QcCheckResult {
	issues := []string{}
	refs := map[string]bool{}
	for _, ref := range input.Draft.ContradictionRefs {
		refs[ref] = true
	}
	section := anomaliesSection(input.Draft)
	var sectionText *string
	if section != nil {
		parts := make([]string, 0, 2+len(section.Paragraphs)+len(section.Bullets))
		parts = append(parts, section.Heading)
		parts = append(parts, section.Paragraphs...)
		parts = append(parts, section.Bullets...)
		joined := strings.Join(parts, "\n")
		sectionText = &joined
	}
	for _, contradiction := range input.Contradictions {
		if !refs[contradiction.ID] {
			issues = append(issues, fmt.Sprintf("纪要矛盾 %s（%s）没有写进“异常与原因”章节", contradiction.ID, contradiction.Subject.Name))
		}
		// 缺章节由“结构完整”负责；这里只在章节存在时核对对象名，避免同一个问题报两遍。
		if sectionText != nil && !strings.Contains(*sectionText, contradiction.Subject.Name) {
			issues = append(issues, fmt.Sprintf("“异常与原因”章节没有提到矛盾对象“%s”", contradiction.Subject.Name))
		}
	}
	return qcResult("contradiction_coverage", "纪要矛盾覆盖", issues, fixWrite(input))
}

// CheckFeedbackResponse 意见响应（仅重做时）：修改说明覆盖退回意见，且被要求修改的章节内容确实变了。
func CheckFeedbackResponse(input contract.QcInput) *contract.QcCheckResult {
	if input.Feedback == nil {
		return nil
	}
	issues := []string{}
	notes := input.Draft.RevisionNotes
	comment := strings.TrimSpace(input.Feedback.Comment)
	matching := []contract.RevisionNote{}
	for _, note := range notes {
		noteComment := strings.TrimSpace(note.Comment)
		if noteComment == comment || strings.Contains(noteComment, comment) {
			matching = append(matching, note)
		}
	}
	if len(matching) == 0 {
		issues = append(issues, fmt.Sprintf("修改说明（revisionNotes）没有覆盖退回意见“%s”", comment))
	} else {
		allEmpty := true
		for _, note := range matching {
			if strings.TrimSpace(note.Change) != "" {
				allEmpty = false
				break
			}
		}
		if allEmpty {
			issues = append(issues, "修改说明没有写明具体改了什么")
		}
	}

	// 返回 nil 表示没有上一版可供比较（与 TS 的 undefined 一致）
	changed := func(sectionID string) *bool {
		if input.PreviousDraft == nil {
			return nil
		}
		before := findSection(input.PreviousDraft.Sections, sectionID)
		after := findSection(input.Draft.Sections, sectionID)
		beforeJSON, _ := json.Marshal(before)
		afterJSON, _ := json.Marshal(after)
		value := string(beforeJSON) != string(afterJSON)
		return &value
	}

	required := []string{}
	if input.Feedback.Sections != nil {
		required = input.Feedback.Sections
	}
	mentioned := map[string]bool{}
	for _, note := range matching {
		for _, id := range note.Sections {
			mentioned[id] = true
		}
	}
	for _, id := range required {
		if len(matching) > 0 && !mentioned[id] {
			issues = append(issues, "修改说明没有提到被要求修改的章节 "+id)
		}
		if flag := changed(id); flag != nil && !*flag {
			issues = append(issues, "章节 "+id+" 的内容与上一版相同，没有按意见修改")
		}
	}
	if len(required) == 0 && input.PreviousDraft != nil {
		anyChanged := false
		for _, section := range input.Draft.Sections {
			if flag := changed(section.ID); flag != nil && *flag {
				anyChanged = true
				break
			}
		}
		if !anyChanged {
			issues = append(issues, "所有章节都与上一版相同，没有按意见修改")
		}
	}
	result := qcResult("feedback_response", "意见响应", issues, fixWrite(input))
	return &result
}

// CheckLength 篇幅。
func CheckLength(input contract.QcInput) contract.QcCheckResult {
	issues := []string{}
	maxPages := input.Config.Builtin.Length.MaxPages
	limit := maxPages * LengthMargin
	if input.Rendered != nil && input.Rendered.EstimatedPages > limit {
		issues = append(issues, fmt.Sprintf("估算 %v 页，超过上限 %v 页（留 10%% 余量即 %v 页），请压缩篇幅",
			formatNumber(input.Rendered.EstimatedPages), formatNumber(maxPages), formatNumber(limit)))
	}
	return qcResult("length", "篇幅", issues, fixWrite(input))
}

func findSection(sections []contract.RenderedSection, id string) *contract.RenderedSection {
	for index := range sections {
		if sections[index].ID == id {
			return &sections[index]
		}
	}
	return nil
}

func containsString(list []string, want string) bool { return indexOf(list, want) >= 0 }

func indexOf(list []string, want string) int {
	for index, item := range list {
		if item == want {
			return index
		}
	}
	return -1
}

// formatNumber 用 JS 的数值写法显示（整数不带小数点）。
func formatNumber(value float64) string {
	if value == math.Trunc(value) && math.Abs(value) < 1e15 {
		return fmt.Sprintf("%d", int64(value))
	}
	return fmt.Sprintf("%g", value)
}
