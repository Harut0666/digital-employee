// 知识库问答员工：路由在两个员工之间提议，问答员工只有一个判断步骤、只开放知识库工具（Spec 0005“员工”“知识库”）。
package server_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

const qaEmployee = "knowledge-qa"

func TestRoutingProposesTheQAEmployeeWhoAnswersFromTheKnowledgeBase(t *testing.T) {
	script := caseScript(t, "A-01")
	h := newHarness(t, script, withEmployee(qaEmployee))
	filed := h.fileDelivered(h.deliveredReport().ID, map[string]any{"description": "第 44 周经营周报，华东、华南两个异常区域。", "tags": []string{"周报"}})
	// 条目 ID 入库后才知道：前台 Agent 的剧本这时再写
	script[agent.FrontdeskStepID] = []agent.ScriptedTurn{
		modelCalls("propose_employee", map[string]any{"employee": qaEmployee, "reason": "要查以前的周报"}),
		modelSays("这是查询历史结论，适合知识库问答员工。"),
		modelCalls(contract.ToolKBSearch, map[string]any{"query": "周报 44"}),
		modelCalls(contract.ToolKBRead, map[string]any{"id": filed.ID}),
		modelSays("第 44 周周报提到杭州湖滨店因商场消防改造停业三天。"),
	}
	created := h.createSession()

	proposed := h.say(created.ID, initiator, "上周周报里杭州湖滨店怎么了")

	card := cardOf(t, proposed.Messages, session.CardEmployeeConfirm)
	if card.Proposed != qaEmployee || card.Source != session.SourceModel {
		t.Fatalf("员工确认卡片 = %+v", card)
	}
	names := []string{}
	for _, candidate := range card.Candidates {
		names = append(names, candidate.Name)
	}
	if fmt.Sprint(names) != "[知识库问答员工 经营周报员工]" && fmt.Sprint(names) != "[经营周报员工 知识库问答员工]" {
		t.Fatalf("候选员工：%v", names)
	}

	answered := h.confirm(created.ID, initiator, qaEmployee)

	if got := textOf(t, answered.Messages, session.KindAssistant); !strings.Contains(got, "停业三天") {
		t.Fatalf("回答 = %q", got)
	}
	handover := frontdeskRequests(h)[2].Request
	if names := fmt.Sprint(toolNames(handover.Tools)); names != "[fill_form_draft propose_card list_files read_file use_folder_file kb_search kb_read]" {
		t.Fatalf("问答员工的前台 Agent 开放的工具：%s", names)
	}
	system := handover.Messages[0].Content[0].Text
	if !strings.Contains(system, "知识库问答员工") || !strings.Contains(system, "背景参考") {
		t.Fatalf("接手后的系统提示词：%s", system)
	}
	called := []string{}
	for _, call := range entriesOf(h.readSession(created.ID).Messages, session.KindToolCalled) {
		called = append(called, call.Tool)
	}
	if fmt.Sprint(called) != "[propose_employee kb_search kb_read]" {
		t.Fatalf("会话日志里的工具调用：%v", called)
	}
}

func TestChineseSentenceSearchFindsTheEntry(t *testing.T) {
	script := caseScript(t, "A-01")
	h := newHarness(t, script, withEmployee(qaEmployee))
	filed := h.fileDelivered(h.deliveredReport().ID, map[string]any{"description": "第 44 周经营周报，华东、华南两个异常区域。"})
	script[agent.FrontdeskStepID] = []agent.ScriptedTurn{
		modelCalls(contract.ToolKBSearch, map[string]any{"query": "华南区域上周有什么异常"}),
		modelCalls(contract.ToolKBSearch, map[string]any{"query": "今天天气怎么样"}),
		modelSays("查到了。"),
	}
	created := h.createSession()
	h.confirm(created.ID, initiator, qaEmployee)

	h.say(created.ID, initiator, "华南区域上周有什么异常")

	calls := entriesOf(h.readSession(created.ID).Messages, session.KindToolCalled)
	if len(calls) != 2 {
		t.Fatalf("会话日志里的工具调用：%+v", calls)
	}
	if found := fmt.Sprint(calls[0].Result); !strings.Contains(found, filed.ID) {
		t.Fatalf("整句搜索没有找到条目：%s", found)
	}
	if unrelated := fmt.Sprint(calls[1].Result); strings.Contains(unrelated, filed.ID) {
		t.Fatalf("无关的整句也搜到了条目：%s", unrelated)
	}
}

// withParagraph 复制一轮提交周报的剧本，在第一段末尾加上一句话。
func withParagraph(t *testing.T, turn agent.ScriptedTurn, sentence string) agent.ScriptedTurn {
	t.Helper()
	raw, err := json.Marshal(turn.ToolCalls)
	if err != nil {
		t.Fatal(err)
	}
	calls := []agent.ScriptedToolCall{}
	if err := json.Unmarshal(raw, &calls); err != nil {
		t.Fatal(err)
	}
	sections := calls[0].Arguments["sections"].([]any)
	first := sections[0].(map[string]any)
	paragraphs := first["paragraphs"].([]any)
	paragraphs[0] = paragraphs[0].(string) + sentence
	return agent.ScriptedTurn{ToolCalls: calls}
}

