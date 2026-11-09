package session

import "testing"

func TestGlobMatching(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"*.md", "a.md", true},
		{"*.md", "资料/深/a.md", true}, // 不含 / 的写法匹配任意深度的文件名
		{"*.md", "a.txt", false},
		{"资料/*.md", "资料/a.md", true},
		{"资料/*.md", "资料/深/a.md", false}, // * 不跨目录
		{"资料/**/*.md", "资料/深/浅/a.md", true},
		{"资料/**/*.md", "资料/a.md", true}, // ** 可以是零层
		{"**/a.md", "a.md", true},
		{"**/a.md", "x/y/a.md", true},
		{"周报?.docx", "周报1.docx", true},
		{"周报?.docx", "周报12.docx", false},
		{"*.{md,txt}", "a.txt", true},
		{"*.{md,txt}", "a.csv", false},
		{"[ab].md", "a.md", true},
		{"[!ab].md", "a.md", false},
		{"a.b", "axb", false}, // 点是字面量
	}
	for _, tc := range cases {
		matcher, err := newGlobMatcher(tc.pattern)
		if err != nil {
			t.Fatalf("%q：%v", tc.pattern, err)
		}
		if got := matcher.matches(tc.path); got != tc.want {
			t.Errorf("glob %q 匹配 %q = %v，want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
	if _, err := newGlobMatcher(""); err == nil {
		t.Error("空 pattern 应报错")
	}
	if _, err := newGlobMatcher("{a,b"); err == nil {
		t.Error("没配对的大括号应报错")
	}
}

func TestPreviewTextCutsOnRuneBoundary(t *testing.T) {
	long := ""
	for len(long) < 3000 {
		long += "字"
	}
	got := previewText(long)
	// 2000 字节在字符中间时往前退到边界：666 个字 = 1998 字节
	if want := long[:1998]; got[:len(want)] != want {
		t.Fatalf("预览没有在字符边界截断：%d", len(got))
	}
}
