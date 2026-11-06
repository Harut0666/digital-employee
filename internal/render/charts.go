package render

// 两张图表：纯 Go 绘制（image.RGBA + image/png），复刻 packages/tools/src/charts.ts
// 的 regionBarOption / trendLineOption：
//   图 1：各区域本周与上周实收柱状对比（柱顶数值标签）
//   图 2：近 4 周全公司与各区域实收折线趋势（全公司虚线）
// 逻辑尺寸 640×300，2x 输出（1280×600）；版式参数取自 ECharts 的 grid/legend/title。

import (
	"image/color"
	"math"
	"sort"
	"strconv"

	"digital-employee/internal/contract"
)

var (
	textColor  = color.RGBA{0x33, 0x33, 0x33, 0xff}
	axisColor  = color.RGBA{0x6e, 0x70, 0x79, 0xff}
	splitColor = color.RGBA{0xe0, 0xe6, 0xf1, 0xff}
	prevBarCol = color.RGBA{0xb7, 0xc4, 0xd9, 0xff}
	companyCol = color.RGBA{0x33, 0x33, 0x33, 0xff}
	// seriesColors 与 charts.ts 的 COLORS 一致。
	seriesColors = []color.RGBA{
		{0x2f, 0x6f, 0xdb, 0xff},
		{0xe0, 0x87, 0x3b, 0xff},
		{0x3a, 0x9d, 0x6e, 0xff},
		{0xa0, 0x5b, 0xd0, 0xff},
		{0xd2, 0x50, 0x6b, 0xff},
	}
)

// 版式常量（ECharts 逻辑坐标）：grid {left:56,right:44,top:56,bottom:36}，legend.top=28。
const (
	plotLeft   = 56.0
	plotRight  = 44.0
	plotTop    = 56.0
	plotBottom = 36.0
	legendTop  = 28.0
)

// seriesColor 第 i 条系列的颜色（循环使用）。
func seriesColor(i int) color.RGBA { return seriesColors[i%len(seriesColors)] }

// regionOrder 区域展示顺序：取门店维表（有序数组）里 region 首次出现的顺序，
// 与 TS 版 compute.ts 的 regionOrder 一致；维表未覆盖的区域按名称排序兜底。
// 注意不要遍历 facts.Regions（map 无序），否则图例/配色/表格行序会随机。
func regionOrder(facts contract.WeeklyFacts) []string {
	seen := make(map[string]bool, len(facts.Regions))
	order := make([]string, 0, len(facts.Regions))
	for _, store := range facts.Stores {
		if seen[store.Region] {
			continue
		}
		if _, ok := facts.Regions[store.Region]; !ok {
			continue
		}
		seen[store.Region] = true
		order = append(order, store.Region)
	}
	if len(order) < len(facts.Regions) {
		rest := make([]string, 0, len(facts.Regions)-len(order))
		for name := range facts.Regions {
			if !seen[name] {
				rest = append(rest, name)
			}
		}
		sort.Strings(rest)
		order = append(order, rest...)
	}
	return order
}

// regionDisplayName 无中文字体时退回 R1/R2…（避免画不出字）。
func regionDisplayName(name string, i int, vf *vectorFont) string {
	if vf != nil {
		return name
	}
	return "R" + strconv.Itoa(i+1)
}

// weekLabel 复刻 charts.ts 的 weekLabel。
func weekLabel(w contract.IsoWeek) string {
	return "第" + strconv.Itoa(w.Week) + "周"
}

// yScale 值轴映射：0 在底、max 在顶。
type yScale struct {
	max, interval, x0, x1, y0, y1 float64
}

func newYScale(maxV float64) yScale {
	interval := niceInterval(maxV)
	top := math.Ceil(maxV/interval) * interval
	if top <= 0 {
		top = interval
	}
	return yScale{
		max:      top,
		interval: interval,
		x0:       plotLeft,
		x1:       chartWidth - plotRight,
		y0:       plotTop,
		y1:       chartHeight - plotBottom,
	}
}

func (s yScale) y(v float64) float64 { return s.y1 - (v/s.max)*(s.y1-s.y0) }

// splits 轴上限包含的等分段数（ECharts 最多 5 段，取“好看”的刻度后可能更少）。
func (s yScale) splits() int {
	n := int(math.Round(s.max / s.interval))
	if n < 1 {
		n = 1
	}
	return n
}

// niceInterval 取“好看”的刻度间隔，等价于 ECharts 的 niceTicks：
// step = 10^floor(log10(raw))；raw 达到 5×step 用 10×step、达到 2×step 用 5×step。
func niceInterval(maxV float64) float64 {
	if maxV <= 0 {
		return 1
	}
	raw := maxV / 5
	step := math.Pow(10, math.Floor(math.Log10(raw)))
	switch {
	case raw >= step*5:
		return step * 10
	case raw >= step*2:
		return step * 5
	default:
		return step
	}
}

