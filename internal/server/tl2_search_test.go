// tl2：find_files、search_content。只读，所有权限模式都允许；边界同读文件。
package server_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

func TestFindFilesGlobSortedByModTimeAndSkipsVCS(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("find_files", map[string]any{"pattern": "**/*.md"}),
		modelCalls("find_files", map[string]any{"pattern": "*.md", "path": "资料"}),
		modelCalls("find_files", map[string]any{"pattern": "*.xyz"}),
		modelSays("找完了。"),
	), withAppMode())
	old := h.writeWorkspaceFile(t, "旧.md", []byte("x"))
	mid := h.writeWorkspaceFile(t, "资料/深/中.md", []byte("x"))
	newest := h.writeWorkspaceFile(t, "最新.md", []byte("x"))
	h.writeWorkspaceFile(t, ".git/config.md", []byte("x"))
	h.writeWorkspaceFile(t, "资料/表.csv", []byte("x"))
	now := time.Now()
	for path, age := range map[string]time.Duration{old: -3 * time.Hour, mid: -2 * time.Hour, newest: -time.Hour} {
		if err := os.Chtimes(path, now.Add(age), now.Add(age)); err != nil {
			t.Fatal(err)
		}
	}
	created := h.folderSession()
	h.mustSetMode(created.ID, session.PermissionPlan) // 只读：plan 下也允许

	h.say(created.ID, contract.RoleInitiator, "找文件")

	detail := h.readSession(created.ID)
	if len(deniedTools(detail)) != 0 || len(permissionCards(detail)) != 0 {
		t.Fatalf("find_files 在 plan 下不该被拒绝或询问：%+v", detail.Messages)
	}
	calls := toolMessages(detail, "find_files")
	if len(calls) != 3 {
		t.Fatalf("调用了 %d 次", len(calls))
	}
	if got := fmt.Sprint(resultMap(t, calls[0])["files"]); got != "[最新.md 资料/深/中.md 旧.md]" {
		t.Fatalf("按修改时间倒序、不含 .git：%s", got)
	}
	if got := fmt.Sprint(resultMap(t, calls[1])["files"]); got != "[资料/深/中.md]" {
		t.Fatalf("带 path 时结果路径仍相对工作区：%s", got)
	}
	none := resultMap(t, calls[2])
	if none["text"] != "没有找到文件" || none["total"] != float64(0) {
		t.Fatalf("无匹配 = %+v", none)
	}
}

func TestFindFilesCapsAtOneHundredAndSaysTotal(t *testing.T) {
	h := newHarness(t, frontdeskScript(modelCalls("find_files", map[string]any{"pattern": "*.txt"}), modelSays("好。")), withAppMode())
	for i := 0; i < 130; i++ {
		h.writeWorkspaceFile(t, fmt.Sprintf("批量/f%03d.txt", i), []byte("x"))
	}
	created := h.folderSession()

	h.say(created.ID, contract.RoleInitiator, "找")

	result := resultMap(t, toolMessages(h.readSession(created.ID), "find_files")[0])
	files, _ := result["files"].([]any)
	if len(files) != 100 || result["total"] != float64(130) || !strings.Contains(fmt.Sprint(result["note"]), "130") {
		t.Fatalf("上限 100 并提示总数：%d %+v", len(files), result["note"])
	}
}

