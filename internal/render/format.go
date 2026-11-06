package render

// 数字展示格式：与 packages/tools/src/format.ts 逐行对齐。
// 金额取整数元带千分位；比例保留 1 位小数（四舍五入，远离零）。

import (
	"math"
	"strconv"
	"strings"
)

// round1 复刻 format.ts 的 round1：sign(x)*Math.round(abs(x)*1000)/10。
// 返回一位小数的百分比数值（0.611 → 61.1）。
func round1(x float64) float64 {
	return math.Copysign(math.Round(math.Abs(x)*1000), x) / 10
}

// pctTenths 把比例转成“百分数的一位小数整数”（0.611 → 611），
// 等价于 round1(x) 的十进制写法，避免 Go fmt 与 JS toFixed 在 .05 处舍入方向不同。
func pctTenths(x float64) int64 {
	return int64(math.Round(math.Abs(x) * 1000))
}

// formatTenths 把 611 渲染成 "61.1"（保留一位小数）；整数时也带一位小数。
func formatTenths(t int64, negative bool) string {
	whole := t / 10
	frac := t % 10
	s := strconv.FormatInt(whole, 10) + "." + strconv.FormatInt(frac, 10)
	if negative && t != 0 {
		return "-" + s
	}
	return s
}

// fmtYuan 复刻 format.ts 的 fmtYuan：整数元 + en-US 千分位。
func fmtYuan(cents int64) string {
	negative := cents < 0
	a := cents
	if negative {
		a = -a
	}
	yuan := a / 100
	if a%100 >= 50 {
		yuan++
	}
	s := groupThousands(strconv.FormatInt(yuan, 10))
	if negative && yuan != 0 {
		return "-" + s
	}
	return s
}

// groupThousands 给十进制整数加千分位（"573635" → "573,635"）。
func groupThousands(s string) string {
	if len(s) <= 3 {
		return s
	}
	first := len(s) % 3
	if first == 0 {
		first = 3
	}
	var b strings.Builder
	b.Grow(len(s) + len(s)/3)
	b.WriteString(s[:first])
	for i := first; i < len(s); i += 3 {
		b.WriteByte(',')
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// fmtPct 复刻 format.ts 的 fmtPct：nil → 占位符 "—"。
func fmtPct(x *float64, empty string) string {
	if x == nil {
		return empty
	}
	return formatTenths(pctTenths(*x), *x < 0) + "%"
}

// fmtWow 复刻 format.ts 的 fmtWow：带正负号；不可比时给出说明。
func fmtWow(x *float64) string {
	if x == nil {
		return "不可比"
	}
	prefix := ""
	if *x > 0 && pctTenths(*x) != 0 {
		prefix = "+"
	}
	return prefix + formatTenths(pctTenths(*x), *x < 0) + "%"
}

// wan 复刻 charts.ts 的 wan：>= 10000 时折成“x.x万”。
func wan(v float64) string {
	if v >= 10000 {
		return trimFloat(math.Round(v/1000)/10) + "万"
	}
	return trimFloat(v)
}

// trimFloat 按 JS 数字转字符串的风格输出（整数不带 ".0"）。
func trimFloat(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}
