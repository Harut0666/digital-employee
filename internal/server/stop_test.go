// 停止运行中的任务：全部从 HTTP 接口验证（Spec 0005“停止任务”）。
package server_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/engine"
	"digital-employee/internal/qc"
	"digital-employee/internal/tools"
)

// stopTimeout 等待异步动作的上限。任务真的卡住时测试失败，而不是挂死。
// 竞态检测下读样例表格要几十秒，所以放得宽。
const stopTimeout = 2 * time.Minute

func waitFor(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(stopTimeout):
		t.Fatalf("等待超时：%s", what)
	}
}

// heldModel 让某个步骤的模型调用停住，直到请求被取消，或测试放行。
type heldModel struct {
	inner agent.ModelProvider
	step  string
	// ignoreCancel 模拟收不到取消的模型：只有测试放行才返回，结果迟到
	ignoreCancel bool
	entered      chan struct{}
	release      chan struct{}
}

func holdModel(step string, ignoreCancel bool) (*heldModel, harnessOption) {
	held := &heldModel{step: step, ignoreCancel: ignoreCancel, entered: make(chan struct{}), release: make(chan struct{})}
	return held, withModel(func(inner agent.ModelProvider) agent.ModelProvider {
		held.inner = inner
		return held
	})
}

func (m *heldModel) Kind() string { return m.inner.Kind() }

func (m *heldModel) Resolve(stepID string, ref *contract.ModelRef) (*agent.ResolvedStepModel, error) {
	resolved, err := m.inner.Resolve(stepID, ref)
	if err != nil || stepID != m.step {
		return resolved, err
	}
	stream := resolved.Stream
	resolved.Stream = func(ctx context.Context, model agent.Model, request agent.StreamRequest, options agent.StreamOptions) (*agent.AssistantMessage, error) {
		close(m.entered)
		if m.ignoreCancel {
			<-m.release
			return stream(context.Background(), model, request, options)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-m.release:
			return stream(ctx, model, request, options)
		}
	}
	return resolved, nil
}

// submitTask 发起人提交任务，不等任务停稳。
func (h *harness) submitTask(taskID string) {
	h.t.Helper()
	if reply := h.tryCommand(taskID, initiator, cmd(contract.CmdSubmit)); !reply.OK {
		h.t.Fatalf("提交被拒绝：%+v", reply)
	}
}

// stop 发起人终止任务，要求接口立刻返回已终止。
func (h *harness) stop(taskID string) {
	h.t.Helper()
	reply := h.tryCommand(taskID, initiator, cmd(contract.CmdTerminate))
	if !reply.OK {
		h.t.Fatalf("终止被拒绝：%d %s %s", reply.status, reply.Error, reply.Message)
	}
	expectState(h.t, *reply.Snapshot, contract.StateTerminated)
}

// taskEvents GET /api/tasks/:id/events。
func (h *harness) taskEvents(taskID string) []contract.TaskEvent {
	h.t.Helper()
	return decode[[]contract.TaskEvent](h.t, []byte(h.eventsJSON(taskID)))
}

// expectStopped 任务停稳后是已终止，并且终止之后没有任何事件。
func (h *harness) expectStopped(taskID string) {
	h.t.Helper()
	settled := make(chan struct{})
	var snapshot contract.TaskSnapshot
	var failure error
	go func() {
		defer close(settled)
		if _, failure = h.engine.WaitForSettled(taskID); failure != nil {
			return
		}
	}()
	waitFor(h.t, settled, "任务停稳")
	if failure != nil {
		h.t.Fatalf("终止后任务不应报错：%v", failure)
	}
	snapshot = h.snapshot(taskID)
	expectState(h.t, snapshot, contract.StateTerminated)
	expectActions(h.t, snapshot)
	expectWaitingFor(h.t, snapshot)

	events := h.taskEvents(taskID)
	last := events[len(events)-1]
	if last.Type != contract.EventStateChanged || last.To != contract.StateTerminated {
		h.t.Fatalf("终止之后还有事件，最后一条是 %+v", last)
	}
}

func TestInitiatorStopsTaskDuringModelCall(t *testing.T) {
	model, holding := holdModel("match_causes", false)
	h := newHarness(t, caseScript(t, "A-01"), holding)
	task := h.createTask(a01Files(t))
	h.submitTask(task.ID)
	waitFor(t, model.entered, "模型调用开始")

	running := h.snapshot(task.ID)
	expectState(t, running, contract.StateRunning)
	expectActions(t, running, "terminate=initiator")
	expectWaitingFor(t, running)

	h.stop(task.ID)
	h.expectStopped(task.ID)
}

