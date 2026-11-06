package tools

// render_report_docx 的默认实现：直接转调 internal/render。
// 工具层不关心 Word/图表的生成细节；入口包也可以注入自定义 RenderFunc 覆盖。

import (
	"digital-employee/internal/contract"
	"digital-employee/internal/render"
)

// DefaultRenderer 用 internal/render 的默认配置生成 Word 周报。
func DefaultRenderer(input contract.RenderReportInput) (contract.RenderReportOutput, error) {
	return render.RenderReportDocx(input, render.Options{})
}
