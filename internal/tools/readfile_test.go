package tools

// 目录文件的文本读取（T41 前台 Agent 的 read_file）：csv、xlsx、docx、txt、md 复用现有解析。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileReusesParsers(t *testing.T) {
	samples := samplesDir(t)
	dir := t.TempDir()
	long := filepath.Join(dir, "长文.md")
	if err := os.WriteFile(long, []byte("# 标题\n\n"+strings.Repeat("字", 25000)), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		path     string
		contains string
		blocked  bool
	}{
		{"xlsx 复用销售明细解析", filepath.Join(samples, "销售明细.xlsx"), "记录ID", true},
		{"csv 原文", filepath.Join(samples, "销售明细.csv"), "记录ID", false},
		{"docx 复用纪要解析", filepath.Join(samples, "周会纪要_2026W44.docx"), "第 44 周", true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			out, err := ReadFile(testCase.path)
			if err != nil {
				t.Fatalf("读文件失败：%v", err)
			}
			if !strings.Contains(out.Text, testCase.contains) {
				t.Fatalf("正文里没有 %q：%.200s", testCase.contains, out.Text)
			}
			// Excel 的正文里不能出现单元格里的原始数字列（序列化后的二进制块）
			if testCase.blocked && strings.Contains(out.Text, "PK\x03\x04") {
				t.Fatal("xlsx 不应把文件原样当作文本")
			}
		})
	}

	out, err := ReadFile(long)
	if err != nil {
		t.Fatalf("读长文失败：%v", err)
	}
	if got := len([]rune(out.Text)); got != readFileLimit {
		t.Fatalf("超过上限应截断到 %d 字，实际 %d", readFileLimit, got)
	}
	if !out.Truncated {
		t.Fatal("截断时应标明 truncated")
	}
}

func TestReadFileRefusesBinaryAndUnknownTypes(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "图像.png")
	if err := os.WriteFile(binary, []byte("\x89PNG\r\n\x1a\n\x00\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(binary); err == nil {
		t.Fatal("不支持的格式应报错，让 Agent 换个办法")
	}
	if _, err := ReadFile(filepath.Join(dir, "不存在.txt")); err == nil {
		t.Fatal("文件不存在应报错")
	}
}
