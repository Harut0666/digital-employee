// de fixtures：从公开样例 samples/scenario-a/销售明细.xlsx 生成变异数据，放到 samples/fixtures/。
// 移植自 packages/cli/src/fixtures.ts。
//
// 变异只能由脚本生成，保证可复现：同样的输入连续跑两次，连字节都一样
// （写出的 zip 条目时间戳、docProps/core.xml 时间都固定）。manifest.json 记下每份夹具的大小、
// sha256 与结构；生成物入库。
package cli

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// fixedDate 写 xlsx 时统一使用的时间戳（让同样的数据每次写出相同字节）。
var fixedDate = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// corruptBytes 损坏夹具保留的字节数上限：zip 尾部的中央目录被截掉，xlsx 读取器会报错。
const corruptBytes = 64 * 1024

const (
	salesSheet    = "销售明细"
	storesSheet   = "门店信息"
	productsSheet = "商品信息"
	targetsSheet  = "销售目标"
)

// fixtureSpec 一份夹具的变异方式。
type fixtureSpec struct {
	key         string
	description string
	mutate      func(workbook *excelize.File) error
	corrupt     bool
}

var fixtureSpecs = []fixtureSpec{
	{
		key:         "销售明细-第44周周中截止",
		description: "删掉 2026-10-28 之后的销售记录，第 44 周数据在周中截止，用来测“周期不完整”暂停",
		mutate: func(workbook *excelize.File) error {
			_, err := cutSalesAfter(workbook, salesSheet, "2026-10-28")
			return err
		},
	},
	{
		key:         "销售明细-缺销售目标表",
		description: "删掉“销售目标”工作表，用来测缺维表暂停（目标达成率算不出来）",
		mutate:      func(workbook *excelize.File) error { return workbook.DeleteSheet(targetsSheet) },
	},
	{
		key:         "销售明细-缺实收金额列",
		description: "销售明细少“实收金额”一列，用来测缺列（实收全部读成 0）",
		mutate:      func(workbook *excelize.File) error { return dropColumn(workbook, salesSheet, "实收金额") },
	},
	{
		key:         "销售明细-损坏",
		description: "截掉尾部（前 64KB）的损坏 xlsx，用来测损坏文件（读不出来 → 准入不通过）",
		corrupt:     true,
	},
	{
		key:         "销售明细-含未知门店编码",
		description: "追加 3 行门店编码 XX99（门店信息表里没有）的销售记录，用来测编码不认识（不计入合计）",
		mutate: func(workbook *excelize.File) error {
			_, err := appendUnknownStoreRows(workbook, salesSheet, unknownStoreOptions{date: "2026-10-28", count: 3, storeCode: "XX99", storeName: "未知门店"})
			return err
		},
	},
	{
		key:         "销售明细-记录ID冲突",
		description: "追加 3 行与 10/27 已有记录同 ID、实收金额不同的记录，用来测记录 ID 冲突（去重时记为冲突、不计入合计）",
		mutate: func(workbook *excelize.File) error {
			_, err := appendIDConflictRows(workbook, salesSheet, idConflictOptions{count: 3, amountDelta: 5, date: "2026-10-27"})
			return err
		},
	},
}

func sha256HexBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// headerOf 表头文字（第 1 行，去空）。
func headerOf(workbook *excelize.File, sheet string) ([]string, error) {
	rows, err := workbook.GetRows(sheet)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []string{}, nil
	}
	header := make([]string, 0, len(rows[0]))
	for _, cell := range rows[0] {
		header = append(header, strings.TrimSpace(cell))
	}
	return header, nil
}

// columnIndex 按表头文字找列号（0 开始）；找不到报错，避免生成“看起来像夹具”的废数据。
func columnIndex(header []string, name string) (int, error) {
	for index, cell := range header {
		if cell == name {
			return index, nil
		}
	}
	return -1, fmt.Errorf("表里没有“%s”列，夹具生成中止", name)
}

func requireSheet(workbook *excelize.File, name string) error {
	for _, sheet := range workbook.GetSheetList() {
		if sheet == name {
			return nil
		}
	}
	return fmt.Errorf("源数据里没有“%s”工作表，夹具生成中止", name)
}

func dateOfCell(value string) string {
	text := strings.TrimSpace(value)
	if len(text) < 10 {
		return ""
	}
	candidate := text[:10]
	if matched, _ := regexp.MatchString(`^\d{4}-\d{2}-\d{2}$`, candidate); matched {
		return candidate
	}
	return ""
}

