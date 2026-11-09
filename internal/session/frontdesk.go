// 前台 Agent 的多轮循环。
//
// 它和判断步骤共用模型提供者和剧本模型，但没有一次性提交：模型每轮要么调工具，要么说话，
// 说话就结束这条回复。工具轮次有上限，对话不占任务预算。
//
// 说的是流式调用（Spec 0006）：文字增量边到边推给会话事件流，不落盘；一轮结束时写完整消息。
// 收到停止时取消模型调用：已输出的部分以“已中断”保存。运行中收到的新消息在下一步之前送达。
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/settings"
)

// MaxToolRounds 单条回复的工具轮次上限，超过就停下说明。
const MaxToolRounds = 20

const roundLimitText = "这条回复调用工具的次数已到上限，我先停在这里。请换个说法，或直接在卡片上操作。"

// taskReader Agent 能看到的全部任务接口：只读。工具里拿不到 Engine，也就发不出任务命令。
type taskReader interface {
	Get(taskID contract.TaskID) (*contract.TaskSnapshot, bool)
}

// errModelUnavailable 模型调用失败，说明已写入会话日志。
var errModelUnavailable = errors.New("模型不可用")

// toolError 带错误码的工具失败：会话日志里 result 记 {error, message}，模型收到的是 message。
type toolError struct{ code, message string }

func (e *toolError) Error() string { return e.message }

func toolFailure(code, format string, args ...any) error {
	return &toolError{code: code, message: fmt.Sprintf(format, args...)}
}

// denial 越权调用：拒绝，并写入会话日志。
type denial struct{ reason string }

func (d *denial) Error() string { return d.reason }

func denied(format string, args ...any) error {
	return &denial{reason: fmt.Sprintf(format, args...)}
}

// tool 这一轮开放给模型的一个工具。
type tool struct {
	spec agent.ToolSpec
	run  func(arguments map[string]any) (any, error)
	// runCtx 需要等人（员工表单卡）的工具用它代替 run：等待期间要响应停止
	runCtx func(ctx context.Context, arguments map[string]any) (any, error)
	// action 动作类别（ActionWriteFile 等）：非空的工具执行前要过权限关口（turnLoop.gate）；空表示读，始终允许
	action string
	// selfDecide 工具自己处理“放行”和“问”：关口只负责计划模式的拒绝。
	// 运行员工的“问”是员工表单卡，使用目录文件的“问”是“使用目录文件”卡，都不再叠一张权限确认卡。
	selfDecide bool
	// guard 始终适用的边界检查（路径越界、隐藏文件、大小……），不受权限模式影响；返回的错误就是拒绝原因
	guard func(arguments map[string]any) error
	// describe 动作的对象（文件路径）和一句话说明，写在权限确认卡上
	describe func(arguments map[string]any) (object, detail string)
}

// turn 一条回复的处理过程。
type turn struct {
	manager *Manager
	current *record
	role    contract.Role
	tasks   taskReader
	// out 这条回复写入的全部消息
	out []Message
	// cards 回复说完之后才发出的卡片
	cards []Card
}

func (t *turn) write(message Message) error {
	written, err := t.manager.store.append(t.current.id, message)
	if err != nil {
		return err
	}
	t.current.apply(written)
	t.out = append(t.out, written)
	return nil
}

// flushCards 发出排队的卡片。
func (t *turn) flushCards() error {
	for index := range t.cards {
		if err := t.write(Message{Kind: KindCard, Card: &t.cards[index]}); err != nil {
			return err
		}
	}
	t.cards = nil
	return nil
}

// history 发给模型的对话：只有人和 Agent 说的话，卡片和留痕是界面消息。
func (t *turn) history() []agent.Message {
	messages := []agent.Message{}
	for _, message := range t.current.messages {
		switch message.Kind {
		case KindUser:
			messages = append(messages, textMessage("user", message.Text))
		case KindAssistant:
			messages = append(messages, textMessage("assistant", message.Text))
		}
	}
	return messages
}

