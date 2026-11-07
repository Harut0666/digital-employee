// 旧工作区：事件日志是 T24 之前写的，没有任何新字段，actions 等投影也要能算出来。
package server_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"digital-employee/internal/contract"
)

// legacyTask 把一份手写的旧事件日志放进工作区，配置取工作区里的员工副本。
func legacyTask(t *testing.T, h *harness, id string, lines ...string) {
	t.Helper()
	dir := filepath.Join(h.workspace, "tasks", id)
	copyEmployee(t, filepath.Join(h.employees, harnessEmployee), filepath.Join(dir, "config"))
	log := strings.Join(append([]string{
		`{"seq":1,"ts":"2026-10-01T00:00:00.000Z","taskId":"` + id + `","type":"task_created","employee":"weekly-report","configHash":"old","form":{"period":{"year":2026,"week":44}},"attachments":[]}`,
	}, lines...), "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
}

func stateChange(seq int, id, from, to, trigger, pause string) string {
	line := `{"seq":` + itoa(seq) + `,"ts":"2026-10-01T00:00:0` + itoa(seq) + `.000Z","taskId":"` + id +
		`","type":"state_changed","from":"` + from + `","to":"` + to + `","trigger":"` + trigger + `","actor":"system"`
	if pause != "" {
		line += `,"pause":` + pause
	}
	return line + "}"
}

func itoa(value int) string { return string(rune('0' + value)) }

func TestLegacyEventLogsShowActions(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))

	legacyTask(t, h, "old-tool-failed",
		stateChange(2, "old-tool-failed", "draft", "admission", "submit", ""),
		stateChange(3, "old-tool-failed", "admission", "running", "admission_passed", ""),
		stateChange(4, "old-tool-failed", "running", "awaiting_operator", "tool_failed",
			`{"code":"tool_failed","message":"工具 load_sales 调用失败（共尝试 3 次）：x。已完成 1/10 步。","options":["retry","terminate"]}`),
	)
	toolFailed := h.snapshot("old-tool-failed")
	expectActions(t, toolFailed, "retry=initiator,operator", "terminate=operator")
	expectWaitingFor(t, toolFailed, initiator, operator)
	if toolFailed.Pause.Title != "工具调用失败" {
		t.Fatalf("title = %q", toolFailed.Pause.Title)
	}

	// 旧版超范围意见停在待人工处理，仍由操作员决定
	legacyTask(t, h, "old-needs-human",
		stateChange(2, "old-needs-human", "draft", "admission", "submit", ""),
		stateChange(3, "old-needs-human", "admission", "running", "admission_passed", ""),
		stateChange(4, "old-needs-human", "running", "awaiting_operator", "needs_human",
			`{"code":"rework_needs_human","message":"步骤判断需要人工处理","options":["retry","terminate"]}`),
	)
	needsHuman := h.snapshot("old-needs-human")
	expectActions(t, needsHuman, "retry=operator", "terminate=operator")
	expectWaitingFor(t, needsHuman, operator)
	if !strings.Contains(needsHuman.Pause.Resolution, "操作员") {
		t.Fatalf("resolution = %q", needsHuman.Pause.Resolution)
	}

	// 旧版待审核的暂停原因里 options 没有 terminate，actions 不受影响
	legacyTask(t, h, "old-review",
		stateChange(2, "old-review", "draft", "admission", "submit", ""),
		stateChange(3, "old-review", "admission", "running", "admission_passed", ""),
		stateChange(4, "old-review", "running", "qc", "steps_completed", ""),
		stateChange(5, "old-review", "qc", "awaiting_review", "qc_passed",
			`{"code":"awaiting_review","message":"已通过质检，等待审核人决定。","options":["approve","return"]}`),
	)
	review := h.snapshot("old-review")
	expectActions(t, review, "approve=reviewer", "return=reviewer", "terminate=reviewer")

	summary := h.summary("old-needs-human")
	if summary.StatusText != "退回意见超出自动处理范围" || !sameRoles(summary.WaitingFor, operator) {
		t.Fatalf("摘要 = %+v", summary)
	}

	// 旧任务的命令也按新规则执行
	expectState(t, h.command("old-review", reviewer, cmd(contract.CmdTerminate)), contract.StateTerminated)
	expectState(t, h.command("old-needs-human", operator, cmd(contract.CmdTerminate)), contract.StateTerminated)
}
