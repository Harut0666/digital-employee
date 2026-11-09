package agent_test

// 判断步骤执行器 + 剧本式模型 + 执行参数解析 + 输出 Schema 校验的单测。

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

// ---------------------------------------------------------------- 假工具客户端

type fakeToolClient struct {
	mu    sync.Mutex
	calls []string
	// results 按工具名返回结果
	results map[string]contract.ToolCallResult
}

func newFakeToolClient() *fakeToolClient {
	return &fakeToolClient{results: map[string]contract.ToolCallResult{}}
}

func (c *fakeToolClient) ListTools() ([]contract.ToolDescriptor, error) {
	names := []string{contract.ToolValidateSubmission, contract.ToolRenderReportDocx}
	descriptors := make([]contract.ToolDescriptor, 0, len(names))
	for _, name := range names {
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
	result, ok := c.results[name]
	c.mu.Unlock()
	if !ok {
		return contract.ToolCallResult{OK: true, Data: map[string]any{"tool": name}}, nil
	}
	return result, nil
}

func (c *fakeToolClient) Close() error { return nil }

// ---------------------------------------------------------------- 夹具

func judgeStep() contract.StepConfig {
	rounds := 4
	return contract.StepConfig{
		ID: "match_causes", Label: "匹配原因", Kind: contract.StepKindJudge,
		Prompt: "prompts/match_causes.md", Tools: []string{},
		OutputSchema: "schemas/match_causes.json",
		Limits:       &contract.StepLimits{Rounds: &rounds},
		Model:        &contract.ModelRef{Connection: "default", Model: "default"},
	}
}

func causesSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"causes"},
		"properties": map[string]any{
			"causes": map[string]any{
				"type":     "array",
				"minItems": float64(1),
				"items": map[string]any{
					"type":     "object",
					"required": []any{"anomalyId", "cause", "level"},
					"properties": map[string]any{
						"anomalyId": map[string]any{"type": "string", "minLength": float64(1)},
						"cause":     map[string]any{"type": "string", "minLength": float64(1)},
						"level": map[string]any{
							"type": "string",
							"enum": []any{"store", "store_type", "region", "company", "unknown"},
						},
					},
					"additionalProperties": false,
				},
			},
		},
	}
}

func validSubmission() map[string]any {
	return map[string]any{
		"causes": []any{map[string]any{"anomalyId": "region:华东", "cause": "天气", "level": "region"}},
	}
}

func textTurn(text string) agent.ScriptedTurn {
	return agent.ScriptedTurn{Text: &text}
}

func toolTurn(calls ...agent.ScriptedToolCall) agent.ScriptedTurn {
	return agent.ScriptedTurn{ToolCalls: calls}
}

func submitTurn(arguments map[string]any) agent.ScriptedTurn {
	return toolTurn(agent.ScriptedToolCall{Name: agent.SubmitToolName, Arguments: arguments})
}

func newRunner() *agent.Runner {
	return agent.NewJudgmentRunner(agent.RunnerOptions{})
}

func runInput(script agent.ModelScript) agent.JudgmentInput {
	return agent.JudgmentInput{
		TaskID:       "task-1",
		Step:         judgeStep(),
		SystemPrompt: "系统提示词",
		Context:      map[string]any{"period": map[string]any{"year": float64(2026), "week": float64(44)}},
		ToolClient:   newFakeToolClient(),
		Model:        agent.NewScriptedModelProvider(script, agent.ScriptedProviderOptions{}),
		OutputSchema: causesSchema(),
		Budget: agent.Budget{
			RemainingModelCalls: 5,
			Deadline:            time.Now().Add(time.Minute),
		},
	}
}

// ---------------------------------------------------------------- 剧本式模型

func TestScriptedProviderConsumesTurnsInOrder(t *testing.T) {
	t.Parallel()
	script := agent.ModelScript{"step": {textTurn("一"), textTurn("二")}}
	provider := agent.NewScriptedModelProvider(script, agent.ScriptedProviderOptions{})
	resolved, err := provider.Resolve("step", &contract.ModelRef{Connection: "default", Model: "default"})
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range []string{"一", "二"} {
		message, err := resolved.Stream(context.Background(), resolved.Model, agent.StreamRequest{}, agent.StreamOptions{})
		if err != nil {
			t.Fatalf("第 %d 轮失败：%v", index+1, err)
		}
		if message.StopReason != agent.StopStop || len(message.Content) != 1 || message.Content[0].Text != want {
			t.Fatalf("第 %d 轮 = %+v", index+1, message)
		}
	}
	// 剧本用完后返回错误消息（不抛 Go error）
	message, err := resolved.Stream(context.Background(), resolved.Model, agent.StreamRequest{}, agent.StreamOptions{})
	if err != nil {
		t.Fatalf("剧本用尽不应抛 Go error：%v", err)
	}
	if message.StopReason != agent.StopError || !strings.Contains(message.ErrorMessage, "剧本已用完") {
		t.Fatalf("剧本用尽 = %+v", message)
	}
	if len(provider.Requests()) != 3 {
		t.Fatalf("记录的请求数 = %d，want 3", len(provider.Requests()))
	}
}

