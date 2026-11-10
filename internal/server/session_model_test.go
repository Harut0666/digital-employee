// T39 会话模型：人选的模型决定任务请求里的 model；运行中切换从下一个判断步骤生效；
// 轨迹事件带模型名；没有可用模型时提交被拦下。全部从 HTTP 接口 + 假模型服务验证。
package server_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/session"
	"digital-employee/internal/settings"
)

// ---------------------------------------------------------------- 按模型作答的假模型服务

// fakeModelTurn 假模型服务对某个模型的一轮响应。
type fakeModelTurn struct {
	ToolCalls []agent.ScriptedToolCall
	Status    int
	// Text 没有工具调用时的纯文本答复（前台 Agent 的回答）
	Text string
}

// choices 这一轮的响应体。
func (t *fakeModelTurn) choices() []map[string]any {
	calls := []map[string]any{}
	for index, call := range t.ToolCalls {
		arguments, _ := json.Marshal(call.Arguments)
		calls = append(calls, map[string]any{
			"id": fmt.Sprintf("call_%d", index), "type": "function",
			"function": map[string]any{"name": call.Name, "arguments": string(arguments)},
		})
	}
	if len(calls) == 0 && t.Text != "" {
		return []map[string]any{{"message": map[string]any{"content": t.Text}, "finish_reason": "stop"}}
	}
	return []map[string]any{{"message": map[string]any{"content": "", "tool_calls": calls}, "finish_reason": "tool_calls"}}
}

// modelCall 假模型服务收到的一次请求。
type modelCall struct {
	// connection 从请求地址的路径段取（连接的 baseUrl 是 {服务}/连接名）
	connection string
	model      string
	body       []byte
}

// toolNames 请求里给出的工具名（判断步骤只有 submit_result，前台 Agent 会带它自己的工具）。
func (c modelCall) toolNames() []string {
	var parsed struct {
		ReasoningEffort string `json:"reasoning_effort"`
		Tools           []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(c.body, &parsed)
	names := []string{}
	if parsed.ReasoningEffort != "" {
		names = append(names, "reasoning_effort="+parsed.ReasoningEffort)
	}
	for _, tool := range parsed.Tools {
		names = append(names, tool.Function.Name)
	}
	return names
}

// routingModelService 按请求里的 model 字段作答的假模型服务：每个模型有自己的回合队列。
type routingModelService struct {
	mu     sync.Mutex
	turns  map[string][]fakeModelTurn
	served map[string]int
	calls  []modelCall
	// onCall 在每次请求时调用（参数是连接名与模型名）
	onCall func(connection, model string)
	// hold 在作答之前调用（按请求排队之后才调用，可以在这里卡住某次调用）；
	// held 卡住的调用开始时关闭，released 关闭后放行
	hold     func(model string)
	held     chan struct{}
	released chan struct{}
	server   *httptest.Server
	hook     func()
}

func newRoutingModelService(t *testing.T) *routingModelService {
	t.Helper()
	service := &routingModelService{turns: map[string][]fakeModelTurn{}, served: map[string]int{}}
	service.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hook := service.hook; hook != nil {
			hook()
		}
		service.serve(w, r)
	}))
	t.Cleanup(service.server.Close)
	return service
}

// script 换一份剧本并清空请求记录。
func (s *routingModelService) script(turns map[string][]fakeModelTurn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turns = turns
	s.served = map[string]int{}
	s.calls = nil
}

// connection 假模型服务上的一个连接：baseUrl 带连接名做路径段，用来核对请求走的是哪个连接。
func (s *routingModelService) connection(name string, models ...string) contract.ModelConnection {
	// 不重试：剧本用完时测试很快看到暂停，而不是等重试
	retries := 0
	return contract.ModelConnection{Name: name, BaseURL: s.server.URL + "/" + name, APIKey: fakeModelKey, Models: models, MaxRetries: &retries}
}

