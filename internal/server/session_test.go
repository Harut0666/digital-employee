// 会话与前台 Agent：全部从主接缝验证，模型走剧本，不联网。
package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

// createSession 发起人开一个新会话（应用目录模式下开在装置的工作区里）。
func (h *harness) createSession() session.View {
	h.t.Helper()
	if len(h.workspaces) > 0 {
		return h.createSessionIn(appWorkspaceID)
	}
	status, data := h.send(http.MethodPost, "/api/sessions", contract.RoleInitiator, "", nil)
	if status != http.StatusCreated {
		h.t.Fatalf("建会话失败：%d %s", status, data)
	}
	return decode[session.View](h.t, data)
}

// readSession 读会话和它的全部消息。
func (h *harness) readSession(id string) session.Detail {
	h.t.Helper()
	status, data := h.send(http.MethodGet, "/api/sessions/"+id, "", "", nil)
	if status != http.StatusOK {
		h.t.Fatalf("读会话失败：%d %s", status, data)
	}
	return decode[session.Detail](h.t, data)
}

func (h *harness) postJSON(path string, role contract.Role, body any) (int, []byte) {
	h.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.send(http.MethodPost, path, role, "application/json", bytes.NewReader(raw))
}

// say 发一条消息，等完整回复。
func (h *harness) say(sessionID string, role contract.Role, text string) session.Reply {
	h.t.Helper()
	status, data := h.postJSON("/api/sessions/"+sessionID+"/messages", role, map[string]any{"text": text})
	if status != http.StatusOK {
		h.t.Fatalf("发消息失败：%d %s", status, data)
	}
	return decode[session.Reply](h.t, data)
}

// frontdeskScript 前台 Agent 的剧本。
func frontdeskScript(turns ...agent.ScriptedTurn) agent.ModelScript {
	return agent.ModelScript{agent.FrontdeskStepID: turns}
}

func modelSays(text string) agent.ScriptedTurn { return agent.ScriptedTurn{Text: &text} }

func modelCalls(name string, arguments map[string]any) agent.ScriptedTurn {
	return agent.ScriptedTurn{ToolCalls: []agent.ScriptedToolCall{{Name: name, Arguments: arguments}}}
}

// cardOf 取一批消息里最后一张某类型的卡片。
func cardOf(t *testing.T, messages []session.Message, cardType string) session.Card {
	t.Helper()
	for index := len(messages) - 1; index >= 0; index-- {
		if card := messages[index].Card; card != nil && card.Type == cardType {
			return *card
		}
	}
	t.Fatalf("消息里没有 %s 卡片：%+v", cardType, messages)
	return session.Card{}
}

// textOf 取一批消息里最后一条某类型的文字。
func textOf(t *testing.T, messages []session.Message, kind string) string {
	t.Helper()
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Kind == kind {
			return messages[index].Text
		}
	}
	t.Fatalf("消息里没有 %s：%+v", kind, messages)
	return ""
}

func TestOneSentenceGetsAnEmployeeProposal(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("propose_employee", map[string]any{"employee": harnessEmployee, "reason": "要出经营周报"}),
		modelSays("这项工作适合经营周报员工，请确认。"),
	), withScriptedModel())
	created := h.createSession()

	reply := h.say(created.ID, contract.RoleInitiator, "帮我出第 44 周的周报")

	if got := textOf(t, reply.Messages, session.KindAssistant); got != "这项工作适合经营周报员工，请确认。" {
		t.Fatalf("回复 = %q", got)
	}
	card := cardOf(t, reply.Messages, session.CardEmployeeConfirm)
	if card.Proposed != harnessEmployee || card.Source != session.SourceModel || card.Reason != "要出经营周报" {
		t.Fatalf("员工确认卡片 = %+v", card)
	}
	if len(card.Candidates) != 1 || card.Candidates[0].Name != "经营周报员工" {
		t.Fatalf("候选员工 = %+v", card.Candidates)
	}

	// 提议不等于接手：人确认之前会话里没有员工
	got := h.readSession(created.ID)
	if got.Employee != "" || got.Status != session.StatusReception {
		t.Fatalf("会话 = %+v", got)
	}
	if textOf(t, got.Messages, session.KindUser) != "帮我出第 44 周的周报" {
		t.Fatalf("历史里没有用户的话：%+v", got.Messages)
	}
	if got.Title != "帮我出第 44 周的周报" {
		t.Fatalf("标题 = %q", got.Title)
	}

	// 模型按员工名称和描述判断，所以这些要出现在它收到的提示词里
	requests := h.provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("模型调用次数 = %d", len(requests))
	}
	first := requests[0].Request
	if first.Messages[0].Role != "system" || !strings.Contains(first.Messages[0].Content[0].Text, "经营周报员工") {
		t.Fatalf("模型收到的系统提示词：%+v", first.Messages[0])
	}
	if len(first.Tools) != 1 || first.Tools[0].Name != "propose_employee" {
		t.Fatalf("接待阶段开放的工具：%+v", first.Tools)
	}
}

func TestProposingAnUnknownEmployeeFallsBackToManualPick(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("propose_employee", map[string]any{"employee": "no-such-employee"}),
		modelSays("抱歉，我判断不出。"),
	), withScriptedModel())
	created := h.createSession()

	reply := h.say(created.ID, contract.RoleInitiator, "随便干点什么")

	card := cardOf(t, reply.Messages, session.CardEmployeeConfirm)
	if card.Proposed != "" || card.Source != session.SourceManual || len(card.Candidates) != 1 {
		t.Fatalf("模型提议不存在的员工时应列出员工手选：%+v", card)
	}
}

// confirm 人在卡片上确认员工。
func (h *harness) confirm(sessionID string, role contract.Role, employee string) session.Reply {
	h.t.Helper()
	status, data := h.postJSON("/api/sessions/"+sessionID+"/employee", role, map[string]any{"employee": employee})
	if status != http.StatusOK {
		h.t.Fatalf("确认员工失败：%d %s", status, data)
	}
	return decode[session.Reply](h.t, data)
}

func weekOf(t *testing.T, draft map[string]any) float64 {
	t.Helper()
	period, ok := draft["period"].(map[string]any)
	if !ok {
		t.Fatalf("草稿里没有统计周期：%+v", draft)
	}
	week, _ := period["week"].(float64)
	return week
}

func toolNames(specs []agent.ToolSpec) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	return names
}

