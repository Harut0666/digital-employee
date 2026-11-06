package tools

// 链式单测：用 samples/scenario-a 真实数据跑 validate → load_sales → compute_metrics
// → parse_minutes → detect_contradictions，并与 TS 版产出的 golden（testdata/）逐字段对拍。
//
// 金额单位：分；比例：小数（0.123 = 12.3%）。golden 由 feat/electrobun-shell 上的
// TypeScript 版在同一样例数据上跑出，是「数据一致性」的基准。

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"digital-employee/internal/contract"
)

func samplesDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "samples", "scenario-a"))
	if err != nil {
		t.Fatalf("定位样例目录失败：%v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("样例目录不存在，跳过：%s", abs)
	}
	return abs
}

func readJSON(t *testing.T, path string) any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", path, err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("解析 %s 失败：%v", path, err)
	}
	return v
}

func toJSONValue(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("解析序列化结果失败：%v", err)
	}
	return out
}

// normalizeSamplePaths 把 golden 里的样例绝对路径换成本机样例目录，便于跨机器对拍。
func normalizeSamplePaths(v any, samples string) any {
	parent := filepath.Dir(samples)
	var walk func(x any) any
	walk = func(x any) any {
		switch val := x.(type) {
		case string:
			idx := strings.Index(val, "scenario-a")
			if idx < 0 {
				return val
			}
			return filepath.Join(parent, val[idx:])
		case []any:
			out := make([]any, len(val))
			for i, item := range val {
				out[i] = walk(item)
			}
			return out
		case map[string]any:
			out := make(map[string]any, len(val))
			for k, item := range val {
				out[k] = walk(item)
			}
			return out
		}
		return x
	}
	return walk(v)
}

func assertJSONEqual(t *testing.T, label string, want, got any) {
	t.Helper()
	if reflect.DeepEqual(want, got) {
		return
	}
	wantJSON, _ := json.MarshalIndent(want, "", "  ")
	gotJSON, _ := json.MarshalIndent(got, "", "  ")
	t.Fatalf("%s 与 TS golden 不一致\n--- want ---\n%s\n--- got ---\n%s", label, wantJSON, gotJSON)
}

func compareGolden(t *testing.T, goldenPath string, got any) {
	t.Helper()
	samples := samplesDir(t)
	want := normalizeSamplePaths(readJSON(t, goldenPath), samples)
	actual := normalizeSamplePaths(toJSONValue(t, got), samples)
	assertJSONEqual(t, filepath.Base(goldenPath), want, actual)
}

// ---------------------------------------------------------------------------
// validate_submission
// ---------------------------------------------------------------------------

func TestValidateSubmissionGolden(t *testing.T) {
	samples := samplesDir(t)
	att := func(name string) contract.AttachmentRef {
		return contract.AttachmentRef{Name: name, Path: filepath.Join(samples, name)}
	}
	w44 := contract.IsoWeek{Year: 2026, Week: 44}
	cases := []struct {
		golden string
		input  contract.ValidateSubmissionInput
	}{
		{"validate-ok.json", contract.ValidateSubmissionInput{
			Period: w44, Attachments: []contract.AttachmentRef{att("销售明细.xlsx"), att("周会纪要_2026W44.docx")},
		}},
		{"validate-missing-sales.json", contract.ValidateSubmissionInput{
			Period: w44, Attachments: []contract.AttachmentRef{att("周会纪要_2026W44.docx")},
		}},
		{"validate-out-of-range.json", contract.ValidateSubmissionInput{
			Period: contract.IsoWeek{Year: 2026, Week: 46}, Attachments: []contract.AttachmentRef{att("销售明细.xlsx")},
		}},
		{"validate-csv-only.json", contract.ValidateSubmissionInput{
			Period: w44, Attachments: []contract.AttachmentRef{att("销售明细.csv"), att("周会纪要_2026W44.docx")},
		}},
		{"validate-week-mismatch.json", contract.ValidateSubmissionInput{
			Period: w44, Attachments: []contract.AttachmentRef{att("销售明细.xlsx"), att("周会纪要_2026W43.docx")},
		}},
		{"validate-no-minutes.json", contract.ValidateSubmissionInput{
			Period: w44, Attachments: []contract.AttachmentRef{att("销售明细.xlsx")},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			out, err := ValidateSubmission(tc.input)
			if err != nil {
				t.Fatalf("validate_submission 返回错误：%v", err)
			}
			compareGolden(t, filepath.Join("testdata", tc.golden), out)
		})
	}
}

