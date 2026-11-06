package tools

// 读取销售数据源（移植自 packages/tools/src/sales/source.ts）：
// xlsx 4 个工作表，或 csv 单表。金额读入后立刻转成整数“分”。

import (
	"encoding/csv"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xuri/excelize/v2"

	"digital-employee/internal/contract"
)

// RawSale 一条销售明细（金额单位：分）。
type RawSale struct {
	ID            string
	Date          string
	Store         string
	StoreName     string
	Channel       string
	Product       string
	ProductName   string
	Qty           int
	GrossCents    int64
	DiscountCents int64
	NetCents      int64
	CostCents     int64
}

// StoreInfo 门店维表。
type StoreInfo struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Region    string `json:"region"`
	City      string `json:"city"`
	StoreType string `json:"storeType"`
	OpenDate  string `json:"openDate"`
}

// ProductInfo 商品维表。
type ProductInfo struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	Category   string `json:"category"`
	PriceCents int64  `json:"priceCents"`
	CostCents  int64  `json:"costCents"`
}

// TargetInfo 销售目标维表。
type TargetInfo struct {
	Region      string `json:"region"`
	Year        int    `json:"year"`
	Week        int    `json:"week"`
	TargetCents int64  `json:"targetCents"`
}

// SalesSource 一个销售数据源。
type SalesSource struct {
	Kind     string // xlsx | csv
	Sales    []RawSale
	Stores   []StoreInfo   // 只有 csv 时为 nil
	Products []ProductInfo // 只有 csv 时为 nil
	Targets  []TargetInfo  // 只有 csv 时为 nil
}

// toCents 元 → 分，四舍五入（加极小量抵消二进制浮点误差，如 1.005）。
func toCents(v float64) int64 {
	eps := 1e-7
	if v < 0 {
		eps = -1e-7
	}
	return int64(math.Round(v*100 + eps))
}

// slice10 等价于 JS 的 String(...).slice(0, 10)（日期只取前 10 位）。
func slice10(s string) string {
	if len(s) <= 10 {
		return s
	}
	return s[:10]
}

// escapeNumber 等价于 JS 的 String(number)，用于把数字单元格当字符串取。
func escapeNumber(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e21 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// ---- 单元格读取 ----

type sheetCell struct {
	raw       string
	formatted string
}

func (c sheetCell) str() string {
	if s := strings.TrimSpace(c.raw); s != "" {
		return s
	}
	return strings.TrimSpace(c.formatted)
}

func (c sheetCell) number() float64 {
	if f, err := strconv.ParseFloat(strings.TrimSpace(c.raw), 64); err == nil {
		return f
	}
	if f, err := strconv.ParseFloat(strings.TrimSpace(c.formatted), 64); err == nil {
		return f
	}
	return 0
}

var dateLayouts = []string{
	"2006-01-02",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-1-2",
	"2006/01/02",
	"2006/1/2",
	"2006/1/2 15:04:05",
	"2006年1月2日",
	"1/2/06",
	"1/2/2006",
	"01-02-06",
	"02-Jan-06",
}

func parseDateLoose(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return FormatDate(t), true
		}
	}
	return "", false
}

func (c sheetCell) date() string {
	raw := strings.TrimSpace(c.raw)
	if raw == "" {
		return slice10(strings.TrimSpace(c.formatted))
	}
	if _, err := strconv.ParseFloat(raw, 64); err == nil && !strings.ContainsAny(raw, "-/:") {
		// 数字单元格：只有显示出日期样式的才按日期处理（与 exceljs 返回 Date 的规则一致）
		if d, ok := parseDateLoose(c.formatted); ok {
			return d
		}
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			return slice10(escapeNumber(f))
		}
		return slice10(raw)
	}
	return slice10(raw)
}

