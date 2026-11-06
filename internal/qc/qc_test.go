package qc

import (
	"strings"
	"testing"

	"digital-employee/internal/contract"
)

// ---------------------------------------------------------------- 夹具（移植自 packages/core/test/qc/fixtures.ts）

func f64(value float64) *float64 { return &value }
func i64(value int64) *int64     { return &value }

func metric(revenueCents int64, options func(*contract.Metric)) contract.Metric {
	out := contract.Metric{RevenueCents: revenueCents}
	if options != nil {
		options(&out)
	}
	return out
}

func store(code, region string, revenueCents int64, options func(*contract.Metric)) contract.StoreMetric {
	return contract.StoreMetric{
		Metric: metric(revenueCents, options),
		Code:   code, Name: code + "店", Region: region, Type: contract.StoreMall,
	}
}

func makeFacts() contract.WeeklyFacts {
	return contract.WeeklyFacts{
		Period:       contract.PeriodRange{IsoWeek: contract.IsoWeek{Year: 2026, Week: 44}, From: "2026-10-26", To: "2026-11-01"},
		DataCoverage: contract.DateRange{From: "2026-09-01", To: "2026-11-01"},
		Company: metric(57_363_500, func(m *contract.Metric) {
			m.PrevRevenueCents = i64(55_000_000)
			m.Wow = f64(0.043)
			m.GrossMarginRate = f64(0.352)
			m.Achievement = f64(0.953)
		}),
		Regions: map[string]contract.Metric{
			"华东": metric(30_000_000, func(m *contract.Metric) {
				m.PrevRevenueCents = i64(34_090_909)
				m.Wow = f64(-0.12)
				m.Achievement = f64(0.9)
			}),
			"华北": metric(27_363_500, func(m *contract.Metric) {
				m.PrevRevenueCents = i64(20_909_091)
				m.Wow = f64(0.3086)
				m.Achievement = f64(1.02)
			}),
		},
		Stores: []contract.StoreMetric{
			store("HZ01", "华东", 5_000_000, func(m *contract.Metric) {
				m.PrevRevenueCents = i64(6_000_000)
				m.Wow = f64(-0.1667)
			}),
			store("BJ02", "华北", 7_000_000, func(m *contract.Metric) {
				m.PrevRevenueCents = i64(6_800_000)
				m.Wow = f64(0.0294)
			}),
		},
		Categories: []contract.CategoryMetric{
			{Category: "饮品", RevenueCents: 10_000_000, PrevRevenueCents: i64(9_000_000), Wow: f64(0.1111)},
		},
		RegionCategories: map[string]map[string]contract.RegionCategoryMetric{
			"华南": {
				"饮品": {
					RevenueCents: 7_468_274, PrevRevenueCents: i64(5_690_794),
					GrossMarginRate: f64(0.60547), PrevGrossMarginRate: f64(0.70361),
				},
			},
		},
		TopProducts: []contract.TopProduct{{Code: "P001", Name: "拿铁", RevenueCents: 1_200_000}},
		Anomalies: []contract.Anomaly{
			{
				ID: "region:华东", Level: "region", Key: "华东", Name: "华东", Region: "华东",
				Wow: -0.12, RevenueCents: 30_000_000, PrevRevenueCents: 34_090_909,
			},
			{
				ID: "store:HZ01", Level: "store", Key: "HZ01", Name: "HZ01店", Region: "华东",
				StoreType: contract.StoreMall, Wow: -0.1667, RevenueCents: 5_000_000, PrevRevenueCents: 6_000_000,
			},
		},
		Trend: []contract.TrendPoint{},
		Quality: func() contract.DataQualityReport {
			out := contract.DataQualityReport{}
			out.Duplicates.Count = 2
			out.Duplicates.RevenueCents = 12_000
			out.Duplicates.Groups = []contract.DuplicateGroup{}
			out.IDConflicts.RecordIDs = []string{}
			out.UnknownCodes.Count = 1
			out.UnknownCodes.RevenueCents = 5_000
			out.Returns.Count = 12
			out.Returns.RevenueCents = -80_000
			out.Returns.Groups = []contract.ReturnGroup{{
				Store: "BJ01", StoreName: "北京朝阳店", Date: "2026-10-15",
				Product: "C01", ProductName: "草莓奶油切块", Count: 15, RevenueCents: -33_000,
			}}
			out.StoreDays = []contract.StoreDay{}
			out.PrevWeekStoreDays = []contract.StoreDay{}
			return out
		}(),
	}
}

