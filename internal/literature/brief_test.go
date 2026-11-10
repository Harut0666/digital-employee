package literature

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"digital-employee/internal/contract"
)

func testConfig() contract.LiteratureQC {
	cfg := contract.DefaultLiteratureQC()
	cfg.Sections = []contract.SectionSpec{
		{ID: "summary", Heading: "结论摘要"}, {ID: "overview", Heading: "资料概况"}, {ID: "routes", Heading: "技术路线分类"},
		{ID: "trend", Heading: "申请趋势"}, {ID: "applicants", Heading: "主要申请人"}, {ID: "leads", Heading: "转化线索"},
		{ID: "risks", Heading: "风险与不确定性"},
	}
	cfg.ForbiddenTerms = []string{"保证盈利", "必然成功"}
	return cfg
}

type pipeline struct {
	lib    *Library
	screen *ScreenResult
	scope  *ScopeResult
	stats  *Stats
	cfg    contract.LiteratureQC
}

func b01(t *testing.T) pipeline {
	t.Helper()
	lib, _ := load(t)
	s := screen(t, "专利清单_固态电池电解质.csv", "固态电池电解质")
	cfg := testConfig()
	scope := EvaluateScope(ScopeInput{Screen: s, Relevance: relevanceFromHints(s), Config: cfg})
	if !scope.OK {
		t.Fatalf("B-01 应通过范围检查：%+v", scope.Pause)
	}
	stats := ComputeStats(StatsInput{Direction: "固态电池电解质", Included: scope.Included, Assignments: assignmentsFromHints(scope.Included), Config: cfg})
	return pipeline{lib: lib, screen: s, scope: scope, stats: stats, cfg: cfg}
}

// goodDraft 用统计结果拼一份合规的简报草稿：数字取自统计，引用都是纳入分析的专利。
func (p pipeline) goodDraft() BriefDraft {
	st := p.stats
	top := st.Routes[0]
	routes := BriefSection{ID: "routes"}
	for _, r := range st.Routes {
		routes.Claims = append(routes.Claims, Claim{
			Route: r.Name,
			Text:  fmt.Sprintf("%s路线共 %d 件，代表专利聚焦材料体系与制备工艺的改进。", r.Name, r.Count),
			Cites: []string{r.Representative[0].ID},
		})
	}
	var leads []string
	for _, l := range st.Transferred {
		leads = append(leads, l.ID)
	}
	return BriefDraft{
		Title: "固态电池电解质技术研判简报",
		Sections: []BriefSection{
			{ID: "summary", Claims: []Claim{
				{Text: fmt.Sprintf("纳入分析的发明共 %d 项，%s路线最多。", st.Included, top.Name), Cites: []string{top.Representative[0].ID}},
				{Text: fmt.Sprintf("申请量最高的是%s，共 %d 项发明。", st.Applicants[0].Name, st.Applicants[0].Count), Cites: []string{top.Representative[1].ID}},
			}},
			{ID: "overview", Claims: []Claim{{Text: "清单核对后纳入分析的资料以专利为主。"}}},
			routes,
			{ID: "trend", Claims: []Claim{{Text: "近三年的申请量整体不低，但最近两年数据不完整。", Cites: []string{top.Representative[0].ID}}}},
			{ID: "applicants", Claims: []Claim{{Text: "头部申请人以企业为主。", Cites: []string{top.Representative[2].ID}}}},
			{ID: "leads", Claims: []Claim{{Text: "高校和科研院所的专利已有多件转让，转化活跃。", Cites: leads[:2]}}},
			{ID: "risks", Claims: []Claim{{Text: "判断依据仅限本地资料库。"}}},
		},
	}
}

