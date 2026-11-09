// tl2：read_file 分页与行号、edit_file 的 replace_all 与换行保持。主接缝：HTTP + 剧本模型 + 临时工作区。
package server_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

// toolMessages 会话日志里某个工具的全部调用留痕（含失败的）。
func toolMessages(detail session.Detail, tool string) []session.Message {
	out := []session.Message{}
	for _, message := range detail.Messages {
		if message.Tool == tool && message.Kind == session.KindToolCalled {
			out = append(out, message)
		}
	}
	return out
}

func resultMap(t *testing.T, message session.Message) map[string]any {
	t.Helper()
	fields, ok := message.Result.(map[string]any)
	if !ok {
		t.Fatalf("工具结果不是对象：%+v", message)
	}
	return fields
}

func TestReadFileOffsetLimitAndFooter(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("read_file", map[string]any{"path": "长.txt", "offset": 3, "limit": 2}),
		modelCalls("read_file", map[string]any{"path": "长.txt", "offset": 6}),
		modelCalls("read_file", map[string]any{"path": "长.txt", "offset": 99}),
		modelCalls("read_file", map[string]any{"path": "长.txt", "offset": 0}),
		modelSays("读完了。"),
	))
	h.writeWorkspaceFile(t, "长.txt", []byte("一\n二\n三\n四\n五\n六\n"))
	created := h.folderSession()

	h.say(created.ID, contract.RoleInitiator, "分页读")

	calls := toolMessages(h.readSession(created.ID), "read_file")
	if len(calls) != 4 {
		t.Fatalf("read_file 调用了 %d 次", len(calls))
	}
	first := resultMap(t, calls[0])
	if first["text"] != "3: 三\n4: 四\n\n（显示第 3–4 行，共 6 行，用 offset=5 继续）" || first["totalLines"] != float64(6) || first["truncated"] != true {
		t.Fatalf("第一次 = %+v", first)
	}
	last := resultMap(t, calls[1])
	if last["text"] != "6: 六\n\n（已到文件末尾，共 6 行）" || last["truncated"] != false {
		t.Fatalf("第二次 = %+v", last)
	}
	if !strings.Contains(calls[2].Reason, "offset 超出范围") || !strings.Contains(calls[2].Reason, "共 6 行") {
		t.Fatalf("越界 offset 的错误 = %q", calls[2].Reason)
	}
	if !strings.Contains(calls[3].Reason, "offset 从 1 起") {
		t.Fatalf("offset=0 的错误 = %q", calls[3].Reason)
	}
}

func TestReadFileRejectsBinaryFiles(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("read_file", map[string]any{"path": "x.txt"}),
		modelCalls("read_file", map[string]any{"path": "y.md"}),
		modelSays("读不了。"),
	))
	h.writeWorkspaceFile(t, "x.txt", []byte("abc\x00def"))
	h.writeWorkspaceFile(t, "y.md", []byte("好\n\xff\xfe坏\n"))
	created := h.folderSession()

	h.say(created.ID, contract.RoleInitiator, "读")

	for _, message := range toolMessages(h.readSession(created.ID), "read_file") {
		if !strings.Contains(message.Reason, "不是文本文件") {
			t.Fatalf("二进制/非法 UTF-8 应报“不是文本文件”：%+v", message)
		}
	}
}

// 越界、链接逃逸对分页读同样有效。
func TestReadFileStillStaysInsideTheWorkspaceWithOffset(t *testing.T) {
	h := newHarness(t, nil)
	outside := filepath.Join(filepath.Dir(h.workspace), "外面")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "机密.txt"), []byte("不能看"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.setFrontdeskScript(frontdeskScript(
		modelCalls("read_file", map[string]any{"path": "../外面/机密.txt", "offset": 1, "limit": 5}),
		modelSays("不行。"),
	))
	created := h.folderSession()

	h.say(created.ID, contract.RoleInitiator, "越界读")

	_, reason := toolDenial(t, h.readSession(created.ID), "read_file")
	if !strings.Contains(reason, "工作区目录") {
		t.Fatalf("原因 = %q", reason)
	}
}

func TestEditFileReplaceAllAndStrictRules(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls(contract.ToolEditFile, map[string]any{"path": "a.md", "old_string": "猫", "new_string": "狗"}),                       // 多处，默认报错
		modelCalls(contract.ToolEditFile, map[string]any{"path": "a.md", "old_string": "猫", "new_string": "狗", "replace_all": true}),  // 全部替换
		modelCalls(contract.ToolEditFile, map[string]any{"path": "a.md", "old_string": "", "new_string": "x"}),                        // 空
		modelCalls(contract.ToolEditFile, map[string]any{"path": "a.md", "old_string": "狗", "new_string": "狗"}),                       // 相同
		modelCalls(contract.ToolEditFile, map[string]any{"path": "a.md", "old_string": "没有", "new_string": "x", "replace_all": true}), // 0 处
		modelSays("改完了。"),
	))
	h.writeWorkspaceFile(t, "a.md", []byte("猫在睡。\n猫在吃。\n"))
	created := h.prefaceWithEmployee()
	h.mustSetMode(created.ID, session.PermissionAcceptEdits)

	h.say(created.ID, contract.RoleInitiator, "改")

	detail := h.readSession(created.ID)
	if got := h.workspaceText(t, "a.md"); got != "狗在睡。\n狗在吃。\n" {
		t.Fatalf("文件 = %q", got)
	}
	calls := toolMessages(detail, contract.ToolEditFile)
	codes := []string{}
	for _, message := range calls {
		fields, _ := message.Result.(map[string]any)
		codes = append(codes, fmt.Sprint(fields["error"]))
	}
	// 第 3、4 次是边界拒绝（tool_denied），不在 tool_called 里
	if fmt.Sprint(codes) != "[ambiguous <nil> not_found]" {
		t.Fatalf("错误码 = %v", codes)
	}
	if resultMap(t, calls[1])["replacements"] != float64(2) {
		t.Fatalf("replace_all 的结果 = %+v", calls[1].Result)
	}
	denied := deniedTools(detail)
	if len(denied) != 2 || !strings.Contains(denied[0].Reason, "old_string") || !strings.Contains(denied[1].Reason, "相同") {
		t.Fatalf("边界拒绝 = %+v", denied)
	}
}

func TestEditFileKeepsCRLF(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls(contract.ToolEditFile, map[string]any{"path": "w.txt", "old_string": "第二行\n第三行", "new_string": "二\n三"}),
		modelSays("好。"),
	))
	h.writeWorkspaceFile(t, "w.txt", []byte("第一行\r\n第二行\r\n第三行\r\n"))
	h.writeWorkspaceFile(t, "u.txt", []byte("甲\n乙\n"))
	created := h.prefaceWithEmployee()
	h.mustSetMode(created.ID, session.PermissionAcceptEdits)

	h.say(created.ID, contract.RoleInitiator, "改")

	if got := h.workspaceText(t, "w.txt"); got != "第一行\r\n二\r\n三\r\n" {
		t.Fatalf("CRLF 文件 = %q", got)
	}
	if got := h.workspaceText(t, "u.txt"); got != "甲\n乙\n" {
		t.Fatalf("LF 文件被动过：%q", got)
	}
}