func (s *routingModelService) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var parsed struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &parsed)
	// 连接的 baseUrl 是 {服务}/连接名：路径的第一段是连接名
	connection := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0]
	s.mu.Lock()
	s.calls = append(s.calls, modelCall{connection: connection, model: parsed.Model, body: body})
	hook := s.onCall
	hold := s.hold
	s.mu.Unlock()
	if hook != nil {
		hook(connection, parsed.Model)
	}
	if hold != nil {
		hold(parsed.Model)
	}
	// 卡住的请求放行之后才轮到下一轮：同一模型的几轮按到达顺序消费
	s.mu.Lock()
	turns := s.turns[parsed.Model]
	index := s.served[parsed.Model]
	s.served[parsed.Model] = index + 1
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if index >= len(turns) {
		// 500 会被重试：重试同样拿不到（重试次数取连接设置的缺省 0）
		http.Error(w, "剧本已用完："+parsed.Model, http.StatusInternalServerError)
		return
	}
	turn := turns[index]
	if turn.Status != 0 {
		http.Error(w, "模拟错误", turn.Status)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": turn.choices()})
}

func (s *routingModelService) called() []modelCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]modelCall{}, s.calls...)
}

func (s *routingModelService) modelsCalled() []string {
	out := []string{}
	for _, call := range s.called() {
		out = append(out, call.model)
	}
	return out
}

// turnsFor 把剧本里给定步骤的轮次排成一个模型的答复（顺序与工作流里判断步骤的顺序一致）。
// 剧本里的工具调用参数原样作答：真实请求会带上推理档位，续轮时在工具结果里回传推理内容；
// 假服务只回答“请求的模型是谁”，不模拟这些。
func turnsFor(script agent.ModelScript, steps ...string) []fakeModelTurn {
	out := []fakeModelTurn{}
	for _, step := range steps {
		for _, turn := range script[step] {
			out = append(out, fakeModelTurn{ToolCalls: turn.ToolCalls})
		}
	}
	return out
}

// ---------------------------------------------------------------- 测试辅助

// sessionModel 读一个会话当前的模型。
func (h *harness) sessionModel(sessionID string) *contract.ModelRef {
	h.t.Helper()
	return h.readSession(sessionID).Model
}

// trySetModel PUT /api/sessions/{id}/model。
func (h *harness) trySetModel(sessionID string, role contract.Role, body any) (int, []byte) {
	h.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.send(http.MethodPut, "/api/sessions/"+sessionID+"/model", role, "application/json", strings.NewReader(string(raw)))
}

// setModel 发起人在会话里选模型，要求成功。
func (h *harness) setModel(sessionID, connection, model string) session.Reply {
	h.t.Helper()
	status, data := h.trySetModel(sessionID, contract.RoleInitiator, map[string]any{"connection": connection, "model": model})
	if status != http.StatusOK {
		h.t.Fatalf("选择模型失败：%d %s", status, data)
	}
	return decode[session.Reply](h.t, data)
}

// modelOptions 会话可选的模型：GET /api/settings/models。
func (h *harness) modelOptions() []session.ModelConnectionOption {
	h.t.Helper()
	status, data := h.send(http.MethodGet, "/api/settings/models", "", "", nil)
	if status != http.StatusOK {
		h.t.Fatalf("读取可选模型失败：%d %s", status, data)
	}
	return decode[[]session.ModelConnectionOption](h.t, data)
}

// setTaskModel 直接换任务当前的模型（模拟“任务在别的连接上跑”）。
func (h *harness) setTaskModel(taskID, connection, model string) {
	h.t.Helper()
	if err := h.engine.SetTaskModel(taskID, contract.ModelRef{Connection: connection, Model: model}); err != nil {
		h.t.Fatalf("换任务模型失败：%v", err)
	}
}

// useConnections 换掉设置里的连接（只改设置；引擎的模型提供者不动）。
func (h *harness) useConnections(connections ...contract.ModelConnection) {
	h.t.Helper()
	if _, err := settings.Update(h.workspace, func(doc *contract.SettingsDocument) {
		doc.Connections = connections
	}); err != nil {
		h.t.Fatal(err)
	}
}

