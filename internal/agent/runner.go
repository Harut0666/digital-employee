// 判断步骤执行器（docs/design.md §6）：每次 Run 新建一次对话循环，只开放白名单工具加 submit_result。
//
// 移植自 packages/core/src/agent/runner.ts。TS 版基于 pi-agent-core 的 Agent，
// Go 版用一个等价的循环实现：模型每轮要么给出工具调用，要么给出文本；
// 工具调用逐个执行（串行），结果回灌给模型；调用 submit_result 并按输出 Schema 校验通过即结束。
//
// 上限：轮次（step.limits.rounds）、步骤时长（step.limits.durationSec → settings.execution.stepTimeoutMs → 120s）、
// 任务剩余模型调用次数与任务截止时间（引擎传入的 budget）。
package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"digital-employee/internal/contract"
)

// SubmitToolName 提交结果的工具名（模型必须调用它）。
const SubmitToolName = "submit_result"

// ModelCalledEvent 一次模型调用完成的回调参数。
type ModelCalledEvent struct {
	Turn       int
	Response   any
	Usage      *contract.EventUsage
	DurationMS int64
	// Connection、Model 这次调用用的连接和模型名（剧本模型为空）
	Connection string
	Model      string
}

// ModelDeltaEvent 判断步骤模型输出的一段文字增量。
//
// 增量只给人边输出边看，不是任务状态：引擎把它推给事件流订阅者而不落盘。
type ModelDeltaEvent struct {
	// StepID 这一段属于哪个判断步骤
	StepID string
	// Turn 第几轮（同一步骤内从 0 开始）
	Turn int
	// Text 这一段文字
	Text string
}

// ToolCalledEvent 判断步骤内一次工具调用的回调参数。
type ToolCalledEvent struct {
	Tool  string
	Args  any
	OK    bool
	Error string
}

// ToolDeniedEvent 越权工具调用（被拒绝并留痕）。
type ToolDeniedEvent struct {
	Tool   string
	Reason string
}

// Budget 任务级剩余预算。
type Budget struct {
	// RemainingModelCalls 剩余可用的模型调用次数
	RemainingModelCalls int
	// Deadline 任务截止时间
	Deadline time.Time
}

// JudgmentInput 一次判断步骤执行的输入。
type JudgmentInput struct {
	TaskID       string
	Step         contract.StepConfig
	SystemPrompt string
	Context      map[string]any
	Corrections  []string
	ToolClient   contract.ToolClient
	Model        ModelProvider
	// ModelRef 任务当前的模型（会话模型），只在步骤没写死模型时使用
	ModelRef     *contract.ModelRef
	OutputSchema map[string]any
	Budget       Budget
	// Replay 断点恢复：本步骤已记录的模型响应，按顺序重放而不调用模型
	Replay []any
	// Signal 取消信号（可为 nil）
	Signal context.Context

	OnModelCalled func(ModelCalledEvent) error
	// OnModelDelta 模型输出的文字增量（Spec 0006“员工步骤在对话中的形式”）。
	// 给了就走流式调用并把每段增量交出去；nil 时保持一次性调用。
	OnModelDelta func(ModelDeltaEvent)
	OnToolCalled func(ToolCalledEvent) error
	OnToolDenied func(ToolDeniedEvent) error
}

// JudgmentRunner 判断步骤执行器。
type JudgmentRunner interface {
	Run(in JudgmentInput) (contract.JudgmentResult, error)
}

// RunnerOptions 执行器的可选装置。
type RunnerOptions struct {
	// Execution 执行与预算默认值（引擎从 settings.execution 读），每个步骤开始前调用一次
	Execution func() *contract.ExecutionSettings
}

// Runner 判断步骤执行器实现。
type Runner struct {
	options RunnerOptions
}

// NewJudgmentRunner 创建判断步骤执行器。
func NewJudgmentRunner(options RunnerOptions) *Runner { return &Runner{options: options} }

type submission struct {
	valid  bool
	output any
}

func describeContext(context map[string]any, corrections []string) string {
	parts := []string{"## 任务上下文（JSON）", "```json", MarshalJSONIndent(context), "```"}
	if len(corrections) > 0 {
		parts = append(parts, "", "## 修正要求（上一版未通过检查，请逐条处理）")
		for index, correction := range corrections {
			parts = append(parts, fmt.Sprintf("%d. %s", index+1, correction))
		}
	}
	parts = append(parts, "", fmt.Sprintf("完成后必须调用 %s 工具提交结果，不要只回复文字。", SubmitToolName))
	return strings.Join(parts, "\n")
}

