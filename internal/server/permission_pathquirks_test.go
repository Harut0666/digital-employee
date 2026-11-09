// 审查 0007 的修复（安全类）：Windows 路径怪癖不能绕过“交付”“材料”不可写。全部从主接缝验证，模型走剧本，不联网。
package server_test

import (
	"os"
	"strings"
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

// 路径怪癖：Windows 会悄悄去掉段尾的点和空格、把 “:” 当数据流分隔、把 CON/NUL 当设备。
// “交付”“材料”还不存在时也不能借它们被建出来；任何模式都一样，也不会弹确认。
func TestWindowsPathQuirksCannotWriteTheProgramFolders(t *testing.T) {
	type attempt struct{ path, reason string }
	attempts := []attempt{
		{"材料./x.md", "结尾"},
		{"交付./x.md", "结尾"},
		{"材料 /x.md", "结尾"},
		{"交付 /x.md", "结尾"},
		{"材料.../x.md", "结尾"},
		{"材料. ./x.md", "结尾"},
		{"notes/材料./x.md", "结尾"},
		{"x.md.", "结尾"},
		{"材料::$DATA/x.md", "冒号"},
		{"notes.md::$DATA", "冒号"},
		{"notes.md:流", "冒号"},
		{"CON", "设备名"},
		{"nul.txt", "设备名"},
		{"notes/Aux.md", "设备名"},
		{"com1", "设备名"},
		{"LPT9.log", "设备名"},
		{"CONIN$", "设备名"},
		{"a<b.md", "不能含有"},
		{"a|b.md", "不能含有"},
		{"a?b.md", "不能含有"},
		{"a*b.md", "不能含有"},
		{"a\x01b.md", "不能含有"},
		// 绕一圈回到“材料”“交付”：规范化之后仍是它们
		{"notes/../材料/x.md", "材料"},
		{"./交付/x.md", "交付"},
		{`材料\x.md`, "材料"},
	}
	for _, mode := range []string{session.PermissionAsk, session.PermissionAcceptEdits, session.PermissionAuto, session.PermissionYolo} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, frontdeskScript(modelSays("占位")))
			// 一条回复的工具轮次有上限（20），分批：每批 10 次调用后说一句话结束这一轮
			const batch = 10
			calls := []agent.ScriptedTurn{}
			rounds := 0
			for index, a := range attempts {
				calls = append(calls, modelCalls(contract.ToolWriteFile, map[string]any{"path": a.path, "content": "绕过"}))
				if (index+1)%batch == 0 || index == len(attempts)-1 {
					calls = append(calls, modelSays("这些我写不了。"))
					rounds++
				}
			}
			h.setFrontdeskScript(frontdeskScript(calls...))
			created := h.prefaceWithEmployee()
			h.mustSetMode(created.ID, mode)

			for round := 0; round < rounds; round++ {
				h.say(created.ID, contract.RoleInitiator, "随便写")
			}

			detail := h.readSession(created.ID)
			denied := deniedTools(detail)
			if len(denied) != len(attempts) {
				t.Fatalf("拒绝留痕 = %d 条，want %d：%+v", len(denied), len(attempts), denied)
			}
			for index, a := range attempts {
				if !strings.Contains(denied[index].Reason, a.reason) {
					t.Fatalf("第 %d 条（%q）的拒绝原因 = %q，want 含 %q", index+1, a.path, denied[index].Reason, a.reason)
				}
			}
			if len(permissionCards(detail)) != 0 {
				t.Fatal("这些写不该弹确认")
			}
			// 工作区里什么都没被建出来：“交付”“材料”这两个文件夹尤其不能出现
			entries, err := os.ReadDir(h.workspace)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				name := entry.Name()
				if strings.HasPrefix(name, "材料") || strings.HasPrefix(name, "交付") || strings.HasPrefix(name, "notes") ||
					strings.HasPrefix(name, "x.md") || strings.HasPrefix(strings.ToLower(name), "con") {
					t.Fatalf("工作区里不该出现 %s", name)
				}
			}
		})
	}
}

// “材料”文件夹已经存在时，带点或空格的写法解到同一个文件夹，同样被拒绝。
func TestWindowsPathQuirksCannotWriteAnExistingMaterialFolder(t *testing.T) {
	h := newHarness(t, frontdeskScript(modelSays("占位")))
	h.writeWorkspaceFile(t, "材料/原件.md", []byte("原件"))
	h.writeWorkspaceFile(t, "交付/周报.md", []byte("周报"))
	paths := []string{"材料./原件.md", "材料 /新.md", "交付./周报.md", "交付 /新.md", "材料::$DATA/新.md"}
	calls := []agent.ScriptedTurn{}
	for _, path := range paths {
		calls = append(calls, modelCalls(contract.ToolWriteFile, map[string]any{"path": path, "content": "覆盖"}))
	}
	h.setFrontdeskScript(frontdeskScript(append(calls, modelSays("写不了。"))...))
	created := h.prefaceWithEmployee()
	h.mustSetMode(created.ID, session.PermissionYolo)

	h.say(created.ID, contract.RoleInitiator, "随便写")

	if denied := deniedTools(h.readSession(created.ID)); len(denied) != len(paths) {
		t.Fatalf("拒绝留痕 = %d 条，want %d：%+v", len(denied), len(paths), denied)
	}
	if got := h.workspaceText(t, "材料/原件.md"); got != "原件" {
		t.Fatalf("材料里的文件被改了：%q", got)
	}
	if got := h.workspaceText(t, "交付/周报.md"); got != "周报" {
		t.Fatalf("交付里的文件被改了：%q", got)
	}
	for _, rel := range []string{"材料/新.md", "交付/新.md"} {
		if h.workspaceHas(rel) {
			t.Fatalf("%s 不该被写出来", rel)
		}
	}
}

// 正常的文件名不受影响：带点的中间名、设备名只是名字的一部分、名字里有空格。
func TestOrdinaryFileNamesStillWork(t *testing.T) {
	h := newHarness(t, frontdeskScript(modelSays("占位")))
	good := []string{"notes/v1.2.md", "console.md", "nullable/说明.md", "comment.txt", "周报 终稿.md"}
	calls := []agent.ScriptedTurn{}
	for _, path := range good {
		calls = append(calls, modelCalls(contract.ToolWriteFile, map[string]any{"path": path, "content": "好"}))
	}
	h.setFrontdeskScript(frontdeskScript(append(calls, modelSays("都写好了。"))...))
	created := h.prefaceWithEmployee()
	h.mustSetMode(created.ID, session.PermissionAcceptEdits)

	h.say(created.ID, contract.RoleInitiator, "写")

	if denied := deniedTools(h.readSession(created.ID)); len(denied) != 0 {
		t.Fatalf("正常的文件名不该被拒绝：%+v", denied)
	}
	for _, rel := range good {
		if !h.workspaceHas(rel) {
			t.Fatalf("%s 应该写出来", rel)
		}
	}
}
