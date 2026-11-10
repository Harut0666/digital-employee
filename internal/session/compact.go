// 前台 Agent 的上下文自动压缩。
//
// 前台 Agent 每一轮都把会话里人和 Agent 说过的话发给模型，一轮之内工具结果还会累积。会话聊得久、
// 读的文件多了，就会超出模型的上下文窗口（设置 → 模型连接 → 每个模型的“上下文窗口”，缺省 128K）。
// 两道处理：
//
//  1. 每轮开始前（compactIfNeeded）：系统提示 + 对话超过可用窗口的 70%，就把较早的对话交给模型
//     压缩成一段摘要，写进会话日志（kindCompacted），最近几条原话保留。以后每一轮都从这段摘要接着发，
//     不会每轮重复压缩；再长了就把“旧摘要 + 更早的原话”再压一次。对话里留一条说明。
//  2. 一轮之中（fitContext）：工具结果累积超过可用窗口的 85%，先把较早几步的工具结果截短；
//     还不够，再丢掉最早的几句对话原文（不写日志，只影响这一次发送）。
//
// 估算按字符粗算（中文约 1 字 1 token，英文约 4 个字符 1 token），宁可早压一点。
package session

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"digital-employee/internal/agent"
)

// kindCompacted 自动压缩的摘要：Text 是摘要，CompactedThrough 是它覆盖到的最后一条消息。界面不显示。
const kindCompacted = "compacted"

const (
	// compactTrigger 每轮开始前，估算超过可用窗口的这个比例就压缩早先的对话
	compactTrigger = 0.70
	// fitTrigger 一轮之中，估算超过可用窗口的这个比例就截短较早的工具结果
	fitTrigger = 0.85
	// keepRecent 压缩时保留最近几条原话（人和 Agent 各算一条）
	keepRecent = 6
	// trimmedToolRunes 截短后的工具结果保留的字数
	trimmedToolRunes = 1500
	// defaultContextWindow 模型没有配置上下文窗口时按这个算
	defaultContextWindow = 128_000
)

const compactedNotice = "对话较长，早先的内容已自动压缩成摘要（以免超出模型的上下文窗口），最近几句原话保留。"

const droppedNotice = "这一轮要发给模型的内容太多，最早的几句对话这次没有带上。"

const trimmedSuffix = "\n…（较早的工具结果已截短以节省上下文；需要时重新调用工具查看）"

const compactPrompt = `你在为一个对话 Agent 压缩早先的对话记录，压缩结果会代替原文继续发给它。请用中文写一段摘要，必须保留：
- 发起人想做什么、提过的要求和偏好；
- 已经确认的事实、决定和结论（数字、文件路径、员工名、任务编号照原样写）；
- 已经做过的事和结果（开过哪些任务、状态如何）；
- 还没完成或还在等的事。
不要编造原文里没有的内容，不要加评价。只输出摘要正文，不要加标题或客套话。`

// summaryHeader 摘要作为对话第一条发给模型时的抬头。
const summaryHeader = "【早先对话的摘要（系统自动压缩，原文较长未全部附上）】\n"

// historyEntry 发给模型的一条原话，带着它在会话日志里的 ID（压缩时要记“覆盖到哪一条”）。
type historyEntry struct {
	id      string
	message agent.Message
}

// historyEntries 最近一次压缩的摘要，以及它之后的原话。没压缩过时 summary 为空、entries 是全部原话。
func (t *turn) historyEntries() (string, []historyEntry) {
	messages := t.current.messages
	summary, start := "", 0
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Kind != kindCompacted {
			continue
		}
		for j := 0; j < index; j++ {
			if messages[j].ID == messages[index].CompactedThrough {
				summary, start = messages[index].Text, j+1
				break
			}
		}
		break
	}
	entries := []historyEntry{}
	for _, message := range messages[start:] {
		switch message.Kind {
		case KindUser:
			entries = append(entries, historyEntry{id: message.ID, message: textMessage("user", message.Text)})
		case KindAssistant:
			entries = append(entries, historyEntry{id: message.ID, message: textMessage("assistant", message.Text)})
		}
	}
	return summary, entries
}

// conversationOf 摘要（若有）加原话，就是发给模型的对话。
func conversationOf(summary string, entries []historyEntry) []agent.Message {
	messages := make([]agent.Message, 0, len(entries)+1)
	if summary != "" {
		messages = append(messages, textMessage("user", summaryHeader+summary))
	}
	for _, entry := range entries {
		messages = append(messages, entry.message)
	}
	return messages
}

// estimateTokens 粗估一段文字的 token 数：非 ASCII 字符按 1 个，ASCII 每 4 个字符 1 个。
func estimateTokens(text string) int {
	ascii, other := 0, 0
	for _, r := range text {
		if r < 128 {
			ascii++
		} else {
			other++
		}
	}
	return other + (ascii+3)/4
}

// estimateMessages 粗估一组消息的 token 数（每条另加少量格式开销）。
func estimateMessages(messages []agent.Message) int {
	total := 0
	for _, message := range messages {
		total += 4
		for _, block := range message.Content {
			total += estimateTokens(block.Text)
			if len(block.Arguments) > 0 {
				if raw, err := json.Marshal(block.Arguments); err == nil {
					total += estimateTokens(string(raw))
				}
			}
		}
	}
	return total
}

