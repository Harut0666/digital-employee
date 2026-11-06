package render

// docx 生成：手写 OOXML（archive/zip），不依赖 docx npm / cgo。
// 版式复刻 packages/tools/src/render.ts：
//   A4（11906×16838 twips）+ 2cm 页边距（1134）、正文五号（10.5pt/21 half-points）、
//   1.5 倍行距（line=360）、首行缩进 420 twips、标题居中 18pt、章节标题 14pt、
//   核心指标表 5 列（表头底色 EAF0FB、10pt）、600px 宽图片居中。

import (
	"archive/zip"
	"bytes"
	"strconv"
	"strings"
	"time"

	"digital-employee/internal/contract"
)

const (
	defaultDocxFont = "Microsoft YaHei"
	// imgWidthPx 图表显示宽度：A4 正文宽 17cm（21cm − 2×2cm），600px @96dpi ≈ 15.9cm。
	imgWidthPx = 600
	// imgHeightPx 按图表 640×300 的比例定高（Math.round(600*300/640)）。
	imgHeightPx = 281
	// emuPerPx 1px = 9525 EMU（96 dpi）。
	emuPerPx = 9525
	// warnColor 首页“数据不完整”标注的颜色。
	warnColor = "C00000"
	// contentWidthTwips A4 正文宽 = 11906 − 2×1134。
	contentWidthTwips = 11906 - 2*1134
)

// docxTime 固定时间戳：同样的输入生成同样的字节（便于幂等比对与 golden 对比）。
var docxTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// documentXML 生成 word/document.xml 正文。
// imageRIDs 是两张图的 relationship id（由 document.xml.rels 定义）。
func documentXML(report contract.RenderedReport, facts contract.WeeklyFacts, font string, imageRIDs [2]string) string {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"` +
		` xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"` +
		` xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"` +
		` xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"` +
		` xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><w:body>`)

	// 标题（HeadingLevel.TITLE，居中 18pt，段后 160）
	b.WriteString(`<w:p><w:pPr><w:pStyle w:val="Title"/><w:spacing w:after="160"/><w:jc w:val="center"/></w:pPr>`)
	b.WriteString(runXML(report.Title, font, true, 36))
	b.WriteString(`</w:p>`)

	// 数据不完整时首页醒目标注：放在标题之后、第一段正文之前，红字加粗
	if gap := coverageGap(facts); gap != nil {
		b.WriteString(`<w:p><w:pPr><w:spacing w:after="120" w:line="300" w:lineRule="auto"/></w:pPr>`)
		b.WriteString(runColorXML(warningText(facts, gap), font, true, 24, warnColor))
		b.WriteString(`</w:p>`)
	}

	core := coreSectionIndex(report)
	for i, section := range report.Sections {
		b.WriteString(sectionHeadingXML(section.Heading, font))
		for _, p := range section.Paragraphs {
			b.WriteString(bodyParagraphXML(p, font))
		}
		for _, bullet := range section.Bullets {
			b.WriteString(bulletParagraphXML(bullet, font))
		}
		if i == core {
			b.WriteString(coreBlockXML(section.Heading, facts, font, imageRIDs))
		}
	}
	if len(report.Sections) == 0 {
		b.WriteString(coreBlockXML("", facts, font, imageRIDs))
	}

	b.WriteString(`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1134" w:right="1134" w:bottom="1134" w:left="1134"` +
		` w:header="851" w:footer="992" w:gutter="0"/><w:cols w:space="425"/><w:docGrid w:linePitch="312"/></w:sectPr>`)
	b.WriteString(`</w:body></w:document>`)
	return b.String()
}

const xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

// sectionHeadingXML 章节标题（HeadingLevel.HEADING_1，14pt，段前 200 段后 80）。
func sectionHeadingXML(heading, font string) string {
	return `<w:p><w:pPr><w:pStyle w:val="Heading1"/><w:spacing w:before="200" w:after="80"/></w:pPr>` +
		runXML(heading, font, true, 28) + `</w:p>`
}

