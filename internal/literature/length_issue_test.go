package literature

import (
	"strings"
	"testing"
)

// 真实模型（deepseek-v4.1-flash）的复测里，篇幅检查反复不过：
//   - 一位小数把“4.54 > 4.50”写成了“约 4.5 页，超过上限 4.5 页”，自相矛盾；
//   - “压缩全文 9%”对模型是误导：简报里表格和来源清单由程序生成，模型写的叙述只占约五分之一。
//
// 所以意见里要写两位小数、绝对字数，并点明“少引几个编号”这个杠杆。
func TestLengthIssueGivesAbsoluteCharTargets(t *testing.T) {
	got := lengthIssue(4.54, 4.5, 5) // 超出 0.04 页 = 64 字，加 120 字余量 = 184
	for _, want := range []string{"4.54", "4.50", "超出 64 字", "至少删减约 184 字", "少引用几个不同的编号", "约少 85 字"} {
		if !strings.Contains(got, want) {
			t.Errorf("篇幅意见缺少 %q：%s", want, got)
		}
	}
	if strings.Contains(got, "%") && strings.Contains(got, "压缩全文") {
		t.Errorf("不应再让模型“压缩全文百分之几”：%s", got)
	}
}

func TestLengthIssueScalesWithOverflow(t *testing.T) {
	got := lengthIssue(6.0, 4.5, 5) // 超出 1.5 页 = 2400 字
	if !strings.Contains(got, "超出 2400 字") || !strings.Contains(got, "至少删减约 2520 字") {
		t.Errorf("字数应随超出量变化：%s", got)
	}
}
