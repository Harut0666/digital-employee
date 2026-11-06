package parity

// A-01 golden 对拍：Go 版工具的输出必须与 TS 版逐字段一致。
// 基准产物来自 golden-a01/steps/*.json（TS 版 run-case 的真实 step 输出），
// 输入附件来自 golden-a01-ws/tasks/<id>/attachments/。
//
// 运行：go test ./internal/parity -run TestGolden -v

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"digital-employee/internal/contract"
	"digital-employee/internal/tools"
)

const goldenDir = "../../golden-a01"

func loadGolden[T any](t *testing.T, step string) T {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(goldenDir, "steps", step+".json"))
	if err != nil {
		t.Fatalf("读 golden %s 失败: %v", step, err)
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("解析 golden %s 失败: %v", step, err)
	}
	return v
}

// attachments 找到 golden 工作区里的任务附件目录（已 gitignore，但本机保留）。
func attachments(t *testing.T) string {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(goldenDir+"-ws", "tasks", "*", "attachments"))
	if len(matches) == 0 {
		t.Skip("没有 golden-a01-ws 工作区（附件基准），跳过")
	}
	return matches[0]
}

// diffJSON 递归比较两个 JSON 值，返回可读差异（最多 limit 条）。
func diffJSON(a, b any, path string, out *[]string, limit int) {
	if len(*out) >= limit {
		return
	}
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: 类型不同（golden object vs got %T）", path, b))
			return
		}
		keys := map[string]bool{}
		for k := range av {
			keys[k] = true
		}
		for k := range bv {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			avk, aok := av[k]
			bvk, bok := bv[k]
			sub := path + "." + k
			switch {
			case aok && !bok:
				*out = append(*out, sub+": golden 有、Go 版缺失")
			case !aok && bok:
				*out = append(*out, sub+": Go 版多出（golden 没有）")
			default:
				diffJSON(avk, bvk, sub, out, limit)
			}
		}
	case []any:
		bv, ok := b.([]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: 类型不同（golden array vs got %T）", path, b))
			return
		}
		if len(av) != len(bv) {
			*out = append(*out, fmt.Sprintf("%s: 数组长度不同（golden %d vs got %d）", path, len(av), len(bv)))
		}
		for i := 0; i < len(av) && i < len(bv); i++ {
			diffJSON(av[i], bv[i], fmt.Sprintf("%s[%d]", path, i), out, limit)
		}
	case float64:
		bv, ok := b.(float64)
		if !ok || av != bv {
			*out = append(*out, fmt.Sprintf("%s: golden=%v got=%v", path, av, b))
		}
	default:
		if fmt.Sprint(a) != fmt.Sprint(b) {
			*out = append(*out, fmt.Sprintf("%s: golden=%v got=%v", path, a, b))
		}
	}
}

func asJSON(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(err)
	}
	return out
}

// TestGoldenComputeMetrics A-01 事实与 TS 版一致（金额/环比/毛利/目标/异常/退货/趋势）。
func TestGoldenComputeMetrics(t *testing.T) {
	att := attachments(t)
	ws := t.TempDir()
	sales := filepath.Join(ws, "sales.xlsx")
	copyFile(t, filepath.Join(att, "销售明细.xlsx"), sales)

	loaded, err := tools.LoadSales(sales, ws)
	if err != nil {
		t.Fatalf("LoadSales: %v", err)
	}
	got, err := tools.ComputeMetrics(contract.ComputeMetricsInput{
		DatasetPath:      loaded.DatasetPath,
		Period:           contract.IsoWeek{Year: 2026, Week: 44},
		AnomalyThreshold: 0.10,
	})
	if err != nil {
		t.Fatalf("ComputeMetrics: %v", err)
	}
	want := loadGolden[contract.WeeklyFacts](t, "compute_metrics")

	var diffs []string
	diffJSON(asJSON(want), asJSON(got), "facts", &diffs, 40)
	if len(diffs) > 0 {
		t.Errorf("WeeklyFacts 与 golden 不一致 %d 处：\n%s", len(diffs), joinLines(diffs))
	}
}

// TestGoldenParseMinutes 纪要解析一致（段落 id/文本/周次）。
func TestGoldenParseMinutes(t *testing.T) {
	att := attachments(t)
	ws := t.TempDir()
	minutes := filepath.Join(ws, "minutes.docx")
	copyFile(t, filepath.Join(att, "周会纪要_2026W44.docx"), minutes)

	got, err := tools.ParseMinutes([]string{minutes})
	if err != nil {
		t.Fatalf("ParseMinutes: %v", err)
	}
	want := loadGolden[contract.ParseMinutesOutput](t, "parse_minutes")
	// path 是输入文件路径的回显，两次运行必然不同；只比较解析内容。
	for i := range want.Minutes {
		want.Minutes[i].Path = "<path>"
	}
	for i := range got.Minutes {
		got.Minutes[i].Path = "<path>"
	}

	var diffs []string
	diffJSON(asJSON(want), asJSON(got), "minutes", &diffs, 40)
	if len(diffs) > 0 {
		t.Errorf("解析结果与 golden 不一致 %d 处：\n%s", len(diffs), joinLines(diffs))
	}
}

// TestGoldenContradictions 矛盾判定一致（输入：golden 的 claims + facts + qc 阈值）。
func TestGoldenContradictions(t *testing.T) {
	var causes struct {
		Claims []contract.Claim `json:"claims"`
	}
	raw, err := os.ReadFile(filepath.Join(goldenDir, "steps", "match_causes.json"))
	if err != nil {
		t.Fatalf("读 match_causes golden: %v", err)
	}
	if err := json.Unmarshal(raw, &causes); err != nil {
		t.Fatalf("解析 claims: %v", err)
	}
	facts := loadGolden[contract.WeeklyFacts](t, "compute_metrics")
	got, err := tools.DetectContradictions(contract.DetectContradictionsInput{
		Claims: causes.Claims,
		Facts:  facts,
		Thresholds: contract.ContradictionThresholds{
			FlatTolerance: 0.05, DirectionMin: 0.02, MagnitudeTolerance: 0.10,
		},
	})
	if err != nil {
		t.Fatalf("DetectContradictions: %v", err)
	}
	want := loadGolden[contract.DetectContradictionsOutput](t, "detect_contradictions")

	var diffs []string
	diffJSON(asJSON(want), asJSON(got), "contradictions", &diffs, 40)
	if len(diffs) > 0 {
		t.Errorf("矛盾判定与 golden 不一致 %d 处：\n%s", len(diffs), joinLines(diffs))
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("读 %s: %v", src, err)
	}
	if err := os.WriteFile(dst, raw, 0o644); err != nil {
		t.Fatalf("写 %s: %v", dst, err)
	}
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += "  - " + l + "\n"
	}
	return out
}
