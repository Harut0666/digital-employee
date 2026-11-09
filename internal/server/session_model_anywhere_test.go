// T45 新对话页选模型、任何状态换模型、交付后仍能对话。
//
// 主接缝：真实引擎、真实工具、剧本模型；全部从 HTTP 接口观察结果：
//   - 建会话时带 model，会话从第一句话起就用它；
//   - 任务已交付、已终止后仍能换模型，只改会话的选择；
//   - 任务运行中换模型仍写“模型已切换”，并从下一个判断步骤生效；
//   - 旧任务推导出的只读会话仍拒绝；
//   - 没有可用模型时仍能建会话；
//   - 任务交付后发消息，得到前台 Agent 的正常回复。
package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"digital-employee/internal/contract"
	"digital-employee/internal/session"
	"digital-employee/internal/settings"
)

// createSessionWithModel 发起人开新会话，请求体带 workspaceId 和可选的 model。
func (h *harness) createSessionWithModel(workspaceID string, model map[string]any) (int, []byte) {
	h.t.Helper()
	body := map[string]any{}
	if workspaceID != "" {
		body["workspaceId"] = workspaceID
	}
	if model != nil {
		body["model"] = model
	}
	return h.postJSON("/api/sessions", contract.RoleInitiator, body)
}

// requireSessionModel 会话当前模型应当是 connection / model（读单个会话，不是列表摘要）。
func requireSessionModel(t *testing.T, h *harness, sessionID, connection, model string) {
	t.Helper()
	got := h.readSession(sessionID).Model
	if got == nil || got.Connection != connection || got.Model != model {
		t.Fatalf("会话模型 = %+v，want %s / %s", got, connection, model)
	}
}

// 建会话时带 model：会话从第一句话起用它，任务请求里的 model 也是它。
func TestCreateSessionWithAModelUsesItFromTheFirstMessage(t *testing.T) {
	service := newRoutingModelService(t)
	// 只有 slow-b 备好了答复：请求若走错模型，后面的步骤就拿不到剧本
	service.script(map[string][]fakeModelTurn{
		"slow-b": turnsFor(caseScript(t, "A-01"), "match_causes", "write_report"),
	})
	h := newHarness(t, nil, withModelConnections(service.connection("conn-a", "fast-a"), service.connection("conn-b", "slow-b")))

	status, data := h.createSessionWithModel("", map[string]any{"connection": "conn-b", "model": "slow-b"})
	if status != http.StatusCreated {
		t.Fatalf("建会话失败：%d %s", status, data)
	}
	created := decode[session.View](t, data)
	// 建会话的响应是列表摘要的形状：模型从单读接口看
	requireSessionModel(t, h, created.ID, "conn-b", "slow-b")
	// 和会话里选模型一样，记进设置的 lastModel，新会话沿用它
	if last := h.lastModel(); last == nil || *last != (contract.ModelRef{Connection: "conn-b", Model: "slow-b"}) {
		t.Fatalf("建会话时选模型应更新 lastModel：%+v", last)
	}

	h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)
	if status, data := h.uploadTo(created.ID, a01Files(t)); status != http.StatusOK {
		t.Fatalf("上传失败：%d %s", status, data)
	}
	submitted := h.submit(created.ID, week44Form)
	taskID := submitted.Session.TaskID
	expectState(t, h.settled(taskID), contract.StateAwaitingReview)

	calls := service.called()
	if len(calls) != 2 {
		t.Fatalf("两个判断步骤各调用一次模型：%v", service.modelsCalled())
	}
	for _, call := range calls {
		if call.connection != "conn-b" || call.model != "slow-b" {
			t.Fatalf("请求走的是 %s / %s，应该是建会话时选的 conn-b / slow-b", call.connection, call.model)
		}
	}
}