// cutSalesAfter 删掉“销售日期”晚于 cutoff 的行；返回删掉的行数。
func cutSalesAfter(workbook *excelize.File, sheet, cutoff string) (int, error) {
	if err := requireSheet(workbook, sheet); err != nil {
		return 0, err
	}
	header, err := headerOf(workbook, sheet)
	if err != nil {
		return 0, err
	}
	column, err := columnIndex(header, "销售日期")
	if err != nil {
		return 0, err
	}
	rows, err := workbook.GetRows(sheet)
	if err != nil {
		return 0, err
	}
	targets := []int{}
	for index := 1; index < len(rows); index++ {
		cells := rows[index]
		if column >= len(cells) {
			continue
		}
		date := dateOfCell(cells[column])
		if date != "" && date > cutoff {
			targets = append(targets, index+1) // excelize 行号从 1 开始
		}
	}
	if len(targets) == 0 {
		return 0, fmt.Errorf("没有 %s 之后的销售记录可删：源数据不是预期的范围", cutoff)
	}
	for index := len(targets) - 1; index >= 0; index-- {
		if err := workbook.RemoveRow(sheet, targets[index]); err != nil {
			return 0, err
		}
	}
	return len(targets), nil
}

// dropColumn 删掉一列（按表头文字定位）。
func dropColumn(workbook *excelize.File, sheet, headerName string) error {
	if err := requireSheet(workbook, sheet); err != nil {
		return err
	}
	header, err := headerOf(workbook, sheet)
	if err != nil {
		return err
	}
	index, err := columnIndex(header, headerName)
	if err != nil {
		return err
	}
	columnName, err := excelize.ColumnNumberToName(index + 1)
	if err != nil {
		return err
	}
	return workbook.RemoveCol(sheet, columnName)
}

func cellAddress(column, row int) (string, error) {
	name, err := excelize.ColumnNumberToName(column)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%d", name, row), nil
}

type unknownStoreOptions struct {
	date      string
	count     int
	storeCode string
	storeName string
}

func rowValues(workbook *excelize.File, sheet string, row int) ([]any, error) {
	rows, err := workbook.GetRows(sheet)
	if err != nil {
		return nil, err
	}
	if row-1 >= len(rows) {
		return nil, fmt.Errorf("第 %d 行不存在", row)
	}
	values := make([]any, len(rows[row-1]))
	for index, cell := range rows[row-1] {
		values[index] = cell
	}
	return values, nil
}

// appendUnknownStoreRows 追加若干行“门店编码在门店信息表里查不到”的记录。
func appendUnknownStoreRows(workbook *excelize.File, sheet string, options unknownStoreOptions) ([]string, error) {
	if err := requireSheet(workbook, sheet); err != nil {
		return nil, err
	}
	header, err := headerOf(workbook, sheet)
	if err != nil {
		return nil, err
	}
	storeColumn, err := columnIndex(header, "门店编码")
	if err != nil {
		return nil, err
	}
	idColumn, err := columnIndex(header, "记录ID")
	if err != nil {
		return nil, err
	}
	dateColumn, err := columnIndex(header, "销售日期")
	if err != nil {
		return nil, err
	}
	nameColumn, nameErr := columnIndex(header, "门店名称")
	if nameErr != nil {
		nameColumn = -1
	}
	rows, err := workbook.GetRows(sheet)
	if err != nil {
		return nil, err
	}
	for index := 1; index < len(rows); index++ {
		if storeColumn < len(rows[index]) && strings.TrimSpace(rows[index][storeColumn]) == options.storeCode {
			return nil, fmt.Errorf("门店编码 %s 在源数据里已经存在，不能用来造“未知编码”夹具", options.storeCode)
		}
	}
	ids := []string{}
	datePart := strings.ReplaceAll(options.date, "-", "")
	for i := 0; i < options.count; i++ {
		values := make([]any, len(header))
		if len(rows) > 1 {
			template := rows[1]
			for index := range values {
				if index < len(template) {
					values[index] = template[index]
				}
			}
		}
		id := fmt.Sprintf("R%s%s-90%02d", datePart, options.storeCode, i+1)
		values[idColumn] = id
		values[dateColumn] = options.date
		values[storeColumn] = options.storeCode
		if nameColumn >= 0 {
			values[nameColumn] = options.storeName
		}
		address, err := cellAddress(1, len(rows)+i+1)
		if err != nil {
			return nil, err
		}
		if err := workbook.SetSheetRow(sheet, address, &values); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

type idConflictOptions struct {
	count       int
	amountDelta float64
	date        string
}

// appendIDConflictRows 追加若干行“与已有记录同 ID、实收金额不同”的记录。
func appendIDConflictRows(workbook *excelize.File, sheet string, options idConflictOptions) ([]string, error) {
	if err := requireSheet(workbook, sheet); err != nil {
		return nil, err
	}
	header, err := headerOf(workbook, sheet)
	if err != nil {
		return nil, err
	}
	netColumn, err := columnIndex(header, "实收金额")
	if err != nil {
		return nil, err
	}
	idColumn, err := columnIndex(header, "记录ID")
	if err != nil {
		return nil, err
	}
	dateColumn, err := columnIndex(header, "销售日期")
	if err != nil {
		return nil, err
	}
	rows, err := workbook.GetRows(sheet)
	if err != nil {
		return nil, err
	}
	sameDay := []int{}
	for index := 1; index < len(rows) && len(sameDay) < options.count; index++ {
		if dateColumn < len(rows[index]) && dateOfCell(rows[index][dateColumn]) == options.date {
			sameDay = append(sameDay, index+1)
		}
	}
	if len(sameDay) < options.count {
		return nil, fmt.Errorf("“%s”表里 %s 的记录不足 %d 条，无法造 ID 冲突夹具", sheet, options.date, options.count)
	}
	ids := []string{}
	for offset, row := range sameDay {
		values := make([]any, len(header))
		cells := rows[row-1]
		for index := range values {
			if index < len(cells) {
				values[index] = cells[index]
			}
		}
		net, _ := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(values[netColumn])), 64)
		values[netColumn] = fmt.Sprintf("%.2f", round2(net+options.amountDelta))
		address, err := cellAddress(1, len(rows)+offset+1)
		if err != nil {
			return nil, err
		}
		if err := workbook.SetSheetRow(sheet, address, &values); err != nil {
			return nil, err
		}
		ids = append(ids, fmt.Sprint(values[idColumn]))
	}
	return ids, nil
}

