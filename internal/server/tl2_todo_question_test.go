// tl2：todo_write 与 ask_user_question。主接缝：HTTP + 剧本模型。
package server_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

// ---------------------------------------------------------------- todo_write

func todoMessages(detail session.Detail) []session.Message {
	return entriesOf(detail.Messages, session.KindTodoWritten)
}

func TestTodoWriteReplacesTheListAndIsAllowedInEveryMode(t *testing.T) {
	for _, mode := range session.PermissionModes {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, frontdeskScript(
				modelCalls(contract.ToolTodoWrite, map[string]any{"todos": []any{
					map[string]any{"content": "读纪要", "status": "in_progress"},
					map[string]any{"content": "写要点", "status": "pending"},
				}}),
				modelCalls(contract.ToolTodoWrite, map[string]any{"todos": []any{
					map[string]any{"content": "读纪要", "status": "completed"},
					map[string]any{"content": "写要点", "status": "in_progress"},
					map[string]any{"content": "发给审核人", "status": "pending"},
				}}),
				modelSays("计划好了。"),
			))
			created := h.prefaceWithEmployee()
			h.mustSetMode(created.ID, mode)

			h.say(created.ID, contract.RoleInitiator, "做个计划")

			detail := h.readSession(created.ID)
			if len(deniedTools(detail)) != 0 || len(permissionCards(detail)) != 0 {
				t.Fatalf("todo_write 在 %s 下不该被拒绝或询问", mode)
			}
			written := todoMessages(detail)
			if len(written) != 2 || len(written[1].Todos) != 3 {
				t.Fatalf("会话日志里的待办 = %+v", written)
			}
			// 会话视图带最新的清单（整表替换，不是追加）
			if got := fmt.Sprint(detail.Todos); got != "[{读纪要 completed} {写要点 in_progress} {发给审核人 pending}]" {
				t.Fatalf("会话视图的 todos = %s", got)
			}
			results := toolMessages(detail, contract.ToolTodoWrite)
			if got := resultMap(t, results[1])["message"]; got != "已更新待办：1 项待做，1 项进行中，1 项已完成" {
				t.Fatalf("结果文字 = %v", got)
			}
		})
	}
}

func TestTodoWriteValidation(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls(contract.ToolTodoWrite, map[string]any{"todos": []any{map[string]any{"content": "  ", "status": "pending"}}}),
		modelCalls(contract.ToolTodoWrite, map[string]any{"todos": []any{
			map[string]any{"content": "甲", "status": "pending"}, map[string]any{"content": "甲", "status": "completed"}}}),
		modelCalls(contract.ToolTodoWrite, map[string]any{"todos": []any{map[string]any{"content": "甲", "status": "done"}}}),
		modelCalls(contract.ToolTodoWrite, map[string]any{"todos": "不是数组"}),
		modelSays("都不对。"),
	))
	created := h.prefaceWithEmployee()

	h.say(created.ID, contract.RoleInitiator, "乱写")

	detail := h.readSession(created.ID)
	if len(todoMessages(detail)) != 0 || detail.Todos == nil || len(detail.Todos) != 0 {
		t.Fatalf("非法的清单不该写入：%+v / %+v", todoMessages(detail), detail.Todos)
	}
	reasons := []string{}
	for _, message := range toolMessages(detail, contract.ToolTodoWrite) {
		reasons = append(reasons, message.Reason)
	}
	for index, want := range []string{"不能为空", "重复", "status", "数组"} {
		if index >= len(reasons) || !strings.Contains(reasons[index], want) {
			t.Fatalf("第 %d 次的错误应含 %q：%v", index+1, want, reasons)
		}
	}
}

// ---------------------------------------------------------------- ask_user_question

func twoQuestions() map[string]any {
	return map[string]any{"questions": []any{
		map[string]any{"id": "period", "question": "统计哪一周？", "header": "统计周期", "options": []any{
			map[string]any{"label": "第 44 周（推荐）", "description": "最近一周"},
			map[string]any{"label": "第 43 周"},
		}},
		map[string]any{"id": "extras", "question": "附加哪些章节？", "multi_select": true, "options": []any{
			map[string]any{"label": "品类"}, map[string]any{"label": "门店"}, map[string]any{"label": "原因"},
		}},
	}}
}

