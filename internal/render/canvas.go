package render

// 纯 Go 绘图画布：图表按 640×300 逻辑坐标绘制，默认以 2x（1280×600）输出。
// 有矢量字体时直接在 2x 画布上排版（文字锐利）；找不到字体时按 1x 绘制后用
// 位图字体（basicfont）输出，最后整体最近邻放大到 2x，保证不 panic 且仍有英文/数字。

import (
	"bytes"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/png"
	"math"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const (
	chartWidth  = 640.0
	chartHeight = 300.0
	chartScale  = 2.0
)

type align int

const (
	alignLeft align = iota
	alignCenter
	alignRight
)

// canvas 一块 RGBA 画布，坐标一律用逻辑像素，内部按 scale 放大。
type canvas struct {
	img   *image.RGBA
	scale float64
	vf    *vectorFont // nil 表示退到位图字体
}

func newChartCanvas(vf *vectorFont) *canvas {
	scale := chartScale
	if vf == nil {
		scale = 1
	}
	img := image.NewRGBA(image.Rect(0, 0, int(chartWidth*scale), int(chartHeight*scale)))
	stddraw.Draw(img, img.Bounds(), &image.Uniform{C: color.White}, image.Point{}, stddraw.Src)
	return &canvas{img: img, scale: scale, vf: vf}
}

// dev 逻辑坐标 → 设备坐标（四舍五入）。
func (c *canvas) dev(v float64) int { return int(math.Round(v * c.scale)) }

// blend 按覆盖度把颜色画到一个像素上（源不透明）。
func (c *canvas) blend(x, y int, col color.RGBA, cov float64) {
	if cov <= 0 {
		return
	}
	b := c.img.Bounds()
	if x < b.Min.X || y < b.Min.Y || x >= b.Max.X || y >= b.Max.Y {
		return
	}
	if cov > 1 {
		cov = 1
	}
	i := c.img.PixOffset(x, y)
	p := c.img.Pix[i : i+4]
	if cov >= 1 {
		p[0], p[1], p[2], p[3] = col.R, col.G, col.B, 0xff
		return
	}
	a := cov
	p[0] = uint8(float64(col.R)*a + float64(p[0])*(1-a) + 0.5)
	p[1] = uint8(float64(col.G)*a + float64(p[1])*(1-a) + 0.5)
	p[2] = uint8(float64(col.B)*a + float64(p[2])*(1-a) + 0.5)
	p[3] = 0xff
}

// rect 实心矩形。
func (c *canvas) rect(x, y, w, h float64, col color.RGBA) {
	x0, y0 := c.dev(x), c.dev(y)
	x1, y1 := c.dev(x+w), c.dev(y+h)
	if x1 <= x0 || y1 <= y0 {
		return
	}
	stddraw.Draw(c.img, image.Rect(x0, y0, x1, y1), &image.Uniform{C: col}, image.Point{}, stddraw.Src)
}

// line 抗锯齿线段，width 为逻辑线宽。
func (c *canvas) line(x1, y1, x2, y2, width float64, col color.RGBA) {
	X1, Y1 := x1*c.scale, y1*c.scale
	X2, Y2 := x2*c.scale, y2*c.scale
	w := width * c.scale
	if w < 1 {
		w = 1
	}
	r := w / 2
	minX := int(math.Floor(math.Min(X1, X2) - r - 1))
	maxX := int(math.Ceil(math.Max(X1, X2) + r + 1))
	minY := int(math.Floor(math.Min(Y1, Y2) - r - 1))
	maxY := int(math.Ceil(math.Max(Y1, Y2) + r + 1))
	for py := minY; py <= maxY; py++ {
		for px := minX; px <= maxX; px++ {
			d := segDist(float64(px)+0.5, float64(py)+0.5, X1, Y1, X2, Y2)
			c.blend(px, py, col, r+0.5-d)
		}
	}
}

// polyline 折线。
func (c *canvas) polyline(pts [][2]float64, width float64, col color.RGBA) {
	for i := 0; i+1 < len(pts); i++ {
		c.line(pts[i][0], pts[i][1], pts[i+1][0], pts[i+1][1], width, col)
	}
}

// dashedPolyline 虚线折线（ECharts lineStyle.type=dashed 的近似）。
func (c *canvas) dashedPolyline(pts [][2]float64, dash, gap, width float64, col color.RGBA) {
	on := true
	remain := dash
	for i := 0; i+1 < len(pts); i++ {
		x1, y1 := pts[i][0], pts[i][1]
		x2, y2 := pts[i+1][0], pts[i+1][1]
		length := math.Hypot(x2-x1, y2-y1)
		if length == 0 {
			continue
		}
		ux, uy := (x2-x1)/length, (y2-y1)/length
		for pos := 0.0; pos < length; {
			step := math.Min(remain, length-pos)
			if on {
				c.line(x1+ux*pos, y1+uy*pos, x1+ux*(pos+step), y1+uy*(pos+step), width, col)
			}
			pos += step
			remain -= step
			if remain <= 1e-9 {
				on = !on
				if on {
					remain = dash
				} else {
					remain = gap
				}
			}
		}
	}
}

// circle 实心圆（symbol）。
func (c *canvas) circle(cx, cy, r float64, col color.RGBA) {
	CX, CY, R := cx*c.scale, cy*c.scale, r*c.scale
	minX := int(math.Floor(CX - R - 1))
	maxX := int(math.Ceil(CX + R + 1))
	minY := int(math.Floor(CY - R - 1))
	maxY := int(math.Ceil(CY + R + 1))
	for py := minY; py <= maxY; py++ {
		for px := minX; px <= maxX; px++ {
			d := math.Hypot(float64(px)+0.5-CX, float64(py)+0.5-CY)
			c.blend(px, py, col, R+0.5-d)
		}
	}
}

// ring 空心圆：先画外圈描边色，再填内圈（ECharts 折线 symbol：白底 + 系列色描边 2px）。
func (c *canvas) ring(cx, cy, r, strokeWidth float64, fill, stroke color.RGBA) {
	c.circle(cx, cy, r+strokeWidth/2, stroke)
	c.circle(cx, cy, r-strokeWidth/2, fill)
}

// roundRect 圆角矩形（ECharts 图例的柱子图标：14×8、圆角 2）。
func (c *canvas) roundRect(x, y, w, h, radius float64, col color.RGBA) {
	if w <= 0 || h <= 0 {
		return
	}
	if radius*2 > w {
		radius = w / 2
	}
	if radius*2 > h {
		radius = h / 2
	}
	CX, CY := (x+w/2)*c.scale, (y+h/2)*c.scale
	W, H, R := w*c.scale, h*c.scale, radius*c.scale
	hw, hh := W/2-R, H/2-R
	minX := int(math.Floor(CX - W/2 - 1))
	maxX := int(math.Ceil(CX + W/2 + 1))
	minY := int(math.Floor(CY - H/2 - 1))
	maxY := int(math.Ceil(CY + H/2 + 1))
	for py := minY; py <= maxY; py++ {
		for px := minX; px <= maxX; px++ {
			dx := math.Max(math.Abs(float64(px)+0.5-CX)-hw, 0)
			dy := math.Max(math.Abs(float64(py)+0.5-CY)-hh, 0)
			d := math.Hypot(dx, dy) - R
			c.blend(px, py, col, 0.5-d)
		}
	}
}

// textBold 合成粗体：同一字符串叠加绘制两遍（右移 0.6 逻辑像素），
// 免得为标题单独加载一个粗体字库文件；无字体回退时同样有效。
func (c *canvas) textBold(x, y float64, s string, size float64, col color.RGBA, a align) {
	c.text(x, y, s, size, col, a)
	c.text(x+0.6, y+0.15, s, size, col, a)
}

// textWidth 文本的逻辑宽度。
func (c *canvas) textWidth(s string, size float64) float64 {
	if s == "" {
		return 0
	}
	if c.vf != nil {
		face := c.vf.face(size * c.scale)
		if face == nil {
			return 0
		}
		return float64(font.MeasureString(face, s)) / 64 / c.scale
	}
	return float64(font.MeasureString(basicfont.Face7x13, s)) / 64 / c.scale
}

// text 在 (x, y) 处绘制文本，y 是基线；align 决定水平对齐。
func (c *canvas) text(x, y float64, s string, size float64, col color.RGBA, a align) {
	if s == "" {
		return
	}
	switch a {
	case alignCenter:
		x -= c.textWidth(s, size) / 2
	case alignRight:
		x -= c.textWidth(s, size)
	}
	var face font.Face
	if c.vf != nil {
		face = c.vf.face(size * c.scale)
	} else {
		face = basicfont.Face7x13
	}
	if face == nil {
		return
	}
	d := font.Drawer{
		Dst:  c.img,
		Src:  image.NewUniform(col),
		Face: face,
		Dot:  fixed.P(c.dev(x), c.dev(y)),
	}
	d.DrawString(s)
}

// encodePNG 输出 2x PNG；1x 画布（无字体回退）整体最近邻放大到 2x。
func (c *canvas) encodePNG() ([]byte, error) {
	var out image.Image = c.img
	if c.scale != chartScale {
		dst := image.NewRGBA(image.Rect(0, 0, int(chartWidth*chartScale), int(chartHeight*chartScale)))
		xdraw.NearestNeighbor.Scale(dst, dst.Bounds(), c.img, c.img.Bounds(), xdraw.Src, nil)
		out = dst
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// segDist 点 (px,py) 到线段 (x1,y1)-(x2,y2) 的距离。
func segDist(px, py, x1, y1, x2, y2 float64) float64 {
	dx, dy := x2-x1, y2-y1
	if dx == 0 && dy == 0 {
		return math.Hypot(px-x1, py-y1)
	}
	t := ((px-x1)*dx + (py-y1)*dy) / (dx*dx + dy*dy)
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return math.Hypot(px-(x1+t*dx), py-(y1+t*dy))
}
