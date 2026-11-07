// 步骤的业务文案写在工作流配置里，快照原样带给前端，前端不写死。
package server_test

import (
	"testing"

	"digital-employee/internal/contract"
)

func stepOf(t *testing.T, snapshot contract.TaskSnapshot, id string) contract.StepStatus {
	t.Helper()
	for _, step := range snapshot.Steps {
		if step.ID == id {
			return step
		}
	}
	t.Fatalf("快照里没有步骤 %s", id)
	return contract.StepStatus{}
}

func TestStepActivityComesFromWorkflowConfig(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"), withWorkflowEdit("activity: 正在撰写周报", "activity: 正在写本周的周报"))
	task := h.createTask(a01Files(t))

	if got := stepOf(t, task, "write_report").Activity; got != "正在写本周的周报" {
		t.Fatalf("write_report 的业务文案 = %q", got)
	}
}

func TestEveryWeeklyReportStepHasActivity(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	task := h.createTask(a01Files(t))

	for _, step := range task.Steps {
		if step.Activity == "" {
			t.Errorf("步骤 %s 没有业务文案", step.ID)
		}
	}
}