func TestConfirmingAnEmployeeFillsTheFormDraft(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("propose_employee", map[string]any{"employee": harnessEmployee}),
		modelSays("请确认由经营周报员工接手。"),
		modelCalls("fill_form_draft", map[string]any{
			"period": map[string]any{"year": 2026, "week": 44},
			"remark": "摘要先写区域情况",
		}),
		modelSays("已经填好统计周期，请检查后提交。"),
	), withScriptedModel())
	created := h.createSession()
	h.say(created.ID, contract.RoleInitiator, "出第 44 周周报，摘要先写区域情况")

	reply := h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)

	card := cardOf(t, reply.Messages, session.CardForm)
	if card.Employee != harnessEmployee || weekOf(t, card.Draft) != 44 || card.Draft["remark"] != "摘要先写区域情况" {
		t.Fatalf("表单卡片 = %+v", card)
	}
	if got := textOf(t, reply.Messages, session.KindAssistant); got != "已经填好统计周期，请检查后提交。" {
		t.Fatalf("回复 = %q", got)
	}
	got := h.readSession(created.ID)
	if got.Status != session.StatusDrafting || got.Employee != harnessEmployee || weekOf(t, got.Draft) != 44 {
		t.Fatalf("会话 = %+v", got)
	}

	// 接手后换成员工的 frontdesk.md，并能看到此前的对话
	requests := h.provider.Requests()
	handover := requests[2].Request
	system := handover.Messages[0].Content[0].Text
	if !strings.Contains(system, "经营周报员工会话里的前台 Agent") {
		t.Fatalf("接手后的系统提示词：%s", system)
	}
	if !strings.Contains(fmt.Sprint(handover.Messages), "出第 44 周周报") {
		t.Fatalf("模型没看到此前的对话：%+v", handover.Messages)
	}
	if !strings.Contains(fmt.Sprint(toolNames(handover.Tools)), "fill_form_draft") {
		t.Fatalf("接手后开放的工具：%v", toolNames(handover.Tools))
	}
}

func TestModelUnavailableStillLetsThePersonPickAnEmployee(t *testing.T) {
	cases := map[string]agent.ModelScript{
		"模型报错": frontdeskScript(agent.ScriptedTurn{Error: stringPointer("模型接口返回 401：invalid key")}),
		"没有剧本": nil,
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, script, withScriptedModel())
			created := h.createSession()

			reply := h.say(created.ID, contract.RoleInitiator, "出周报")

			if got := textOf(t, reply.Messages, session.KindNotice); !strings.Contains(got, "模型暂时不可用") {
				t.Fatalf("说明 = %q", got)
			}
			card := cardOf(t, reply.Messages, session.CardEmployeeConfirm)
			if card.Proposed != "" || card.Source != session.SourceManual || len(card.Candidates) != 1 {
				t.Fatalf("手选卡片 = %+v", card)
			}

			picked := h.confirm(created.ID, contract.RoleInitiator, card.Candidates[0].ID)

			form := cardOf(t, picked.Messages, session.CardForm)
			if form.Employee != harnessEmployee || len(form.Draft) != 0 {
				t.Fatalf("手选后应给出空白表单：%+v", form)
			}
			if got := h.readSession(created.ID); got.Employee != harnessEmployee {
				t.Fatalf("会话 = %+v", got)
			}
		})
	}
}

func stringPointer(text string) *string { return &text }

func TestEmployeeCanBePickedBeforeSayingAnything(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())
	created := h.createSession()

	reply := h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)

	if form := cardOf(t, reply.Messages, session.CardForm); form.Employee != harnessEmployee {
		t.Fatalf("表单卡片 = %+v", form)
	}
	if calls := len(h.provider.Requests()); calls != 0 {
		t.Fatalf("没人说话时不该调模型，调了 %d 次", calls)
	}
}

func TestOnlyTheInitiatorConfirmsAnEmployeeAndOnlyOnce(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())
	created := h.createSession()
	path := "/api/sessions/" + created.ID + "/employee"

	if status, data := h.postJSON(path, contract.RoleReviewer, map[string]any{"employee": harnessEmployee}); status != http.StatusForbidden {
		t.Fatalf("审核人确认员工：%d %s", status, data)
	}
	if status, data := h.postJSON(path, contract.RoleInitiator, map[string]any{"employee": "no-such-employee"}); status != http.StatusBadRequest {
		t.Fatalf("确认不存在的员工：%d %s", status, data)
	}
	h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)
	if status, data := h.postJSON(path, contract.RoleInitiator, map[string]any{"employee": harnessEmployee}); status != http.StatusConflict {
		t.Fatalf("重复确认员工：%d %s", status, data)
	}
}

// setDefaultEmployee 通过设置接口指定默认员工。
func (h *harness) setDefaultEmployee(employee string) (int, []byte) {
	h.t.Helper()
	status, data := h.send(http.MethodGet, "/api/settings/app", "", "", nil)
	if status != http.StatusOK {
		h.t.Fatalf("读设置失败：%d %s", status, data)
	}
	revision := decode[struct {
		Revision int `json:"revision"`
	}](h.t, data).Revision
	raw, err := json.Marshal(map[string]any{"revision": revision, "defaultEmployee": employee})
	if err != nil {
		h.t.Fatal(err)
	}
	return h.send(http.MethodPatch, "/api/settings/app", contract.RoleInitiator, "application/json", bytes.NewReader(raw))
}

func TestDefaultEmployeeIsProposedWithoutAskingTheModel(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())
	if status, data := h.setDefaultEmployee(harnessEmployee); status != http.StatusOK {
		t.Fatalf("设置默认员工失败：%d %s", status, data)
	}
	created := h.createSession()

	reply := h.say(created.ID, contract.RoleInitiator, "出周报")

	card := cardOf(t, reply.Messages, session.CardEmployeeConfirm)
	if card.Proposed != harnessEmployee || card.Source != session.SourceDefault {
		t.Fatalf("员工确认卡片 = %+v", card)
	}
	if calls := len(h.provider.Requests()); calls != 0 {
		t.Fatalf("有默认员工时不该调模型，调了 %d 次", calls)
	}
	// 仍由人确认
	if got := h.readSession(created.ID); got.Employee != "" {
		t.Fatalf("默认员工也要人确认：%+v", got)
	}
}