// setSessionModelOverride 直接往设置里写会话模型（模拟“设置页保存后会话模型已经不可用”）。
func (h *harness) setSessionModelOverride(ref contract.ModelRef) {
	h.t.Helper()
	if _, err := settings.Update(h.workspace, func(doc *contract.SettingsDocument) {
		doc.LastModel = &ref
	}); err != nil {
		h.t.Fatal(err)
	}
}

// lastModel 读设置文档里的 lastModel。
func (h *harness) lastModel() *contract.ModelRef {
	h.t.Helper()
	return settings.Read(h.workspace).LastModel
}

// openedSession 建会话、确认员工、上传第 44 周的材料（还没提交）。
func (h *harness) openedSession() session.View {
	h.t.Helper()
	created := h.createSession()
	h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)
	if status, data := h.uploadTo(created.ID, a01Files(h.t)); status != http.StatusOK {
		h.t.Fatalf("上传失败：%d %s", status, data)
	}
	return created
}

// eventsOf 某个类型的事件。
func eventsOf(t *testing.T, h *harness, taskID, eventType string) []contract.TaskEvent {
	t.Helper()
	out := []contract.TaskEvent{}
	for _, event := range h.taskEvents(taskID) {
		if event.Type == eventType {
			out = append(out, event)
		}
	}
	return out
}

// ---------------------------------------------------------------- 验收

// 会话里选的模型决定请求里的 model 字段（两个连接、各自列出模型）。
func TestJudgmentRequestsUseTheSessionModel(t *testing.T) {
	service := newRoutingModelService(t)
	// 只有 slow-b 备好了答复：请求若走错模型，后面的步骤就拿不到剧本
	service.script(map[string][]fakeModelTurn{
		"slow-b": turnsFor(caseScript(t, "A-01"), "match_causes", "write_report"),
	})
	h := newHarness(t, nil,
		withModelConnections(service.connection("conn-a", "fast-a"), service.connection("conn-b", "slow-b")),
	)
	created := h.openedSession()

	reply := h.setModel(created.ID, "conn-b", "slow-b")
	if reply.Session.Model == nil || *reply.Session.Model != (contract.ModelRef{Connection: "conn-b", Model: "slow-b"}) {
		t.Fatalf("选择模型后会话模型 = %+v", reply.Session.Model)
	}
	// 选择时同时更新设置的 lastModel
	if last := h.lastModel(); last == nil || *last != (contract.ModelRef{Connection: "conn-b", Model: "slow-b"}) {
		t.Fatalf("选择时应更新 lastModel：%+v", last)
	}

	submitted := h.submit(created.ID, week44Form)
	taskID := submitted.Session.TaskID
	expectState(t, h.settled(taskID), contract.StateAwaitingReview)

	calls := service.called()
	if len(calls) != 2 {
		t.Fatalf("两个判断步骤各调用一次模型：%v", service.modelsCalled())
	}
	for index, call := range calls {
		if call.model != "slow-b" || call.connection != "conn-b" {
			t.Fatalf("第 %d 个请求走的是 %s / %s，应该是会话模型的 conn-b / slow-b", index, call.connection, call.model)
		}
	}

	// 发起事件带会话模型
	events := h.taskEvents(taskID)
	if events[0].Type != contract.EventTaskCreated || events[0].Model == nil || *events[0].Model != (contract.ModelRef{Connection: "conn-b", Model: "slow-b"}) {
		t.Fatalf("发起事件应带会话模型：%+v", events[0])
	}
	// 模型调用事件记录连接和模型名
	called := eventsOf(t, h, taskID, contract.EventModelCalled)
	if len(called) != 2 {
		t.Fatalf("model_called 事件数 = %d", len(called))
	}
	for _, event := range called {
		if event.Connection != "conn-b" || event.ModelName != "slow-b" {
			t.Fatalf("model_called 应记录连接和模型名：%+v", event)
		}
	}
}

