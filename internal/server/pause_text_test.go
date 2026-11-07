// 面向人的暂停说明和任务摘要：都是读取时算出的投影，不写入事件。
package server_test

import (
	"strings"
	"testing"

	"digital-employee/internal/contract"
)

func TestToolFailurePauseSpeaksPlainly(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	h.setToolFaults(map[string]string{contract.ToolLoadSales: "error"})
	task := h.createTask(a01Files(t))
	paused := h.command(task.ID, initiator, cmd(contract.CmdSubmit))

	pause := paused.Pause
	if pause.Title != "工具调用失败" {
		t.Fatalf("title = %q", pause.Title)
	}
	if !strings.Contains(pause.Resolution, "重试") {
		t.Fatalf("resolution 没说下一步：%q", pause.Resolution)
	}
	if !strings.Contains(pause.Message, "读取并清洗销售数据") {
		t.Fatalf("message 应说明卡在哪一步：%q", pause.Message)
	}
	for _, technical := range []string{contract.ToolLoadSales, "故障注入"} {
		if strings.Contains(pause.Message, technical) {
			t.Fatalf("message 不应含技术细节 %q：%q", technical, pause.Message)
		}
	}
	if pause.Detail["tool"] != contract.ToolLoadSales || !strings.Contains(pause.Detail["error"].(string), "故障注入") {
		t.Fatalf("技术细节应在 detail：%+v", pause.Detail)
	}
}

func TestPauseTextFollowsCodeAndDetail(t *testing.T) {
	cases := []struct {
		name       string
		script     string
		edit       harnessOption
		files      func(t *testing.T) []upload
		role       contract.Role
		title      string
		resolution string
	}{
		{
			name: "缺销售明细", script: "A-01",
			files: func(*testing.T) []upload { return nil },
			title: "缺少销售明细", resolution: "上传",
		},
		{
			name: "缺纪要", script: "A-01",
			files: func(t *testing.T) []upload { return []upload{sampleFile(t, "销售明细.xlsx")} },
			title: "缺少本周纪要", resolution: "无纪要继续",
		},
		{
			name: "模型调用超限", script: "A-01", edit: withWorkflowEdit("modelCalls: 30", "modelCalls: 1"),
			files: a01Files,
			title: "模型调用次数已达上限", resolution: "操作员",
		},
		{
			name: "质检用尽", script: "S-24",
			files: a01Files,
			title: "质检未通过", resolution: "操作员",
		},
		{
			name: "待审核", script: "A-01",
			files: a01Files,
			title: "待审核", resolution: "通过",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			options := []harnessOption{}
			if testCase.edit != nil {
				options = append(options, testCase.edit)
			}
			h := newHarness(t, caseScript(t, testCase.script), options...)
			task := h.createTask(testCase.files(t))
			paused := h.command(task.ID, initiator, cmd(contract.CmdSubmit))
			if paused.Pause.Title != testCase.title {
				t.Fatalf("title = %q，want %q", paused.Pause.Title, testCase.title)
			}
			if !strings.Contains(paused.Pause.Resolution, testCase.resolution) {
				t.Fatalf("resolution = %q，want 含 %q", paused.Pause.Resolution, testCase.resolution)
			}
		})
	}
}

func TestOutOfScopePauseTellsReviewerWhatToDo(t *testing.T) {
	_, _, returned := returnedOutOfScope(t)
	if returned.Pause.Title != "退回意见超出自动处理范围" {
		t.Fatalf("title = %q", returned.Pause.Title)
	}
	if !strings.Contains(returned.Pause.Resolution, "修改意见") || !strings.Contains(returned.Pause.Resolution, "通过") {
		t.Fatalf("resolution = %q", returned.Pause.Resolution)
	}
	if got := strings.Join(returned.Pause.Options, ","); got != "approve,return,terminate" {
		t.Fatalf("options = %s", got)
	}
}

func TestSnapshotOptionsFollowActions(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	task := h.createTask(a01Files(t))
	review := h.command(task.ID, initiator, cmd(contract.CmdSubmit))
	if got := strings.Join(review.Pause.Options, ","); got != "approve,return,terminate" {
		t.Fatalf("options = %s", got)
	}
}

func TestProjectionsAreNotWrittenToEvents(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	h.setToolFaults(map[string]string{contract.ToolLoadSales: "error"})
	task := h.createTask(a01Files(t))
	h.command(task.ID, initiator, cmd(contract.CmdSubmit))

	events := h.eventsJSON(task.ID)
	for _, field := range []string{`"actions"`, `"waitingFor"`, `"statusText"`, `"title"`, `"resolution"`} {
		if strings.Contains(events, field) {
			t.Fatalf("事件里不应出现 %s", field)
		}
	}
}

func TestSummaryShowsStatusAndWaitingFor(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	h.setToolFaults(map[string]string{contract.ToolLoadSales: "error"})
	task := h.createTask(a01Files(t))

	draft := h.summary(task.ID)
	if draft.StatusText != "草稿" || !sameRoles(draft.WaitingFor, initiator) {
		t.Fatalf("草稿摘要 = %+v", draft)
	}

	h.command(task.ID, initiator, cmd(contract.CmdSubmit))
	paused := h.summary(task.ID)
	if paused.StatusText != "工具调用失败" || !sameRoles(paused.WaitingFor, initiator, operator) {
		t.Fatalf("暂停摘要 = %+v", paused)
	}

	h.setToolFaults(map[string]string{})
	h.command(task.ID, operator, cmd(contract.CmdRetry))
	h.command(task.ID, reviewer, cmd(contract.CmdApprove))
	delivered := h.summary(task.ID)
	if delivered.StatusText != "已交付" || len(delivered.WaitingFor) != 0 {
		t.Fatalf("已交付摘要 = %+v", delivered)
	}
}

func sameRoles(got []contract.Role, want ...contract.Role) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
