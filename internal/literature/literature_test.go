package literature

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"digital-employee/internal/contract"
)

const (
	employeeDir = "../../employees/literature-brief"
	samplesDir  = "../../samples/scenario-b"
)

func load(t *testing.T) (*Library, *Lexicon) {
	t.Helper()
	lib, err := LoadLibrary(LibraryDir(employeeDir))
	if err != nil {
		t.Fatal(err)
	}
	lex, err := LoadLexicon(employeeDir)
	if err != nil {
		t.Fatal(err)
	}
	return lib, lex
}

func screen(t *testing.T, listFile, direction string) *ScreenResult {
	t.Helper()
	lib, lex := load(t)
	rows, err := ReadList(filepath.Join(samplesDir, listFile))
	if err != nil {
		t.Fatal(err)
	}
	return Screen(rows, lib, lex, direction, listFile)
}

// relevanceFromHints 用词表的提示当作模型的相关性判断，用来在没有模型的测试里走完后面的计算。
func relevanceFromHints(s *ScreenResult) []Relevance {
	out := []Relevance{}
	for _, inv := range s.Inventions {
		relevant := inv.HintRelevant != "no"
		reason := "IPC 分类属于该方向"
		if !relevant {
			reason = "IPC 分类 " + inv.IPC + " 不属于该方向"
		}
		out = append(out, Relevance{ID: inv.ID, Relevant: relevant, Reason: reason})
	}
	return out
}

func assignmentsFromHints(inv []Invention) []RouteAssignment {
	out := []RouteAssignment{}
	for _, i := range inv {
		out = append(out, RouteAssignment{ID: i.ID, Route: i.HintRoute, Basis: i.HintBasis})
	}
	return out
}

func idsOf(items []Excluded) []string {
	out := []string{}
	for _, e := range items {
		out = append(out, e.ID)
	}
	sort.Strings(out)
	return out
}

func TestLibrarySizes(t *testing.T) {
	lib, _ := load(t)
	if len(lib.Patents) != 299 || len(lib.Papers) != 98 {
		t.Fatalf("库大小应为专利 299、论文 98，实际 %d、%d", len(lib.Patents), len(lib.Papers))
	}
	if lib.Kind("ZZ2018338362B") != "patent" || lib.Kind("LW0094") != "paper" || lib.Kind("ZZ2024999999A") != "" {
		t.Fatal("编号归属判断不对")
	}
}