func TestLateModelResultIsDiscardedAfterStop(t *testing.T) {
	model, holding := holdModel("match_causes", true)
	h := newHarness(t, caseScript(t, "A-01"), holding)
	task := h.createTask(a01Files(t))
	h.submitTask(task.ID)
	waitFor(t, model.entered, "模型调用开始")

	h.stop(task.ID)
	before := len(h.taskEvents(task.ID))
	close(model.release)

	h.expectStopped(task.ID)
	if after := len(h.taskEvents(task.ID)); after != before {
		t.Fatalf("迟到的模型结果写了 %d 条事件", after-before)
	}
	if used := h.snapshot(task.ID).Budget.ModelCalls.Used; used != 0 {
		t.Fatalf("迟到的模型调用不应计入预算：%d", used)
	}
}

func TestOnlyInitiatorStopsRunningTask(t *testing.T) {
	model, holding := holdModel("match_causes", false)
	h := newHarness(t, caseScript(t, "A-01"), holding)
	task := h.createTask(a01Files(t))
	h.submitTask(task.ID)
	waitFor(t, model.entered, "模型调用开始")

	refused := h.tryCommand(task.ID, operator, cmd(contract.CmdTerminate))
	if refused.status != http.StatusForbidden || refused.Error != contract.ErrForbiddenRole {
		t.Fatalf("操作员不能终止运行中的任务：%+v", refused)
	}
	if want := "操作员无权在「执行中」执行「终止」，应由发起人执行。"; refused.Message != want {
		t.Fatalf("拒绝提示 = %q，want %q", refused.Message, want)
	}
	expectState(t, h.snapshot(task.ID), contract.StateRunning)

	h.stop(task.ID)
	h.expectStopped(task.ID)
}

// heldTools 让某个工具的调用停住，直到测试放行。进程内工具打断不了，取消对它无效。
type heldTools struct {
	contract.ToolClient
	tool    string
	entered chan struct{}
	release chan struct{}
}

func holdTool(tool string) (*heldTools, harnessOption) {
	held := &heldTools{tool: tool, entered: make(chan struct{}), release: make(chan struct{})}
	return held, withTools(func(inner contract.ToolClient) contract.ToolClient {
		held.ToolClient = inner
		return held
	})
}

func (c *heldTools) Call(name string, args any) (contract.ToolCallResult, error) {
	if name == c.tool {
		close(c.entered)
		<-c.release
	}
	return c.ToolClient.Call(name, args)
}

func TestInitiatorStopsTaskDuringToolCall(t *testing.T) {
	for _, stage := range []struct {
		tool  string
		state contract.TaskState
	}{
		{contract.ToolValidateSubmission, contract.StateAdmission},
		{contract.ToolLoadSales, contract.StateRunning},
	} {
		t.Run(string(stage.state), func(t *testing.T) {
			tool, holding := holdTool(stage.tool)
			h := newHarness(t, caseScript(t, "A-01"), holding)
			task := h.createTask(a01Files(t))
			h.submitTask(task.ID)
			waitFor(t, tool.entered, "工具调用开始")

			running := h.snapshot(task.ID)
			expectState(t, running, stage.state)
			expectActions(t, running, "terminate=initiator")

			h.stop(task.ID)
			before := len(h.taskEvents(task.ID))
			close(tool.release)

			h.expectStopped(task.ID)
			if after := len(h.taskEvents(task.ID)); after != before {
				t.Fatalf("迟到的工具结果写了 %d 条事件", after-before)
			}
		})
	}
}

// heldQC 让质检停住，直到测试放行。
type heldQC struct {
	entered chan struct{}
	release chan struct{}
}

func (q *heldQC) Run(input contract.QcInput) []contract.QcCheckResult {
	close(q.entered)
	<-q.release
	return qc.NewEngine().Run(input)
}

func TestInitiatorStopsTaskDuringQc(t *testing.T) {
	held := &heldQC{entered: make(chan struct{}), release: make(chan struct{})}
	h := newHarness(t, caseScript(t, "A-01"), withQC(held))
	task := h.createTask(a01Files(t))
	h.submitTask(task.ID)
	waitFor(t, held.entered, "质检开始")

	running := h.snapshot(task.ID)
	expectState(t, running, contract.StateQC)
	expectActions(t, running, "terminate=initiator")

	h.stop(task.ID)
	close(held.release)

	h.expectStopped(task.ID)
	if deliverables := h.snapshot(task.ID).Deliverables; len(deliverables) != 0 {
		t.Fatalf("质检中终止不应产生交付物：%+v", deliverables)
	}
}

func TestStopInterruptsToolRetryWait(t *testing.T) {
	// 重试等待比测试的等待上限还长：不被打断就一定超时
	h := newHarness(t, caseScript(t, "A-01"), withWorkflowEdit("backoffMs: [0]", "backoffMs: [600000]"))
	h.setToolFaults(map[string]string{contract.ToolLoadSales: "error"})
	task := h.createTask(a01Files(t))
	h.submitTask(task.ID)
	h.waitForEvent(task.ID, func(event contract.TaskEvent) bool {
		return event.Type == contract.EventToolResult && event.OK != nil && !*event.OK
	})

	h.stop(task.ID)
	h.expectStopped(task.ID)
}