// describeSeconds 把毫秒按人读的秒数写进失败说明（0.05 秒这种小数保留两位）。
func describeSeconds(timeoutMS int) string {
	seconds := float64(timeoutMS) / 1000
	if seconds == math.Trunc(seconds) {
		return strconv.FormatInt(int64(seconds), 10)
	}
	text := strconv.FormatFloat(seconds, 'f', 2, 64)
	text = strings.TrimRight(text, "0")
	text = strings.TrimRight(text, ".")
	return text
}

func toolParameters(schema any) map[string]any {
	if object, ok := schema.(map[string]any); ok {
		return object
	}
	return map[string]any{"type": "object", "additionalProperties": true}
}

func (r *Runner) execution() *contract.ExecutionSettings {
	if r.options.Execution == nil {
		return nil
	}
	return r.options.Execution()
}

// Run 执行一个判断步骤。
func (r *Runner) Run(in JudgmentInput) (contract.JudgmentResult, error) {
	execution := r.execution()
	realCalls := 0
	fail := func(kind contract.JudgmentFailure, message string) contract.JudgmentResult {
		return contract.JudgmentResult{OK: false, Failure: kind, Message: message, ModelCalls: realCalls}
	}

	// 判断步骤取模型的顺序：流程配置里给这一步写死的模型 → 任务当前的模型。
	// 任务当前的模型交给提供者；步骤写死了模型时再盖在它前面（写死的优先）。
	ref := in.Step.Model
	if in.ModelRef != nil && !IsPinned(in.Step.Model) {
		ref = in.ModelRef
	}
	resolved, err := in.Model.Resolve(in.Step.ID, ref)
	if err != nil {
		return fail(contract.FailModelError, err.Error()), nil
	}
	validator := NewValidator(in.OutputSchema)
	if compileErr := validator.CompileError(); compileErr != nil {
		return fail(contract.FailInvalidOutput, "输出 Schema 无法编译："+compileErr.Error()), nil
	}
	descriptors, err := in.ToolClient.ListTools()
	if err != nil {
		return fail(contract.FailModelError, "读取工具列表失败："+err.Error()), nil
	}

	rounds := 1
	if in.Step.Limits != nil && in.Step.Limits.Rounds != nil {
		rounds = *in.Step.Limits.Rounds
	}
	var stepDurationMS *float64
	if in.Step.Limits != nil && in.Step.Limits.DurationSec != nil {
		value := *in.Step.Limits.DurationSec * 1000
		stepDurationMS = &value
	}
	stepTimeoutMS := ResolveStepTimeoutMS(stepDurationMS, execution)
	stepDeadline := time.Now().Add(time.Duration(stepTimeoutMS) * time.Millisecond)
	taskDeadline := in.Budget.Deadline
	if taskDeadline.IsZero() {
		taskDeadline = stepDeadline
	}
	if !time.Now().Before(taskDeadline) {
		return fail(contract.FailTaskDuration, "任务总时长已到"), nil
	}
	// 步骤超时与任务截止取更早的一个
	effectiveDeadline := stepDeadline
	if taskDeadline.Before(effectiveDeadline) {
		effectiveDeadline = taskDeadline
	}
	parent := in.Signal
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithDeadline(parent, effectiveDeadline)
	defer cancel()

	// ---- 工具表与白名单 ----
	whitelist := map[string]bool{}
	for _, tool := range in.Step.Tools {
		whitelist[tool] = true
	}
	registered := map[string]bool{}
	visibleTools := []ToolSpec{}
	for _, descriptor := range descriptors {
		registered[descriptor.Name] = true
		if !whitelist[descriptor.Name] {
			continue
		}
		schema, _ := descriptor.InputSchema.(map[string]any)
		visibleTools = append(visibleTools, ToolSpec{
			Name:        descriptor.Name,
			Description: descriptor.Description,
			Parameters:  toolParameters(schema),
		})
	}
	visibleTools = append(visibleTools, ToolSpec{
		Name:        SubmitToolName,
		Description: "提交本步骤的最终结果。参数必须符合输出 Schema。提交后本步骤结束。",
		Parameters:  in.OutputSchema,
	})

	denied := map[string]bool{}
	var current *submission
	// 最近一次不合格提交的 Schema 错误。提交不合格不立刻结束步骤：把错误交还给模型，让它在轮次上限内重交；
	// 轮次用完还不合格才按 invalid_output 失败（真实模型常多写一个 Schema 没有的字段，重交一次就能过）。
	invalidErrors := ""
	var failureKind contract.JudgmentFailure
	failureMessage := ""
	modelError := ""

	messages := []Message{}
	if in.SystemPrompt != "" {
		messages = append(messages, Message{Role: "system", Content: []ContentBlock{TextBlock(in.SystemPrompt)}})
	}
	messages = append(messages, Message{
		Role:    "user",
		Content: []ContentBlock{TextBlock(describeContext(in.Context, in.Corrections))},
	})

	turns := 0
	for {
		// 提交优先：已经提交的结果不因随后的计时器或取消而作废。
		if current != nil {
			break
		}
		if failureMessage != "" {
			break
		}
		if turns >= rounds {
			if invalidErrors != "" {
				failureKind = contract.FailInvalidOutput
				failureMessage = "提交的结果不符合输出 Schema：" + invalidErrors
			} else {
				failureKind = contract.FailStepRounds
				failureMessage = fmt.Sprintf("超过本步骤的轮次上限（%d 轮）", rounds)
			}
			break
		}
		turn := turns
		turns++

		var message AssistantMessage
		if turn < len(in.Replay) {
			// 断点重放：直接使用记录的响应，不调用模型、不记预算、不再回调 onModelCalled。
			decoded, ok := DecodeAssistantMessage(in.Replay[turn])
			if !ok {
				return fail(contract.FailModelError, "断点重放失败：记录的模型响应无法解析"), nil
			}
			message = decoded
		} else {
			if realCalls >= in.Budget.RemainingModelCalls {
				failureKind = contract.FailTaskCalls
				failureMessage = fmt.Sprintf("任务的模型调用预算已用完（剩余 %d 次）", in.Budget.RemainingModelCalls)
				break
			}
			if !time.Now().Before(taskDeadline) {
				failureKind = contract.FailTaskDuration
				failureMessage = "任务总时长已到"
				break
			}
			realCalls++
			started := time.Now()
			options := StreamOptions{
				APIKey:     resolved.APIKey,
				MaxTokens:  resolved.Model.MaxTokens,
				MaxRetries: ResolveModelMaxRetries(resolved.MaxRetries, execution),
				TimeoutMS:  resolved.TimeoutMS,
			}
			produced, streamErr := requestModel(ctx, resolved, StreamRequest{
				Messages: messages,
				Tools:    visibleTools,
			}, options, in.OnModelDelta, in.Step.ID, turn)
			if streamErr != nil {
				if ctx.Err() != nil {
					failureKind, failureMessage = classifyCancel(in.Signal, taskDeadline, stepTimeoutMS)
					break
				}
				var callErr *ModelCallError
				if errors.As(streamErr, &callErr) {
					result := fail(contract.FailModelUnavailable, callErr.Message)
					result.ModelError = callErr.Kind
					return result, nil
				}
				modelError = streamErr.Error()
				break
			}
			message = *produced
			// 出错或被中止的响应不记录（重放它没有意义，恢复后应当重新请求）。
			if message.StopReason != StopError && message.StopReason != StopAborted {
				if in.OnModelCalled != nil {
					if err := in.OnModelCalled(ModelCalledEvent{
						Turn:       turn,
						Response:   message,
						Usage:      &contract.EventUsage{Input: message.Usage.Input, Output: message.Usage.Output},
						DurationMS: time.Since(started).Milliseconds(),
						Connection: resolved.Connection,
						Model:      resolved.ModelID,
					}); err != nil {
						return fail(contract.FailModelError, err.Error()), err
					}
				}
			}
		}

		messages = append(messages, Message{Role: "assistant", Content: message.Content})
		if message.StopReason == StopError {
			modelError = message.ErrorMessage
			if modelError == "" {
				modelError = "模型调用失败"
			}
			break
		}
		if message.StopReason == StopAborted {
			failureKind = contract.FailModelError
			failureMessage = "步骤已被取消"
			break
		}

		toolCalls := []ContentBlock{}
		for _, block := range message.Content {
			if block.Type == ContentToolCall {
				toolCalls = append(toolCalls, block)
			}
		}
		if len(toolCalls) == 0 {
			// 模型只回复文字：继续下一轮（把它的文字留在上下文里）
			continue
		}

		results := []ContentBlock{}
		for _, call := range toolCalls {
			if call.Name == SubmitToolName {
				arguments := call.Arguments
				if arguments == nil {
					arguments = map[string]any{}
				}
				if errs := validator.Validate(arguments); len(errs) > 0 {
					// 不合格：本次提交无效，把具体错误告诉模型，下一轮重交；同一条消息里的其他工具调用照常处理
					invalidErrors = ErrorText(errs)
					results = append(results, ToolResultBlock(call.ID,
						"提交的结果不符合输出 Schema，本次提交无效："+invalidErrors+"。请只保留 Schema 里定义的字段，修正后再次调用 "+SubmitToolName+" 提交完整结果。", true))
					continue
				} else {
					current = &submission{valid: true, output: arguments}
					results = append(results, ToolResultBlock(call.ID, "已提交", false))
				}
				// 提交后本步骤结束（terminate）
				break
			}
			if !registered[call.Name] || !whitelist[call.Name] {
				reason := fmt.Sprintf("工具 %s 未对本步骤开放，调用被拒绝。本步骤可用的工具：%s。",
					call.Name, strings.Join(append(append([]string{}, in.Step.Tools...), SubmitToolName), "、"))
				if !denied[call.ID] {
					denied[call.ID] = true
					if in.OnToolDenied != nil {
						if err := in.OnToolDenied(ToolDeniedEvent{Tool: call.Name, Reason: reason}); err != nil {
							return fail(contract.FailModelError, err.Error()), err
						}
					}
				}
				results = append(results, ToolResultBlock(call.ID, reason, true))
				continue
			}
			result, callErr := in.ToolClient.Call(call.Name, call.Arguments)
			if callErr != nil {
				message := callErr.Error()
				if in.OnToolCalled != nil {
					if err := in.OnToolCalled(ToolCalledEvent{Tool: call.Name, Args: call.Arguments, OK: false, Error: message}); err != nil {
						return fail(contract.FailModelError, err.Error()), err
					}
				}
				results = append(results, ToolResultBlock(call.ID, message, true))
				continue
			}
			if !result.OK {
				if in.OnToolCalled != nil {
					if err := in.OnToolCalled(ToolCalledEvent{Tool: call.Name, Args: call.Arguments, OK: false, Error: result.Error}); err != nil {
						return fail(contract.FailModelError, err.Error()), err
					}
				}
				results = append(results, ToolResultBlock(call.ID, result.Error, true))
				continue
			}
			if in.OnToolCalled != nil {
				if err := in.OnToolCalled(ToolCalledEvent{Tool: call.Name, Args: call.Arguments, OK: true}); err != nil {
					return fail(contract.FailModelError, err.Error()), err
				}
			}
			results = append(results, ToolResultBlock(call.ID, MarshalJSONValue(result.Data), false))
		}
		if current != nil {
			break
		}
		// OpenAI 兼容接口要求每个工具调用对应一条 tool 消息
		for _, result := range results {
			messages = append(messages, Message{Role: "tool", Content: []ContentBlock{result}})
		}
	}

	if current != nil {
		return contract.JudgmentResult{OK: true, Output: current.output, ModelCalls: realCalls}, nil
	}
	if failureMessage != "" {
		return fail(failureKind, failureMessage), nil
	}
	if modelError != "" {
		return fail(contract.FailModelError, modelError), nil
	}
	return fail(contract.FailNoOutput, "模型没有调用 "+SubmitToolName+" 提交结果"), nil
}

// classifyCancel 区分步骤超时、任务超时与外部取消。
func classifyCancel(signal context.Context, taskDeadline time.Time, stepTimeoutMS int) (contract.JudgmentFailure, string) {
	if signal != nil && signal.Err() != nil {
		return contract.FailModelError, "步骤已被取消"
	}
	if !time.Now().Before(taskDeadline) {
		return contract.FailTaskDuration, "任务总时长已到"
	}
	return contract.FailStepDuration, fmt.Sprintf("超过本步骤的时长上限（%s 秒）", describeSeconds(stepTimeoutMS))
}