func TestValidateAllowMissingMinutes(t *testing.T) {
	samples := samplesDir(t)
	input := contract.ValidateSubmissionInput{
		Period:              contract.IsoWeek{Year: 2026, Week: 44},
		Attachments:         []contract.AttachmentRef{{Name: "销售明细.xlsx", Path: filepath.Join(samples, "销售明细.xlsx")}, {Name: "周会纪要_2026W43.docx", Path: filepath.Join(samples, "周会纪要_2026W43.docx")}},
		AllowMissingMinutes: true,
	}
	out, err := ValidateSubmission(input)
	if err != nil {
		t.Fatalf("validate_submission 返回错误：%v", err)
	}
	if !out.OK {
		t.Fatalf("选择无纪要继续后应通过，实际 pause=%+v", out.Pause)
	}
}

// ---------------------------------------------------------------------------
// load_sales
// ---------------------------------------------------------------------------

func loadSampleDataset(t *testing.T) string {
	t.Helper()
	return sharedSampleLoad(t).DatasetPath
}

func jsonDigest(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

func TestLoadSalesGolden(t *testing.T) {
	out := sharedSampleLoad(t)
	if out.Rows != 44820 {
		t.Errorf("rows = %d，期望 44820", out.Rows)
	}
	wantCoverage := contract.DateRange{From: "2026-08-31", To: "2026-11-01"}
	if out.Coverage != wantCoverage {
		t.Errorf("coverage = %+v，期望 %+v", out.Coverage, wantCoverage)
	}
	if _, err := os.Stat(out.DatasetPath); err != nil {
		t.Errorf("数据集文件不存在：%s", out.DatasetPath)
	}

	// 清洗结果与 TS 版逐字节一致（字段顺序也一致，才能对上 JSON 摘要）
	digests := readJSON(t, filepath.Join("testdata", "dataset-digests.json")).(map[string]any)
	ds, err := readDataset(out.DatasetPath)
	if err != nil {
		t.Fatalf("读取数据集失败：%v", err)
	}
	if ds.Source.Name != "销售明细.xlsx" || ds.Source.Kind != "xlsx" || ds.Source.SHA256 != "ff7f1866cae6f35f64318e8652ea8815d7aa82dea971e6a072c96a9d51eb9e8b" {
		t.Errorf("source = %+v", ds.Source)
	}
	if got := jsonDigest(t, ds.Rows); got != digests["rowsSha256"] {
		t.Errorf("rows 摘要不一致：got %s want %v", got, digests["rowsSha256"])
	}
	if got := jsonDigest(t, ds.Removed); got != digests["removedSha256"] {
		t.Errorf("removed 摘要不一致：got %s want %v", got, digests["removedSha256"])
	}
	if got := jsonDigest(t, ds.Stores); got != digests["storesSha256"] {
		t.Errorf("stores 摘要不一致：got %s want %v", got, digests["storesSha256"])
	}
	if got := jsonDigest(t, ds.Products); got != digests["productsSha256"] {
		t.Errorf("products 摘要不一致：got %s want %v", got, digests["productsSha256"])
	}
	if got := jsonDigest(t, ds.Targets); got != digests["targetsSha256"] {
		t.Errorf("targets 摘要不一致：got %s want %v", got, digests["targetsSha256"])
	}
}

func TestLoadSalesIdempotent(t *testing.T) {
	salesPath := filepath.Join(samplesDir(t), "销售明细.xlsx")
	outDir := t.TempDir()
	first, err := LoadSales(salesPath, outDir)
	if err != nil {
		t.Fatalf("第一次 load_sales 失败：%v", err)
	}
	info, err := os.Stat(first.DatasetPath)
	if err != nil {
		t.Fatalf("stat 数据集失败：%v", err)
	}
	second, err := LoadSales(salesPath, outDir)
	if err != nil {
		t.Fatalf("第二次 load_sales 失败：%v", err)
	}
	if second != first {
		t.Errorf("第二次结果不一致：%+v vs %+v", second, first)
	}
	info2, err := os.Stat(second.DatasetPath)
	if err != nil {
		t.Fatalf("stat 数据集失败：%v", err)
	}
	if !info.ModTime().Equal(info2.ModTime()) {
		t.Errorf("幂等调用不应重写数据集文件")
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("读目录失败：%v", err)
	}
	if len(entries) != 1 {
		t.Errorf("outDir 下应只有 1 个文件，实际 %d 个", len(entries))
	}
}

func TestLoadSalesCsv(t *testing.T) {
	samples := samplesDir(t)
	out, err := LoadSales(filepath.Join(samples, "销售明细.csv"), t.TempDir())
	if err != nil {
		t.Fatalf("load_sales(csv) 失败：%v", err)
	}
	if out.Rows != 44820 {
		t.Errorf("rows = %d，期望 44820", out.Rows)
	}
	if want := (contract.DateRange{From: "2026-08-31", To: "2026-11-01"}); out.Coverage != want {
		t.Errorf("coverage = %+v，期望 %+v", out.Coverage, want)
	}
	ds, err := readDataset(out.DatasetPath)
	if err != nil {
		t.Fatalf("读取数据集失败：%v", err)
	}
	if ds.Stores != nil || ds.Products != nil || ds.Targets != nil {
		t.Errorf("CSV 数据集不应带维表")
	}
	// 只有 CSV 的数据集不能算指标
	_, err = ComputeMetrics(contract.ComputeMetricsInput{
		DatasetPath: out.DatasetPath, Period: contract.IsoWeek{Year: 2026, Week: 44}, AnomalyThreshold: 0.1,
	})
	if err == nil || !strings.Contains(err.Error(), "维表") {
		t.Errorf("期望报“缺维表”，实际 %v", err)
	}
}

// ---------------------------------------------------------------------------
// compute_metrics
// ---------------------------------------------------------------------------

func TestComputeMetricsGolden(t *testing.T) {
	datasetPath := loadSampleDataset(t)
	for _, week := range []int{36, 42, 43, 44} {
		t.Run(fmt.Sprintf("W%d", week), func(t *testing.T) {
			facts, err := ComputeMetrics(contract.ComputeMetricsInput{
				DatasetPath:      datasetPath,
				Period:           contract.IsoWeek{Year: 2026, Week: week},
				AnomalyThreshold: 0.1,
			})
			if err != nil {
				t.Fatalf("compute_metrics 失败：%v", err)
			}
			compareGolden(t, filepath.Join("testdata", fmt.Sprintf("facts-W%d.json", week)), facts)
		})
	}
}

func TestComputeMetricsOutOfRange(t *testing.T) {
	datasetPath := loadSampleDataset(t)
	_, err := ComputeMetrics(contract.ComputeMetricsInput{
		DatasetPath:      datasetPath,
		Period:           contract.IsoWeek{Year: 2026, Week: 46},
		AnomalyThreshold: 0.1,
	})
	if err == nil {
		t.Fatal("超出数据覆盖范围的周期应报错")
	}
}

// 关键数字直接断言一遍（与 samples/cases/A-01.json 的 expect.checks 对齐）。
func TestComputeMetricsKeyNumbers(t *testing.T) {
	datasetPath := loadSampleDataset(t)
	facts, err := ComputeMetrics(contract.ComputeMetricsInput{
		DatasetPath:      datasetPath,
		Period:           contract.IsoWeek{Year: 2026, Week: 44},
		AnomalyThreshold: 0.1,
	})
	if err != nil {
		t.Fatalf("compute_metrics 失败：%v", err)
	}
	if facts.Company.RevenueCents != 57363487 {
		t.Errorf("company.revenueCents = %d，期望 57363487", facts.Company.RevenueCents)
	}
	if facts.Period.From != "2026-10-26" || facts.Period.To != "2026-11-01" {
		t.Errorf("period = %+v", facts.Period)
	}
	storeWow := map[string]float64{"HZ01": -0.3831871934125745, "SH02": -0.17936710817616908,
		"SZ01": -0.15323177373900645, "SZ02": -0.17253297923310076}
	for code, want := range storeWow {
		var found bool
		for _, s := range facts.Stores {
			if s.Code == code {
				found = true
				if s.Wow == nil || *s.Wow != want {
					t.Errorf("store %s wow = %v，期望 %v", code, s.Wow, want)
				}
			}
		}
		if !found {
			t.Errorf("缺少门店 %s", code)
		}
	}
	if facts.Quality.Returns.Count != 11 || facts.Quality.Returns.RevenueCents != -26000 {
		t.Errorf("退货 = %d 笔 / %d 分，期望 11 / -26000", facts.Quality.Returns.Count, facts.Quality.Returns.RevenueCents)
	}
	if len(facts.Quality.StoreDays) != 3 {
		t.Errorf("storeDays = %+v", facts.Quality.StoreDays)
	}
	if facts.Trend[3].CompanyCents != facts.Company.RevenueCents {
		t.Errorf("趋势末项应等于本周实收")
	}
	if facts.Company.DeliveryShare == nil || *facts.Company.DeliveryShare != 0.3183786927039495 {
		t.Errorf("deliveryShare = %v", facts.Company.DeliveryShare)
	}
}

// ---------------------------------------------------------------------------
// parse_minutes
// ---------------------------------------------------------------------------

func TestParseMinutesGolden(t *testing.T) {
	samples := samplesDir(t)
	for _, week := range []int{42, 43, 44} {
		t.Run(fmt.Sprintf("W%d", week), func(t *testing.T) {
			out, err := ParseMinutes([]string{filepath.Join(samples, fmt.Sprintf("周会纪要_2026W%d.docx", week))})
			if err != nil {
				t.Fatalf("parse_minutes 失败：%v", err)
			}
			compareGolden(t, filepath.Join("testdata", fmt.Sprintf("minutes-W%d.json", week)), out)
		})
	}
}

func TestParseMinutesMultiple(t *testing.T) {
	samples := samplesDir(t)
	out, err := ParseMinutes([]string{
		filepath.Join(samples, "周会纪要_2026W42.docx"),
		filepath.Join(samples, "周会纪要_2026W43.docx"),
	})
	if err != nil {
		t.Fatalf("parse_minutes 失败：%v", err)
	}
	if len(out.Minutes) != 2 {
		t.Fatalf("minutes 数量 = %d", len(out.Minutes))
	}
	if out.Minutes[0].Week == nil || out.Minutes[0].Week.Week != 42 {
		t.Errorf("第一份周次 = %+v", out.Minutes[0].Week)
	}
	if out.Minutes[0].Paragraphs[0].ID != "W42-p01" {
		t.Errorf("段落 ID = %s", out.Minutes[0].Paragraphs[0].ID)
	}
}

// ---------------------------------------------------------------------------
// detect_contradictions
// ---------------------------------------------------------------------------

func testThresholds() contract.ContradictionThresholds {
	return contract.ContradictionThresholds{FlatTolerance: 0.05, DirectionMin: 0.02, MagnitudeTolerance: 0.1}
}

func goldenClaims() []contract.Claim {
	week := contract.IsoWeek{Year: 2026, Week: 44}
	quote := "（测试手写说法）"
	mag := func(v float64) *float64 { return &v }
	base := func(level contract.ClaimSubjectLevel, key string, dir contract.ClaimDirection) contract.Claim {
		return contract.Claim{
			Subject:    contract.ClaimSubject{Level: level, Key: key},
			Metric:     "wow",
			Direction:  dir,
			SourceWeek: week,
			Quote:      quote,
		}
	}
	c := func(level contract.ClaimSubjectLevel, key string, dir contract.ClaimDirection, q string) contract.Claim {
		claim := base(level, key, dir)
		claim.Quote = q
		return claim
	}
	withMag := func(claim contract.Claim, v float64) contract.Claim {
		claim.Magnitude = mag(v)
		return claim
	}
	return []contract.Claim{
		c(contract.ClaimRegion, "华南", contract.ClaimFlat, "本周华南整体销售与上周基本持平"),
		c(contract.ClaimRegion, "华东", contract.ClaimDown, "本周华东受门店停业和竞品开业影响下滑较多"),
		c(contract.ClaimStore, "HZ01", contract.ClaimDown, "杭州湖滨店 10 月 27 日至 29 日因商场消防改造停业三天"),
		c(contract.ClaimStore, "SH02", contract.ClaimDown, "上海静安店对面竞品 10 月 24 日开业"),
		base(contract.ClaimRegion, "华东", contract.ClaimUp),
		base(contract.ClaimRegion, "华北", contract.ClaimFlat),
		base(contract.ClaimRegion, "西南", contract.ClaimUp),
		withMag(base(contract.ClaimRegion, "华东", contract.ClaimDown), 0.3),
		withMag(base(contract.ClaimRegion, "华东", contract.ClaimDown), 0.2),
		base(contract.ClaimStore, "SH02", contract.ClaimFlat),
		base(contract.ClaimCompany, "company", contract.ClaimFlat),
		base(contract.ClaimRegion, "华南", contract.ClaimUp),
		base(contract.ClaimRegion, "客流", contract.ClaimFlat),
		base(contract.ClaimCategory, "口感", contract.ClaimDown),
	}
}

func TestDetectContradictionsGolden(t *testing.T) {
	datasetPath := loadSampleDataset(t)
	facts, err := ComputeMetrics(contract.ComputeMetricsInput{
		DatasetPath: datasetPath, Period: contract.IsoWeek{Year: 2026, Week: 44}, AnomalyThreshold: 0.1,
	})
	if err != nil {
		t.Fatalf("compute_metrics 失败：%v", err)
	}
	out, err := DetectContradictions(contract.DetectContradictionsInput{
		Claims: goldenClaims(), Facts: facts, Thresholds: testThresholds(),
	})
	if err != nil {
		t.Fatalf("detect_contradictions 失败：%v", err)
	}
	compareGolden(t, filepath.Join("testdata", "contradictions-W44.json"), out)
}

func TestDetectContradictionsUncomparable(t *testing.T) {
	datasetPath := loadSampleDataset(t)
	facts, err := ComputeMetrics(contract.ComputeMetricsInput{
		DatasetPath: datasetPath, Period: contract.IsoWeek{Year: 2026, Week: 36}, AnomalyThreshold: 0.1,
	})
	if err != nil {
		t.Fatalf("compute_metrics 失败：%v", err)
	}
	out, err := DetectContradictions(contract.DetectContradictionsInput{
		Claims: []contract.Claim{{
			Subject:    contract.ClaimSubject{Level: contract.ClaimCompany, Key: "company"},
			Metric:     "wow",
			Direction:  contract.ClaimFlat,
			SourceWeek: contract.IsoWeek{Year: 2026, Week: 36},
			Quote:      "（测试手写说法）",
		}},
		Facts:      facts,
		Thresholds: testThresholds(),
	})
	if err != nil {
		t.Fatalf("detect_contradictions 失败：%v", err)
	}
	compareGolden(t, filepath.Join("testdata", "contradictions-W36.json"), out)
}
