package render

// render_report_docx 行为：幂等、文件名与 sha、数据不完整标注、选项、无字体回退。

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderReportDocxEndToEnd(t *testing.T) {
	input := loadFixture(t)
	input.IdempotencyKey = "20261106024716-7ca80b-render_document-r0-62075fb8056cb600"
	out, err := RenderReportDocx(input, Options{})
	if err != nil {
		t.Fatalf("RenderReportDocx 失败: %v", err)
	}
	if !strings.HasSuffix(out.DocxPath, ".docx") {
		t.Errorf("产物不是 .docx: %s", out.DocxPath)
	}
	if out.ChartCount != 2 {
		t.Errorf("ChartCount = %d, want 2", out.ChartCount)
	}
	if out.EstimatedPages != 2.2 {
		t.Errorf("EstimatedPages = %v, want 2.2", out.EstimatedPages)
	}
	data, err := os.ReadFile(out.DocxPath)
	if err != nil {
		t.Fatalf("读取产物失败: %v", err)
	}
	sum := sha256.Sum256(data)
	if out.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("SHA256 = %s, 与实际文件不一致", out.SHA256)
	}
	if len(out.SHA256) != 64 {
		t.Errorf("SHA256 长度 = %d, want 64", len(out.SHA256))
	}

	parts := docxParts(t, data)
	text := docxText(t, parts["word/document.xml"])
	for _, want := range []string{
		input.Report.Title,
		"本周摘要",
		"核心指标",
		"品类表现",
		"异常与原因",
		"数据质量说明",
		"下周关注事项",
		"全公司",
		"华东", "华南", "华北", "西南",
		"573,635", "-9.3%", "61.1%", "89.8%",
		"84.1%",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("docx 文本缺少 %q", want)
		}
	}
	// 两张图都是 1280×600 的非空白 PNG
	for _, name := range []string{"word/media/image1.png", "word/media/image2.png"} {
		w, h, ink := pngSize(t, parts[name])
		if w != 1280 || h != 600 || ink < 1000 {
			t.Errorf("%s 异常：%dx%d ink=%d", name, w, h, ink)
		}
	}
}

func TestRenderReportDocxIdempotent(t *testing.T) {
	input := loadFixture(t)
	input.IdempotencyKey = "task2/step-render/hashA"
	first, err := RenderReportDocx(input, Options{})
	if err != nil {
		t.Fatalf("首次渲染失败: %v", err)
	}
	stat1, err := os.Stat(first.DocxPath)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(first.DocxPath)
	if err != nil {
		t.Fatal(err)
	}

	second, err := RenderReportDocx(input, Options{})
	if err != nil {
		t.Fatalf("二次渲染失败: %v", err)
	}
	if second != first {
		t.Errorf("幂等调用返回不一致: %+v vs %+v", second, first)
	}
	stat2, err := os.Stat(first.DocxPath)
	if err != nil {
		t.Fatal(err)
	}
	if !stat1.ModTime().Equal(stat2.ModTime()) {
		t.Errorf("幂等调用不应重写文件")
	}
	after, err := os.ReadFile(first.DocxPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("幂等调用改动了文件内容")
	}
	// 只生成一个 docx
	entries, err := os.ReadDir(input.OutDir)
	if err != nil {
		t.Fatal(err)
	}
	docxCount := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".docx") {
			docxCount++
		}
	}
	if docxCount != 1 {
		t.Errorf("输出目录里 docx 数量 = %d, want 1", docxCount)
	}

	// 换一个幂等键是另一个文件
	other := input
	other.IdempotencyKey = "task2/step-render/hashB"
	third, err := RenderReportDocx(other, Options{})
	if err != nil {
		t.Fatalf("换键渲染失败: %v", err)
	}
	if third.DocxPath == first.DocxPath {
		t.Errorf("不同幂等键不应复用同一文件")
	}
	if len(third.SHA256) != 64 {
		t.Errorf("SHA256 异常: %s", third.SHA256)
	}
}

func TestRenderReportDocxFilename(t *testing.T) {
	input := loadFixture(t)
	input.IdempotencyKey = "task 7/step-render/含中文:hash"
	out, err := RenderReportDocx(input, Options{})
	if err != nil {
		t.Fatalf("RenderReportDocx 失败: %v", err)
	}
	base := filepath.Base(out.DocxPath)
	// safeName 把非字母数字（含空白、斜杠、冒号）替换成 "_"
	if !strings.HasPrefix(base, "report-task_7_step-render_") {
		t.Errorf("文件名不符合 safeName 规则: %s", base)
	}
	keySum := sha256.Sum256([]byte(input.IdempotencyKey))
	wantHash := hex.EncodeToString(keySum[:])[:12]
	if !strings.HasSuffix(base, "-"+wantHash+".docx") {
		t.Errorf("文件名哈希不是 sha256(key)[:12] 组成的: %s, want %s", base, wantHash)
	}
}

