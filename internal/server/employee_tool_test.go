// 员工当工具（T49）：run_employee 与员工表单卡。全部从主接缝验证：HTTP 接口 + 真实引擎 + 剧本模型 + 临时目录。
//
// 观察点：会话消息里的员工表单卡、POST /employee-forms/{requestId} 的答复、工具结果（模型收到的文字和会话日志）、
// 任务快照，以及工作区目录里的文件。
package server_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

// ---------------------------------------------------------------- 装置补充

// waitForm 等一张还没答复的员工表单卡出现在会话里，返回它。
func (h *harness) waitForm(sessionID string) session.Card {
	h.t.Helper()
	deadline := time.Now().Add(stopTimeout)
	for {
		detail := h.readSession(sessionID)
		for _, message := range detail.Messages {
			if card := message.Card; card != nil && card.Type == session.CardEmployeeForm && card.Pending {
				return *card
			}
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("等不到员工表单卡：%+v", detail.Messages)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// formCards 会话里的员工表单卡（不论是否已答复），按出现顺序。
func formCards(detail session.Detail) []session.Card {
	out := []session.Card{}
	for _, message := range detail.Messages {
		if card := message.Card; card != nil && card.Type == session.CardEmployeeForm {
			out = append(out, *card)
		}
	}
	return out
}

// answerForm 人在员工表单卡上答复。
func (h *harness) answerForm(sessionID, requestID string, role contract.Role, body map[string]any) (int, []byte) {
	h.t.Helper()
	return h.postJSON("/api/sessions/"+sessionID+"/employee-forms/"+requestID, role, body)
}

// confirmForm 点“确认开工”：表单字段取卡片上的预填值（extra 里可以覆盖 form、attachments、skipped）。
func (h *harness) confirmForm(sessionID string, card session.Card, extra map[string]any) (int, []byte) {
	h.t.Helper()
	body := map[string]any{"decision": "confirm", "form": card.Draft}
	for key, value := range extra {
		body[key] = value
	}
	return h.answerForm(sessionID, card.RequestID, contract.RoleInitiator, body)
}

func (h *harness) mustConfirmForm(sessionID string, card session.Card, extra map[string]any) session.Reply {
	h.t.Helper()
	status, data := h.confirmForm(sessionID, card, extra)
	if status != http.StatusOK {
		h.t.Fatalf("确认开工失败：%d %s", status, data)
	}
	return decode[session.Reply](h.t, data)
}

// runScript 前台 Agent 的剧本叠在 A-01 的任务剧本上：确认开工后任务能一路跑到待审核。
func runScript(t *testing.T, turns ...agent.ScriptedTurn) agent.ModelScript {
	t.Helper()
	script := caseScript(t, "A-01")
	script[agent.FrontdeskStepID] = turns
	return script
}

// runEmployeeCall 前台 Agent 调 run_employee：第 44 周，材料由 materials 给。
func runEmployeeCall(fields map[string]any, materials ...string) agent.ScriptedTurn {
	args := map[string]any{"employee": harnessEmployee, "fields": fields, "reason": "要出经营周报"}
	if len(materials) > 0 {
		list := make([]any, 0, len(materials))
		for _, item := range materials {
			list = append(list, item)
		}
		args["materials"] = list
	}
	return modelCalls(session.ToolRunEmployee, args)
}

var period44 = map[string]any{"period": map[string]any{"year": 2026, "week": 44}}

// putFolderMaterials 把样例的两份材料放进工作区目录的“资料”文件夹。
func (h *harness) putFolderMaterials(t *testing.T) (sales, minutes string) {
	t.Helper()
	sales = h.writeWorkspaceFile(t, "资料/销售明细.xlsx", sampleFile(t, "销售明细.xlsx").content)
	minutes = h.writeWorkspaceFile(t, "资料/周会纪要_2026W44.docx", sampleFile(t, "周会纪要_2026W44.docx").content)
	return sales, minutes
}

const (
	folderSales   = "资料/销售明细.xlsx"
	folderMinutes = "资料/周会纪要_2026W44.docx"
)

// startedTask 工具结果里的任务编号（模型收到的就是这段文字）。
func startedTask(t *testing.T, detail session.Detail) string {
	t.Helper()
	result, _ := loggedToolResult(t, detail, session.ToolRunEmployee).(map[string]any)
	taskID, _ := result["taskId"].(string)
	if taskID == "" || result["state"] == nil {
		t.Fatalf("run_employee 的结果应带任务编号和当前状态：%+v", result)
	}
	return taskID
}

// ---------------------------------------------------------------- 校验失败：具体错误交回 Agent，不开表单

func TestRunEmployeeValidationFailuresGoBackToTheAgentWithoutAForm(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls(session.ToolRunEmployee, map[string]any{"employee": "no-such-employee", "fields": period44}),
		runEmployeeCall(map[string]any{"period": map[string]any{"year": 2026, "week": 99}}),
		runEmployeeCall(map[string]any{"period": map[string]any{"year": 2026, "week": 44}, "no_such_field": "x"}),
		runEmployeeCall(map[string]any{"period": map[string]any{"year": 2026, "week": 44}, "salesFile": "销售明细.xlsx"}),
		runEmployeeCall(period44, "资料/不存在.xlsx"),
		runEmployeeCall(map[string]any{"period": map[string]any{"year": 2026, "week": 44}, "allowMissingMinutes": true}),
		modelSays("这几次都没成，我换个办法。"),
	), withScriptedModel())
	h.putFolderMaterials(t)
	created := h.createSession()

	h.say(created.ID, contract.RoleInitiator, "出周报")

	detail := h.readSession(created.ID)
	if cards := formCards(detail); len(cards) != 0 {
		t.Fatalf("校验不过不该开表单：%+v", cards)
	}
	if h.taskCount() != 0 {
		t.Fatal("校验不过不该建任务")
	}
	results := toolResultTexts(h)
	if len(results) != 6 {
		t.Fatalf("6 次调用都应收到错误：%v", results)
	}
	for index, want := range []string{"没有这个员工", "period", "no_such_field", "salesFile", "不存在.xlsx", "allowMissingMinutes"} {
		if !strings.Contains(results[index], want) {
			t.Errorf("第 %d 次的错误应点明 %q：%q", index+1, want, results[index])
		}
	}
	if got := lastAssistant(detail.Messages); got != "这几次都没成，我换个办法。" {
		t.Fatalf("回复 = %q", got)
	}
}

// ---------------------------------------------------------------- 员工表单卡：预填标记、材料来源、确认开工

// ask 和 accept_edits 一定弹员工表单卡，即使表单完整有效、材料也齐了。
func TestAskAndAcceptEditsAlwaysShowTheFormEvenWhenItIsComplete(t *testing.T) {
	for _, mode := range []string{session.PermissionAsk, session.PermissionAcceptEdits} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, runScript(t,
				runEmployeeCall(period44, folderSales, folderMinutes),
				modelSays("表单已发给你，请核对。"),
			), withScriptedModel(), withAppMode())
			h.putFolderMaterials(t)
			created := h.createSession()
			h.mustSetMode(created.ID, mode)
			stream := h.openStream(created.ID)
			defer stream.close()
			turn := h.sayAsync(created.ID, contract.RoleInitiator, "出第 44 周周报")

			card := h.waitForm(created.ID)

			if card.Employee != harnessEmployee || card.EmployeeName == "" || !card.Pending || card.RequestID == "" {
				t.Fatalf("表单卡头 = %+v", card)
			}
			// 预填标记：period 是 Agent 填的；材料标出来源是目录里的哪个路径
			if fmt.Sprint(card.Prefilled) != "[period]" {
				t.Fatalf("预填字段 = %v", card.Prefilled)
			}
			if len(card.Materials) != 2 || card.Materials[0].Source != session.MaterialFolder || card.Materials[0].Path != folderSales ||
				card.Materials[0].Name != "销售明细.xlsx" || card.Materials[1].Path != folderMinutes {
				t.Fatalf("材料 = %+v", card.Materials)
			}
			if h.taskCount() != 0 {
				t.Fatal("人确认之前不该开工")
			}
			// 人没确认，这一轮停在工具调用处；表单卡不是权限确认卡
			if cards := permissionCards(h.readSession(created.ID)); len(cards) != 0 {
				t.Fatalf("不该再弹权限确认卡：%+v", cards)
			}
			// 人不通过卡片也不能确认：别的角色
			if status, data := h.confirmForm(created.ID, card, nil); status != http.StatusOK {
				t.Fatalf("确认开工：%d %s", status, data)
			}
			h.waitTurn(stream, turn)
			detail := h.readSession(created.ID)
			taskID := startedTask(t, detail)
			expectState(t, h.settled(taskID), contract.StateAwaitingReview)
			if cards := formCards(detail); len(cards) != 1 || cards[0].Pending || cards[0].Decision != session.DecisionFormConfirmed || cards[0].TaskID != taskID {
				t.Fatalf("确认后的表单卡 = %+v", cards)
			}
			if got := lastAssistant(detail.Messages); got != "表单已发给你，请核对。" {
				t.Fatalf("确认后 Agent 接着说话：%q", got)
			}
		})
	}
}

