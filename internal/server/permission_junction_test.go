package server_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

// makeDirLink 建一个指向目录的链接：Windows 用目录联接（junction，不需要管理员权限，普通用户机器上也能跑），
// 其他系统用符号链接。建不了就跳过并说明原因。
func makeDirLink(t *testing.T, link, target string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		command := exec.Command("cmd", "/c", "mklink", "/J", link, target)
		if output, err := command.CombinedOutput(); err != nil {
			t.Skipf("建不了目录联接（%v）：%s", err, strings.TrimSpace(string(output)))
		}
		return
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("建不了符号链接（%v）", err)
	}
}

// 工作区里的目录联接指到工作区外：顺着它写被拒绝，外面不会出现文件；指回工作区里的“交付”“材料”也一样。
// 联接指向工作区里的普通文件夹时，写进去是允许的（它就是那个文件夹）。
func TestWritesThroughJunctionsFollowTheSameBoundaries(t *testing.T) {
	h := newHarness(t, frontdeskScript(modelSays("占位")))
	outside := filepath.Join(filepath.Dir(h.workspace), "联接外面")
	for _, dir := range []string{outside, filepath.Join(h.workspace, "交付"), filepath.Join(h.workspace, "材料"), filepath.Join(h.workspace, "笔记")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	makeDirLink(t, filepath.Join(h.workspace, "出口"), outside)
	makeDirLink(t, filepath.Join(h.workspace, "到交付"), filepath.Join(h.workspace, "交付"))
	makeDirLink(t, filepath.Join(h.workspace, "到材料"), filepath.Join(h.workspace, "材料"))
	makeDirLink(t, filepath.Join(h.workspace, "到笔记"), filepath.Join(h.workspace, "笔记"))

	type attempt struct{ path, reason string }
	denied := []attempt{
		{"出口/跑出去.md", "工作区目录之外"},
		{"出口/新目录/跑出去.md", "工作区目录之外"},
		{"到交付/冒名.md", "交付"},
		{"到材料/混进材料.md", "材料"},
	}
	calls := []agent.ScriptedTurn{}
	for _, a := range denied {
		calls = append(calls, modelCalls(contract.ToolWriteFile, map[string]any{"path": a.path, "content": "越界"}))
	}
	calls = append(calls, modelCalls(contract.ToolWriteFile, map[string]any{"path": "到笔记/联接内.md", "content": "正常"}))
	h.setFrontdeskScript(frontdeskScript(append(calls, modelSays("完了。"))...))
	created := h.prefaceWithEmployee()
	h.mustSetMode(created.ID, session.PermissionFull)

	h.say(created.ID, contract.RoleInitiator, "随便写")

	detail := h.readSession(created.ID)
	got := deniedTools(detail)
	if len(got) != len(denied) {
		t.Fatalf("拒绝留痕 = %d 条，want %d：%+v", len(got), len(denied), got)
	}
	for index, a := range denied {
		if !strings.Contains(got[index].Reason, a.reason) {
			t.Fatalf("第 %d 条（%s）的拒绝原因 = %q，want 含 %q", index+1, a.path, got[index].Reason, a.reason)
		}
	}
	for _, name := range []string{"跑出去.md", "新目录"} {
		if _, err := os.Stat(filepath.Join(outside, name)); !os.IsNotExist(err) {
			t.Fatalf("工作区外面不该出现 %s：%v", name, err)
		}
	}
	for _, rel := range []string{"交付/冒名.md", "材料/混进材料.md"} {
		if h.workspaceHas(rel) {
			t.Fatalf("%s 不该被写出来", rel)
		}
	}
	if text := h.workspaceText(t, "笔记/联接内.md"); text != "正常" {
		t.Fatalf("联接指向工作区内的普通文件夹时应能写：%q", text)
	}
}

// 悬空的目录联接（目标已经不存在）：写进去会跟着联接跑到别处，必须拒绝，也不能把目标建出来。
func TestWritesThroughDanglingJunctionsAreDenied(t *testing.T) {
	h := newHarness(t, frontdeskScript(modelSays("占位")))
	gone := filepath.Join(filepath.Dir(h.workspace), "已删除的目标")
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	makeDirLink(t, filepath.Join(h.workspace, "悬空"), gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	paths := []string{"悬空/x.md", "悬空"}
	calls := []agent.ScriptedTurn{}
	for _, path := range paths {
		calls = append(calls, modelCalls(contract.ToolWriteFile, map[string]any{"path": path, "content": "写进悬空联接"}))
	}
	h.setFrontdeskScript(frontdeskScript(append(calls, modelSays("写不了。"))...))
	created := h.prefaceWithEmployee()
	h.mustSetMode(created.ID, session.PermissionFull)

	h.say(created.ID, contract.RoleInitiator, "随便写")

	if got := deniedTools(h.readSession(created.ID)); len(got) != len(paths) {
		t.Fatalf("拒绝留痕 = %d 条，want %d：%+v", len(got), len(paths), got)
	}
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Fatalf("悬空联接的目标不该被建出来：%v", err)
	}
}
