// 流转规则与可用操作：全部从 HTTP 接口验证（Spec 0005“操作规则”“流转表改动”）。
package server_test

import (
	"net/http"
	"testing"

	"digital-employee/internal/contract"
)

const (
	initiator = contract.RoleInitiator
	reviewer  = contract.RoleReviewer
	operator  = contract.RoleOperator
)

func cmd(commandType string) map[string]any { return map[string]any{"type": commandType} }

func TestActionsFollowTaskFromDraftToDelivered(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))

	task := h.createTask(a01Files(t))
	expectState(t, task, contract.StateDraft)
	expectActions(t, task, "submit=initiator", "terminate=initiator")
	expectWaitingFor(t, task, initiator)

	review := h.command(task.ID, initiator, cmd(contract.CmdSubmit))
	expectState(t, review, contract.StateAwaitingReview)
	expectActions(t, review, "approve=reviewer", "return=reviewer", "terminate=reviewer")
	expectWaitingFor(t, review, reviewer)

	delivered := h.command(task.ID, reviewer, cmd(contract.CmdApprove))
	expectState(t, delivered, contract.StateDelivered)
	expectActions(t, delivered)
	expectWaitingFor(t, delivered)
}

func TestInitiatorAbandonsDraft(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	task := h.createTask(a01Files(t))

	refused := h.tryCommand(task.ID, reviewer, cmd(contract.CmdTerminate))
	if refused.status != http.StatusForbidden || refused.Error != contract.ErrForbiddenRole {
		t.Fatalf("审核人放弃草稿应被拒绝：%+v", refused)
	}
	wantMessage := "审核人无权在「草稿」执行「放弃草稿」，应由发起人执行。"
	if refused.Message != wantMessage {
		t.Fatalf("拒绝提示 = %q，want %q", refused.Message, wantMessage)
	}

	abandoned := h.command(task.ID, initiator, cmd(contract.CmdTerminate))
	expectState(t, abandoned, contract.StateTerminated)
	expectActions(t, abandoned)
	expectWaitingFor(t, abandoned)

	tooLate := h.tryCommand(task.ID, initiator, cmd(contract.CmdSubmit))
	if tooLate.status != http.StatusConflict || tooLate.Error != contract.ErrIllegalTransition {
		t.Fatalf("已终止任务不能再提交：%+v", tooLate)
	}
	if want := "任务当前处于「已终止」，不能执行「提交」。"; tooLate.Message != want {
		t.Fatalf("拒绝提示 = %q，want %q", tooLate.Message, want)
	}
}

func TestReviewerTerminatesTaskAwaitingReview(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	task := h.createTask(a01Files(t))
	h.command(task.ID, initiator, cmd(contract.CmdSubmit))

	refused := h.tryCommand(task.ID, initiator, cmd(contract.CmdTerminate))
	if refused.status != http.StatusForbidden {
		t.Fatalf("发起人不能终止待审核的任务：%+v", refused)
	}

	terminated := h.command(task.ID, reviewer, cmd(contract.CmdTerminate))
	expectState(t, terminated, contract.StateTerminated)
	if len(terminated.Deliverables) == 0 {
		t.Fatal("终止不应丢掉已有交付物")
	}
}

func TestInitiatorRetriesToolFailure(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	h.setToolFaults(map[string]string{contract.ToolLoadSales: "error"})
	task := h.createTask(a01Files(t))

	paused := h.command(task.ID, initiator, cmd(contract.CmdSubmit))
	expectState(t, paused, contract.StateAwaitingOperator)
	expectActions(t, paused, "retry=initiator,operator", "terminate=operator")
	expectWaitingFor(t, paused, initiator, operator)

	// 角色名去重：重试在流转表里有两个目标，提示里每个角色只出现一次
	refused := h.tryCommand(task.ID, reviewer, cmd(contract.CmdRetry))
	if want := "审核人无权在「待人工处理」执行「重试」，应由发起人、操作员执行。"; refused.Message != want {
		t.Fatalf("拒绝提示 = %q，want %q", refused.Message, want)
	}
	if refused := h.tryCommand(task.ID, initiator, cmd(contract.CmdTerminate)); refused.status != http.StatusForbidden {
		t.Fatalf("发起人不能终止待人工处理的任务：%+v", refused)
	}

	h.setToolFaults(map[string]string{})
	resumed := h.command(task.ID, initiator, cmd(contract.CmdRetry))
	expectState(t, resumed, contract.StateAwaitingReview)
}

