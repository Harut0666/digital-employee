// 自定义规则的 6 种模板（contract/qc.ts 的 QC_TEMPLATES）。params 形状：
//
//	forbidden_terms      { terms: string[] }                      全文不得出现
//	required_terms       { terms: string[] }                      全文必须出现
//	section_must_contain { section: string, terms: string[] }    指定章节（id）必须出现
//	max_items            { section: string, max: number }         指定章节的条数上限（有要点按要点，否则按段落）
//	max_pages            { max: number }                          渲染后估算页数上限（不留余量）
//	number_range         { path: string, min?: number, max?: number }
//	                     槽位路径指向的事实值须在区间内，按展示口径比较：金额为元、比例为百分数、计数为整数
//
// 参数缺失或类型不对时返回“参数无效”，不带 fixStep（无法自动修正，转人工）。
// 移植自 packages/core/src/qc/custom.ts。
package qc

import (
	"fmt"
	"strings"

	"digital-employee/internal/contract"
)

func customFailWith(rule contract.CustomRule, issues []string, fixStep string) contract.QcCheckResult {
	return contract.QcCheckResult{ID: rule.ID, Label: rule.Label, Passed: false, Issues: issues, FixStep: fixStep}
}

// customFailManual 无法自动修正（参数有误、事实缺失等），不给 fixStep，由引擎转人工。
func customFailManual(rule contract.CustomRule, issues []string) contract.QcCheckResult {
	return contract.QcCheckResult{ID: rule.ID, Label: rule.Label, Passed: false, Issues: issues}
}

func customPass(rule contract.CustomRule) contract.QcCheckResult {
	return contract.QcCheckResult{ID: rule.ID, Label: rule.Label, Passed: true, Issues: []string{}}
}

func customInvalid(rule contract.CustomRule, what string) contract.QcCheckResult {
	return customFailManual(rule, []string{fmt.Sprintf("自定义规则“%s”参数无效：%s", rule.Label, what)})
}

// paramStrings 取一个非空字符串数组参数。
func paramStrings(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		if len(typed) == 0 {
			return nil, false
		}
		return typed, true
	case []any:
		if len(typed) == 0 {
			return nil, false
		}
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, text)
		}
		return out, true
	}
	return nil, false
}

// paramNumber 取一个数字参数（JSON 解析为 float64；测试里可能直接构造 int）。
func paramNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	}
	return 0, false
}

func sectionText(draft contract.DraftReport, id string) (heading string, text string, ok bool) {
	for _, section := range draft.Sections {
		if section.ID != id {
			continue
		}
		parts := make([]string, 0, 2+len(section.Paragraphs)+len(section.Bullets))
		parts = append(parts, section.Heading)
		parts = append(parts, section.Paragraphs...)
		parts = append(parts, section.Bullets...)
		return section.Heading, strings.Join(parts, "\n"), true
	}
	return "", "", false
}

func fullText(draft contract.DraftReport) string {
	pieces := NarrativePieces(draft)
	parts := make([]string, 0, len(pieces))
	for _, piece := range pieces {
		parts = append(parts, piece.Text)
	}
	return strings.Join(parts, "\n")
}