// 目录文件是确认时的副本：确认之后改原文件不影响任务；确认之前改了则以确认时的内容为准。
func TestFolderFilesAreCopiedAtConfirmation(t *testing.T) {
	h := newHarness(t, runScript(t,
		runEmployeeCall(period44, folderSales, folderMinutes),
		modelSays("已开工。"),
	), withScriptedModel(), withAppMode())
	sales, _ := h.putFolderMaterials(t)
	created := h.createSession()
	stream := h.openStream(created.ID)
	defer stream.close()
	turn := h.sayAsync(created.ID, contract.RoleInitiator, "出第 44 周周报")
	card := h.waitForm(created.ID)
	// 弹卡时还没复制：会话材料里没有这两份
	if got := h.readSession(created.ID); len(got.Attachments) != 0 {
		t.Fatalf("确认之前不该复制目录文件：%+v", got.Attachments)
	}

	h.mustConfirmForm(created.ID, card, nil)
	h.waitTurn(stream, turn)

	size := int64(len(sampleFile(t, "销售明细.xlsx").content))
	got := h.readSession(created.ID)
	if len(got.Attachments) != 2 || got.Attachments[0].Name != "销售明细.xlsx" || got.Attachments[0].Size != size {
		t.Fatalf("确认后的会话材料 = %+v", got.Attachments)
	}
	if err := os.WriteFile(sales, []byte("原文件被改了"), 0o644); err != nil {
		t.Fatal(err)
	}
	copied, err := os.ReadFile(filepath.Join(h.appDirs(), "sessions", created.ID, "attachments", "销售明细.xlsx"))
	if err != nil || int64(len(copied)) != size {
		t.Fatalf("会话里的副本被原文件的修改影响了：%v 大小 %d", err, len(copied))
	}
	snapshot := h.settled(startedTask(t, h.readSession(created.ID)))
	expectState(t, snapshot, contract.StateAwaitingReview)
	if len(snapshot.Attachments) != 2 {
		t.Fatalf("任务附件 = %+v", snapshot.Attachments)
	}
}

