package knowledge

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// extractText 抽取文件正文：Word 取 word/document.xml 的文字，每段一行；Markdown 和纯文本原样；其余为空。
func extractText(name string, content []byte) (string, error) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".docx":
		return wordText(content)
	case ".md", ".txt":
		return string(content), nil
	}
	return "", nil
}

func wordText(content []byte) (string, error) {
	archive, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", err
	}
	for _, file := range archive.File {
		if file.Name != "word/document.xml" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return "", err
		}
		defer reader.Close()
		return documentText(reader)
	}
	return "", fmt.Errorf("不是 Word 文档：缺少 word/document.xml")
}

// documentText 按文档顺序取文字：每段一行（表格单元格里的段落也一样），空段落跳过。
func documentText(reader io.Reader) (string, error) {
	decoder := xml.NewDecoder(reader)
	out := strings.Builder{}
	line := strings.Builder{}
	inText := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			switch typed.Name.Local {
			case "t":
				inText = true
			case "tab":
				line.WriteString("\t")
			case "br":
				line.WriteString("\n")
			}
		case xml.EndElement:
			switch typed.Name.Local {
			case "t":
				inText = false
			case "p":
				if text := strings.TrimRight(line.String(), " \t"); text != "" {
					out.WriteString(text)
					out.WriteString("\n")
				}
				line.Reset()
			}
		case xml.CharData:
			if inText {
				line.Write(typed)
			}
		}
	}
	return out.String(), nil
}
