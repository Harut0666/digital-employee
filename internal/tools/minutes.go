package tools

// 周会纪要解析（移植自 packages/tools/src/minutes.ts）。
// TS 版先用 mammoth 把 docx 转成 HTML，再按 <p>/<table> 的顺序切块；
// Go 版直接用 archive/zip + encoding/xml 读 word/document.xml，产出同样的块序列。

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"

	"digital-employee/internal/contract"
)

// minutesBlock 一个正文块：段落或表格。
type minutesBlock struct {
	isTable bool
	text    string
	rows    [][]string
}

// normalizeText 等价于 TS 版 text()：剥掉标签后把连续空白压成一个空格并 trim。
func normalizeText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// parseDocumentXML 按文档顺序取出正文段落与表格。
func parseDocumentXML(r io.Reader) ([]minutesBlock, error) {
	dec := xml.NewDecoder(r)
	blocks := make([]minutesBlock, 0, 32)

	var inBody, inTable, inRow, inCell bool
	var inPara, inCellPara, inText bool
	var paraBuf, cellBuf strings.Builder
	var row []string
	var rows [][]string

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "body":
				inBody = true
			case "tbl":
				if inBody && !inTable {
					inTable = true
					rows = nil
				}
			case "tr":
				if inTable {
					inRow = true
					row = nil
				}
			case "tc":
				if inRow {
					inCell = true
					cellBuf.Reset()
				}
			case "p":
				if inCell && inRow {
					inCellPara = true
					paraBuf.Reset()
				} else if inBody && !inTable {
					inPara = true
					paraBuf.Reset()
				}
			case "t":
				if inPara || inCellPara {
					inText = true
				}
			case "tab":
				if inPara || inCellPara {
					paraBuf.WriteString(" ")
				}
			}
		case xml.CharData:
			if inText {
				if inPara {
					paraBuf.Write(t)
				} else if inCellPara {
					cellBuf.Write(t)
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				if inPara {
					inPara = false
					if text := normalizeText(paraBuf.String()); text != "" {
						blocks = append(blocks, minutesBlock{text: text})
					}
				} else if inCellPara {
					inCellPara = false
					cellBuf.WriteString(paraBuf.String())
				}
			case "tc":
				if inCell {
					inCell = false
					row = append(row, normalizeText(cellBuf.String()))
				}
			case "tr":
				if inRow {
					inRow = false
					rows = append(rows, row)
				}
			case "tbl":
				if inTable {
					inTable = false
					blocks = append(blocks, minutesBlock{isTable: true, rows: rows})
				}
			case "body":
				inBody = false
			}
		}
	}
	return blocks, nil
}

func readDocxBlocks(path string) ([]minutesBlock, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return parseDocumentXML(rc)
	}
	return nil, fmt.Errorf("docx 缺少 word/document.xml：%s", path)
}

var (
	weekRE    = regexp.MustCompile(`(\d{4})\s*年\s*第\s*(\d{1,2})\s*周`)
	sectionRE = regexp.MustCompile(`^[一二三四五六七八九十]+、\s*(.+)$`)
)

// DetectWeek 从标题类文字识别周次，如“（2026 年第 44 周）”。
func DetectWeek(s string) *contract.IsoWeek {
	m := weekRE.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	year := atoiOr(m[1], 0)
	week := atoiOr(m[2], 0)
	return &contract.IsoWeek{Year: year, Week: week}
}

func atoiOr(s string, fallback int) int {
	n := 0
	if s == "" {
		return fallback
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return fallback
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// parseMinutesFile 解析一份纪要。
func parseMinutesFile(path string) (*contract.ParsedMinutes, error) {
	blocks, err := readDocxBlocks(path)
	if err != nil {
		return nil, err
	}
	firstPara := -1
	paraIndexes := make([]int, 0, 3)
	for i, b := range blocks {
		if b.isTable {
			continue
		}
		if firstPara < 0 {
			firstPara = i
		}
		if len(paraIndexes) < 3 {
			paraIndexes = append(paraIndexes, i)
		}
	}
	title := ""
	if firstPara >= 0 {
		title = blocks[firstPara].text
	}
	var week *contract.IsoWeek
	for _, idx := range paraIndexes {
		if detected := DetectWeek(blocks[idx].text); detected != nil {
			week = detected
			break
		}
	}
	prefix := "Wxx"
	if week != nil {
		prefix = fmt.Sprintf("W%02d", week.Week)
	}
	paragraphs := make([]contract.MinutesParagraph, 0, len(blocks))
	section := ""
	push := func(text string) {
		p := contract.MinutesParagraph{
			ID:   fmt.Sprintf("%s-p%02d", prefix, len(paragraphs)+1),
			Text: text,
		}
		if section != "" {
			p.Section = section
		}
		paragraphs = append(paragraphs, p)
	}
	start := firstPara + 1 // firstPara = -1 时从头开始，与 TS 的 indexOf → -1 一致
	for _, b := range blocks[start:] {
		if !b.isTable {
			if m := sectionRE.FindStringSubmatch(b.text); m != nil {
				section = m[1]
				continue
			}
			push(b.text)
			continue
		}
		var header []string
		if len(b.rows) > 0 {
			header = b.rows[0]
		}
		for _, r := range b.rows[min(1, len(b.rows)):] {
			parts := make([]string, 0, len(r))
			for i, c := range r {
				if i < len(header) && header[i] != "" {
					parts = append(parts, header[i]+"："+c)
				} else {
					parts = append(parts, c)
				}
			}
			push(strings.Join(parts, "；"))
		}
	}
	parsed := &contract.ParsedMinutes{
		Path:       path,
		Week:       week,
		Title:      title,
		Paragraphs: paragraphs,
	}
	return parsed, nil
}

// ParseMinutes 解析一份或多份纪要（对应 parse_minutes 工具）。
func ParseMinutes(paths []string) (contract.ParseMinutesOutput, error) {
	out := contract.ParseMinutesOutput{Minutes: make([]contract.ParsedMinutes, 0, len(paths))}
	for _, path := range paths {
		m, err := parseMinutesFile(path)
		if err != nil {
			return contract.ParseMinutesOutput{}, &materialError{path: path, err: err}
		}
		out.Minutes = append(out.Minutes, *m)
	}
	return out, nil
}
