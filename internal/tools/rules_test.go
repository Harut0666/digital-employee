package tools

// design.md §10.2 的清洗与口径规则，用一份手工构造的小数据逐条验证
// （移植自 packages/tools/test/rules.test.ts）。

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"digital-employee/internal/contract"
)

type ruleRow struct {
	id         string
	date       string
	store      string
	channel    string
	product    string
	qty        int
	grossCents float64 // 元
	discCents  float64 // 元
	netCents   float64 // 元
	costCents  float64 // 元
}

func ruleID(store, date string) string {
	return "R" + strings.ReplaceAll(date, "-", "") + store + "-0001"
}

func ruleBase(store, date string, net float64) ruleRow {
	return ruleRow{
		id: ruleID(store, date), date: date, store: store, channel: "堂食自提", product: "P1",
		qty: 1, grossCents: net, netCents: net, costCents: net * 0.4,
	}
}

func ruleDays(from string, n int) []string {
	out := make([]string, 0, n)
	date := from
	for i := 0; i < n; i++ {
		out = append(out, date)
		next, err := AddDays(date, 1)
		if err != nil {
			panic(err)
		}
		date = next
	}
	return out
}

func ruleRows() []ruleRow {
	rows := make([]ruleRow, 0, 64)
	// A01：W36 每天 100，W37 每天 110（周合计 700 → 770，环比恰好 +10%）
	for _, d := range ruleDays("2026-08-31", 7) {
		rows = append(rows, ruleBase("A01", d, 100))
	}
	for _, d := range ruleDays("2026-09-07", 7) {
		rows = append(rows, ruleBase("A01", d, 110))
	}
	// A02（9/3 开业）：W36 从 9/3 起每天 50；W37 前 6 天每天 50（9/13 无数据），另有一笔退货
	for _, d := range ruleDays("2026-09-03", 4) {
		rows = append(rows, ruleBase("A02", d, 50))
	}
	for _, d := range ruleDays("2026-09-07", 6) {
		rows = append(rows, ruleBase("A02", d, 50))
	}
	// 数据质量
	rows = append(rows, ruleBase("A01", "2026-09-08", 110)) // 完全重复
	conflict := ruleBase("A01", "2026-09-09", 110)          // ID 冲突（同 ID 内容不同）
	conflict.grossCents, conflict.netCents, conflict.costCents = 120, 120, 48
	rows = append(rows, conflict)
	rows = append(rows, ruleRow{ // 未知门店
		id: "R-UNK-STORE", date: "2026-09-10", store: "ZZ99", channel: "堂食自提", product: "P1",
		qty: 1, grossCents: 30, netCents: 30, costCents: 12,
	})
	rows = append(rows, ruleRow{ // 未知商品
		id: "R-UNK-PROD", date: "2026-09-10", store: "A01", channel: "堂食自提", product: "P9",
		qty: 1, grossCents: 40, netCents: 40, costCents: 16,
	})
	// 金额对不上：原价 120 − 折扣 5 ≠ 实收 110，以实收为准
	for i := range rows {
		if rows[i].id == ruleID("A01", "2026-09-11") {
			rows[i].grossCents, rows[i].discCents, rows[i].netCents, rows[i].costCents = 120, 5, 110, 44
		}
	}
	rows = append(rows, ruleRow{ // 退货
		id: "R-RETURN", date: "2026-09-12", store: "A02", channel: "堂食自提", product: "P1",
		qty: -1, grossCents: -10, netCents: -10, costCents: -4,
	})
	return rows
}

func setSheet(t *testing.T, f *excelize.File, sheet string, header []any, rows []ruleRow, mapRow func(r ruleRow) []any) {
	t.Helper()
	var err error
	if sheet != "销售明细" {
		if _, err = f.NewSheet(sheet); err != nil {
			t.Fatalf("新建工作表 %s 失败：%v", sheet, err)
		}
	}
	if err = f.SetSheetRow(sheet, "A1", &header); err != nil {
		t.Fatalf("写表头失败：%v", err)
	}
	for i, r := range rows {
		values := mapRow(r)
		if err = f.SetSheetRow(sheet, "A"+strconv.Itoa(i+2), &values); err != nil {
			t.Fatalf("写第 %d 行失败：%v", i+2, err)
		}
	}
}