func makeQCConfig() contract.QCConfig {
	return contract.QCConfig{
		Builtin: contract.BuiltinQC{
			Structure: contract.StructureQC{
				Enabled: true,
				Sections: []contract.SectionSpec{
					{ID: "summary", Heading: "本周摘要"},
					{ID: "metrics", Heading: "核心指标"},
					{ID: "categories", Heading: "品类表现"},
					{ID: "anomalies", Heading: "异常与原因"},
					{ID: "quality", Heading: "数据质量说明"},
					{ID: "next_week", Heading: "下周关注事项"},
				},
				SummaryMaxSentences: 5, NextWeekMaxItems: 5, MinCharts: 2,
			},
			Traceability:          contract.EnabledQC{Enabled: true},
			AnomalyCoverage:       contract.AnomalyCoverageQC{Enabled: true, Threshold: 0.1},
			QuoteIntegrity:        contract.EnabledQC{Enabled: true},
			ContradictionCoverage: contract.EnabledQC{Enabled: true},
			ForbiddenTerms:        contract.ForbiddenTermsQC{Enabled: true, Terms: []string{"客单价", "同比"}},
			Length:                contract.LengthQC{Enabled: true, MaxPages: 3},
			FeedbackResponse:      contract.EnabledQC{Enabled: true},
		},
		Contradiction: contract.ContradictionThresholds{FlatTolerance: 0.05, DirectionMin: 0.02, MagnitudeTolerance: 0.1},
		Custom:        []contract.CustomRule{},
	}
}

func makeDraft() contract.DraftReport {
	return contract.DraftReport{
		Title: "第44周经营周报",
		Sections: []contract.RenderedSection{
			{ID: "summary", Heading: "本周摘要", Paragraphs: []string{"本周实收 {{company.revenue}}，环比 {{company.wow}}。"}},
			{ID: "metrics", Heading: "核心指标", Paragraphs: []string{"华东实收 {{regions.华东.revenue}}。"}},
			{ID: "categories", Heading: "品类表现", Paragraphs: []string{"饮品环比 {{categories.饮品.wow}}。"}},
			{ID: "anomalies", Heading: "异常与原因", Paragraphs: []string{"华东环比 {{regions.华东.wow}}，门店 HZ01 环比 {{stores.HZ01.wow}}。"}},
			{ID: "quality", Heading: "数据质量说明", Paragraphs: []string{"退货 {{quality.returns.count}} 笔，已照常冲减。"}},
			{ID: "next_week", Heading: "下周关注事项", Bullets: []string{"跟进华东恢复情况", "关注 HZ01 门店客流"}},
		},
		Causes: []contract.DraftCause{
			{
				AnomalyID: "region:华东", Cause: "华东整体受天气影响", Level: "region",
				SourceWeek: &contract.IsoWeek{Year: 2026, Week: 44}, Quote: "华东区受连续降雨影响",
			},
			{AnomalyID: "store:HZ01", Cause: "原因待核实", Level: "unknown"},
		},
	}
}

func makeQcInput(options func(*contract.QcInput)) contract.QcInput {
	input := contract.QcInput{
		Config: makeQCConfig(),
		Draft:  makeDraft(),
		Facts:  makeFacts(),
		Minutes: contract.ParseMinutesOutput{Minutes: []contract.ParsedMinutes{{
			Path: "w44.docx", Week: &contract.IsoWeek{Year: 2026, Week: 44}, Title: "2026年第44周周会纪要",
			Paragraphs: []contract.MinutesParagraph{
				{ID: "W44-p01", Text: "本周总体平稳。"},
				{ID: "W44-p02", Text: "华东区受连续降雨影响，到店客流下降。"},
			},
		}}},
		Rendered: &contract.RenderedStats{EstimatedPages: 2, ChartCount: 2},
	}
	if options != nil {
		options(&input)
	}
	return input
}