// 不是这个员工：把“用户拒绝，原因：选错员工”交回 Agent，不建任务。
func TestNotThisEmployeeHandsTheRefusalBackToTheAgent(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		runEmployeeCall(period44),
		modelSays("好的，我再确认一下该由谁来做。"),
	), withScriptedModel())
	created := h.createSession()
	stream := h.openStream(created.ID)
	defer stream.close()
	turn := h.sayAsync(created.ID, contract.RoleInitiator, "出周报")
	card := h.waitForm(created.ID)

	status, data := h.answerForm(created.ID, card.RequestID, contract.RoleInitiator, map[string]any{"decision": "reject"})
	if status != http.StatusOK {
		t.Fatalf("不是这个员工：%d %s", status, data)
	}
	h.waitTurn(stream, turn)

	if results := toolResultTexts(h); len(results) != 1 || !strings.Contains(results[0], "用户拒绝，原因：选错员工") {
		t.Fatalf("交给模型的结果 = %v", results)
	}
	detail := h.readSession(created.ID)
	if cards := formCards(detail); len(cards) != 1 || cards[0].Pending || cards[0].Decision != session.DecisionFormRejected {
		t.Fatalf("表单卡 = %+v", cards)
	}
	if denied := deniedTools(detail); len(denied) != 1 || denied[0].Tool != session.ToolRunEmployee || !strings.Contains(denied[0].Reason, "选错员工") {
		t.Fatalf("拒绝留痕 = %+v", denied)
	}
	if h.taskCount() != 0 {
		t.Fatal("拒绝之后不该有任务")
	}
	if got := lastAssistant(detail.Messages); got != "好的，我再确认一下该由谁来做。" {
		t.Fatalf("回复 = %q", got)
	}
	// 答复过的卡片不能再答复
	if status, _ := h.answerForm(created.ID, card.RequestID, contract.RoleInitiator, map[string]any{"decision": "confirm", "form": card.Draft}); status != http.StatusConflict {
		t.Fatalf("重复答复应被拒绝：%d", status)
	}
}

func TestOnlyTheInitiatorAnswersTheFormAndTheDecisionIsValidated(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		runEmployeeCall(period44),
		modelSays("好。"),
	), withScriptedModel())
	created := h.createSession()
	stream := h.openStream(created.ID)
	defer stream.close()
	turn := h.sayAsync(created.ID, contract.RoleInitiator, "出周报")
	card := h.waitForm(created.ID)

	for _, role := range []contract.Role{contract.RoleReviewer, contract.RoleOperator, ""} {
		if status, data := h.answerForm(created.ID, card.RequestID, role, map[string]any{"decision": "reject"}); status != http.StatusForbidden {
			t.Fatalf("角色 %q 答复应被拒绝：%d %s", role, status, data)
		}
	}
	if status, data := h.answerForm(created.ID, card.RequestID, contract.RoleInitiator, map[string]any{"decision": "maybe"}); status != http.StatusBadRequest {
		t.Fatalf("不认识的答复：%d %s", status, data)
	}
	if status, data := h.answerForm(created.ID, "0000000000000000", contract.RoleInitiator, map[string]any{"decision": "reject"}); status != http.StatusNotFound {
		t.Fatalf("不存在的表单：%d %s", status, data)
	}
	// 一个也没答复成功：卡片仍在等
	if still := h.waitForm(created.ID); still.RequestID != card.RequestID {
		t.Fatalf("卡片 = %+v", still)
	}
	h.answerForm(created.ID, card.RequestID, contract.RoleInitiator, map[string]any{"decision": "reject"})
	h.waitTurn(stream, turn)
}

// ---------------------------------------------------------------- 开工清单：必需项不齐不能确认，可跳过项要人确认并留痕

