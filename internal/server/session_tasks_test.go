// T47：一个会话先后运行多个任务。全部从主接缝验证：真实引擎、剧本模型、临时目录。
package server_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

// runRow GET /api/records 里的一行（只取这张工单关心的字段）。
type runRow struct {
	TaskID        string `json:"taskId"`
	SessionID     string `json:"sessionId"`
	WorkspaceID   string `json:"workspaceId"`
	WorkspaceName string `json:"workspaceName"`
	RunNumber     int    `json:"runNumber"`
	State         string `json:"state"`
	StatusDot     string `json:"statusDot"`
}

// runRecords 以某个角色读记录接口：返回 分组 → 行。
func (h *harness) runRecords(role contract.Role) map[string][]runRow {
	h.t.Helper()
	status, data := h.send(http.MethodGet, "/api/records", role, "", nil)
	if status != http.StatusOK {
		h.t.Fatalf("读记录失败：%d %s", status, data)
	}
	var body struct {
		Groups []struct {
			Key   string   `json:"key"`
			Items []runRow `json:"items"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		h.t.Fatalf("记录不是预期的 JSON：%v\n%s", err, data)
	}
	out := map[string][]runRow{}
	for _, group := range body.Groups {
		out[group.Key] = group.Items
	}
	return out
}

func rowOf(groups map[string][]runRow, taskID string) (string, runRow, bool) {
	for key, items := range groups {
		for _, item := range items {
			if item.TaskID == taskID {
				return key, item, true
			}
		}
	}
	return "", runRow{}, false
}

// firstRun 开一个会话，确认员工、上传样例材料、提交，等任务到待审核。
func (h *harness) firstRun() (sessionID string, taskID string) {
	h.t.Helper()
	created := h.createSession()
	h.confirmed(created.ID, harnessEmployee)
	uploadOK(h, created.ID, a01Files(h.t)...)
	taskID = h.submit(created.ID, week44Form).Session.TaskID
	expectState(h.t, h.settled(taskID), contract.StateAwaitingReview)
	return created.ID, taskID
}

func TestOneSessionRunsTwoTasksOneAfterAnother(t *testing.T) {
	h := newHarness(t, twice(caseScript(t, "A-01")), withScriptedModel())
	sessionID, first := h.firstRun()

	// 第一个任务还没结束：再开新任务被拒绝，原因看得懂，也没有多出任务
	status, data := h.trySubmit(sessionID, initiator, week44Form)
	if status != http.StatusConflict || !strings.Contains(string(data), session.ErrSessionHasTask) ||
		!strings.Contains(string(data), "还没结束") || !strings.Contains(string(data), first) {
		t.Fatalf("有未结束任务时再开：%d %s", status, data)
	}
	if count := h.taskCount(); count != 1 {
		t.Fatalf("被拒绝的提交不该留下任务，任务数 = %d", count)
	}

	// 第一个任务交付后，同一个会话可以开第二个
	expectState(t, h.command(first, reviewer, cmd(contract.CmdApprove)), contract.StateDelivered)
	delivered := h.readSession(sessionID)
	if delivered.Status != session.StatusClosed || delivered.TaskID != first || delivered.TaskState != contract.StateDelivered ||
		len(delivered.TaskIDs) != 1 || delivered.TaskIDs[0] != first {
		t.Fatalf("第一个任务交付后的会话 = %+v", delivered.View)
	}
	second := h.submit(sessionID, week44Form).Session.TaskID
	if second == "" || second == first {
		t.Fatalf("第二个任务 = %q", second)
	}
	expectState(t, h.settled(second), contract.StateAwaitingReview)

	// 会话视图按最新的任务算：状态、等待谁、任务列表
	got := h.readSession(sessionID)
	if got.TaskID != second || got.Status != session.StatusTask || got.TaskState != contract.StateAwaitingReview ||
		len(got.TaskIDs) != 2 || got.TaskIDs[0] != first || got.TaskIDs[1] != second {
		t.Fatalf("会话视图 = %+v", got.View)
	}
	if len(got.WaitingFor) != 1 || got.WaitingFor[0] != reviewer || got.StatusText == "" {
		t.Fatalf("等待谁 = %v，状态文字 = %q", got.WaitingFor, got.StatusText)
	}

	// 状态点按最新的任务和读的人算
	for role, want := range map[contract.Role]string{reviewer: "waiting", initiator: "idle"} {
		item, found := h.workspacesAs(role).findSession(sessionID)
		if !found || item.StatusDot != want || item.TaskID != second {
			t.Fatalf("%s 看到的会话摘要 = %+v，want 状态点 %s", role, item, want)
		}
	}

	// 对话里两个任务的开工位置、质检卡片、审核卡片都在，各自在发生的时间点
	ids := map[string]bool{}
	for _, message := range got.Messages {
		if ids[message.ID] {
			t.Fatalf("消息 ID 重复：%s", message.ID)
		}
		ids[message.ID] = true
	}
	var started, qcs, reviews []session.Card
	deliveredNotices, firstDelivered, secondStarted := 0, -1, -1
	for index, message := range got.Messages {
		if message.Kind == session.KindNotice && message.Text == "任务已交付。" {
			deliveredNotices++
			firstDelivered = index
		}
		card := message.Card
		if card == nil {
			continue
		}
		switch card.Type {
		case session.CardForm:
			if card.Submitted {
				started = append(started, *card)
				if card.TaskID == second {
					secondStarted = index
				}
			}
		case session.CardQC:
			qcs = append(qcs, *card)
		case session.CardReview:
			reviews = append(reviews, *card)
		}
	}
	if len(started) != 2 || started[0].TaskID != first || started[1].TaskID != second {
		t.Fatalf("开工位置（已提交的表单卡）= %+v", started)
	}
	if len(qcs) != 2 || qcs[0].TaskID != first || qcs[1].TaskID != second {
		t.Fatalf("质检卡片 = %+v", qcs)
	}
	if len(reviews) != 2 || len(reviews[0].Actions) != 0 || len(reviews[1].Actions) == 0 {
		t.Fatalf("审核卡片：第一个任务已结束不该有按钮，第二个有：%+v", reviews)
	}
	if deliveredNotices != 1 || firstDelivered >= secondStarted {
		t.Fatalf("第一个任务的“已交付”说明要在第二个任务开工之前：交付说明 %d 条，位置 %d / %d", deliveredNotices, firstDelivered, secondStarted)
	}

	// 记录按任务列出，每个任务一行，带会话、工作区、第几次运行，按各自的状态分组
	groups := h.runRecords(reviewer)
	firstGroup, firstRow, ok1 := rowOf(groups, first)
	secondGroup, secondRow, ok2 := rowOf(groups, second)
	if !ok1 || !ok2 {
		t.Fatalf("记录里要有两个任务：%+v", groups)
	}
	if firstGroup != "delivered" || secondGroup != "review" {
		t.Fatalf("分组：第一个 %s，第二个 %s", firstGroup, secondGroup)
	}
	if firstRow.SessionID != sessionID || secondRow.SessionID != sessionID || firstRow.RunNumber != 1 || secondRow.RunNumber != 2 ||
		firstRow.WorkspaceID == "" || firstRow.WorkspaceName == "" || secondRow.WorkspaceName == "" {
		t.Fatalf("记录行 = %+v / %+v", firstRow, secondRow)
	}
	// 审核人看：第二个任务等他审核；第一个已经交付并且不是最新的，是空闲
	if secondRow.StatusDot != "waiting" || firstRow.StatusDot != "idle" {
		t.Fatalf("审核人看到的状态点：第一个 %s，第二个 %s", firstRow.StatusDot, secondRow.StatusDot)
	}
}

func TestUnreadFollowsTheLatestTask(t *testing.T) {
	h := newHarness(t, twice(caseScript(t, "A-01")), withScriptedModel())
	sessionID, first := h.firstRun()
	expectState(t, h.command(first, reviewer, cmd(contract.CmdApprove)), contract.StateDelivered)

	item, found := h.workspacesAs(initiator).findSession(sessionID)
	if !found || item.StatusDot != "done" || !item.Unread {
		t.Fatalf("第一个任务交付、还没打开过：%+v", item)
	}
	// 开第二个任务：会话回到进行中，旧任务的“已完成未读”不再提示
	second := h.submit(sessionID, week44Form).Session.TaskID
	h.settled(second)
	item, _ = h.workspacesAs(initiator).findSession(sessionID)
	if item.Unread || item.StatusDot == "done" {
		t.Fatalf("第二个任务进行中：%+v", item)
	}
	expectState(t, h.command(second, reviewer, cmd(contract.CmdApprove)), contract.StateDelivered)
	item, _ = h.workspacesAs(initiator).findSession(sessionID)
	if item.StatusDot != "done" || !item.Unread || item.TaskID != second {
		t.Fatalf("第二个任务交付后的会话摘要 = %+v", item)
	}
	_, firstRow, _ := rowOf(h.runRecords(initiator), first)
	if firstRow.StatusDot != "idle" {
		t.Fatalf("早先的任务不再提示已完成未读：%+v", firstRow)
	}
	// 打开会话就算读过
	h.readSession(sessionID)
	if item, _ = h.workspacesAs(initiator).findSession(sessionID); item.Unread || item.StatusDot != "idle" {
		t.Fatalf("打开之后：%+v", item)
	}
}
func TestMessageAfterDeliveryGetsANormalReplyAboutTheLatestTask(t *testing.T) {
	h := newHarness(t, twice(caseScript(t, "A-01")), withScriptedModel())
	sessionID, first := h.firstRun()
	expectState(t, h.command(first, reviewer, cmd(contract.CmdApprove)), contract.StateDelivered)
	second := h.submit(sessionID, week44Form).Session.TaskID
	h.settled(second)
	expectState(t, h.command(second, reviewer, cmd(contract.CmdApprove)), contract.StateDelivered)

	h.setFrontdeskScript(frontdeskScript(
		modelCalls("query_task", map[string]any{}),
		modelCalls("query_task", map[string]any{"taskId": first}),
		modelSays("两个任务都交付了。"),
	))
	h.sayTurn(sessionID, initiator, "现在是什么情况")
	reply := h.saidAfter(sessionID)

	if got := textOf(t, messagesOf(reply), session.KindAssistant); got != "两个任务都交付了。" {
		t.Fatalf("交付后发消息的回复 = %q", got)
	}
	calls := entriesOf(reply.Messages, session.KindToolCalled)
	if len(calls) != 2 || !strings.Contains(jsonOf(t, calls[0].Result), second) || !strings.Contains(jsonOf(t, calls[1].Result), first) {
		t.Fatalf("不带任务号时查最新的任务，带本会话的任务号可以查更早的：%+v", calls)
	}
	if refused := entriesOf(reply.Messages, session.KindToolDenied); len(refused) != 0 {
		t.Fatalf("本会话的任务不该被拒绝：%+v", refused)
	}
}

func jsonOf(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestFirstTaskIsStillReadableAfterTheSecondIsTerminated(t *testing.T) {
	h := newHarness(t, twice(twice(caseScript(t, "A-01"))), withScriptedModel())
	sessionID, first := h.firstRun()
	expectState(t, h.command(first, reviewer, cmd(contract.CmdApprove)), contract.StateDelivered)
	second := h.submit(sessionID, week44Form).Session.TaskID
	h.settled(second)
	expectState(t, h.command(second, reviewer, cmd(contract.CmdTerminate)), contract.StateTerminated)

	got := h.readSession(sessionID)

	if got.TaskID != second || got.TaskState != contract.StateTerminated || got.Status != session.StatusClosed {
		t.Fatalf("以最新的任务为准：%+v", got.View)
	}
	groups := h.runRecords(initiator)
	if group, _, _ := rowOf(groups, first); group != "delivered" {
		t.Fatalf("第一个任务仍是已交付：%s", group)
	}
	if group, row, _ := rowOf(groups, second); group != "terminated" || row.RunNumber != 2 {
		t.Fatalf("第二个任务：%s %+v", group, row)
	}
	// 终止之后会话照常开新任务
	third := h.submit(sessionID, week44Form).Session.TaskID
	h.settled(third)
	if third == "" || len(h.readSession(sessionID).TaskIDs) != 3 {
		t.Fatalf("第三个任务 = %q", third)
	}
}

func TestOldSessionLogWithOneTaskIdReadsAsOneTask(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"), withScriptedModel())
	task := h.createTask(a01Files(t))
	// 旧版本写的会话日志：task_bound 只有一个 taskId，没有任务列表
	dir := filepath.Join(h.appDirs(), "sessions", "s-old-0001")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := strings.Join([]string{
		`{"id":"s1","seq":1,"ts":"2026-10-01T00:00:00Z","kind":"created","workspaceId":"single"}`,
		`{"id":"s2","seq":2,"ts":"2026-10-01T00:00:01Z","kind":"user","role":"initiator","text":"出周报"}`,
		`{"id":"s3","seq":3,"ts":"2026-10-01T00:00:02Z","kind":"employee_set","employee":"` + harnessEmployee + `"}`,
		`{"id":"s4","seq":4,"ts":"2026-10-01T00:00:03Z","kind":"task_bound","taskId":"` + task.ID + `"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "log.jsonl"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}

	got := h.readSession("s-old-0001")

	if got.TaskID != task.ID || len(got.TaskIDs) != 1 || got.TaskIDs[0] != task.ID || got.Status != session.StatusTask {
		t.Fatalf("旧会话日志读作一个任务：%+v", got.View)
	}
	_, row, found := rowOf(h.runRecords(initiator), task.ID)
	if !found || row.SessionID != "s-old-0001" || row.RunNumber != 1 {
		t.Fatalf("旧会话的记录行 = %+v", row)
	}
}