func textMessage(role, text string) agent.Message {
	return agent.Message{Role: role, Content: []agent.ContentBlock{agent.TextBlock(text)}}
}

// ---------------------------------------------------------------- 一轮回复

// turnLoop 一轮回复的过程：这一轮开着的时候一直拿着会话的写锁。
type turnLoop struct {
	job  *turnJob
	bus  *bus
	turn *turn
}

func newTurnLoop(job *turnJob) *turnLoop {
	return &turnLoop{job: job, bus: job.manager.bus(job.session)}
}

// run 跑这一轮，直到说完、停止或出错。
func (loop *turnLoop) run(ctx context.Context) (turnOutcome, error) {
	deps := loop.job.manager
	// 整轮占住写锁（包括中间的模型调用）：期间人的操作被拒绝（见 begin，返回 session_busy），
	// 发消息不受影响（走 beginUnlocked 排成插话）；这一轮自己写日志是重入，不受影响。
	lock := deps.lockOf(loop.job.session)
	lock.Lock(background)
	defer lock.Unlock(background)
	current, err := deps.load(loop.job.session)
	if err != nil {
		return turnOutcome{mode: turnErrored, reason: TurnError}, err
	}
	t := &turn{manager: deps, current: current, role: speakerRole(current, loop.job.role), tasks: deps.deps.Engine}
	loop.turn = t
	return loop.assist(ctx), nil
}

// turnStart 这一轮登记时会话日志写到第几条。
func (m *Manager) turnStart(job *turnJob) int64 {
	job.mu.Lock()
	defer job.mu.Unlock()
	return job.floor
}

// interjections 这一轮里还要送达的插话：日志里有人说过、但还没送进过模型请求的话。
func (loop *turnLoop) interjections() []agent.Message {
	if loop.job == nil || loop.turn == nil || loop.turn.current == nil {
		return nil
	}
	job := loop.job
	job.mu.Lock()
	defer job.mu.Unlock()
	if job.steer <= job.included {
		return nil
	}
	// 标记到“这一轮实际送出的条数”：开轮之前多记的一条（上一轮尾声才到、其实已经被带走）在这里清掉
	messages := []agent.Message{}
	for _, message := range loop.turn.current.messages {
		if message.Kind == KindUser && !job.delivered[message.Seq] {
			job.delivered[message.Seq] = true
			messages = append(messages, textMessage("user", message.Text))

		}
	}

	if len(messages) == 0 {
		// 没有真正要送的话，不记这次送达
		return nil
	}
	job.included = job.steer
	return messages
}

// write 写进会话日志并推送；一轮里的消息都带着轮次编号。
func (loop *turnLoop) write(message Message) error {
	// 不属于某一轮的一次性调用（比如起草入库描述）只落日志：没有轮次、没有订阅者，也就没有额外的话要说
	if loop.job == nil || loop.bus == nil {

		return loop.turn.write(message)
	}
	message.Turn = loop.job.id

	if err := loop.turn.write(message); err != nil {
		return err
	}

	written := loop.turn.out[len(loop.turn.out)-1]

	// write 用的是回放出来的那份状态；取最新的一份记下这一条，后面读它才看得见
	if current, err := loop.job.manager.load(loop.job.session); err == nil {
		loop.turn.current = current
	}
	loop.bus.publish(AgentEvent{Turn: loop.job.id, Type: EventMessage, Msg: &written})
	if written.Card != nil {
		loop.bus.card(written)
	}
	return nil
}

// appendDelta 在写锁里把一段文字增量并进这一轮的那一条（单独更新一行，不经过 write）。
func (loop *turnLoop) appendDelta(text string) error {
	t := loop.turn
	if len(t.current.messages) > 0 {
		last := len(t.current.messages) - 1
		if entry := &t.current.messages[last]; entry.Kind == KindTurnText && entry.Turn == loop.job.id {
			updated, err := loop.job.manager.store.update(t.current.id, entry.Seq, func(message *Message) {
				message.Text += text
			})
			if err == nil {
				t.current.messages[last] = updated
			}
			return nil
		}
	}
	return loop.write(Message{Kind: KindTurnText, Text: text})
}

