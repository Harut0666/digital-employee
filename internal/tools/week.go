package tools

// ISO 周与日期工具（移植自 packages/tools/src/week.ts）。
// 日期一律用 "YYYY-MM-DD" 字符串，内部用 UTC 计算，避免时区漂移。

import (
	"fmt"
	"time"

	"digital-employee/internal/contract"
)

const day = 24 * time.Hour

// ParseDate 解析 "YYYY-MM-DD"。
func ParseDate(s string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02", s, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("日期格式不正确：%s", s)
	}
	return t, nil
}

// FormatDate 格式化成 "YYYY-MM-DD"。
func FormatDate(t time.Time) string { return t.UTC().Format("2006-01-02") }

// AddDays 日期加减天数。
func AddDays(date string, days int) (string, error) {
	t, err := ParseDate(date)
	if err != nil {
		return "", err
	}
	return FormatDate(t.AddDate(0, 0, days)), nil
}

// mondayOf 第 week 周的周一（ISO：包含 1 月 4 日的那一周为第 1 周）。
func mondayOf(year, week int) time.Time {
	jan4 := time.Date(year, time.January, 4, 0, 0, 0, 0, time.UTC)
	dow := (int(jan4.Weekday()) + 6) % 7 // 周一=0
	return jan4.AddDate(0, 0, -dow+(week-1)*7)
}

// PeriodRangeOf 统计周期：周一 from / 周日 to。
func PeriodRangeOf(w contract.IsoWeek) contract.PeriodRange {
	monday := mondayOf(w.Year, w.Week)
	return contract.PeriodRange{
		IsoWeek: contract.IsoWeek{Year: w.Year, Week: w.Week},
		From:    FormatDate(monday),
		To:      FormatDate(monday.AddDate(0, 0, 6)),
	}
}

// IsoWeekOf 日期所在的 ISO 周。
func IsoWeekOf(date string) (contract.IsoWeek, error) {
	t, err := ParseDate(date)
	if err != nil {
		return contract.IsoWeek{}, err
	}
	year, week := t.ISOWeek()
	return contract.IsoWeek{Year: year, Week: week}, nil
}

// ShiftWeek 周次平移 delta 周。
func ShiftWeek(w contract.IsoWeek, delta int) contract.IsoWeek {
	monday := mondayOf(w.Year, w.Week).AddDate(0, 0, delta*7)
	year, week := monday.ISOWeek()
	return contract.IsoWeek{Year: year, Week: week}
}

// WeekKey 形如 2026-W44。
func WeekKey(w contract.IsoWeek) string {
	return fmt.Sprintf("%d-W%02d", w.Year, w.Week)
}

// DatesBetween 闭区间内的所有日期。
func DatesBetween(from, to string) ([]string, error) {
	start, err := ParseDate(from)
	if err != nil {
		return nil, err
	}
	end, err := ParseDate(to)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, 7)
	for t := start; !t.After(end); t = t.Add(day) {
		out = append(out, FormatDate(t))
	}
	return out, nil
}

// datesBetween 内部使用：调用方保证日期合法。
func datesBetween(from, to string) []string {
	out, _ := DatesBetween(from, to)
	return out
}

// WeekLabel 形如 “2026 年第 44 周”。
func WeekLabel(w contract.IsoWeek) string {
	return fmt.Sprintf("%d 年第 %d 周", w.Year, w.Week)
}