// B-01：清单共 49 行，3 行重复、1 个编号查不到、1 组同族合并，4 件与方向无关，最终纳入 40 项发明。
func TestScreenB01(t *testing.T) {
	s := screen(t, "专利清单_固态电池电解质.csv", "固态电池电解质")
	if s.Rows != 49 {
		t.Fatalf("清单行数 %d", s.Rows)
	}
	if got := strings.Join(idsOf(s.Duplicates), ","); got != "ZZ2018338362B,ZZ2019237653B,ZZ2024211797A" {
		t.Fatalf("重复行 %s", got)
	}
	if got := idsOf(s.NotFound); len(got) != 1 || got[0] != "ZZ2024999999A" {
		t.Fatalf("查不到 %v", got)
	}
	if len(s.FamilyMerged) != 1 || s.FamilyMerged[0].Representative != "ZZ2020659978B" || strings.Join(s.FamilyMerged[0].Merged, ",") != "ZZ2020805193B" {
		t.Fatalf("同族合并 %+v", s.FamilyMerged)
	}
	if len(s.Inventions) != 44 {
		t.Fatalf("核对后应有 44 项发明，实际 %d", len(s.Inventions))
	}

	scope := EvaluateScope(ScopeInput{Screen: s, Relevance: relevanceFromHints(s), Config: contract.DefaultLiteratureQC()})
	if !scope.OK || scope.Counts.Included != 40 || scope.Counts.Irrelevant != 4 {
		t.Fatalf("应纳入 40 项、剔除 4 件无关：%+v pause=%+v", scope.Counts, scope.Pause)
	}
	irrelevant := []string{}
	for _, r := range scope.Irrelevant {
		irrelevant = append(irrelevant, r.ID)
	}
	sort.Strings(irrelevant)
	if got := strings.Join(irrelevant, ","); got != "ZZ2019161869B,ZZ2020492484B,ZZ2021609431A,ZZ2022537775U" {
		t.Fatalf("无关件 %s", got)
	}

	stats := ComputeStats(StatsInput{Direction: "固态电池电解质", Included: scope.Included, Assignments: assignmentsFromHints(scope.Included), Config: contract.DefaultLiteratureQC()})
	counts := map[string]int{}
	for _, r := range stats.Routes {
		counts[r.Name] = r.Count
	}
	for name, want := range map[string]int{"硫化物": 14, "聚合物及复合": 9, "氧化物": 9, "卤化物": 8} {
		if d := counts[name] - want; d < -1 || d > 1 {
			t.Errorf("路线 %s 应为 %d 件（±1），实际 %d", name, want, counts[name])
		}
	}
	if len(stats.Unassigned) != 0 {
		t.Errorf("有未归类的发明：%v", stats.Unassigned)
	}

	// 申请量前 5 名：E10 8、E01 6、R01 5、E02 4，第 5 名起并列 3 件
	wantTop := map[string]int{"申请人E10": 8, "申请人E01": 6, "申请人R01": 5, "申请人E02": 4}
	for name, want := range wantTop {
		found := false
		for _, a := range stats.Applicants {
			if a.Name == name {
				found = true
				if a.Count != want {
					t.Errorf("%s 申请量应为 %d，实际 %d", name, want, a.Count)
				}
			}
		}
		if !found {
			t.Errorf("前 5 名里缺 %s：%+v", name, stats.Applicants)
		}
	}
	if len(stats.Applicants) < 5 {
		t.Errorf("前 5 名至少列出 5 个申请人，实际 %d", len(stats.Applicants))
	}

	// 转化线索：高校、科研院所专利里已发生的转让
	transferred := []string{}
	for _, l := range stats.Transferred {
		transferred = append(transferred, l.ID)
	}
	sort.Strings(transferred)
	wantTransferred := "ZZ2018374236B,ZZ2021158885B,ZZ2021383840B,ZZ2023560167B,ZZ2023667632B,ZZ2024545754A,ZZ2025481709A"
	if got := strings.Join(transferred, ","); got != wantTransferred {
		t.Errorf("转化线索（已转让）\n got  %s\n want %s", got, wantTransferred)
	}
	if len(stats.WorthContact) > 3 {
		t.Errorf("值得接洽最多 3 件")
	}

	// 趋势：2024 到 2026 年标注不完整
	for _, y := range stats.Trend {
		if want := y.Year >= 2024; y.Incomplete != want {
			t.Errorf("%d 年 incomplete=%v，应为 %v", y.Year, y.Incomplete, want)
		}
	}

	// 缺摘要的两件要能识别出来
	missing := []string{}
	for _, inv := range s.Inventions {
		if inv.AbstractMissing {
			missing = append(missing, inv.ID)
		}
	}
	sort.Strings(missing)
	if got := strings.Join(missing, ","); got != "ZZ2024545635A,ZZ2025274723A" {
		t.Errorf("缺摘要 %s", got)
	}
}

// B-02：36 件均有效；路线 聚阴离子 14、层状氧化物 13、普鲁士蓝类 9；E09 13 件明显领先。
func TestScreenB02(t *testing.T) {
	s := screen(t, "专利清单_钠离子电池正极材料.csv", "钠离子电池正极材料")
	if len(s.Duplicates) != 0 || len(s.NotFound) != 0 || len(s.FamilyMerged) != 0 || len(s.Inventions) != 36 {
		t.Fatalf("清单 36 件均有效：重复 %d 查不到 %d 同族 %d 发明 %d", len(s.Duplicates), len(s.NotFound), len(s.FamilyMerged), len(s.Inventions))
	}
	scope := EvaluateScope(ScopeInput{Screen: s, Relevance: relevanceFromHints(s), Config: contract.DefaultLiteratureQC()})
	if !scope.OK || scope.Counts.Included != 36 {
		t.Fatalf("应全部纳入：%+v", scope)
	}
	stats := ComputeStats(StatsInput{Direction: "钠离子电池正极材料", Included: scope.Included, Assignments: assignmentsFromHints(scope.Included), Config: contract.DefaultLiteratureQC()})
	counts := map[string]int{}
	for _, r := range stats.Routes {
		counts[r.Name] = r.Count
	}
	for name, want := range map[string]int{"聚阴离子": 14, "层状氧化物": 13, "普鲁士蓝类": 9} {
		if d := counts[name] - want; d < -1 || d > 1 {
			t.Errorf("路线 %s 应为 %d 件（±1），实际 %d", name, want, counts[name])
		}
	}
	if len(stats.Applicants) == 0 || stats.Applicants[0].Name != "申请人E09" || stats.Applicants[0].Count != 13 {
		t.Errorf("申请人E09 应以 13 件领先：%+v", stats.Applicants)
	}
	transferred := []string{}
	for _, l := range stats.Transferred {
		transferred = append(transferred, l.ID)
	}
	sort.Strings(transferred)
	if got := strings.Join(transferred, ","); got != "ZZ2020294782B,ZZ2021117740B" {
		t.Errorf("转化线索（已转让）%s", got)
	}
}