func TestRequiredChecklistItemsBlockConfirmationAndSkippingNeedsThePerson(t *testing.T) {
	h := newHarness(t, runScript(t,
		runEmployeeCall(period44),
		modelSays("已开工。"),
	), withScriptedModel())
	created := h.createSession()
	stream := h.openStream(created.ID)
	defer stream.close()
	turn := h.sayAsync(created.ID, contract.RoleInitiator, "出第 44 周周报，材料我马上传")
	card := h.waitForm(created.ID)
	if len(card.Materials) != 0 {
		t.Fatalf("Agent 没给材料：%+v", card.Materials)
	}

	// 缺必需的销售明细：不能确认，卡片仍在等，没有任务
	status, data := h.confirmForm(created.ID, card, nil)
	if status != http.StatusConflict || !strings.Contains(string(data), session.ErrChecklistIncomplete) || !strings.Contains(string(data), "销售明细") {
		t.Fatalf("缺必需项时确认：%d %s", status, data)
	}
	if h.taskCount() != 0 || h.waitForm(created.ID).RequestID != card.RequestID {
		t.Fatal("被清单拦下之后卡片应还在等，也不该有任务")
	}

	// 人补传材料（会话材料），可跳过的“本周纪要”由人确认跳过；必需项和不存在的项不能跳过
	uploadOK(h, created.ID, sampleFile(t, "销售明细.xlsx"))
	for _, item := range []string{"sales", "no-such-item"} {
		if status, data := h.confirmForm(created.ID, card, map[string]any{"attachments": []string{"销售明细.xlsx"}, "skipped": []string{item}}); status != http.StatusBadRequest {
			t.Fatalf("跳过 %s：%d %s", item, status, data)
		}
	}
	if status, data := h.answerForm(created.ID, card.RequestID, contract.RoleReviewer, map[string]any{"decision": "confirm", "form": card.Draft}); status != http.StatusForbidden {
		t.Fatalf("审核人确认：%d %s", status, data)
	}
	h.mustConfirmForm(created.ID, card, map[string]any{"attachments": []string{"销售明细.xlsx"}, "skipped": []string{"minutes"}})
	h.waitTurn(stream, turn)

	detail := h.readSession(created.ID)
	notice := ""
	for _, message := range detail.Messages {
		if message.Kind == session.KindNotice && strings.Contains(message.Text, "本周纪要") {
			notice = message.Text
		}
	}
	if !strings.Contains(notice, "发起人确认跳过") || !strings.Contains(notice, "无纪要继续") {
		t.Fatalf("跳过没有留痕：%q", notice)
	}
	snapshot := h.settled(startedTask(t, detail))
	if snapshot.Form["allowMissingMinutes"] != true {
		t.Fatalf("任务表单里没有跳过的字段：%+v", snapshot.Form)
	}
	if snapshot.Pause != nil && snapshot.Pause.Code == contract.PauseMinutesMissing {
		t.Fatalf("跳过了纪要仍因缺纪要暂停：%+v", snapshot.Pause)
	}
}

// 人在卡片上改的字段才算数。
func TestTheFormAsEditedByThePersonWins(t *testing.T) {
	h := newHarness(t, runScript(t,
		runEmployeeCall(map[string]any{"period": map[string]any{"year": 2026, "week": 43}}, folderSales, folderMinutes),
		modelSays("已开工。"),
	), withScriptedModel())
	h.putFolderMaterials(t)
	created := h.createSession()
	stream := h.openStream(created.ID)
	defer stream.close()
	turn := h.sayAsync(created.ID, contract.RoleInitiator, "出周报")
	card := h.waitForm(created.ID)

	h.mustConfirmForm(created.ID, card, map[string]any{"form": week44Form})
	h.waitTurn(stream, turn)

	snapshot := h.settled(startedTask(t, h.readSession(created.ID)))
	if week := weekOf(t, snapshot.Form); week != 44 {
		t.Fatalf("人改过的统计周期才算数：%v", week)
	}
}

// ---------------------------------------------------------------- 权限模式

func TestPlanModeRefusesRunEmployee(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		runEmployeeCall(period44),
		modelSays("计划模式下我只能给方案。"),
	), withScriptedModel())
	created := h.createSession()
	h.mustSetMode(created.ID, session.PermissionPlan)

	h.say(created.ID, contract.RoleInitiator, "出周报")

	detail := h.readSession(created.ID)
	denied := deniedTools(detail)
	if len(denied) != 1 || denied[0].Tool != session.ToolRunEmployee || !strings.Contains(denied[0].Reason, "计划模式") {
		t.Fatalf("plan 模式下的拒绝留痕 = %+v", denied)
	}
	if len(formCards(detail)) != 0 || h.taskCount() != 0 {
		t.Fatal("plan 模式下不弹表单、不开工")
	}
}