// 建会话时没带 model：沿用 lastModel；lastModel 也没有时取第一个可用模型。
// 没有可用模型时仍能建会话（提交前检查另行拦截）。
func TestCreateSessionFallsBackToLastModelOrTheFirstAvailable(t *testing.T) {
	service := newRoutingModelService(t)
	// lastModel 落在 conn-a / m1（装置默认取第一条连接的第一个模型）
	h := newHarness(t, nil, withModelConnections(service.connection("conn-a", "m1", "m2"), service.connection("conn-b", "m3")))
	// noKeys 改连接有没有 Key、清不清 lastModel（先改要读的连接再改 lastModel）
	noKeys := func(clearLast bool) {
		t.Helper()
		if _, err := settings.Update(h.workspace, func(doc *contract.SettingsDocument) {
			for index := range doc.Connections {
				doc.Connections[index].APIKey = ""
			}
			if clearLast {
				doc.LastModel = nil
			}
		}); err != nil {
			t.Fatal(err)
		}
	}

	status, data := h.createSessionWithModel("", nil)
	if status != http.StatusCreated {
		t.Fatalf("建会话失败：%d %s", status, data)
	}
	first := decode[session.View](t, data)
	requireSessionModel(t, h, first.ID, "conn-a", "m1")

	// lastModel 指向没有 Key 的 conn-a：取第一个可用模型 conn-b，没人明确选过就不写 lastModel
	if _, err := settings.Update(h.workspace, func(doc *contract.SettingsDocument) { doc.Connections[0].APIKey = "" }); err != nil {
		t.Fatal(err)
	}
	status, data = h.createSessionWithModel("", nil)
	if status != http.StatusCreated {
		t.Fatalf("建会话失败：%d %s", status, data)
	}
	requireSessionModel(t, h, decode[session.View](t, data).ID, "conn-b", "m3")
	if last := h.lastModel(); last == nil || last.Model != "m1" {
		t.Fatalf("没人明确选过时不应改动 lastModel：%+v", last)
	}

	// 没有任何可用模型：照样建会话，模型为空
	noKeys(true)
	status, data = h.createSessionWithModel("", nil)
	if status != http.StatusCreated {
		t.Fatalf("没有可用模型时建会话应照样成功：%d %s", status, data)
	}
	if got := h.readSession(decode[session.View](t, data).ID).Model; got != nil {
		t.Fatalf("没有可用模型时会话模型应为空：%+v", got)
	}
}

// 建会话时带的 model 按会话里选模型的规则校验。
func TestCreateSessionValidatesTheModel(t *testing.T) {
	service := newRoutingModelService(t)
	h := newHarness(t, nil, withModelConnections(service.connection("keyed", "m1", "m2")))
	for _, testCase := range []struct {
		name string
		body any
	}{
		{"连接不存在", map[string]any{"connection": "nope", "model": "m1"}},
		{"模型不在连接的模型列表里", map[string]any{"connection": "keyed", "model": "qwen"}},
		{"缺 model", map[string]any{"connection": "keyed"}},
		{"不是对象", "keyed/m1"},
	} {
		raw, err := json.Marshal(map[string]any{"model": testCase.body})
		if err != nil {
			t.Fatal(err)
		}
		status, data := h.send(http.MethodPost, "/api/sessions", contract.RoleInitiator, "application/json", bytes.NewReader(raw))
		if status < 400 || status >= 500 {
			t.Fatalf("%s 应被拒绝：%d %s", testCase.name, status, data)
		}
	}
	if count := len(h.readWorkspaceSessions()); count != 0 {
		t.Fatalf("被拒绝的建会话不应建出会话：%d", count)
	}
}

// readWorkspaceSessions 列出全部会话（不按工作区过滤）。
func (h *harness) readWorkspaceSessions() []session.View {
	h.t.Helper()
	status, data := h.send(http.MethodGet, "/api/sessions", "", "", nil)
	if status != http.StatusOK {
		h.t.Fatalf("列出会话失败：%d %s", status, data)
	}
	return decode[[]session.View](h.t, data)
}

