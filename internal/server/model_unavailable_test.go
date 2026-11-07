// 模型不可用：真实模型提供者接本地假模型服务，从 HTTP 接口验证（Spec 0005“模型不可用”）。
package server_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

const fakeModelKey = "sk-fake-model-key-1234"

// fakeModelService OpenAI 兼容的假模型服务：按顺序把剧本的回合答给调用方，
// 在 failFrom 这一回合起一直返回 failStatus，直到调用 recover。
type fakeModelService struct {
	server *httptest.Server

	mu         sync.Mutex
	turns      []agent.ScriptedTurn
	answered   int
	requests   int
	bodies     [][]byte
	failFrom   int
	failStatus int
	failBody   string
}

// newFakeModelService 回合按工作流里判断步骤的顺序排列。
func newFakeModelService(t *testing.T, script agent.ModelScript, failFrom, failStatus int) *fakeModelService {
	t.Helper()
	service := &fakeModelService{failFrom: failFrom, failStatus: failStatus, failBody: "服务不可用"}
	service.turns = append(service.turns, script["match_causes"]...)
	service.turns = append(service.turns, script["write_report"]...)
	service.server = httptest.NewServer(http.HandlerFunc(service.serve))
	t.Cleanup(service.server.Close)
	return service
}

func (s *fakeModelService) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	s.bodies = append(s.bodies, body)
	if s.failStatus != 0 && s.answered >= s.failFrom {
		http.Error(w, s.failBody, s.failStatus)
		return
	}
	turn := s.turns[s.answered]
	s.answered++
	calls := []map[string]any{}
	for index, call := range turn.ToolCalls {
		arguments, _ := json.Marshal(call.Arguments)
		calls = append(calls, map[string]any{
			"id": fmt.Sprintf("call_%d_%d", s.answered, index), "type": "function",
			"function": map[string]any{"name": call.Name, "arguments": string(arguments)},
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{
		"message":       map[string]any{"content": "", "tool_calls": calls},
		"finish_reason": "tool_calls",
	}}})
}

// recover 模型服务恢复正常。
func (s *fakeModelService) recover() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failStatus = 0
}

func (s *fakeModelService) requestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

func connectionTo(url string, maxRetries int) contract.ModelConnection {
	return contract.ModelConnection{
		Name: "fake", BaseURL: url, APIKey: fakeModelKey, Models: []string{"fake-model"}, MaxRetries: &maxRetries,
	}
}

// stepStarts 每个步骤被启动的次数。
func stepStarts(t *testing.T, h *harness, taskID string) map[string]int {
	t.Helper()
	events := []contract.TaskEvent{}
	if err := json.Unmarshal([]byte(h.eventsJSON(taskID)), &events); err != nil {
		t.Fatal(err)
	}
	starts := map[string]int{}
	for _, event := range events {
		if event.Type == contract.EventStepStarted {
			starts[event.StepID]++
		}
	}
	return starts
}

func TestInitiatorRetriesAfterAuthFailureFromFailedStep(t *testing.T) {
	// 第一个判断步骤成功，第二个收到 401
	service := newFakeModelService(t, caseScript(t, "A-01"), 1, http.StatusUnauthorized)
	h := newHarness(t, nil, withModelConnection(connectionTo(service.server.URL, 2)))
	task := h.createTask(a01Files(t))

	paused := h.command(task.ID, initiator, cmd(contract.CmdSubmit))
	expectState(t, paused, contract.StateAwaitingOperator)
	if paused.Pause.Code != contract.PauseModelUnavailable {
		t.Fatalf("暂停原因 = %+v", paused.Pause)
	}
	if paused.Pause.Title != "模型认证失败" || !strings.Contains(paused.Pause.Resolution, "Key") {
		t.Fatalf("说明要给出下一步：%+v", paused.Pause)
	}
	expectActions(t, paused, "retry=initiator,operator", "terminate=operator")
	expectWaitingFor(t, paused, initiator, operator)
	if text := h.summary(task.ID).StatusText; text != "模型认证失败" {
		t.Fatalf("任务列表状态 = %q", text)
	}
	// 认证失败不重试：配了 2 次重试，401 也只请求一次
	if got := service.requestCount(); got != 2 {
		t.Fatalf("模型请求次数 = %d，want 2（一次成功 + 一次 401）", got)
	}

	service.recover()
	resumed := h.command(task.ID, initiator, cmd(contract.CmdRetry))
	expectState(t, resumed, contract.StateAwaitingReview)

	starts := stepStarts(t, h, task.ID)
	for _, step := range []string{"admission", "load_data", "compute_metrics", "parse_minutes", "match_causes", "detect_contradictions"} {
		if starts[step] != 1 {
			t.Fatalf("已完成的步骤 %s 启动了 %d 次：%v", step, starts[step], starts)
		}
	}
	if starts["write_report"] != 2 {
		t.Fatalf("失败的步骤应再跑一次：%v", starts)
	}
	if got := service.requestCount(); got != 3 {
		t.Fatalf("重试后模型请求次数 = %d，want 3", got)
	}
}

