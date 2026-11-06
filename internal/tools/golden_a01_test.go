package tools

// 与 golden-a01（TS 版跑 A-01 的真实 step 产物）对拍 admission / load_data 两步。
// compute_metrics / parse_minutes / detect_contradictions 的对拍在 internal/parity 里，
// 这里补齐工具注册表链路（validate_submission / load_sales）与数据集文件命名。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"digital-employee/internal/contract"
)

const goldenA01Dir = "../../golden-a01"

func readGoldenStep(t *testing.T, step string) any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(goldenA01Dir, "steps", step+".json"))
	if err != nil {
		t.Skipf("没有 golden %s，跳过：%v", step, err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("解析 golden %s 失败：%v", step, err)
	}
	return v
}

// normalizeAttachmentPaths golden 里的任务附件路径 → 本机样例目录（文件内容相同）。
func normalizeAttachmentPaths(v any, samples string) any {
	var walk func(x any) any
	walk = func(x any) any {
		switch val := x.(type) {
		case string:
			idx := strings.LastIndexAny(val, `/\`)
			if idx < 0 {
				return val
			}
			base := val[idx+1:]
			if base == "销售明细.xlsx" || base == "销售明细.csv" || strings.HasPrefix(base, "周会纪要_") {
				return filepath.Join(samples, base)
			}
			return val
		case []any:
			out := make([]any, len(val))
			for i, item := range val {
				out[i] = walk(item)
			}
			return out
		case map[string]any:
			out := make(map[string]any, len(val))
			for k, item := range val {
				out[k] = walk(item)
			}
			return out
		}
		return x
	}
	return walk(v)
}

func TestGoldenA01AdmissionPayload(t *testing.T) {
	samples := samplesDir(t)
	want := normalizeAttachmentPaths(readGoldenStep(t, "admission"), samples)
	registry := NewRegistry()
	res, err := registry.Call(contract.ToolValidateSubmission, map[string]any{
		"period": map[string]any{"year": 2026, "week": 44},
		"attachments": []any{
			map[string]any{"name": "销售明细.xlsx", "path": filepath.Join(samples, "销售明细.xlsx")},
			map[string]any{"name": "周会纪要_2026W44.docx", "path": filepath.Join(samples, "周会纪要_2026W44.docx")},
		},
	})
	if err != nil {
		t.Fatalf("Call 返回 error：%v", err)
	}
	if !res.OK {
		t.Fatalf("Call 失败：%s", res.Error)
	}
	got := toJSONValue(t, res.Data)
	assertJSONEqual(t, "admission", want, got)
}

func TestGoldenA01LoadPayload(t *testing.T) {
	samples := samplesDir(t)
	wantMap, ok := readGoldenStep(t, "load_data").(map[string]any)
	if !ok {
		t.Fatalf("golden load_data 不是对象")
	}
	loaded, err := LoadSales(filepath.Join(samples, "销售明细.xlsx"), t.TempDir())
	if err != nil {
		t.Fatalf("LoadSales 失败：%v", err)
	}
	if got, want := float64(loaded.Rows), wantMap["rows"]; got != want {
		t.Errorf("rows = %v，期望 %v", got, want)
	}
	gotCoverage := toJSONValue(t, loaded.Coverage)
	assertJSONEqual(t, "load_data.coverage", wantMap["coverage"], gotCoverage)
	// 文件名按内容哈希命名，前缀应与 TS 版一致
	wantBase, _ := wantMap["datasetPath"].(string)
	wantSHA := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(wantBase), "sales-"), "-v1.json")
	gotBase := filepath.Base(loaded.DatasetPath)
	if gotBase != "sales-"+wantSHA+"-v1.json" {
		t.Errorf("数据集文件名 = %s，期望 sales-%s-v1.json", gotBase, wantSHA)
	}
}
