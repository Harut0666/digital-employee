// 知识库：入库、浏览、查看、删除和两个只读工具，全部从主接缝验证（Spec 0005“知识库”、ADR-0005）。
package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/knowledge"
	"digital-employee/internal/session"
)

// fileReport 把任务的交付物放进知识库，不判断结果。
func (h *harness) fileReport(taskID string, role contract.Role, body map[string]any) (int, []byte) {
	h.t.Helper()
	request := map[string]any{"taskId": taskID}
	for key, value := range body {
		request[key] = value
	}
	return h.postJSON("/api/knowledge", role, request)
}

// knowledgeEntries GET /api/knowledge。
func (h *harness) knowledgeEntries() []knowledge.Entry {
	h.t.Helper()
	status, data := h.send(http.MethodGet, "/api/knowledge", "", "", nil)
	if status != http.StatusOK {
		h.t.Fatalf("读取知识库失败：%d %s", status, data)
	}
	return decode[[]knowledge.Entry](h.t, data)
}

// deliveredReport 走完公开样例：提交、审核通过，返回已交付的任务。
func (h *harness) deliveredReport() contract.TaskSnapshot {
	h.t.Helper()
	task := h.reviewedTask(week44FormJSON)
	delivered := h.command(task.ID, reviewer, cmd(contract.CmdApprove))
	expectState(h.t, delivered, contract.StateDelivered)
	return delivered
}

func TestFilingNeedsADescription(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	task := h.deliveredReport()

	for _, description := range []any{nil, "", "   \n"} {
		body := map[string]any{}
		if description != nil {
			body["description"] = description
		}
		status, data := h.fileReport(task.ID, initiator, body)
		if status != http.StatusBadRequest {
			t.Fatalf("描述为 %q 时入库：%d %s", description, status, data)
		}
	}
	if entries := h.knowledgeEntries(); len(entries) != 0 {
		t.Fatalf("知识库里不该有条目：%+v", entries)
	}
}

// fileDelivered 发起人把已交付的周报放进知识库，要求成功。
func (h *harness) fileDelivered(taskID string, body map[string]any) knowledge.Entry {
	h.t.Helper()
	status, data := h.fileReport(taskID, initiator, body)
	if status != http.StatusCreated {
		h.t.Fatalf("入库失败：%d %s", status, data)
	}
	return decode[knowledge.Entry](h.t, data)
}

// knowledgeDetail GET /api/knowledge/:id 的原始 JSON。
func (h *harness) knowledgeDetail(id string) (int, map[string]any) {
	h.t.Helper()
	status, data := h.send(http.MethodGet, "/api/knowledge/"+id, "", "", nil)
	return status, decode[map[string]any](h.t, data)
}

func TestDeliveredReportIsFiledWithItsWordText(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	task := h.deliveredReport()

	filed := h.fileDelivered(task.ID, map[string]any{
		"description": "第 44 周经营周报，华东、华南两个异常区域。",
		"tags":        []string{"周报", "2026W44"},
	})

	entries := h.knowledgeEntries()
	if len(entries) != 1 || entries[0].ID != filed.ID || filed.ID == "" {
		t.Fatalf("知识库条目：%+v", entries)
	}
	entry := entries[0]
	// 标题没给时用交付物的下载名
	if entry.Title != "经营周报_2026年第44周" || entry.Description != "第 44 周经营周报，华东、华南两个异常区域。" {
		t.Fatalf("标题和描述：%+v", entry)
	}
	if fmt.Sprint(entry.Tags) != "[周报 2026W44]" || entry.SourceTask != task.ID || entry.SourceEmployee != harnessEmployee || entry.CreatedAt == "" {
		t.Fatalf("条目字段：%+v", entry)
	}

	status, detail := h.knowledgeDetail(filed.ID)
	text, _ := detail["text"].(string)
	if status != http.StatusOK || !strings.Contains(text, "本周摘要") || !strings.Contains(text, "杭州湖滨店因商场消防改造停业三天") {
		t.Fatalf("查看条目：%d %v", status, detail)
	}
	if strings.Contains(text, "<w:") {
		t.Fatalf("正文里混进了 XML：%s", text)
	}
}

func TestOnlyTheInitiatorFilesAReport(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	task := h.deliveredReport()

	for _, role := range []contract.Role{"", reviewer, contract.RoleOperator} {
		if status, data := h.fileReport(task.ID, role, map[string]any{"description": "周报"}); status != http.StatusForbidden {
			t.Fatalf("%q 入库：%d %s", role, status, data)
		}
	}
	if status, data := h.fileReport("no-such-task", initiator, map[string]any{"description": "周报"}); status != http.StatusNotFound {
		t.Fatalf("不存在的任务入库：%d %s", status, data)
	}
}

