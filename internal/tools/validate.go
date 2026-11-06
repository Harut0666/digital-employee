package tools

// validate_submission：按 design.md §10.1 的顺序，只报最根本的一个原因
// （移植自 packages/tools/src/validate.ts）。

import (
	"path/filepath"
	"strings"

	"digital-employee/internal/contract"
)

// SalesFile 识别出的销售明细文件。
type salesPick struct {
	Ref  contract.AttachmentRef
	Kind string // xlsx | csv
}

func extensionOf(ref contract.AttachmentRef) string {
	name := ref.Name
	if name == "" {
		name = ref.Path
	}
	return strings.ToLower(filepath.Ext(name))
}

// pickSales 选出销售明细文件：xlsx 优先于 csv，同类型取最后一个
// （补交的更正文件追加在末尾）。
func pickSales(files []contract.AttachmentRef) *salesPick {
	type candidate struct {
		ref  contract.AttachmentRef
		kind string
	}
	cands := make([]candidate, 0, len(files))
	for _, ref := range files {
		switch extensionOf(ref) {
		case ".xlsx":
			cands = append(cands, candidate{ref: ref, kind: "xlsx"})
		case ".csv":
			cands = append(cands, candidate{ref: ref, kind: "csv"})
		}
	}
	lastOf := func(kind string) *salesPick {
		for i := len(cands) - 1; i >= 0; i-- {
			if cands[i].kind == kind {
				return &salesPick{Ref: cands[i].ref, Kind: cands[i].kind}
			}
		}
		return nil
	}
	if pick := lastOf("xlsx"); pick != nil {
		return pick
	}
	return lastOf("csv")
}

func dimensionsOf(src *SalesSource) contract.Dimensions {
	if src == nil {
		return contract.Dimensions{}
	}
	return contract.Dimensions{
		Stores:   len(src.Stores) > 0,
		Products: len(src.Products) > 0,
		Targets:  len(src.Targets) > 0,
	}
}

func failValidation(pause *contract.PauseReason, minutes []contract.MinutesDetection, base *contract.ValidateSubmissionOutput) contract.ValidateSubmissionOutput {
	out := contract.ValidateSubmissionOutput{
		OK:         false,
		Pause:      pause,
		Dimensions: contract.Dimensions{},
		Minutes:    minutes,
	}
	if base != nil {
		out.Dimensions = base.Dimensions
		out.Minutes = base.Minutes
		out.Sales = base.Sales
		out.Coverage = base.Coverage
	}
	return out
}