// 没有在会话里选过模型时，用设置的 lastModel（人上一次在会话里选的）。
func TestSessionFallsBackToTheLastModelChosen(t *testing.T) {
	service := newRoutingModelService(t)
	service.script(map[string][]fakeModelTurn{
		"model-b": turnsFor(caseScript(t, "A-01"), "match_causes", "write_report"),
	})
	h := newHarness(t, nil, withModelConnection(contract.ModelConnection{
		Name: "fake", BaseURL: service.server.URL, APIKey: fakeModelKey, Models: []string{"model-a", "model-b"},
	}))
	if _, err := settings.Update(h.workspace, func(doc *contract.SettingsDocument) {
		doc.LastModel = &contract.ModelRef{Connection: "fake", Model: "model-b"}
	}); err != nil {
		t.Fatal(err)
	}
	created := h.openedSession()
	if got := h.sessionModel(created.ID); got == nil || got.Model != "model-b" {
		t.Fatalf("新会话应沿用 lastModel：%+v", got)
	}

	submitted := h.submit(created.ID, week44Form)
	expectState(t, h.settled(submitted.Session.TaskID), contract.StateAwaitingReview)
	for _, model := range service.modelsCalled() {
		if model != "model-b" {
			t.Fatalf("请求用的模型 = %q，应该是 lastModel 的 model-b", model)
		}
	}
}

// 运行中换模型：第一个判断步骤的调用做完，第二个判断步骤用新模型；轨迹里记一条“模型已切换”。
func TestSwitchingModelMidRunAppliesFromTheNextJudgmentStep(t *testing.T) {
	service := newRoutingModelService(t)
	writeReport := caseScript(t, "A-01")
	service.script(map[string][]fakeModelTurn{
		"model-a": turnsFor(writeReport, "match_causes"),
		"model-b": turnsFor(writeReport, "write_report"),
	})
	// 第一个判断步骤的调用卡住；人在这时切换，切换完成之后再放行这次调用（正在进行的调用不中断）
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
	}))
	created := h.openedSession()
	h.setModel(created.ID, "fake", "model-a")

	submitted := h.submit(created.ID, week44Form)
	taskID := submitted.Session.TaskID
	select {
	case <-service.held:
	case <-time.After(stopTimeout):
		t.Fatal("第一个判断步骤的模型调用没有到达")
	}
	reply := h.setModel(created.ID, "fake", "model-b")
	if reply.Session.Model == nil || reply.Session.Model.Model != "model-b" {
		t.Fatalf("切换后会话模型 = %+v", reply.Session.Model)
	}
	if got := h.sessionModel(created.ID); got == nil || got.Model != "model-b" {
		t.Fatalf("切换应写进会话日志：%+v", got)
	}
	switches := eventsOf(t, h, taskID, contract.EventModelSwitched)
	if len(switches) != 1 {
		t.Fatalf("任务运行中切换应立即写一条“模型已切换”任务事件（不等正在进行的调用结束）：%+v", switches)
	}
	close(service.released)

	expectState(t, h.settled(taskID), contract.StateAwaitingReview)
	if got := strings.Join(service.modelsCalled(), ","); got != "model-a,model-b" {
		t.Fatalf("请求用的模型 = %v，want [model-a model-b]（第一个步骤用切换前的，第二个用切换后的）", service.modelsCalled())
	}
	if switches[0].FromModel == nil || switches[0].FromModel.Model != "model-a" ||
		switches[0].Model == nil || switches[0].Model.Model != "model-b" {
		t.Fatalf("切换事件应记下前后两个模型：%+v", switches[0])
	}
	called := eventsOf(t, h, taskID, contract.EventModelCalled)
	if len(called) != 2 || called[0].ModelName != "model-a" || called[1].ModelName != "model-b" {
		t.Fatalf("模型调用事件应各自记下当时用的模型：%+v", called)
	}
	// 正在进行的调用不中断：切换发生在第一条 model_called 之前，它照常记下 model-a
	if switches[0].Seq > called[0].Seq {
		t.Fatalf("切换事件应早于第一轮模型调用：switch=%d calls=%d,%d", switches[0].Seq, called[0].Seq, called[1].Seq)
	}
}