// RunCustomRule 执行一条自定义规则；模板不认识时返回 nil（与 TS 的 undefined 一致）。
func RunCustomRule(rule contract.CustomRule, input contract.QcInput) *contract.QcCheckResult {
	fail := func(issues []string) contract.QcCheckResult { return customFailWith(rule, issues, fixWrite(input)) }
	params := rule.Params
	if params == nil {
		params = map[string]any{}
	}

	switch rule.Template {
	case contract.TemplateForbiddenTerms:
		terms, ok := paramStrings(params["terms"])
		if !ok {
			result := customInvalid(rule, "terms 应为非空字符串数组")
			return &result
		}
		text := fullText(input.Draft)
		issues := []string{}
		for _, term := range terms {
			if strings.Contains(text, term) {
				issues = append(issues, fmt.Sprintf("出现禁止的表述“%s”", term))
			}
		}
		if len(issues) == 0 {
			result := customPass(rule)
			return &result
		}
		result := fail(issues)
		return &result

	case contract.TemplateRequiredTerms:
		terms, ok := paramStrings(params["terms"])
		if !ok {
			result := customInvalid(rule, "terms 应为非空字符串数组")
			return &result
		}
		text := fullText(input.Draft)
		issues := []string{}
		for _, term := range terms {
			if !strings.Contains(text, term) {
				issues = append(issues, fmt.Sprintf("缺少必须出现的表述“%s”", term))
			}
		}
		if len(issues) == 0 {
			result := customPass(rule)
			return &result
		}
		result := fail(issues)
		return &result

	case contract.TemplateSectionMustContain:
		terms, termsOK := paramStrings(params["terms"])
		sectionID, sectionOK := params["section"].(string)
		if !termsOK || !sectionOK {
			result := customInvalid(rule, "需要 section（字符串）和 terms（非空字符串数组）")
			return &result
		}
		heading, text, found := sectionText(input.Draft, sectionID)
		if !found {
			result := fail([]string{"缺少章节 " + sectionID})
			return &result
		}
		issues := []string{}
		for _, term := range terms {
			if !strings.Contains(text, term) {
				issues = append(issues, fmt.Sprintf("「%s」缺少“%s”", heading, term))
			}
		}
		if len(issues) == 0 {
			result := customPass(rule)
			return &result
		}
		result := fail(issues)
		return &result

	case contract.TemplateMaxItems:
		sectionID, sectionOK := params["section"].(string)
		max, maxOK := paramNumber(params["max"])
		if !sectionOK || !maxOK {
			result := customInvalid(rule, "需要 section（字符串）和 max（数字）")
			return &result
		}
		var section *contract.RenderedSection
		for index := range input.Draft.Sections {
			if input.Draft.Sections[index].ID == sectionID {
				section = &input.Draft.Sections[index]
				break
			}
		}
		if section == nil {
			result := fail([]string{"缺少章节 " + sectionID})
			return &result
		}
		count := len(section.Paragraphs)
		if len(section.Bullets) > 0 {
			count = len(section.Bullets)
		}
		if float64(count) <= max {
			result := customPass(rule)
			return &result
		}
		result := fail([]string{fmt.Sprintf("「%s」有 %d 条，不得超过 %s 条", section.Heading, count, formatNumber(max))})
		return &result

	case contract.TemplateMaxPages:
		max, ok := paramNumber(params["max"])
		if !ok {
			result := customInvalid(rule, "需要 max（数字）")
			return &result
		}
		if input.Rendered == nil {
			result := customPass(rule)
			return &result
		}
		if input.Rendered.EstimatedPages <= max {
			result := customPass(rule)
			return &result
		}
		result := fail([]string{fmt.Sprintf("估算 %s 页，超过 %s 页，请压缩篇幅",
			formatNumber(input.Rendered.EstimatedPages), formatNumber(max))})
		return &result

	case contract.TemplateNumberRange:
		path, pathOK := params["path"].(string)
		minValue, minPresent := params["min"]
		maxValue, maxPresent := params["max"]
		min, minOK := paramNumber(minValue)
		max, maxOK := paramNumber(maxValue)
		if !pathOK ||
			(minPresent && !minOK) || (maxPresent && !maxOK) ||
			(!minPresent && !maxPresent) {
			result := customInvalid(rule, "需要 path（字符串）以及 min / max 中至少一个数字")
			return &result
		}
		slot, found := ResolveSlot(path, input.Facts)
		if !found {
			result := customFailManual(rule, []string{"无法在事实中找到 " + path})
			return &result
		}
		shown := DisplayValue(slot)
		if shown == nil {
			result := customFailManual(rule, []string{path + " 暂无数据，无法判断是否在区间内"})
			return &result
		}
		low := minPresent && *shown < min
		high := maxPresent && *shown > max
		if !low && !high {
			result := customPass(rule)
			return &result
		}
		lowText, highText := "-∞", "+∞"
		if minPresent {
			lowText = formatNumber(min)
		}
		if maxPresent {
			highText = formatNumber(max)
		}
		result := customFailManual(rule, []string{fmt.Sprintf("%s 为 %s，不在 [%s, %s] 内",
			path, formatNumber(*shown), lowText, highText)})
		return &result
	}
	return nil
}
