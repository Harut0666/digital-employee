package cli

import (
	"fmt"
	"os"
	"strings"
)

// readMarkdown 读交付的 Markdown 简报（场景 B）：正文纯文本，加上按出现顺序解析出来的管道表格，
// 和 Word 的证据同一种结构，用例里的 mustContain、table:行/列 检查对两种交付物都能用。
func readMarkdown(path string) (*DocxEvidence, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 Markdown 失败：%w", err)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	evidence := &DocxEvidence{Path: path, Text: text, Tables: [][][]string{}}
	var current [][]string
	flush := func() {
		if len(current) > 0 {
			evidence.Tables = append(evidence.Tables, current)
		}
		current = nil
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			flush()
			continue
		}
		cells := splitMarkdownRow(trimmed)
		if isMarkdownSeparator(cells) {
			continue
		}
		current = append(current, cells)
	}
	flush()
	return evidence, nil
}

func splitMarkdownRow(line string) []string {
	line = strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
	cells := []string{}
	var cell strings.Builder
	escaped := false
	for _, r := range line {
		switch {
		case escaped:
			cell.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '|':
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteRune(r)
		}
	}
	return append(cells, strings.TrimSpace(cell.String()))
}

func isMarkdownSeparator(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		stripped := strings.Trim(cell, ": ")
		if stripped == "" || strings.Trim(stripped, "-") != "" {
			return false
		}
	}
	return true
}