// 流程配置给某一步写死了模型：那一步用写死的，其余用任务当前模型。
func TestStepPinnedModelBeatsTheSessionModel(t *testing.T) {
	service := newRoutingModelService(t)
	writeReport := caseScript(t, "A-01")
	script := map[string][]fakeModelTurn{
		"model-a": turnsFor(writeReport, "match_causes"),
		"pinned":  turnsFor(writeReport, "write_report"),
	}
	service.script(script)
	h := newHarness(t, nil,
		withModelConnections(service.connection("fake", "model-a"), service.connection("pin", "pinned")),
		// 给 write_report 写死模型
		withWorkflowEdit("  - id: write_report\n    label: 撰写周报叙述\n", "  - id: write_report\n    label: 撰写周报叙述\n    model:\n      connection: pin\n      model: pinned\n"),
	)
	created := h.openedSession()
	h.setModel(created.ID, "fake", "model-a")

	submitted := h.submit(created.ID, week44Form)
	h.settled(submitted.Session.TaskID)
	calls := service.called()
	if len(calls) != 2 || calls[0].connection != "fake" || calls[0].model != "model-a" || calls[1].connection != "pin" || calls[1].model != "pinned" {
		summary := []string{}
		for _, call := range calls {
			summary = append(summary, call.connection+"/"+call.model)
		}
		t.Fatalf("请求应依次走 fake/model-a、pin/pinned，实际 %v", summary)
	}
	called := eventsOf(t, h, submitted.Session.TaskID, contract.EventModelCalled)
	if len(called) != 2 || called[1].Connection != "pin" || called[1].ModelName != "pinned" {
		t.Fatalf("写死模型的步骤应记录 pin/pinned：%+v", called)
	}
}