// auto：表单完整有效、必需开工清单项备齐，免确认直接开工（留痕）；不完整就弹表单。yolo 同。
func TestAutoAndYoloStartDirectlyWhenTheFormIsCompleteAndOtherwiseAsk(t *testing.T) {
	for _, mode := range []string{session.PermissionAuto, session.PermissionYolo} {
		t.Run(mode+" 完整有效：直接开工", func(t *testing.T) {
			h := newHarness(t, runScript(t,
				runEmployeeCall(period44, "销售明细.xlsx", "周会纪要_2026W44.docx"),
				modelSays("已经开工了。"),
			), withScriptedModel())
			created := h.createSession()
			uploadOK(h, created.ID, a01Files(t)...)
			h.mustSetMode(created.ID, mode)

			h.say(created.ID, contract.RoleInitiator, "出第 44 周周报")

			detail := h.readSession(created.ID)
			if len(formCards(detail)) != 0 || len(permissionCards(detail)) != 0 {
				t.Fatalf("完整有效的表单不该弹卡：%+v", detail.Messages)
			}
			taskID := startedTask(t, detail)
			expectState(t, h.settled(taskID), contract.StateAwaitingReview)
			if allowed := entriesOf(detail.Messages, session.KindPermissionAllowed); len(allowed) != 1 || allowed[0].Tool != session.ToolRunEmployee || allowed[0].Reason == "" {
				t.Fatalf("免确认直接开工也要留痕：%+v", allowed)
			}
			if got := lastAssistant(detail.Messages); got != "已经开工了。" {
				t.Fatalf("回复 = %q", got)
			}
		})

		t.Run(mode+" 缺必需材料：弹表单", func(t *testing.T) {
			h := newHarness(t, frontdeskScript(
				runEmployeeCall(period44),
				modelSays("缺销售明细，表单发给你补。"),
			), withScriptedModel())
			created := h.createSession()
			h.mustSetMode(created.ID, mode)
			stream := h.openStream(created.ID)
			defer stream.close()
			turn := h.sayAsync(created.ID, contract.RoleInitiator, "出第 44 周周报")

			card := h.waitForm(created.ID)
			h.answerForm(created.ID, card.RequestID, contract.RoleInitiator, map[string]any{"decision": "reject"})
			h.waitTurn(stream, turn)
			if h.taskCount() != 0 {
				t.Fatal("不完整的表单不该直接开工")
			}
		})

		t.Run(mode+" 表单不完整：弹表单", func(t *testing.T) {
			h := newHarness(t, frontdeskScript(
				// 统计周期没填全（缺周）：不完整
				runEmployeeCall(map[string]any{"period": map[string]any{"year": 2026}}, "销售明细.xlsx"),
				modelSays("周次还没定，表单发给你补。"),
			), withScriptedModel())
			created := h.createSession()
			uploadOK(h, created.ID, sampleFile(t, "销售明细.xlsx"))
			h.mustSetMode(created.ID, mode)
			stream := h.openStream(created.ID)
			defer stream.close()
			turn := h.sayAsync(created.ID, contract.RoleInitiator, "出周报")

			card := h.waitForm(created.ID)
			if len(card.Materials) != 1 || card.Materials[0].Source != session.MaterialAttachment {
				t.Fatalf("会话里已有的材料：%+v", card.Materials)
			}
			h.answerForm(created.ID, card.RequestID, contract.RoleInitiator, map[string]any{"decision": "reject"})
			h.waitTurn(stream, turn)
			if h.taskCount() != 0 {
				t.Fatal("不完整的表单不该直接开工")
			}
		})
	}
}

// 目录文件用作材料：auto 要问（弹表单卡，由人确认）；yolo 自动确认，直接开工并复制。
func TestOnlyYoloAutoConfirmsFolderFiles(t *testing.T) {
	t.Run("auto：弹表单让人确认", func(t *testing.T) {
		h := newHarness(t, runScript(t,
			runEmployeeCall(period44, folderSales, folderMinutes),
			modelSays("好。"),
		), withScriptedModel())
		h.putFolderMaterials(t)
		created := h.createSession()
		h.mustSetMode(created.ID, session.PermissionAuto)
		stream := h.openStream(created.ID)
		defer stream.close()
		turn := h.sayAsync(created.ID, contract.RoleInitiator, "出周报")

		card := h.waitForm(created.ID)
		if h.taskCount() != 0 {
			t.Fatal("auto 下目录文件要人确认，不该直接开工")
		}
		h.mustConfirmForm(created.ID, card, nil)
		h.waitTurn(stream, turn)
		expectState(t, h.settled(startedTask(t, h.readSession(created.ID))), contract.StateAwaitingReview)
	})

	t.Run("yolo：自动确认并复制，直接开工", func(t *testing.T) {
		h := newHarness(t, runScript(t,
			runEmployeeCall(period44, folderSales, folderMinutes),
			modelSays("已开工。"),
		), withScriptedModel())
		h.putFolderMaterials(t)
		created := h.createSession()
		h.mustSetMode(created.ID, session.PermissionYolo)

		h.say(created.ID, contract.RoleInitiator, "出周报")

		detail := h.readSession(created.ID)
		if len(formCards(detail)) != 0 {
			t.Fatalf("yolo 下不该弹表单：%+v", formCards(detail))
		}
		if len(detail.Attachments) != 2 {
			t.Fatalf("目录文件应在开工时复制进会话材料：%+v", detail.Attachments)
		}
		expectState(t, h.settled(startedTask(t, detail)), contract.StateAwaitingReview)
	})
}

