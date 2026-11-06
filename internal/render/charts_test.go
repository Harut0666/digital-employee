package render

// 图表 PNG：尺寸（640×300 逻辑 × 2x）、非空白、无字体回退不 panic、空数据健壮性。

import (
	"testing"

	"digital-employee/internal/contract"
)

func TestChartPNGsWithFont(t *testing.T) {
	vf := resolveChartFont(nil)
	if vf == nil {
		t.Skip("本机没有可用的中文字体，跳过中文渲染断言（回退路径见 TestChartPNGFallback）")
	}
	input := loadFixture(t)
	bar, err := renderRegionBarPNG(input.Facts, vf)
	if err != nil {
		t.Fatalf("柱状图渲染失败: %v", err)
	}
	trend, err := renderTrendLinePNG(input.Facts, vf)
	if err != nil {
		t.Fatalf("趋势图渲染失败: %v", err)
	}
	for name, data := range map[string][]byte{"bar": bar, "trend": trend} {
		w, h, ink := pngSize(t, data)
		if w != 1280 || h != 600 {
			t.Errorf("%s 尺寸 = %dx%d, want 1280x600（640×300 逻辑 × 2x）", name, w, h)
		}
		if ink < 2000 {
			t.Errorf("%s 内容过少（非白像素 %d），疑似空白图", name, ink)
		}
	}
}

func TestChartPNGFallback(t *testing.T) {
	input := loadFixture(t)
	bar, err := renderRegionBarPNG(input.Facts, nil)
	if err != nil {
		t.Fatalf("无字体时柱状图不应报错: %v", err)
	}
	trend, err := renderTrendLinePNG(input.Facts, nil)
	if err != nil {
		t.Fatalf("无字体时趋势图不应报错: %v", err)
	}
	for name, data := range map[string][]byte{"bar": bar, "trend": trend} {
		w, h, ink := pngSize(t, data)
		if w != 1280 || h != 600 {
			t.Errorf("%s 回退后尺寸 = %dx%d, want 1280x600", name, w, h)
		}
		if ink < 1000 {
			t.Errorf("%s 回退后内容过少（非白像素 %d）", name, ink)
		}
	}
	// 无字体时图例/标签退回英文（R1 等），不再尝试画中文
	if vf := resolveChartFont([]string{"Z:/missing.ttf"}); vf != nil {
		t.Errorf("不存在的字体路径不应解析出字体")
	}
}

func TestChartPNGEmptyFacts(t *testing.T) {
	facts := contract.WeeklyFacts{}
	bar, err := renderRegionBarPNG(facts, nil)
	if err != nil {
		t.Fatalf("空事实柱状图不应报错: %v", err)
	}
	trend, err := renderTrendLinePNG(facts, nil)
	if err != nil {
		t.Fatalf("空事实趋势图不应报错: %v", err)
	}
	for name, data := range map[string][]byte{"bar": bar, "trend": trend} {
		w, h, _ := pngSize(t, data)
		if w != 1280 || h != 600 {
			t.Errorf("%s 空数据尺寸 = %dx%d", name, w, h)
		}
	}
}

func TestCanvasTextMeasure(t *testing.T) {
	c := newChartCanvas(nil)
	if got := c.textWidth("abc", 13); got <= 0 {
		t.Errorf("位图字体宽度应为正，得到 %v", got)
	}
	if got := c.textWidth("", 13); got != 0 {
		t.Errorf("空串宽度应为 0，得到 %v", got)
	}
	vf := resolveChartFont(nil)
	if vf == nil {
		return
	}
	c = newChartCanvas(vf)
	ascii := c.textWidth("abc", 13)
	cjk := c.textWidth("中文", 13)
	if ascii <= 0 || cjk <= 0 {
		t.Fatalf("字体测量宽度应为正：ascii=%v cjk=%v", ascii, cjk)
	}
	if cjk <= ascii {
		t.Errorf("两个汉字应比三个拉丁字母宽：cjk=%v ascii=%v", cjk, ascii)
	}
}

func TestSeriesColors(t *testing.T) {
	want := []struct{ r, g, b uint8 }{
		{0x2f, 0x6f, 0xdb},
		{0xe0, 0x87, 0x3b},
		{0x3a, 0x9d, 0x6e},
		{0xa0, 0x5b, 0xd0},
		{0xd2, 0x50, 0x6b},
	}
	for i, w := range want {
		col := seriesColor(i)
		if col.R != w.r || col.G != w.g || col.B != w.b {
			t.Errorf("seriesColor(%d) = %v, want %v", i, col, w)
		}
	}
	if got := seriesColor(5); got != seriesColor(0) {
		t.Errorf("色板应循环使用")
	}
}