// bodyParagraphXML 正文段落：段后 80、1.5 倍行距（360）、首行缩进 420。
func bodyParagraphXML(text, font string) string {
	return `<w:p><w:pPr><w:spacing w:after="80" w:line="360" w:lineRule="auto"/><w:ind w:firstLine="420"/></w:pPr>` +
		runXML(text, font, false, 21) + `</w:p>`
}

// bulletParagraphXML 要点：项目符号（numbering.xml 的 numId=1）。
func bulletParagraphXML(text, font string) string {
	return `<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr>` +
		`<w:spacing w:after="40" w:line="340" w:lineRule="auto"/></w:pPr>` +
		runXML(text, font, false, 21) + `</w:p>`
}

// coreBlockXML 核心指标块：表格 + 两张图。章节标题本身是“核心指标”时不再插入同名标题。
func coreBlockXML(sectionHeading string, facts contract.WeeklyFacts, font string, imageRIDs [2]string) string {
	var b strings.Builder
	if !isCoreHeading(sectionHeading) {
		b.WriteString(`<w:p><w:pPr><w:spacing w:before="120" w:after="80"/></w:pPr>`)
		b.WriteString(runXML("核心指标", font, true, 24))
		b.WriteString(`</w:p>`)
	}
	b.WriteString(metricsTableXML(facts, font))
	b.WriteString(imageParagraphXML(imageRIDs[0], "各区域实收对比图", 1, imgWidthPx, imgHeightPx))
	b.WriteString(imageParagraphXML(imageRIDs[1], "近 4 周实收趋势图", 2, imgWidthPx, imgHeightPx))
	return b.String()
}

// ---- 核心指标表 ----

