// 读取交付的 Word（docx）正文与表格：archive/zip + encoding/xml，不依赖外部程序。
// 移植自 CLI checks.ts 里 mammoth 的 extractRawText + convertToHtml 的效果：
// 正文纯文本按段落拼接；表格按 [表][行][列] 解析。
package cli

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

const wordNamespace = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"

type xmlNode struct {
	XMLName xml.Name
	Content string    `xml:",chardata"`
	Nodes   []xmlNode `xml:",any"`
}

func readDocx(path string) (*DocxEvidence, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("打开 docx 失败：%w", err)
	}
	defer reader.Close()
	var document *zip.File
	for _, file := range reader.File {
		if file.Name == "word/document.xml" {
			document = file
			break
		}
	}
	if document == nil {
		return nil, fmt.Errorf("docx 里没有 word/document.xml")
	}
	stream, err := document.Open()
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	data, err := io.ReadAll(stream)
	if err != nil {
		return nil, err
	}
	var root xmlNode
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("docx 正文不是合法 XML：%w", err)
	}
	evidence := &DocxEvidence{Path: path, Tables: [][][]string{}}
	var text strings.Builder
	walkDocx(root, &text, &evidence.Tables)
	evidence.Text = text.String()
	return evidence, nil
}

func isWordElement(node xmlNode, name string) bool {
	return node.XMLName.Local == name && (node.XMLName.Space == wordNamespace || node.XMLName.Space == "")
}

// gatherInline 收集一个节点内的文字（w:t 文本、w:tab → 空格、w:br → 换行）。
func gatherInline(node xmlNode, builder *strings.Builder) {
	if isWordElement(node, "t") {
		builder.WriteString(node.Content)
		return
	}
	if isWordElement(node, "tab") {
		builder.WriteString(" ")
		return
	}
	if isWordElement(node, "br") || isWordElement(node, "cr") {
		builder.WriteString("\n")
		return
	}
	for _, child := range node.Nodes {
		gatherInline(child, builder)
	}
}

// paragraphText 一个 w:p 的纯文本。
func paragraphText(node xmlNode) string {
	var builder strings.Builder
	for _, child := range node.Nodes {
		gatherInline(child, &builder)
	}
	return builder.String()
}

// walkDocx 按文档顺序收集正文文本与表格。
func walkDocx(node xmlNode, text *strings.Builder, tables *[][][]string) {
	if isWordElement(node, "p") {
		text.WriteString(paragraphText(node))
		text.WriteString("\n")
		return
	}
	if isWordElement(node, "tbl") {
		table := parseTable(node)
		if len(table) > 0 {
			*tables = append(*tables, table)
		}
		// 表格单元里的文字也算正文（mammoth 的 extractRawText 同样包含）
		for _, row := range node.Nodes {
			for _, cell := range row.Nodes {
				walkDocx(cell, text, tables)
			}
		}
		return
	}
	for _, child := range node.Nodes {
		walkDocx(child, text, tables)
	}
}

// parseTable w:tbl → [行][列]文本。
func parseTable(node xmlNode) [][]string {
	rows := [][]string{}
	for _, row := range node.Nodes {
		if !isWordElement(row, "tr") {
			continue
		}
		cells := []string{}
		for _, cell := range row.Nodes {
			if !isWordElement(cell, "tc") {
				continue
			}
			parts := []string{}
			collectCellText(cell, &parts)
			cells = append(cells, strings.TrimSpace(strings.Join(parts, " ")))
		}
		rows = append(rows, cells)
	}
	return rows
}

func collectCellText(node xmlNode, parts *[]string) {
	if isWordElement(node, "p") {
		text := strings.TrimSpace(paragraphText(node))
		if text != "" {
			*parts = append(*parts, text)
		}
		return
	}
	for _, child := range node.Nodes {
		collectCellText(child, parts)
	}
}