// 交付（或终止）之后仍能换模型：只改会话的选择，不写任务事件；下一个任务用它。
func TestSwitchingModelAfterTheTaskEndedOnlyChangesTheSession(t *testing.T) {
	for _, end := range []struct {
		name string
		run  func(h *harness, taskID string) contract.TaskSnapshot
	}{
		{"交付后", func(h *harness, taskID string) contract.TaskSnapshot {
			return h.command(taskID, reviewer, cmd(contract.CmdApprove))
		}},
		{"终止后", func(h *harness, taskID string) contract.TaskSnapshot {
			return h.command(taskID, reviewer, cmd(contract.CmdTerminate))
		}},
	} {
		end := end
		t.Run(end.name, func(t *testing.T) {
			service := newRoutingModelService(t)
			service.script(map[string][]fakeModelTurn{
				"model-a": turnsFor(caseScript(t, "A-01"), "match_causes", "write_report"),
			})
			h := newHarness(t, nil, withModelConnection(contract.ModelConnection{
				Name: "fake", BaseURL: service.server.URL, APIKey: fakeModelKey, Models: []string{"model-a", "model-b"},
			}))
			created := h.openedSession()
			h.setModel(created.ID, "fake", "model-a")
			submitted := h.submit(created.ID, week44Form)
			taskID := submitted.Session.TaskID
			expectState(t, h.settled(taskID), contract.StateAwaitingReview)

			finished := end.run(h, taskID)
			switchesBefore := len(eventsOf(t, h, taskID, contract.EventModelSwitched))

			h.setModel(created.ID, "fake", "model-b")
			if got := taskStateNow(h, taskID); got != finished.State {
				t.Fatalf("换模型不应改动已结束任务的状态：%s → %s", finished.State, got)
			}
			if switchesAfter := len(eventsOf(t, h, taskID, contract.EventModelSwitched)); switchesAfter != switchesBefore {
				t.Fatalf("任务已结束时换模型不应写任务事件：%d → %d", switchesBefore, switchesAfter)
			}
			// 只改会话的选择：会话记录里留一条“选择模型”，下一次对话、下一个任务用它
			detail := h.readSession(created.ID)
			if detail.Model == nil || *detail.Model != (contract.ModelRef{Connection: "fake", Model: "model-b"}) || !detail.Selected {
				t.Fatalf("会话的选择 = %+v selected=%v", detail.Model, detail.Selected)
			}
			if last := h.lastModel(); last == nil || last.Model != "model-b" {
				t.Fatalf("换模型应更新 lastModel：%+v", last)
			}
		})
	}
}

// 旧任务推导出的只读会话选模型仍被拒绝。
func TestSelectingAModelOnALegacySessionIsStillRejected(t *testing.T) {
	// 直接建任务、跑完：它没有会话日志，以旧任务的只读会话出现
	h := newHarness(t, caseScript(t, "A-01"), withScriptedModel())
	task := h.createTask(a01Files(t))
	if got := h.command(task.ID, initiator, cmd(contract.CmdSubmit)); got.State != contract.StateAwaitingReview {
		t.Fatalf("任务应走到待审核：%+v", got)
	}
	delivered := h.command(task.ID, reviewer, cmd(contract.CmdApprove))
	expectState(t, delivered, contract.StateDelivered)

	legacyID := ""
	for _, view := range h.readWorkspaceSessions() {
		if view.TaskID == task.ID {
			legacyID = view.ID
			if !view.ReadOnly {
				t.Fatalf("旧任务推导出的会话应是只读的：%+v", view)
			}
		}
	}
	if legacyID == "" {
		t.Fatal("找不到旧任务推导出的会话")
	}
	status, data := h.trySetModel(legacyID, contract.RoleInitiator, map[string]any{"connection": scriptedConnection.Name, "model": scriptedConnection.Models[0]})
	if status != http.StatusConflict {
		t.Fatalf("只读会话选模型应被拒绝（409）：%d %s", status, data)
	}
}

// 任务运行中选模型：仍写“模型已切换”，从下一个判断步骤生效（T39 的规则不变）。
func TestRunningTaskKeepsWritingTheSwitchEvent(t *testing.T) {
	service := newRoutingModelService(t)
	writeReport := caseScript(t, "A-01")
	service.script(map[string][]fakeModelTurn{
		"model-a": turnsFor(writeReport, "match_causes"),
		"model-b": turnsFor(writeReport, "write_report"),
	})
	service.held = make(chan struct{})
	service.released = make(chan struct{})
	var once sync.Once
	service.hold = func(model string) {
		if model != "model-a" {
			return
		}
		once.Do(func() { close(service.held) })
		<-service.released
	}
	h := newHarness(t, nil, withModelConnection(contract.ModelConnection{
		Name: "fake", BaseURL: service.server.URL, APIKey: fakeModelKey, Models: []string{"model-a", "model-b"},
	}), withScriptedModel())
	// 用剧本模型建会话、换模型都能走；任务是真实引擎（判断步骤走假服务）
	created := h.openedSession()
	h.setModel(created.ID, "fake", "model-a")
	submitted := h.submit(created.ID, week44Form)
	taskID := submitted.Session.TaskID
	select {
	case <-service.held:
	case <-time.After(stopTimeout):
		t.Fatal("第一个判断步骤的模型调用没有到达")
	}
	h.setModel(created.ID, "fake", "model-b")
	if switches := eventsOf(t, h, taskID, contract.EventModelSwitched); len(switches) != 1 {
		t.Fatalf("任务运行中换模型应写一条“模型已切换”：%+v", switches)
	}
	close(service.released)
	expectState(t, h.settled(taskID), contract.StateAwaitingReview)
	if got := strings.Join(service.modelsCalled(), ","); got != "model-a,model-b" {
		t.Fatalf("请求用的模型 = %v，want [model-a model-b]", service.modelsCalled())
	}
}