// use_folder_file（提议把目录文件用作材料）也过权限关口：plan 拒绝，yolo 自动确认，其余发“使用目录文件”卡由人确认。
func TestUseFolderFileFollowsThePermissionMode(t *testing.T) {
	cases := []struct {
		mode string
		want string // deny / card / auto
	}{
		{session.PermissionPlan, "deny"},
		{session.PermissionAsk, "card"},
		{session.PermissionAcceptEdits, "card"},
		{session.PermissionAuto, "card"},
		{session.PermissionYolo, "auto"},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			h := newHarness(t, frontdeskScript(
				modelCalls("use_folder_file", map[string]any{"path": folderSales, "reason": "本周销售明细"}),
				modelSays("看完了。"),
			))
			h.putFolderMaterials(t)
			created := h.createSession()
			h.mustSetMode(created.ID, tc.mode)

			h.say(created.ID, contract.RoleInitiator, "用这份销售明细")

			detail := h.readSession(created.ID)
			switch tc.want {
			case "deny":
				if denied := deniedTools(detail); len(denied) != 1 || !strings.Contains(denied[0].Reason, "计划模式") || len(detail.Attachments) != 0 {
					t.Fatalf("plan 下的拒绝 = %+v，材料 %+v", denied, detail.Attachments)
				}
			case "card":
				card := cardOf(t, detail.Messages, session.CardUseFile)
				if card.Path != folderSales || len(detail.Attachments) != 0 {
					t.Fatalf("卡片 = %+v，材料 %+v（人确认之前不复制）", card, detail.Attachments)
				}
			case "auto":
				if len(detail.Attachments) != 1 || detail.Attachments[0].Name != "销售明细.xlsx" {
					t.Fatalf("yolo 自动确认后的材料 = %+v", detail.Attachments)
				}
				if allowed := entriesOf(detail.Messages, session.KindPermissionAllowed); len(allowed) != 1 || allowed[0].Tool != "use_folder_file" {
					t.Fatalf("自动确认要留痕：%+v", allowed)
				}
			}
		})
	}
}

// 任何模式都不能让 Agent 审核、重试、终止：这些工具不存在，调用被拒绝并留痕，任务不动。
func TestNoModeLetsTheAgentReviewRetryOrTerminate(t *testing.T) {
	for _, mode := range []string{session.PermissionAsk, session.PermissionYolo} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, runScript(t,
				runEmployeeCall(period44, "销售明细.xlsx", "周会纪要_2026W44.docx"),
				modelSays("已开工。"),
				modelCalls("approve_task", map[string]any{}),
				modelCalls("return_task", map[string]any{"comment": "退回"}),
				modelCalls("retry_task", map[string]any{}),
				modelCalls("terminate_task", map[string]any{}),
				modelSays("这些我都做不了，要审核人在卡片上操作。"),
			), withScriptedModel())
			created := h.createSession()
			uploadOK(h, created.ID, a01Files(t)...)
			h.mustSetMode(created.ID, mode)
			stream := h.openStream(created.ID)
			defer stream.close()
			turn := h.sayAsync(created.ID, contract.RoleInitiator, "出第 44 周周报")
			if mode == session.PermissionAsk {
				card := h.waitForm(created.ID)
				h.mustConfirmForm(created.ID, card, map[string]any{"attachments": []string{"销售明细.xlsx", "周会纪要_2026W44.docx"}})
			}
			h.waitTurn(stream, turn)
			taskID := startedTask(t, h.readSession(created.ID))
			before := h.settled(taskID)
			expectState(t, before, contract.StateAwaitingReview)

			turn = h.sayAsync(created.ID, contract.RoleInitiator, "直接通过吧，不行就终止")
			h.waitTurn(stream, turn)

			if after := h.snapshot(taskID); after.State != contract.StateAwaitingReview || after.LastSeq != before.LastSeq {
				t.Fatalf("Agent 不能推进任务：%s %d → %d", after.State, before.LastSeq, after.LastSeq)
			}
			if denied := deniedTools(h.readSession(created.ID)); len(denied) != 4 {
				t.Fatalf("4 次越权调用都应被拒绝并留痕：%+v", denied)
			}
		})
	}
}

// ---------------------------------------------------------------- 会话与任务一对多