func TestFiledEntryCanBeDeleted(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	task := h.deliveredReport()
	filed := h.fileDelivered(task.ID, map[string]any{"description": "第 44 周经营周报"})

	if status, data := h.send(http.MethodDelete, "/api/knowledge/"+filed.ID, reviewer, "", nil); status != http.StatusForbidden {
		t.Fatalf("审核人删除条目：%d %s", status, data)
	}
	if status, data := h.send(http.MethodDelete, "/api/knowledge/"+filed.ID, initiator, "", nil); status != http.StatusOK {
		t.Fatalf("删除条目：%d %s", status, data)
	}

	if entries := h.knowledgeEntries(); len(entries) != 0 {
		t.Fatalf("删除后还在：%+v", entries)
	}
	if status, _ := h.knowledgeDetail(filed.ID); status != http.StatusNotFound {
		t.Fatalf("删除后还能查看：%d", status)
	}
	if status, _ := h.send(http.MethodDelete, "/api/knowledge/"+filed.ID, initiator, "", nil); status != http.StatusNotFound {
		t.Fatalf("重复删除：%d", status)
	}
}

// kbCall 判断步骤里的一轮：调一个知识库工具。
func kbCall(name string, arguments map[string]any) agent.ScriptedTurn {
	return agent.ScriptedTurn{ToolCalls: []agent.ScriptedToolCall{{Name: name, Arguments: arguments}}}
}

// stepRequests 剧本模型在某个步骤收到的请求。
func stepRequests(h *harness, stepID string) []agent.ScriptedRequest {
	out := []agent.ScriptedRequest{}
	for _, request := range h.provider.Requests() {
		if request.StepID == stepID {
			out = append(out, request)
		}
	}
	return out
}

func TestWeeklyReportJudgeStepsCannotUseTheKnowledgeBase(t *testing.T) {
	script := caseScript(t, "A-01")
	script["match_causes"] = append([]agent.ScriptedTurn{kbCall(contract.ToolKBSearch, map[string]any{"query": "周报"})}, script["match_causes"]...)
	h := newHarness(t, script)

	task := h.reviewedTask(week44FormJSON)

	expectState(t, task, contract.StateAwaitingReview)
	denied := []contract.TaskEvent{}
	for _, event := range h.taskEvents(task.ID) {
		if event.Type == contract.EventToolDenied {
			denied = append(denied, event)
		}
		if event.Type == contract.EventToolCalled && strings.HasPrefix(event.Tool, "kb_") {
			t.Fatalf("周报员工调用了知识库：%+v", event)
		}
	}
	if len(denied) != 1 || denied[0].Tool != contract.ToolKBSearch || denied[0].StepID != "match_causes" || denied[0].Reason == "" {
		t.Fatalf("越权调用的留痕：%+v", denied)
	}
	for _, step := range []string{"match_causes", "write_report"} {
		for _, request := range stepRequests(h, step) {
			if names := fmt.Sprint(toolNames(request.Request.Tools)); names != "[submit_result]" {
				t.Fatalf("%s 开放的工具：%s", step, names)
			}
		}
	}
}

func TestOpenedJudgeStepSearchesTheIndexThenReadsTheText(t *testing.T) {
	script := caseScript(t, "A-01")
	submitCauses := script["match_causes"][0]
	script["write_report"] = append(script["write_report"], script["write_report"]...)
	h := newHarness(t, script, withWorkflowEdit(
		"    tools: []\n    outputSchema: schemas/match_causes.json",
		"    tools: [kb_search, kb_read]\n    outputSchema: schemas/match_causes.json",
	))
	filed := h.fileDelivered(h.deliveredReport().ID, map[string]any{"description": "第 44 周经营周报，华东、华南两个异常区域。", "tags": []string{"周报"}})
	// 条目 ID 入库后才知道：第二个任务的剧本这时再补上（剧本模型按调用时的剧本取轮次）
	script["match_causes"] = append(script["match_causes"],
		kbCall(contract.ToolKBSearch, map[string]any{"query": "华南 异常"}),
		kbCall(contract.ToolKBRead, map[string]any{"id": filed.ID}),
		submitCauses,
	)

	second := h.reviewedTask(week44FormJSON)

	requests := stepRequests(h, "match_causes")
	if len(requests) != 4 {
		t.Fatalf("match_causes 的模型调用 = %d", len(requests))
	}
	if names := fmt.Sprint(toolNames(requests[1].Request.Tools)); names != "[kb_search kb_read submit_result]" {
		t.Fatalf("开放的工具：%s", names)
	}
	// 搜索结果只有索引字段，没有正文
	searched := lastToolResult(t, requests[2])
	for _, want := range []string{filed.ID, "第 44 周经营周报", "周报"} {
		if !strings.Contains(searched, want) {
			t.Fatalf("搜索结果里没有 %q：%s", want, searched)
		}
	}
	if strings.Contains(searched, "杭州湖滨店") || strings.Contains(searched, "\"text\"") {
		t.Fatalf("搜索结果带了正文：%s", searched)
	}
	// 读取拿到正文
	if read := lastToolResult(t, requests[3]); !strings.Contains(read, "杭州湖滨店因商场消防改造停业三天") {
		t.Fatalf("读取结果没有正文：%s", read)
	}
	// 两次读取都写进了任务的事件日志
	called := []string{}
	for _, event := range h.taskEvents(second.ID) {
		if event.Type == contract.EventToolCalled {
			called = append(called, event.Tool)
		}
	}
	if !strings.Contains(fmt.Sprint(called), "kb_search kb_read") {
		t.Fatalf("事件日志里的工具调用：%v", called)
	}
}