func TestScriptedProviderToolCalls(t *testing.T) {
	t.Parallel()
	script := agent.ModelScript{"step": {toolTurn(
		agent.ScriptedToolCall{Name: agent.SubmitToolName, Arguments: map[string]any{"a": float64(1)}},
		agent.ScriptedToolCall{Name: contract.ToolValidateSubmission, Arguments: map[string]any{}},
	)}}
	provider := agent.NewScriptedModelProvider(script, agent.ScriptedProviderOptions{})
	resolved, _ := provider.Resolve("step", &contract.ModelRef{})
	message, err := resolved.Stream(context.Background(), resolved.Model, agent.StreamRequest{}, agent.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if message.StopReason != agent.StopToolUse {
		t.Fatalf("stopReason = %s", message.StopReason)
	}
	if len(message.Content) != 2 {
		t.Fatalf("内容块 = %+v", message.Content)
	}
	if message.Content[0].ID != "scripted_call_1" || message.Content[0].Name != agent.SubmitToolName {
		t.Fatalf("第一个工具调用 = %+v", message.Content[0])
	}
	if message.Content[1].ID != "scripted_call_2" {
		t.Fatalf("工具调用 id 应逐个递增：%+v", message.Content[1])
	}
	// 响应能原样重放（断点恢复用）
	decoded, ok := agent.DecodeAssistantMessage(message)
	if !ok || decoded.StopReason != agent.StopToolUse || len(decoded.Content) != 2 {
		t.Fatalf("重放解码失败：%+v / %v", decoded, ok)
	}
}

func TestScriptedProviderDelayRespectsContext(t *testing.T) {
	t.Parallel()
	delay := 5000
	then := textTurn("完成")
	script := agent.ModelScript{"step": {agent.ScriptedTurn{DelayMS: &delay, Then: &then}}}
	provider := agent.NewScriptedModelProvider(script, agent.ScriptedProviderOptions{})
	resolved, _ := provider.Resolve("step", &contract.ModelRef{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	message, err := resolved.Stream(ctx, resolved.Model, agent.StreamRequest{}, agent.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("延迟没有随上下文取消")
	}
	if message.StopReason != agent.StopAborted {
		t.Fatalf("中止时应返回 aborted：%+v", message)
	}
}

// ---------------------------------------------------------------- 执行器

func TestRunnerSubmitsValidResult(t *testing.T) {
	t.Parallel()
	input := runInput(agent.ModelScript{"match_causes": {submitTurn(validSubmission())}})
	var modelCalls []agent.ModelCalledEvent
	input.OnModelCalled = func(event agent.ModelCalledEvent) error {
		modelCalls = append(modelCalls, event)
		return nil
	}
	result, err := newRunner().Run(input)
	if err != nil {
		t.Fatalf("执行失败：%v", err)
	}
	if !result.OK {
		t.Fatalf("应成功：%+v", result)
	}
	if result.ModelCalls != 1 {
		t.Fatalf("modelCalls = %d，want 1", result.ModelCalls)
	}
	output, _ := result.Output.(map[string]any)
	causes, _ := output["causes"].([]any)
	if len(causes) != 1 {
		t.Fatalf("output = %+v", result.Output)
	}
	if len(modelCalls) != 1 || modelCalls[0].Turn != 0 {
		t.Fatalf("model_called 回调 = %+v", modelCalls)
	}
	if modelCalls[0].Usage == nil || modelCalls[0].Response == nil {
		t.Fatalf("model_called 缺字段：%+v", modelCalls[0])
	}
}

// 有人看增量时判断步骤改走流式：每片文字都交出去（不落盘），落盘的仍是完整的一次调用。
func TestRunnerEmitsModelDeltas(t *testing.T) {
	t.Parallel()
	text := "先匹配原因"
	turn := agent.ScriptedTurn{
		Text:      &text,
		Chunks:    []string{"先", "匹配", "原因"},
		ToolCalls: []agent.ScriptedToolCall{{Name: agent.SubmitToolName, Arguments: validSubmission()}},
	}
	input := runInput(agent.ModelScript{"match_causes": {turn}})
	var deltas []agent.ModelDeltaEvent
	var modelCalls []agent.ModelCalledEvent
	input.OnModelDelta = func(event agent.ModelDeltaEvent) { deltas = append(deltas, event) }
	input.OnModelCalled = func(event agent.ModelCalledEvent) error {
		modelCalls = append(modelCalls, event)
		return nil
	}

	result, err := newRunner().Run(input)
	if err != nil {
		t.Fatalf("执行失败：%v", err)
	}
	if !result.OK {
		t.Fatalf("应成功：%+v", result)
	}
	if len(deltas) != 3 {
		t.Fatalf("文字增量 = %+v", deltas)
	}
	for _, delta := range deltas {
		if delta.StepID != "match_causes" || delta.Turn != 0 || delta.Text == "" {
			t.Fatalf("增量应带步骤、轮次和文字：%+v", delta)
		}
	}
	if got := strings.Join([]string{deltas[0].Text, deltas[1].Text, deltas[2].Text}, ""); got != text {
		t.Fatalf("增量拼起来 = %q，want %q", got, text)
	}
	// 落盘的仍是一次调用的完整响应
	if len(modelCalls) != 1 {
		t.Fatalf("model_called 回调 = %+v", modelCalls)
	}
}

func TestRunnerRejectsInvalidOutput(t *testing.T) {
	t.Parallel()
	input := runInput(agent.ModelScript{"match_causes": {submitTurn(map[string]any{"causes": []any{}})}})
	result, err := newRunner().Run(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Failure != contract.FailInvalidOutput {
		t.Fatalf("结果 = %+v", result)
	}
	if !strings.Contains(result.Message, "提交的结果不符合输出 Schema") {
		t.Fatalf("失败说明 = %q", result.Message)
	}
}

func TestRunnerNoSubmission(t *testing.T) {
	t.Parallel()
	// 模型只说话不提交：轮次用尽
	input := runInput(agent.ModelScript{"match_causes": {textTurn("我拒绝提交")}})
	rounds := 1
	input.Step.Limits = &contract.StepLimits{Rounds: &rounds}
	result, err := newRunner().Run(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Failure != contract.FailStepRounds {
		t.Fatalf("结果 = %+v", result)
	}

	// 剧本里没有这一轮 → model_error（剧本已用完）
	input = runInput(agent.ModelScript{"match_causes": {textTurn("第一轮")}})
	result, err = newRunner().Run(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Failure != contract.FailModelError {
		t.Fatalf("结果 = %+v", result)
	}
}

func TestRunnerModelError(t *testing.T) {
	t.Parallel()
	message := "连接超时"
	input := runInput(agent.ModelScript{"match_causes": {agent.ScriptedTurn{Error: &message}}})
	result, err := newRunner().Run(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Failure != contract.FailModelError || !strings.Contains(result.Message, "连接超时") {
		t.Fatalf("结果 = %+v", result)
	}
}

func TestRunnerDeniesToolOutsideWhitelist(t *testing.T) {
	t.Parallel()
	input := runInput(agent.ModelScript{"match_causes": {
		toolTurn(agent.ScriptedToolCall{Name: contract.ToolValidateSubmission, Arguments: map[string]any{}}),
		submitTurn(validSubmission()),
	}})
	denied := []agent.ToolDeniedEvent{}
	input.OnToolDenied = func(event agent.ToolDeniedEvent) error {
		denied = append(denied, event)
		return nil
	}
	result, err := newRunner().Run(input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("越权调用后仍应能提交：%+v", result)
	}
	if len(denied) != 1 || denied[0].Tool != contract.ToolValidateSubmission {
		t.Fatalf("tool_denied 回调 = %+v", denied)
	}
	if !strings.Contains(denied[0].Reason, "未对本步骤开放") {
		t.Fatalf("拒绝原因 = %q", denied[0].Reason)
	}
	// 越权工具没有被真正调用
	runtimeClient := input.ToolClient.(*fakeToolClient)
	if len(runtimeClient.calls) != 0 {
		t.Fatalf("越权工具不应被调用：%v", runtimeClient.calls)
	}
}

func TestRunnerExecutesWhitelistedTool(t *testing.T) {
	t.Parallel()
	step := judgeStep()
	step.Tools = []string{contract.ToolValidateSubmission}
	input := runInput(agent.ModelScript{"match_causes": {
		toolTurn(agent.ScriptedToolCall{Name: contract.ToolValidateSubmission, Arguments: map[string]any{"period": map[string]any{}}}),
		submitTurn(validSubmission()),
	}})
	input.Step = step
	called := []agent.ToolCalledEvent{}
	input.OnToolCalled = func(event agent.ToolCalledEvent) error {
		called = append(called, event)
		return nil
	}
	result, err := newRunner().Run(input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("结果 = %+v", result)
	}
	if len(called) != 1 || !called[0].OK || called[0].Tool != contract.ToolValidateSubmission {
		t.Fatalf("tool_called 回调 = %+v", called)
	}
	runtimeClient := input.ToolClient.(*fakeToolClient)
	if len(runtimeClient.calls) != 1 {
		t.Fatalf("白名单工具应被调用一次：%v", runtimeClient.calls)
	}
}

func TestRunnerSendsSystemPromptFirst(t *testing.T) {
	t.Parallel()
	input := runInput(agent.ModelScript{"match_causes": {submitTurn(validSubmission())}})
	if _, err := newRunner().Run(input); err != nil {
		t.Fatal(err)
	}
	requests := input.Model.(*agent.ScriptedProvider).Requests()
	if len(requests) != 1 {
		t.Fatalf("请求数 = %d", len(requests))
	}
	first := requests[0].Request.Messages[0]
	if first.Role != "system" || first.Content[0].Text != "系统提示词" {
		t.Fatalf("第一条消息应是系统提示词：%+v", first)
	}
}

func TestRunnerKeepsEveryToolResultOfATurn(t *testing.T) {
	t.Parallel()
	step := judgeStep()
	step.Tools = []string{contract.ToolValidateSubmission}
	call := agent.ScriptedToolCall{Name: contract.ToolValidateSubmission, Arguments: map[string]any{"period": map[string]any{}}}
	input := runInput(agent.ModelScript{"match_causes": {toolTurn(call, call), submitTurn(validSubmission())}})
	input.Step = step
	if _, err := newRunner().Run(input); err != nil {
		t.Fatal(err)
	}
	requests := input.Model.(*agent.ScriptedProvider).Requests()
	toolMessages := 0
	for _, message := range requests[len(requests)-1].Request.Messages {
		if message.Role == "tool" {
			if len(message.Content) != 1 {
				t.Fatalf("一条 tool 消息只放一个结果，否则真实接口只收到第一个：%+v", message)
			}
			toolMessages++
		}
	}
	if toolMessages != 2 {
		t.Fatalf("tool 消息数 = %d，want 2", toolMessages)
	}
}

func TestRunnerReplaysRecordedResponses(t *testing.T) {
	t.Parallel()
	// 断点恢复：已记录的模型响应直接重放，不调用模型
	response := map[string]any{
		"role": "assistant",
		"content": []any{map[string]any{
			"type": agent.ContentToolCall, "id": "call-1", "name": agent.SubmitToolName,
			"arguments": validSubmission(),
		}},
		"api": "openai-completions", "provider": "scripted", "model": "scripted",
		"usage":      map[string]any{"input": float64(0), "output": float64(0)},
		"stopReason": agent.StopToolUse, "timestamp": float64(1),
	}
	input := runInput(agent.ModelScript{})
	input.Replay = []any{response}
	modelCalled := 0
	input.OnModelCalled = func(event agent.ModelCalledEvent) error {
		modelCalled++
		return nil
	}
	result, err := newRunner().Run(input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("重放后应成功：%+v", result)
	}
	if result.ModelCalls != 0 {
		t.Fatalf("重放不应计预算：modelCalls = %d", result.ModelCalls)
	}
	if modelCalled != 0 {
		t.Fatalf("重放不应触发 model_called 回调：%d", modelCalled)
	}
	provider := input.Model.(*agent.ScriptedProvider)
	if len(provider.Requests()) != 0 {
		t.Fatalf("重放不应调用模型：%d", len(provider.Requests()))
	}
}

func TestRunnerBudgetAndDeadline(t *testing.T) {
	t.Parallel()
	// 预算耗尽
	input := runInput(agent.ModelScript{"match_causes": {submitTurn(validSubmission())}})
	input.Budget.RemainingModelCalls = 0
	result, err := newRunner().Run(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Failure != contract.FailTaskCalls {
		t.Fatalf("预算耗尽 = %+v", result)
	}

	// 任务已过截止时间
	input = runInput(agent.ModelScript{"match_causes": {submitTurn(validSubmission())}})
	input.Budget.Deadline = time.Now().Add(-time.Second)
	result, err = newRunner().Run(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Failure != contract.FailTaskDuration {
		t.Fatalf("超时 = %+v", result)
	}
}

// ---------------------------------------------------------------- 执行参数解析

func TestExecutionResolvers(t *testing.T) {
	t.Parallel()
	execution := contract.DefaultExecutionSettings

	stepTimeout := 30_000.0
	if got := agent.ResolveStepTimeoutMS(&stepTimeout, &execution); got != 30_000 {
		t.Errorf("步骤配置优先：%d", got)
	}
	if got := agent.ResolveStepTimeoutMS(nil, &execution); got != execution.StepTimeoutMS {
		t.Errorf("设置默认值：%d", got)
	}
	if got := agent.ResolveStepTimeoutMS(nil, nil); got != agent.BuiltinStepTimeoutMS {
		t.Errorf("内置值：%d", got)
	}

	retries := 0
	if got := agent.ResolveModelMaxRetries(&retries, &execution); got != 0 {
		t.Errorf("0 是合法值（不重试）：%d", got)
	}
	if got := agent.ResolveModelMaxRetries(nil, &execution); got != execution.ModelMaxRetries {
		t.Errorf("设置默认值：%d", got)
	}
	if got := agent.ResolveModelMaxRetries(nil, nil); got != agent.BuiltinModelMaxRetries {
		t.Errorf("内置值：%d", got)
	}

	qcAutofix := 0
	if got := agent.ResolveQcAutofix(&qcAutofix, &execution); got != 0 {
		t.Errorf("0 表示不自动修正：%d", got)
	}
	if got := agent.ResolveQcAutofix(nil, nil); got != agent.BuiltinQcAutofix {
		t.Errorf("内置值：%d", got)
	}

	if got := agent.ResolveToolRetryBackoffMS([]int{1000, 2000}, &execution); len(got) != 2 || got[0] != 1000 {
		t.Errorf("员工配置优先：%v", got)
	}
	if got := agent.ResolveToolRetryBackoffMS(nil, &execution); len(got) != len(execution.RetryBackoffMS) {
		t.Errorf("设置默认值：%v", got)
	}
	if got := agent.ResolveToolRetryBackoffMS(nil, nil); len(got) != 3 || got[0] != 500 {
		t.Errorf("内置值：%v", got)
	}
}

func TestNormalizeThinkingLevel(t *testing.T) {
	t.Parallel()
	if got := agent.NormalizeThinkingLevel(nil); got != contract.ThinkingOff {
		t.Errorf("nil = %s", got)
	}
	bad := "turbo"
	if got := agent.NormalizeThinkingLevel(&bad); got != contract.ThinkingOff {
		t.Errorf("坏值 = %s", got)
	}
	high := contract.ThinkingHigh
	if got := agent.NormalizeThinkingLevel(&high); got != contract.ThinkingHigh {
		t.Errorf("合法值 = %s", got)
	}
}

// ---------------------------------------------------------------- 输出 Schema 校验

func TestValidator(t *testing.T) {
	t.Parallel()
	validator := agent.NewValidator(causesSchema())
	if errors := validator.Validate(validSubmission()); len(errors) != 0 {
		t.Fatalf("合法输出不应报错：%v", errors)
	}

	cases := []struct {
		value any
		want  string
	}{
		{map[string]any{}, "must have required property 'causes'"},
		{map[string]any{"causes": []any{}}, "must NOT have fewer than 1 items"},
		{map[string]any{"causes": []any{map[string]any{"anomalyId": "a", "cause": "b", "level": "nope"}}}, "must be equal to one of the allowed values"},
		{map[string]any{"causes": []any{map[string]any{"anomalyId": "a", "cause": "b", "level": "region", "extra": 1}}}, "must NOT have additional properties"},
		{map[string]any{"causes": "nope"}, "must be array"},
	}
	for _, testCase := range cases {
		errors := validator.Validate(testCase.value)
		if len(errors) == 0 {
			t.Errorf("应报错：%+v", testCase.value)
			continue
		}
		text := agent.ErrorText(errors)
		if !strings.Contains(text, testCase.want) {
			t.Errorf("%v 的错误说明 = %q，want 包含 %q", testCase.value, text, testCase.want)
		}
	}

	// 编译错误
	broken := agent.NewValidator(map[string]any{"type": "object", "properties": map[string]any{
		"a": map[string]any{"type": "string", "pattern": "["},
	}})
	if broken.CompileError() == nil {
		t.Fatal("非法 pattern 应编译失败")
	}

	// 响应能序列化成与 TS 版一致的形状
	raw, err := json.Marshal(agent.FailedAssistantMessage(agent.Model{API: "openai-completions", Provider: "scripted", ID: "scripted"}, agent.StopError, "失败"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"role":"assistant"`, `"stopReason":"error"`, `"errorMessage":"失败"`, `"api":"openai-completions"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("消息序列化缺少 %s：%s", want, raw)
		}
	}
}