func TestWeeklyReportNeverCarriesNumbersFromTheKnowledgeBase(t *testing.T) {
	const oldNumber = "87.65"
	script := caseScript(t, "A-01")
	submitCauses, submitReport := script["match_causes"][0], script["write_report"][0]
	h := newHarness(t, script)
	h.fileDelivered(h.deliveredReport().ID, map[string]any{"description": "上期周报：华东环比 +" + oldNumber + "%（旧口径）。", "tags": []string{"周报"}})
	// 第二份周报：写作步骤先想查知识库，再把知识库里的数字直接写进叙述，质检打回后才交出干净的稿子
	script["match_causes"] = append(script["match_causes"], submitCauses)
	script["write_report"] = append(script["write_report"],
		kbCall(contract.ToolKBSearch, map[string]any{"query": "华东 环比"}),
		withParagraph(t, submitReport, "上期知识库记载华东环比 +"+oldNumber+"%。"),
		submitReport,
	)

	second := h.command(h.reviewedTask(week44FormJSON).ID, reviewer, cmd(contract.CmdApprove))

	expectState(t, second, contract.StateDelivered)
	denied, qcPassed := []string{}, []bool{}
	for _, event := range h.taskEvents(second.ID) {
		switch event.Type {
		case contract.EventToolDenied:
			denied = append(denied, event.StepID+":"+event.Tool)
		case contract.EventToolCalled:
			if strings.HasPrefix(event.Tool, "kb_") {
				t.Fatalf("周报员工读到了知识库：%+v", event)
			}
		case contract.EventQCResult:
			qcPassed = append(qcPassed, *event.Passed)
		}
	}
	if fmt.Sprint(denied) != "[write_report:kb_search]" {
		t.Fatalf("越权调用的留痕：%v", denied)
	}
	if fmt.Sprint(qcPassed) != "[false true]" {
		t.Fatalf("写进叙述的裸数字没有被质检拦下：%v", qcPassed)
	}
	// 交付的周报正文（入库时抽出的文字）里没有知识库的数字
	filed := h.fileDelivered(second.ID, map[string]any{"description": "第二份第 44 周周报"})
	_, detail := h.knowledgeDetail(filed.ID)
	text, _ := detail["text"].(string)
	if !strings.Contains(text, "本周摘要") || strings.Contains(text, oldNumber) {
		t.Fatalf("交付的周报正文：%s", text)
	}
}

func TestQATaskIsOneJudgeStepWithOnlyKnowledgeTools(t *testing.T) {
	script := caseScript(t, "A-01")
	h := newHarness(t, script, withEmployee(qaEmployee))
	filed := h.fileDelivered(h.deliveredReport().ID, map[string]any{"description": "第 44 周经营周报，华东、华南两个异常区域。"})
	script["answer"] = []agent.ScriptedTurn{
		kbCall(contract.ToolKBSearch, map[string]any{"query": "周报 44"}),
		kbCall(contract.ToolKBRead, map[string]any{"id": filed.ID}),
		kbCall("submit_result", map[string]any{
			"answer":  "第 44 周周报提到杭州湖滨店因商场消防改造停业三天。",
			"sources": []any{map[string]any{"id": filed.ID, "title": filed.Title}},
		}),
	}
	created := h.createSession()
	h.confirm(created.ID, initiator, qaEmployee)

	h.submit(created.ID, map[string]any{"question": "第 44 周周报里杭州湖滨店怎么了"})

	task := h.settled(h.readSession(created.ID).TaskID)
	expectState(t, task, contract.StateAwaitingReview)
	if len(task.Steps) != 1 || task.Steps[0].ID != "answer" || task.Steps[0].Kind != contract.StepKindJudge {
		t.Fatalf("问答员工的步骤：%+v", task.Steps)
	}
	for _, request := range stepRequests(h, "answer") {
		if names := fmt.Sprint(toolNames(request.Request.Tools)); names != "[kb_search kb_read submit_result]" {
			t.Fatalf("判断步骤开放的工具：%s", names)
		}
	}
	called := []string{}
	for _, event := range h.taskEvents(task.ID) {
		if event.Type == contract.EventToolCalled {
			called = append(called, event.Tool)
		}
	}
	if fmt.Sprint(called) != "[kb_search kb_read]" {
		t.Fatalf("事件日志里的工具调用：%v", called)
	}
	if !strings.Contains(h.eventsJSON(task.ID), "因商场消防改造停业三天") {
		t.Fatal("事件日志里没有读到的正文或回答")
	}
}
