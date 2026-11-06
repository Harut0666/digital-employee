package engine

// 引擎状态机 / 编排 / 命令 / 预算 / 断点重放的单测。
// 用最小员工配置 + 假工具客户端 + 假判断执行器/质检引擎，不依赖 internal/tools（那是 tools-data 的包）。

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

// ---------------------------------------------------------------- 测试员工配置

type demoEmployeeOptions struct {
	modelCalls int
	timeoutSec int
	qcAutofix  int
	withRework bool
}

func writeDemoEmployee(t *testing.T, employeesDir string, options demoEmployeeOptions) {
	t.Helper()
	if options.modelCalls == 0 {
		options.modelCalls = 50
	}
	if options.timeoutSec == 0 {
		options.timeoutSec = 600
	}
	if options.qcAutofix == 0 {
		options.qcAutofix = 1
	}
	dir := filepath.Join(employeesDir, "demo")
	for _, sub := range []string{"prompts", "schemas"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("SKILL.md", "# 演示员工\n\n你是演示员工。")
	writeFile("form.schema.json", `{"type":"object","properties":{"period":{"type":"object"}}}`)
	writeFile("prompts/causes.md", "写原因")
	writeFile("prompts/write.md", "写周报")
	writeFile("schemas/causes.json", `{"type":"object","properties":{"causes":{"type":"array"}}}`)
	writeFile("schemas/write.json", `{"type":"object","properties":{"title":{"type":"string"}}}`)
	writeFile("qc.yaml", `builtin:
  structure:
    enabled: true
    sections: [{ id: summary, heading: 本周摘要 }]
    summaryMaxSentences: 5
    nextWeekMaxItems: 5
    minCharts: 0
  traceability: { enabled: true }
  anomaly_coverage: { enabled: true, threshold: 0.1 }
  quote_integrity: { enabled: true }
  contradiction_coverage: { enabled: true }
  forbidden_terms: { enabled: true, terms: [] }
  length: { enabled: true, maxPages: 3 }
  feedback_response: { enabled: true }
contradiction: { flatTolerance: 0.05, directionMin: 0.02, magnitudeTolerance: 0.1 }
custom: []
`)
	workflow := `id: demo
name: 演示员工
limits:
  task: { modelCalls: ` + itoa(options.modelCalls) + `, durationSec: ` + itoa(options.timeoutSec) + ` }
  toolRetry: { attempts: 2, backoffMs: [0] }
  qcAutofix: ` + itoa(options.qcAutofix) + `
admissionStep: admission
factsStep: compute
qcSources: { draft: write, minutes: parse_minutes, rendered: render }
qcFixSteps: { write: write, causes: causes }
steps:
  - { id: admission, label: 准入, kind: compute, tool: validate_submission, input: { period: "$form.period" } }
  - { id: compute, label: 计算, kind: compute, tool: compute_metrics, input: { period: "$form.period" } }
  - { id: causes, label: 原因, kind: judge, prompt: prompts/causes.md, input: { period: "$form.period" }, tools: [], outputSchema: schemas/causes.json, limits: { rounds: 3 } }
  - { id: write, label: 写作, kind: judge, prompt: prompts/write.md, input: { period: "$form.period" }, tools: [], outputSchema: schemas/write.json, limits: { rounds: 3 }, rework: true }
  - { id: parse_minutes, label: 纪要, kind: compute, tool: parse_minutes, input: { paths: [] } }
  - { id: render, label: 渲染, kind: compute, tool: render_report_docx, input: { report: "$rendered.write", facts: "$steps.compute", outDir: "$task.dir", idempotencyKey: "$task.idempotencyKey" } }
  - { id: qc, label: 质检, kind: qc }
`
	writeFile("workflow.yaml", workflow)
}

func itoa(value int) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

// ---------------------------------------------------------------- 假工具客户端

type fakeToolClient struct {
	mu      sync.Mutex
	calls   []string
	outputs map[string]func(args map[string]any) (any, error)
	results map[string]contract.ToolCallResult
}

func newFakeToolClient() *fakeToolClient {
	client := &fakeToolClient{
		outputs: map[string]func(args map[string]any) (any, error){},
		results: map[string]contract.ToolCallResult{},
	}
	client.outputs[contract.ToolValidateSubmission] = func(args map[string]any) (any, error) {
		return map[string]any{"ok": true, "sales": map[string]any{"path": "sales.xlsx"}, "dimensions": map[string]any{"stores": true}}, nil
	}
	client.outputs[contract.ToolComputeMetrics] = func(args map[string]any) (any, error) {
		return map[string]any{"company": map[string]any{"revenueCents": float64(100000)}}, nil
	}
	client.outputs[contract.ToolParseMinutes] = func(args map[string]any) (any, error) {
		return map[string]any{"minutes": []any{}}, nil
	}
	client.outputs[contract.ToolRenderReportDocx] = func(args map[string]any) (any, error) {
		outDir, _ := args["outDir"].(string)
		if outDir == "" {
			return nil, errors.New("缺少 outDir")
		}
		path := filepath.Join(outDir, "deliverables", "report.docx")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte("fake docx"), 0o644); err != nil {
			return nil, err
		}
		return map[string]any{"docxPath": path, "sha256": "deadbeef", "estimatedPages": float64(2), "chartCount": float64(3)}, nil
	}
	return client
}