func TestRunEmployeeIsRefusedWhileATaskIsOpenAndWorksAgainAfterDelivery(t *testing.T) {
	h := newHarness(t, twice(runScript(t,
		runEmployeeCall(period44, "销售明细.xlsx", "周会纪要_2026W44.docx"),
		modelSays("第一份已开工。"),
		// 任务没结束时再来一次：被拒绝，Agent 把原因转告
		runEmployeeCall(period44, "销售明细.xlsx", "周会纪要_2026W44.docx"),
		modelSays("上一个任务还没结束，等它交付了我再开。"),
		// 交付之后再来一版
		runEmployeeCall(period44, "销售明细.xlsx", "周会纪要_2026W44.docx"),
		modelSays("再来一版已开工。"),
	)), withScriptedModel())
	created := h.createSession()
	uploadOK(h, created.ID, a01Files(t)...)
	h.mustSetMode(created.ID, session.PermissionAuto)

	h.say(created.ID, contract.RoleInitiator, "出第 44 周周报")
	first := startedTask(t, h.readSession(created.ID))
	expectState(t, h.settled(first), contract.StateAwaitingReview)

	h.say(created.ID, contract.RoleInitiator, "再出一份")
	detail := h.readSession(created.ID)
	if h.taskCount() != 1 {
		t.Fatalf("任务没结束时被拒绝，不该多出任务：%d", h.taskCount())
	}
	refused := ""
	for _, message := range detail.Messages {
		if message.Kind == session.KindToolCalled && message.Tool == session.ToolRunEmployee && message.Reason != "" {
			refused = message.Reason
		}
	}
	if !strings.Contains(refused, "还没结束") || !strings.Contains(refused, first) {
		t.Fatalf("拒绝的原因应说明哪个任务还没结束：%q", refused)
	}

	expectState(t, h.command(first, reviewer, cmd(contract.CmdApprove)), contract.StateDelivered)
	h.say(created.ID, contract.RoleInitiator, "再来一版")
	got := h.readSession(created.ID)
	if len(got.TaskIDs) != 2 || got.TaskIDs[0] != first {
		t.Fatalf("交付后可以再运行一个员工：%+v", got.TaskIDs)
	}
	expectState(t, h.settled(got.TaskID), contract.StateAwaitingReview)
}

// ---------------------------------------------------------------- 载入公开样例