func findCheck(results []contract.QcCheckResult, id string) *contract.QcCheckResult {
	for index := range results {
		if results[index].ID == id {
			return &results[index]
		}
	}
	return nil
}

// ---------------------------------------------------------------- 槽位渲染（移植自 test/qc/slots.test.ts）

func TestRenderSlots(t *testing.T) {
	facts := makeFacts()
	cases := []struct {
		text string
		want string
	}{
		{"{{regions.华东.wow}}", "-12.0%"},
		{"环比 {{regions.华北.wow}}", "环比 +30.9%"},
		{"{{company.revenue}}", "573,635 元"},
		{"{{stores.HZ01.wow}} / {{categories.饮品.wow}}", "-16.7% / +11.1%"},
		{"{{quality.returns.count}} 笔", "12 笔"},
		{"华南饮品毛利率由 {{regionCategories.华南.饮品.prevGrossMarginRate}} 降至 {{regionCategories.华南.饮品.grossMarginRate}}",
			"华南饮品毛利率由 70.4% 降至 60.5%"},
		{"{{quality.returns.groups.BJ01.count}} 份 / {{quality.returns.groups.BJ01.revenue}}", "15 份 / -330 元"},
		{"{{quality.returns.groups.C01.count}} 份", "15 份"},
		{"{{company.achievement}}", "95.3%"},
		{"{{ company.revenue }}", "573,635 元"},
	}
	for _, testCase := range cases {
		got := RenderSlots(testCase.text, facts)
		if got.Text != testCase.want {
			t.Errorf("RenderSlots(%q) = %q，want %q", testCase.text, got.Text, testCase.want)
		}
		if len(got.Unresolved) != 0 {
			t.Errorf("RenderSlots(%q) unresolved = %v", testCase.text, got.Unresolved)
		}
	}

	// 未知路径进入 unresolved，原文保留；重复槽位只报一次
	out := RenderSlots("A {{regions.西南.wow}} B {{company.nope}} C {{regions.西南.wow}}", facts)
	if out.Text != "A {{regions.西南.wow}} B {{company.nope}} C {{regions.西南.wow}}" {
		t.Errorf("未知槽位不应被替换：%q", out.Text)
	}
	if strings.Join(out.Unresolved, ",") != "regions.西南.wow,company.nope" {
		t.Errorf("unresolved = %v", out.Unresolved)
	}

	// 没有渲染格式的字段视为无法解析
	if out := RenderSlots("{{period.week}}", facts); len(out.Unresolved) != 1 {
		t.Errorf("没有渲染格式的字段应 unresolved，实际 %v", out.Unresolved)
	}

	// 环比为 null 时写明不可比，其余 null 写暂无
	nullFacts := makeFacts()
	nullFacts.Company.Wow = nil
	nullFacts.Company.PrevRevenueCents = nil
	nullFacts.Company.GrossMarginRate = nil
	if got := RenderSlots("{{company.wow}}", nullFacts).Text; got != "不可比（无上周数据）" {
		t.Errorf("null wow = %q", got)
	}
	if got := RenderSlots("{{company.grossMarginRate}}", nullFacts).Text; got != "暂无" {
		t.Errorf("null grossMarginRate = %q", got)
	}

	// 四舍五入：0.1235 → 123.5 → +12.4%
	rounded := makeFacts()
	rounded.Company.Wow = f64(0.1235)
	if got := RenderSlots("{{company.wow}}", rounded).Text; got != "+12.4%" {
		t.Errorf("0.1235 应渲染为 +12.4%%，实际 %q", got)
	}
}

// ---------------------------------------------------------------- 8 类内置质检 happy path