func TestDefaultEmployeeMustExist(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())

	if status, data := h.setDefaultEmployee("no-such-employee"); status != http.StatusBadRequest {
		t.Fatalf("设置不存在的默认员工：%d %s", status, data)
	}
	if status, data := h.setDefaultEmployee(harnessEmployee); status != http.StatusOK {
		t.Fatalf("设置默认员工失败：%d %s", status, data)
	}
	_, data := h.send(http.MethodGet, "/api/settings/app", "", "", nil)
	if !strings.Contains(string(data), `"defaultEmployee":"`+harnessEmployee+`"`) {
		t.Fatalf("设置视图里没有默认员工：%s", data)
	}
	if status, data := h.setDefaultEmployee(""); status != http.StatusOK {
		t.Fatalf("清除默认员工失败：%d %s", status, data)
	}
}

func TestDeletedDefaultEmployeeFallsThroughToTheModel(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("propose_employee", map[string]any{"employee": harnessEmployee}),
		modelSays("请确认。"),
	), withScriptedModel())
	if status, data := h.setDefaultEmployee(harnessEmployee); status != http.StatusOK {
		t.Fatalf("设置默认员工失败：%d %s", status, data)
	}
	// 手改设置文件留下一个已不存在的员工
	path := filepath.Join(h.workspace, "settings.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.ReplaceAll(raw, []byte(`"defaultEmployee": "`+harnessEmployee+`"`), []byte(`"defaultEmployee": "gone"`)), 0o644); err != nil {
		t.Fatal(err)
	}
	created := h.createSession()

	reply := h.say(created.ID, contract.RoleInitiator, "出周报")

	if card := cardOf(t, reply.Messages, session.CardEmployeeConfirm); card.Source != session.SourceModel {
		t.Fatalf("默认员工已不存在时应走模型提议：%+v", card)
	}
}

// uploadTo 在会话里上传材料。
func (h *harness) uploadTo(sessionID string, files []upload) (int, []byte) {
	h.t.Helper()
	return h.uploadToWorkspace(sessionID, files, "")
}

// uploadToWorkspace 在某个工作区的会话里上传材料：应用目录模式下上传初稿要带 workspaceId。
func (h *harness) uploadToWorkspace(sessionID string, files []upload, workspaceID string) (int, []byte) {
	h.t.Helper()
	fields := map[string]string{}
	if workspaceID != "" {
		fields["workspaceId"] = workspaceID
	}
	contentType, body := multipartBody(h.t, fields, files)
	return h.send(http.MethodPost, "/api/sessions/"+sessionID+"/attachments", contract.RoleInitiator, contentType, body)
}

// trySubmit 人在表单卡片上点提交。
func (h *harness) trySubmit(sessionID string, role contract.Role, form map[string]any) (int, []byte) {
	h.t.Helper()
	body := map[string]any{}
	if form != nil {
		body["form"] = form
	}
	return h.postJSON("/api/sessions/"+sessionID+"/submit", role, body)
}

func (h *harness) submit(sessionID string, form map[string]any) session.Reply {
	h.t.Helper()
	status, data := h.trySubmit(sessionID, contract.RoleInitiator, form)
	if status != http.StatusOK {
		h.t.Fatalf("提交失败：%d %s", status, data)
	}
	return decode[session.Reply](h.t, data)
}

func (h *harness) taskCount() int {
	h.t.Helper()
	_, data := h.send(http.MethodGet, "/api/tasks", "", "", nil)
	return len(decode[[]contract.TaskSummary](h.t, data))
}

var week44Form = map[string]any{"period": map[string]any{"year": 2026, "week": 44}}

func TestSubmittingCreatesTheTaskWithTheUploadedMaterials(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())
	created := h.createSession()
	h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)
	if status, data := h.uploadTo(created.ID, []upload{sampleFile(t, "销售明细.xlsx")}); status != http.StatusOK {
		t.Fatalf("上传失败：%d %s", status, data)
	}
	if got := h.readSession(created.ID); len(got.Attachments) != 1 || got.Attachments[0].Name != "销售明细.xlsx" {
		t.Fatalf("会话里的材料：%+v", got.Attachments)
	}
	if count := h.taskCount(); count != 0 {
		t.Fatalf("提交之前不该有任务，有 %d 个", count)
	}

	reply := h.submit(created.ID, week44Form)

	taskID := reply.Session.TaskID
	if taskID == "" || reply.Session.Status != session.StatusTask {
		t.Fatalf("提交后的会话：%+v", reply.Session)
	}
	// 只有销售明细：准入校验暂停，等发起人补纪要
	snapshot := h.settled(taskID)
	expectState(t, snapshot, contract.StateAwaitingInitiator)
	if snapshot.Pause.Code != contract.PauseMinutesMissing {
		t.Fatalf("暂停原因 = %+v", snapshot.Pause)
	}
	if len(snapshot.Attachments) != 1 || snapshot.Employee != harnessEmployee {
		t.Fatalf("任务快照：%+v", snapshot)
	}
	got := h.readSession(created.ID)
	if got.TaskID != taskID || fmt.Sprint(got.WaitingFor) != fmt.Sprint([]contract.Role{contract.RoleInitiator}) || got.StatusText == "" {
		t.Fatalf("提交后读到的会话：%+v", got)
	}
}

func TestSubmitUsesTheDraftWhenTheCardSendsNoForm(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("fill_form_draft", map[string]any{"period": map[string]any{"year": 2026, "week": 44}}),
		modelSays("已填好。"),
	), withScriptedModel())
	created := h.createSession()
	h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)
	h.say(created.ID, contract.RoleInitiator, "第 44 周")
	uploadOK(h, created.ID, sampleFile(t, "销售明细.xlsx"))

	reply := h.submit(created.ID, nil)

	if week := weekOf(t, h.settled(reply.Session.TaskID).Form); week != 44 {
		t.Fatalf("任务的统计周期 = %v", week)
	}
}

func TestSubmitFormFromTheCardWinsOverTheDraft(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("fill_form_draft", map[string]any{"period": map[string]any{"year": 2026, "week": 43}}),
		modelSays("已填好。"),
	), withScriptedModel())
	created := h.createSession()
	h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)
	h.say(created.ID, contract.RoleInitiator, "第 43 周")
	uploadOK(h, created.ID, sampleFile(t, "销售明细.xlsx"))

	reply := h.submit(created.ID, week44Form)

	if week := weekOf(t, h.settled(reply.Session.TaskID).Form); week != 44 {
		t.Fatalf("人在卡片上改的内容才算数，任务的统计周期 = %v", week)
	}
}