// availableTokens 能放进上下文的输入量：上下文窗口减去输出上限（至少留一半窗口给输入）。
func availableTokens(model agent.Model) int {
	window := model.ContextWindow
	if window <= 0 {
		window = defaultContextWindow
	}
	available := window - model.MaxTokens
	if available < window/2 {
		available = window / 2
	}
	return available
}

// compactIfNeeded 每轮开始前检查：太长就把较早的对话压缩成摘要写进日志。压缩失败不影响这一轮
// （一轮之中的 fitContext 还会兜底），所以这里的错误都不往外抛。
func (loop *turnLoop) compactIfNeeded(ctx context.Context, system string) {
	resolved, reason := loop.lookupModel()
	if reason != "" {
		return // 模型不可用：交给 converse 去报错
	}
	summary, entries := loop.turn.historyEntries()
	available := availableTokens(resolved.Model)
	if estimateTokens(system)+estimateMessages(conversationOf(summary, entries)) <= int(float64(available)*compactTrigger) {
		return
	}
	if len(entries) <= keepRecent {
		return
	}
	old := entries[:len(entries)-keepRecent]
	text, err := loop.summarize(ctx, resolved, transcriptOf(summary, old, available/2))
	if err != nil || strings.TrimSpace(text) == "" {
		return
	}
	loop.say(Message{Kind: kindCompacted, Text: strings.TrimSpace(text), CompactedThrough: old[len(old)-1].id})
	loop.say(Message{Kind: KindNotice, Text: compactedNotice})
}

// transcriptOf 要压缩的那段对话的文字稿：旧摘要在前，原话在后。太长时保留后面的部分（越近越重要）。
func transcriptOf(summary string, entries []historyEntry, limit int) string {
	lines := []string{}
	if summary != "" {
		lines = append(lines, "（更早之前的摘要）"+summary)
	}
	for _, entry := range entries {
		speaker := "发起人"
		if entry.message.Role == "assistant" {
			speaker = "Agent"
		}
		text := ""
		for _, block := range entry.message.Content {
			text += block.Text
		}
		lines = append(lines, speaker+"："+text)
	}
	for len(lines) > 1 && estimateTokens(strings.Join(lines, "\n")) > limit {
		lines = lines[1:]
	}
	return strings.Join(lines, "\n")
}

// summarize 让模型写摘要：不带工具、不推送增量。
func (loop *turnLoop) summarize(ctx context.Context, resolved *agent.ResolvedStepModel, transcript string) (string, error) {
	messages := []agent.Message{
		{Role: "system", Content: []agent.ContentBlock{agent.TextBlock(compactPrompt)}},
		textMessage("user", transcript),
	}
	produced, err := resolved.EventStream(ctx, resolved.Model, agent.StreamRequest{Messages: messages}, agent.EventStreamOptions{StreamOptions: loop.streamOptions(resolved)})
	if err != nil {
		return "", err
	}
	if produced == nil || produced.StopReason == agent.StopError || produced.StopReason == agent.StopAborted {
		if produced != nil && produced.ErrorMessage != "" {
			return "", errors.New(produced.ErrorMessage)
		}
		return "", errors.New("摘要没有生成")
	}
	text := strings.Builder{}
	for _, block := range produced.Content {
		if block.Type == agent.ContentText {
			text.WriteString(block.Text)
		}
	}
	return text.String(), nil
}

// fitContext 一轮之中发给模型之前检查：超过可用窗口的 85% 时，先截短较早几步的工具结果
// （最近一次模型回复之后的那批结果保留原样），还不够再丢掉最早的几句对话原文。
// historyEnd 是对话原文在 messages 里的结束位置（messages[1:historyEnd] 是原文，[0] 是系统提示）。
// 返回处理后的消息、新的 historyEnd，以及丢掉了几句原文。
func fitContext(messages []agent.Message, historyEnd, available int) ([]agent.Message, int, int) {
	limit := int(float64(available) * fitTrigger)
	if estimateMessages(messages) <= limit {
		return messages, historyEnd, 0
	}
	// 最近一次模型回复的位置：它之后的工具结果是模型马上要看的，不截
	lastAssistant := -1
	for index := len(messages) - 1; index >= historyEnd; index-- {
		if messages[index].Role == "assistant" {
			lastAssistant = index
			break
		}
	}
	out := make([]agent.Message, len(messages))
	copy(out, messages)
	for index := historyEnd; index < lastAssistant && estimateMessages(out) > limit; index++ {
		if out[index].Role != "tool" {
			continue
		}
		blocks := make([]agent.ContentBlock, len(out[index].Content))
		copy(blocks, out[index].Content)
		for b := range blocks {
			if runes := []rune(blocks[b].Text); len(runes) > trimmedToolRunes {
				blocks[b].Text = string(runes[:trimmedToolRunes]) + trimmedSuffix
			}
		}
		out[index] = agent.Message{Role: out[index].Role, Content: blocks}
	}
	// 还超：丢掉最早的对话原文，至少留下最后一句（通常是人这一轮说的话）
	dropped := 0
	for historyEnd-dropped > 2 && estimateMessages(out) > limit {
		out = append(out[:1], out[2:]...)
		dropped++
	}
	return out, historyEnd - dropped, dropped
}
