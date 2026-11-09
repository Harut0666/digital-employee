// 审查 0007 的修复（角色与留痕）：写文件和目录文件自动确认只认发起人的话；放开模式直接开工的目录文件要留痕。
package server_test

import (
	"strings"
	"testing"

	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

// 不是发起人（审核人、操作员）的话触发的这一轮：写文件、改文件不管哪个模式都被拒绝并留痕，不弹确认、不落盘。
func TestOnlyTheInitiatorsWordsCanWriteFiles(t *testing.T) {
	for _, role := range []contract.Role{contract.RoleReviewer, contract.RoleOperator} {
		for _, mode := range []string{session.PermissionAsk, session.PermissionAcceptEdits, session.PermissionAuto, session.PermissionYolo} {
			t.Run(string(role)+"/"+mode, func(t *testing.T) {
				h := newHarness(t, frontdeskScript(
					modelCalls(contract.ToolWriteFile, map[string]any{"path": "notes/新.md", "content": "审核人让写的"}),
					modelCalls(contract.ToolEditFile, map[string]any{"path": "旧.md", "oldText": "甲", "newText": "乙"}),
					modelSays("只有发起人能让我写文件。"),
				))
				h.writeWorkspaceFile(t, "旧.md", []byte("甲"))
				created := h.prefaceWithEmployee()
				h.mustSetMode(created.ID, mode)

				h.say(created.ID, role, "帮我写个文件")

				detail := h.readSession(created.ID)
				denied := deniedTools(detail)
				if len(denied) != 2 || denied[0].Tool != contract.ToolWriteFile || denied[1].Tool != contract.ToolEditFile {
					t.Fatalf("拒绝留痕 = %+v", denied)
				}
				for _, entry := range denied {
					if entry.Decision != "" {
						t.Fatalf("程序按角色拒绝的不是发起人的决定：%+v", entry)
					}
					if !strings.Contains(entry.Reason, "只有发起人") || !strings.Contains(entry.Reason, contract.RoleLabels[role]) {
						t.Fatalf("拒绝原因要说明只有发起人可以：%q", entry.Reason)
					}
				}
				// 原因作为工具结果交给了模型
				results := toolResultTexts(h)
				if len(results) != 2 || !strings.Contains(results[0], "只有发起人") {
					t.Fatalf("模型拿到的工具结果 = %v", results)
				}
				if len(permissionCards(detail)) != 0 {
					t.Fatal("不该为非发起人弹权限确认")
				}
				if h.workspaceHas("notes/新.md") {
					t.Fatal("非发起人触发的写不该落盘")
				}
				if got := h.workspaceText(t, "旧.md"); got != "甲" {
					t.Fatalf("旧.md 被改了：%q", got)
				}
				if allowed := entriesOf(detail.Messages, session.KindPermissionAllowed); len(allowed) != 0 {
					t.Fatalf("不该有自动放行的留痕：%+v", allowed)
				}
			})
		}
	}
}

// 说话的人中途换成发起人：这一轮以最近一句话的角色为准，可以写。
func TestTheInitiatorCanWriteAfterAReviewerSpoke(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelSays("先看看。"),
		modelCalls(contract.ToolWriteFile, map[string]any{"path": "a.md", "content": "甲"}),
		modelSays("写好了。"),
	))
	created := h.prefaceWithEmployee()
	h.mustSetMode(created.ID, session.PermissionAcceptEdits)

	h.say(created.ID, contract.RoleReviewer, "你好")
	h.say(created.ID, contract.RoleInitiator, "写一个")

	if got := h.workspaceText(t, "a.md"); got != "甲" {
		t.Fatalf("发起人说话时应该能写：%q", got)
	}
}

// 放开模式的目录文件自动确认也只认发起人：审核人说话时被拒绝，不复制进会话材料。
func TestOnlyTheInitiatorsWordsAutoConfirmFolderFiles(t *testing.T) {
	for _, mode := range []string{session.PermissionAsk, session.PermissionYolo} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, frontdeskScript(
				modelCalls("use_folder_file", map[string]any{"path": folderSales, "reason": "本周销售明细"}),
				modelSays("只有发起人能让我用目录文件。"),
			))
			h.putFolderMaterials(t)
			created := h.createSession()
			h.mustSetMode(created.ID, mode)

			h.say(created.ID, contract.RoleReviewer, "用这份销售明细")

			detail := h.readSession(created.ID)
			if denied := deniedTools(detail); len(denied) != 1 || !strings.Contains(denied[0].Reason, "只有发起人") {
				t.Fatalf("拒绝留痕 = %+v", denied)
			}
			if len(detail.Attachments) != 0 {
				t.Fatalf("不该复制进会话材料：%+v", detail.Attachments)
			}
			if allowed := entriesOf(detail.Messages, session.KindPermissionAllowed); len(allowed) != 0 {
				t.Fatalf("不该有自动放行的留痕：%+v", allowed)
			}
			for _, message := range detail.Messages {
				if message.Card != nil && message.Card.Type == session.CardUseFile {
					t.Fatalf("不该给非发起人发使用目录文件卡：%+v", message.Card)
				}
			}
		})
	}
}

// ---------------------------------------------------------------- yolo 直接开工的留痕

// 放开模式不弹表单直接开工时，自动确认了哪些目录文件也要写进会话日志（auto 下目录文件要人确认，不走这里）。
func TestYoloDirectStartTracesTheFolderFilesItConfirmed(t *testing.T) {
	h := newHarness(t, runScript(t,
		runEmployeeCall(period44, folderSales, folderMinutes),
		modelSays("已开工。"),
	), withScriptedModel())
	h.putFolderMaterials(t)
	created := h.createSession()
	h.mustSetMode(created.ID, session.PermissionYolo)

	h.say(created.ID, contract.RoleInitiator, "出周报")

	detail := h.readSession(created.ID)
	startedTask(t, detail)
	folderTraces := map[string]bool{}
	runTraces := 0
	for _, message := range entriesOf(detail.Messages, session.KindPermissionAllowed) {
		switch message.Tool {
		case session.ToolRunEmployee:
			runTraces++
		case "use_folder_file":
			args, _ := message.Args.(map[string]any)
			path, _ := args["path"].(string)
			folderTraces[path] = true
			if !strings.Contains(message.Reason, "放开") || !strings.Contains(message.Reason, path) {
				t.Fatalf("目录文件的留痕要写明模式和文件：%+v", message)
			}
		}
	}
	if runTraces != 1 || len(folderTraces) != 2 || !folderTraces[folderSales] || !folderTraces[folderMinutes] {
		t.Fatalf("留痕：run_employee %d 条，目录文件 %v", runTraces, folderTraces)
	}
}

// 没有目录文件的直接开工只留一条留痕。
func TestDirectStartWithoutFolderFilesTracesOnce(t *testing.T) {
	h := newHarness(t, runScript(t,
		runEmployeeCall(period44, "销售明细.xlsx", "周会纪要_2026W44.docx"),
		modelSays("已开工。"),
	), withScriptedModel())
	created := h.createSession()
	uploadOK(h, created.ID, a01Files(t)...)
	h.mustSetMode(created.ID, session.PermissionYolo)

	h.say(created.ID, contract.RoleInitiator, "出周报")

	allowed := entriesOf(h.readSession(created.ID).Messages, session.KindPermissionAllowed)
	if len(allowed) != 1 || allowed[0].Tool != session.ToolRunEmployee {
		t.Fatalf("留痕 = %+v", allowed)
	}
}
