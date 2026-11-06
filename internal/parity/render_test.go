package parity

// A-01 渲染对拍：用 golden 的 DraftReport + WeeklyFacts 走 Go 版槽位渲染 + docx 生成，
// 再把产出的 docx 与 TS 版 golden docx 的文本内容对比。
//
// 运行：go test ./internal/parity -run TestGoldenRender -v

import (
	"archive/zip"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"digital-employee/internal/contract"
	"digital-employee/internal/qc"
	"digital-employee/internal/render"
)

// docxText 按文档顺序拼接 docx 正文里的文字（w:t）。
func docxText(t *testing.T, path string) (string, int) {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("打开 docx %s: %v", path, err)
	}
	defer zr.Close()

	media := 0
	var doc []byte
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "word/media/") {
			media++
		}
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("读 document.xml: %v", err)
			}
			buf := new(strings.Builder)
			tmp := make([]byte, 32*1024)
			for {
				n, err := rc.Read(tmp)
				if n > 0 {
					buf.Write(tmp[:n])
				}
				if err != nil {
					break
				}
			}
			rc.Close()
			doc = []byte(buf.String())
		}
	}
	if doc == nil {
		t.Fatalf("%s 里没有 word/document.xml", path)
	}

	dec := xml.NewDecoder(strings.NewReader(string(doc)))
	var out strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch el := tok.(type) {
		case xml.StartElement:
			if el.Name.Local == "t" {
				var text string
				if err := dec.DecodeElement(&text, &el); err == nil {
					out.WriteString(text)
					out.WriteString("\n")
				}
			}
		case xml.CharData:
			// 忽略样式表等非 w:t 文本
		}
	}
	return out.String(), media
}

// renderDraft 把 DraftReport 的槽位渲染成 RenderedReport（等价引擎里的 $rendered 步骤输入）。
func renderDraft(t *testing.T, draft contract.DraftReport, facts contract.WeeklyFacts) contract.RenderedReport {
	t.Helper()
	slots := qc.NewSlots(facts)
	out := contract.RenderedReport{Title: slots.Render(draft.Title).Text}
	for _, s := range draft.Sections {
		rs := contract.RenderedSection{ID: s.ID, Heading: slots.Render(s.Heading).Text}
		for _, p := range s.Paragraphs {
			rs.Paragraphs = append(rs.Paragraphs, slots.Render(p).Text)
		}
		for _, b := range s.Bullets {
			rs.Bullets = append(rs.Bullets, slots.Render(b).Text)
		}
		out.Sections = append(out.Sections, rs)
	}
	return out
}

func TestGoldenRender(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(goldenDir, "steps", "write_report.json"))
	if err != nil {
		t.Fatalf("读 write_report golden: %v", err)
	}
	var draft contract.DraftReport
	if err := json.Unmarshal(raw, &draft); err != nil {
		t.Fatalf("解析 DraftReport: %v", err)
	}
	facts := loadGolden[contract.WeeklyFacts](t, "compute_metrics")

	rendered := renderDraft(t, draft, facts)
	// 渲染后不应残留未解析的槽位
	for _, s := range rendered.Sections {
		for _, p := range s.Paragraphs {
			if strings.Contains(p, "{{") {
				t.Errorf("章节 %s 的段落里残留未解析槽位: %s", s.ID, p)
			}
		}
	}

	outDir := t.TempDir()
	got, err := render.RenderReportDocx(contract.RenderReportInput{
		Report:         rendered,
		Facts:          facts,
		OutDir:         outDir,
		IdempotencyKey: "parity-a01-render",
	}, render.Options{})
	if err != nil {
		t.Fatalf("RenderReportDocx: %v", err)
	}

	goText, goMedia := docxText(t, got.DocxPath)
	goldenText, goldenMedia := docxText(t, filepath.Join(goldenDir, "report.docx"))

	if goMedia != 2 {
		t.Errorf("Go docx 图片数=%d，期望 2", goMedia)
	}
	if goldenMedia != 2 {
		t.Errorf("golden docx 图片数=%d，期望 2（golden 文件可能又拷错了）", goldenMedia)
	}

	// 1) 章节标题顺序一致
	var goldenHeadings, goHeadings []string
	for _, h := range []string{"本周摘要", "核心指标", "品类表现", "异常与原因", "数据质量说明", "下周关注事项"} {
		if strings.Contains(goldenText, h) {
			goldenHeadings = append(goldenHeadings, h)
		}
		if strings.Contains(goText, h) {
			goHeadings = append(goHeadings, h)
		}
	}
	if strings.Join(goldenHeadings, "→") != strings.Join(goHeadings, "→") {
		t.Errorf("章节标题不一致：golden=%v go=%v", goldenHeadings, goHeadings)
	}

	// 2) A-01 业务数字必须都能在 docx 里读到（与 golden 相同的表格口径）
	numbers := []string{"573,635", "-9.3%", "61.1%", "89.8%", "197,618", "-12.0%", "84.1%",
		"177,306", "-12.7%", "92.3%", "119,712", "-3.3%", "92.1%", "78,998", "-2.6%", "96.3%"}
	for _, n := range numbers {
		if !strings.Contains(goText, n) {
			t.Errorf("Go docx 缺少数字 %s", n)
		}
		if !strings.Contains(goldenText, n) {
			t.Logf("注意：golden docx 里也没有数字 %s（口径可能不同）", n)
		}
	}

	// 3) 与 golden 的文本差异（只报 golden 有、Go 没有的句子，最多 15 条）
	var missing []string
	for _, line := range strings.Split(goldenText, "\n") {
		line = strings.TrimSpace(line)
		if len([]rune(line)) < 8 {
			continue
		}
		if !strings.Contains(goText, line) {
			missing = append(missing, line)
			if len(missing) >= 15 {
				break
			}
		}
	}
	if len(missing) > 0 {
		t.Logf("golden 有而 Go 版没有的文本片段 %d 条（可能是叙述措辞差异，非阻塞）：\n  - %s",
			len(missing), strings.Join(missing, "\n  - "))
	}

	t.Logf("Go docx: %s（%.1f KB，estimatedPages=%.1f，charts=%d）", got.DocxPath,
		float64(fileSize(t, got.DocxPath))/1024, got.EstimatedPages, got.ChartCount)
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}