func TestInitiatorRetriesAdmissionToolFailure(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	h.setToolFaults(map[string]string{contract.ToolValidateSubmission: "error"})
	task := h.createTask(a01Files(t))

	paused := h.command(task.ID, initiator, cmd(contract.CmdSubmit))
	expectState(t, paused, contract.StateAwaitingOperator)

	h.setToolFaults(map[string]string{})
	reply := h.tryCommand(task.ID, initiator, cmd(contract.CmdRetry))
	if !reply.OK || reply.Snapshot.State != contract.StateAdmission {
		t.Fatalf("准入步骤的工具故障重试后应回到准入校验：%+v", reply)
	}
	expectState(t, h.settled(task.ID), contract.StateAwaitingReview)
}

func TestOnlyOperatorRetriesLimitExceeded(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"), withWorkflowEdit("modelCalls: 30", "modelCalls: 1"))
	task := h.createTask(a01Files(t))

	paused := h.command(task.ID, initiator, cmd(contract.CmdSubmit))
	expectState(t, paused, contract.StateAwaitingOperator)
	if paused.Pause.Code != contract.PauseLimitExceeded {
		t.Fatalf("暂停原因 = %+v", paused.Pause)
	}
	expectActions(t, paused, "retry=operator", "terminate=operator")
	expectWaitingFor(t, paused, operator)

	refused := h.tryCommand(task.ID, initiator, cmd(contract.CmdRetry))
	if refused.status != http.StatusForbidden || refused.Error != contract.ErrForbiddenRole {
		t.Fatalf("发起人不能重试超限：%+v", refused)
	}
	if want := "发起人无权在「待人工处理」执行「重试」，应由操作员执行。"; refused.Message != want {
		t.Fatalf("拒绝提示 = %q，want %q", refused.Message, want)
	}

	// 操作员重试时预算窗口重新计算，任务能继续走完
	expectState(t, h.command(task.ID, operator, cmd(contract.CmdRetry)), contract.StateAwaitingReview)
}

func TestOnlyOperatorRetriesQcExhausted(t *testing.T) {
	h := newHarness(t, caseScript(t, "S-24"))
	task := h.createTask(a01Files(t))

	paused := h.command(task.ID, initiator, cmd(contract.CmdSubmit))
	expectState(t, paused, contract.StateAwaitingOperator)
	if paused.Pause.Code != contract.PauseQcExhausted {
		t.Fatalf("暂停原因 = %+v", paused.Pause)
	}
	expectActions(t, paused, "retry=operator", "terminate=operator")
	expectWaitingFor(t, paused, operator)

	if refused := h.tryCommand(task.ID, initiator, cmd(contract.CmdRetry)); refused.status != http.StatusForbidden {
		t.Fatalf("发起人不能重试质检用尽：%+v", refused)
	}
	expectState(t, h.command(task.ID, operator, cmd(contract.CmdTerminate)), contract.StateTerminated)
}

func TestPauseOptionsLimitCommands(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	task := h.createTask([]upload{sampleFile(t, "销售明细.xlsx")})
	paused := h.command(task.ID, initiator, cmd(contract.CmdSubmit))
	expectState(t, paused, contract.StateAwaitingInitiator)
	expectActions(t, paused, "supplement=initiator", "continue_without_minutes=initiator", "terminate=initiator")
	expectWaitingFor(t, paused, initiator)

	// 缺纪要时“按已有数据继续”没有意义，后端要和按钮一致
	refused := h.tryCommand(task.ID, initiator, cmd(contract.CmdContinueWithPartial))
	if refused.status != http.StatusConflict || refused.Error != contract.ErrIllegalTransition {
		t.Fatalf("缺纪要时不能按已有数据继续：%+v", refused)
	}
}