func round2(value float64) float64 {
	return float64(int64(value*100+0.5)) / 100
}

// ---------------------------------------------------------------- 确定性写出

var coreDatePattern = regexp.MustCompile(`<dcterms:(created|modified)([^>]*)>[^<]*</dcterms:(created|modified)>`)

// writeWorkbookDeterministic 写 xlsx 并固定时间戳：zip 条目时间与 docProps/core.xml 都固定。
func writeWorkbookDeterministic(workbook *excelize.File, path string) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	buffer, err := workbook.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	reader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	if err != nil {
		return nil, err
	}
	// 先按名字排序，保证 zip 条目顺序稳定
	files := make([]*zip.File, 0, len(reader.File))
	for _, file := range reader.File {
		files = append(files, file)
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].Name < files[j].Name })

	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, file := range files {
		if file.FileInfo().IsDir() {
			continue
		}
		source, err := file.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(source)
		_ = source.Close()
		if err != nil {
			return nil, err
		}
		if file.Name == "docProps/core.xml" {
			data = coreDatePattern.ReplaceAll(data, []byte(`<dcterms:$1$2>2026-01-01T00:00:00Z</dcterms:$1>`))
		}
		header := &zip.FileHeader{
			Name:     file.Name,
			Method:   zip.Deflate,
			Modified: fixedDate,
		}
		writerFile, err := writer.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := writerFile.Write(data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	data := output.Bytes()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return nil, err
	}
	return data, nil
}

// ---------------------------------------------------------------- manifest

type fixtureCoverage struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type fixtureEntry struct {
	File         string           `json:"file"`
	Description  string           `json:"description"`
	Bytes        int              `json:"bytes"`
	SHA256       string           `json:"sha256"`
	Sheets       []string         `json:"sheets"`
	SalesRows    int              `json:"salesRows"`
	SalesColumns []string         `json:"salesColumns"`
	Coverage     *fixtureCoverage `json:"coverage"`
}

type fixtureManifest struct {
	Source       string         `json:"source"`
	SourceSHA256 string         `json:"sourceSha256"`
	GeneratedBy  string         `json:"generatedBy"`
	Fixtures     []fixtureEntry `json:"fixtures"`
}

type fixtureStats struct {
	sheets       []string
	salesRows    int
	salesColumns []string
	coverage     *fixtureCoverage
}