// B-04：硅基负极材料 + 固态电解质清单：无关件超过一半，暂停并说明依据。
func TestScopeB04DirectionMismatch(t *testing.T) {
	s := screen(t, "专利清单_固态电池电解质.csv", "硅基负极材料")
	scope := EvaluateScope(ScopeInput{Screen: s, Relevance: relevanceFromHints(s), Config: contract.DefaultLiteratureQC()})
	if scope.OK || scope.Pause == nil || scope.Pause.Code != "list_direction_mismatch" {
		t.Fatalf("应暂停为 list_direction_mismatch：%+v", scope.Pause)
	}
	if !strings.Contains(scope.Pause.Message, "固态电池电解质") {
		t.Errorf("暂停说明里应点出清单更像固态电池电解质：%s", scope.Pause.Message)
	}
}

// B-05：钙钛矿太阳能电池清单的 6 个编号库里都查不到：资料不足，只能补充资料或终止。
func TestScreenB05InsufficientMaterial(t *testing.T) {
	s := screen(t, "专利清单_钙钛矿太阳能电池.csv", "钙钛矿太阳能电池")
	if len(s.NotFound) != 6 || len(s.Inventions) != 0 {
		t.Fatalf("应 6 个编号查不到、没有发明：%d %d", len(s.NotFound), len(s.Inventions))
	}
	if s.Gate == nil || s.Gate.Code != "insufficient_material" {
		t.Fatalf("应给出资料不足的暂停：%+v", s.Gate)
	}
	for _, want := range []string{"专利库", "论文库", "查不到"} {
		if !strings.Contains(s.Gate.Message, want) {
			t.Errorf("说明里应写明查了哪些库、结果如何，缺“%s”：%s", want, s.Gate.Message)
		}
	}
	if included, _ := s.Gate.Detail["included"].(int); included != 0 {
		t.Errorf("0 件不能选择继续")
	}
}

func TestScopeSmallSample(t *testing.T) {
	s := screen(t, "专利清单_钠离子电池正极材料.csv", "钠离子电池正极材料")
	s.Inventions = s.Inventions[:3]
	paused := EvaluateScope(ScopeInput{Screen: s, Relevance: relevanceFromHints(s), Config: contract.DefaultLiteratureQC()})
	if paused.OK || paused.Pause == nil || paused.Pause.Code != "insufficient_material" {
		t.Fatalf("纳入 3 件应暂停：%+v", paused.Pause)
	}
	if n, _ := paused.Pause.Detail["included"].(int); n != 3 {
		t.Errorf("暂停详情里应带纳入件数 3：%v", paused.Pause.Detail)
	}
	cont := EvaluateScope(ScopeInput{Screen: s, Relevance: relevanceFromHints(s), Config: contract.DefaultLiteratureQC(), AllowSmallSample: true})
	if !cont.OK || !cont.SmallSample || cont.Counts.Included != 3 {
		t.Fatalf("按现有资料继续后应通过并标记样本过少：%+v", cont)
	}
}

func TestSearchPapers(t *testing.T) {
	lib, _ := load(t)
	out := Search(lib, SearchInput{Direction: "钠离子电池正极材料", Queries: []string{"聚阴离子"}, TopK: 5})
	if len(out.Hits) == 0 {
		t.Fatal("应检索到钠离子电池正极材料的论文")
	}
	for _, h := range out.Hits {
		if h.Kind != "paper" || lib.Kind(h.ID) != "paper" {
			t.Fatalf("只应返回论文库里的论文：%+v", h)
		}
	}
	// 关键词直接标了方向的论文排在前面
	if !strings.Contains(lib.Papers[out.Hits[0].ID].Keywords, "钠离子电池正极材料") {
		t.Errorf("第一条应是关键词标了该方向的论文：%+v", lib.Papers[out.Hits[0].ID])
	}
	none := Search(lib, SearchInput{Direction: "钙钛矿太阳能电池", Queries: []string{"钙钛矿太阳能电池"}})
	for _, h := range none.Hits {
		if strings.Contains(lib.Papers[h.ID].Keywords, "钙钛矿太阳能电池") {
			t.Errorf("库里没有这个方向，不该有关键词命中：%+v", h)
		}
	}
}

func TestListWithoutIDColumn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.csv")
	if err := writeFile(path, "序号,标题\n1,某专利\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadList(path); err == nil || !strings.Contains(err.Error(), "专利编号") {
		t.Fatalf("缺“专利编号”列应报错并点名：%v", err)
	}
}