func TestOneSessionHasAtMostOneTask(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())
	created := h.createSession()
	h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)
	uploadOK(h, created.ID, sampleFile(t, "销售明细.xlsx"))
	first := h.submit(created.ID, week44Form)
	h.settled(first.Session.TaskID)

	status, data := h.trySubmit(created.ID, contract.RoleInitiator, week44Form)

	if status != http.StatusConflict || !strings.Contains(string(data), session.ErrSessionHasTask) {
		t.Fatalf("第二次提交：%d %s", status, data)
	}
	if count := h.taskCount(); count != 1 {
		t.Fatalf("任务数 = %d", count)
	}
}

func TestSubmitIsRefusedWithoutEmployeeOrForOtherRoles(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())
	created := h.createSession()

	if status, data := h.trySubmit(created.ID, contract.RoleInitiator, week44Form); status != http.StatusConflict {
		t.Fatalf("没确认员工就提交：%d %s", status, data)
	}
	h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)
	if status, data := h.trySubmit(created.ID, contract.RoleReviewer, week44Form); status != http.StatusForbidden {
		t.Fatalf("审核人提交：%d %s", status, data)
	}
	if count := h.taskCount(); count != 0 {
		t.Fatalf("被拒绝的提交不该留下任务，有 %d 个", count)
	}
}

func TestUploadingKeepsOnlyTheFileNameAndReplacesSameName(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())
	created := h.createSession()

	for _, content := range []string{"旧", "新内容"} {
		if status, data := h.uploadTo(created.ID, []upload{{name: `..\..\evil.csv`, content: []byte(content)}}); status != http.StatusOK {
			t.Fatalf("上传失败：%d %s", status, data)
		}
	}

	got := h.readSession(created.ID)
	if len(got.Attachments) != 1 || got.Attachments[0].Name != "evil.csv" || got.Attachments[0].Size != int64(len("新内容")) {
		t.Fatalf("会话里的材料：%+v", got.Attachments)
	}
	if status, data := h.uploadTo(created.ID, nil); status != http.StatusBadRequest {
		t.Fatalf("没带文件的上传：%d %s", status, data)
	}
}

func TestOnlyTheInitiatorUploadsMaterials(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())
	created := h.createSession()
	contentType, body := multipartBody(t, nil, []upload{{name: "a.csv", content: []byte("x")}})

	status, data := h.send(http.MethodPost, "/api/sessions/"+created.ID+"/attachments", contract.RoleReviewer, contentType, body)

	if status != http.StatusForbidden {
		t.Fatalf("审核人上传：%d %s", status, data)
	}
	if got := h.readSession(created.ID); len(got.Attachments) != 0 {
		t.Fatalf("被拒绝的上传不该留下材料：%+v", got.Attachments)
	}
}

func TestUploadingAfterSubmitGoesThroughThePauseCard(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())
	created := h.createSession()
	h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)
	uploadOK(h, created.ID, sampleFile(t, "销售明细.xlsx"))
	reply := h.submit(created.ID, week44Form)
	h.settled(reply.Session.TaskID)

	status, data := h.uploadTo(created.ID, []upload{sampleFile(t, "周会纪要_2026W44.docx")})

	if status != http.StatusConflict || !strings.Contains(string(data), session.ErrSessionHasTask) {
		t.Fatalf("任务开始后上传：%d %s", status, data)
	}
}

// pausedSession 走到“缺本周纪要”暂停：开工清单只要求销售明细，纪要缺失由准入校验暂停。
func pausedSession(h *harness) (session.Detail, contract.TaskSnapshot) {
	h.t.Helper()
	created := h.createSession()
	h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)
	uploadOK(h, created.ID, sampleFile(h.t, "销售明细.xlsx"))
	reply := h.submit(created.ID, week44Form)
	snapshot := h.settled(reply.Session.TaskID)
	expectState(h.t, snapshot, contract.StateAwaitingInitiator)
	return h.readSession(created.ID), snapshot
}

func frontdeskRequests(h *harness) []agent.ScriptedRequest {
	out := []agent.ScriptedRequest{}
	for _, request := range h.provider.Requests() {
		if request.StepID == agent.FrontdeskStepID {
			out = append(out, request)
		}
	}
	return out
}

// entriesOf 取会话日志里某一类留痕。
func entriesOf(messages []session.Message, kind string) []session.Message {
	out := []session.Message{}
	for _, message := range messages {
		if message.Kind == kind {
			out = append(out, message)
		}
	}
	return out
}

func TestWhyPausedIsAnsweredFromTheRealSnapshot(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("query_task", map[string]any{}),
		modelSays("任务缺本周纪要，补上后就能继续。"),
	), withScriptedModel())
	opened, snapshot := pausedSession(h)

	reply := h.say(opened.ID, contract.RoleInitiator, "为什么暂停")

	if got := textOf(t, reply.Messages, session.KindAssistant); got != "任务缺本周纪要，补上后就能继续。" {
		t.Fatalf("回复 = %q", got)
	}
	// 模型的第二轮收到的工具结果就是快照里的暂停说明
	requests := frontdeskRequests(h)
	if len(requests) != 2 {
		t.Fatalf("模型调用次数 = %d", len(requests))
	}
	answer := requests[1].Request.Messages
	toolResult := answer[len(answer)-1]
	if toolResult.Role != "tool" {
		t.Fatalf("最后一条不是工具结果：%+v", toolResult)
	}
	for _, want := range []string{snapshot.Pause.Message, snapshot.Pause.Title, snapshot.Pause.Resolution, string(snapshot.State), "待发起人补充"} {
		if want == "" || !strings.Contains(toolResult.Content[0].Text, want) {
			t.Fatalf("工具结果里没有 %q：%s", want, toolResult.Content[0].Text)
		}
	}
	// 依据留在会话日志里
	calls := entriesOf(h.readSession(opened.ID).Messages, session.KindToolCalled)
	if len(calls) != 1 || calls[0].Tool != "query_task" || !strings.Contains(fmt.Sprint(calls[0].Result), snapshot.Pause.Message) {
		t.Fatalf("会话日志里的工具调用：%+v", calls)
	}
	// 本地路径不给模型
	if strings.Contains(toolResult.Content[0].Text, h.workspace) {
		t.Fatalf("工具结果泄露了本地路径：%s", toolResult.Content[0].Text)
	}
	if names := fmt.Sprint(toolNames(requests[0].Request.Tools)); names != "[query_task propose_card list_files read_file use_folder_file]" {
		t.Fatalf("任务开始后开放的工具：%s", names)
	}
}