// waitQuestion 等一张还没作答的提问卡出现。
func (h *harness) waitQuestion(sessionID string) session.Card {
	h.t.Helper()
	deadline := time.Now().Add(stopTimeout)
	for {
		for _, message := range h.readSession(sessionID).Messages {
			if card := message.Card; card != nil && card.Type == session.CardQuestion && card.Pending {
				return *card
			}
		}
		if time.Now().After(deadline) {
			h.t.Fatal("等不到提问卡")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (h *harness) answerQuestion(sessionID, requestID string, role contract.Role, answers any) (int, []byte) {
	h.t.Helper()
	return h.postJSON("/api/sessions/"+sessionID+"/questions/"+requestID, role, map[string]any{"answers": answers})
}

func TestAskUserQuestionPausesAndReturnsTheAnswers(t *testing.T) {
	for _, mode := range session.PermissionModes {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, frontdeskScript(
				modelCalls(contract.ToolAskUserQuestion, twoQuestions()),
				modelSays("收到你的选择。"),
			))
			created := h.prefaceWithEmployee()
			h.mustSetMode(created.ID, mode)
			stream := h.openStream(created.ID)
			defer stream.close()
			turn := h.sayAsync(created.ID, contract.RoleInitiator, "帮我出周报")

			card := h.waitQuestion(created.ID)
			if len(card.Questions) != 2 || card.Questions[0].Header != "统计周期" || !card.Questions[1].MultiSelect || !requestIDPattern.MatchString(card.RequestID) {
				t.Fatalf("提问卡 = %+v", card)
			}
			if h.sessionBusy(created.ID) == false {
				t.Fatal("等待提问时这一轮应还在进行")
			}
			// 只有发起人能答
			for _, role := range []contract.Role{contract.RoleReviewer, contract.RoleOperator} {
				if status, data := h.answerQuestion(created.ID, card.RequestID, role, []any{}); status != http.StatusForbidden {
					t.Fatalf("角色 %s 回答提问应 403：%d %s", role, status, data)
				}
			}
			// 校验：缺题、不存在的选项、单选选了两个、空回答
			bad := [][]any{
				{map[string]any{"id": "period", "selected": []any{"第 44 周（推荐）"}}},
				{map[string]any{"id": "period", "selected": []any{"第 99 周"}}, map[string]any{"id": "extras", "selected": []any{"品类"}}},
				{map[string]any{"id": "period", "selected": []any{"第 44 周（推荐）", "第 43 周"}}, map[string]any{"id": "extras", "selected": []any{"品类"}}},
				{map[string]any{"id": "period", "selected": []any{}}, map[string]any{"id": "extras", "selected": []any{"品类"}}},
				{map[string]any{"id": "nope", "selected": []any{"x"}}},
			}
			for index, answers := range bad {
				if status, data := h.answerQuestion(created.ID, card.RequestID, contract.RoleInitiator, answers); status != http.StatusBadRequest {
					t.Fatalf("非法答案 #%d 应 400：%d %s", index, status, data)
				}
			}
			if status, data := h.answerQuestion(created.ID, card.RequestID, contract.RoleInitiator, []any{
				map[string]any{"id": "period", "selected": []any{"第 44 周（推荐）"}},
				map[string]any{"id": "extras", "selected": []any{"品类", "原因"}, "custom": "再加一段摘要"},
			}); status != http.StatusOK {
				t.Fatalf("提交答案：%d %s", status, data)
			}
			h.waitTurn(stream, turn)

			detail := h.readSession(created.ID)
			cards := []session.Card{}
			for _, message := range detail.Messages {
				if message.Card != nil && message.Card.Type == session.CardQuestion {
					cards = append(cards, *message.Card)
				}
			}
			if len(cards) != 1 || cards[0].Pending || cards[0].Decision != session.DecisionAnswered || len(cards[0].Answers) != 2 || cards[0].Answers[1].Custom != "再加一段摘要" {
				t.Fatalf("答复后的提问卡 = %+v", cards)
			}
			// 答案原样作为工具结果交给模型
			results := toolResultTexts(h)
			want := `{"answers":[{"id":"period","selected":["第 44 周（推荐）"]},{"id":"extras","selected":["品类","原因"],"custom":"再加一段摘要"}]}`
			if len(results) != 1 || results[0] != want {
				t.Fatalf("交给模型的结果 = %v", results)
			}
			// 答过的卡不能再答
			if status, _ := h.answerQuestion(created.ID, card.RequestID, contract.RoleInitiator, []any{}); status != http.StatusConflict && status != http.StatusBadRequest {
				t.Fatalf("重复回答：%d", status)
			}
		})
	}
}