func readSheetRows(f *excelize.File, name string) ([][]sheetCell, error) {
	if _, err := f.GetSheetIndex(name); err != nil {
		return nil, fmt.Errorf("缺少“%s”工作表", name)
	}
	rawRows, err := f.GetRows(name, excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, err
	}
	fmtRows, err := f.GetRows(name)
	if err != nil {
		return nil, err
	}
	out := make([][]sheetCell, 0, len(rawRows))
	for i, rawRow := range rawRows {
		var fmtRow []string
		if i < len(fmtRows) {
			fmtRow = fmtRows[i]
		}
		n := len(rawRow)
		if len(fmtRow) > n {
			n = len(fmtRow)
		}
		row := make([]sheetCell, n)
		for j := 0; j < n; j++ {
			if j < len(rawRow) {
				row[j].raw = rawRow[j]
			}
			if j < len(fmtRow) {
				row[j].formatted = fmtRow[j]
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func isEmptyRow(row []sheetCell) bool {
	for _, c := range row {
		if c.str() != "" {
			return false
		}
	}
	return true
}

// headerIndex 用第一行做列名 → 序号映射（重名列后者覆盖，与 JS 对象赋值一致）。
func headerIndex(row []sheetCell) map[string]int {
	idx := make(map[string]int, len(row))
	for i, c := range row {
		idx[c.str()] = i
	}
	return idx
}

// xlsxRow 按列名取一格。
type xlsxRow struct {
	idx  map[string]int
	cell []sheetCell
}

func (r xlsxRow) str(name string) string {
	i, ok := r.idx[name]
	if !ok || i >= len(r.cell) {
		return ""
	}
	return r.cell[i].str()
}

func (r xlsxRow) num(name string) float64 {
	i, ok := r.idx[name]
	if !ok || i >= len(r.cell) {
		return 0
	}
	return r.cell[i].number()
}

func (r xlsxRow) date(name string) string {
	i, ok := r.idx[name]
	if !ok || i >= len(r.cell) {
		return ""
	}
	return r.cell[i].date()
}

// csvRow 按列名取一格。
type csvRow struct {
	idx map[string]int
	rec []string
}

func (r csvRow) str(name string) string {
	i, ok := r.idx[name]
	if !ok || i >= len(r.rec) {
		return ""
	}
	return strings.TrimSpace(r.rec[i])
}

func (r csvRow) num(name string) float64 {
	s := strings.TrimSpace(r.str(name))
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

func (r csvRow) date(name string) string { return slice10(r.str(name)) }

// saleRowReader 销售明细行的统一取值接口。
type saleRowReader interface {
	str(name string) string
	num(name string) float64
	date(name string) string
}

func toSale(r saleRowReader) RawSale {
	return RawSale{
		ID:            r.str("记录ID"),
		Date:          r.date("销售日期"),
		Store:         r.str("门店编码"),
		StoreName:     r.str("门店名称"),
		Channel:       r.str("渠道"),
		Product:       r.str("商品编码"),
		ProductName:   r.str("商品名称"),
		Qty:           int(r.num("销售数量")),
		GrossCents:    toCents(r.num("原价金额")),
		DiscountCents: toCents(r.num("折扣金额")),
		NetCents:      toCents(r.num("实收金额")),
		CostCents:     toCents(r.num("成本金额")),
	}
}

var sheetNames = struct {
	Sales    string
	Stores   string
	Products string
	Targets  string
}{"销售明细", "门店信息", "商品信息", "销售目标"}

func readXlsx(path string) (*SalesSource, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	salesRows, err := readSheetRows(f, sheetNames.Sales)
	if err != nil {
		return nil, err
	}
	src := &SalesSource{Kind: "xlsx"}
	if len(salesRows) > 0 {
		idx := headerIndex(salesRows[0])
		src.Sales = make([]RawSale, 0, len(salesRows)-1)
		for _, row := range salesRows[1:] {
			if isEmptyRow(row) {
				continue
			}
			src.Sales = append(src.Sales, toSale(xlsxRow{idx: idx, cell: row}))
		}
	}

	if rows, err := readSheetRows(f, sheetNames.Stores); err == nil {
		src.Stores = make([]StoreInfo, 0, len(rows))
		if len(rows) > 0 {
			idx := headerIndex(rows[0])
			for _, row := range rows[1:] {
				if isEmptyRow(row) {
					continue
				}
				r := xlsxRow{idx: idx, cell: row}
				src.Stores = append(src.Stores, StoreInfo{
					Code:      r.str("门店编码"),
					Name:      r.str("门店名称"),
					Region:    r.str("区域"),
					City:      r.str("城市"),
					StoreType: r.str("店型"),
					OpenDate:  r.date("开业日期"),
				})
			}
		}
	}
	if rows, err := readSheetRows(f, sheetNames.Products); err == nil {
		src.Products = make([]ProductInfo, 0, len(rows))
		if len(rows) > 0 {
			idx := headerIndex(rows[0])
			for _, row := range rows[1:] {
				if isEmptyRow(row) {
					continue
				}
				r := xlsxRow{idx: idx, cell: row}
				src.Products = append(src.Products, ProductInfo{
					Code:       r.str("商品编码"),
					Name:       r.str("商品名称"),
					Category:   r.str("品类"),
					PriceCents: toCents(r.num("零售单价(元)")),
					CostCents:  toCents(r.num("单位成本(元)")),
				})
			}
		}
	}
	if rows, err := readSheetRows(f, sheetNames.Targets); err == nil {
		src.Targets = make([]TargetInfo, 0, len(rows))
		if len(rows) > 0 {
			idx := headerIndex(rows[0])
			for _, row := range rows[1:] {
				if isEmptyRow(row) {
					continue
				}
				r := xlsxRow{idx: idx, cell: row}
				src.Targets = append(src.Targets, TargetInfo{
					Region:      r.str("区域"),
					Year:        int(r.num("年份")),
					Week:        weekNumber(r.str("周次")),
					TargetCents: toCents(r.num("实收目标(元)")),
				})
			}
		}
	}
	return src, nil
}

var weekDigits = regexp.MustCompile(`(\d+)`)

// weekNumber 从 "W36" 这类文本里取出周次，识别不出为 0。
func weekNumber(s string) int {
	m := weekDigits.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

func readCsv(path string) (*SalesSource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, errors.New("CSV 缺少“记录ID”等销售明细字段")
	}
	idx := make(map[string]int, len(records[0]))
	for i, name := range records[0] {
		idx[name] = i
	}
	if _, ok := idx["记录ID"]; !ok {
		return nil, errors.New("CSV 缺少“记录ID”等销售明细字段")
	}
	src := &SalesSource{Kind: "csv", Sales: make([]RawSale, 0, len(records)-1)}
	for _, rec := range records[1:] {
		empty := true
		for _, v := range rec {
			if strings.TrimSpace(v) != "" {
				empty = false
				break
			}
		}
		if empty {
			continue
		}
		src.Sales = append(src.Sales, toSale(csvRow{idx: idx, rec: rec}))
	}
	return src, nil
}

// readSalesSource 读取销售数据源（xlsx 或 csv）。
// 同一文件（路径 + 大小 + 修改时间）在进程内只解析一次，与 TS 版一致。
func readSalesSource(path string) (*SalesSource, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("文件不存在：%s", path)
		}
		return nil, err
	}
	key := fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano())
	sourceCache.mu.Lock()
	if sourceCache.key == key && sourceCache.value != nil {
		hit := sourceCache.value
		sourceCache.mu.Unlock()
		return hit, nil
	}
	sourceCache.mu.Unlock()

	ext := strings.ToLower(filepath.Ext(path))
	var src *SalesSource
	switch ext {
	case ".csv":
		src, err = readCsv(path)
	case ".xlsx":
		src, err = readXlsx(path)
	default:
		return nil, fmt.Errorf("不支持的销售数据格式：%s", ext)
	}
	if err != nil {
		return nil, err
	}
	sourceCache.mu.Lock()
	sourceCache.key = key
	sourceCache.value = src // 只缓存最近一份，避免内存占用
	sourceCache.mu.Unlock()
	return src, nil
}

var sourceCache struct {
	mu    sync.Mutex
	key   string
	value *SalesSource
}

// coverageOf 数据覆盖的日期范围，没有可识别日期时返回 nil。
func coverageOf(sales []RawSale) *contract.DateRange {
	var from, to string
	for _, s := range sales {
		if s.Date == "" {
			continue
		}
		if from == "" || s.Date < from {
			from = s.Date
		}
		if to == "" || s.Date > to {
			to = s.Date
		}
	}
	if from == "" {
		return nil
	}
	return &contract.DateRange{From: from, To: to}
}
