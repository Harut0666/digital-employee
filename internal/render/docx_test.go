package render

// docx 结构断言：OOXML 合法、版式参数、表格 5 列、2 张图、图片尺寸、文档属性。

import (
	"strings"
	"testing"

	"digital-employee/internal/contract"
)

func TestDocumentXMLStructure(t *testing.T) {
	input := loadFixture(t)
	doc := documentXML(input.Report, input.Facts, defaultDocxFont, [2]string{"rId3", "rId4"})
	assertWellFormedXML(t, "document.xml", []byte(doc))

	// 标题、章节、核心指标表与图片
	if !strings.Contains(doc, `<w:pStyle w:val="Title"/>`) {
		t.Errorf("缺少 Title 段落样式")
	}
	if !strings.Contains(doc, input.Report.Title) {
		t.Errorf("缺少报告标题 %q", input.Report.Title)
	}
	if got := strings.Count(doc, `<w:tbl>`); got != 1 {
		t.Errorf("表格数 = %d, want 1", got)
	}
	if got := strings.Count(doc, `<w:gridCol `); got != 5 {
		t.Errorf("表格列数 = %d, want 5", got)
	}
	if got := strings.Count(doc, `<wp:inline `); got != 2 {
		t.Errorf("图片数 = %d, want 2", got)
	}
	for _, rid := range []string{`r:embed="rId3"`, `r:embed="rId4"`} {
		if !strings.Contains(doc, rid) {
			t.Errorf("缺少图片关系 %s", rid)
		}
	}
	// 600px 宽、按 640×300 比例定高（281px）
	if !strings.Contains(doc, `wp:extent cx="5715000" cy="2676525"`) {
		t.Errorf("图片显示尺寸不是 600×281px（EMU 5715000×2676525）")
	}

	// 表头 5 个单元格都有 EAF0FB 底色
	if got := strings.Count(doc, `w:fill="EAF0FB"`); got != 5 {
		t.Errorf("表头底色单元格数 = %d, want 5", got)
	}
	// 正文首行缩进 420 twips、1.5 倍行距 line=360
	if !strings.Contains(doc, `w:firstLine="420"`) {
		t.Errorf("缺少正文首行缩进 420")
	}
	if !strings.Contains(doc, `w:line="360"`) {
		t.Errorf("缺少 1.5 倍行距 line=360")
	}
	if !strings.Contains(doc, `w:line="340"`) {
		t.Errorf("缺少要点行距 line=340")
	}
	// A4 + 2cm 页边距
	if !strings.Contains(doc, `w:pgSz w:w="11906" w:h="16838"`) {
		t.Errorf("缺少 A4 页面尺寸")
	}
	if !strings.Contains(doc, `w:top="1134" w:right="1134" w:bottom="1134" w:left="1134"`) {
		t.Errorf("缺少 2cm（1134 twips）页边距")
	}
	// 正文字体与字号（10.5pt = 21 half-points）：标题 18pt、章节 14pt、表格 10pt
	if !strings.Contains(doc, `w:ascii="Microsoft YaHei"`) {
		t.Errorf("缺少 Microsoft YaHei 字体")
	}
	for _, size := range []string{`w:sz w:val="36"`, `w:sz w:val="28"`, `w:sz w:val="21"`, `w:sz w:val="20"`} {
		if !strings.Contains(doc, size) {
			t.Errorf("缺少字号 %s", size)
		}
	}
	// 要点使用项目符号
	if !strings.Contains(doc, `<w:numPr>`) {
		t.Errorf("缺少项目符号 numPr")
	}
	// 标题行重复
	if !strings.Contains(doc, `<w:tblHeader/>`) {
		t.Errorf("表头行缺少 tblHeader")
	}
}

func TestDocumentXMLSectionOrderAndCoreBlock(t *testing.T) {
	input := loadFixture(t)
	doc := documentXML(input.Report, input.Facts, defaultDocxFont, [2]string{"rId3", "rId4"})

	summary := strings.Index(doc, "本周摘要")
	metrics := strings.Index(doc, "核心指标")
	table := strings.Index(doc, `<w:tbl>`)
	categories := strings.Index(doc, "品类表现")
	if summary < 0 || metrics < 0 || table < 0 || categories < 0 {
		t.Fatalf("章节或表格缺失 summary=%d metrics=%d table=%d categories=%d", summary, metrics, table, categories)
	}
	if !(summary < metrics && metrics < table && table < categories) {
		t.Errorf("章节与核心指标块顺序不对：summary=%d metrics=%d table=%d categories=%d", summary, metrics, table, categories)
	}
	// 章节标题本身是“核心指标”时不再插入同名标题（只出现一次）
	if got := strings.Count(doc, "核心指标"); got != 1 {
		t.Errorf("“核心指标”出现 %d 次, want 1", got)
	}
}

func TestDocumentXMLCoreHeadingInserted(t *testing.T) {
	input := loadFixture(t)
	// 去掉“核心指标”章节后，渲染器应插入表格标题（只出现一次）
	report := input.Report
	report.Sections = nil
	for _, s := range input.Report.Sections {
		if s.ID == "metrics" || s.Heading == "核心指标" {
			continue
		}
		report.Sections = append(report.Sections, s)
	}
	doc := documentXML(report, input.Facts, defaultDocxFont, [2]string{"rId3", "rId4"})
	if got := strings.Count(doc, "核心指标"); got != 1 {
		t.Errorf("没有核心指标章节时应插入一次表格标题，实际出现 %d 次", got)
	}
	if !strings.Contains(doc, `<w:tbl>`) {
		t.Errorf("缺少核心指标表")
	}
}