func TestSearchContentGroupsByFileAndSkipsBinaries(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("search_content", map[string]any{"pattern": "华南.*下降"}),
		modelCalls("search_content", map[string]any{"pattern": "华南", "include": "*.md"}),
		modelCalls("search_content", map[string]any{"pattern": "华南", "path": "资料/b.txt"}),
		modelCalls("search_content", map[string]any{"pattern": "(未闭合"}),
		modelCalls("search_content", map[string]any{"pattern": "不存在的词"}),
		modelSays("搜完了。"),
	), withAppMode())
	h.writeWorkspaceFile(t, "a.md", []byte("无关\n华南环比下降\n华东持平\r\n华南小幅下降\n"))
	h.writeWorkspaceFile(t, "资料/b.txt", []byte("华南下降了\n"))
	h.writeWorkspaceFile(t, "c.bin", []byte("华南\x00下降"))
	h.writeWorkspaceFile(t, ".git/x.md", []byte("华南环比下降"))
	created := h.folderSession()
	h.mustSetMode(created.ID, session.PermissionPlan)

	h.say(created.ID, contract.RoleInitiator, "搜")

	detail := h.readSession(created.ID)
	calls := toolMessages(detail, "search_content")
	if len(calls) != 5 {
		t.Fatalf("调用了 %d 次：%+v", len(calls), deniedTools(detail))
	}
	first := fmt.Sprint(resultMap(t, calls[0])["text"])
	for _, want := range []string{"共找到 3 处匹配", "a.md:\n  Line 2: 华南环比下降\n  Line 4: 华南小幅下降", "资料/b.txt:\n  Line 1: 华南下降了", "跳过了 1 个二进制"} {
		if !strings.Contains(first, want) {
			t.Fatalf("结果里没有 %q：\n%s", want, first)
		}
	}
	if strings.Contains(first, ".git") || strings.Contains(first, "c.bin:") {
		t.Fatalf("不该搜 .git 和二进制：\n%s", first)
	}
	if got := fmt.Sprint(resultMap(t, calls[1])["text"]); !strings.Contains(got, "共找到 2 处匹配") || strings.Contains(got, "b.txt") {
		t.Fatalf("include 只搜 *.md：\n%s", got)
	}
	if got := fmt.Sprint(resultMap(t, calls[2])["text"]); !strings.Contains(got, "共找到 1 处匹配") {
		t.Fatalf("path 指向单个文件：\n%s", got)
	}
	if !strings.Contains(calls[3].Reason, "正则") {
		t.Fatalf("非法正则的错误 = %q", calls[3].Reason)
	}
	if got := fmt.Sprint(resultMap(t, calls[4])["text"]); got != "没有找到匹配" {
		t.Fatalf("无匹配 = %q", got)
	}
}

func TestSearchContentCapsMatchesAndLongLines(t *testing.T) {
	h := newHarness(t, frontdeskScript(modelCalls("search_content", map[string]any{"pattern": "命中"}), modelSays("好。")), withAppMode())
	var lines []string
	for i := 0; i < 300; i++ {
		lines = append(lines, "命中 "+strings.Repeat("字", 1))
	}
	lines[0] = "命中" + strings.Repeat("长", 1500) // 4500 字节
	h.writeWorkspaceFile(t, "多.txt", []byte(strings.Join(lines, "\n")))
	created := h.folderSession()

	h.say(created.ID, contract.RoleInitiator, "搜")

	text := fmt.Sprint(resultMap(t, toolMessages(h.readSession(created.ID), "search_content")[0])["text"])
	if !strings.Contains(text, "共找到 300 处匹配，只显示前 250 条") || strings.Count(text, "Line ") != 250 {
		t.Fatalf("上限 250 并提示：%.200s（%d 条）", text, strings.Count(text, "Line "))
	}
	if !strings.Contains(text, "该行预览已截断") {
		t.Fatal("超长行预览应截断并标注")
	}
}

// 越界与链接逃逸：find_files / search_content 的 path 同样受工作区边界约束。
func TestSearchToolsStayInsideTheWorkspace(t *testing.T) {
	h := newHarness(t, nil, withAppMode())
	outside := filepath.Join(filepath.Dir(h.workspace), "外面")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "机密.txt"), []byte("命中机密"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(h.workspace, "跳板")
	makeDirLink(t, link, outside)
	h.setFrontdeskScript(frontdeskScript(
		modelCalls("find_files", map[string]any{"pattern": "*", "path": "../外面"}),
		modelCalls("search_content", map[string]any{"pattern": "命中", "path": filepath.Dir(h.workspace)}),
		modelCalls("find_files", map[string]any{"pattern": "**/*.txt"}),
		modelCalls("search_content", map[string]any{"pattern": "命中机密"}),
		modelSays("不行。"),
	))
	created := h.folderSession()

	h.say(created.ID, contract.RoleInitiator, "越界")

	detail := h.readSession(created.ID)
	if got := deniedTools(detail); len(got) != 2 || !strings.Contains(got[0].Reason, "工作区目录") {
		t.Fatalf("越界的 path 应被拒绝：%+v", got)
	}
	calls := toolMessages(detail, "find_files")
	if files := fmt.Sprint(resultMap(t, calls[len(calls)-1])["files"]); strings.Contains(files, "机密") {
		t.Fatalf("链接不该带出工作区外的文件：%s", files)
	}
	searches := toolMessages(detail, "search_content")
	if got := fmt.Sprint(resultMap(t, searches[len(searches)-1])["text"]); strings.Contains(got, "机密") {
		t.Fatalf("链接不该带出工作区外的内容：%s", got)
	}
}