// metricsTableXML 5 列表格：范围 / 实收（元）/ 环比 / 毛利率 / 目标达成率。
func metricsTableXML(facts contract.WeeklyFacts, font string) string {
	widths := [5]int{20, 22, 18, 20, 20}
	head := [5]string{"范围", "实收（元）", "环比", "毛利率", "目标达成率"}

	var b strings.Builder
	b.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="5000" w:type="pct"/><w:tblBorders>` + thinBordersXML +
		`</w:tblBorders><w:tblLayout w:type="fixed"/></w:tblPr><w:tblGrid>`)
	grid := gridColsWidths(widths)
	for _, w := range grid {
		b.WriteString(`<w:gridCol w:w="` + strconv.Itoa(w) + `"/>`)
	}
	b.WriteString(`</w:tblGrid>`)

	// 表头
	b.WriteString(`<w:tr><w:trPr><w:tblHeader/></w:trPr>`)
	for i, text := range head {
		b.WriteString(tableCellXML(text, font, tableCellOpts{bold: true, header: true, right: i > 0, pct: widths[i]}))
	}
	b.WriteString(`</w:tr>`)

	// 全公司
	b.WriteString(`<w:tr>`)
	b.WriteString(tableCellXML("全公司", font, tableCellOpts{bold: true, pct: widths[0]}))
	b.WriteString(tableCellXML(fmtYuan(facts.Company.RevenueCents), font, tableCellOpts{bold: true, right: true, pct: widths[1]}))
	b.WriteString(tableCellXML(fmtWow(facts.Company.Wow), font, tableCellOpts{bold: true, right: true, pct: widths[2]}))
	b.WriteString(tableCellXML(fmtPct(facts.Company.GrossMarginRate, "—"), font, tableCellOpts{bold: true, right: true, pct: widths[3]}))
	b.WriteString(tableCellXML(fmtPct(facts.Company.Achievement, "—"), font, tableCellOpts{bold: true, right: true, pct: widths[4]}))
	b.WriteString(`</w:tr>`)

	// 各区域
	for _, name := range sortedRegionNames(facts) {
		m := facts.Regions[name]
		b.WriteString(`<w:tr>`)
		b.WriteString(tableCellXML(name, font, tableCellOpts{pct: widths[0]}))
		b.WriteString(tableCellXML(fmtYuan(m.RevenueCents), font, tableCellOpts{right: true, pct: widths[1]}))
		b.WriteString(tableCellXML(fmtWow(m.Wow), font, tableCellOpts{right: true, pct: widths[2]}))
		b.WriteString(tableCellXML(fmtPct(m.GrossMarginRate, "—"), font, tableCellOpts{right: true, pct: widths[3]}))
		b.WriteString(tableCellXML(fmtPct(m.Achievement, "—"), font, tableCellOpts{right: true, pct: widths[4]}))
		b.WriteString(`</w:tr>`)
	}
	b.WriteString(`</w:tbl>`)
	return b.String()
}

const thinBordersXML = `<w:top w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/>` +
	`<w:left w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/>` +
	`<w:bottom w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/>` +
	`<w:right w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/>` +
	`<w:insideH w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/>` +
	`<w:insideV w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/>`

// gridColsWidths 百分比列宽 → 固定布局的 twips 列宽（最后一列吃掉舍入误差）。
func gridColsWidths(pct [5]int) [5]int {
	var out [5]int
	sum := 0
	for i := 0; i < 4; i++ {
		out[i] = int(float64(contentWidthTwips)*float64(pct[i])/100 + 0.5)
		sum += out[i]
	}
	out[4] = contentWidthTwips - sum
	return out
}

type tableCellOpts struct {
	bold   bool
	header bool
	right  bool
	pct    int
}

// tableCellXML 单元格：宽度按百分比、细边框、表头底色 EAF0FB、上下 50/左右 100 twips 边距。
func tableCellXML(text, font string, opts tableCellOpts) string {
	var b strings.Builder
	b.WriteString(`<w:tc><w:tcPr><w:tcW w:w="` + strconv.Itoa(opts.pct*50) + `" w:type="pct"/>`)
	if opts.header {
		b.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="EAF0FB"/>`)
	}
	b.WriteString(`<w:tcMar><w:top w:w="50" w:type="dxa"/><w:left w:w="100" w:type="dxa"/>` +
		`<w:bottom w:w="50" w:type="dxa"/><w:right w:w="100" w:type="dxa"/></w:tcMar></w:tcPr>`)
	align := "left"
	if opts.right {
		align = "right"
	}
	b.WriteString(`<w:p><w:pPr><w:jc w:val="` + align + `"/></w:pPr>`)
	b.WriteString(runXML(text, font, opts.bold, 20))
	b.WriteString(`</w:p></w:tc>`)
	return b.String()
}

// ---- 图片 ----

// imageParagraphXML 居中图片段落（600px 宽，按图表比例定高）。
func imageParagraphXML(rid, name string, docPrID, widthPx, heightPx int) string {
	cx := strconv.Itoa(widthPx * emuPerPx)
	cy := strconv.Itoa(heightPx * emuPerPx)
	escName := xmlEsc(name)
	id := strconv.Itoa(docPrID)
	return `<w:p><w:pPr><w:spacing w:before="60" w:after="60"/><w:jc w:val="center"/></w:pPr>` +
		`<w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0">` +
		`<wp:extent cx="` + cx + `" cy="` + cy + `"/>` +
		`<wp:effectExtent l="0" t="0" r="0" b="0"/>` +
		`<wp:docPr id="` + id + `" name="` + escName + `" descr="` + escName + `"/>` +
		`<wp:cNvGraphicFramePr><a:graphicFrameLocks noChangeAspect="1"/></wp:cNvGraphicFramePr>` +
		`<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture">` +
		`<pic:pic><pic:nvPicPr><pic:cNvPr id="` + id + `" name="` + escName + `"/><pic:cNvPicPr/></pic:nvPicPr>` +
		`<pic:blipFill><a:blip r:embed="` + rid + `"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill>` +
		`<pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="` + cx + `" cy="` + cy + `"/></a:xfrm>` +
		`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr>` +
		`</pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`
}

