package render

// 测试公共工具：加载真实 fixture、解包 docx、XML 合法性、文本提取、PNG 统计。

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"digital-employee/internal/contract"
)

const wordNS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"

// loadFixture 加载从 TS 版 A-01 真实运行事件里导出的渲染输入
// （report + facts 都取自 samples/scenario-a 的真实数据）。
func loadFixture(t *testing.T) contract.RenderReportInput {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "a01_render_input.json"))
	if err != nil {
		t.Fatalf("读取 fixture 失败: %v", err)
	}
	var input contract.RenderReportInput
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatalf("解析 fixture 失败: %v", err)
	}
	input.OutDir = t.TempDir()
	return input
}

// docxParts 解包 docx 成 文件名 → 内容。
func docxParts(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("docx 不是合法 zip: %v", err)
	}
	parts := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("打开 zip 成员 %s 失败: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("读取 zip 成员 %s 失败: %v", f.Name, err)
		}
		parts[f.Name] = b
	}
	return parts
}

// assertWellFormedXML 断言 XML 能被完整解析（Word 打开的最低要求之一）。
func assertWellFormedXML(t *testing.T, name string, data []byte) {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		if _, err := dec.Token(); err != nil {
			if err == io.EOF {
				return
			}
			t.Fatalf("%s 不是合法 XML: %v", name, err)
		}
	}
}

// docxText 提取 document.xml 里所有 w:t 文本（按出现顺序拼接）。
func docxText(t *testing.T, document []byte) string {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(document))
	var b strings.Builder
	inText := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("document.xml 解析失败: %v", err)
		}
		switch token := tok.(type) {
		case xml.StartElement:
			if token.Name.Space == wordNS && token.Name.Local == "t" {
				inText = true
			}
		case xml.CharData:
			if inText {
				b.Write(token)
			}
		case xml.EndElement:
			if token.Name.Space == wordNS && token.Name.Local == "t" {
				inText = false
			}
		}
	}
	return b.String()
}

// pngSize 解码 PNG，返回宽高和非白像素数。
func pngSize(t *testing.T, data []byte) (width, height, inkPixels int) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("不是合法 PNG: %v", err)
	}
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r>>8 < 250 || g>>8 < 250 || b>>8 < 250 {
				inkPixels++
			}
		}
	}
	return bounds.Dx(), bounds.Dy(), inkPixels
}

// readXMLAttrValue 取 XML 里某属性的字面值（测试断言用，正则足够）。
func readXMLAttrValue(t *testing.T, data []byte, attr string) []string {
	t.Helper()
	s := string(data)
	var out []string
	for {
		i := strings.Index(s, attr+`="`)
		if i < 0 {
			return out
		}
		s = s[i+len(attr)+2:]
		j := strings.IndexByte(s, '"')
		if j < 0 {
			return out
		}
		out = append(out, s[:j])
		s = s[j+1:]
	}
}
