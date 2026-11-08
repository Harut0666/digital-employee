package tools

// 目录文件的文本读取（T41 前台 Agent 的 read_file）：
// csv、xlsx、docx、txt、md，复用现有的销售明细、纪要和 CSV 解析代码；超过 2 万字截断。
// 路径是否在工作区目录里由调用方（会话模块）先判定，这里只负责把文件读成文本。

import (
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"

	"digital-employee/internal/contract"
)

// readFileLimit read_file 最多返回的字数。
const readFileLimit = 20000

// maxSpreadsheetRows read_file 读表格时最多列多少行，免得文件再大也把整份塞给模型。
const maxSpreadsheetRows = 200

// ReadFile 把一个文件读成文本；不支持的格式报错。
func ReadFile(path string) (contract.ReadFileOutput, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md":
		raw, err := os.ReadFile(path)
		if err != nil {
			return contract.ReadFileOutput{}, err
		}
		return textOutput(string(raw)), nil
	case ".csv", ".xlsx", ".docx":
		return readSpreadsheetText(path)
	}
	return contract.ReadFileOutput{}, fmt.Errorf("不支持读取这种文件：%s（可以读 csv、xlsx、docx、txt、md）", filepath.Ext(path))
}

// textOutput 按字数截断。
func textOutput(text string) contract.ReadFileOutput {
	runes := []rune(text)
	if len(runes) <= readFileLimit {
		return contract.ReadFileOutput{Text: text}
	}
	return contract.ReadFileOutput{Text: string(runes[:readFileLimit]), Truncated: true}
}

// readSpreadsheetText 表格类文件读成逐行的文本，再按字数截断。
func readSpreadsheetText(path string) (contract.ReadFileOutput, error) {
	var text string
	var err error
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv":
		text, err = readCsvText(path)
	case ".xlsx":
		text, err = readXlsxText(path)
	default:
		text, err = readDocxText(path)
	}
	if err != nil {
		return contract.ReadFileOutput{}, err
	}
	return textOutput(text), nil
}

// readCsvText 把 CSV 读成逐行的文本（每行一格一格用制表符隔开）。
func readCsvText(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(raw), "\ufeff")))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	records, err := reader.ReadAll()
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, min(len(records), maxSpreadsheetRows))
	for _, record := range records[:min(len(records), maxSpreadsheetRows)] {
		lines = append(lines, strings.Join(record, "\t"))
	}
	if len(records) > maxSpreadsheetRows {
		lines = append(lines, fmt.Sprintf("……（共 %d 行，只列出前 %d 行）", len(records), maxSpreadsheetRows))
	}
	return strings.Join(lines, "\n"), nil
}

// readXlsxText 逐张工作表列出单元格（复用 readSheetRows）。
func readXlsxText(path string) (string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return "", errors.New("工作簿里没有工作表")
	}
	sections := make([]string, 0, len(sheets))
	for _, sheet := range sheets {
		rows, err := readSheetRows(f, sheet)
		if err != nil {
			return "", err
		}
		shown := 0
		lines := make([]string, 0, min(len(rows), maxSpreadsheetRows)+1)
		for _, row := range rows {
			if shown >= maxSpreadsheetRows {
				lines = append(lines, fmt.Sprintf("……（共 %d 行，只列出前 %d 行）", len(rows), maxSpreadsheetRows))
				break
			}
			if isEmptyRow(row) {
				continue
			}
			cells := make([]string, 0, len(row))
			for _, cell := range row {
				cells = append(cells, cell.str())
			}
			lines = append(lines, strings.Join(cells, "\t"))
			shown++
		}
		sections = append(sections, "【工作表："+sheet+"】\n"+strings.Join(lines, "\n"))
	}
	return strings.Join(sections, "\n\n"), nil
}

// readDocxText 复用纪要解析：标题、周次和逐段正文。
func readDocxText(path string) (string, error) {
	blocks, err := readDocxBlocks(path)
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.isTable {
			for index, row := range block.rows {
				if index == 0 {
					lines = append(lines, "[表格] "+strings.Join(row, "\t"))
					continue
				}
				lines = append(lines, strings.Join(row, "\t"))
			}
			continue
		}
		lines = append(lines, block.text)
	}
	return strings.Join(lines, "\n"), nil
}