func (c *fakeToolClient) ListTools() ([]contract.ToolDescriptor, error) {
	descriptors := []contract.ToolDescriptor{}
	for _, name := range contract.ToolNames {
		descriptors = append(descriptors, contract.ToolDescriptor{
			Name: name, Description: name,
			InputSchema: map[string]any{"type": "object", "additionalProperties": true},
		})
	}
	return descriptors, nil
}

func (c *fakeToolClient) Call(name string, args any) (contract.ToolCallResult, error) {
	c.mu.Lock()
	c.calls = append(c.calls, name)
	result, fixed := c.results[name]
	handler := c.outputs[name]
	c.mu.Unlock()
	if fixed {
		return result, nil
	}
	if handler == nil {
		return contract.ToolCallResult{OK: false, Error: "未注册的工具：" + name}, nil
	}
	mapped, _ := args.(map[string]any)
	data, err := handler(mapped)
	if err != nil {
		return contract.ToolCallResult{OK: false, Error: err.Error(), Retryable: true}, nil
	}
	return contract.ToolCallResult{OK: true, Data: data}, nil
}

func (c *fakeToolClient) Close() error { return nil }

func (c *fakeToolClient) callCount(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, call := range c.calls {
		if call == name {
			count++
		}
	}
	return count
}

// ---------------------------------------------------------------- 假判断执行器 / 质检引擎

type fakeJudgment struct {
	mu      sync.Mutex
	calls   []string
	outputs map[string]any
	fail    map[string]contract.JudgmentFailure
}

func newFakeJudgment() *fakeJudgment {
	return &fakeJudgment{
		outputs: map[string]any{},
		fail:    map[string]contract.JudgmentFailure{},
	}
}

func (f *fakeJudgment) Run(in agent.JudgmentInput) (contract.JudgmentResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, in.Step.ID)
	failure, shouldFail := f.fail[in.Step.ID]
	output := f.outputs[in.Step.ID]
	f.mu.Unlock()

	// 模拟一次模型调用（引擎据此记 model_called 事件）
	if in.OnModelCalled != nil {
		if err := in.OnModelCalled(agent.ModelCalledEvent{
			Turn: 0, Response: map[string]any{"role": "assistant"},
			Usage: &contract.EventUsage{Input: 1, Output: 1},
		}); err != nil {
			return contract.JudgmentResult{}, err
		}
	}
	if shouldFail {
		return contract.JudgmentResult{OK: false, Failure: failure, Message: "模拟失败"}, nil
	}
	if output == nil {
		output = map[string]any{}
	}
	return contract.JudgmentResult{OK: true, Output: output}, nil
}

func (f *fakeJudgment) callCount(stepID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, call := range f.calls {
		if call == stepID {
			count++
		}
	}
	return count
}

type fakeQC struct {
	mu      sync.Mutex
	queue   [][]contract.QcCheckResult
	results []contract.QcCheckResult
	runs    int
}

func newFakeQC() *fakeQC {
	return &fakeQC{}
}

func (f *fakeQC) Run(input contract.QcInput) []contract.QcCheckResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs++
	if len(f.queue) > 0 {
		next := f.queue[0]
		f.queue = f.queue[1:]
		f.results = next
		return next
	}
	if f.results != nil {
		return f.results
	}
	return []contract.QcCheckResult{{ID: "structure", Label: "结构完整", Passed: true, Issues: []string{}}}
}

func passingChecks() []contract.QcCheckResult {
	return []contract.QcCheckResult{
		{ID: "structure", Label: "结构完整", Passed: true, Issues: []string{}},
		{ID: "traceability", Label: "数据溯源", Passed: true, Issues: []string{}},
	}
}

// ---------------------------------------------------------------- 引擎装配

type demoEngine struct {
	engine   *Engine
	tools    *fakeToolClient
	judgment *fakeJudgment
	qc       *fakeQC
	dir      string
}

