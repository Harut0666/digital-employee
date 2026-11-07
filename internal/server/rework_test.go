// 超范围意见：任务带着说明回到待审核，原交付物保持不变，审核人可通过、再退回或终止。
package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

const outOfScopeReason = "意见要求把实收改成含税口径，这属于计算口径变更"

// outOfScopeScript 第一次退回被判为超范围，再次退回时按正常意见重做。
func outOfScopeScript(t *testing.T) agent.ModelScript {
	t.Helper()
	script := caseScript(t, "A-01-rework")
	var outOfScope agent.ScriptedTurn
	raw := `{"toolCalls":[{"name":"submit_result","arguments":{
		"items":[{"id":"F1","comment":"实收改成含税口径","sections":[],"kind":"calculation_basis","action":"无法照办"}],
		"needsHuman":true,"needsHumanReason":"` + outOfScopeReason + `"}}]}`
	if err := json.Unmarshal([]byte(raw), &outOfScope); err != nil {
		t.Fatal(err)
	}
	script["interpret_feedback"] = append([]agent.ScriptedTurn{outOfScope}, script["interpret_feedback"]...)
	return script
}

func docxOf(t *testing.T, snapshot contract.TaskSnapshot) contract.DeliverableRef {
	t.Helper()
	for _, deliverable := range snapshot.Deliverables {
		if deliverable.Kind == contract.DeliverableDocx {
			return deliverable
		}
	}
	t.Fatalf("没有 docx 交付物：%+v", snapshot.Deliverables)
	return contract.DeliverableRef{}
}

// returnedOutOfScope 走到“超范围意见回到待审核”，返回原始送审快照和回到待审核后的快照。
func returnedOutOfScope(t *testing.T) (h *harness, original, returned contract.TaskSnapshot) {
	t.Helper()
	h = newHarness(t, outOfScopeScript(t))
	task := h.createTask(a01Files(t))
	original = h.command(task.ID, initiator, cmd(contract.CmdSubmit))
	expectState(t, original, contract.StateAwaitingReview)
	returned = h.command(task.ID, reviewer, map[string]any{"type": contract.CmdReturn, "comment": "实收改成含税口径"})
	return h, original, returned
}

func TestOutOfScopeFeedbackReturnsToReview(t *testing.T) {
	h, original, returned := returnedOutOfScope(t)

	expectState(t, returned, contract.StateAwaitingReview)
	if returned.Pause == nil || returned.Pause.Code != contract.PauseReworkNeedsHuman {
		t.Fatalf("暂停原因 = %+v", returned.Pause)
	}
	if !strings.Contains(returned.Pause.Message, outOfScopeReason) {
		t.Fatalf("暂停说明没有写明原因：%q", returned.Pause.Message)
	}
	expectActions(t, returned, "approve=reviewer", "return=reviewer", "terminate=reviewer")
	expectWaitingFor(t, returned, reviewer)

	// 原交付物保留，版本不变，也还能下载
	if docxOf(t, returned) != docxOf(t, original) {
		t.Fatalf("交付物被改动：%+v → %+v", docxOf(t, original), docxOf(t, returned))
	}
	status, _ := h.send(http.MethodGet, "/api/tasks/"+returned.ID+"/deliverables/docx", "", "", nil)
	if status != http.StatusOK {
		t.Fatalf("下载原交付物 = %d", status)
	}
	// 步骤进度也回到送审时：写作和渲染的结果还在
	for _, step := range returned.Steps {
		if step.ID == "write_report" || step.ID == "render_document" || step.ID == "qc" {
			if step.Status != contract.StepDone {
				t.Fatalf("步骤 %s 状态 = %s，want done", step.ID, step.Status)
			}
		}
	}
}

func TestReviewerApprovesAfterOutOfScopeFeedback(t *testing.T) {
	h, original, returned := returnedOutOfScope(t)

	delivered := h.command(returned.ID, reviewer, cmd(contract.CmdApprove))
	expectState(t, delivered, contract.StateDelivered)
	if docxOf(t, delivered) != docxOf(t, original) {
		t.Fatalf("交付的不是原交付物：%+v", docxOf(t, delivered))
	}
}

func TestReviewerReturnsAgainAfterOutOfScopeFeedback(t *testing.T) {
	h, original, returned := returnedOutOfScope(t)

	reworked := h.command(returned.ID, reviewer, map[string]any{
		"type": contract.CmdReturn, "comment": "摘要请再精简，突出本周最重要的两个异常。", "sections": []string{"summary"},
	})
	expectState(t, reworked, contract.StateAwaitingReview)
	if reworked.Pause == nil || reworked.Pause.Code != contract.PauseAwaitingReview {
		t.Fatalf("重做后应正常送审：%+v", reworked.Pause)
	}
	if docxOf(t, reworked).SHA256 == docxOf(t, original).SHA256 {
		t.Fatal("重做后应产出新版本交付物")
	}
	expectState(t, h.command(returned.ID, reviewer, cmd(contract.CmdApprove)), contract.StateDelivered)
}

func TestReviewerTerminatesAfterOutOfScopeFeedback(t *testing.T) {
	h, _, returned := returnedOutOfScope(t)

	terminated := h.command(returned.ID, reviewer, cmd(contract.CmdTerminate))
	expectState(t, terminated, contract.StateTerminated)
	expectActions(t, terminated)
}