func TestBuiltinChecksHappyPath(t *testing.T) {
	engine := NewEngine()
	results := engine.Run(makeQcInput(nil))
	// 没有退回意见：feedback_response 不产出结果，所以是 7 项
	if len(results) != 7 {
		t.Fatalf("质检项数 = %d，want 7：%+v", len(results), results)
	}
	for _, result := range results {
		if !result.Passed {
			t.Errorf("质检项 %s 应通过，实际未通过：%v", result.ID, result.Issues)
		}
		if result.Issues == nil {
			t.Errorf("质检项 %s 的 issues 应为空数组而不是 null", result.ID)
		}
	}
	order := []string{"structure", "traceability", "anomaly_coverage", "quote_integrity", "contradiction_coverage", "forbidden_terms", "length"}
	for index, id := range order {
		if results[index].ID != id {
			t.Errorf("第 %d 项 = %s，want %s", index, results[index].ID, id)
		}
	}
}

func TestStructureCheck(t *testing.T) {
	// 缺章节
	input := makeQcInput(func(in *contract.QcInput) {
		in.Draft.Sections = in.Draft.Sections[1:]
	})
	got := CheckStructure(input)
	if got.Passed || !strings.Contains(got.Issues[0], "缺少章节：本周摘要") {
		t.Fatalf("缺章节应报错：%+v", got)
	}
	if got.FixStep != "write_report" {
		t.Fatalf("FixStep = %q", got.FixStep)
	}

	// 章节顺序错误
	input = makeQcInput(func(in *contract.QcInput) {
		in.Draft.Sections[0], in.Draft.Sections[1] = in.Draft.Sections[1], in.Draft.Sections[0]
	})
	if got := CheckStructure(input); got.Passed {
		t.Fatal("章节顺序错误应报错")
	}

	// 摘要句数超限
	input = makeQcInput(func(in *contract.QcInput) {
		in.Draft.Sections[0].Paragraphs = []string{"一。二。三。四。五。六。"}
	})
	if got := CheckStructure(input); got.Passed {
		t.Fatal("摘要超句数应报错")
	}

	// 下周关注条数超限
	input = makeQcInput(func(in *contract.QcInput) {
		in.Draft.Sections[5].Bullets = []string{"1", "2", "3", "4", "5", "6"}
	})
	if got := CheckStructure(input); got.Passed {
		t.Fatal("下周关注超条数应报错")
	}

	// 图表不足
	input = makeQcInput(func(in *contract.QcInput) {
		in.Rendered = &contract.RenderedStats{EstimatedPages: 2, ChartCount: 1}
	})
	if got := CheckStructure(input); got.Passed {
		t.Fatal("图表不足应报错")
	}
}

func TestTraceabilityCheck(t *testing.T) {
	// 裸数字
	input := makeQcInput(func(in *contract.QcInput) {
		in.Draft.Sections[0].Paragraphs = []string{"本周实收 573635 元。"}
	})
	got := CheckTraceability(input)
	if got.Passed || !strings.Contains(got.Issues[0], "出现未经槽位引用的数字") {
		t.Fatalf("裸数字应报错：%+v", got)
	}

	// 槽位对不上事实
	input = makeQcInput(func(in *contract.QcInput) {
		in.Draft.Sections[0].Paragraphs = []string{"本周实收 {{company.nope}}。"}
	})
	got = CheckTraceability(input)
	if got.Passed || !strings.Contains(strings.Join(got.Issues, "；"), "槽位无法对应到事实") {
		t.Fatalf("未知槽位应报错：%+v", got)
	}

	// 区域合计不等于全公司
	input = makeQcInput(func(in *contract.QcInput) {
		in.Facts.Company.RevenueCents = 1
	})
	got = CheckTraceability(input)
	if got.Passed || !strings.Contains(got.Issues[0], "区域合计") {
		t.Fatalf("区域合计不一致应报错：%+v", got)
	}

	// 白名单与引号内引用放行：门店编码、周次、日期、纪要摘录都不算裸数字
	input = makeQcInput(func(in *contract.QcInput) {
		in.Draft.Sections[3].Paragraphs = []string{"第 44 周 HZ01 门店 10 月 27 日停业，纪要称“华东区受连续降雨影响”。"}
	})
	if got := CheckTraceability(input); !got.Passed {
		t.Fatalf("白名单数字不应报错：%v", got.Issues)
	}
}

