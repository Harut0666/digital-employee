package literature

import (
	"strings"
	"testing"
)

// 真实模型（deepseek-v4.1-flash）的复测里，篇幅检查连续 3 次提示“约 4.5 页，超过上限 4.5 页”：
// 一位小数把“4.54 > 4.50”写成了自相矛盾的话，模型也不知道还要删多少。这里守住两件事：
// 页数写到两位小数；给出整体至少要压缩的比例（多留 3 个百分点）。
func TestLengthIssueShowsTwoDecimalsAndConcreteCut(t *testing.T) {
	got := lengthIssue(4.54, 4.5, 5)
	for _, want := range []string{"4.54", "4.50", "压缩至少约 4%"} {
		if !strings.Contains(got, want) {
			t.Errorf("篇幅意见缺少 %q：%s", want, got)
		}
	}
}

func TestLengthIssueScalesWithOverflow(t *testing.T) {
	got := lengthIssue(6.0, 4.5, 5) // 超出 25%，要压缩 25%+3%
	if !strings.Contains(got, "至少约 28%") {
		t.Errorf("应要求压缩约 28%%：%s", got)
	}
}