// ---- 图例 ----

type legendKind int

const (
	legendBar legendKind = iota
	legendLine
	legendLineDashed
)

type legendEntry struct {
	name string
	col  color.RGBA
	kind legendKind
}

// drawLegend 水平居中图例（legend.top=28、itemWidth=14、itemHeight=8、itemGap=10）。
func drawLegend(c *canvas, entries []legendEntry, size float64) {
	if len(entries) == 0 {
		return
	}
	const (
		itemW       = 14.0
		itemGap     = 10.0
		iconTextGap = 5.0
	)
	widths := make([]float64, len(entries))
	total := 0.0
	for i, e := range entries {
		widths[i] = itemW + iconTextGap + c.textWidth(e.name, size)
		total += widths[i]
	}
	total += itemGap * float64(len(entries)-1)
	x := (chartWidth - total) / 2
	baseline := legendTop + size*0.85
	cy := legendTop + 6
	for i, e := range entries {
		switch e.kind {
		case legendBar:
			c.rect(x, cy-4, itemW, 8, e.col)
		case legendLine:
			c.line(x, cy, x+itemW, cy, 2, e.col)
			c.circle(x+itemW/2, cy, 3.5, e.col)
		case legendLineDashed:
			c.dashedPolyline([][2]float64{{x, cy}, {x + itemW, cy}}, 4, 2.5, 2, e.col)
			c.circle(x+itemW/2, cy, 3.5, e.col)
		}
		c.text(x+itemW+iconTextGap, baseline, e.name, size, textColor, alignLeft)
		x += widths[i] + itemGap
	}
}

// drawTitle 标题（title.left=center、top=4、fontSize=15）。
func drawTitle(c *canvas, title string) {
	c.text(chartWidth/2, 4+15*0.8, title, 15, textColor, alignCenter)
}

// drawGridAndYAxis 横向分隔线与值轴标签（axisLabel.formatter=wan）。
// 没有中文字体时用纯数字（避免“万”字画不出来只剩数字）。
func drawGridAndYAxis(c *canvas, sc yScale) {
	splits := sc.splits()
	for i := 0; i <= splits; i++ {
		v := sc.interval * float64(i)
		y := sc.y(v)
		c.line(sc.x0, y, sc.x1, y, 1, splitColor)
		label := wan(v)
		if c.vf == nil {
			label = trimFloat(v)
		}
		c.text(sc.x0-8, y+4, label, 12, textColor, alignRight)
	}
}

// ---- 图 1：各区域本周与上周实收对比 ----

func renderRegionBarPNG(facts contract.WeeklyFacts, vf *vectorFont) ([]byte, error) {
	c := newChartCanvas(vf)
	names := regionOrder(facts)

	hasPrev := len(names) > 0
	for _, name := range names {
		if facts.Regions[name].PrevRevenueCents == nil {
			hasPrev = false
			break
		}
	}
	cur := make([]float64, len(names))
	prev := make([]float64, len(names))
	maxV := 0.0
	for i, name := range names {
		m := facts.Regions[name]
		cur[i] = math.Round(float64(m.RevenueCents) / 100)
		if m.PrevRevenueCents != nil {
			prev[i] = math.Round(float64(*m.PrevRevenueCents) / 100)
		}
		if cur[i] > maxV {
			maxV = cur[i]
		}
		if hasPrev && prev[i] > maxV {
			maxV = prev[i]
		}
	}

	sc := newYScale(maxV)
	drawGridAndYAxis(c, sc)
	c.line(sc.x0, sc.y1, sc.x1, sc.y1, 1, axisColor)

	if len(names) > 0 {
		band := (sc.x1 - sc.x0) / float64(len(names))
		seriesCount := 1
		if hasPrev {
			seriesCount = 2
		}
		groupW := band * 0.8 // barCategoryGap 默认 20%
		barW := groupW / (float64(seriesCount) + float64(seriesCount-1)*0.3)
		type bar struct {
			x, w, v float64
			col     color.RGBA
		}
		bars := make([]bar, 0, len(names)*seriesCount)
		for i := range names {
			center := sc.x0 + band*(float64(i)+0.5)
			x := center - groupW/2
			if hasPrev {
				bars = append(bars, bar{x: x, w: barW, v: prev[i], col: prevBarCol})
				x += barW * 1.3 // barGap 默认 30%
			}
			bars = append(bars, bar{x: x, w: barW, v: cur[i], col: seriesColors[0]})
		}
		for _, b := range bars {
			top := sc.y(b.v)
			c.rect(b.x, top, b.w, sc.y1-top, b.col)
			c.text(b.x+b.w/2, top-4, trimFloat(b.v), 11, textColor, alignCenter)
		}
		// 类目轴：边界刻度 + 居中标签
		for i := 0; i <= len(names); i++ {
			x := sc.x0 + band*float64(i)
			c.line(x, sc.y1, x, sc.y1+5, 1, axisColor)
		}
		for i, name := range names {
			c.text(sc.x0+band*(float64(i)+0.5), sc.y1+18, regionDisplayName(name, i, vf), 13, textColor, alignCenter)
		}
	}

	entries := make([]legendEntry, 0, 2)
	if hasPrev {
		name := "上周"
		if vf == nil {
			name = "Last week"
		}
		entries = append(entries, legendEntry{name: name, col: prevBarCol, kind: legendBar})
	}
	curName := "本周"
	if vf == nil {
		curName = "This week"
	}
	entries = append(entries, legendEntry{name: curName, col: seriesColors[0], kind: legendBar})
	drawLegend(c, entries, 13)

	title := "各区域实收对比（" + weekLabel(facts.Period.IsoWeek) + "，元）"
	if vf == nil {
		title = "Region revenue by week (Week " + strconv.Itoa(facts.Period.Week) + ", CNY)"
	}
	drawTitle(c, title)
	return c.encodePNG()
}

