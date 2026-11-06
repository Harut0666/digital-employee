package tools

// validate_submission 的边界与异常输入（对应 packages/tools/test/validate.test.ts 的关键用例）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"digital-employee/internal/contract"
)

func attachmentsFor(samples string, names ...string) []contract.AttachmentRef {
	out := make([]contract.AttachmentRef, 0, len(names))
	for _, name := range names {
		out = append(out, contract.AttachmentRef{Name: name, Path: filepath.Join(samples, name)})
	}
	return out
}

func TestValidateMissingSales(t *testing.T) {
	samples := samplesDir(t)
	out, err := ValidateSubmission(contract.ValidateSubmissionInput{
		Period:      contract.IsoWeek{Year: 2026, Week: 44},
		Attachments: attachmentsFor(samples, "周会纪要_2026W44.docx"),
	})
	if err != nil {
		t.Fatalf("返回错误：%v", err)
	}
	if out.OK || out.Pause == nil || out.Pause.Code != contract.PauseMissingSales {
		t.Fatalf("期望 missing_sales，实际 %+v", out)
	}
	if !strings.Contains(out.Pause.Message, "销售明细") {
		t.Errorf("message 应提到销售明细：%s", out.Pause.Message)
	}
	// 纪要仍然先识别
	if len(out.Minutes) != 1 || out.Minutes[0].Week == nil || out.Minutes[0].Week.Week != 44 {
		t.Errorf("minutes = %+v", out.Minutes)
	}
}

func TestValidateOutOfRange(t *testing.T) {
	samples := samplesDir(t)
	out, err := ValidateSubmission(contract.ValidateSubmissionInput{
		Period:      contract.IsoWeek{Year: 2026, Week: 46},
		Attachments: attachmentsFor(samples, "销售明细.xlsx"),
	})
	if err != nil {
		t.Fatalf("返回错误：%v", err)
	}
	if out.Pause == nil || out.Pause.Code != contract.PausePeriodOutOfRange {
		t.Fatalf("期望 period_out_of_range，实际 %+v", out.Pause)
	}
	if out.Coverage == nil || out.Coverage.From != "2026-08-31" || out.Coverage.To != "2026-11-01" {
		t.Errorf("coverage = %+v", out.Coverage)
	}
	for _, want := range []string{"2026-08-31", "2026-11-01"} {
		if !strings.Contains(out.Pause.Message, want) {
			t.Errorf("message 应包含 %s：%s", want, out.Pause.Message)
		}
	}
}

func TestValidateCsvOnly(t *testing.T) {
	samples := samplesDir(t)
	out, err := ValidateSubmission(contract.ValidateSubmissionInput{
		Period:      contract.IsoWeek{Year: 2026, Week: 44},
		Attachments: attachmentsFor(samples, "销售明细.csv", "周会纪要_2026W44.docx"),
	})
	if err != nil {
		t.Fatalf("返回错误：%v", err)
	}
	if out.Pause == nil || out.Pause.Code != contract.PauseMissingDimensions {
		t.Fatalf("期望 missing_dimensions，实际 %+v", out.Pause)
	}
	if out.Sales == nil || out.Sales.Kind != "csv" {
		t.Errorf("sales = %+v", out.Sales)
	}
	for _, want := range []string{"门店信息", "商品信息", "销售目标"} {
		if !strings.Contains(out.Pause.Message, want) {
			t.Errorf("message 应包含 %s：%s", want, out.Pause.Message)
		}
	}
}

func TestValidateMinutesMissingAndMismatch(t *testing.T) {
	samples := samplesDir(t)
	missing, err := ValidateSubmission(contract.ValidateSubmissionInput{
		Period:      contract.IsoWeek{Year: 2026, Week: 44},
		Attachments: attachmentsFor(samples, "销售明细.xlsx"),
	})
	if err != nil {
		t.Fatalf("返回错误：%v", err)
	}
	if missing.Pause == nil || missing.Pause.Code != contract.PauseMinutesMissing {
		t.Fatalf("期望 minutes_missing，实际 %+v", missing.Pause)
	}

	mismatch, err := ValidateSubmission(contract.ValidateSubmissionInput{
		Period:      contract.IsoWeek{Year: 2026, Week: 44},
		Attachments: attachmentsFor(samples, "销售明细.xlsx", "周会纪要_2026W43.docx"),
	})
	if err != nil {
		t.Fatalf("返回错误：%v", err)
	}
	if mismatch.Pause == nil || mismatch.Pause.Code != contract.PauseMinutesWeekMismatch {
		t.Fatalf("期望 minutes_week_mismatch，实际 %+v", mismatch.Pause)
	}
	if len(mismatch.Minutes) != 1 || mismatch.Minutes[0].Week == nil || mismatch.Minutes[0].Week.Week != 43 {
		t.Errorf("minutes = %+v", mismatch.Minutes)
	}
}

