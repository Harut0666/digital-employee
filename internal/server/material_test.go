// 材料问题：执行中发现材料本身有问题，任务转待发起人补充，补充后继续。
package server_test

import (
	"strings"
	"testing"

	"digital-employee/internal/contract"
)

const corruptMinutes = "周会纪要_2026W44.docx"

// corruptMinutesTask 损坏的纪要：准入只把它识别成“周次不符”，选择无纪要继续后才在解析时失败。
func corruptMinutesTask(t *testing.T, h *harness) contract.TaskSnapshot {
	t.Helper()
	task := h.createTask([]upload{
		sampleFile(t, "销售明细.xlsx"),
		{name: corruptMinutes, content: []byte("这不是一个 docx 文件")},
	})
	admission := h.command(task.ID, initiator, cmd(contract.CmdSubmit))
	expectState(t, admission, contract.StateAwaitingInitiator)
	if admission.Pause.Code != contract.PauseMinutesWeekMismatch {
		t.Fatalf("准入暂停原因 = %+v", admission.Pause)
	}
	return h.command(task.ID, initiator, cmd(contract.CmdContinueWithoutMinutes))
}

func TestCorruptMinutesPauseForInitiator(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	paused := corruptMinutesTask(t, h)

	expectState(t, paused, contract.StateAwaitingInitiator)
	if paused.Pause.Code != contract.PauseMaterialIssue {
		t.Fatalf("暂停原因 = %+v", paused.Pause)
	}
	expectActions(t, paused, "supplement=initiator", "terminate=initiator")
	expectWaitingFor(t, paused, initiator)
	if !strings.Contains(paused.Pause.Message, corruptMinutes) {
		t.Fatalf("暂停说明没有指出哪个文件：%q", paused.Pause.Message)
	}
	if strings.Contains(paused.Pause.Message, contract.ToolParseMinutes) {
		t.Fatalf("工具名不应出现在暂停说明里：%q", paused.Pause.Message)
	}
	if paused.Pause.Detail["tool"] != contract.ToolParseMinutes {
		t.Fatalf("技术细节应放在 detail：%+v", paused.Pause.Detail)
	}
	// 材料问题重试也不会好，不走工具重试
	calls := 0
	for _, event := range decode[[]contract.TaskEvent](t, []byte(h.eventsJSON(paused.ID))) {
		if event.Type == contract.EventToolCalled && event.Tool == contract.ToolParseMinutes {
			calls++
		}
	}
	if calls != 1 {
		t.Fatalf("parse_minutes 调用了 %d 次，want 1", calls)
	}
}

func TestSupplementReplacesCorruptMinutesAndContinues(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	paused := corruptMinutesTask(t, h)

	resumed := h.supplement(paused.ID, []upload{sampleFile(t, "周会纪要_2026W44.docx")})
	expectState(t, resumed, contract.StateAwaitingReview)
	// 损坏的文件被替换掉，否则解析步骤还会再失败
	if len(resumed.Attachments) != 2 {
		t.Fatalf("附件 = %+v，want 销售明细和新纪要", resumed.Attachments)
	}
	expectState(t, h.command(paused.ID, reviewer, cmd(contract.CmdApprove)), contract.StateDelivered)
}

func TestSupplementWithoutNewFileFallsBackToAdmission(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	paused := corruptMinutesTask(t, h)

	// 没传新文件：损坏的纪要已移除，无纪要继续的选择还在，任务不再卡在同一个文件上
	resumed := h.supplement(paused.ID, nil)
	if resumed.Pause != nil && resumed.Pause.Code == contract.PauseMaterialIssue {
		t.Fatalf("不应再次因同一个文件暂停：%+v", resumed.Pause)
	}
	if len(resumed.Attachments) != 1 {
		t.Fatalf("附件 = %+v，want 只剩销售明细", resumed.Attachments)
	}
}

func TestInitiatorTerminatesTaskWithMaterialIssue(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	paused := corruptMinutesTask(t, h)
	expectState(t, h.command(paused.ID, initiator, cmd(contract.CmdTerminate)), contract.StateTerminated)
}