// ---- 图 2：近 4 周实收趋势（全公司与各区域） ----

func renderTrendLinePNG(facts contract.WeeklyFacts, vf *vectorFont) ([]byte, error) {
	c := newChartCanvas(vf)
	trend := facts.Trend
	n := len(trend)
	regionNames := regionOrder(facts)

	company := make([]float64, n)
	maxV := 0.0
	for i, t := range trend {
		company[i] = math.Round(float64(t.CompanyCents) / 100)
		if company[i] > maxV {
			maxV = company[i]
		}
	}
	regionVals := make([][]float64, len(regionNames))
	for ri, name := range regionNames {
		vals := make([]float64, n)
		for i, t := range trend {
			vals[i] = math.Round(float64(t.Regions[name]) / 100)
			if vals[i] > maxV {
				maxV = vals[i]
			}
		}
		regionVals[ri] = vals
	}

	sc := newYScale(maxV)
	drawGridAndYAxis(c, sc)
	c.line(sc.x0, sc.y1, sc.x1, sc.y1, 1, axisColor)

	if n > 0 {
		xAt := func(i int) float64 {
			if n == 1 {
				return (sc.x0 + sc.x1) / 2
			}
			return sc.x0 + (sc.x1-sc.x0)*float64(i)/float64(n-1)
		}
		// TS 的 series 顺序：全公司（虚线）在前，各区域实线在后。
		pts := make([][2]float64, n)
		for i := range trend {
			pts[i] = [2]float64{xAt(i), sc.y(company[i])}
		}
		c.dashedPolyline(pts, 6, 4, 2, companyCol)
		for _, p := range pts {
			c.circle(p[0], p[1], 3.5, companyCol)
		}
		for ri := range regionNames {
			pts := make([][2]float64, n)
			for i := range trend {
				pts[i] = [2]float64{xAt(i), sc.y(regionVals[ri][i])}
			}
			col := seriesColor(ri)
			c.polyline(pts, 2, col)
			for _, p := range pts {
				c.circle(p[0], p[1], 3.5, col)
			}
		}
		// 类目轴：刻度 + 周次标签
		for i := 0; i < n; i++ {
			x := xAt(i)
			c.line(x, sc.y1, x, sc.y1+5, 1, axisColor)
			label := weekLabel(trend[i].Week)
			if vf == nil {
				label = "W" + strconv.Itoa(trend[i].Week.Week)
			}
			c.text(x, sc.y1+18, label, 13, textColor, alignCenter)
		}
	}

	entries := make([]legendEntry, 0, len(regionNames)+1)
	companyName := "全公司"
	if vf == nil {
		companyName = "All"
	}
	entries = append(entries, legendEntry{name: companyName, col: companyCol, kind: legendLineDashed})
	for i, name := range regionNames {
		entries = append(entries, legendEntry{name: regionDisplayName(name, i, vf), col: seriesColor(i), kind: legendLine})
	}
	drawLegend(c, entries, 13)

	title := "近 4 周实收趋势（元）"
	if vf == nil {
		title = "4-week revenue trend (CNY)"
	}
	drawTitle(c, title)
	return c.encodePNG()
}