func TestValidateSalesFileUnreadable(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "损坏.xlsx")
	if err := os.WriteFile(broken, []byte("not a zip file"), 0o644); err != nil {
		t.Fatalf("写临时文件失败：%v", err)
	}
	out, err := ValidateSubmission(contract.ValidateSubmissionInput{
		Period:      contract.IsoWeek{Year: 2026, Week: 44},
		Attachments: []contract.AttachmentRef{{Name: "损坏.xlsx", Path: broken}},
	})
	if err != nil {
		t.Fatalf("返回错误：%v", err)
	}
	if out.Pause == nil || out.Pause.Code != contract.PauseInvalidInput {
		t.Fatalf("期望 invalid_input，实际 %+v", out.Pause)
	}
	if out.Dimensions.Stores || out.Dimensions.Products || out.Dimensions.Targets {
		t.Errorf("dimensions 应为空：%+v", out.Dimensions)
	}

	// 文件不存在也是 invalid_input
	missing, err := ValidateSubmission(contract.ValidateSubmissionInput{
		Period:      contract.IsoWeek{Year: 2026, Week: 44},
		Attachments: []contract.AttachmentRef{{Name: "不存在.xlsx", Path: filepath.Join(dir, "不存在.xlsx")}},
	})
	if err != nil {
		t.Fatalf("返回错误：%v", err)
	}
	if missing.Pause == nil || missing.Pause.Code != contract.PauseInvalidInput {
		t.Fatalf("期望 invalid_input，实际 %+v", missing.Pause)
	}
}

func TestValidatePartialPeriod(t *testing.T) {
	samples := samplesDir(t)
	raw, err := os.ReadFile(filepath.Join(samples, "销售明细.csv"))
	if err != nil {
		t.Fatalf("读 CSV 失败：%v", err)
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for i, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if i == 0 || (len(fields) > 1 && fields[1] <= "2026-10-29") {
			kept = append(kept, line)
		}
	}
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "销售明细.csv")
	if err := os.WriteFile(csvPath, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatalf("写 CSV 失败：%v", err)
	}
	attachments := []contract.AttachmentRef{
		{Name: "销售明细.csv", Path: csvPath},
		{Name: "周会纪要_2026W44.docx", Path: filepath.Join(samples, "周会纪要_2026W44.docx")},
	}
	out, err := ValidateSubmission(contract.ValidateSubmissionInput{
		Period: contract.IsoWeek{Year: 2026, Week: 44}, Attachments: attachments,
	})
	if err != nil {
		t.Fatalf("返回错误：%v", err)
	}
	if out.Pause == nil || out.Pause.Code != contract.PausePeriodIncomplete {
		t.Fatalf("期望 period_incomplete，实际 %+v", out.Pause)
	}
	if out.Coverage == nil || out.Coverage.To != "2026-10-29" {
		t.Errorf("coverage = %+v", out.Coverage)
	}
	// 选择按已有数据继续后进入后续检查（CSV 缺维表）
	partial, err := ValidateSubmission(contract.ValidateSubmissionInput{
		Period: contract.IsoWeek{Year: 2026, Week: 44}, Attachments: attachments, AllowPartialPeriod: true,
	})
	if err != nil {
		t.Fatalf("返回错误：%v", err)
	}
	if partial.Pause == nil || partial.Pause.Code != contract.PauseMissingDimensions {
		t.Fatalf("期望 missing_dimensions，实际 %+v", partial.Pause)
	}
}

// 纪要周次按正文标题识别，不看文件名；同名路径追加在末尾时取最新一份。
func TestValidatePicksLatestSalesFile(t *testing.T) {
	samples := samplesDir(t)
	dir := t.TempDir()
	corrected := filepath.Join(dir, "销售明细_更正.xlsx")
	raw, err := os.ReadFile(filepath.Join(samples, "销售明细.xlsx"))
	if err != nil {
		t.Fatalf("读样例失败：%v", err)
	}
	if err := os.WriteFile(corrected, raw, 0o644); err != nil {
		t.Fatalf("写临时文件失败：%v", err)
	}
	attachments := []contract.AttachmentRef{
		{Name: "销售明细.xlsx", Path: filepath.Join(samples, "销售明细.xlsx")},
		{Name: "周会纪要_2026W44.docx", Path: filepath.Join(samples, "周会纪要_2026W44.docx")},
		{Name: "销售明细_更正.xlsx", Path: corrected},
	}
	out, err := ValidateSubmission(contract.ValidateSubmissionInput{
		Period: contract.IsoWeek{Year: 2026, Week: 44}, Attachments: attachments,
	})
	if err != nil {
		t.Fatalf("返回错误：%v", err)
	}
	if out.Sales == nil || out.Sales.Path != corrected {
		t.Fatalf("应取最新的 xlsx：%+v", out.Sales)
	}
	// 更晚的 csv 不能顶掉 xlsx
	attachments = append(attachments, contract.AttachmentRef{Name: "销售明细.csv", Path: filepath.Join(samples, "销售明细.csv")})
	out, err = ValidateSubmission(contract.ValidateSubmissionInput{
		Period: contract.IsoWeek{Year: 2026, Week: 44}, Attachments: attachments,
	})
	if err != nil {
		t.Fatalf("返回错误：%v", err)
	}
	if out.Sales == nil || out.Sales.Path != corrected {
		t.Fatalf("xlsx 应优先于 csv：%+v", out.Sales)
	}
}