// say 说一句程序写的话（模型不可用、达到上限、已中断）。
func (loop *turnLoop) say(message Message) {
	_ = loop.write(message)
}

// flushCards 发出排队的卡片。
func (loop *turnLoop) flushCards() error {

	for index := range loop.turn.cards {
		if err := loop.write(Message{Kind: KindCard, Card: &loop.turn.cards[index]}); err != nil {
			return err
		}
	}
	loop.turn.cards = nil
	return nil
}

// resolveModel 取前台 Agent 的模型；失败写入会话日志。
func (loop *turnLoop) resolveModel() (*agent.ResolvedStepModel, error) {
	fail := func(reason string) (*agent.ResolvedStepModel, error) {
		loop.say(Message{Kind: KindModelFailed, Reason: reason})
		return nil, errModelUnavailable
	}
	var provider agent.ModelProvider
	if loop.turn.manager.deps.Model != nil {
		provider = loop.turn.manager.deps.Model()
	}
	if provider == nil {
		return fail("没有可用的模型提供者")
	}
	// 前台 Agent 也用会话模型（Spec 0006）；绑定未结束的任务时是任务当前的模型，
	// 任务结束后是会话的选择（Spec 0007）
	t := loop.turn
	model := t.manager.taskModel(t.current)
	if model.Connection != "" && model.Model != "" {
		if connection, found := settings.FindConnection(t.manager.effectiveModelSettings().Connections, model.Connection); found && connection.APIKey == "" {
			return fail("模型连接「" + model.Connection + "」没有配置 API Key")
		}
	}
	resolved, err := provider.Resolve(agent.FrontdeskStepID, &model)
	if err != nil {
		return fail(err.Error())
	}
	if provider.Kind() == "real" && resolved.APIKey == "" {
		return fail("模型连接没有配置 API Key")
	}
	return resolved, nil
}

// stream 一次流式模型调用；文字增量直接推送，停止之后返回的结尾由调用方处理。
func (loop *turnLoop) stream(ctx context.Context, resolved *agent.ResolvedStepModel, messages []agent.Message, tools []agent.ToolSpec) (*agent.AssistantMessage, error) {
	var execution *contract.ExecutionSettings
	if loop.turn.manager.deps.Execution != nil {
		execution = loop.turn.manager.deps.Execution()
	}
	send := func(text string) {
		if text == "" {
			return
		}
		// 不属于某一轮的一次性调用（比如起草入库描述）：只交给调用方，不推送、不记账
		if loop.job == nil || loop.bus == nil {
			return
		}
		loop.bus.textDelta(text)
		loop.bus.publish(AgentEvent{Turn: loop.job.id, Type: EventText, Text: text})
		// 增量按轮次记一份（只更新日志里的那一行，不单独推送）。
		// 增量不是一条对话消息：中途派生出的写入不会因此排到卡片前面
		_ = loop.appendDelta(text)
	}
	return resolved.EventStream(ctx, resolved.Model, agent.StreamRequest{Messages: messages, Tools: tools}, agent.EventStreamOptions{
		StreamOptions: agent.StreamOptions{
			APIKey:     resolved.APIKey,
			MaxTokens:  resolved.Model.MaxTokens,
			MaxRetries: agent.ResolveModelMaxRetries(resolved.MaxRetries, execution),
			TimeoutMS:  resolved.TimeoutMS,
		},
		OnText: send,
	})
}