func statsOf(workbook *excelize.File) fixtureStats {
	stats := fixtureStats{sheets: workbook.GetSheetList()}
	if err := requireSheet(workbook, salesSheet); err != nil {
		return stats
	}
	header, err := headerOf(workbook, salesSheet)
	if err != nil {
		return stats
	}
	stats.salesColumns = header
	rows, err := workbook.GetRows(salesSheet)
	if err != nil {
		return stats
	}
	dateColumn, _ := columnIndex(header, "销售日期")
	stats.salesRows = maxInt(len(rows)-1, 0)
	if dateColumn < 0 {
		return stats
	}
	from, to := "", ""
	for index := 1; index < len(rows); index++ {
		if dateColumn >= len(rows[index]) {
			continue
		}
		date := dateOfCell(rows[index][dateColumn])
		if date == "" {
			continue
		}
		if from == "" || date < from {
			from = date
		}
		if to == "" || date > to {
			to = date
		}
	}
	if from != "" {
		stats.coverage = &fixtureCoverage{From: from, To: to}
	}
	return stats
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func describeSource(sourcePath, repoRoot string) string {
	rel, err := filepath.Rel(repoRoot, sourcePath)
	if err == nil && rel != "" && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
		return filepath.ToSlash(rel)
	}
	return filepath.Base(sourcePath)
}

// generateFixtures 生成全部夹具与 manifest.json，返回 manifest。
func generateFixtures(sourcePath, outDir, repoRoot string) (*fixtureManifest, error) {
	sourceBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("找不到源数据：%s", sourcePath)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	manifest := &fixtureManifest{
		Source:       describeSource(sourcePath, repoRoot),
		SourceSHA256: sha256HexBytes(sourceBytes),
		GeneratedBy:  "go run ./cmd/digital-employee fixtures",
		Fixtures:     []fixtureEntry{},
	}
	for _, spec := range fixtureSpecs {
		path := filepath.Join(outDir, spec.key+".xlsx")
		var data []byte
		var stats fixtureStats
		if spec.corrupt {
			limit := corruptBytes
			if len(sourceBytes)/2 < limit {
				limit = len(sourceBytes) / 2
			}
			data = sourceBytes[:limit]
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return nil, err
			}
			stats = fixtureStats{sheets: []string{}, salesColumns: []string{}}
		} else {
			workbook, err := excelize.OpenFile(sourcePath)
			if err != nil {
				return nil, err
			}
			if spec.mutate != nil {
				if err := spec.mutate(workbook); err != nil {
					_ = workbook.Close()
					return nil, err
				}
			}
			stats = statsOf(workbook)
			data, err = writeWorkbookDeterministic(workbook, path)
			_ = workbook.Close()
			if err != nil {
				return nil, err
			}
		}
		if stats.sheets == nil {
			stats.sheets = []string{}
		}
		if stats.salesColumns == nil {
			stats.salesColumns = []string{}
		}
		manifest.Fixtures = append(manifest.Fixtures, fixtureEntry{
			File: spec.key + ".xlsx", Description: spec.description,
			Bytes: len(data), SHA256: sha256HexBytes(data),
			Sheets: stats.sheets, SalesRows: stats.salesRows, SalesColumns: stats.salesColumns,
			Coverage: stats.coverage,
		})
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(outDir, "manifest.json"), append(manifestData, '\n'), 0o644); err != nil {
		return nil, err
	}
	return manifest, nil
}

// runFixtures de fixtures 的实现。
func runFixtures(outDirArg, sourceArg string, deps Deps) (int, error) {
	repoRoot := RepoRoot()
	samplesDir := ResolveSamplesDir(deps.Env, repoRoot)
	sourcePath := sourceArg
	if sourcePath == "" {
		sourcePath = filepath.Join(samplesDir, "scenario-a", "销售明细.xlsx")
	}
	if absolute, err := filepath.Abs(sourcePath); err == nil {
		sourcePath = absolute
	}
	outDir := outDirArg
	if outDir == "" {
		outDir = filepath.Join(samplesDir, "fixtures")
	}
	if absolute, err := filepath.Abs(outDir); err == nil {
		outDir = absolute
	}
	manifest, err := generateFixtures(sourcePath, outDir, repoRoot)
	if err != nil {
		return 1, err
	}
	fmt.Fprintf(deps.stdout(), "源数据：%s（sha256 %s…）\n", manifest.Source, firstN(manifest.SourceSHA256, 12))
	for _, fixture := range manifest.Fixtures {
		structure := "损坏文件"
		if fixture.Coverage != nil {
			structure = fmt.Sprintf("%d 个工作表、销售明细 %d 行（%s 至 %s）", len(fixture.Sheets), fixture.SalesRows, fixture.Coverage.From, fixture.Coverage.To)
		}
		fmt.Fprintf(deps.stdout(), "  %s  %.2fMB  %s\n", fixture.File, float64(fixture.Bytes)/1024/1024, structure)
	}
	fmt.Fprintf(deps.stdout(), "已生成 %d 份夹具：%s\n", len(manifest.Fixtures), outDir)
	return 0, nil
}

func firstN(text string, n int) string {
	if len(text) <= n {
		return text
	}
	return text[:n]
}
