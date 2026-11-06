package render

// 临时技术路线验证：生成 docx + 两张 PNG 到指定目录，供外部工具（python-docx）检查。
// 验证完删除本文件。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"digital-employee/internal/contract"
)

func TestSpikeArtifacts(t *testing.T) {
	outDir := os.Getenv("RENDER_SPIKE_DIR")
	if outDir == "" {
		t.Skip("设置 RENDER_SPIKE_DIR=<dir> 后运行，用于生成人工比对的 docx/PNG 产物")
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "a01_render_input.json"))
	if err != nil {
		t.Fatal(err)
	}
	var input contract.RenderReportInput
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	input.OutDir = outDir
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = "spike"
	}
	out, err := RenderReportDocx(input, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("docx: %s", out.DocxPath)
	t.Logf("sha=%s pages=%.1f charts=%d", out.SHA256, out.EstimatedPages, out.ChartCount)

	// 单独把两张图也存下来看
	vf := resolveChartFont(nil)
	t.Logf("chart font loaded: %v", vf != nil)
	bar, err := renderRegionBarPNG(input.Facts, vf)
	if err != nil {
		t.Fatal(err)
	}
	trend, err := renderTrendLinePNG(input.Facts, vf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "bar.png"), bar, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "trend.png"), trend, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("pngs in %s", outDir)
}