// ---- run 与转义 ----

// runXML 一个文本 run（字体同时用于 ascii/eastAsia/hAnsi/cs）。
func runXML(text, font string, bold bool, sizeHalfPoints int) string {
	return runColorXML(text, font, bold, sizeHalfPoints, "")
}

// runColorXML 带颜色的文本 run。
func runColorXML(text, font string, bold bool, sizeHalfPoints int, colorHex string) string {
	var b strings.Builder
	b.WriteString(`<w:r><w:rPr>`)
	escFont := xmlEsc(font)
	b.WriteString(`<w:rFonts w:ascii="` + escFont + `" w:hAnsi="` + escFont + `" w:eastAsia="` + escFont + `" w:cs="` + escFont + `"/>`)
	if bold {
		b.WriteString(`<w:b/>`)
	}
	if colorHex != "" {
		b.WriteString(`<w:color w:val="` + xmlEsc(colorHex) + `"/>`)
	}
	size := strconv.Itoa(sizeHalfPoints)
	b.WriteString(`<w:sz w:val="` + size + `"/><w:szCs w:val="` + size + `"/></w:rPr>`)
	b.WriteString(textRunsXML(text))
	b.WriteString(`</w:r>`)
	return b.String()
}

// textRunsXML 文本内容：换行转 <w:br/>，其余转义后放进 <w:t>。
func textRunsXML(text string) string {
	text = sanitizeXMLText(text)
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteString(`<w:br/>`)
		}
		b.WriteString(`<w:t xml:space="preserve">` + xmlEsc(line) + `</w:t>`)
	}
	return b.String()
}

// xmlEsc 转义 XML 文本与属性值（属性里的引号也转义）。
func xmlEsc(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 16)
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sanitizeXMLText 去掉 XML 1.0 不允许的控制字符（保留 \t \n \r）。
func sanitizeXMLText(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return r
		case r < 0x20:
			return ' '
		case r == 0xFFFE || r == 0xFFFF:
			return ' '
		default:
			return r
		}
	}, s)
}

// warningText 首页“数据不完整”标注的文案（与 render.ts 一致）。
func warningText(facts contract.WeeklyFacts, gap *contract.DateRange) string {
	period := "统计周期为 " + fmtMonthDay(facts.Period.From) + " 至 " + fmtMonthDay(facts.Period.To)
	var covered string
	if gap.From == gap.To {
		covered = "仅覆盖 " + fmtMonthDay(gap.From) + " 一天"
	} else {
		covered = "仅覆盖 " + fmtMonthDay(gap.From) + " 至 " + fmtMonthDay(gap.To)
	}
	return "数据不完整：本报告数据" + covered + "（" + period + "），周报按已有数据编写。"
}

// ---- 打包 ----

func stylesXML(font string) string {
	escFont := xmlEsc(font)
	return xmlHeader +
		`<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="` + escFont + `" w:eastAsia="` + escFont +
		`" w:hAnsi="` + escFont + `" w:cs="` + escFont + `"/><w:sz w:val="21"/><w:szCs w:val="21"/></w:rPr></w:rPrDefault>` +
		`<w:pPrDefault><w:pPr><w:spacing w:after="0" w:line="240" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>` +
		`<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>` +
		`<w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/>` +
		`<w:qFormat/><w:pPr><w:jc w:val="center"/></w:pPr><w:rPr><w:b/><w:sz w:val="36"/><w:szCs w:val="36"/></w:rPr></w:style>` +
		`<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/>` +
		`<w:qFormat/><w:pPr><w:keepNext/><w:outlineLvl w:val="0"/><w:spacing w:before="200" w:after="80"/></w:pPr>` +
		`<w:rPr><w:b/><w:sz w:val="28"/><w:szCs w:val="28"/></w:rPr></w:style>` +
		`</w:styles>`
}