func TestOperatorRetriesModelUnavailable(t *testing.T) {
	service := newFakeModelService(t, caseScript(t, "A-01"), 0, http.StatusUnauthorized)
	h := newHarness(t, nil, withModelConnection(connectionTo(service.server.URL, 0)))
	task := h.createTask(a01Files(t))
	expectState(t, h.command(task.ID, initiator, cmd(contract.CmdSubmit)), contract.StateAwaitingOperator)

	service.recover()
	expectState(t, h.command(task.ID, operator, cmd(contract.CmdRetry)), contract.StateAwaitingReview)
}

func TestModelErrorCategories(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		title    string
		advice   string
		requests int // 重试 2 次时的请求次数
	}{
		{"密钥无效", http.StatusUnauthorized, "模型认证失败", "Key", 1},
		{"无权访问", http.StatusForbidden, "模型认证失败", "Key", 1},
		{"限流", http.StatusTooManyRequests, "模型限流或欠费", "余额", 3},
		{"欠费", http.StatusPaymentRequired, "模型限流或欠费", "余额", 3},
		{"服务端错误", http.StatusBadGateway, "模型服务出错", "稍后", 3},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			service := newFakeModelService(t, caseScript(t, "A-01"), 0, testCase.status)
			h := newHarness(t, nil, withModelConnection(connectionTo(service.server.URL, 2)))
			task := h.createTask(a01Files(t))

			paused := h.command(task.ID, initiator, cmd(contract.CmdSubmit))
			expectState(t, paused, contract.StateAwaitingOperator)
			if paused.Pause.Code != contract.PauseModelUnavailable || paused.Pause.Title != testCase.title {
				t.Fatalf("暂停原因 = %+v", paused.Pause)
			}
			if !strings.Contains(paused.Pause.Resolution, testCase.advice) {
				t.Fatalf("下一步 = %q，应提到 %q", paused.Pause.Resolution, testCase.advice)
			}
			if got := service.requestCount(); got != testCase.requests {
				t.Fatalf("模型请求次数 = %d，want %d", got, testCase.requests)
			}
		})
	}
}

func TestModelServiceUnreachable(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	url := closed.URL
	closed.Close()

	h := newHarness(t, nil, withModelConnection(connectionTo(url, 1)))
	task := h.createTask(a01Files(t))

	paused := h.command(task.ID, initiator, cmd(contract.CmdSubmit))
	expectState(t, paused, contract.StateAwaitingOperator)
	if paused.Pause.Code != contract.PauseModelUnavailable || paused.Pause.Title != "模型连接不上" {
		t.Fatalf("暂停原因 = %+v", paused.Pause)
	}
	if !strings.Contains(paused.Pause.Resolution, "网络") {
		t.Fatalf("下一步 = %q", paused.Pause.Resolution)
	}
}

// 模型服务把 Key 回显到错误里时，它不能进事件日志或快照。
func TestModelErrorNeverLeaksKey(t *testing.T) {
	service := newFakeModelService(t, caseScript(t, "A-01"), 0, http.StatusUnauthorized)
	service.failBody = "Incorrect API key provided: " + fakeModelKey
	h := newHarness(t, nil, withModelConnection(connectionTo(service.server.URL, 0)))
	task := h.createTask(a01Files(t))

	paused := h.command(task.ID, initiator, cmd(contract.CmdSubmit))
	expectState(t, paused, contract.StateAwaitingOperator)
	raw, err := json.Marshal(paused)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), fakeModelKey) || strings.Contains(h.eventsJSON(task.ID), fakeModelKey) {
		t.Fatal("Key 出现在了快照或事件里")
	}
}

// 模型输出不合规是判断步骤失败，不是模型不可用。
func TestInvalidModelOutputIsNotModelUnavailable(t *testing.T) {
	script := agent.ModelScript{"match_causes": {{ToolCalls: []agent.ScriptedToolCall{
		{Name: agent.SubmitToolName, Arguments: map[string]any{"unexpected": true}},
	}}}}
	h := newHarness(t, script)
	task := h.createTask(a01Files(t))

	paused := h.command(task.ID, initiator, cmd(contract.CmdSubmit))
	expectState(t, paused, contract.StateAwaitingOperator)
	if paused.Pause.Code != contract.PauseToolFailed {
		t.Fatalf("暂停原因 = %+v", paused.Pause)
	}
}