func TestForbiddenTermsCheck(t *testing.T) {
	input := makeQcInput(func(in *contract.QcInput) {
		in.Draft.Sections[1].Paragraphs = []string{"客单价提升明显。"}
	})
	got := CheckForbiddenTerms(input)
	if got.Passed || !strings.Contains(got.Issues[0], "客单价") {
		t.Fatalf("禁用指标应报错：%+v", got)
	}
}

func TestAnomalyCoverageCheck(t *testing.T) {
	input := makeQcInput(func(in *contract.QcInput) {
		in.Draft.Causes = in.Draft.Causes[1:]
	})
	got := CheckAnomalyCoverage(input)
	if got.Passed || !strings.Contains(got.Issues[0], "region:华东") {
		t.Fatalf("漏掉异常应报错：%+v", got)
	}
	if got.FixStep != "match_causes" {
		t.Fatalf("FixStep = %q，want match_causes", got.FixStep)
	}
}

func TestQuoteIntegrityCheck(t *testing.T) {
	// 摘录不是原文连续片段
	input := makeQcInput(func(in *contract.QcInput) {
		in.Draft.Causes[0].Quote = "这句话纪要里没有"
	})
	if got := CheckQuoteIntegrity(input); got.Passed {
		t.Fatal("摘录不在原文中应报错")
	}

	// 没有这一周的纪要
	input = makeQcInput(func(in *contract.QcInput) {
		in.Draft.Causes[0].SourceWeek = &contract.IsoWeek{Year: 2026, Week: 45}
	})
	if got := CheckQuoteIntegrity(input); got.Passed {
		t.Fatal("缺少对应周次纪要应报错")
	}

	// level != unknown 但缺摘录
	input = makeQcInput(func(in *contract.QcInput) {
		in.Draft.Causes[0].Quote = ""
	})
	if got := CheckQuoteIntegrity(input); got.Passed {
		t.Fatal("非 unknown 缺摘录应报错")
	}
}

func TestContradictionCoverageCheck(t *testing.T) {
	contradiction := contract.DetectedContradiction{
		ID:      "contradiction:region:华南",
		Subject: contract.DetectedSubject{Level: contract.ClaimRegion, Key: "华南", Name: "华南"},
		Claim:   contract.DetectedClaim{Direction: contract.ClaimFlat},
		Rule:    contract.RuleFlat, SourceWeek: contract.IsoWeek{Year: 2026, Week: 44},
		Quote: "本周华南整体销售与上周基本持平",
	}
	input := makeQcInput(func(in *contract.QcInput) {
		in.Contradictions = []contract.DetectedContradiction{contradiction}
	})
	got := CheckContradictionCoverage(input)
	if got.Passed || len(got.Issues) != 2 {
		t.Fatalf("未引用矛盾应报两条问题：%+v", got)
	}

	input = makeQcInput(func(in *contract.QcInput) {
		in.Contradictions = []contract.DetectedContradiction{contradiction}
		in.Draft.ContradictionRefs = []string{contradiction.ID}
		in.Draft.Sections[3].Paragraphs = append(in.Draft.Sections[3].Paragraphs, "纪要称华南持平，但数据显示下滑，以数据为准。")
	})
	if got := CheckContradictionCoverage(input); !got.Passed {
		t.Fatalf("引用并提到对象后应通过：%v", got.Issues)
	}
}