const numberingXML = xmlHeader +
	`<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
	`<w:abstractNum w:abstractNumId="0"><w:multiLevelType w:val="hybridMultilevel"/>` +
	`<w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="•"/><w:lvlJc w:val="left"/>` +
	`<w:pPr><w:ind w:left="425" w:hanging="425"/></w:pPr>` +
	`<w:rPr><w:rFonts w:ascii="Arial" w:hAnsi="Arial" w:hint="default"/></w:rPr></w:lvl></w:abstractNum>` +
	`<w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>` +
	`</w:numbering>`

const settingsXML = xmlHeader +
	`<w:settings xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
	`<w:zoom w:percent="100"/><w:defaultTabStop w:val="420"/>` +
	`<w:compat><w:compatSetting w:name="compatibilityMode" w:uri="http://schemas.microsoft.com/office/word" w:val="15"/></w:compat>` +
	`</w:settings>`

const contentTypesXML = xmlHeader +
	`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
	`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
	`<Default Extension="xml" ContentType="application/xml"/>` +
	`<Default Extension="png" ContentType="image/png"/>` +
	`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
	`<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>` +
	`<Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/>` +
	`<Override PartName="/word/settings.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"/>` +
	`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>` +
	`<Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>` +
	`</Types>`

const rootRelsXML = xmlHeader +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
	`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>` +
	`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>` +
	`</Relationships>`

const documentRelsXML = xmlHeader +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
	`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering" Target="numbering.xml"/>` +
	`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>` +
	`<Relationship Id="rId4" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image2.png"/>` +
	`<Relationship Id="rId5" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/settings" Target="settings.xml"/>` +
	`</Relationships>`

func coreXML(title string) string {
	stamp := docxTime.Format("2006-01-02T15:04:05Z")
	return xmlHeader +
		`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties"` +
		` xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/"` +
		` xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<dc:title>` + xmlEsc(title) + `</dc:title>` +
		`<dc:creator>数字员工</dc:creator>` +
		`<cp:lastModifiedBy>数字员工</cp:lastModifiedBy>` +
		`<dcterms:created xsi:type="dcterms:W3CDTF">` + stamp + `</dcterms:created>` +
		`<dcterms:modified xsi:type="dcterms:W3CDTF">` + stamp + `</dcterms:modified>` +
		`</cp:coreProperties>`
}

const appXML = xmlHeader +
	`<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties"` +
	` xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">` +
	`<Application>Digital Employee</Application><DocSecurity>0</DocSecurity><ScaleCrop>false</ScaleCrop>` +
	`</Properties>`

type docxPart struct {
	name string
	data []byte
}

// buildDocx 打包完整的 .docx（OOXML 最小完整包 + 两张 PNG）。
func buildDocx(report contract.RenderedReport, facts contract.WeeklyFacts, font string, barPNG, trendPNG []byte) ([]byte, error) {
	document := documentXML(report, facts, font, [2]string{"rId3", "rId4"})
	parts := []docxPart{
		{"[Content_Types].xml", []byte(contentTypesXML)},
		{"_rels/.rels", []byte(rootRelsXML)},
		{"docProps/core.xml", []byte(coreXML(report.Title))},
		{"docProps/app.xml", []byte(appXML)},
		{"word/document.xml", []byte(document)},
		{"word/styles.xml", []byte(stylesXML(font))},
		{"word/numbering.xml", []byte(numberingXML)},
		{"word/settings.xml", []byte(settingsXML)},
		{"word/_rels/document.xml.rels", []byte(documentRelsXML)},
		{"word/media/image1.png", barPNG},
		{"word/media/image2.png", trendPNG},
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range parts {
		h := &zip.FileHeader{Name: p.name, Method: zip.Deflate, Modified: docxTime}
		h.SetMode(0o644)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(p.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
