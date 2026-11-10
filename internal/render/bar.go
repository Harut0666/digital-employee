package render

// 通用柱状图：场景 B 的“技术路线分布”和“申请趋势”。沿用周报图表的画布、坐标轴、配色和字体，
// 纯 Go 绘制，没有中文字体时类目标签退回 R1/R2…（年份本来就是数字，不受影响）。

import (
	"image/color"
	"strconv"
)

// BarChart 一张柱状图。
type BarChart struct {
	Title  string
	Labels []string
	Values []int
	// Faded 与 Labels 一一对应：为 true 的柱画成浅色（如因公开滞后而不完整的年份）；可以为空
	Faded []bool
	// FadedLegend 浅色柱的图例说明；没有浅色柱时不画图例
	FadedLegend string
	// Legend 实色柱的图例说明（有浅色柱时与 FadedLegend 一起出现）
	Legend string
}

var fadedBarColor = color.RGBA{0xb7, 0xc4, 0xd9, 0xff}

// RenderBarPNG 把柱状图画成 2x PNG。fontPaths 为空时按 DE_FONT_PATHS 和系统常见中文字体找。
func RenderBarPNG(chart BarChart, fontPaths []string) ([]byte, error) {
	vf := resolveChartFont(fontPaths)
	c := newChartCanvas(vf)
	drawTitle(c, chart.Title)

	maxV := 0.0
	for _, v := range chart.Values {
		if float64(v) > maxV {
			maxV = float64(v)
		}
	}
	sc := newYScale(maxV)
	drawGridAndYAxis(c, sc)
	drawAxisBottom(c, sc)

	n := len(chart.Values)
	if n > 0 {
		step := (sc.x1 - sc.x0) / float64(n)
		barW := step * 0.55
		if barW > 56 {
			barW = 56
		}
		for i, v := range chart.Values {
			cx := sc.x0 + step*(float64(i)+0.5)
			col := seriesColor(0)
			if i < len(chart.Faded) && chart.Faded[i] {
				col = fadedBarColor
			}
			top := sc.y(float64(v))
			if v > 0 {
				c.rect(cx-barW/2, top, barW, sc.y1-top, col)
			}
			c.text(cx, top-5, strconv.Itoa(v), barLabelFontSize, barLabelCol, alignCenter)
			label := ""
			if i < len(chart.Labels) {
				label = regionDisplayName(chart.Labels[i], i, vf)
			}
			c.text(cx, xAxisLabelBaseline(sc), label, axisLabelFontSize, labelColor, alignCenter)
		}
	}

	hasFaded := false
	for _, f := range chart.Faded {
		hasFaded = hasFaded || f
	}
	if hasFaded && chart.FadedLegend != "" {
		entries := []legendEntry{}
		if chart.Legend != "" {
			entries = append(entries, legendEntry{name: chart.Legend, col: seriesColor(0), kind: legendBar})
		}
		entries = append(entries, legendEntry{name: chart.FadedLegend, col: fadedBarColor, kind: legendBar})
		drawLegend(c, entries, legendFontSize)
	}
	return c.encodePNG()
}