// converse 跑一条回复，返回模型最后说的话。
func (loop *turnLoop) converse(ctx context.Context, system string, conversation []agent.Message, tools []tool) (string, string, error) {
	resolved, err := loop.resolveModel()
	if err != nil {
		return "", TurnError, err
	}
	specs := make([]agent.ToolSpec, 0, len(tools))
	open := map[string]tool{}
	for _, item := range tools {
		specs = append(specs, item.spec)
		open[item.spec.Name] = item
	}
	messages := append([]agent.Message{{Role: "system", Content: []agent.ContentBlock{agent.TextBlock(system)}}}, conversation...)
	// 这一轮开始时排着的插话（比如接手后的“据此填表”）在第一步之前送达。
	// 不属于某一轮的一次性调用（比如起草入库描述）没有插话。
	if loop.job != nil {

		current, loadErr := loop.job.manager.load(loop.job.session)
		if loadErr != nil {
			return "", TurnError, loadErr
		}
		loop.turn.current = current
		messages = append(messages, loop.interjections()...)

	}
	// 这一轮已经说出的部分（包括调用工具之前的那半句），停止或断流时都要保留
	spoken := strings.Builder{}
	for rounds := 0; ; rounds++ {

		produced, err := loop.stream(ctx, resolved, messages, specs)
		if produced != nil {

			spoken.WriteString(agent.PartialText(produced))
		}
		if err == nil && produced.StopReason == agent.StopError {
			// 回复中途断开：已经说出的部分照实写下来，再说明断在哪里
			err = errors.New(produced.ErrorMessage)
		}
		if ctx.Err() != nil || (produced != nil && produced.StopReason == agent.StopAborted) {

			return interruptedReply(spoken.String()), TurnStopped, nil
		}
		if err != nil {
			loop.say(Message{Kind: KindModelFailed, Reason: agent.RedactAPIKey(err.Error(), resolved.APIKey)})
			if hint := agent.StreamErrorHint(produced); hint != "" {
				loop.say(Message{Kind: KindNotice, Text: hint})
			}
			return "", TurnError, errModelUnavailable
		}

		text := strings.Builder{}
		calls := []agent.ContentBlock{}
		for _, block := range produced.Content {
			switch block.Type {
			case agent.ContentText:
				text.WriteString(block.Text)
			case agent.ContentToolCall:
				calls = append(calls, block)
			}
		}

		if len(calls) == 0 {
			return strings.TrimSpace(text.String()), TurnCompleted, nil
		}
		if rounds >= MaxToolRounds {
			return roundLimitText, TurnRoundLimit, nil
		}

		messages = append(messages, agent.Message{Role: "assistant", Content: produced.Content})

		for index, call := range calls {
			result, err := loop.call(ctx, open, call)
			if err != nil {
				return "", TurnError, err
			}
			messages = append(messages, agent.Message{Role: "tool", Content: []agent.ContentBlock{result}})

			if ctx.Err() != nil {
				// 停止：还没开始的工具记“已取消”；已经说出的话保留下来（补一句“已中断”）

				// 当前这一次还没有结果时也算“已取消”（它可能其实已经执行过，结果不再采用）
				rest := append([]agent.ContentBlock{call}, calls[index+1:]...)
				loop.cancelCalls(rest)
				return interruptedReply(spoken.String()), TurnStopped, nil
			}
		}

		// 下一步之前送达运行中收到的新消息
		interj := loop.interjections()

		messages = append(messages, interj...)
	}
}

// interruptedReply 停止时返回已经说出的部分（补一句“已中断”）。
func interruptedReply(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return text + agent.InterruptedSuffix
}

// cancelCalls 没开始的工具调用记一条“已取消”的结果：会话里与模型拿到的都是这一条。
func (loop *turnLoop) cancelCalls(calls []agent.ContentBlock) {
	for _, call := range calls {
		loop.say(Message{Kind: KindToolCalled, Tool: call.Name, Args: call.Arguments, Result: cancelledResult})
	}
}

// cancelledResult 没开始的工具调用的结果。
const cancelledResult = "已取消：这一轮被停止，工具没有执行。"