func TestDocumentXMLWarning(t *testing.T) {
	input := loadFixture(t)
	facts := input.Facts
	facts.Period.To = "2026-11-01"
	facts.DataCoverage.To = "2026-10-29"
	doc := documentXML(input.Report, facts, defaultDocxFont, [2]string{"rId3", "rId4"})
	assertWellFormedXML(t, "document.xml(warning)", []byte(doc))

	for _, want := range []string{"数据不完整", "10 月 29 日", "10 月 26 日", "11 月 1 日", "周报按已有数据编写", `w:color w:val="C00000"`} {
		if !strings.Contains(doc, want) {
			t.Errorf("数据不完整标注缺少 %q", want)
		}
	}
	// 标注在标题之后、第一段正文之前
	title := strings.Index(doc, input.Report.Title)
	warning := strings.Index(doc, "数据不完整")
	summary := strings.Index(doc, input.Report.Sections[0].Heading)
	if !(title < warning && warning < summary) {
		t.Errorf("标注顺序不对 title=%d warning=%d summary=%d", title, warning, summary)
	}
}

func TestDocumentXMLNoWarningWhenComplete(t *testing.T) {
	input := loadFixture(t)
	doc := documentXML(input.Report, input.Facts, defaultDocxFont, [2]string{"rId3", "rId4"})
	if strings.Contains(doc, "数据不完整") {
		t.Errorf("覆盖完整时不应出现数据不完整标注")
	}
}

func TestDocumentXMLTableNumbers(t *testing.T) {
	input := loadFixture(t)
	doc := documentXML(input.Report, input.Facts, defaultDocxFont, [2]string{"rId3", "rId4"})
	// A-01 expect.checks 里的核心指标数字必须能在表格里读出来
	wants := []string{
		"573,635", "-9.3%", "61.1%", "89.8%", // 全公司
		"197,618", "-12.0%", "84.1%", // 华东
		"177,306", "-12.7%", "92.3%", // 华南
		"119,712", "-3.3%", "92.1%", // 华北
		"78,998", "-2.6%", "96.3%", // 西南
	}
	for _, want := range wants {
		if !strings.Contains(doc, want) {
			t.Errorf("表格里缺少 %q", want)
		}
	}
	// 区域行顺序 = 门店维表首次出现顺序（与 TS 一致）
	order := []string{"全公司", "华东", "华南", "华北", "西南"}
	last := -1
	for _, name := range order {
		idx := strings.Index(doc, ">"+name+"<")
		if idx < 0 {
			t.Fatalf("表格里找不到行标签 %q", name)
		}
		if idx <= last {
			t.Errorf("表格行顺序不对：%q 出现在错误位置", name)
		}
		last = idx
	}
}

func TestBuildDocxPackage(t *testing.T) {
	input := loadFixture(t)
	bar, err := renderRegionBarPNG(input.Facts, nil)
	if err != nil {
		t.Fatalf("绘制柱状图失败: %v", err)
	}
	trend, err := renderTrendLinePNG(input.Facts, nil)
	if err != nil {
		t.Fatalf("绘制趋势图失败: %v", err)
	}
	data, err := buildDocx(input.Report, input.Facts, defaultDocxFont, bar, trend)
	if err != nil {
		t.Fatalf("打包 docx 失败: %v", err)
	}
	parts := docxParts(t, data)

	required := []string{
		"[Content_Types].xml", "_rels/.rels", "docProps/core.xml", "docProps/app.xml",
		"word/document.xml", "word/styles.xml", "word/numbering.xml", "word/settings.xml",
		"word/_rels/document.xml.rels", "word/media/image1.png", "word/media/image2.png",
	}
	for _, name := range required {
		if _, ok := parts[name]; !ok {
			t.Errorf("docx 缺少部件 %s", name)
		}
	}
	for name, body := range parts {
		if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".rels") {
			assertWellFormedXML(t, name, body)
		}
	}

	// 每个关系目标都真实存在于包里（Word 打不开的常见原因就是断链）
	for _, target := range readXMLAttrValue(t, parts["word/_rels/document.xml.rels"], "Target") {
		if _, ok := parts["word/"+target]; !ok {
			t.Errorf("关系目标不存在: word/%s", target)
		}
	}
	for _, target := range readXMLAttrValue(t, parts["_rels/.rels"], "Target") {
		if _, ok := parts[target]; !ok {
			t.Errorf("包里缺少根关系目标: %s", target)
		}
	}

	// 文档属性：creator/title（render.ts 里的 creator: "数字员工"）
	core := string(parts["docProps/core.xml"])
	if !strings.Contains(core, "<dc:creator>数字员工</dc:creator>") {
		t.Errorf("core.xml 缺少 creator=数字员工")
	}
	if !strings.Contains(core, "<dc:title>"+input.Report.Title+"</dc:title>") {
		t.Errorf("core.xml 缺少 title")
	}

	// 两张 PNG 都是 2x 输出（1280×600）
	for _, name := range []string{"word/media/image1.png", "word/media/image2.png"} {
		w, h, ink := pngSize(t, parts[name])
		if w != 1280 || h != 600 {
			t.Errorf("%s 尺寸 = %dx%d, want 1280x600", name, w, h)
		}
		if ink < 1000 {
			t.Errorf("%s 几乎是空白图（非白像素 %d）", name, ink)
		}
	}
}

func TestDocumentXMLSanitizesControlChars(t *testing.T) {
	input := loadFixture(t)
	report := input.Report
	report.Sections = append([]contract.RenderedSection{}, input.Report.Sections...)
	report.Sections[0].Paragraphs = append(append([]string{}, report.Sections[0].Paragraphs...), "含控制字符\x00\x01的段落")
	doc := documentXML(report, input.Facts, defaultDocxFont, [2]string{"rId3", "rId4"})
	assertWellFormedXML(t, "document.xml(control chars)", []byte(doc))
}