func writeRulesWorkbook(t *testing.T, path string, withSheets bool) {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", "销售明细"); err != nil {
		t.Fatalf("重命名工作表失败：%v", err)
	}
	salesHeader := []any{"记录ID", "销售日期", "门店编码", "门店名称", "渠道", "商品编码", "商品名称", "销售数量", "原价金额", "折扣金额", "实收金额", "成本金额"}
	setSheet(t, f, "销售明细", salesHeader, ruleRows(), func(r ruleRow) []any {
		return []any{r.id, r.date, r.store, r.store, r.channel, r.product, r.product, r.qty,
			r.grossCents, r.discCents, r.netCents, r.costCents}
	})

	if withSheets {
		storeHeader := []any{"门店编码", "门店名称", "区域", "城市", "店型", "面积(㎡)", "开业日期"}
		storeRows := []ruleRow{
			{store: "A01", date: "2020-01-01"},
			{store: "A02", date: "2026-09-03"},
		}
		setSheet(t, f, "门店信息", storeHeader, storeRows, func(r ruleRow) []any {
			name, city, storeType, area := "甲店", "上海", "商场店", 100
			if r.store == "A02" {
				name, storeType, area = "乙店", "街边店", 80
			}
			return []any{r.store, name, "华东", city, storeType, area, r.date}
		})

		productHeader := []any{"商品编码", "商品名称", "品类", "规格", "零售单价(元)", "单位成本(元)"}
		productRows := []ruleRow{{product: "P1"}, {product: "P2"}}
		setSheet(t, f, "商品信息", productHeader, productRows, func(r ruleRow) []any {
			if r.product == "P2" {
				return []any{"P2", "吐司", "面包", "条", 10, 4}
			}
			return []any{"P1", "拿铁", "饮品", "杯", 10, 4}
		})

		targetHeader := []any{"区域", "年份", "周次", "周起始日", "周结束日", "实收目标(元)"}
		targetRows := []ruleRow{{date: "W36:2026-08-31:2026-09-06:1000"}, {date: "W37:2026-09-07:2026-09-13:1200"}}
		setSheet(t, f, "销售目标", targetHeader, targetRows, func(r ruleRow) []any {
			parts := strings.Split(r.date, ":")
			var target int
			switch parts[0] {
			case "W36":
				target = 1000
			default:
				target = 1200
			}
			return []any{"华东", 2026, parts[0], parts[1], parts[2], target}
		})
	}

	if err := f.SaveAs(path); err != nil {
		t.Fatalf("保存工作簿失败：%v", err)
	}
}

func rulesDataset(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	xlsx := filepath.Join(dir, "销售明细.xlsx")
	writeRulesWorkbook(t, xlsx, true)
	loaded, err := LoadSales(xlsx, dir)
	if err != nil {
		t.Fatalf("load_sales 失败：%v", err)
	}
	return loaded.DatasetPath
}

func computeWeek(t *testing.T, datasetPath string, week int, threshold float64) contract.WeeklyFacts {
	t.Helper()
	facts, err := ComputeMetrics(contract.ComputeMetricsInput{
		DatasetPath:      datasetPath,
		Period:           contract.IsoWeek{Year: 2026, Week: week},
		AnomalyThreshold: threshold,
	})
	if err != nil {
		t.Fatalf("compute_metrics(W%d) 失败：%v", week, err)
	}
	return facts
}

func findStore(t *testing.T, facts contract.WeeklyFacts, code string) contract.StoreMetric {
	t.Helper()
	for _, s := range facts.Stores {
		if s.Code == code {
			return s
		}
	}
	t.Fatalf("缺少门店 %s", code)
	return contract.StoreMetric{}
}