func TestFeedbackResponseCheck(t *testing.T) {
	// 没有退回意见：不产出结果
	if result := CheckFeedbackResponse(makeQcInput(nil)); result != nil {
		t.Fatalf("没有退回意见时不应产出结果：%+v", result)
	}

	feedback := &contract.Feedback{Comment: "摘要写得太长", Sections: []string{"summary"}}
	previous := makeDraft()

	// 修改说明没有覆盖退回意见
	input := makeQcInput(func(in *contract.QcInput) {
		in.Feedback = feedback
		in.PreviousDraft = &previous
	})
	result := CheckFeedbackResponse(input)
	if result == nil || result.Passed || !strings.Contains(result.Issues[0], "没有覆盖退回意见") {
		t.Fatalf("未覆盖意见应报错：%+v", result)
	}

	// 覆盖了意见、章节也确实改了
	changed := makeDraft()
	changed.Sections[0].Paragraphs = []string{"本周实收 {{company.revenue}}，环比 {{company.wow}}，整体承压。"}
	changed.RevisionNotes = []contract.RevisionNote{{
		Comment: "摘要写得太长", Sections: []string{"summary"}, Change: "压缩为一句并补充结论",
	}}
	input = makeQcInput(func(in *contract.QcInput) {
		in.Draft = changed
		in.Feedback = feedback
		in.PreviousDraft = &previous
	})
	if result := CheckFeedbackResponse(input); result == nil || !result.Passed {
		t.Fatalf("覆盖意见且章节有改动应通过：%+v", result)
	}

	// 章节没变
	unchanged := makeDraft()
	unchanged.RevisionNotes = []contract.RevisionNote{{
		Comment: "摘要写得太长", Sections: []string{"summary"}, Change: "压缩为一句",
	}}
	input = makeQcInput(func(in *contract.QcInput) {
		in.Draft = unchanged
		in.Feedback = feedback
		in.PreviousDraft = &previous
	})
	result = CheckFeedbackResponse(input)
	if result == nil || result.Passed {
		t.Fatal("章节内容没变应报错")
	}
}

func TestLengthCheck(t *testing.T) {
	input := makeQcInput(func(in *contract.QcInput) {
		in.Rendered = &contract.RenderedStats{EstimatedPages: 3, ChartCount: 2}
	})
	got := CheckLength(input)
	if got.Passed {
		t.Fatal("3 页超过 3*0.9 的余量应报错")
	}
	input = makeQcInput(func(in *contract.QcInput) {
		in.Rendered = &contract.RenderedStats{EstimatedPages: 2.5, ChartCount: 2}
	})
	if got := CheckLength(input); !got.Passed {
		t.Fatalf("2.5 页应通过：%v", got.Issues)
	}
}

// ---------------------------------------------------------------- 自定义规则

func TestCustomRules(t *testing.T) {
	makeRule := func(template string, params map[string]any) contract.CustomRule {
		return contract.CustomRule{ID: "custom-1", Label: "自定义", Template: template, Enabled: true, Params: params}
	}

	// required_terms 不满足 → 带 fixStep
	input := makeQcInput(func(in *contract.QcInput) {
		in.Config.Custom = []contract.CustomRule{makeRule(contract.TemplateRequiredTerms, map[string]any{"terms": []any{"外卖占比"}})}
	})
	results := NewEngine().Run(input)
	got := findCheck(results, "custom-1")
	if got == nil || got.Passed || got.FixStep != "write_report" {
		t.Fatalf("required_terms 应不通过并给 fixStep：%+v", got)
	}

	// number_range 按展示口径比较：金额为元、比例为百分数
	input = makeQcInput(func(in *contract.QcInput) {
		in.Config.Custom = []contract.CustomRule{makeRule(contract.TemplateNumberRange, map[string]any{"path": "company.revenue", "min": float64(500000), "max": float64(600000)})}
	})
	results = NewEngine().Run(input)
	got = findCheck(results, "custom-1")
	if got == nil || !got.Passed {
		t.Fatalf("number_range 应通过：%+v", got)
	}

	// 参数无效 → 不带 fixStep（无法自动修正）
	input = makeQcInput(func(in *contract.QcInput) {
		in.Config.Custom = []contract.CustomRule{makeRule(contract.TemplateMaxItems, map[string]any{"section": "summary"})}
	})
	results = NewEngine().Run(input)
	got = findCheck(results, "custom-1")
	if got == nil || got.Passed || got.FixStep != "" {
		t.Fatalf("参数无效应不通过且不给 fixStep：%+v", got)
	}
}