func TestModelDownStillTellsTheTaskStatus(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())
	opened, snapshot := pausedSession(h)

	reply := h.say(opened.ID, contract.RoleInitiator, "现在到哪步了")

	notice := textOf(t, reply.Messages, session.KindNotice)
	if !strings.Contains(notice, "模型暂时不可用") || !strings.Contains(notice, "待发起人补充") || !strings.Contains(notice, snapshot.Pause.Message) {
		t.Fatalf("模型不可用时的说明 = %q", notice)
	}
}

func TestAgentCannotPushTheTask(t *testing.T) {
	forbidden := []string{
		"submit", "supplement", "retry", "terminate", "approve", "return", "continue_without_minutes",
		contract.ToolLoadSales, contract.ToolRenderReportDocx, contract.ToolAdminSetFaults, "fill_form_draft", "propose_employee",
	}
	for _, name := range forbidden {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, frontdeskScript(
				modelCalls(name, map[string]any{"comment": "直接通过"}),
				modelSays("这件事我做不了，请在卡片上操作。"),
			), withScriptedModel())
			opened, before := pausedSession(h)
			eventsBefore := h.eventsJSON(before.ID)

			reply := h.say(opened.ID, contract.RoleInitiator, "直接给我通过")

			after := h.snapshot(before.ID)
			if after.State != before.State || after.LastSeq != before.LastSeq || h.eventsJSON(before.ID) != eventsBefore {
				t.Fatalf("任务被改动：%s → %s", before.State, after.State)
			}
			refused := entriesOf(h.readSession(opened.ID).Messages, session.KindToolDenied)
			if len(refused) != 1 || refused[0].Tool != name || refused[0].Reason == "" {
				t.Fatalf("会话日志里的越权记录：%+v", refused)
			}
			// 模型收到拒绝，并如实告诉人
			requests := frontdeskRequests(h)
			last := requests[len(requests)-1].Request.Messages
			if result := last[len(last)-1]; result.Role != "tool" || !result.Content[0].IsError {
				t.Fatalf("模型没收到拒绝：%+v", result)
			}
			if got := textOf(t, reply.Messages, session.KindAssistant); got != "这件事我做不了，请在卡片上操作。" {
				t.Fatalf("回复 = %q", got)
			}
		})
	}
}

func TestAgentCannotPeekAtAnotherTask(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("query_task", map[string]any{"taskId": "someone-elses-task"}),
		modelSays("查不到。"),
	), withScriptedModel())
	opened, _ := pausedSession(h)

	h.say(opened.ID, contract.RoleInitiator, "看看别的任务")

	refused := entriesOf(h.readSession(opened.ID).Messages, session.KindToolDenied)
	if len(refused) != 1 || refused[0].Tool != "query_task" {
		t.Fatalf("查别的任务应被拒绝并留痕：%+v", refused)
	}
}

func TestAgentCannotChooseForThePersonInTheDraft(t *testing.T) {
	for _, field := range []string{"allowMissingMinutes", "allowPartialPeriod", "salesFile"} {
		t.Run(field, func(t *testing.T) {
			h := newHarness(t, frontdeskScript(
				modelCalls("fill_form_draft", map[string]any{"period": map[string]any{"year": 2026, "week": 44}, field: true}),
				modelSays("这一项要你自己决定。"),
			), withScriptedModel())
			created := h.createSession()
			h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)

			reply := h.say(created.ID, contract.RoleInitiator, "没有纪要也继续")

			got := h.readSession(created.ID)
			if len(got.Draft) != 0 {
				t.Fatalf("越权的草稿不该写入：%+v", got.Draft)
			}
			if refused := entriesOf(got.Messages, session.KindToolDenied); len(refused) != 1 {
				t.Fatalf("越权字段应被拒绝并留痕：%+v", got.Messages)
			}
			for _, message := range reply.Messages {
				if message.Card != nil {
					t.Fatalf("被拒绝的草稿不该发卡片：%+v", message.Card)
				}
			}
		})
	}
}

func TestInvalidDraftValueIsReturnedToTheModel(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("fill_form_draft", map[string]any{"period": map[string]any{"year": 2026, "week": 99}}),
		modelSays("第 99 周不存在，请确认是哪一周。"),
	), withScriptedModel())
	created := h.createSession()
	h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)

	h.say(created.ID, contract.RoleInitiator, "第 99 周")

	got := h.readSession(created.ID)
	if len(got.Draft) != 0 {
		t.Fatalf("不合法的值不该进草稿：%+v", got.Draft)
	}
	if refused := entriesOf(got.Messages, session.KindToolDenied); len(refused) != 0 {
		t.Fatalf("填错不算越权：%+v", refused)
	}
}

func TestToolRoundsAreCapped(t *testing.T) {
	turns := []agent.ScriptedTurn{}
	for index := 0; index <= session.MaxToolRounds+5; index++ {
		turns = append(turns, modelCalls("query_task", map[string]any{}))
	}
	h := newHarness(t, frontdeskScript(turns...), withScriptedModel())
	opened, _ := pausedSession(h)

	reply := h.say(opened.ID, contract.RoleInitiator, "一直查")

	if got := textOf(t, reply.Messages, session.KindAssistant); !strings.Contains(got, "上限") {
		t.Fatalf("超限时要说明：%q", got)
	}
	if calls := len(frontdeskRequests(h)); calls != session.MaxToolRounds+1 {
		t.Fatalf("模型调用次数 = %d", calls)
	}
	if executed := len(entriesOf(reply.Messages, session.KindToolCalled)); executed != session.MaxToolRounds {
		t.Fatalf("工具执行次数 = %d", executed)
	}
}

func TestChatDoesNotUseTheTaskBudget(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("query_task", map[string]any{}),
		modelSays("还在等材料。"),
	), withScriptedModel())
	opened, before := pausedSession(h)

	h.say(opened.ID, contract.RoleInitiator, "现在到哪步了")

	if after := h.snapshot(before.ID); after.Budget.ModelCalls.Used != before.Budget.ModelCalls.Used {
		t.Fatalf("对话占用了任务预算：%d → %d", before.Budget.ModelCalls.Used, after.Budget.ModelCalls.Used)
	}
}

