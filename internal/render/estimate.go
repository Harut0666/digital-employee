package render

// 页数估算：与 packages/tools/src/render.ts 的 estimatePages 逐行一致。
// A4、2cm 页边距、五号字（10.5pt）、1.5 倍行距。

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"digital-employee/internal/contract"
)

const (
	// pageLines 每页可排的正文行数。
	pageLines = 46
	// charsPerLine 每行汉字数。
	charsPerLine = 45
	// chartLines 一张图（含图注）折合的行数。
	chartLines = 14
	// warnLines 首页“数据不完整”标注折合的行数。
	warnLines = 2
)

var (
	// coreHeadingRe 核心指标章节：标题或 id 含“指标”/“metrics”（忽略大小写）。
	coreHeadingRe = regexp.MustCompile(`指标|(?i:metrics)`)
	// whitespaceRe 对应 JS 的 /\s+/g。
	whitespaceRe = regexp.MustCompile(`\s+`)
)

// EstimatePages 按版式估算页数（保留一位小数、向上取）。
// 质检按“3 页 × 90%”判断，取整会让实际上限变成 2 页，所以必须带小数。
func EstimatePages(report contract.RenderedReport, facts contract.WeeklyFacts) float64 {
	lines := 3.0 // 标题与副标题
	if coverageGap(facts) != nil {
		lines += warnLines // 首页“数据不完整”标注
	}
	tableRows := 2 + len(facts.Regions) // 表头 + 全公司 + 各区域
	core := coreSectionIndex(report)
	for i, s := range report.Sections {
		lines += 2.2 + 0.3 // 章节标题（三号/加粗）与段前后间距
		for _, p := range s.Paragraphs {
			lines += linesOf(p, 2) + 0.4
		}
		for _, b := range s.Bullets {
			lines += linesOf(b, 2) + 0.2
		}
		if i == core {
			lines += float64(tableRows)*1.6 + 2 + 2*chartLines
		}
	}
	if len(report.Sections) == 0 {
		lines += float64(tableRows)*1.6 + 2 + 2*chartLines
	}
	if lines < 1 {
		lines = 1
	}
	// 保留一位小数（向上取）
	return math.Max(1, math.Ceil((lines/float64(pageLines))*10)/10)
}

// coverageGap 数据不完整：数据覆盖范围没盖住整个统计周期。
// 只比较日期，不比较时间；覆盖范围超出周期不算不完整。完整时返回 nil。
func coverageGap(facts contract.WeeklyFacts) *contract.DateRange {
	coverage, period := facts.DataCoverage, facts.Period
	if coverage.From > period.From || coverage.To < period.To {
		return &contract.DateRange{From: coverage.From, To: coverage.To}
	}
	return nil
}

// textWidth 一个字符宽：非 CJK（码点 < 0x2e80）记 0.5，其余记 1。
func textWidth(s string) float64 {
	w := 0.0
	for _, r := range s {
		if r < 0x2e80 {
			w += 0.5
		} else {
			w++
		}
	}
	return w
}

// linesOf 一段文字折合的行数（至少 1 行）。
func linesOf(s string, indent float64) float64 {
	return math.Max(1, math.Ceil((textWidth(s)+indent)/charsPerLine))
}

// coreSectionIndex 核心指标表与图表放在哪个章节之后：优先标题含“指标”的章节，否则第一章。
func coreSectionIndex(report contract.RenderedReport) int {
	for i, s := range report.Sections {
		if coreHeadingRe.MatchString(s.Heading + s.ID) {
			return i
		}
	}
	return 0
}

// isCoreHeading 章节标题本身是“核心指标”时，不再插入同名的表格标题。
func isCoreHeading(heading string) bool {
	return whitespaceRe.ReplaceAllString(heading, "") == "核心指标"
}

// fmtMonthDay ISO 日期转“10 月 29 日”（与提示词里的日期写法一致）。
func fmtMonthDay(iso string) string {
	parts := strings.Split(iso, "-")
	if len(parts) < 3 {
		return iso
	}
	month, err1 := strconv.Atoi(strings.TrimSpace(parts[1]))
	day, err2 := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err1 != nil || err2 != nil {
		return iso
	}
	return strconv.Itoa(month) + " 月 " + strconv.Itoa(day) + " 日"
}
