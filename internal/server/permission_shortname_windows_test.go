//go:build windows

package server_test

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

// shortPathOf 路径的 8.3 短名；卷上没有开 8.3 短名时返回原路径。
func shortPathOf(t *testing.T, path string) string {
	t.Helper()
	long, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, 1024)
	n, err := syscall.GetShortPathName(long, &buffer[0], uint32(len(buffer)))
	if err != nil || n == 0 {
		return path
	}
	return syscall.UTF16ToString(buffer[:n])
}

// 用 8.3 短名写“交付”“材料”（它们已经存在时短名指向同一个文件夹）：解开之后仍然是保留文件夹，被拒绝。
func TestShortNamesCannotWriteTheProgramFolders(t *testing.T) {
	h := newHarness(t, frontdeskScript(modelSays("占位")))
	h.writeWorkspaceFile(t, "材料/原件.md", []byte("原件"))
	h.writeWorkspaceFile(t, "交付/周报.md", []byte("周报"))
	var paths []string
	for _, name := range []string{"材料", "交付"} {
		short := filepath.Base(shortPathOf(t, filepath.Join(h.workspace, name)))
		if strings.EqualFold(short, name) {
			t.Skipf("这个卷上没有 %s 的 8.3 短名", name)
		}
		paths = append(paths, short+"/新.md")
	}
	calls := []agent.ScriptedTurn{}
	for _, path := range paths {
		calls = append(calls, modelCalls(contract.ToolWriteFile, map[string]any{"path": path, "content": "绕过"}))
	}
	h.setFrontdeskScript(frontdeskScript(append(calls, modelSays("写不了。"))...))
	created := h.prefaceWithEmployee()
	h.mustSetMode(created.ID, session.PermissionFull)

	h.say(created.ID, contract.RoleInitiator, "随便写")

	if denied := deniedTools(h.readSession(created.ID)); len(denied) != len(paths) {
		t.Fatalf("拒绝留痕 = %d 条，want %d：%+v", len(denied), len(paths), denied)
	}
	for _, rel := range []string{"材料/新.md", "交付/新.md"} {
		if h.workspaceHas(rel) {
			t.Fatalf("%s 不该被写出来", rel)
		}
	}
}
