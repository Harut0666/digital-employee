package session

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func numbered(count int) string {
	lines := make([]string, count)
	for index := range lines {
		lines[index] = "第" + strconv.Itoa(index+1) + "行"
	}
	return strings.Join(lines, "\n")
}

func TestReadLinesNumbersAndReportsEndOfFile(t *testing.T) {
	got, err := readLines(strings.NewReader("甲\n乙\r\n丙\n"), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 3 || got.Start != 1 || got.End != 3 {
		t.Fatalf("范围 = %+v", got)
	}
	want := "1: 甲\n2: 乙\n3: 丙\n\n（已到文件末尾，共 3 行）"
	if got.Text != want {
		t.Fatalf("正文 = %q，want %q", got.Text, want)
	}
	if got.Truncated {
		t.Fatal("读到末尾不算截断")
	}
}

func TestReadLinesWindowAndContinueHint(t *testing.T) {
	got, err := readLines(strings.NewReader(numbered(10)), 3, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got.Start != 3 || got.End != 6 || got.Total != 10 {
		t.Fatalf("范围 = %+v", got)
	}
	if !strings.HasPrefix(got.Text, "3: 第3行\n4: 第4行\n5: 第5行\n6: 第6行\n") {
		t.Fatalf("正文 = %q", got.Text)
	}
	if !strings.HasSuffix(got.Text, "（显示第 3–6 行，共 10 行，用 offset=7 继续）") {
		t.Fatalf("页脚 = %q", got.Text)
	}
	if !got.Truncated {
		t.Fatal("窗口没到末尾应标 Truncated")
	}
}

func TestReadLinesCapsAtTwoThousandLines(t *testing.T) {
	got, err := readLines(strings.NewReader(numbered(2500)), 1, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if got.End != 2000 || got.Total != 2500 {
		t.Fatalf("范围 = %+v", got)
	}
	if !strings.Contains(got.Text, "用 offset=2001 继续") {
		t.Fatalf("页脚 = %q", got.Text[len(got.Text)-80:])
	}
}

func TestReadLinesOffsetOutOfRange(t *testing.T) {
	_, err := readLines(strings.NewReader(numbered(3)), 4, 0)
	if err == nil || !strings.Contains(err.Error(), "offset") || !strings.Contains(err.Error(), "共 3 行") {
		t.Fatalf("错误 = %v", err)
	}
	if _, err := readLines(strings.NewReader(numbered(3)), 0, 0); err == nil {
		t.Fatal("offset 从 1 起，0 应报错")
	}
	if _, err := readLines(strings.NewReader(numbered(3)), -2, 0); err == nil {
		t.Fatal("offset 为负应报错")
	}
	// 空文件：没有第 1 行可读
	empty, err := readLines(strings.NewReader(""), 1, 0)
	if err != nil || empty.Total != 0 || !strings.Contains(empty.Text, "空") {
		t.Fatalf("空文件 = %+v %v", empty, err)
	}
}

func TestReadLinesTruncatesLongLines(t *testing.T) {
	long := strings.Repeat("字", 3000)
	got, err := readLines(strings.NewReader("短\n"+long+"\n尾"), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "2: "+strings.Repeat("字", 2000)+"…（该行已截断，只显示前 2000 个字符）") {
		t.Fatalf("长行没有被截断并标注：%.120q", got.Text)
	}
	if strings.Contains(got.Text, strings.Repeat("字", 2001)) {
		t.Fatal("每行最多 2000 字符")
	}
	if got.Total != 3 {
		t.Fatalf("总行数 = %d", got.Total)
	}
}

func TestReadLinesCapsSelectedContentAtFiftyKB(t *testing.T) {
	// 每行 1000 字节的英文行，100 行 = 100KB
	line := strings.Repeat("a", 1000)
	lines := make([]string, 100)
	for index := range lines {
		lines[index] = line
	}
	got, err := readLines(strings.NewReader(strings.Join(lines, "\n")), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated || got.End >= 100 || got.End < 40 {
		t.Fatalf("范围 = %+v", got)
	}
	if !strings.Contains(got.Text, "输出已截断") || !strings.Contains(got.Text, "offset=") {
		t.Fatalf("页脚 = %q", got.Text[len(got.Text)-120:])
	}
	if got.Total != 100 {
		t.Fatalf("截断后仍应统计总行数：%d", got.Total)
	}
	if len(got.Text) > 50*1024+200 {
		t.Fatalf("输出 %d 字节，超过了 50KB", len(got.Text))
	}
}

func TestReadLinesRejectsBinaryAndInvalidUTF8(t *testing.T) {
	if _, err := readLines(strings.NewReader("abc\x00def\n"), 1, 0); err == nil || !strings.Contains(err.Error(), "不是文本文件") {
		t.Fatalf("含 NUL 的内容 = %v", err)
	}
	if _, err := readLines(strings.NewReader("正常\n坏\xff\xfe字节\n"), 1, 0); err == nil || !strings.Contains(err.Error(), "不是文本文件") {
		t.Fatalf("非法 UTF-8 = %v", err)
	}
	// 窗口之外的坏字节也算：整个文件都要是文本
	if _, err := readLines(strings.NewReader("甲\n乙\n\xff\n"), 1, 1); err == nil {
		t.Fatal("窗口外的非法字节也应报错")
	}
}

func TestReadTextFileDetectsNULInFirstBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.bin")
	if err := os.WriteFile(path, []byte("PK\x03\x04\x00\x00data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readTextFile(path, 1, 0); err == nil || !strings.Contains(err.Error(), "不是文本文件") {
		t.Fatalf("错误 = %v", err)
	}
}

func TestReadTextFileStreamsBigFilesAndStillCountsLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.log")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 99) + "\n"
	for index := 0; index < 110_000; index++ { // 11MB
		if _, err := file.WriteString(line); err != nil {
			t.Fatal(err)
		}
	}
	file.Close()
	got, err := readTextFile(path, 5, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 110_000 || got.Start != 5 || got.End != 7 {
		t.Fatalf("大文件的范围 = %+v", got)
	}
}
