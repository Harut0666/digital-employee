//go:build ignore

// 生成应用图标：绿色圆角方块里一个白色半身人像（“数字员工”）。颜色取自前端主题色 --primary (#1a7f67)。
//
//	go run scripts/gen-icon.go
//
// 输出：
//   - cmd/digital-employee/winres/icon.ico  多尺寸 ICO（PNG 压缩），go-winres 嵌进 exe 资源
//   - internal/desktop/icon.png            256x256 PNG，窗口运行时设置图标（go:embed）
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
)

var sizes = []int{16, 24, 32, 48, 64, 128, 256}

func main() {
	var images [][]byte
	for _, size := range sizes {
		var buffer bytes.Buffer
		if err := png.Encode(&buffer, render(size)); err != nil {
			log.Fatal(err)
		}
		images = append(images, buffer.Bytes())
	}
	write(filepath.Join("cmd", "digital-employee", "winres", "icon.ico"), buildICO(images))
	write(filepath.Join("internal", "desktop", "icon.png"), images[len(images)-1])
}

func write(path string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("写入 %s（%d 字节）", path, len(data))
}

// buildICO 把若干 PNG 打成一个 ICO（Vista 之后支持 PNG 压缩的图标项）。
func buildICO(images [][]byte) []byte {
	var out bytes.Buffer
	_ = binary.Write(&out, binary.LittleEndian, [3]uint16{0, 1, uint16(len(images))})
	offset := 6 + 16*len(images)
	for index, data := range images {
		size := sizes[index]
		dimension := byte(size)
		if size >= 256 {
			dimension = 0
		}
		out.Write([]byte{dimension, dimension, 0, 0})
		_ = binary.Write(&out, binary.LittleEndian, [2]uint16{1, 32})
		_ = binary.Write(&out, binary.LittleEndian, uint32(len(data)))
		_ = binary.Write(&out, binary.LittleEndian, uint32(offset))
		offset += len(data)
	}
	for _, data := range images {
		out.Write(data)
	}
	return out.Bytes()
}

// render 4x4 超采样画一个 size x size 的图标。坐标归一化到 0..1。
func render(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const samples = 4
	top := [3]float64{0x24, 0xa5, 0x85}
	bottom := [3]float64{0x12, 0x68, 0x54}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a float64
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					u := (float64(x) + (float64(sx)+0.5)/samples) / float64(size)
					v := (float64(y) + (float64(sy)+0.5)/samples) / float64(size)
					if !insideRoundedSquare(u, v, 0.02, 0.22) {
						continue
					}
					a++
					if insideFigure(u, v) {
						r, g, b = r+255, g+255, b+255
						continue
					}
					r += top[0] + (bottom[0]-top[0])*v
					g += top[1] + (bottom[1]-top[1])*v
					b += top[2] + (bottom[2]-top[2])*v
				}
			}
			if a == 0 {
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(r / a), G: uint8(g / a), B: uint8(b / a), A: uint8(255 * a / (samples * samples))})
		}
	}
	return img
}

// insideRoundedSquare 点是否在圆角方块里；margin 是四周留白，radius 是圆角半径。
func insideRoundedSquare(u, v, margin, radius float64) bool {
	if u < margin || u > 1-margin || v < margin || v > 1-margin {
		return false
	}
	cx := math.Min(math.Max(u, margin+radius), 1-margin-radius)
	cy := math.Min(math.Max(v, margin+radius), 1-margin-radius)
	return math.Hypot(u-cx, v-cy) <= radius
}

// insideFigure 半身人像：圆形的头 + 椭圆的肩膀（被方块底边截掉下半部分）。
func insideFigure(u, v float64) bool {
	if math.Hypot(u-0.5, v-0.37) <= 0.15 {
		return true
	}
	dx, dy := (u-0.5)/0.30, (v-0.86)/0.27
	return dx*dx+dy*dy <= 1
}