func TestPauseShowsUpAsACardWithLiveActions(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())
	opened, snapshot := pausedSession(h)

	card := cardOf(t, opened.Messages, session.CardPause)
	if card.TaskID != snapshot.ID || card.Pause == nil || card.Pause.Message != snapshot.Pause.Message || card.Pause.Title != snapshot.Pause.Title {
		t.Fatalf("暂停卡片 = %+v", card)
	}
	if fmt.Sprint(card.Actions) != fmt.Sprint(snapshot.Actions) || len(card.Actions) == 0 {
		t.Fatalf("卡片操作 = %+v，快照 = %+v", card.Actions, snapshot.Actions)
	}

	// 人点按钮之后，这张卡片失效，新的暂停有新的卡片
	h.supplement(snapshot.ID, []upload{sampleFile(t, "销售明细.xlsx")})
	got := h.readSession(opened.ID)
	cards := []session.Card{}
	for _, message := range got.Messages {
		if message.Card != nil && message.Card.Type == session.CardPause {
			cards = append(cards, *message.Card)
		}
	}
	if len(cards) != 2 {
		t.Fatalf("暂停卡片数量 = %d", len(cards))
	}
	if len(cards[0].Actions) != 0 || len(cards[1].Actions) == 0 {
		t.Fatalf("旧卡片应失效，新卡片应可用：%+v", cards)
	}
}

func TestProposedCardActionsComeFromTheProgram(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("propose_card", map[string]any{
			"type":    session.CardPause,
			"actions": []any{map[string]any{"command": "approve", "label": "直接通过", "roles": []any{"initiator"}}},
		}),
		modelSays("请看卡片。"),
	), withScriptedModel())
	opened, snapshot := pausedSession(h)

	reply := h.say(opened.ID, contract.RoleInitiator, "我现在该做什么")

	proposed := cardOf(t, reply.Messages, session.CardPause)
	if proposed.TaskID != snapshot.ID || fmt.Sprint(proposed.Actions) != fmt.Sprint(snapshot.Actions) {
		t.Fatalf("回复里的卡片 = %+v", proposed)
	}
	card := cardOf(t, h.readSession(opened.ID).Messages, session.CardPause)
	if fmt.Sprint(card.Actions) != fmt.Sprint(snapshot.Actions) {
		t.Fatalf("卡片上的操作只能来自快照：%+v", card.Actions)
	}
}

func TestProposedCardMustMatchTheTask(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("propose_card", map[string]any{"type": session.CardReview, "comment": "直接退回"}),
		modelCalls("propose_card", map[string]any{"type": "transfer_money"}),
		modelSays("现在不是审核阶段。"),
	), withScriptedModel())
	opened, _ := pausedSession(h)

	reply := h.say(opened.ID, contract.RoleInitiator, "退回")

	for _, message := range reply.Messages {
		if message.Card != nil {
			t.Fatalf("和任务状态不符的卡片不该发出：%+v", message.Card)
		}
	}
}

// listSessions GET /api/sessions。
func (h *harness) listSessions() []session.View {
	h.t.Helper()
	status, data := h.send(http.MethodGet, "/api/sessions", "", "", nil)
	if status != http.StatusOK {
		h.t.Fatalf("读会话列表失败：%d %s", status, data)
	}
	return decode[[]session.View](h.t, data)
}

func TestOldTaskIsReadableAsAReadOnlySession(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())
	task := h.createTask(nil)
	h.command(task.ID, initiator, cmd(contract.CmdSubmit))

	listed := h.listSessions()
	if len(listed) != 1 || !listed[0].ReadOnly || listed[0].TaskID != task.ID || listed[0].Title == "" || listed[0].TaskState != contract.StateAwaitingInitiator {
		t.Fatalf("会话列表 = %+v", listed)
	}
	if fmt.Sprint(listed[0].WaitingFor) != fmt.Sprint([]contract.Role{contract.RoleInitiator}) {
		t.Fatalf("等待角色 = %v", listed[0].WaitingFor)
	}

	got := h.readSession(listed[0].ID)
	if got.Employee != harnessEmployee || got.Status != session.StatusTask {
		t.Fatalf("只读会话 = %+v", got)
	}
	if form := cardOf(t, got.Messages, session.CardForm); !form.Submitted || weekOf(t, form.Draft) != 44 {
		t.Fatalf("表单卡片 = %+v", form)
	}
	// 只读的是对话，暂停卡片的按钮照常可用
	if pause := cardOf(t, got.Messages, session.CardPause); len(pause.Actions) == 0 {
		t.Fatalf("暂停卡片 = %+v", pause)
	}

	path := "/api/sessions/" + got.ID
	writes := map[string]func() (int, []byte){
		"发消息": func() (int, []byte) { return h.postJSON(path+"/messages", initiator, map[string]any{"text": "你好"}) },
		"确认员工": func() (int, []byte) {
			return h.postJSON(path+"/employee", initiator, map[string]any{"employee": harnessEmployee})
		},
		"提交": func() (int, []byte) { return h.trySubmit(got.ID, initiator, week44Form) },
		"上传": func() (int, []byte) { return h.uploadTo(got.ID, []upload{{name: "a.csv", content: []byte("x")}}) },
	}
	for name, write := range writes {
		if status, data := write(); status != http.StatusConflict || !strings.Contains(string(data), session.ErrSessionReadOnly) {
			t.Fatalf("%s：%d %s", name, status, data)
		}
	}
}

func TestSessionListHasNewestFirstAndEachTaskOnce(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())
	oldTask := h.createTask(nil)
	bound, _ := pausedSession(h)
	fresh := h.createSession()

	listed := h.listSessions()

	if len(listed) != 3 {
		t.Fatalf("一个旧任务、一个带任务的会话、一个新会话，实际 %d 条：%+v", len(listed), listed)
	}
	if listed[0].ID != fresh.ID || listed[1].ID != bound.ID || listed[2].TaskID != oldTask.ID {
		t.Fatalf("顺序不对：%+v", listed)
	}
	if _, data := h.send(http.MethodGet, "/api/sessions", "", "", nil); strings.Contains(string(data), "\"messages\"") {
		t.Fatalf("列表不该带消息：%s", data)
	}
}