// ValidateSubmission 表单与附件校验。
func ValidateSubmission(input contract.ValidateSubmissionInput) (contract.ValidateSubmissionOutput, error) {
	period := input.Period
	rangeOf := PeriodRangeOf(period)

	// 纪要始终先识别：即使最终因别的原因暂停，也把识别结果交给调用方
	minutes := make([]contract.MinutesDetection, 0)
	for _, ref := range input.Attachments {
		if extensionOf(ref) != ".docx" {
			continue
		}
		detection := contract.MinutesDetection{Path: ref.Path}
		if parsed, err := parseMinutesFile(ref.Path); err == nil {
			detection.Week = parsed.Week
		}
		minutes = append(minutes, detection)
	}

	pick := pickSales(input.Attachments)

	// 3. 缺销售明细（没有数据就无从判断覆盖范围，所以先于 1、2 处理）
	if pick == nil {
		return failValidation(&contract.PauseReason{
			Code:    contract.PauseMissingSales,
			Message: "缺少销售明细：未找到 xlsx 或 csv 格式的销售数据，无法生成周报。请上传《销售明细.xlsx》后继续。",
			Detail:  map[string]any{"missing": []string{"销售明细"}},
		}, minutes, nil), nil
	}

	src, err := readSalesSource(pick.Ref.Path)
	if err != nil {
		displayName := pick.Ref.Name
		if displayName == "" {
			displayName = filepath.Base(pick.Ref.Path)
		}
		return failValidation(&contract.PauseReason{
			Code: contract.PauseInvalidInput,
			Message: "销售数据文件无法读取（" + displayName + "）：" + err.Error() +
				"。请确认文件未损坏且包含“销售明细”表后重新上传。",
		}, minutes, nil), nil
	}
	sales := &contract.SalesFile{Path: pick.Ref.Path, Kind: pick.Kind}
	dimensions := dimensionsOf(src)
	coverage := coverageOf(src.Sales)
	base := &contract.ValidateSubmissionOutput{
		Sales:      sales,
		Dimensions: dimensions,
		Minutes:    minutes,
	}
	if coverage != nil {
		base.Coverage = coverage
	}
	if coverage == nil {
		return failValidation(&contract.PauseReason{
			Code:    contract.PauseInvalidInput,
			Message: "销售数据文件（" + salesDisplayName(pick) + "）中没有可识别的销售日期，请检查文件内容。",
		}, minutes, base), nil
	}

	// 1. 周次超出数据范围
	if rangeOf.To < coverage.From || rangeOf.From > coverage.To {
		return failValidation(&contract.PauseReason{
			Code: contract.PausePeriodOutOfRange,
			Message: "所选周期（" + WeekLabel(period) + "，" + rangeOf.From + " 至 " + rangeOf.To +
				"）超出销售数据覆盖范围：数据实际覆盖 " + coverage.From + " 至 " + coverage.To +
				"。请改选范围内的周次，或补充对应日期的销售数据。",
			Detail: map[string]any{"period": rangeOf, "coverage": *coverage},
		}, minutes, base), nil
	}

	// 2. 周次不完整（数据在周中开始或截止）
	if !input.AllowPartialPeriod && (coverage.To < rangeOf.To || coverage.From > rangeOf.From) {
		from := rangeOf.From
		if coverage.From > from {
			from = coverage.From
		}
		to := rangeOf.To
		if coverage.To < to {
			to = coverage.To
		}
		return failValidation(&contract.PauseReason{
			Code: contract.PausePeriodIncomplete,
			Message: WeekLabel(period) + "（" + rangeOf.From + " 至 " + rangeOf.To + "）的销售数据不完整：数据只覆盖 " +
				from + " 至 " + to + "。可补充缺失日期的数据，或选择“按已有数据继续”（周报首页将标注“数据不完整”）。",
			Detail: map[string]any{
				"period":    rangeOf,
				"coverage":  *coverage,
				"available": contract.DateRange{From: from, To: to},
			},
		}, minutes, base), nil
	}

	// 4. 缺维表（只传了 CSV，或 xlsx 缺工作表）
	missingDims := make([]string, 0, 3)
	if !dimensions.Stores {
		missingDims = append(missingDims, "门店信息")
	}
	if !dimensions.Products {
		missingDims = append(missingDims, "商品信息")
	}
	if !dimensions.Targets {
		missingDims = append(missingDims, "销售目标")
	}
	if len(missingDims) > 0 {
		reason := "销售明细文件中缺少相应工作表"
		if pick.Kind == "csv" {
			reason = "只上传了 CSV，其中不含门店信息、商品信息和销售目标"
		}
		return failValidation(&contract.PauseReason{
			Code: contract.PauseMissingDimensions,
			Message: "缺少维表：" + strings.Join(missingDims, "、") + "。" + reason +
				"，无法按区域、品类和目标达成率统计。请上传完整的《销售明细.xlsx》（含 4 个工作表）。",
			Detail: map[string]any{"missing": missingDims},
		}, minutes, base), nil
	}

	// 5. 缺本周纪要，或纪要周次不符
	hasThisWeek := false
	for _, m := range minutes {
		if m.Week != nil && m.Week.Year == period.Year && m.Week.Week == period.Week {
			hasThisWeek = true
			break
		}
	}
	if !hasThisWeek && !input.AllowMissingMinutes {
		if len(minutes) == 0 {
			return failValidation(&contract.PauseReason{
				Code:    contract.PauseMinutesMissing,
				Message: "缺少 " + WeekLabel(period) + " 的周会纪要。可补充纪要，或选择“无纪要继续”（异常原因将一律写“原因待核实”）。",
				Detail:  map[string]any{"period": period},
			}, minutes, base), nil
		}
		found := make([]string, 0, len(minutes))
		for _, m := range minutes {
			if m.Week != nil {
				found = append(found, WeekLabel(*m.Week))
			} else {
				found = append(found, "周次无法识别")
			}
		}
		weeks := make([]*contract.IsoWeek, 0, len(minutes))
		for _, m := range minutes {
			weeks = append(weeks, m.Week)
		}
		return failValidation(&contract.PauseReason{
			Code: contract.PauseMinutesWeekMismatch,
			Message: "上传的纪要周次与统计周期不符：统计 " + WeekLabel(period) + "，但纪要标题显示为 " +
				strings.Join(found, "、") + "。请上传 " + WeekLabel(period) + " 的纪要，或选择“无纪要继续”。",
			Detail: map[string]any{"period": period, "found": weeks},
		}, minutes, base), nil
	}

	out := contract.ValidateSubmissionOutput{
		OK:         true,
		Sales:      sales,
		Dimensions: dimensions,
		Minutes:    minutes,
		Coverage:   coverage,
	}
	return out, nil
}

func salesDisplayName(pick *salesPick) string {
	if pick.Ref.Name != "" {
		return pick.Ref.Name
	}
	return filepath.Base(pick.Ref.Path)
}