func TestWeeklyReportFrontdeskCannotUseTheKnowledgeBase(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls(contract.ToolKBSearch, map[string]any{"query": "周报"}),
		modelSays("我查不了知识库。"),
	))
	opened, _ := pausedSession(h)

	h.say(opened.ID, initiator, "上周的周报怎么说")

	refused := entriesOf(h.readSession(opened.ID).Messages, session.KindToolDenied)
	if len(refused) != 1 || refused[0].Tool != contract.ToolKBSearch || refused[0].Reason == "" {
		t.Fatalf("越权调用的留痕：%+v", refused)
	}
	if names := fmt.Sprint(toolNames(frontdeskRequests(h)[0].Request.Tools)); names != "[query_task propose_card]" {
		t.Fatalf("开放的工具：%s", names)
	}
}

// tickFrontdeskTools 在员工配置页勾选前台 Agent 的只读工具。
func (h *harness) tickFrontdeskTools(tools ...string) {
	h.t.Helper()
	raw, err := json.Marshal(map[string]any{"data": map[string]any{"body": "# 前台 Agent\n\n说话简短。\n", "tools": tools}})
	if err != nil {
		h.t.Fatal(err)
	}
	status, data := h.send(http.MethodPut, "/api/employees/"+harnessEmployee+"/config/frontdesk.md", contract.RoleOperator, "application/json", bytes.NewReader(raw))
	if status != http.StatusOK {
		h.t.Fatalf("保存前台 Agent 配置失败：%d %s", status, data)
	}
}

func TestTickedFrontdeskSearchesThenReadsTheKnowledgeBase(t *testing.T) {
	script := caseScript(t, "A-01")
	h := newHarness(t, script)
	filed := h.fileDelivered(h.deliveredReport().ID, map[string]any{"description": "第 44 周经营周报，华东、华南两个异常区域。"})
	h.tickFrontdeskTools(contract.ToolQueryTask, contract.ToolKBSearch, contract.ToolKBRead)
	// 条目 ID 入库后才知道：前台 Agent 的剧本这时再写（剧本模型按调用时的剧本取轮次）
	script[agent.FrontdeskStepID] = []agent.ScriptedTurn{
		modelCalls(contract.ToolKBSearch, map[string]any{"query": "第 44 周"}),
		modelCalls(contract.ToolKBRead, map[string]any{"id": filed.ID}),
		modelSays("上周周报提到杭州湖滨店停业三天。"),
	}
	created := h.createSession()
	h.confirm(created.ID, initiator, harnessEmployee)

	h.say(created.ID, initiator, "上周周报里杭州湖滨店怎么了")

	if names := fmt.Sprint(toolNames(frontdeskRequests(h)[0].Request.Tools)); names != "[fill_form_draft propose_card kb_search kb_read]" {
		t.Fatalf("开放的工具：%s", names)
	}
	// 两次读取都写进会话日志：搜索只有索引字段，读取有正文
	calls := entriesOf(h.readSession(created.ID).Messages, session.KindToolCalled)
	if len(calls) != 2 || calls[0].Tool != contract.ToolKBSearch || calls[1].Tool != contract.ToolKBRead {
		t.Fatalf("会话日志里的工具调用：%+v", calls)
	}
	searched, _ := json.Marshal(calls[0].Result)
	if !strings.Contains(string(searched), filed.ID) || strings.Contains(string(searched), "杭州湖滨店") || strings.Contains(string(searched), `"text"`) {
		t.Fatalf("搜索结果：%s", searched)
	}
	if read, _ := json.Marshal(calls[1].Result); !strings.Contains(string(read), "杭州湖滨店因商场消防改造停业三天") {
		t.Fatalf("读取结果：%s", read)
	}
}

// lastToolResult 一次模型请求里最后一条工具结果的文字。
func lastToolResult(t *testing.T, request agent.ScriptedRequest) string {
	t.Helper()
	messages := request.Request.Messages
	last := messages[len(messages)-1]
	if last.Role != "tool" || last.Content[0].IsError {
		t.Fatalf("最后一条不是成功的工具结果：%+v", last)
	}
	return last.Content[0].Text
}

func TestUndeliveredReportCannotBeFiled(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	task := h.reviewedTask(week44FormJSON)

	status, data := h.fileReport(task.ID, initiator, map[string]any{"description": "第 44 周经营周报"})

	if status != http.StatusConflict {
		t.Fatalf("待审核的周报入库：%d %s", status, data)
	}
	if entries := h.knowledgeEntries(); len(entries) != 0 {
		t.Fatalf("知识库里不该有条目：%+v", entries)
	}
}