func TestSessionSurvivesARestart(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("fill_form_draft", map[string]any{"period": map[string]any{"year": 2026, "week": 44}}),
		modelSays("已填好。"),
	), withScriptedModel())
	created := h.createSession()
	h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)
	h.say(created.ID, contract.RoleInitiator, "第 44 周")
	if status, data := h.uploadTo(created.ID, []upload{{name: "a.csv", content: []byte("x")}}); status != http.StatusOK {
		t.Fatalf("上传失败：%d %s", status, data)
	}

	h.baseURL = h.serve()

	got := h.readSession(created.ID)
	if got.Employee != harnessEmployee || weekOf(t, got.Draft) != 44 || len(got.Attachments) != 1 {
		t.Fatalf("重启后的会话 = %+v", got)
	}
	if textOf(t, got.Messages, session.KindUser) != "第 44 周" {
		t.Fatalf("重启后的历史 = %+v", got.Messages)
	}
	reply := h.submit(created.ID, nil)
	if reply.Session.TaskID == "" {
		t.Fatalf("重启后不能提交：%+v", reply)
	}
	// 任务跑完再结束：临时目录清理时任务还在写日志会删不掉（T39 的剧本任务会真的跑起来）
	h.settled(reply.Session.TaskID)
}

func TestTornLastLogLineIsDropped(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())
	created := h.createSession()
	h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)
	path := filepath.Join(h.workspace, "sessions", created.ID, "log.jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"seq":99,"kind":"us`); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if got := h.readSession(created.ID); got.Employee != harnessEmployee {
		t.Fatalf("读半行之后 = %+v", got)
	}
	// 之后的写入不能接在半行后面
	if status, data := h.uploadTo(created.ID, []upload{{name: "a.csv", content: []byte("x")}}); status != http.StatusOK {
		t.Fatalf("上传失败：%d %s", status, data)
	}
	if got := h.readSession(created.ID); len(got.Attachments) != 1 || got.Employee != harnessEmployee {
		t.Fatalf("写入后 = %+v", got)
	}
}

func TestSecondMessageWhileReplyingIsRefused(t *testing.T) {
	delay := 300
	answer := modelSays("好的。")
	h := newHarness(t, frontdeskScript(agent.ScriptedTurn{DelayMS: &delay, Then: &answer}), withScriptedModel())
	created := h.createSession()
	h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)

	first := make(chan session.Reply, 1)
	go func() { first <- h.say(created.ID, contract.RoleInitiator, "第一句") }()
	deadline := time.Now().Add(5 * time.Second)
	for len(frontdeskRequests(h)) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	status, data := h.postJSON("/api/sessions/"+created.ID+"/messages", contract.RoleInitiator, map[string]any{"text": "第二句"})
	if status != http.StatusConflict || !strings.Contains(string(data), session.ErrSessionBusy) {
		t.Fatalf("回复期间发第二条：%d %s", status, data)
	}
	// 上传不用等回复
	if status, data := h.uploadTo(created.ID, []upload{{name: "a.csv", content: []byte("x")}}); status != http.StatusOK {
		t.Fatalf("回复期间上传：%d %s", status, data)
	}
	if got := textOf(t, (<-first).Messages, session.KindAssistant); got != "好的。" {
		t.Fatalf("回复 = %q", got)
	}
}

func TestEmptyMessageIsRefused(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())
	created := h.createSession()

	if status, data := h.postJSON("/api/sessions/"+created.ID+"/messages", contract.RoleInitiator, map[string]any{"text": "  "}); status != http.StatusBadRequest {
		t.Fatalf("空消息：%d %s", status, data)
	}
	if status, data := h.postJSON("/api/sessions/"+created.ID+"/messages", "", map[string]any{"text": "你好"}); status != http.StatusForbidden {
		t.Fatalf("没有角色：%d %s", status, data)
	}
}

// chatServer 本地假的 OpenAI 兼容服务：按顺序回 replies，记下收到的请求。
func chatServer(t *testing.T, replies ...func(w http.ResponseWriter)) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	received := &[]map[string]any{}
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		body := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["authorization"] = r.Header.Get("Authorization")
		index := len(*received)
		*received = append(*received, body)
		if index >= len(replies) {
			http.Error(w, "没有更多回复", http.StatusInternalServerError)
			return
		}
		replies[index](w)
	}))
	t.Cleanup(server.Close)
	return server, received
}

func withRealFrontdesk(t *testing.T, h *harness, baseURL, apiKey string) {
	t.Helper()
	retries := 0
	provider := agent.NewModelProvider(contract.ModelSettings{
		Connections: []contract.ModelConnection{{
			Name: "local", BaseURL: baseURL, APIKey: apiKey, Models: []string{"m"}, MaxRetries: &retries,
		}},
		Default: contract.ModelRef{Connection: "local", Model: "m"},
	})
	h.frontdesk = provider
	h.baseURL = h.serve()
}

func TestRealConnectionGetsSystemPromptAndOneToolMessagePerCall(t *testing.T) {
	calls := `{"choices":[{"finish_reason":"tool_calls","message":{"content":"","tool_calls":[
		{"id":"c1","type":"function","function":{"name":"query_task","arguments":"{}"}},
		{"id":"c2","type":"function","function":{"name":"approve","arguments":"{}"}}]}}]}`
	final := `{"choices":[{"finish_reason":"stop","message":{"content":"还在等材料。"}}]}`
	fake, received := chatServer(t,
		func(w http.ResponseWriter) { _, _ = w.Write([]byte(calls)) },
		func(w http.ResponseWriter) { _, _ = w.Write([]byte(final)) },
	)
	h := newHarness(t, nil, withScriptedModel())
	// T39：前台 Agent 用会话模型。任务在执行中不能改会话里记的模型，改为任务当前的模型
	// 在 local 连接上（引擎与前台 Agent 共用这一个真实提供者）。
	opened, _ := pausedSession(h)
	h.setTaskModel(opened.TaskID, "local", "m")
	withRealFrontdesk(t, h, fake.URL, "local-test-key")

	reply := h.say(opened.ID, contract.RoleInitiator, "到哪步了")

	if got := textOf(t, reply.Messages, session.KindAssistant); got != "还在等材料。" {
		t.Fatalf("回复 = %q", got)
	}
	if len(*received) != 2 {
		t.Fatalf("请求次数 = %d", len(*received))
	}
	messages := (*received)[1]["messages"].([]any)
	if role := messages[0].(map[string]any)["role"]; role != "system" {
		t.Fatalf("第一条消息 = %+v", messages[0])
	}
	toolIDs := []string{}
	for _, raw := range messages {
		message := raw.(map[string]any)
		if message["role"] == "tool" {
			toolIDs = append(toolIDs, message["tool_call_id"].(string))
		}
	}
	if fmt.Sprint(toolIDs) != "[c1 c2]" {
		t.Fatalf("每个工具调用都要有自己的 tool 消息：%v", toolIDs)
	}
}

func TestModelFailureNeverLeaksTheKey(t *testing.T) {
	fake, _ := chatServer(t, func(w http.ResponseWriter) {
		http.Error(w, "invalid key local-test-key", http.StatusUnauthorized)
	})
	// 会话模型也在 local 连接上：前台 Agent 用会话模型，请求走这个假服务（T39）
	h := newHarness(t, nil, withScriptedModel(),
		withModelConnection(contract.ModelConnection{Name: "local", BaseURL: fake.URL, APIKey: "local-test-key", Models: []string{"m"}}))
	withRealFrontdesk(t, h, fake.URL, "local-test-key")
	created := h.createSession()

	reply := h.say(created.ID, contract.RoleInitiator, "出周报")

	if card := cardOf(t, reply.Messages, session.CardEmployeeConfirm); card.Source != session.SourceManual {
		t.Fatalf("模型失败时应手选：%+v", card)
	}
	_, data := h.send(http.MethodGet, "/api/sessions/"+created.ID, "", "", nil)
	if strings.Contains(string(data), "local-test-key") {
		t.Fatalf("响应里有 Key：%s", data)
	}
	if failed := entriesOf(h.readSession(created.ID).Messages, session.KindModelFailed); len(failed) != 1 || !strings.Contains(failed[0].Reason, "401") {
		t.Fatalf("模型失败要留痕：%+v", failed)
	}
}

func TestNoKeyMeansManualPickWithoutCallingTheModel(t *testing.T) {
	fake, received := chatServer(t)
	h := newHarness(t, nil, withScriptedModel())
	withRealFrontdesk(t, h, fake.URL, "")
	created := h.createSession()

	reply := h.say(created.ID, contract.RoleInitiator, "出周报")

	if card := cardOf(t, reply.Messages, session.CardEmployeeConfirm); card.Source != session.SourceManual {
		t.Fatalf("没有 Key 时应手选：%+v", card)
	}
	if len(*received) != 0 {
		t.Fatalf("没有 Key 不该发请求，发了 %d 次", len(*received))
	}
}

// returnComment A-01-rework 剧本对应的退回意见。
const returnComment = "摘要请再精简，突出本周最重要的两个异常。"

func TestConversationFromOneSentenceToDelivery(t *testing.T) {
	script := caseScript(t, "A-01-rework")
	script[agent.FrontdeskStepID] = []agent.ScriptedTurn{
		modelCalls("propose_employee", map[string]any{"employee": harnessEmployee, "reason": "要出经营周报"}),
		modelSays("请确认由经营周报员工接手。"),
		modelCalls("fill_form_draft", map[string]any{"period": map[string]any{"year": 2026, "week": 44}}),
		modelSays("已填好第 44 周，请上传材料后提交。"),
		modelCalls("propose_card", map[string]any{"type": session.CardReview, "comment": returnComment, "sections": []any{"summary"}}),
		modelSays("退回意见已写好，请在卡片上确认。"),
	}
	h := newHarness(t, script, withScriptedModel())
	created := h.createSession()
	h.say(created.ID, initiator, "出第 44 周周报")
	h.confirm(created.ID, initiator, harnessEmployee)
	if status, data := h.uploadTo(created.ID, a01Files(t)); status != http.StatusOK {
		t.Fatalf("上传失败：%d %s", status, data)
	}

	taskID := h.submit(created.ID, nil).Session.TaskID
	snapshot := h.settled(taskID)
	expectState(t, snapshot, contract.StateAwaitingReview)

	// 审核人在对话里说，只得到一张草稿卡片，任务不动
	reply := h.say(created.ID, reviewer, "摘要太长了，退回吧")
	card := cardOf(t, reply.Messages, session.CardReview)
	if card.Comment != returnComment || fmt.Sprint(card.Sections) != "[summary]" || len(card.Actions) == 0 {
		t.Fatalf("审核卡片 = %+v", card)
	}
	if after := h.snapshot(taskID); after.State != contract.StateAwaitingReview || after.LastSeq != snapshot.LastSeq {
		t.Fatalf("对话不能推进任务：%s", after.State)
	}
	requests := frontdeskRequests(h)
	if system := requests[len(requests)-1].Request.Messages[0].Content[0].Text; !strings.Contains(system, "审核人") {
		t.Fatalf("模型不知道在和谁说话：%s", system)
	}

	// 人点了卡片才退回
	expectState(t, h.command(taskID, reviewer, map[string]any{"type": contract.CmdReturn, "comment": card.Comment, "sections": card.Sections}), contract.StateAwaitingReview)
	expectState(t, h.command(taskID, reviewer, cmd(contract.CmdApprove)), contract.StateDelivered)

	got := h.readSession(created.ID)
	if got.Status != session.StatusClosed || got.TaskState != contract.StateDelivered {
		t.Fatalf("交付后的会话 = %+v", got)
	}
	if text := textOf(t, got.Messages, session.KindNotice); text != "任务已交付。" {
		t.Fatalf("最后的说明 = %q", text)
	}
	if qc := cardOf(t, got.Messages, session.CardQC); qc.Passed == nil || !*qc.Passed {
		t.Fatalf("质检卡片 = %+v", qc)
	}
	if review := cardOf(t, got.Messages, session.CardReview); len(review.Actions) != 0 {
		t.Fatalf("交付后审核卡片不该有按钮：%+v", review.Actions)
	}
	if status, data := h.trySubmit(created.ID, initiator, week44Form); status != http.StatusConflict {
		t.Fatalf("交付后再提交：%d %s", status, data)
	}
}

func TestNewSessionHasEmptyHistoryAndNoTask(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())

	created := h.createSession()

	got := h.readSession(created.ID)
	if got.ID != created.ID || got.Status != session.StatusReception {
		t.Fatalf("会话 = %+v", got)
	}
	if got.Employee != "" || got.TaskID != "" || len(got.Messages) != 0 {
		t.Fatalf("新会话不该有员工、任务和消息：%+v", got)
	}
	if _, data := h.send(http.MethodGet, "/api/sessions/"+created.ID, "", "", nil); !strings.Contains(string(data), "\"messages\":[]") {
		t.Fatalf("没有消息时也要返回空数组：%s", data)
	}
}

func TestUnknownSessionIsNotFound(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())

	for _, id := range []string{"s-missing", "..%2F..%2Fsettings", "task-missing"} {
		status, data := h.send(http.MethodGet, "/api/sessions/"+id, "", "", nil)
		if status != http.StatusNotFound {
			t.Fatalf("%s：%d %s", id, status, data)
		}
	}
}