// call 执行一次工具调用，写入留痕，返回交给模型的结果。
func (loop *turnLoop) call(ctx context.Context, open map[string]tool, call agent.ContentBlock) (agent.ContentBlock, error) {
	publish := func(event AgentEvent) {
		event.Turn = loop.job.id
		event.ID, event.Tool, event.Args = call.ID, call.Name, call.Arguments
		loop.bus.publish(event)
	}

	publish(AgentEvent{Type: EventToolStart})
	loop.bus.toolStarted(AgentTool{ID: call.ID, Tool: call.Name, Args: call.Arguments})
	end := func(result any, reason string, isErr bool) {
		loop.bus.toolEnded(call.ID, result, reason, isErr)
		publish(AgentEvent{Type: EventToolEnd, Result: result, Reason: reason, IsErr: isErr})
	}

	item, ok := open[call.Name]
	if !ok {
		names := make([]string, 0, len(open))
		for name := range open {
			names = append(names, name)
		}
		sort.Strings(names)
		reason := fmt.Sprintf("工具 %s 现在没有开放给前台 Agent。现在可用的工具：%s。", call.Name, strings.Join(names, "、"))
		end(nil, reason, true)
		return loop.deny(call, reason)
	}
	// 工具在写锁里执行、并当场写留痕：它可能改会话状态（草稿、待发的卡片），执行和留痕要看到同一份现状，
	// 也不能和人的提交、确认交错。
	var (
		result   any
		runErr   error
		refused  *denial
		writeErr error
	)

	// 工具执行前的同一个关口：始终适用的边界 → 权限模式（放行、询问、拒绝）
	refusal, cancelled, gateErr := loop.gate(ctx, call, item)
	switch {
	case gateErr != nil:
		return agent.ContentBlock{}, gateErr
	case cancelled:
		// 等权限确认时被停止：动作没有执行；留痕“已取消”由调用方统一补写
		end(nil, cancelledResult, true)
		return agent.ToolResultBlock(call.ID, cancelledResult, true), nil
	case refusal != "":
		runErr = &denial{reason: refusal}
	default:
		if item.runCtx != nil {
			result, runErr = item.runCtx(ctx, call.Arguments)
		} else {
			result, runErr = item.run(call.Arguments)
		}
		if errors.Is(runErr, errTurnCancelled) {
			// 等人的时候被停止：动作没有执行；留痕“已取消”由调用方统一补写
			end(nil, cancelledResult, true)
			return agent.ToolResultBlock(call.ID, cancelledResult, true), nil
		}
	}

	// 执行完写留痕（工具可能改了草稿、待发的卡片，留痕紧跟着写下）
	switch {
	case errors.As(runErr, &refused):
		_, writeErr = loop.deny(call, refused.reason)
	case runErr != nil:
		failed := Message{Kind: KindToolCalled, Tool: call.Name, Args: call.Arguments, Reason: runErr.Error()}
		var coded *toolError
		if errors.As(runErr, &coded) {
			failed.Result = map[string]any{"error": coded.code, "message": coded.message}
		}
		writeErr = loop.write(failed)
	default:
		writeErr = loop.write(Message{Kind: KindToolCalled, Tool: call.Name, Args: call.Arguments, Result: result})
	}
	switch {
	case refused != nil:
		end(nil, refused.reason, true)
		if writeErr != nil {
			return agent.ContentBlock{}, writeErr
		}
		return agent.ToolResultBlock(call.ID, refused.reason, true), nil
	case runErr != nil:
		end(nil, runErr.Error(), true)
		if writeErr != nil {
			return agent.ContentBlock{}, writeErr
		}
		return agent.ToolResultBlock(call.ID, runErr.Error(), true), nil
	case writeErr != nil:
		return agent.ContentBlock{}, writeErr
	}
	end(result, "", false)
	raw, err := json.Marshal(result)
	if err != nil {
		return agent.ContentBlock{}, err
	}
	return agent.ToolResultBlock(call.ID, string(raw), false), nil
}

func (loop *turnLoop) deny(call agent.ContentBlock, reason string) (agent.ContentBlock, error) {
	if err := loop.write(Message{Kind: KindToolDenied, Tool: call.Name, Args: call.Arguments, Reason: reason}); err != nil {
		return agent.ContentBlock{}, err
	}
	return agent.ToolResultBlock(call.ID, reason, true), nil
}

// denyLocked 在写锁里写越权留痕。
func (loop *turnLoop) denyLocked(call agent.ContentBlock, reason string) error {
	return loop.write(Message{Kind: KindToolDenied, Tool: call.Name, Args: call.Arguments, Reason: reason})
}
