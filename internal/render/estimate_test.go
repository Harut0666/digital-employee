package render

// 格式与页数估算：与 packages/tools/src/{format,render}.ts 对拍。

import (
	"math"
	"testing"

	"digital-employee/internal/contract"
)

func ptrF(v float64) *float64 { return &v }
func ptrI(v int64) *int64     { return &v }

func TestFmtYuan(t *testing.T) {
	cases := []struct {
		cents int64
		want  string
	}{
		{0, "0"},
		{99, "1"},
		{100, "1"},
		{57363487, "573,635"},
		{19761793, "197,618"},
		{-260, "-3"},
		{-57363487, "-573,635"},
		{123456789, "1,234,568"},
	}
	for _, c := range cases {
		if got := fmtYuan(c.cents); got != c.want {
			t.Errorf("fmtYuan(%d) = %q, want %q", c.cents, got, c.want)
		}
	}
}

func TestFmtPct(t *testing.T) {
	cases := []struct {
		in   *float64
		want string
	}{
		{nil, "—"},
		{ptrF(0.6108412656294762), "61.1%"},
		{ptrF(0.8409273617021277), "84.1%"},
		{ptrF(-0.11980637675380817), "-12.0%"},
		{ptrF(0), "0.0%"},
	}
	for _, c := range cases {
		if got := fmtPct(c.in, "—"); got != c.want {
			t.Errorf("fmtPct(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFmtWow(t *testing.T) {
	cases := []struct {
		in   *float64
		want string
	}{
		{nil, "不可比"},
		{ptrF(-0.09304698021976021), "-9.3%"},
		{ptrF(0.0123), "+1.2%"},
		{ptrF(0), "0.0%"},
		{ptrF(0.0004), "0.0%"},
	}
	for _, c := range cases {
		if got := fmtWow(c.in); got != c.want {
			t.Errorf("fmtWow(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWan(t *testing.T) {
	cases := map[float64]string{
		0:      "0",
		9999:   "9999",
		20000:  "2万",
		224516: "22.5万",
		700000: "70万",
		250000: "25万",
	}
	for in, want := range cases {
		if got := wan(in); got != want {
			t.Errorf("wan(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestTextWidthAndLines(t *testing.T) {
	if got := textWidth("abc"); got != 1.5 {
		t.Errorf("textWidth(abc) = %v, want 1.5", got)
	}
	if got := textWidth("中文"); got != 2 {
		t.Errorf("textWidth(中文) = %v, want 2", got)
	}
	if got := textWidth("a中"); got != 1.5 {
		t.Errorf("textWidth(a中) = %v, want 1.5", got)
	}
	// 45 个汉字一行，46 个两行；空串至少 1 行
	if got := linesOf("", 0); got != 1 {
		t.Errorf("linesOf(\"\") = %v, want 1", got)
	}
	if got := linesOf(rep("汉", 45), 0); got != 1 {
		t.Errorf("linesOf(45 汉字) = %v, want 1", got)
	}
	if got := linesOf(rep("汉", 46), 0); got != 2 {
		t.Errorf("linesOf(46 汉字) = %v, want 2", got)
	}
	// 缩进 2 也计入宽度
	if got := linesOf(rep("汉", 44), 2); got != 2 {
		t.Errorf("linesOf(44 汉字 + 缩进 2) = %v, want 2", got)
	}
}

func rep(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

func TestEstimatePagesFixture(t *testing.T) {
	input := loadFixture(t)
	pages := EstimatePages(input.Report, input.Facts)
	// Python 逐行复算 TS 算法得到 98.0 行 → 2.2 页
	if pages != 2.2 {
		t.Fatalf("EstimatePages(A-01 真实 fixture) = %v, want 2.2", pages)
	}
	// 必须保留一位小数（质检按 3 页 × 90% 判断）
	if math.Round(pages*10)/10 != pages {
		t.Errorf("估算页数应保留一位小数，得到 %v", pages)
	}
}

func TestEstimatePagesGrowsWithContent(t *testing.T) {
	input := loadFixture(t)
	small := EstimatePages(input.Report, input.Facts)

	bigReport := input.Report
	bigReport.Sections = append([]contract.RenderedSection{}, input.Report.Sections...)
	extra := make([]string, 0, 80)
	for i := 0; i < 80; i++ {
		extra = append(extra, rep("数据口径说明", 12))
	}
	bigReport.Sections[0].Paragraphs = append(append([]string{}, bigReport.Sections[0].Paragraphs...), extra...)
	big := EstimatePages(bigReport, input.Facts)
	if big <= small {
		t.Errorf("内容越多页数应越多：small=%v big=%v", small, big)
	}
}

func TestEstimatePagesEmptySections(t *testing.T) {
	input := loadFixture(t)
	report := contract.RenderedReport{Title: input.Report.Title}
	pages := EstimatePages(report, input.Facts)
	if pages < 1 {
		t.Errorf("页数下限应为 1，得到 %v", pages)
	}
	// 无章节时核心指标表与两张图仍计入估算：把区域数放大后页数应明显变多
	wide := input.Facts
	wide.Regions = make(map[string]contract.Metric, 40)
	for i := 0; i < 40; i++ {
		wide.Regions[rep("区", 1)+string(rune('A'+i))] = contract.Metric{}
	}
	widePages := EstimatePages(report, wide)
	if widePages <= pages {
		t.Errorf("区域越多表格越高，页数应增加：before=%v after=%v", pages, widePages)
	}
}

func TestEstimatePagesCoverageGap(t *testing.T) {
	input := loadFixture(t)
	if coverageGap(input.Facts) != nil {
		t.Fatalf("A-01 fixture 的覆盖范围本应完整")
	}
	partial := input.Facts
	partial.Period.To = "2026-11-08"
	gap := coverageGap(partial)
	if gap == nil || gap.From != partial.DataCoverage.From || gap.To != partial.DataCoverage.To {
		t.Fatalf("coverageGap = %+v, want 覆盖范围", gap)
	}
	// 覆盖范围超出周期不算不完整
	beyond := input.Facts
	beyond.DataCoverage.From = "2026-08-01"
	beyond.DataCoverage.To = "2026-12-31"
	if coverageGap(beyond) != nil {
		t.Errorf("覆盖范围超出周期不应视为不完整")
	}
}

func TestCoreSectionIndex(t *testing.T) {
	report := contract.RenderedReport{Sections: []contract.RenderedSection{
		{ID: "summary", Heading: "本周摘要"},
		{ID: "metrics", Heading: "核心指标"},
	}}
	if got := coreSectionIndex(report); got != 1 {
		t.Errorf("coreSectionIndex = %d, want 1", got)
	}
	byID := contract.RenderedReport{Sections: []contract.RenderedSection{
		{ID: "summary", Heading: "本周摘要"},
		{ID: "metrics_overview", Heading: "经营情况"},
	}}
	if got := coreSectionIndex(byID); got != 1 {
		t.Errorf("按 id 里的 metrics 应命中，得到 %d", got)
	}
	none := contract.RenderedReport{Sections: []contract.RenderedSection{{ID: "summary", Heading: "本周摘要"}}}
	if got := coreSectionIndex(none); got != 0 {
		t.Errorf("没有指标章节时回退第一章，得到 %d", got)
	}
	if !isCoreHeading(" 核心指标 ") {
		t.Errorf("空白字符不应影响 isCoreHeading")
	}
	if isCoreHeading("核心指标与图表") {
		t.Errorf("isCoreHeading 只认完全等于“核心指标”")
	}
}

func TestFmtMonthDay(t *testing.T) {
	if got := fmtMonthDay("2026-10-29"); got != "10 月 29 日" {
		t.Errorf("fmtMonthDay = %q", got)
	}
	if got := fmtMonthDay("2026-11-01"); got != "11 月 1 日" {
		t.Errorf("fmtMonthDay = %q", got)
	}
	if got := fmtMonthDay("bad"); got != "bad" {
		t.Errorf("无法解析时应原样返回，得到 %q", got)
	}
}

func TestRegionOrderFromStores(t *testing.T) {
	input := loadFixture(t)
	order := regionOrder(input.Facts)
	want := []string{"华东", "华南", "华北", "西南"}
	if len(order) != len(want) {
		t.Fatalf("regionOrder = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("regionOrder = %v, want %v", order, want)
		}
	}

	// 门店维表缺失时按名称排序兜底（确定性）
	noStores := input.Facts
	noStores.Stores = nil
	fallback := regionOrder(noStores)
	if len(fallback) != len(want) {
		t.Fatalf("兜底顺序长度 = %d, want %d", len(fallback), len(want))
	}
	for i := 1; i < len(fallback); i++ {
		if fallback[i-1] >= fallback[i] {
			t.Errorf("兜底顺序应稳定排序，得到 %v", fallback)
		}
	}
}

func TestNiceInterval(t *testing.T) {
	cases := map[float64]float64{
		0:       1,
		100:     50,
		224516:  50000,
		686010:  100000,
		1000000: 500000,
	}
	for in, want := range cases {
		if got := niceInterval(in); got != want {
			t.Errorf("niceInterval(%v) = %v, want %v", in, got, want)
		}
	}
}