func TestRulesDuplicateAndConflict(t *testing.T) {
	facts := computeWeek(t, rulesDataset(t), 37, 0.1)
	want := contract.DuplicateGroup{Store: "A01", Date: "2026-09-08", Count: 1, RevenueCents: 11000}
	if facts.Quality.Duplicates.Count != 1 || facts.Quality.Duplicates.RevenueCents != 11000 {
		t.Errorf("duplicates = %+v", facts.Quality.Duplicates)
	}
	if len(facts.Quality.Duplicates.Groups) != 1 || facts.Quality.Duplicates.Groups[0] != want {
		t.Errorf("duplicate groups = %+v", facts.Quality.Duplicates.Groups)
	}
	if facts.Quality.IDConflicts.Count != 1 || len(facts.Quality.IDConflicts.RecordIDs) != 1 ||
		facts.Quality.IDConflicts.RecordIDs[0] != ruleID("A01", "2026-09-09") {
		t.Errorf("idConflicts = %+v", facts.Quality.IDConflicts)
	}
	// 重复与冲突行都没有进入合计
	if got := findStore(t, facts, "A01").RevenueCents; got != 77000 {
		t.Errorf("A01 revenue = %d，期望 77000", got)
	}
}

func TestRulesAmountMismatch(t *testing.T) {
	facts := computeWeek(t, rulesDataset(t), 37, 0.1)
	if facts.Quality.AmountMismatch.Count != 1 {
		t.Errorf("amountMismatch = %+v", facts.Quality.AmountMismatch)
	}
	if got := findStore(t, facts, "A01").RevenueCents; got != 77000 {
		t.Errorf("A01 revenue = %d，期望 77000（以实收为准）", got)
	}
}

func TestRulesUnknownCodes(t *testing.T) {
	facts := computeWeek(t, rulesDataset(t), 37, 0.1)
	if facts.Quality.UnknownCodes.Count != 2 || facts.Quality.UnknownCodes.RevenueCents != 7000 {
		t.Errorf("unknownCodes = %+v", facts.Quality.UnknownCodes)
	}
	if facts.Company.RevenueCents != 106000 {
		t.Errorf("company revenue = %d，期望 106000", facts.Company.RevenueCents)
	}
}

func TestRulesReturns(t *testing.T) {
	facts := computeWeek(t, rulesDataset(t), 37, 0.1)
	want := contract.ReturnGroup{
		Store: "A02", StoreName: "乙店", Date: "2026-09-12", Product: "P1", ProductName: "拿铁",
		Count: 1, RevenueCents: -1000,
	}
	if facts.Quality.Returns.Count != 1 || facts.Quality.Returns.RevenueCents != -1000 {
		t.Errorf("returns = %+v", facts.Quality.Returns)
	}
	if len(facts.Quality.Returns.Groups) != 1 || facts.Quality.Returns.Groups[0] != want {
		t.Errorf("return groups = %+v", facts.Quality.Returns.Groups)
	}
	if got := findStore(t, facts, "A02").RevenueCents; got != 29000 {
		t.Errorf("A02 revenue = %d，期望 29000", got)
	}
}

func TestRulesStoreDays(t *testing.T) {
	datasetPath := rulesDataset(t)
	w36 := computeWeek(t, datasetPath, 36, 0.1)
	want36 := []contract.StoreDay{
		{Store: "A02", Date: "2026-08-31", Status: "not_open"},
		{Store: "A02", Date: "2026-09-01", Status: "not_open"},
		{Store: "A02", Date: "2026-09-02", Status: "not_open"},
	}
	if len(w36.Quality.StoreDays) != len(want36) {
		t.Fatalf("W36 storeDays = %+v", w36.Quality.StoreDays)
	}
	for i := range want36 {
		if w36.Quality.StoreDays[i] != want36[i] {
			t.Errorf("W36 storeDays[%d] = %+v，期望 %+v", i, w36.Quality.StoreDays[i], want36[i])
		}
	}
	w37 := computeWeek(t, datasetPath, 37, 0.1)
	want37 := []contract.StoreDay{{Store: "A02", Date: "2026-09-13", Status: "no_data"}}
	if len(w37.Quality.StoreDays) != 1 || w37.Quality.StoreDays[0] != want37[0] {
		t.Errorf("W37 storeDays = %+v", w37.Quality.StoreDays)
	}
}

func TestRulesAnomalyThresholdStrict(t *testing.T) {
	datasetPath := rulesDataset(t)
	at := computeWeek(t, datasetPath, 37, 0.1)
	a01 := findStore(t, at, "A01")
	if a01.Wow == nil || *a01.Wow != 0.1 {
		t.Fatalf("A01 wow = %v，期望 0.1", a01.Wow)
	}
	for _, a := range at.Anomalies {
		if a.ID == "store:A01" {
			t.Errorf("恰好 +10.0%% 不应算异常")
		}
	}
	below := computeWeek(t, datasetPath, 37, 0.099)
	found := false
	for _, a := range below.Anomalies {
		if a.ID == "store:A01" {
			found = true
		}
	}
	if !found {
		t.Errorf("阈值降到 0.099 后 A01 应算异常")
	}
}