// 任务交付后发一条消息：前台 Agent 正常回复（不以任务状态拒绝）。
func TestSessionStillAnswersAfterTheTaskIsDelivered(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"), withScriptedModel())
	created := h.openedSession()
	submitted := h.submit(created.ID, week44Form)
	taskID := submitted.Session.TaskID
	expectState(t, h.settled(taskID), contract.StateAwaitingReview)
	h.command(taskID, reviewer, cmd(contract.CmdApprove))
	expectState(t, h.snapshot(taskID), contract.StateDelivered)

	// 交付之后的前台 Agent 回答
	answer := "第 44 周周报已经交付，华南区实收环比下降 12%，原因见周报。"
	h.setFrontdeskScript(frontdeskScript(modelSays(answer)))
	reply := h.say(created.ID, contract.RoleInitiator, "交付的周报里华南为什么下降？")
	found := false
	for _, message := range reply.Messages {
		if message.Kind == "assistant" && message.Text == answer {
			found = true
		}
	}
	if !found {
		t.Fatalf("交付后应得到前台 Agent 的正常回复：%+v", reply.Messages)
	}
	if got := taskStateNow(h, taskID); got != contract.StateDelivered {
		t.Fatalf("对话不应改动已交付的任务：%s", got)
	}
}

// taskStateNow 当前任务的状态。
func taskStateNow(h *harness, taskID string) contract.TaskState {
	h.t.Helper()
	return h.snapshot(taskID).State
}

// 任务交付后换了模型，下一次对话就用新选的模型回答（前台 Agent 不再沿用已结束任务记下的模型）。
func TestFrontdeskAnswersAfterDeliveryWithTheSessionsNewModel(t *testing.T) {
	service := newRoutingModelService(t)
	judgement := caseScript(t, "A-01")
	service.script(map[string][]fakeModelTurn{
		"model-a": turnsFor(judgement, "match_causes", "write_report"),
		"model-b": {{Text: "第 44 周周报已经交付，华南区实收环比下降。"}},
	})
	h := newHarness(t, nil, withModelConnection(contract.ModelConnection{
		Name: "fake", BaseURL: service.server.URL, APIKey: fakeModelKey, Models: []string{"model-a", "model-b"},
	}))
	created := h.openedSession()
	h.setModel(created.ID, "fake", "model-a")
	submitted := h.submit(created.ID, week44Form)
	taskID := submitted.Session.TaskID
	expectState(t, h.settled(taskID), contract.StateAwaitingReview)
	h.command(taskID, reviewer, cmd(contract.CmdApprove))
	expectState(t, h.snapshot(taskID), contract.StateDelivered)

	h.setModel(created.ID, "fake", "model-b")
	reply := h.say(created.ID, contract.RoleInitiator, "交付的周报里华南为什么下降？")
	found := false
	for _, message := range reply.Messages {
		if message.Kind == "assistant" && strings.Contains(message.Text, "已经交付") {
			found = true
		}
	}
	if !found {
		t.Fatalf("交付后应得到前台 Agent 的正常回复：%+v", reply.Messages)
	}
	calls := service.called()
	if last := calls[len(calls)-1]; last.model != "model-b" {
		t.Fatalf("交付后对话用的模型 = %s，应是会话新选的 model-b（%v）", last.model, service.modelsCalled())
	}
}