// copySamples 把仓库里的公开样例放进应用目录（生产里由首次启动释放）。
func (h *harness) copySamples(t *testing.T) {
	t.Helper()
	copyEmployee(t, repoPath("samples", "scenario-a"), filepath.Join(h.appDirs(), "samples", "scenario-a"))
	raw, err := os.ReadFile(repoPath("samples", "cases", "A-01.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.appDirs(), "samples", "cases")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "A-01.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// workspaceFiles 工作区目录里所有文件的相对路径。
func (h *harness) workspaceFiles(t *testing.T) []string {
	t.Helper()
	out := []string{}
	_ = filepath.Walk(h.workspace, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(h.workspace, path)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

func TestLoadingThePublicSampleGivesAPrefilledFormAndKeepsTheWorkspaceClean(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"), withScriptedModel(), withAppMode())
	h.copySamples(t)
	created := h.createSession()

	status, data := h.postJSON("/api/sessions/"+created.ID+"/sample", contract.RoleInitiator, map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("载入公开样例：%d %s", status, data)
	}
	card := h.waitForm(created.ID)
	if card.Employee != harnessEmployee || !card.Pending || !card.Standalone {
		t.Fatalf("样例表单卡 = %+v", card)
	}
	if fmt.Sprint(card.Prefilled) != "[period]" || weekOf(t, card.Draft) != 44 {
		t.Fatalf("样例的统计周期应预填：%v %+v", card.Prefilled, card.Draft)
	}
	if len(card.Materials) != 2 || card.Materials[0].Source != session.MaterialSample || card.Materials[0].Name != "销售明细.xlsx" {
		t.Fatalf("样例材料 = %+v", card.Materials)
	}
	// 样例文件不写进人的工作目录；也没有会话材料、没有任务
	if files := h.workspaceFiles(t); len(files) != 0 {
		t.Fatalf("工作区目录里不该有样例文件：%v", files)
	}
	if h.taskCount() != 0 || len(h.readSession(created.ID).Attachments) != 0 {
		t.Fatal("人确认之前不该开工，也不该复制材料")
	}
	// 只有发起人能载入
	if status, _ := h.postJSON("/api/sessions/"+created.ID+"/sample", contract.RoleReviewer, map[string]any{}); status != http.StatusForbidden {
		t.Fatalf("审核人载入样例：%d", status)
	}

	h.mustConfirmForm(created.ID, card, nil)

	got := h.readSession(created.ID)
	if got.TaskID == "" || got.Employee != harnessEmployee {
		t.Fatalf("确认后的会话 = %+v", got.View)
	}
	expectState(t, h.settled(got.TaskID), contract.StateAwaitingReview)
	for _, file := range h.workspaceFiles(t) {
		if strings.Contains(file, "销售明细") || strings.Contains(file, "周会纪要") {
			t.Fatalf("任务开工后工作区目录里也不该有样例材料：%s", file)
		}
	}
	if cards := formCards(got); len(cards) != 1 || cards[0].Pending || cards[0].Decision != session.DecisionFormConfirmed {
		t.Fatalf("确认后的表单卡 = %+v", cards)
	}
}

// 没有样例（应用目录里没释放）时给出明确的错误。
func TestLoadingTheSampleWithoutSamplesFails(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel(), withAppMode())
	created := h.createSession()

	status, data := h.postJSON("/api/sessions/"+created.ID+"/sample", contract.RoleInitiator, map[string]any{})

	if status != http.StatusNotFound {
		t.Fatalf("没有样例：%d %s", status, data)
	}
}

// ---------------------------------------------------------------- 停止

// 表单卡等人时点停止：这一轮结束，未决的卡片记为已取消，不开工。
func TestStoppingWhileTheFormIsPendingCancelsIt(t *testing.T) {
	h := newHarness(t, frontdeskScript(runEmployeeCall(period44), modelSays("不会说到这里。")), withScriptedModel(), withSerial())
	created := h.createSession()
	stream := h.openStream(created.ID)
	defer stream.close()
	turn := h.sayAsync(created.ID, contract.RoleInitiator, "出周报")
	card := h.waitForm(created.ID)

	status, data := h.send(http.MethodPost, "/api/sessions/"+created.ID+"/stop", contract.RoleInitiator, "", nil)
	if status != http.StatusOK {
		t.Fatalf("停止：%d %s", status, data)
	}
	h.waitTurn(stream, turn)

	detail := h.readSession(created.ID)
	cards := formCards(detail)
	if len(cards) != 1 || cards[0].Pending || cards[0].Decision != session.DecisionCancelled {
		t.Fatalf("停止后的表单卡 = %+v", cards)
	}
	if h.taskCount() != 0 {
		t.Fatal("停止之后不该开工")
	}
	if status, _ := h.confirmForm(created.ID, card, nil); status != http.StatusConflict {
		t.Fatalf("已取消的卡片不能再确认：%d", status)
	}
}

// ---------------------------------------------------------------- 系统提示词和工具

func TestAgentToolsAndPromptDescribeEmployeesAsTools(t *testing.T) {
	h := newHarness(t, frontdeskScript(modelSays("好的。")), withScriptedModel())
	created := h.createSession()

	h.say(created.ID, contract.RoleInitiator, "你好")

	request := frontdeskRequests(h)[0].Request
	names := toolNames(request.Tools)
	for _, want := range []string{session.ToolRunEmployee, "list_files", "read_file", "write_file", "edit_file", "use_folder_file"} {
		if !containsString(names, want) {
			t.Errorf("应开放 %s：%v", want, names)
		}
	}
	for _, gone := range []string{"propose_employee", "fill_form_draft", "propose_card"} {
		if containsString(names, gone) {
			t.Errorf("%s 不再给 Agent：%v", gone, names)
		}
	}
	system := request.Messages[0].Content[0].Text
	for _, want := range []string{harnessEmployee, "经营周报", "period", "权限模式", "run_employee"} {
		if !strings.Contains(system, want) {
			t.Errorf("系统提示词应提到 %q", want)
		}
	}
	for _, gone := range []string{"propose_employee", "确认员工后"} {
		if strings.Contains(system, gone) {
			t.Errorf("系统提示词不该再有旧流程 %q", gone)
		}
	}
}

// ---------------------------------------------------------------- 状态点和角色

// 员工表单在等发起人确认：发起人看到“等你处理”，别的角色不是；答复之后回到空闲。
func TestAPendingFormLightsTheDotForTheInitiator(t *testing.T) {
	h := newHarness(t, frontdeskScript(runEmployeeCall(period44), modelSays("好。")), withScriptedModel())
	created := h.createSession()
	stream := h.openStream(created.ID)
	defer stream.close()
	turn := h.sayAsync(created.ID, contract.RoleInitiator, "出周报")
	card := h.waitForm(created.ID)

	if got := h.sessionSummaryOf(created.ID, contract.RoleInitiator); got.StatusDot != "waiting" {
		t.Fatalf("发起人看表单在等的会话：%+v", got)
	}
	if got := h.sessionSummaryOf(created.ID, contract.RoleReviewer); got.StatusDot == "waiting" {
		t.Fatalf("审核人不该看到等你处理：%+v", got)
	}
	h.answerForm(created.ID, card.RequestID, contract.RoleInitiator, map[string]any{"decision": "reject"})
	h.waitTurn(stream, turn)
	h.waitIdle(created.ID)
	if got := h.sessionSummaryOf(created.ID, contract.RoleInitiator); got.StatusDot != "idle" {
		t.Fatalf("答复之后：%+v", got)
	}
}

// 现在说话的不是发起人：Agent 调 run_employee 被拒绝并留痕，不弹表单。
func TestOnlyTheInitiatorsWordsCanStartAnEmployee(t *testing.T) {
	h := newHarness(t, frontdeskScript(runEmployeeCall(period44), modelSays("要请发起人来说。")), withScriptedModel())
	created := h.createSession()

	h.say(created.ID, contract.RoleReviewer, "出周报")

	detail := h.readSession(created.ID)
	denied := deniedTools(detail)
	if len(denied) != 1 || denied[0].Tool != session.ToolRunEmployee || !strings.Contains(denied[0].Reason, "只有发起人") {
		t.Fatalf("审核人说话时的拒绝留痕 = %+v", denied)
	}
	if len(formCards(detail)) != 0 || h.taskCount() != 0 {
		t.Fatal("不该弹表单或开工")
	}
}
