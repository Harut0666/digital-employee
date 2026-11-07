// 开工清单：全部从主接缝验证。清单只判定文件是否已上传、必填项是否已填写，内容问题留给准入校验。
package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

// draftingSession 发起人开会话并确认经营周报员工。
func draftingSession(h *harness) session.View {
	h.t.Helper()
	created := h.createSession()
	h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)
	return created
}

func TestMissingRequiredItemsRefuseToStart(t *testing.T) {
	h := newHarness(t, nil)
	opened := draftingSession(h)

	status, data := h.trySubmit(opened.ID, contract.RoleInitiator, week44Form)

	if status != http.StatusConflict || !strings.Contains(string(data), session.ErrChecklistIncomplete) || !strings.Contains(string(data), "销售明细") {
		t.Fatalf("缺销售明细时提交：%d %s", status, data)
	}
	if count := h.taskCount(); count != 0 {
		t.Fatalf("被清单拦下的提交不该留下任务，有 %d 个", count)
	}
}

// itemStatus 把清单写成“id=状态”的列表，便于逐字比较。
func itemStatus(t *testing.T, checklist *session.Checklist) string {
	t.Helper()
	if checklist == nil {
		t.Fatal("会话里没有开工清单")
	}
	rows := []string{}
	for _, item := range checklist.Items {
		rows = append(rows, item.ID+"="+item.Status)
	}
	return strings.Join(rows, " ")
}

func TestOnlyTheProgramTicksTheChecklist(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		// Agent 想自己勾选：没有这样的工具，也不能替人上传
		modelCalls("tick_checklist", map[string]any{"item": "sales"}),
		modelCalls("fill_form_draft", map[string]any{"salesFile": "销售明细.xlsx"}),
		modelCalls("fill_form_draft", map[string]any{"period": map[string]any{"year": 2026, "week": 44}}),
		modelSays("统计周期已填好，还缺销售明细。"),
	))
	opened := draftingSession(h)
	if got := itemStatus(t, h.readSession(opened.ID).Checklist); got != "period=missing sales=missing minutes=missing" {
		t.Fatalf("接手时的清单：%s", got)
	}

	h.say(opened.ID, contract.RoleInitiator, "第 44 周，销售明细我已经传了")

	got := h.readSession(opened.ID)
	if status := itemStatus(t, got.Checklist); status != "period=done sales=missing minutes=missing" {
		t.Fatalf("Agent 只能把周期填进草稿，不能勾选材料：%s", status)
	}
	if refused := entriesOf(got.Messages, session.KindToolDenied); len(refused) != 2 {
		t.Fatalf("越权勾选应被拒绝并留痕：%+v", refused)
	}
	if got.Checklist.Ready || strings.Join(got.Checklist.Missing, "、") != "销售明细" {
		t.Fatalf("清单 = %+v", got.Checklist)
	}

	// 人上传了文件，程序才勾选
	reply := uploadOK(h, opened.ID, sampleFile(t, "销售明细.xlsx"))
	if status := itemStatus(t, reply.Session.Checklist); status != "period=done sales=done minutes=missing" || !reply.Session.Checklist.Ready {
		t.Fatalf("上传后的清单：%s %+v", status, reply.Session.Checklist)
	}
}

// skipItem 人在清单上确认跳过一项。
func (h *harness) skipItem(sessionID string, role contract.Role, item string) (int, []byte) {
	h.t.Helper()
	return h.postJSON("/api/sessions/"+sessionID+"/checklist/"+item+"/skip", role, map[string]any{})
}

func TestSkippedItemIsConfirmedByThePersonAndLeavesATrace(t *testing.T) {
	h := newHarness(t, nil)
	opened := draftingSession(h)
	uploadOK(h, opened.ID, sampleFile(t, "销售明细.xlsx"))

	if status, data := h.skipItem(opened.ID, contract.RoleReviewer, "minutes"); status != http.StatusForbidden {
		t.Fatalf("审核人跳过：%d %s", status, data)
	}
	if status, data := h.skipItem(opened.ID, contract.RoleInitiator, "sales"); status != http.StatusBadRequest {
		t.Fatalf("跳过必需项：%d %s", status, data)
	}
	if status, data := h.skipItem(opened.ID, contract.RoleInitiator, "no-such-item"); status != http.StatusBadRequest {
		t.Fatalf("跳过不存在的项：%d %s", status, data)
	}
	status, data := h.skipItem(opened.ID, contract.RoleInitiator, "minutes")
	if status != http.StatusOK {
		t.Fatalf("跳过本周纪要：%d %s", status, data)
	}
	if got := itemStatus(t, decode[session.Reply](t, data).Session.Checklist); got != "period=missing sales=done minutes=skipped" {
		t.Fatalf("跳过后的清单：%s", got)
	}
	// 留痕：会话里写明谁跳过了哪一项
	got := h.readSession(opened.ID)
	if notice := textOf(t, got.Messages, session.KindNotice); !strings.Contains(notice, "本周纪要") || !strings.Contains(notice, "无纪要继续") {
		t.Fatalf("跳过没有留痕：%q", notice)
	}

	// 开工时写入表单字段，准入校验沿用“无纪要继续”的语义，不再因缺纪要暂停
	taskID := h.submit(opened.ID, week44Form).Session.TaskID
	snapshot := h.settled(taskID)
	if snapshot.Form["allowMissingMinutes"] != true {
		t.Fatalf("任务表单里没有跳过的字段：%+v", snapshot.Form)
	}
	if snapshot.Pause != nil && snapshot.Pause.Code == contract.PauseMinutesMissing {
		t.Fatalf("跳过了纪要仍因缺纪要暂停：%+v", snapshot.Pause)
	}
	// 开工后不能再跳过
	if status, data := h.skipItem(opened.ID, contract.RoleInitiator, "minutes"); status != http.StatusConflict {
		t.Fatalf("开工后跳过：%d %s", status, data)
	}
}