// waitForEvent 轮询事件接口，直到出现符合条件的事件。
func (h *harness) waitForEvent(taskID string, match func(contract.TaskEvent) bool) {
	h.t.Helper()
	deadline := time.Now().Add(stopTimeout)
	for time.Now().Before(deadline) {
		for _, event := range h.taskEvents(taskID) {
			if match(event) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.t.Fatal("等待事件超时")
}

// restart 用同一工作区起一个新引擎并恢复活跃任务，相当于进程重启。
func (h *harness) restart(model agent.ModelProvider) *engine.Engine {
	h.t.Helper()
	registry := tools.NewRegistry()
	h.t.Cleanup(func() { _ = registry.Close() })
	restarted, err := engine.New(engine.Deps{
		Dir:          h.workspace,
		EmployeesDir: h.employees,
		ToolClient:   registry,
		Model:        model,
	})
	if err != nil {
		h.t.Fatalf("重启引擎失败：%v", err)
	}
	if err := restarted.ResumeAll(); err != nil {
		h.t.Fatalf("恢复任务失败：%v", err)
	}
	return restarted
}

func TestStoppedTaskStaysStoppedAfterRestart(t *testing.T) {
	model, holding := holdModel("match_causes", false)
	h := newHarness(t, caseScript(t, "A-01"), holding)
	task := h.createTask(a01Files(t))
	h.submitTask(task.ID)
	waitFor(t, model.entered, "模型调用开始")
	h.stop(task.ID)
	h.expectStopped(task.ID)
	before := len(h.taskEvents(task.ID))

	restarted := h.restart(agent.NewScriptedModelProvider(caseScript(t, "A-01"), agent.ScriptedProviderOptions{}))
	snapshot, err := restarted.WaitForSettled(task.ID)
	if err != nil {
		t.Fatalf("等待任务停稳失败：%v", err)
	}
	expectState(t, *snapshot, contract.StateTerminated)
	events, err := restarted.Events(task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != before {
		t.Fatalf("已终止的任务重启后写了 %d 条事件", len(events)-before)
	}
}

func TestCrashedTaskResumesAfterRestartAndCanBeStopped(t *testing.T) {
	script := caseScript(t, "A-01")
	h := newHarness(t, script, withCrashAt("match_causes"))
	task := h.createTask(a01Files(t))
	h.submitTask(task.ID)
	var crash *engine.SimulatedCrash
	if _, err := h.engine.WaitForSettled(task.ID); !errors.As(err, &crash) {
		t.Fatalf("应该崩溃在 match_causes：%v", err)
	}

	// 恢复后重放已记录的响应，接着走到下一个判断步骤
	model, _ := holdModel("write_report", false)
	model.inner = agent.NewScriptedModelProvider(script, agent.ScriptedProviderOptions{})
	restarted := h.restart(model)
	waitFor(t, model.entered, "恢复后的模型调用开始")

	reply := restarted.Command(task.ID, contract.TaskCommand{Type: contract.CmdTerminate}, initiator)
	if !reply.OK {
		t.Fatalf("恢复的任务应该能终止：%+v", reply)
	}
	snapshot, err := restarted.WaitForSettled(task.ID)
	if err != nil {
		t.Fatalf("等待任务停稳失败：%v", err)
	}
	expectState(t, *snapshot, contract.StateTerminated)
}

func TestStopAbortsRealModelRequest(t *testing.T) {
	var requests atomic.Int32
	entered := make(chan struct{})
	aborted := make(chan struct{})
	finished := make(chan struct{})
	fake := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// 读完请求体，服务端才能感知客户端断开
		_, _ = io.Copy(io.Discard, r.Body)
		attempt := requests.Add(1)
		if attempt == 1 {
			close(entered)
		}
		select {
		case <-r.Context().Done():
			if attempt == 1 {
				close(aborted)
			}
		case <-finished:
		}
	}))
	t.Cleanup(fake.Close)
	t.Cleanup(func() { close(finished) })

	retries := 3
	real := agent.NewModelProvider(contract.ModelSettings{
		Default: contract.ModelRef{Connection: "fake", Model: "fake-model"},
		Connections: []contract.ModelConnection{{
			Name: "fake", BaseURL: fake.URL, APIKey: "test-key", Models: []string{"fake-model"}, MaxRetries: &retries,
		}},
	})
	h := newHarness(t, agent.ModelScript{}, withModel(func(agent.ModelProvider) agent.ModelProvider { return real }))
	task := h.createTask(a01Files(t))
	h.submitTask(task.ID)
	waitFor(t, entered, "模型请求到达")

	h.stop(task.ID)
	waitFor(t, aborted, "模型请求被中止")
	h.expectStopped(task.ID)
	if got := requests.Load(); got != 1 {
		t.Fatalf("终止后不应重试模型请求，共收到 %d 次", got)
	}
}