func TestRulesBaseWeekMissingDays(t *testing.T) {
	facts := computeWeek(t, rulesDataset(t), 37, 0.1)
	// A02 环比 = 290 / 200 - 1；上周只有 not_open，没有 no_data
	var a02 *contract.Anomaly
	for i := range facts.Anomalies {
		if facts.Anomalies[i].ID == "store:A02" {
			a02 = &facts.Anomalies[i]
		}
	}
	if a02 == nil {
		t.Fatalf("缺少 A02 异常：%+v", facts.Anomalies)
	}
	if a02.BaseWeekHasMissingDays {
		t.Errorf("上周只有未开业时不应标“基数偏低”")
	}
	for _, d := range facts.Quality.PrevWeekStoreDays {
		if d.Status != "not_open" {
			t.Errorf("prevWeekStoreDays = %+v", facts.Quality.PrevWeekStoreDays)
		}
	}
}

func TestRulesTargetsMarginAndCategories(t *testing.T) {
	facts := computeWeek(t, rulesDataset(t), 37, 0.1)
	region := facts.Regions["华东"]
	if region.TargetCents == nil || *region.TargetCents != 120000 {
		t.Errorf("华东 target = %v", region.TargetCents)
	}
	if facts.Company.TargetCents == nil || *facts.Company.TargetCents != 120000 {
		t.Errorf("company target = %v", facts.Company.TargetCents)
	}
	if facts.Company.Achievement == nil || *facts.Company.Achievement != 106000.0/120000.0 {
		t.Errorf("achievement = %v", facts.Company.Achievement)
	}
	if facts.Company.DeliveryShare == nil || *facts.Company.DeliveryShare != 0 {
		t.Errorf("deliveryShare = %v", facts.Company.DeliveryShare)
	}
	if facts.Company.GrossMarginRate == nil || *facts.Company.GrossMarginRate != 0.6 {
		t.Errorf("grossMarginRate = %v", facts.Company.GrossMarginRate)
	}
	var drinks, bread *contract.CategoryMetric
	for i := range facts.Categories {
		switch facts.Categories[i].Category {
		case "饮品":
			drinks = &facts.Categories[i]
		case "面包":
			bread = &facts.Categories[i]
		}
	}
	if drinks == nil || drinks.RevenueCents != 106000 || drinks.PrevRevenueCents == nil || *drinks.PrevRevenueCents != 90000 {
		t.Errorf("饮品 = %+v", drinks)
	}
	if bread == nil || bread.RevenueCents != 0 {
		t.Errorf("面包 = %+v", bread)
	}
}

func TestRulesDatasetWithoutDimensions(t *testing.T) {
	dir := t.TempDir()
	xlsx := filepath.Join(dir, "无维表.xlsx")
	writeRulesWorkbook(t, xlsx, false)

	// validate 报缺维表（xlsx 缺工作表）
	validated, err := ValidateSubmission(contract.ValidateSubmissionInput{
		Period:      contract.IsoWeek{Year: 2026, Week: 37},
		Attachments: []contract.AttachmentRef{{Name: "无维表.xlsx", Path: xlsx}},
	})
	if err != nil {
		t.Fatalf("validate 失败：%v", err)
	}
	if validated.Pause == nil || validated.Pause.Code != contract.PauseMissingDimensions {
		t.Fatalf("期望 missing_dimensions，实际 %+v", validated.Pause)
	}

	loaded, err := LoadSales(xlsx, dir)
	if err != nil {
		t.Fatalf("load_sales 失败：%v", err)
	}
	_, err = ComputeMetrics(contract.ComputeMetricsInput{
		DatasetPath: loaded.DatasetPath, Period: contract.IsoWeek{Year: 2026, Week: 37}, AnomalyThreshold: 0.1,
	})
	if err == nil || !strings.Contains(err.Error(), "维表") {
		t.Errorf("没有维表的数据集应报错，实际 %v", err)
	}
}
