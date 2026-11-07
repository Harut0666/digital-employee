package render

// 图表中文字体：用系统字体（新增依赖已在 go.mod：golang.org/x/image）。
// 找不到字体时不 panic：图表退回英文/数字（见 canvas.text 的位图字体分支）。

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

// defaultSystemFonts 与 charts.ts 的 SYSTEM_FONT_CANDIDATES 一致（顺序也一致）。
var defaultSystemFonts = []string{
	`C:\Windows\Fonts\msyh.ttc`,
	`C:\Windows\Fonts\simhei.ttf`,
	`C:\Windows\Fonts\Deng.ttf`,
	"/System/Library/Fonts/PingFang.ttc",
	"/System/Library/Fonts/STHeiti Medium.ttc",
	"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
	"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
	"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
}

// vectorFont 一个可缩放的轮廓字体（ttf/otf/ttc 的第一个字面）。
// 解析好的字体可以并发使用，进程内共享；由它创建的 face 不行，见 canvas.face。
type vectorFont struct {
	font *opentype.Font
}

// newFace 新建指定像素大小的 face；失败返回 nil。
func (v *vectorFont) newFace(sizePx float64) font.Face {
	f, err := opentype.NewFace(v.font, &opentype.FaceOptions{
		Size:    sizePx,
		DPI:     72, // 1pt = 1px，sizePx 直接就是像素高度
		Hinting: font.HintingNone,
	})
	if err != nil {
		return nil
	}
	return f
}

// fontCache 缓存按路径解析出来的字体（与 charts.ts 的 fontCache 作用相同）。
var (
	fontCacheMu sync.Mutex
	fontCache   = map[string]*vectorFont{}
)

// resolveChartFont 按 options / 环境变量 / 系统候选顺序找第一个可用字体。
// 返回 nil 表示没有可用字体，图表应退回英文/数字。
func resolveChartFont(paths []string) *vectorFont {
	candidates := resolveFontPaths(paths)
	key := strings.Join(candidates, "\x00")
	fontCacheMu.Lock()
	defer fontCacheMu.Unlock()
	if f, ok := fontCache[key]; ok {
		return f
	}
	f := loadVectorFont(candidates)
	fontCache[key] = f
	return f
}

// resolveFontPaths 选择字体候选：显式路径 > DE_FONT_PATHS > 系统候选。
func resolveFontPaths(given []string) []string {
	if len(given) > 0 {
		return given
	}
	if env := os.Getenv("DE_FONT_PATHS"); env != "" {
		return filepath.SplitList(env)
	}
	return defaultSystemFonts
}

// loadVectorFont 依次尝试候选路径，返回第一个能解析的字体；都失败返回 nil。
func loadVectorFont(paths []string) *vectorFont {
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil || len(data) == 0 {
			continue
		}
		// ttc/otc 用 ParseCollection；单个 ttf/otf 它也会返回 1 个字面的集合。
		if coll, err := opentype.ParseCollection(data); err == nil && coll.NumFonts() > 0 {
			if f, err := coll.Font(0); err == nil {
				return &vectorFont{font: f}
			}
		}
		// 极少数单字面字体 ParseCollection 不认，退回 Parse 再试一次。
		if f, err := opentype.Parse(data); err == nil {
			return &vectorFont{font: f}
		}
	}
	return nil
}