func TestAskUserQuestionIsCancelledByStop(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls(contract.ToolAskUserQuestion, twoQuestions()),
		modelSays("不该说到这。"),
	))
	created := h.prefaceWithEmployee()
	stream := h.openStream(created.ID)
	defer stream.close()
	turn := h.sayAsync(created.ID, contract.RoleInitiator, "问我")
	card := h.waitQuestion(created.ID)

	if status, data := h.postJSON("/api/sessions/"+created.ID+"/stop", contract.RoleInitiator, map[string]any{}); status != http.StatusOK {
		t.Fatalf("停止：%d %s", status, data)
	}
	h.waitTurn(stream, turn)
	h.waitIdle(created.ID)

	detail := h.readSession(created.ID)
	var found *session.Card
	for _, message := range detail.Messages {
		if message.Card != nil && message.Card.Type == session.CardQuestion {
			found = message.Card
		}
	}
	if found == nil || found.Pending || found.Decision != session.DecisionCancelled {
		t.Fatalf("停止后未决的提问记为已取消：%+v", found)
	}
	if got := lastAssistant(detail.Messages); got == "不该说到这。" {
		t.Fatalf("停止之后不该继续往下说：%q", got)
	}
	if status, _ := h.answerQuestion(created.ID, card.RequestID, contract.RoleInitiator, []any{
		map[string]any{"id": "period", "selected": []any{"第 43 周"}}, map[string]any{"id": "extras", "selected": []any{"品类"}}}); status != http.StatusConflict {
		t.Fatalf("已取消的提问再回答应 409：%d", status)
	}
}

func TestAskUserQuestionValidatesTheToolArguments(t *testing.T) {
	five := []any{}
	for i := 0; i < 5; i++ {
		five = append(five, map[string]any{"id": fmt.Sprint("q", i), "question": "?", "options": []any{map[string]any{"label": "a"}, map[string]any{"label": "b"}}})
	}
	h := newHarness(t, frontdeskScript(
		modelCalls(contract.ToolAskUserQuestion, map[string]any{"questions": five}),
		modelCalls(contract.ToolAskUserQuestion, map[string]any{"questions": []any{map[string]any{"id": "a", "question": "?", "options": []any{map[string]any{"label": "只有一个"}}}}}),
		modelCalls(contract.ToolAskUserQuestion, map[string]any{"questions": []any{map[string]any{"id": "a", "question": "?", "options": []any{
			map[string]any{"label": "1"}, map[string]any{"label": "2"}, map[string]any{"label": "3"}, map[string]any{"label": "4"}, map[string]any{"label": "5"}}}}}),
		modelSays("参数不对。"),
	))
	created := h.prefaceWithEmployee()

	h.say(created.ID, contract.RoleInitiator, "问")

	detail := h.readSession(created.ID)
	for _, message := range detail.Messages {
		if message.Card != nil && message.Card.Type == session.CardQuestion {
			t.Fatalf("参数不合格不该出卡：%+v", message.Card)
		}
	}
	calls := toolMessages(detail, contract.ToolAskUserQuestion)
	if len(calls) != 3 || !strings.Contains(calls[0].Reason, "最多问 4") || !strings.Contains(calls[1].Reason, "2–4 个选项") || !strings.Contains(calls[2].Reason, "2–4 个选项") {
		t.Fatalf("参数错误 = %+v", calls)
	}
}

// 别的角色触发的一轮里没人能答：直接拒绝，让 Agent 把问题写在回复里。
func TestAskUserQuestionIsRefusedWhenTheSpeakerIsNotTheInitiator(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls(contract.ToolAskUserQuestion, twoQuestions()),
		modelSays("请直接告诉我统计周期。"),
	))
	created := h.prefaceWithEmployee()

	h.say(created.ID, contract.RoleReviewer, "帮我出周报")

	detail := h.readSession(created.ID)
	if got := deniedTools(detail); len(got) != 1 || !strings.Contains(got[0].Reason, "审核人") {
		t.Fatalf("拒绝留痕 = %+v", got)
	}
}