func TestWrongWeekMinutesStartAndAdmissionPausesForASupplement(t *testing.T) {
	h := newHarness(t, nil)
	opened := draftingSession(h)

	// 清单只看文件传了没有：第 43 周的纪要也算“本周纪要”已上传
	reply := uploadOK(h, opened.ID, sampleFile(t, "销售明细.xlsx"), sampleFile(t, "周会纪要_2026W43.docx"))
	if got := itemStatus(t, reply.Session.Checklist); got != "period=missing sales=done minutes=done" {
		t.Fatalf("上传后的清单：%s", got)
	}
	taskID := h.submit(opened.ID, week44Form).Session.TaskID

	// 周次不符是内容问题，由准入校验暂停并提示补充
	snapshot := h.settled(taskID)
	expectState(t, snapshot, contract.StateAwaitingInitiator)
	if snapshot.Pause.Code != contract.PauseMinutesWeekMismatch {
		t.Fatalf("暂停原因 = %+v", snapshot.Pause)
	}
	pause := cardOf(t, h.readSession(opened.ID).Messages, session.CardPause)
	if !strings.Contains(fmt.Sprint(actionTable(contract.TaskSnapshot{Actions: pause.Actions})), "supplement=initiator") {
		t.Fatalf("暂停卡片上应能补充：%+v", pause.Actions)
	}
	// 补上本周纪要，越过准入
	after := h.supplement(taskID, []upload{sampleFile(t, "周会纪要_2026W44.docx")})
	if after.Pause != nil && (after.Pause.Code == contract.PauseMinutesWeekMismatch || after.Pause.Code == contract.PauseMinutesMissing) {
		t.Fatalf("补充后仍卡在纪要：%+v", after.Pause)
	}
}

func TestFrontdeskSeesTheChecklistToGuideThePerson(t *testing.T) {
	h := newHarness(t, frontdeskScript(modelSays("还缺销售明细，请上传。")))
	opened := draftingSession(h)
	uploadOK(h, opened.ID, sampleFile(t, "周会纪要_2026W44.docx"))
	if status, data := h.skipItem(opened.ID, contract.RoleInitiator, "minutes"); status != http.StatusOK {
		t.Fatalf("跳过本周纪要：%d %s", status, data)
	}

	h.say(opened.ID, contract.RoleInitiator, "还缺什么")

	requests := frontdeskRequests(h)
	system := requests[len(requests)-1].Request.Messages[0].Content[0].Text
	for _, want := range []string{"开工清单", "统计周期：还缺（必需）", "销售明细：还缺（必需）", "本周纪要：已备齐"} {
		if !strings.Contains(system, want) {
			t.Fatalf("前台 Agent 的提示词里没有 %q：\n%s", want, system)
		}
	}
}

// saveChecklist 在员工配置页保存开工清单。
func (h *harness) saveChecklist(items []map[string]any) (int, []byte) {
	h.t.Helper()
	raw, err := json.Marshal(map[string]any{"data": map[string]any{"items": items}})
	if err != nil {
		h.t.Fatal(err)
	}
	return h.send(http.MethodPut, "/api/employees/"+harnessEmployee+"/config/checklist.yaml", contract.RoleOperator, "application/json", bytes.NewReader(raw))
}

func TestChecklistIsEditedOnTheEmployeeConfigPage(t *testing.T) {
	h := newHarness(t, nil)

	// 配置页能读到清单文件
	status, data := h.send(http.MethodGet, "/api/employees/"+harnessEmployee+"/config", "", "", nil)
	if status != http.StatusOK || !strings.Contains(string(data), `"path":"checklist.yaml"`) {
		t.Fatalf("配置页没有开工清单：%d", status)
	}
	// 判定来源写错、引用不存在的字段都不能保存
	bad := []map[string]any{
		{"id": "sales", "label": "销售明细", "required": true, "source": "guess"},
		{"id": "remark", "label": "备注", "required": true, "source": "field", "field": "no_such_field"},
	}
	if status, data := h.saveChecklist(bad); status != http.StatusBadRequest || !strings.Contains(string(data), "source") || !strings.Contains(string(data), "no_such_field") {
		t.Fatalf("保存错误的清单：%d %s", status, data)
	}

	// 岗位要求变了：备注改成必填，纪要不再要求。保存后新会话立即使用
	status, data = h.saveChecklist([]map[string]any{
		{"id": "sales", "label": "销售明细", "required": true, "source": "upload", "accept": []any{".xlsx", ".csv"}},
		{"id": "remark", "label": "写作要求", "required": true, "source": "field", "field": "remark"},
	})
	if status != http.StatusOK {
		t.Fatalf("保存清单失败：%d %s", status, data)
	}
	opened := draftingSession(h)
	if got := itemStatus(t, h.readSession(opened.ID).Checklist); got != "sales=missing remark=missing" {
		t.Fatalf("保存后的清单：%s", got)
	}
}

// uploadOK 在会话里上传材料，要求成功。
func uploadOK(h *harness, sessionID string, files ...upload) session.Reply {
	h.t.Helper()
	status, data := h.uploadTo(sessionID, files)
	if status != http.StatusOK {
		h.t.Fatalf("上传失败：%d %s", status, data)
	}
	return decode[session.Reply](h.t, data)
}
