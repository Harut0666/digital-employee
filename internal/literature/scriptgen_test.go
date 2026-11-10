package literature

// 剧本生成器：为场景 B 的用例生成剧本式假模型的输出（samples/cases/scripts/B-*.json）。
// 剧本里的判断输出由真实的清单核对和统计结果推出来（词表提示当“模型”），数字不是手写的。
// 平时不跑；要重新生成时：
//
//	$env:DE_GEN_SCRIPTS = "D:\...\samples\cases\scripts"; go test ./internal/literature -run TestGenerateLiteratureScripts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"digital-employee/internal/contract"
)

type scriptTurn struct {
	ToolCalls []map[string]any `json:"toolCalls"`
}

func submit(arguments any) scriptTurn {
	return scriptTurn{ToolCalls: []map[string]any{{"name": "submit_result", "arguments": arguments}}}
}

func sentences(text string) []string {
	out := []string{}
	for _, s := range strings.Split(text, "。") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// pick 取摘要里以某个词开头的句子（没有就取第 fallback 句）。
func pickSentence(abstract, prefix string, fallback int) string {
	ss := sentences(abstract)
	for _, s := range ss {
		if strings.HasPrefix(s, prefix) {
			return s
		}
	}
	if fallback < len(ss) {
		return ss[fallback]
	}
	return ""
}

func relevanceScript(s *ScreenResult) scriptTurn {
	items := []map[string]any{}
	for _, inv := range s.Inventions {
		relevant := inv.HintRelevant != "no"
		reason := fmt.Sprintf("IPC 分类 %s 属于该技术方向，解决的是该方向的材料体系问题", inv.IPC)
		if !relevant {
			reason = fmt.Sprintf("IPC 分类 %s 不属于该技术方向，“%s”不是这个方向的材料", inv.IPC, inv.Title)
			// 清单整体更像另一个方向（B-04）：点明它们属于哪个方向
			if s.ListTheme != nil && s.ListTheme.Direction != s.Direction && inv.IPC != "" {
				reason = fmt.Sprintf("属于“%s”方向（IPC %s），与所研判的“%s”无关", s.ListTheme.Direction, inv.IPC, s.Direction)
			}
		}
		items = append(items, map[string]any{"id": inv.ID, "relevant": relevant, "reason": reason})
	}
	return submit(map[string]any{"items": items})
}

func routesScript(included []Invention) scriptTurn {
	assignments := []map[string]any{}
	for _, inv := range included {
		route, basis := inv.HintRoute, inv.HintBasis
		assignments = append(assignments, map[string]any{"id": inv.ID, "route": route, "basis": basis})
	}
	return submit(map[string]any{"assignments": assignments})
}

// briefScript 用统计结果拼一份有出处的简报草稿：性能指标原样取自代表专利的摘要。
func briefScript(lib *Library, s *ScreenResult, scope *ScopeResult, stats *Stats, direction string, extraRisk string, withNotes bool) map[string]any {
	claim := func(text string, cites []string, route string) map[string]any {
		if cites == nil {
			cites = []string{}
		}
		m := map[string]any{"text": text, "cites": cites}
		if route != "" {
			m["route"] = route
		}
		return m
	}
	top := stats.Routes[0]
	lead := stats.Applicants[0]
	var leadCite []string
	for _, inv := range scope.Included {
		for _, a := range inv.Applicants {
			if a == lead.Name && len(leadCite) == 0 {
				leadCite = []string{inv.ID}
			}
		}
	}
	incompleteFrom := yearOf(stats.CutoffDate) - 2
	var recentCite []string
	for _, inv := range scope.Included {
		if y := yearOf(inv.FilingDate); y >= incompleteFrom && len(recentCite) < 2 {
			recentCite = append(recentCite, inv.ID)
		}
	}
	transferred := []string{}
	for _, l := range stats.Transferred {
		transferred = append(transferred, l.ID)
	}
	worth := []string{}
	for _, l := range stats.WorthContact {
		worth = append(worth, l.ID)
	}

	summary := []any{
		claim(fmt.Sprintf("纳入分析的发明共 %d 项，%s路线最多（%d 件）。", stats.Included, top.Name, top.Count), []string{top.Representative[0].ID}, ""),
		claim(fmt.Sprintf("申请量最高的是%s，共 %d 项发明。", lead.Name, lead.Count), leadCite, ""),
	}
	if len(recentCite) > 0 {
		summary = append(summary, claim(fmt.Sprintf("%d–%d 年的申请量因专利公开滞后而不完整，趋势判断需谨慎。", incompleteFrom, yearOf(stats.CutoffDate)), recentCite, ""))
	}
	if len(transferred) > 0 {
		summary = append(summary, claim(fmt.Sprintf("高校、科研院所已有 %d 件专利发生转让或许可，转化已有先例。", len(transferred)), transferred[:min(2, len(transferred))], ""))
	}
	if len(worth) > 0 {
		summary = append(summary, claim("尚未转让的高校、科研院所专利中，有被引较多、状态有效的专利值得接洽。", worth[:1], ""))
	}

	routes := []any{}
	for _, r := range stats.Routes {
		p := lib.Patents[r.Representative[0].ID]
		means := pickSentence(p.Abstract, "针对", 1)
		perf := pickSentence(p.Abstract, "实施例表明", 2)
		text := fmt.Sprintf("%s路线共 %d 件。代表专利“%s”%s；%s。", r.Name, r.Count, p.Title, means, perf)
		if means == "" || perf == "" {
			text = fmt.Sprintf("%s路线共 %d 件，代表专利“%s”体现了这条路线的主要技术手段。", r.Name, r.Count, p.Title)
		}
		routes = append(routes, claim(text, []string{p.ID}, r.Name))
	}
	// 缺摘要的专利：引用时程序自动加注依据
	missing := []string{}
	for _, inv := range scope.Included {
		if inv.AbstractMissing {
			missing = append(missing, inv.ID)
		}
	}
	if len(missing) > 0 {
		routes = append(routes, claim("另有个别专利在库中缺摘要，只能依据标题和主权项判断它们的路线归属。", missing, top.Name))
	}

	overview := []any{}
	if len(scope.Irrelevant) > 0 {
		overview = append(overview, claim("清单里混入的无关专利主要是其他材料方向和电池结构、消防类专利，已按 IPC 分类和技术内容剔除。", nil, ""))
	}
	papers := Search(lib, SearchInput{Direction: direction, Queries: stats.RouteNames, Scope: "papers", TopK: 8})
	if len(papers.Hits) > 0 {
		overview = append(overview, claim("论文库中有同方向的研究，可作为专利之外的佐证。", []string{papers.Hits[0].ID}, ""))
	}

	trend := []any{}
	trendNote := fmt.Sprintf("整体看，近三年（%d 年起）的申请占 %.1f%%，但最近两年的数据因公开滞后不完整，不能据此判断下滑。", stats.RecentFromYear, stats.RecentSharePct)
	if len(recentCite) > 0 {
		trend = append(trend, claim(trendNote, recentCite[:1], ""))
	}
	// 哪条路线近年占比最高：依据申请年份统计
	hot := stats.Routes[0]
	for _, r := range stats.Routes {
		if r.RecentSharePct > hot.RecentSharePct {
			hot = r
		}
	}
	trend = append(trend, claim(fmt.Sprintf("%s路线近三年（%d 年起）申请占该路线的 %.1f%%，高于整体的 %.1f%%，近年更活跃。", hot.Name, stats.RecentFromYear, hot.RecentSharePct, stats.RecentSharePct),
		[]string{hot.Representative[0].ID}, ""))

	applicants := []any{claim(fmt.Sprintf("%s以 %d 项发明领先，申请人以企业为主，高校和科研院所也有参与。", lead.Name, lead.Count), leadCite, "")}

	leads := []any{}
	if len(transferred) > 0 {
		leads = append(leads, claim("已发生的转让集中在少数几家受让方，说明这些高校、科研院所的成果有被承接的先例。", transferred, ""))
	}
	if len(worth) > 0 {
		leads = append(leads, claim("值得接洽的专利按被引次数排序，依据是法律状态有效或在实质审查中、尚未转让。", worth, ""))
	}

	risks := []any{claim("性能指标均来自专利摘要的自述，未经独立验证。", nil, "")}
	if extraRisk != "" {
		risks = append(risks, claim(extraRisk, nil, ""))
	}

	draft := map[string]any{
		"title": direction + "技术研判简报",
		"sections": []any{
			map[string]any{"id": "summary", "claims": summary},
			map[string]any{"id": "overview", "claims": overview},
			map[string]any{"id": "routes", "claims": routes},
			map[string]any{"id": "trend", "claims": trend},
			map[string]any{"id": "applicants", "claims": applicants},
			map[string]any{"id": "leads", "claims": leads},
			map[string]any{"id": "risks", "claims": risks},
		},
	}
	if withNotes {
		draft["revisionNotes"] = []any{map[string]any{
			"comment":  "风险与不确定性写得太简单，请补充数据口径和样本的局限。",
			"sections": []string{"risks"},
			"change":   "在风险与不确定性里补充了数据口径、同族合并和样本范围的局限。",
		}}
	}
	return draft
}

func writeScript(t *testing.T, dir, name string, script map[string][]any) {
	t.Helper()
	data, err := json.MarshalIndent(script, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateLiteratureScripts(t *testing.T) {
	dir := os.Getenv("DE_GEN_SCRIPTS")
	if dir == "" {
		t.Skip("设置 DE_GEN_SCRIPTS 为剧本目录才会重新生成")
	}
	lib, lex := load(t)
	cfg := testConfig()
	build := func(listFile, direction string) (*ScreenResult, *ScopeResult, *Stats) {
		rows, err := ReadList(filepath.Join(samplesDir, listFile))
		if err != nil {
			t.Fatal(err)
		}
		s := Screen(rows, lib, lex, direction, listFile)
		scope := EvaluateScope(ScopeInput{Screen: s, Relevance: relevanceFromHints(s), Config: cfg})
		if !scope.OK {
			return s, scope, nil
		}
		stats := ComputeStats(StatsInput{Direction: direction, Included: scope.Included, Assignments: assignmentsFromHints(scope.Included), Config: cfg})
		return s, scope, stats
	}

	// B-01：固态电池电解质；B-03 上传清单后继续，用同一份剧本
	s1, scope1, stats1 := build("专利清单_固态电池电解质.csv", "固态电池电解质")
	b01 := map[string][]any{
		"judge_relevance": {relevanceScript(s1)},
		"judge_routes":    {routesScript(scope1.Included)},
		"write_brief":     {submit(briefScript(lib, s1, scope1, stats1, "固态电池电解质", "", false))},
	}
	writeScript(t, dir, "B-01", b01)
	writeScript(t, dir, "B-03", b01)

	// B-01 变体：审核退回后重做，第二轮写简报带修改说明
	rework := map[string][]any{
		"judge_relevance": {relevanceScript(s1)},
		"judge_routes":    {routesScript(scope1.Included)},
		"write_brief": {
			submit(briefScript(lib, s1, scope1, stats1, "固态电池电解质", "", false)),
			submit(briefScript(lib, s1, scope1, stats1, "固态电池电解质", "数据口径：清单核对只依据专利库的字段，同族合并后按发明计数，趋势按申请年份统计，没有核对专利的实际实施情况；样本限于清单给出的专利，不代表该方向的全部申请。", true)),
		},
	}
	writeScript(t, dir, "B-01-rework", rework)

	// B-02：钠离子电池正极材料
	s2, scope2, stats2 := build("专利清单_钠离子电池正极材料.csv", "钠离子电池正极材料")
	writeScript(t, dir, "B-02", map[string][]any{
		"judge_relevance": {relevanceScript(s2)},
		"judge_routes":    {routesScript(scope2.Included)},
		"write_brief":     {submit(briefScript(lib, s2, scope2, stats2, "钠离子电池正极材料", "", false))},
	})

	// B-04：方向写的是硅基负极材料，清单却是固态电解质：模型把它们逐条判为无关，程序暂停
	s4, _, _ := build("专利清单_固态电池电解质.csv", "硅基负极材料")
	writeScript(t, dir, "B-04", map[string][]any{"judge_relevance": {relevanceScript(s4)}})

	// B-06：清单只有 3 项有效发明：纳入偏少暂停；按现有资料继续后从头重跑，所以两个判断步骤各要多一轮
	s6, scope6, stats6 := build("专利清单_钠离子电池正极材料_少量.csv", "钠离子电池正极材料")
	if scope6.Counts.Included != 3 {
		t.Fatalf("B-06 的清单应纳入 3 项：%+v", scope6.Counts)
	}
	scope6.OK, scope6.SmallSample = true, true
	stats6 = ComputeStats(StatsInput{Direction: "钠离子电池正极材料", Included: scope6.Included, Assignments: assignmentsFromHints(scope6.Included), Config: cfg})
	writeScript(t, dir, "B-06", map[string][]any{
		"judge_relevance": {relevanceScript(s6), relevanceScript(s6)},
		"judge_routes":    {routesScript(scope6.Included)},
		"write_brief":     {submit(briefScript(lib, s6, scope6, stats6, "钠离子电池正极材料", "样本只有 3 项发明，每条路线的判断都只是线索，不能代表该方向的整体情况。", false))},
	})

	// B-05：清单里的编号库里都查不到：清单核对步骤就暂停，没有判断步骤要跑
	writeScript(t, dir, "B-05", map[string][]any{})

	routeNames := []string{}
	for _, r := range stats1.Routes {
		routeNames = append(routeNames, r.Name)
	}
	sort.Strings(routeNames)
	t.Logf("B-01 路线：%v", routeNames)
	_ = contract.DefaultLiteratureQC
}