func TestRenderReportDocxPartialCoverage(t *testing.T) {
	input := loadFixture(t)
	facts := input.Facts
	facts.Period.To = "2026-11-01"
	facts.DataCoverage.To = "2026-10-29"
	input.Facts = facts
	input.IdempotencyKey = "task7/step-render/partial"
	out, err := RenderReportDocx(input, Options{})
	if err != nil {
		t.Fatalf("RenderReportDocx 失败: %v", err)
	}
	parts := docxParts(t, mustReadFile(t, out.DocxPath))
	text := docxText(t, parts["word/document.xml"])
	if !strings.Contains(text, "数据不完整") {
		t.Errorf("部分覆盖应出现“数据不完整”标注")
	}
	// 标注写明实际覆盖范围（8 月 31 日 至 10 月 29 日）与统计周期
	for _, want := range []string{"仅覆盖", "8 月 31 日", "10 月 29 日", "统计周期为 10 月 26 日 至 11 月 1 日"} {
		if !strings.Contains(text, want) {
			t.Errorf("标注缺少 %q，实际文本: %s", want, text[:min(len(text), 300)])
		}
	}
	if strings.Index(text, "数据不完整") > strings.Index(text, "本周摘要") {
		t.Errorf("标注应排在第一章正文之前")
	}
	if out.EstimatedPages < 1 || out.EstimatedPages > 3 {
		t.Errorf("估算页数超出常规周报范围: %v", out.EstimatedPages)
	}
}

func TestRenderReportDocxNoPrevWeek(t *testing.T) {
	input := loadFixture(t)
	for name, m := range input.Facts.Regions {
		m.PrevRevenueCents = nil
		m.Wow = nil
		input.Facts.Regions[name] = m
	}
	input.Facts.Company.PrevRevenueCents = nil
	input.Facts.Company.Wow = nil
	input.IdempotencyKey = "task4/step-render/w36"
	out, err := RenderReportDocx(input, Options{})
	if err != nil {
		t.Fatalf("无上周数据时渲染失败: %v", err)
	}
	if out.ChartCount != 2 {
		t.Errorf("无上周数据也要出两张图，ChartCount = %d", out.ChartCount)
	}
	parts := docxParts(t, mustReadFile(t, out.DocxPath))
	text := docxText(t, parts["word/document.xml"])
	if !strings.Contains(text, "不可比") {
		t.Errorf("无上周数据时环比应显示“不可比”")
	}
	// 仍然有表头和全公司行
	if !strings.Contains(text, "全公司") {
		t.Errorf("缺少全公司行")
	}
}

func TestRenderReportDocxEmptySections(t *testing.T) {
	input := loadFixture(t)
	report := input.Report
	report.Sections = nil
	input.Report = report
	input.IdempotencyKey = "task/step-render/empty"
	out, err := RenderReportDocx(input, Options{})
	if err != nil {
		t.Fatalf("空章节渲染失败: %v", err)
	}
	parts := docxParts(t, mustReadFile(t, out.DocxPath))
	text := docxText(t, parts["word/document.xml"])
	if !strings.Contains(text, "核心指标") {
		t.Errorf("空章节时也应插入核心指标标题与表格")
	}
	if strings.Count(text, "核心指标") != 1 {
		t.Errorf("“核心指标”应只出现一次")
	}
	if !strings.Contains(string(parts["word/document.xml"]), `<w:tbl>`) {
		t.Errorf("缺少核心指标表")
	}
	if got := strings.Count(string(parts["word/document.xml"]), `<wp:inline `); got != 2 {
		t.Errorf("图片数 = %d, want 2", got)
	}
}

func TestRenderReportDocxCustomFont(t *testing.T) {
	input := loadFixture(t)
	input.IdempotencyKey = "task/step-render/font"
	out, err := RenderReportDocx(input, Options{DocxFont: "宋体"})
	if err != nil {
		t.Fatalf("RenderReportDocx 失败: %v", err)
	}
	parts := docxParts(t, mustReadFile(t, out.DocxPath))
	doc := string(parts["word/document.xml"])
	if !strings.Contains(doc, `w:ascii="宋体"`) {
		t.Errorf("DocxFont 选项未生效")
	}
	if !strings.Contains(string(parts["word/styles.xml"]), `w:ascii="宋体"`) {
		t.Errorf("styles.xml 未使用 DocxFont")
	}
}

func TestRenderReportDocxFontFallback(t *testing.T) {
	input := loadFixture(t)
	input.IdempotencyKey = "task/step-render/no-font"
	out, err := RenderReportDocx(input, Options{ChartFontPaths: []string{"Z:/definitely-missing-font.ttf"}})
	if err != nil {
		t.Fatalf("找不到字体时应退回英文/数字而不是报错: %v", err)
	}
	parts := docxParts(t, mustReadFile(t, out.DocxPath))
	for _, name := range []string{"word/media/image1.png", "word/media/image2.png"} {
		w, h, ink := pngSize(t, parts[name])
		if w != 1280 || h != 600 || ink < 1000 {
			t.Errorf("%s 无字体回退后异常：%dx%d ink=%d", name, w, h, ink)
		}
	}
}

func TestRenderReportDocxEmptyOutDir(t *testing.T) {
	input := loadFixture(t)
	input.OutDir = ""
	if _, err := RenderReportDocx(input, Options{}); err == nil {
		t.Fatalf("outDir 为空时应报错")
	}
}

func TestRenderReportDocxNestedOutDir(t *testing.T) {
	input := loadFixture(t)
	input.IdempotencyKey = "task/step-render/nested"
	input.OutDir = filepath.Join(t.TempDir(), "a", "b", "deliverables")
	out, err := RenderReportDocx(input, Options{})
	if err != nil {
		t.Fatalf("输出目录不存在时应自动创建: %v", err)
	}
	if _, err := os.Stat(out.DocxPath); err != nil {
		t.Fatalf("产物不存在: %v", err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	return data
}
