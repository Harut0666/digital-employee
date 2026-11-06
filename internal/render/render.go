package render

// render_report_docx：把已渲染好的报告 + 事实生成 Word 周报（含两张图表 PNG）。
// 与 packages/tools/src/render.ts 的 renderReportDocx 行为一致：
//   - 文件名 report-<safeName(idempotencyKey)>-<sha256(key)[:12]>.docx
//   - 同名同键文件已存在时直接返回（幂等，重试不产生重复文件）
//   - 先写临时文件再 rename，避免读到半成品
//   - 返回文件 sha256、按版式估算的页数、图表数

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"digital-employee/internal/contract"
)

// chartCount 报告固定包含两张图（各区域柱状对比 + 近 4 周趋势）。
const chartCount = 2

// Options 渲染选项。
type Options struct {
	// DocxFont Word 正文字体名（不嵌入字体，由阅读端系统提供）；空则用 Microsoft YaHei。
	DocxFont string
	// ChartFontPaths 图表中文字体候选路径（ttf/otf/ttc）；空则依次尝试
	// DE_FONT_PATHS 环境变量与常见系统中文字体。
	ChartFontPaths []string
}

// unsafenameRe 与 render.ts 的 safeName 一致：字母/数字/下划线/点/横线以外替换成 "_"。
var unsafeNameRe = regexp.MustCompile(`[^\p{L}\p{N}_.-]+`)

// safeName 把幂等键转成安全文件名片段（最多 60 个字符）。
func safeName(key string) string {
	s := unsafeNameRe.ReplaceAllString(key, "_")
	r := []rune(s)
	if len(r) > 60 {
		r = r[:60]
	}
	return string(r)
}

// RenderReportDocx 生成报告 docx。同名同键文件已存在时直接返回，不重复生成。
func RenderReportDocx(input contract.RenderReportInput, opts Options) (contract.RenderReportOutput, error) {
	if strings.TrimSpace(input.OutDir) == "" {
		return contract.RenderReportOutput{}, errors.New("render: outDir 不能为空")
	}
	font := opts.DocxFont
	if strings.TrimSpace(font) == "" {
		font = defaultDocxFont
	}

	keySum := sha256.Sum256([]byte(input.IdempotencyKey))
	keyHash := hex.EncodeToString(keySum[:])[:12]
	docxPath := filepath.Join(input.OutDir, "report-"+safeName(input.IdempotencyKey)+"-"+keyHash+".docx")
	estimatedPages := EstimatePages(input.Report, input.Facts)

	if _, statErr := os.Stat(docxPath); statErr != nil {
		if !errors.Is(statErr, os.ErrNotExist) {
			return contract.RenderReportOutput{}, fmt.Errorf("render: 检查已有文件失败: %w", statErr)
		}
		// 只有真正要生成时才加载字体、画图（重复调用/重试不会重复算）。
		vf := resolveChartFont(opts.ChartFontPaths)
		barPNG, err := renderRegionBarPNG(input.Facts, vf)
		if err != nil {
			return contract.RenderReportOutput{}, fmt.Errorf("render: 绘制区域柱状图失败: %w", err)
		}
		trendPNG, err := renderTrendLinePNG(input.Facts, vf)
		if err != nil {
			return contract.RenderReportOutput{}, fmt.Errorf("render: 绘制趋势图失败: %w", err)
		}
		docxBytes, err := buildDocx(input.Report, input.Facts, font, barPNG, trendPNG)
		if err != nil {
			return contract.RenderReportOutput{}, fmt.Errorf("render: 打包 docx 失败: %w", err)
		}
		if err := os.MkdirAll(input.OutDir, 0o755); err != nil {
			return contract.RenderReportOutput{}, fmt.Errorf("render: 创建输出目录失败: %w", err)
		}
		tmp := docxPath + "." + strconv.Itoa(os.Getpid()) + ".tmp"
		if err := os.WriteFile(tmp, docxBytes, 0o644); err != nil {
			return contract.RenderReportOutput{}, fmt.Errorf("render: 写临时文件失败: %w", err)
		}
		if err := os.Rename(tmp, docxPath); err != nil {
			_ = os.Remove(tmp)
			return contract.RenderReportOutput{}, fmt.Errorf("render: 重命名临时文件失败: %w", err)
		}
	}

	data, err := os.ReadFile(docxPath)
	if err != nil {
		return contract.RenderReportOutput{}, fmt.Errorf("render: 读取 docx 失败: %w", err)
	}
	sum := sha256.Sum256(data)
	return contract.RenderReportOutput{
		DocxPath:       docxPath,
		SHA256:         hex.EncodeToString(sum[:]),
		EstimatedPages: estimatedPages,
		ChartCount:     chartCount,
	}, nil
}
