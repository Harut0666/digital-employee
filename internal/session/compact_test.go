package session

import (
	"context"
	"strings"
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

// compactFixture 一个接了剧本模型的会话，用来跑压缩。
func compactFixture(t *testing.T, script agent.ModelScript) (*Manager, *turnLoop, *agent.ScriptedProvider) {
	t.Helper()
	provider := agent.NewScriptedModelProvider(script, agent.ScriptedProviderOptions{})
	manager := New(Deps{Dir: t.TempDir(), Workspace: func(string) bool { return true }, Model: func() agent.ModelProvider { return provider }})
	view, err := manager.Create("ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	current, err := manager.load(view.ID)
	if err != nil {
		t.Fatal(err)
	}
	job := &turnJob{manager: manager, session: view.ID, id: 1, role: contract.RoleInitiator, delivered: map[int64]bool{}}
	loop := &turnLoop{job: job, bus: manager.bus(view.ID), turn: &turn{manager: manager, current: current, role: contract.RoleInitiator}}
	return manager, loop, provider
}

// say 往会话里写几句很长的对话：人和 Agent 交替。
func sayLong(t *testing.T, loop *turnLoop, count, runes int) []string {
	t.Helper()
	ids := []string{}
	for index := 0; index < count; index++ {
		kind := KindUser
		if index%2 == 1 {
			kind = KindAssistant
		}
		if err := loop.turn.write(Message{Kind: kind, Text: strings.Repeat("周", runes)}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, loop.turn.current.messages[len(loop.turn.current.messages)-1].ID)
	}
	return ids
}

func TestEstimateTokens(t *testing.T) {
	if got := estimateTokens("周报"); got != 2 {
		t.Fatalf("中文按 1 字 1 token：%d", got)
	}
	if got := estimateTokens("abcdefgh"); got != 2 {
		t.Fatalf("英文按 4 字符 1 token：%d", got)
	}
}

// 超过可用窗口的 70%：较早的对话压成摘要写进日志，最近 6 句原话保留；之后发给模型的是“摘要 + 最近原话”。
func TestCompactSummarizesOlderTurnsWhenLong(t *testing.T) {
	summary := "发起人要出第 44 周的周报，材料在工作区的销售明细.xlsx。"
	_, loop, provider := compactFixture(t, agent.ModelScript{agent.FrontdeskStepID: {{Text: &summary}}})
	ids := sayLong(t, loop, 12, 8000) // 约 9.6 万 token，剧本模型窗口 128K、输出 8K

	loop.compactIfNeeded(context.Background(), "系统提示")

	var compacted, notice *Message
	for index := range loop.turn.current.messages {
		message := &loop.turn.current.messages[index]
		switch {
		case message.Kind == kindCompacted:
			compacted = message
		case message.Kind == KindNotice && message.Text == compactedNotice:
			notice = message
		}
	}
	if compacted == nil || notice == nil {
		t.Fatalf("应写入摘要和说明：compacted=%v notice=%v", compacted, notice)
	}
	if compacted.Text != summary || compacted.CompactedThrough != ids[5] {
		t.Fatalf("摘要 = %q，覆盖到 %q，want 覆盖到第 6 句 %q", compacted.Text, compacted.CompactedThrough, ids[5])
	}
	// 写摘要的那次调用：发给模型的是文字稿，不带工具
	requests := provider.Requests()
	if len(requests) != 1 || len(requests[0].Request.Tools) != 0 || !strings.Contains(requests[0].Request.Messages[1].Content[0].Text, "发起人：") {
		t.Fatalf("写摘要的请求不对：%+v", requests)
	}

	history := loop.turn.history()
	if len(history) != 7 || !strings.HasPrefix(history[0].Content[0].Text, summaryHeader) || !strings.Contains(history[0].Content[0].Text, summary) {
		t.Fatalf("压缩后的对话应是摘要 + 最近 6 句：%d 条，首条 %q", len(history), history[0].Content[0].Text[:40])
	}

	// 下一轮不再重复压缩：摘要之后只剩 6 句，远低于阈值
	loop.compactIfNeeded(context.Background(), "系统提示")
	if got := len(provider.Requests()); got != 1 {
		t.Fatalf("不该再压缩一次：请求数 %d", got)
	}
}

// 没超过阈值时什么都不做，也不调用模型。
func TestCompactLeavesShortConversationAlone(t *testing.T) {
	_, loop, provider := compactFixture(t, agent.ModelScript{})
	sayLong(t, loop, 4, 200)
	loop.compactIfNeeded(context.Background(), "系统提示")
	if len(provider.Requests()) != 0 {
		t.Fatal("短对话不该调用模型")
	}
	for _, message := range loop.turn.current.messages {
		if message.Kind == kindCompacted {
			t.Fatal("短对话不该写摘要")
		}
	}
}

// 一轮之中：先截短较早几步的工具结果，最近一批保留原样；还不够再丢最早的原文，至少留最后一句。
func TestFitContextTrimsOldToolResultsFirst(t *testing.T) {
	big := strings.Repeat("数", 20000)
	messages := []agent.Message{
		{Role: "system", Content: []agent.ContentBlock{agent.TextBlock("系统")}},
		textMessage("user", "帮我看看这些文件"),
		{Role: "assistant", Content: []agent.ContentBlock{agent.ToolCallBlock("c1", "read_file", nil)}},
		{Role: "tool", Content: []agent.ContentBlock{agent.ToolResultBlock("c1", big, false)}},
		{Role: "assistant", Content: []agent.ContentBlock{agent.ToolCallBlock("c2", "read_file", nil)}},
		{Role: "tool", Content: []agent.ContentBlock{agent.ToolResultBlock("c2", big, false)}},
	}
	out, end, dropped := fitContext(messages, 2, 30000) // 阈值 25500，原来约 4 万
	if dropped != 0 || end != 2 {
		t.Fatalf("截短工具结果就够了，不该丢原文：dropped=%d end=%d", dropped, end)
	}
	if !strings.HasSuffix(out[3].Content[0].Text, trimmedSuffix) {
		t.Fatal("较早的工具结果应被截短")
	}
	if out[5].Content[0].Text != big {
		t.Fatal("最近一次回复之后的工具结果应保留原样")
	}
	if messages[3].Content[0].Text != big {
		t.Fatal("不能改动传入的消息")
	}

	// 原文本身就太长：丢最早的原文，但至少留一句
	long := []agent.Message{
		{Role: "system", Content: []agent.ContentBlock{agent.TextBlock("系统")}},
		textMessage("user", big), textMessage("assistant", big), textMessage("user", "最后一句"),
	}
	out, end, dropped = fitContext(long, 4, 10000)
	if dropped != 2 || end != 2 || out[1].Content[0].Text != "最后一句" {
		t.Fatalf("应丢掉最早两句、留下最后一句：dropped=%d end=%d", dropped, end)
	}
}