func newDemoEngine(t *testing.T, options demoEmployeeOptions) *demoEngine {
	t.Helper()
	root := t.TempDir()
	employeesDir := filepath.Join(root, "employees")
	if err := os.MkdirAll(employeesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeDemoEmployee(t, employeesDir, options)

	toolClient := newFakeToolClient()
	judgment := newFakeJudgment()
	judgment.outputs["causes"] = map[string]any{"causes": []any{}}
	judgment.outputs["write"] = map[string]any{
		"title": "周报 {{company.revenue}}",
		"sections": []any{
			map[string]any{"id": "summary", "heading": "本周摘要", "paragraphs": []any{"实收 {{company.revenue}}"}},
		},
		"causes": []any{},
	}
	qcEngine := newFakeQC()
	qcEngine.results = passingChecks()

	eng, err := NewWithOptions(Deps{
		WorkspaceDir: filepath.Join(root, "workspace"),
		EmployeesDir: employeesDir,
		ToolClient:   toolClient,
		Model:        agent.NewScriptedModelProvider(agent.ModelScript{}, agent.ScriptedProviderOptions{}),
	}, Options{Judgment: judgment, QC: qcEngine})
	if err != nil {
		t.Fatal(err)
	}
	return &demoEngine{engine: eng, tools: toolClient, judgment: judgment, qc: qcEngine, dir: root}
}

func (d *demoEngine) create(t *testing.T) *contract.TaskSnapshot {
	t.Helper()
	created, err := d.engine.Create(contract.CreateTaskInput{
		Employee: "demo",
		Form:     map[string]any{"period": map[string]any{"year": float64(2026), "week": float64(44)}},
		Actor:    contract.RoleInitiator,
	})
	if err != nil {
		t.Fatalf("创建任务失败：%v", err)
	}
	return created
}

func (d *demoEngine) submit(t *testing.T, taskID string) *contract.TaskSnapshot {
	t.Helper()
	result := d.engine.Command(taskID, contract.TaskCommand{Type: contract.CmdSubmit}, contract.RoleInitiator)
	if !result.OK {
		t.Fatalf("submit 被拒绝：%s（%s）", result.Error, result.Message)
	}
	snapshot, err := d.engine.WaitForSettled(taskID)
	if err != nil {
		t.Fatalf("等待停稳失败：%v", err)
	}
	return snapshot
}

func eventTypesOf(events []contract.TaskEvent) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

// ---------------------------------------------------------------- 状态流转表

func TestTransitionsTable(t *testing.T) {
	// 同一 from+trigger 只允许一个目标（retry 例外：awaiting_operator 下有两个）
	seen := map[string]contract.TaskState{}
	for _, transition := range contract.Transitions {
		key := string(transition.From) + "--" + string(transition.Trigger)
		if existing, ok := seen[key]; ok && existing != transition.To {
			if transition.Trigger != contract.TriggerRetry {
				t.Errorf("%s 有多个目标：%s / %s", key, existing, transition.To)
			}
			continue
		}
		seen[key] = transition.To
	}
	// 每个状态都有出去的路（终态除外）
	for _, state := range contract.TaskStates {
		if contract.IsTerminal(state) {
			continue
		}
		found := false
		for _, transition := range contract.Transitions {
			if transition.From == state {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("状态 %s 没有任何流转", state)
		}
	}
	// 关键流转存在
	cases := []struct {
		from    contract.TaskState
		trigger contract.TransitionTrigger
		to      contract.TaskState
	}{
		{contract.StateDraft, contract.TriggerSubmit, contract.StateAdmission},
		{contract.StateAdmission, contract.TriggerAdmissionPassed, contract.StateRunning},
		{contract.StateAdmission, contract.TriggerAdmissionFailed, contract.StateAwaitingInitiator},
		{contract.StateRunning, contract.TriggerStepsCompleted, contract.StateQC},
		{contract.StateQC, contract.TriggerQcPassed, contract.StateAwaitingReview},
		{contract.StateQC, contract.TriggerQcAutofix, contract.StateRunning},
		{contract.StateQC, contract.TriggerQcExhausted, contract.StateAwaitingOperator},
		{contract.StateAwaitingReview, contract.TriggerApprove, contract.StateDelivered},
		{contract.StateAwaitingReview, contract.TriggerReturn, contract.StateRunning},
		{contract.StateAwaitingOperator, contract.TriggerRetry, contract.StateRunning},
		{contract.StateAwaitingOperator, contract.TriggerTerminate, contract.StateTerminated},
	}
	for _, testCase := range cases {
		transition, ok := contract.FindTransition(testCase.from, testCase.trigger)
		if !ok || transition.To != testCase.to {
			t.Errorf("%s --%s--> 期望 %s，实际 %v / %v", testCase.from, testCase.trigger, testCase.to, transition.To, ok)
		}
	}
}

func TestIllegalTransitionsAreRejected(t *testing.T) {
	demo := newDemoEngine(t, demoEmployeeOptions{})
	task := demo.create(t)

	// draft 状态下只允许 initiator 的 submit
	attempts := []struct {
		command contract.TaskCommand
		actor   contract.Role
		error   string
		trigger contract.TransitionTrigger
	}{
		{contract.TaskCommand{Type: contract.CmdApprove}, contract.RoleReviewer, contract.ErrIllegalTransition, contract.TriggerApprove},
		{contract.TaskCommand{Type: contract.CmdReturn, Comment: "改"}, contract.RoleReviewer, contract.ErrIllegalTransition, contract.TriggerReturn},
		{contract.TaskCommand{Type: contract.CmdRetry}, contract.RoleOperator, contract.ErrIllegalTransition, contract.TriggerRetry},
		{contract.TaskCommand{Type: contract.CmdTerminate}, contract.RoleInitiator, contract.ErrIllegalTransition, contract.TriggerTerminate},
		{contract.TaskCommand{Type: contract.CmdSubmit}, contract.RoleReviewer, contract.ErrForbiddenRole, contract.TriggerSubmit},
	}
	for _, attempt := range attempts {
		result := demo.engine.Command(task.ID, attempt.command, attempt.actor)
		if result.OK {
			t.Fatalf("%s 应被拒绝", attempt.command.Type)
		}
		if result.Error != attempt.error {
			t.Fatalf("%s 的错误码 = %s，want %s", attempt.command.Type, result.Error, attempt.error)
		}
	}

	// 状态没有变化
	snapshot, ok := demo.engine.Get(task.ID)
	if !ok || snapshot.State != contract.StateDraft {
		t.Fatalf("状态 = %v，want draft", snapshot)
	}

	// 每次拒绝都写 transition_rejected 事件
	events, err := demo.engine.Events(task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	rejected := []contract.TaskEvent{}
	for _, event := range events {
		if event.Type == contract.EventTransitionRejected {
			rejected = append(rejected, event)
		}
	}
	if len(rejected) != len(attempts) {
		t.Fatalf("transition_rejected 事件数 = %d，want %d", len(rejected), len(attempts))
	}
	for index, event := range rejected {
		if event.From != contract.StateDraft {
			t.Errorf("第 %d 条 from = %s", index, event.From)
		}
		if event.Trigger != attempts[index].trigger {
			t.Errorf("第 %d 条 trigger = %s，want %s", index, event.Trigger, attempts[index].trigger)
		}
		if event.Actor != attempts[index].actor {
			t.Errorf("第 %d 条 actor = %s，want %s", index, event.Actor, attempts[index].actor)
		}
		if event.Reason == "" {
			t.Errorf("第 %d 条缺少 reason", index)
		}
	}
}

// ---------------------------------------------------------------- 全流程

func TestHappyPathToDelivered(t *testing.T) {
	demo := newDemoEngine(t, demoEmployeeOptions{})
	task := demo.create(t)

	snapshot := demo.submit(t, task.ID)
	if snapshot.State != contract.StateAwaitingReview {
		t.Fatalf("状态 = %s，want awaiting_review", snapshot.State)
	}
	if snapshot.Pause == nil || snapshot.Pause.Code != contract.PauseAwaitingReview {
		t.Fatalf("暂停原因 = %+v", snapshot.Pause)
	}
	if len(snapshot.QC) != 2 {
		t.Fatalf("质检项 = %d，want 2", len(snapshot.QC))
	}
	// 步骤都完成了
	for _, step := range snapshot.Steps {
		if step.Kind == contract.StepKindQC {
			if step.Status != contract.StepDone {
				t.Fatalf("质检步骤状态 = %s", step.Status)
			}
			continue
		}
		if step.Status != contract.StepDone {
			t.Fatalf("步骤 %s 状态 = %s，want done", step.ID, step.Status)
		}
	}
	// 工具调用顺序：admission → compute → parse_minutes → render
	if demo.tools.callCount(contract.ToolValidateSubmission) != 1 ||
		demo.tools.callCount(contract.ToolComputeMetrics) != 1 ||
		demo.tools.callCount(contract.ToolRenderReportDocx) != 1 {
		t.Fatalf("工具调用次数不对：%v", demo.tools.calls)
	}
	// 判断步骤各执行一次
	if demo.judgment.callCount("causes") != 1 || demo.judgment.callCount("write") != 1 {
		t.Fatalf("判断步骤调用次数不对：%v", demo.judgment.calls)
	}

	// 审核通过
	result := demo.engine.Command(task.ID, contract.TaskCommand{Type: contract.CmdApprove}, contract.RoleReviewer)
	if !result.OK {
		t.Fatalf("approve 被拒绝：%s（%s）", result.Error, result.Message)
	}
	snapshot = mustWait(t, demo.engine, task.ID)
	if snapshot.State != contract.StateDelivered {
		t.Fatalf("状态 = %s，want delivered", snapshot.State)
	}
	kinds := map[contract.DeliverableKind]bool{}
	for _, deliverable := range snapshot.Deliverables {
		kinds[deliverable.Kind] = true
		if _, err := os.Stat(deliverable.Path); err != nil {
			t.Fatalf("交付物 %s 不存在：%v", deliverable.Kind, err)
		}
	}
	if !kinds[contract.DeliverableDocx] || !kinds[contract.DeliverableQCRecord] {
		t.Fatalf("交付物 = %+v", snapshot.Deliverables)
	}

	// 事件日志：seq 连续、首条 task_created、末条 delivered 的 state_changed
	events, err := demo.engine.Events(task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if events[0].Type != contract.EventTaskCreated {
		t.Fatalf("首条事件 = %s", events[0].Type)
	}
	for index, event := range events {
		if event.Seq != int64(index+1) {
			t.Fatalf("第 %d 条 seq = %d", index+1, event.Seq)
		}
	}
	last := events[len(events)-1]
	if last.Type != contract.EventStateChanged || last.To != contract.StateDelivered || last.Trigger != contract.TriggerApprove {
		t.Fatalf("末条事件 = %+v", last)
	}

	// 从磁盘重放事件得到的快照必须与内存一致（断点恢复的基础）
	replayed, err := demo.engine.loadRuntime(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	replayedSnapshot := replayed.proj.Snapshot(task.ID, 0)
	if replayedSnapshot.State != contract.StateDelivered ||
		len(replayedSnapshot.Deliverables) != len(snapshot.Deliverables) ||
		replayedSnapshot.LastSeq != snapshot.LastSeq {
		t.Fatalf("重放快照与内存不一致：%+v", replayedSnapshot)
	}
}

func TestReviewReturnRework(t *testing.T) {
	demo := newDemoEngine(t, demoEmployeeOptions{})
	task := demo.create(t)
	if snapshot := demo.submit(t, task.ID); snapshot.State != contract.StateAwaitingReview {
		t.Fatalf("状态 = %s", snapshot.State)
	}

	// 退回应填写意见
	result := demo.engine.Command(task.ID, contract.TaskCommand{Type: contract.CmdReturn}, contract.RoleReviewer)
	if result.OK || result.Error != contract.ErrInvalidInput {
		t.Fatalf("空意见的退回应被拒绝：%+v", result)
	}

	result = demo.engine.Command(task.ID, contract.TaskCommand{
		Type: contract.CmdReturn, Comment: "摘要写得太长", Sections: []string{"summary"},
	}, contract.RoleReviewer)
	if !result.OK {
		t.Fatalf("退回被拒绝：%+v", result)
	}
	snapshot := mustWait(t, demo.engine, task.ID)
	if snapshot.State != contract.StateAwaitingReview {
		t.Fatalf("重做后状态 = %s，want awaiting_review", snapshot.State)
	}
	if snapshot.ReworkRound != 1 {
		t.Fatalf("reworkRound = %d，want 1", snapshot.ReworkRound)
	}
	// 只有标记了 rework 的判断步骤重跑；causes 不重跑
	if demo.judgment.callCount("write") != 2 {
		t.Fatalf("write 调用次数 = %d，want 2", demo.judgment.callCount("write"))
	}
	if demo.judgment.callCount("causes") != 1 {
		t.Fatalf("causes 调用次数 = %d，want 1", demo.judgment.callCount("causes"))
	}
	// 计算步骤入参没变时复用检查点，不重新调工具
	if demo.tools.callCount(contract.ToolComputeMetrics) != 1 {
		t.Fatalf("compute_metrics 调用次数 = %d，want 1（应复用检查点）", demo.tools.callCount(contract.ToolComputeMetrics))
	}

	// 再通过 → delivered
	result = demo.engine.Command(task.ID, contract.TaskCommand{Type: contract.CmdApprove}, contract.RoleReviewer)
	if !result.OK {
		t.Fatalf("approve 被拒绝：%+v", result)
	}
	snapshot = mustWait(t, demo.engine, task.ID)
	if snapshot.State != contract.StateDelivered {
		t.Fatalf("状态 = %s，want delivered", snapshot.State)
	}
}

func TestQcAutofixRerunsFixStep(t *testing.T) {
	demo := newDemoEngine(t, demoEmployeeOptions{qcAutofix: 1})
	// 第一次质检不通过（fixStep = write），第二次通过
	demo.qc.queue = [][]contract.QcCheckResult{
		{{ID: "structure", Label: "结构完整", Passed: false, Issues: []string{"缺少章节：本周摘要"}, FixStep: "write"}},
		passingChecks(),
	}
	task := demo.create(t)
	snapshot := demo.submit(t, task.ID)
	if snapshot.State != contract.StateAwaitingReview {
		t.Fatalf("状态 = %s，want awaiting_review", snapshot.State)
	}
	if demo.qc.runs != 2 {
		t.Fatalf("质检运行次数 = %d，want 2", demo.qc.runs)
	}
	if demo.judgment.callCount("write") != 2 {
		t.Fatalf("write 调用次数 = %d，want 2（自动修正应重跑）", demo.judgment.callCount("write"))
	}
	// 质检结果事件记录两轮
	events, _ := demo.engine.Events(task.ID, 0)
	attempts := []int{}
	for _, event := range events {
		if event.Type == contract.EventQCResult && event.Attempt != nil {
			attempts = append(attempts, *event.Attempt)
		}
	}
	if len(attempts) != 2 || attempts[0] != 1 || attempts[1] != 2 {
		t.Fatalf("qc_result attempt = %v，want [1 2]", attempts)
	}
}

func TestQcExhaustedGoesToOperator(t *testing.T) {
	demo := newDemoEngine(t, demoEmployeeOptions{qcAutofix: 1})
	alwaysFail := []contract.QcCheckResult{
		{ID: "structure", Label: "结构完整", Passed: false, Issues: []string{"缺少章节：本周摘要"}, FixStep: "write"},
	}
	demo.qc.results = alwaysFail
	task := demo.create(t)
	snapshot := demo.submit(t, task.ID)
	if snapshot.State != contract.StateAwaitingOperator {
		t.Fatalf("状态 = %s，want awaiting_operator", snapshot.State)
	}
	if snapshot.Pause == nil || snapshot.Pause.Code != contract.PauseQcExhausted {
		t.Fatalf("暂停原因 = %+v", snapshot.Pause)
	}
	if len(snapshot.Pause.Options) != 2 || snapshot.Pause.Options[0] != contract.CmdRetry {
		t.Fatalf("操作员选项 = %v", snapshot.Pause.Options)
	}

	// 操作员重试 → 重新执行并再次停在同样的位置
	demo.qc.results = passingChecks()
	result := demo.engine.Command(task.ID, contract.TaskCommand{Type: contract.CmdRetry}, contract.RoleOperator)
	if !result.OK {
		t.Fatalf("retry 被拒绝：%+v", result)
	}
	snapshot = mustWait(t, demo.engine, task.ID)
	if snapshot.State != contract.StateAwaitingReview {
		t.Fatalf("重试后状态 = %s，want awaiting_review", snapshot.State)
	}
}

func TestModelCallBudgetPauses(t *testing.T) {
	demo := newDemoEngine(t, demoEmployeeOptions{modelCalls: 1})
	task := demo.create(t)
	snapshot := demo.submit(t, task.ID)
	if snapshot.State != contract.StateAwaitingOperator {
		t.Fatalf("状态 = %s，want awaiting_operator", snapshot.State)
	}
	if snapshot.Pause == nil || snapshot.Pause.Code != contract.PauseLimitExceeded {
		t.Fatalf("暂停原因 = %+v", snapshot.Pause)
	}
	if snapshot.Budget.ModelCalls.Limit != 1 || snapshot.Budget.ModelCalls.Used != 1 {
		t.Fatalf("预算 = %+v", snapshot.Budget.ModelCalls)
	}
	// 第一个判断步骤执行过，第二个被预算挡住
	if demo.judgment.callCount("causes") != 1 || demo.judgment.callCount("write") != 0 {
		t.Fatalf("判断步骤调用 = %v", demo.judgment.calls)
	}
}

func TestJudgeFailurePauses(t *testing.T) {
	demo := newDemoEngine(t, demoEmployeeOptions{})
	demo.judgment.fail["causes"] = contract.FailInvalidOutput
	task := demo.create(t)
	snapshot := demo.submit(t, task.ID)
	if snapshot.State != contract.StateAwaitingOperator {
		t.Fatalf("状态 = %s，want awaiting_operator", snapshot.State)
	}
	if snapshot.Pause == nil || snapshot.Pause.Code != contract.PauseToolFailed {
		t.Fatalf("暂停原因 = %+v", snapshot.Pause)
	}
	events, _ := demo.engine.Events(task.ID, 0)
	found := false
	for _, event := range events {
		if event.Type == contract.EventStepFailed && event.StepID == "causes" {
			found = true
		}
	}
	if !found {
		t.Fatal("缺少 step_failed 事件")
	}
}

// ---------------------------------------------------------------- 断点重放（真实判断执行器 + 剧本式模型）

func crashableWorkspace(t *testing.T) (workspace, employeesDir string) {
	t.Helper()
	root := t.TempDir()
	employeesDir = filepath.Join(root, "employees")
	if err := os.MkdirAll(employeesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeDemoEmployee(t, employeesDir, demoEmployeeOptions{})
	return filepath.Join(root, "workspace"), employeesDir
}

func newScriptedEngine(t *testing.T, workspace, employeesDir string, script agent.ModelScript, faults *contract.FaultHooks) (*Engine, *agent.ScriptedProvider, *fakeToolClient, *fakeQC) {
	t.Helper()
	provider := agent.NewScriptedModelProvider(script, agent.ScriptedProviderOptions{})
	toolClient := newFakeToolClient()
	qcEngine := newFakeQC()
	qcEngine.results = passingChecks()
	eng, err := NewWithOptions(Deps{
		WorkspaceDir: workspace,
		EmployeesDir: employeesDir,
		ToolClient:   toolClient,
		Model:        provider,
		Faults:       faults,
	}, Options{QC: qcEngine})
	if err != nil {
		t.Fatal(err)
	}
	return eng, provider, toolClient, qcEngine
}

func submitScript() agent.ModelScript {
	return agent.ModelScript{
		"causes": {{ToolCalls: []agent.ScriptedToolCall{{Name: agent.SubmitToolName, Arguments: map[string]any{
			"causes": []any{map[string]any{"anomalyId": "region:华东", "cause": "天气", "level": "region"}},
		}}}}},
		"write": {{ToolCalls: []agent.ScriptedToolCall{{Name: agent.SubmitToolName, Arguments: map[string]any{
			"title": "周报",
			"sections": []any{map[string]any{
				"id": "summary", "heading": "本周摘要", "paragraphs": []any{"实收 {{company.revenue}}"},
			}},
			"causes": []any{},
		}}}}},
	}
}

func TestResumeReplaysRecordedModelResponses(t *testing.T) {
	workspace, employeesDir := crashableWorkspace(t)
	faults := &contract.FaultHooks{CrashAtStep: "write"}
	first, firstProvider, _, _ := newScriptedEngine(t, workspace, employeesDir, submitScript(), faults)

	task, err := first.Create(contract.CreateTaskInput{
		Employee: "demo",
		Form:     map[string]any{"period": map[string]any{"year": float64(2026), "week": float64(44)}},
		Actor:    contract.RoleInitiator,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := first.Command(task.ID, contract.TaskCommand{Type: contract.CmdSubmit}, contract.RoleInitiator)
	if !result.OK {
		t.Fatalf("submit 被拒绝：%+v", result)
	}
	_, waitErr := first.WaitForSettled(task.ID)
	var crash *SimulatedCrash
	if !errors.As(waitErr, &crash) {
		t.Fatalf("应因崩溃注入而失败，实际 %v", waitErr)
	}
	if crash.StepID != "write" {
		t.Fatalf("崩溃步骤 = %s，want write", crash.StepID)
	}
	// 崩溃后这个引擎实例不再写事件
	if result := first.Command(task.ID, contract.TaskCommand{Type: contract.CmdTerminate}, contract.RoleInitiator); result.OK {
		t.Fatal("崩溃后命令不应生效")
	}

	events, err := first.Events(task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	hasFault := false
	lastSeq := int64(0)
	for _, event := range events {
		if event.Type == contract.EventFaultInjected {
			hasFault = true
		}
		lastSeq = event.Seq
	}
	if !hasFault {
		t.Fatal("缺少 fault_injected 事件")
	}
	if len(firstProvider.Requests()) == 0 {
		t.Fatal("崩溃前应已调用过模型")
	}

	// 模拟进程重启：新引擎 + 新剧本提供者（游标归零），从磁盘恢复
	faults.CrashAtStep = ""
	second, secondProvider, _, _ := newScriptedEngine(t, workspace, employeesDir, submitScript(), faults)
	if err := second.ResumeAll(); err != nil {
		t.Fatalf("ResumeAll 失败：%v", err)
	}
	snapshot, err := second.WaitForSettled(task.ID)
	if err != nil {
		t.Fatalf("恢复后等待停稳失败：%v", err)
	}
	if snapshot.State != contract.StateAwaitingReview {
		t.Fatalf("恢复后状态 = %s，want awaiting_review", snapshot.State)
	}
	// 重放：write 步骤已记录的模型响应被直接复用，不再请求模型
	if got := len(secondProvider.Requests()); got != 0 {
		t.Fatalf("恢复后模型调用次数 = %d，want 0（应重放已记录的响应）", got)
	}
	// 事件 seq 连续，没有重复的 task_created
	resumed, err := second.Events(task.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	taskCreated := 0
	for index, event := range resumed {
		if event.Type == contract.EventTaskCreated {
			taskCreated++
		}
		if event.Seq != int64(index+1) {
			t.Fatalf("恢复后第 %d 条 seq = %d", index+1, event.Seq)
		}
	}
	if taskCreated != 1 {
		t.Fatalf("task_created 事件数 = %d，want 1", taskCreated)
	}
	if resumed[len(resumed)-1].Seq < lastSeq {
		t.Fatalf("恢复后的事件数比崩溃前少")
	}
}

func TestResumeAllSkipsFinishedTasks(t *testing.T) {
	demo := newDemoEngine(t, demoEmployeeOptions{})
	task := demo.create(t)
	demo.submit(t, task.ID)
	// 暂停态任务在 ResumeAll 时不应重新驱动
	before := len(eventTypesOf(mustEvents(t, demo.engine, task.ID)))
	if err := demo.engine.ResumeAll(); err != nil {
		t.Fatalf("ResumeAll 失败：%v", err)
	}
	if _, err := demo.engine.WaitForSettled(task.ID); err != nil {
		t.Fatalf("等待停稳失败：%v", err)
	}
	after := len(eventTypesOf(mustEvents(t, demo.engine, task.ID)))
	if after != before {
		t.Fatalf("暂停任务被 ResumeAll 重新执行：事件 %d → %d", before, after)
	}
}

func TestCreateValidation(t *testing.T) {
	demo := newDemoEngine(t, demoEmployeeOptions{})
	// 不存在的员工目录
	if _, err := demo.engine.Create(contract.CreateTaskInput{Employee: "nope", Actor: contract.RoleInitiator}); err == nil {
		t.Fatal("不存在的员工应报错")
	}
	// 非法员工 id
	if _, err := demo.engine.Create(contract.CreateTaskInput{Employee: "../demo", Actor: contract.RoleInitiator}); err == nil {
		t.Fatal("非法员工 id 应报错")
	}
	// reviewer 不能建任务
	if _, err := demo.engine.Create(contract.CreateTaskInput{Employee: "demo", Actor: contract.RoleReviewer}); err == nil {
		t.Fatal("reviewer 建任务应报错")
	}
	// 附件不存在
	if _, err := demo.engine.Create(contract.CreateTaskInput{
		Employee: "demo", Actor: contract.RoleInitiator,
		Attachments: []contract.AttachmentInput{{Name: "a.xlsx", SourcePath: filepath.Join(demo.dir, "nope.xlsx")}},
	}); err == nil {
		t.Fatal("附件不存在应报错")
	}
	// 未知任务
	result := demo.engine.Command("not-a-task", contract.TaskCommand{Type: contract.CmdSubmit}, contract.RoleInitiator)
	if result.OK || result.Error != contract.ErrNotFound {
		t.Fatalf("未知任务应返回 not_found：%+v", result)
	}
}

func TestListAndSubscribe(t *testing.T) {
	demo := newDemoEngine(t, demoEmployeeOptions{})
	task := demo.create(t)

	var received []contract.TaskEvent
	var mu sync.Mutex
	cancel := demo.engine.Subscribe(task.ID, func(event contract.TaskEvent) {
		mu.Lock()
		received = append(received, event)
		mu.Unlock()
	})
	demo.submit(t, task.ID)
	cancel()

	mu.Lock()
	count := len(received)
	mu.Unlock()
	if count == 0 {
		t.Fatal("订阅者没有收到事件")
	}

	summaries := demo.engine.List()
	if len(summaries) != 1 {
		t.Fatalf("任务数 = %d，want 1", len(summaries))
	}
	if summaries[0].State != contract.StateAwaitingReview {
		t.Fatalf("列表里的状态 = %s", summaries[0].State)
	}
	if !strings.Contains(summaries[0].Title, "2026 年第 44 周") {
		t.Fatalf("列表标题 = %q", summaries[0].Title)
	}
}

// TestConcurrentReadsDuringExecution 验证 HTTP 层并发读（Get/List/Events）与执行中的写事件不冲突。
// 配合 `go test -race` 使用；server 的 SSE/REST 就是这种并发形态。
func TestConcurrentReadsDuringExecution(t *testing.T) {
	demo := newDemoEngine(t, demoEmployeeOptions{})
	task := demo.create(t)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, ok := demo.engine.Get(task.ID); !ok {
					t.Errorf("执行中的任务应始终可读")
					return
				}
				demo.engine.List()
				if _, err := demo.engine.Events(task.ID, 0); err != nil {
					t.Errorf("读取事件失败：%v", err)
					return
				}
				cancel := demo.engine.Subscribe(task.ID, func(contract.TaskEvent) {})
				cancel()
			}
		}()
	}

	result := demo.engine.Command(task.ID, contract.TaskCommand{Type: contract.CmdSubmit}, contract.RoleInitiator)
	if !result.OK {
		t.Fatalf("submit 被拒绝：%+v", result)
	}
	snapshot, err := demo.engine.WaitForSettled(task.ID)
	if err != nil {
		t.Fatalf("等待停稳失败：%v", err)
	}
	close(stop)
	wg.Wait()
	if snapshot.State != contract.StateAwaitingReview {
		t.Fatalf("状态 = %s，want awaiting_review", snapshot.State)
	}
}

// TestConcurrentDuplicateApprove 两个 approve 同时到达时，只有一个真正生效，另一个被拒。
func TestConcurrentDuplicateApprove(t *testing.T) {
	demo := newDemoEngine(t, demoEmployeeOptions{})
	task := demo.create(t)
	demo.submit(t, task.ID)

	results := make([]contract.CommandResult, 2)
	var wg sync.WaitGroup
	for index := 0; index < 2; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index] = demo.engine.Command(task.ID, contract.TaskCommand{Type: contract.CmdApprove}, contract.RoleReviewer)
		}(index)
	}
	wg.Wait()

	okCount := 0
	for _, result := range results {
		if result.OK {
			okCount++
			continue
		}
		if result.Error != contract.ErrIllegalTransition {
			t.Fatalf("被拒绝的命令应返回 illegal_transition，实际 %s（%s）", result.Error, result.Message)
		}
	}
	if okCount != 1 {
		t.Fatalf("approve 成功次数 = %d，want 1", okCount)
	}

	events := mustEvents(t, demo.engine, task.ID)
	approvals := 0
	delivered := 0
	for _, event := range events {
		if event.Type == contract.EventReviewDecision && event.Decision == "approve" {
			approvals++
		}
		if event.Type == contract.EventStateChanged && event.To == contract.StateDelivered {
			delivered++
		}
	}
	if approvals != 1 || delivered != 1 {
		t.Fatalf("review_decision approve = %d，delivered 流转 = %d（都应为 1）", approvals, delivered)
	}
}

func mustWait(t *testing.T, eng *Engine, taskID string) *contract.TaskSnapshot {
	t.Helper()
	snapshot, err := eng.WaitForSettled(taskID)
	if err != nil {
		t.Fatalf("等待停稳失败：%v", err)
	}
	return snapshot
}

func mustEvents(t *testing.T, eng *Engine, taskID string) []contract.TaskEvent {
	t.Helper()
	events, err := eng.Events(taskID, 0)
	if err != nil {
		t.Fatalf("读取事件失败：%v", err)
	}
	return events
}