func (p pipeline) render(t *testing.T, draft BriefDraft) *Rendered {
	t.Helper()
	out, err := RenderBrief(RenderInput{
		Direction: "固态电池电解质", Draft: draft, Stats: p.stats, Scope: p.scope, Screen: p.screen, Config: p.cfg,
		OutDir: t.TempDir(), IdempotencyKey: "task-b01-write_brief-r0-abc", EmployeeDir: employeeDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (p pipeline) qc(draft BriefDraft, rendered *Rendered) map[string]contract.QcCheckResult {
	byID := map[string]contract.QcCheckResult{}
	for _, r := range RunQC(QcInput{Config: p.cfg, Draft: draft, Stats: p.stats, Scope: p.scope, Screen: p.screen, Rendered: rendered, Lib: p.lib}) {
		byID[r.ID] = r
	}
	return byID
}

func TestRenderAndQCPass(t *testing.T) {
	p := b01(t)
	draft := p.goodDraft()
	rendered := p.render(t, draft)

	data, err := os.ReadFile(rendered.MDPath)
	if err != nil {
		t.Fatal(err)
	}
	md := string(data)
	for _, want := range []string{"# 固态电池电解质技术研判简报", "## 结论摘要", "## 资料概况", "## 技术路线分类", "## 申请趋势", "## 主要申请人", "## 转化线索", "## 风险与不确定性", "## 来源清单", "最终纳入分析：40 项发明", "![技术路线分布](assets/", "![申请趋势](assets/"} {
		if !strings.Contains(md, want) {
			t.Errorf("简报里应有 %q", want)
		}
	}
	if rendered.ChartCount != 2 {
		t.Errorf("应有 2 张图表，实际 %d", rendered.ChartCount)
	}
	for _, chart := range rendered.Charts {
		if info, err := os.Stat(chart); err != nil || info.Size() == 0 {
			t.Errorf("图表文件不存在或为空：%s", chart)
		}
		if filepath.Dir(chart) != rendered.AssetsDir {
			t.Errorf("图表应放在 assets 文件夹：%s", chart)
		}
	}
	// 来源清单：正文里出现的每个 [n] 都对应来源清单里的一条
	if len(rendered.Citations) == 0 {
		t.Fatal("没有引用")
	}
	for _, c := range rendered.Citations {
		if c.Kind != "patent" && c.Kind != "paper" {
			t.Errorf("来源清单里有库中不存在的编号：%+v", c)
		}
		if !strings.Contains(md, fmt.Sprintf("[%d] %s", c.N, c.ID)) {
			t.Errorf("来源清单缺 [%d] %s", c.N, c.ID)
		}
	}
	if rendered.EstimatedPages <= 0 || rendered.EstimatedPages > 5 {
		t.Errorf("篇幅估算异常：%.2f 页", rendered.EstimatedPages)
	}

	for id, r := range p.qc(draft, rendered) {
		if !r.Passed {
			t.Errorf("质检 %s 应通过：%v", id, r.Issues)
		}
	}
}

func TestMissingAbstractIsAnnotated(t *testing.T) {
	p := b01(t)
	draft := p.goodDraft()
	draft.Sections[0].Claims[0].Cites = []string{"ZZ2024545635A"} // 库里缺摘要
	rendered := p.render(t, draft)
	data, _ := os.ReadFile(rendered.MDPath)
	if !strings.Contains(string(data), "（依据标题和主权项）") {
		t.Error("引用缺摘要的专利时应自动加注“（依据标题和主权项）”")
	}
	if !strings.Contains(string(data), "ZZ2024545635A") {
		t.Error("来源清单里应有这件专利")
	}
}

func TestQCCatchesViolations(t *testing.T) {
	p := b01(t)
	cases := []struct {
		name   string
		mutate func(d *BriefDraft)
		check  string
		want   string
	}{
		{"编造编号", func(d *BriefDraft) { d.Sections[0].Claims[0].Cites = []string{"ZZ2024999999A"} }, "cite_valid", "不在专利库或论文库中"},
		{"引用被剔除的无关专利", func(d *BriefDraft) { d.Sections[0].Claims[0].Cites = []string{"ZZ2022537775U"} }, "cite_valid", "与方向无关"},
		{"正文里编造编号", func(d *BriefDraft) { d.Sections[1].Claims[0].Text = "另有 ZZ2023123456A 值得关注。" }, "cite_valid", "不存在的编号"},
		{"摘要没有角标", func(d *BriefDraft) { d.Sections[0].Claims[0].Cites = nil }, "summary_cited", "没有引用角标"},
		{"摘要超过 5 条", func(d *BriefDraft) {
			for i := 0; i < 5; i++ {
				d.Sections[0].Claims = append(d.Sections[0].Claims, d.Sections[0].Claims[0])
			}
		}, "structure", "不得超过 5 条"},
		{"缺章节", func(d *BriefDraft) { d.Sections = d.Sections[:5] }, "structure", "缺少章节"},
		{"路线没有描述", func(d *BriefDraft) {
			d.Sections[2].Claims = d.Sections[2].Claims[:1]
		}, "structure", "缺少对主要技术手段"},
		{"编造数字", func(d *BriefDraft) { d.Sections[3].Claims[0].Text = "近三年申请量增长了 87%。" }, "numbers_traceable", "87"},
		{"路线章节句子没有出处", func(d *BriefDraft) { d.Sections[2].Claims[0].Cites = nil }, "cite_valid", "没有引用角标"},
		{"禁用词", func(d *BriefDraft) { d.Sections[6].Claims[0].Text = "该方向必然成功。" }, "forbidden_terms", "必然成功"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			draft := p.goodDraft()
			c.mutate(&draft)
			rendered := p.render(t, draft)
			r := p.qc(draft, rendered)[c.check]
			if r.Passed {
				t.Fatalf("质检 %s 应不通过", c.check)
			}
			if r.FixStep != DefaultFixStep {
				t.Errorf("不通过时应指向重跑 %s，实际 %q", DefaultFixStep, r.FixStep)
			}
			if !strings.Contains(strings.Join(r.Issues, "；"), c.want) {
				t.Errorf("问题里应含“%s”：%v", c.want, r.Issues)
			}
		})
	}
}

func TestNumbersFromCitedAbstractAreAllowed(t *testing.T) {
	p := b01(t)
	draft := p.goodDraft()
	// 找一个只出现在 ZZ2018338362B 摘要里的数字：不在统计结果里，也不在另一件代表专利的原文里
	base := allowedNumbers(QcInput{Stats: p.stats, Scope: p.scope, Screen: p.screen})
	other := p.lib.Patents[p.stats.Routes[0].Representative[0].ID]
	otherText := other.Title + " " + other.Abstract + " " + other.Claim
	pick, source := "", ""
	for _, inv := range p.scope.Included {
		if inv.ID == other.ID {
			continue
		}
		for _, n := range numberPattern.FindAllString(p.lib.Patents[inv.ID].Abstract, -1) {
			norm := normNumber(n)
			if !base[norm] && !smallNumber(norm) && !isYear(norm) && !strings.Contains(otherText, n) {
				pick, source = n, inv.ID
				break
			}
		}
		if pick != "" {
			break
		}
	}
	if pick == "" {
		t.Fatal("找不到专属于某件专利摘要的数字，测试数据不够用")
	}
	draft.Sections[2].Claims[0].Cites = []string{source}
	draft.Sections[2].Claims[0].Text = "所引专利的摘要给出的指标为 " + pick + "。"
	rendered := p.render(t, draft)
	if r := p.qc(draft, rendered)["numbers_traceable"]; !r.Passed {
		t.Fatalf("所引专利摘要里的数字可以照写：%v", r.Issues)
	}
	// 但换成没有引用它的句子，同样的数字就不行
	draft.Sections[2].Claims[0].Cites = []string{p.stats.Routes[0].Representative[0].ID}
	rendered = p.render(t, draft)
	if r := p.qc(draft, rendered)["numbers_traceable"]; r.Passed {
		t.Fatal("没有引用写有这些数字的资料时，数字应被拦住")
	}
}

func TestFeedbackResponse(t *testing.T) {
	p := b01(t)
	prev := p.goodDraft()
	next := p.goodDraft()
	next.Sections[1].Claims = append(next.Sections[1].Claims, Claim{Text: "补充说明清单核对的口径。"})
	next.RevisionNotes = []contract.RevisionNote{{Comment: "资料概况要补充口径", Sections: []string{"overview"}, Change: "补充了口径说明"}}
	rendered := p.render(t, next)
	run := func(draft BriefDraft) contract.QcCheckResult {
		for _, r := range RunQC(QcInput{Config: p.cfg, Draft: draft, Stats: p.stats, Scope: p.scope, Screen: p.screen, Rendered: rendered, Lib: p.lib,
			PreviousDraft: &prev, Feedback: &contract.Feedback{Comment: "资料概况要补充口径"}}) {
			if r.ID == "feedback_response" {
				return r
			}
		}
		t.Fatal("重做时应有意见响应检查")
		return contract.QcCheckResult{}
	}
	if r := run(next); !r.Passed {
		t.Fatalf("改了对应章节并写了修改说明应通过：%v", r.Issues)
	}
	unchanged := prev
	unchanged.RevisionNotes = next.RevisionNotes
	if r := run(unchanged); r.Passed {
		t.Fatal("声称改了章节但内容和上一版一样，应不通过")
	}
	if r := run(prev); r.Passed {
		t.Fatal("没有修改说明应不通过")
	}
}

func TestQCSkipsFeedbackOnFirstRound(t *testing.T) {
	p := b01(t)
	draft := p.goodDraft()
	for id := range p.qc(draft, p.render(t, draft)) {
		if id == "feedback_response" {
			t.Fatal("首轮不检查意见响应")
		}
	}
}