// 提交前检查按会话模型：连接没有 Key、模型不在列表里、连接已被删除、模型被去掉，都拦下并给设置入口。
func TestPreflightChecksTheSessionModel(t *testing.T) {
	service := newRoutingModelService(t)
	keyed := service.connection("keyed", "m1", "m2")
	keyless := contract.ModelConnection{Name: "keyless", BaseURL: service.server.URL + "/keyless", Models: []string{"n1"}}
	gone := service.connection("gone", "g1")
	shrunk := service.connection("shrunk", "s1", "s2")
	dropConnection := func(h *harness, name string) {
		t.Helper()
		if _, err := settings.Update(h.workspace, func(doc *contract.SettingsDocument) {
			kept := []contract.ModelConnection{}
			for _, connection := range doc.Connections {
				if connection.Name != name {
					kept = append(kept, connection)
				}
			}
			doc.Connections = kept
		}); err != nil {
			t.Fatal(err)
		}
	}
	// keyedOnly 只留 keyed：没有别的可用连接时，选过的模型不可用就直接拦住
	keyedOnly := func(h *harness) {
		t.Helper()
		dropConnection(h, "keyless")
		dropConnection(h, "gone")
		dropConnection(h, "shrunk")
		// 缺省模型也落在选过的那条连接上：没有别的可用连接时，选过的模型不可用就拦住
		h.setSessionModelOverride(contract.ModelRef{Connection: "keyed", Model: "m1"})
	}
	useConnections := func(h *harness, connections ...contract.ModelConnection) {
		if _, err := settings.Update(h.workspace, func(doc *contract.SettingsDocument) {
			doc.Connections = connections
		}); err != nil {
			t.Fatal(err)
		}
	}
	setKey := func(h *harness, name, key string) {
		t.Helper()
		if _, err := settings.Update(h.workspace, func(doc *contract.SettingsDocument) {
			for index := range doc.Connections {
				if doc.Connections[index].Name == name {
					doc.Connections[index].APIKey = key
				}
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	setModels := func(h *harness, name string, models ...string) {
		if _, err := settings.Update(h.workspace, func(doc *contract.SettingsDocument) {
			for index := range doc.Connections {
				if doc.Connections[index].Name == name {
					doc.Connections[index].Models = models
				}
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name       string
		connection string
		model      string
		message    string
		// atSelection 开始时设置里的连接（默认四个都在）
		atSelection []contract.ModelConnection
		// before 在人选模型之前改设置
		before func(h *harness)
		// after 选中之后再改设置
		after func(h *harness)
	}{
		{name: "连接没有 Key", connection: "keyless", model: "n1", message: "Key",
			atSelection: []contract.ModelConnection{keyed, service.connection("keyless", "n1"), gone, shrunk},
			after:       func(h *harness) { setKey(h, "keyless", "") }},
		// 选过的模型后来被从列表里去掉
		{name: "模型不在连接的模型列表里", connection: "keyed", model: "m-old", message: "模型列表",
			before: func(h *harness) { setModels(h, "keyed", "m1", "m2", "m-old") },
			after:  func(h *harness) { setModels(h, "keyed", "m1", "m2") }},
		// 只有 keyed 一条连接、缺省模型（新会话沿用它）也不是选过的那个：换成 m1 就能继续
		{name: "模型不在连接的模型列表里，还有别的可用模型", connection: "keyed", model: "m-old",
			before: func(h *harness) {
				setModels(h, "keyed", "m1", "m2", "m-old")
				dropConnection(h, "keyless")
				dropConnection(h, "gone")
				dropConnection(h, "shrunk")
			},
			after: func(h *harness) { setModels(h, "keyed", "m1", "m2") }},
		{name: "模型被从列表里去掉", connection: "shrunk", model: "s1", message: "shrunk",
			after: func(h *harness) { setModels(h, "shrunk", "s2"); keyedOnly(h) }},
		{name: "连接已被删除", connection: "gone", model: "g1", message: "gone",
			after: func(h *harness) { dropConnection(h, "gone"); keyedOnly(h) }},
		{name: "会话模型可用", connection: "keyed", model: "m2"},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			// 每个用例一套干净的连接：设置全局共用，用例之间互不影响
			h := newHarness(t, nil, withModelConnections(keyed, keyless, gone, shrunk))
			if len(testCase.atSelection) > 0 {
				useConnections(h, testCase.atSelection...)
			}
			created := h.openedSession()
			if testCase.before != nil {
				testCase.before(h)
			}
			h.setModel(created.ID, testCase.connection, testCase.model)
			if testCase.message != "" {
				// 缺省模型（新会话沿用它）落在 keyed/m1 上：排除“别的模型还能用”的干扰，只看选过的模型
				h.setSessionModelOverride(contract.ModelRef{Connection: "keyed", Model: "m1"})
			}
			if testCase.after != nil {
				testCase.after(h)
			}
			if testCase.message == "" {
				if reply := h.preflight(); !reply.OK {
					t.Fatalf("会话模型可用或还有别的可用模型时应通过：%+v", reply)
				}
				return
			}
			// 提交前检查只按会话模型：界面先用它提示，提交时再拦一次
			status, data := h.send(http.MethodGet, "/api/preflight", "", "", nil)
			if status != http.StatusOK {
				t.Fatalf("提交前检查接口出错：%d %s", status, data)
			}
			status, data = h.trySubmit(created.ID, contract.RoleInitiator, week44Form)
			if status != http.StatusPreconditionFailed {
				t.Fatalf("应被提交前检查拦下（412）：%d %s", status, data)
			}
			reply := decode[preflightReply](t, data)
			if reply.Error != contract.ErrModelNotConfigured || !strings.Contains(reply.Message, testCase.message) {
				t.Fatalf("说明应提到 %q：%+v", testCase.message, reply)
			}
			if reply.Settings.Section != "models" || reply.Settings.Label == "" {
				t.Fatalf("应带设置入口：%+v", reply.Settings)
			}
			if count := h.taskCount(); count != 0 {
				t.Fatalf("被拦下的提交不应创建任务：%d", count)
			}
		})
	}
}

// 会话里只能选连接已列出的模型，而且连接要有 Key；只有发起人能改。
func TestSetSessionModelValidatesAgainstTheConnection(t *testing.T) {
	service := newRoutingModelService(t)
	h := newHarness(t, nil, withModelConnections(
		service.connection("keyed", "m1", "m2"),
		service.connection("keyless", "n1"),
	))
	// 模拟模式之外，连接没有 Key 也就不该选
	if _, err := settings.Update(h.workspace, func(doc *contract.SettingsDocument) {
		for index := range doc.Connections {
			if doc.Connections[index].Name == "keyless" {
				doc.Connections[index].APIKey = ""
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	created := h.createSession()
	initial := h.sessionModel(created.ID)

	for _, testCase := range []struct {
		name string
		body map[string]any
	}{
		{"连接不存在", map[string]any{"connection": "nope", "model": "m1"}},
		{"模型不在连接的模型列表里", map[string]any{"connection": "keyed", "model": "qwen"}},
		{"连接没有 Key", map[string]any{"connection": "keyless", "model": "n1"}},
		{"缺 model", map[string]any{"connection": "keyed"}},
	} {
		status, data := h.trySetModel(created.ID, contract.RoleInitiator, testCase.body)
		if status < 400 || status >= 500 {
			t.Fatalf("%s 应被拒绝：%d %s", testCase.name, status, data)
		}
		if got := h.sessionModel(created.ID); fmt.Sprint(got) != fmt.Sprint(initial) {
			t.Fatalf("%s 被拒绝后会话模型不应变：%v → %v", testCase.name, initial, got)
		}
	}
	for _, role := range []contract.Role{contract.RoleReviewer, contract.RoleOperator} {
		if status, _ := h.trySetModel(created.ID, role, map[string]any{"connection": "keyed", "model": "m1"}); status != http.StatusForbidden {
			t.Fatalf("%s 不能选模型：%d", role, status)
		}
	}
}

// 选模型后会话视图给出当前模型；说明由前端按前后两次的模型插进对话。
func TestChoosingAModelUpdatesTheSessionView(t *testing.T) {
	service := newRoutingModelService(t)
	h := newHarness(t, nil, withModelConnections(service.connection("conn-a", "m1", "m2")))
	created := h.createSession()
	before := h.readSession(created.ID)
	if before.Model == nil || before.Model.Model != "m1" || before.Selected {
		t.Fatalf("没选过时用缺省模型，并标明没明确选过：%+v selected=%v", before.Model, before.Selected)
	}
	h.setModel(created.ID, "conn-a", "m2")

	detail := h.readSession(created.ID)
	if detail.Model == nil || *detail.Model != (contract.ModelRef{Connection: "conn-a", Model: "m2"}) || !detail.Selected {
		t.Fatalf("会话模型 = %+v selected=%v", detail.Model, detail.Selected)
	}

	// 会话可选的模型：只列有 Key 的连接，以及它列出的模型
	options := h.modelOptions()
	if len(options) != 1 || options[0].Name != "conn-a" || strings.Join(options[0].Models, ",") != "m1,m2" {
		t.Fatalf("会话可选的模型 = %+v", options)
	}

	count := len(detail.Messages)
	h.setModel(created.ID, "conn-a", "m2")
	if after := len(h.readSession(created.ID).Messages); after != count {
		t.Fatalf("重复选同一个模型不应再写日志：%d → %d", count, after)
	}
}

// 旧设置文档里的 defaultConnection / defaultModel 换算成 lastModel：任务请求用换算后的模型。
func TestLegacyDefaultModelBecomesTheTaskModel(t *testing.T) {
	service := newRoutingModelService(t)
	service.script(map[string][]fakeModelTurn{
		"model-v2": turnsFor(caseScript(t, "A-01"), "match_causes", "write_report"),
	})
	// 直接建任务、从任务上提交（旧任务路径，不经过会话）。
	// 设置里不放 lastModel：换算只能来自旧文档的 defaultConnection / defaultModel。
	h := newHarness(t, nil)
	legacy := fmt.Sprintf(`{"version":2,"revision":1,"connections":[{"name":"fake","baseUrl":%q,"apiKey":%q,"models":["model-v1","model-v2"]}],"defaultConnection":"fake","defaultModel":"model-v2"}`,
		service.server.URL, fakeModelKey)
	if err := os.WriteFile(settings.Path(h.workspace), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	task := h.createTask(a01Files(t))
	h.command(task.ID, initiator, cmd(contract.CmdSubmit))
	for _, model := range service.modelsCalled() {
		if model != "model-v2" {
			t.Fatalf("旧文档换算后任务应用 model-v2，实际 %v", service.modelsCalled())
		}
	}
}
