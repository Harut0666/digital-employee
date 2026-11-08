// 入库确认卡片：交付后在对话里出现，模型起草描述，人确认或修改；模型不可用时由人手写（Spec 0005“知识库”）。
package server_test

import (
	"net/http"
	"strings"
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

// deliveredSession 在对话里走完公开样例：确认员工、上传、提交、审核通过。
func (h *harness) deliveredSession() (sessionID, taskID string) {
	h.t.Helper()
	created := h.createSession()
	h.confirmed(created.ID, harnessEmployee)
	if status, data := h.uploadTo(created.ID, a01Files(h.t)); status != http.StatusOK {
		h.t.Fatalf("上传失败：%d %s", status, data)
	}
	taskID = h.submit(created.ID, week44Form).Session.TaskID
	expectState(h.t, h.settled(taskID), contract.StateAwaitingReview)
	expectState(h.t, h.command(taskID, reviewer, cmd(contract.CmdApprove)), contract.StateDelivered)
	return created.ID, taskID
}

// draftFiling 请模型为入库卡片起草描述。
func (h *harness) draftFiling(sessionID string, role contract.Role) (int, []byte) {
	h.t.Helper()
	return h.postJSON("/api/sessions/"+sessionID+"/filing-draft", role, map[string]any{})
}

func filingCard(h *harness, sessionID string) session.Card {
	h.t.Helper()
	return cardOf(h.t, h.readSession(sessionID).Messages, session.CardFiling)
}

const draftedDescription = "第 44 周经营周报：华东、华南两个异常区域，杭州湖滨店停业三天。"

func TestDeliveredSessionOffersAFilingCardDraftedByTheModel(t *testing.T) {
	script := caseScript(t, "A-01")
	script[agent.FrontdeskStepID] = []agent.ScriptedTurn{modelSays(draftedDescription)}
	h := newHarness(t, script)
	sessionID, taskID := h.deliveredSession()

	offered := filingCard(h, sessionID)
	if offered.TaskID != taskID || offered.Title != "经营周报_2026年第44周" || offered.Description != "" || offered.Filed != "" {
		t.Fatalf("交付后的入库卡片 = %+v", offered)
	}

	if status, data := h.draftFiling(sessionID, initiator); status != http.StatusOK {
		t.Fatalf("起草描述：%d %s", status, data)
	}

	drafted := filingCard(h, sessionID)
	if drafted.Description != draftedDescription || drafted.DraftFailed {
		t.Fatalf("起草后的入库卡片 = %+v", drafted)
	}
	// 模型读的是交付物正文，不开放任何工具
	requests := frontdeskRequests(h)
	asked := requests[len(requests)-1].Request
	if len(asked.Tools) != 0 || !strings.Contains(asked.Messages[0].Content[0].Text, "描述") || !strings.Contains(asked.Messages[1].Content[0].Text, "杭州湖滨店因商场消防改造停业三天") {
		t.Fatalf("起草描述时模型收到的请求：%+v", asked)
	}
	// 描述只是草稿：人确认（可以改）后才入库
	if entries := h.knowledgeEntries(); len(entries) != 0 {
		t.Fatalf("没确认就入库了：%+v", entries)
	}
	filed := h.fileDelivered(taskID, map[string]any{"title": drafted.Title, "description": "改过的描述：" + drafted.Description})
	if after := filingCard(h, sessionID); after.Filed != filed.ID {
		t.Fatalf("入库后的卡片 = %+v", after)
	}
}

func TestFilingDescriptionIsHandWrittenWhenTheModelIsDown(t *testing.T) {
	script := caseScript(t, "A-01")
	script[agent.FrontdeskStepID] = []agent.ScriptedTurn{{Error: stringPointer("模型接口返回 503")}}
	h := newHarness(t, script)
	sessionID, taskID := h.deliveredSession()

	status, data := h.draftFiling(sessionID, initiator)

	if status != http.StatusOK {
		t.Fatalf("模型不可用时起草：%d %s", status, data)
	}
	if text := textOf(t, decode[session.Reply](t, data).Messages, session.KindNotice); !strings.Contains(text, "自己写") {
		t.Fatalf("模型不可用时的说明 = %q", text)
	}
	card := filingCard(h, sessionID)
	if card.Description != "" || !card.DraftFailed {
		t.Fatalf("模型不可用时的入库卡片 = %+v", card)
	}
	filed := h.fileDelivered(taskID, map[string]any{"description": "人手写的描述"})
	if after := filingCard(h, sessionID); after.Filed != filed.ID {
		t.Fatalf("手写入库后的卡片 = %+v", after)
	}
}

func TestOnlyTheInitiatorDraftsAfterDelivery(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	created := h.createSession()
	h.confirmed(created.ID, harnessEmployee)

	if status, data := h.draftFiling(created.ID, initiator); status != http.StatusConflict {
		t.Fatalf("还没交付就起草：%d %s", status, data)
	}
	if status, data := h.draftFiling(created.ID, reviewer); status != http.StatusForbidden {
		t.Fatalf("审核人起草：%d %s", status, data)
	}
	for _, message := range h.readSession(created.ID).Messages {
		if message.Card != nil && message.Card.Type == session.CardFiling {
			t.Fatalf("没交付就出现了入库卡片：%+v", message.Card)
		}
	}
}
