// T38：工作区列表带会话摘要、记录 tab、状态点、会话重命名与归档。
//
// 主接缝是 HTTP 装置（真实引擎、真实工具、剧本模型、临时目录）。单目录模式下工作区列表只有一项，
// 应用目录模式（工作区登记、“未归属”）在 internal/app。
package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/qc"
)

// sessionSummary GET /api/workspaces 里的会话摘要（T38 新增的字段，加摘要里原有的几项）。
type sessionSummary struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	StatusDot     string   `json:"statusDot"`
	Unread        bool     `json:"unread"`
	Archived      bool     `json:"archived"`
	WorkspaceID   string   `json:"workspaceId"`
	WorkspaceName string   `json:"workspaceName"`
	Employee      string   `json:"employee"`
	TaskID        string   `json:"taskId"`
	StatusText    string   `json:"statusText"`
	UpdatedAt     string   `json:"updatedAt"`
	Waiting       []string `json:"waitingFor"`
}

// workspaceItem GET /api/workspaces 里的一项。
type workspaceItem struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Sessions []sessionSummary `json:"sessions"`
}

// workspaceList GET /api/workspaces 的响应。
type workspaceList struct {
	Workspaces []workspaceItem  `json:"workspaces"`
	Unassigned []sessionSummary `json:"unassigned"`
}

// recordItem GET /api/records 里的一条记录。
type recordItem struct {
	TaskID        string `json:"taskId"`
	SessionID     string `json:"sessionId"`
	Title         string `json:"title"`
	Employee      string `json:"employee"`
	WorkspaceID   string `json:"workspaceId"`
	WorkspaceName string `json:"workspaceName"`
	State         string `json:"state"`
	StatusText    string `json:"statusText"`
	StatusDot     string `json:"statusDot"`
	UpdatedAt     string `json:"updatedAt"`
}

// recordList GET /api/records 的响应。
type recordList struct {
	Groups []struct {
		Key   string       `json:"key"`
		Label string       `json:"label"`
		Items []recordItem `json:"items"`
	} `json:"groups"`
}

func getInto(t *testing.T, h *harness, path string, role contract.Role, into any) {
	t.Helper()
	status, data := h.send(http.MethodGet, path, role, "", nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s：%d %s", path, status, data)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatalf("GET %s 的响应不是预期的 JSON：%v\n%s", path, err, data)
	}
}

// workspacesAs 以某个角色读工作区列表。
func (h *harness) workspacesAs(role contract.Role) workspaceList {
	h.t.Helper()
	var list workspaceList
	getInto(h.t, h, "/api/workspaces", role, &list)
	return list
}

// records 读记录 tab。
func (h *harness) records() recordList {
	h.t.Helper()
	var list recordList
	getInto(h.t, h, "/api/records", contract.RoleInitiator, &list)
	return list
}

// findSession 在分组里找会话。
func (l workspaceList) findSession(sessionID string) (sessionSummary, bool) {
	for _, workspace := range l.Workspaces {
		for _, item := range workspace.Sessions {
			if item.ID == sessionID {
				return item, true
			}
		}
	}
	for _, item := range l.Unassigned {
		if item.ID == sessionID {
			return item, true
		}
	}
	return sessionSummary{}, false
}

// sessionSummaryOf 工作区列表里这个会话的摘要；找不到时测试失败。
func (h *harness) sessionSummaryOf(sessionID string, role contract.Role) sessionSummary {
	h.t.Helper()
	list := h.workspacesAs(role)
	item, ok := list.findSession(sessionID)
	if !ok {
		h.t.Fatalf("工作区列表里没有会话 %s：%+v", sessionID, list)
	}
	return item
}

// group 一组记录里的 “状态点/会话”，按列表顺序。
func (l recordList) group(key string) ([]string, bool) {
	for _, g := range l.Groups {
		if g.Key == key {
			out := make([]string, 0, len(g.Items))
			for _, item := range g.Items {
				out = append(out, item.StatusDot+"/"+item.SessionID)
			}
			return out, true
		}
	}
	return nil, false
}

// find 任务在记录里的那一条。
func (l recordList) find(taskID string) (recordItem, bool) {
	for _, group := range l.Groups {
		for _, item := range group.Items {
			if item.TaskID == taskID {
				return item, true
			}
		}
	}
	return recordItem{}, false
}

// patch 发一个 PATCH 请求，返回状态码和响应体。
func (h *harness) patch(path string, body map[string]any) (int, []byte) {
	h.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.send(http.MethodPatch, path, contract.RoleInitiator, "application/json", bytes.NewReader(raw))
}

func (h *harness) sessionPatch(id string, body map[string]any) map[string]any {
	h.t.Helper()
	status, data := h.patch("/api/sessions/"+id, body)
	if status != http.StatusOK {
		h.t.Fatalf("PATCH /api/sessions/%s 失败：%d %s", id, status, data)
	}
	return decode[map[string]any](h.t, data)
}

// askedSession 开一个会话，说第一句话（员工选谁由剧本决定）。
func (h *harness) askedSession() string {
	h.t.Helper()
	created := h.createSession()
	h.say(created.ID, contract.RoleInitiator, "帮我出第 44 周的经营周报")
	return created.ID
}

// draftedSession 走到“员工已接手、等提交”的会话。
func (h *harness) draftedSession() string {
	h.t.Helper()
	id := h.askedSession()
	h.confirm(id, contract.RoleInitiator, harnessEmployee)
	return id
}

// submittedSession 开一个会话，提交任务并等它停稳。
func (h *harness) submittedSession(files []upload) (string, contract.TaskSnapshot) {
	h.t.Helper()
	return h.submittedSessionOf(h.draftedSession(), files)
}

func (h *harness) submittedSessionOf(sessionID string, files []upload) (string, contract.TaskSnapshot) {
	h.t.Helper()
	h.uploadTo(sessionID, files)
	reply := h.submit(sessionID, week44Form)
	if reply.Session == nil || reply.Session.TaskID == "" {
		h.t.Fatalf("提交后会话应绑定任务：%+v", reply)
	}
	return sessionID, h.settled(reply.Session.TaskID)
}

// finish 把已停稳的任务推到终态：待审核就通过；停到待人工处理（比如剧本用完了模型调用失败）就由操作员终止。
func (h *harness) finish(taskID string) {
	h.t.Helper()
	switch h.snapshot(taskID).State {
	case contract.StateAwaitingReview:
		h.command(taskID, contract.RoleReviewer, map[string]any{"type": "approve"})
	case contract.StateDelivered, contract.StateTerminated:
	default:
		h.command(taskID, contract.RoleOperator, map[string]any{"type": "terminate"})
	}
}

// runningSession 提交任务，等某一步的工具或模型开始（这时任务停在某个“运行中”的状态）。
func (h *harness) runningSession(sessionID string, entered <-chan struct{}) (string, contract.TaskSnapshot) {
	h.t.Helper()
	h.uploadTo(sessionID, a01Files(h.t))
	reply := h.submit(sessionID, week44Form)
	waitFor(h.t, entered, "任务进入运行中")
	return sessionID, h.snapshot(reply.Session.TaskID)
}

// --- 状态点：四种各一个用例 ---

func TestStatusDotWaitingForMe(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	// 待确认员工：只有发起人能确认
	reception := h.createSession()
	if got := h.sessionSummaryOf(reception.ID, contract.RoleInitiator); got.StatusDot != "waiting" || got.Unread {
		t.Fatalf("待确认员工的会话状态点应是等你处理：%+v", got)
	}
	// 待补充：只有发起人能补材料
	supplement, task := h.submittedSession([]upload{sampleFile(t, "销售明细.xlsx")})
	expectState(t, task, contract.StateAwaitingInitiator)
	if got := h.sessionSummaryOf(supplement, contract.RoleInitiator); got.StatusDot != "waiting" {
		t.Fatalf("待补充的会话状态点应是等你处理：%+v", got)
	}
	// 待审核：审核人能通过或退回
	review, task := h.submittedSession(a01Files(t))
	expectState(t, task, contract.StateAwaitingReview)
	got := h.sessionSummaryOf(review, contract.RoleReviewer)
	if got.StatusDot != "waiting" || len(got.Waiting) != 1 || got.Waiting[0] != "reviewer" {
		t.Fatalf("待审核的会话对审核人应是等你处理，并在等审核人：%+v", got)
	}
}

func TestStatusDotWaitingDependsOnTheRole(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	// 待审核只归审核人：发起人和操作员看到的都不是“等你处理”，也不是“已完成未读”
	review, _ := h.submittedSession(a01Files(t))
	if got := h.sessionSummaryOf(review, contract.RoleInitiator); got.StatusDot != "idle" || got.Unread {
		t.Fatalf("发起人看待审核的会话应是空闲：%+v", got)
	}
	if got := h.sessionSummaryOf(review, contract.RoleOperator); got.StatusDot != "idle" {
		t.Fatalf("操作员看待审核的会话应是空闲：%+v", got)
	}
}

// lockedProvider 在前台 Agent 调模型时卡住，直到测试放行。
type lockedProvider struct {
	inner   agent.ModelProvider
	step    string
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (p *lockedProvider) Kind() string { return p.inner.Kind() }

func (p *lockedProvider) Resolve(stepID string, ref *contract.ModelRef) (*agent.ResolvedStepModel, error) {
	resolved, err := p.inner.Resolve(stepID, ref)
	if err != nil || stepID != p.step || resolved == nil {
		return resolved, err
	}
	stream := resolved.Stream
	resolved.Stream = func(ctx context.Context, model agent.Model, request agent.StreamRequest, options agent.StreamOptions) (*agent.AssistantMessage, error) {
		p.once.Do(func() {
			close(p.entered)
			<-p.release
		})
		return stream(ctx, model, request, options)
	}
	return resolved, nil
}

func TestStatusDotRunningWhileFrontdeskReplies(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	gate := &lockedProvider{inner: h.model, step: agent.FrontdeskStepID, entered: make(chan struct{}), release: make(chan struct{})}
	h.frontdesk = gate
	h.baseURL = h.serve()
	created := h.createSession()

	asked := make(chan int, 1)
	go func() {
		raw, _ := json.Marshal(map[string]string{"text": "帮我出第 44 周的经营周报"})
		status, _ := h.send(http.MethodPost, "/api/sessions/"+created.ID+"/messages", contract.RoleInitiator, "application/json", bytes.NewReader(raw))
		asked <- status
	}()
	waitFor(t, gate.entered, "前台 Agent 开始回复")
	// 前台 Agent 正在回复：运行中压过“等你处理”
	if got := h.sessionSummaryOf(created.ID, contract.RoleInitiator); got.StatusDot != "running" {
		t.Fatalf("前台 Agent 回复中的会话状态点应是运行中：%+v", got)
	}
	// 同一个“忙”：这时再发消息得到 session_busy
	raw, _ := json.Marshal(map[string]string{"text": "再问一句"})
	busy, data := h.send(http.MethodPost, "/api/sessions/"+created.ID+"/messages", contract.RoleInitiator, "application/json", bytes.NewReader(raw))
	var failure struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(data, &failure)
	if busy != http.StatusConflict || failure.Error != "session_busy" {
		t.Fatalf("回复中的会话应报 session_busy：%d %s", busy, data)
	}
	close(gate.release)
	if status := <-asked; status != http.StatusOK {
		t.Fatalf("回复失败：%d", status)
	}
	if got := h.sessionSummaryOf(created.ID, contract.RoleInitiator); got.StatusDot != "waiting" {
		t.Fatalf("回复结束后待确认员工的会话应回到等你处理：%+v", got)
	}
}

// heldQC 让质检停住，直到测试放行。
type gatedQC struct {
	entered chan struct{}
	// once 第一次质检时通知测试
	once        sync.Once
	releaseOnce sync.Once
	mu          sync.Mutex
	// release 当前的放行通道
	release chan struct{}
}

func (q *gatedQC) Run(input contract.QcInput) []contract.QcCheckResult {
	q.once.Do(func() { close(q.entered) })
	<-q.gate()
	return qc.NewEngine().Run(input)
}

// gate 当前的放行通道。
func (q *gatedQC) gate() chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.release
}

// releaseGate 换一条已经关闭的放行通道：卡住的质检马上放行，之后进来的质检也马上放行。只换一次。
func releaseGate(q *gatedQC) {
	q.releaseOnce.Do(func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		close(q.release)
		q.release = make(chan struct{})
		close(q.release)
	})
}

func TestStatusDotRunningWhileTaskExecutes(t *testing.T) {
	t.Run("admission", func(t *testing.T) {
		tool, holding := holdTool(contract.ToolValidateSubmission)
		h := newHarness(t, caseScript(t, "A-01"), holding)
		defer tool.open()
		sessionID := h.draftedSession()
		id, task := h.runningSession(sessionID, tool.entered)
		expectState(t, task, contract.StateAdmission)
		if got := h.sessionSummaryOf(id, contract.RoleInitiator); got.StatusDot != "running" || got.Unread {
			t.Fatalf("准入校验中的会话状态点应是运行中：%+v", got)
		}
	})
	t.Run("running", func(t *testing.T) {
		model, holding := holdModel("match_causes", false)
		h := newHarness(t, caseScript(t, "A-01"), holding)
		sessionID := h.draftedSession()
		id, task := h.runningSession(sessionID, model.entered)
		expectState(t, task, contract.StateRunning)
		got := h.sessionSummaryOf(id, contract.RoleInitiator)
		// 放行并等任务停稳：临时目录随后要清理，不能留着正在写的任务
		close(model.release)
		h.settled(task.ID)
		if got.StatusDot != "running" {
			t.Fatalf("执行中的会话状态点应是运行中：%+v", got)
		}
	})
	t.Run("qc", func(t *testing.T) {
		held := &gatedQC{entered: make(chan struct{}), release: make(chan struct{})}
		h := newHarness(t, caseScript(t, "A-01"), withQC(held))
		sessionID := h.draftedSession()
		id, task := h.runningSession(sessionID, held.entered)
		expectState(t, task, contract.StateQC)
		got := h.sessionSummaryOf(id, contract.RoleInitiator)
		close(held.release)
		h.settled(task.ID)
		if got.StatusDot != "running" {
			t.Fatalf("质检中的会话状态点应是运行中：%+v", got)
		}
	})
}

// open 放行所有等待中的工具调用（只放一次，重复调用没关系）。
func (c *heldTools) open() { c.once.Do(func() { close(c.release) }) }
// --- 已读：记录在会话日志里，打开会话时写入 ---

func TestOpenedSessionWritesReadToItsLog(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	review, _ := h.submittedSession(a01Files(t))
	h.readSession(review)
	logPath := filepath.Join(h.workspace, "sessions", review, "log.jsonl")
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"kind":"read"`) {
		t.Fatalf("会话日志里应有已读：\n%s", raw)
	}
	// 已读随日志保存：重启后仍是已读
	h.baseURL = h.serve()
	if got := h.sessionSummaryOf(review, contract.RoleReviewer); got.StatusDot != "waiting" || got.Unread {
		t.Fatalf("重启后已读仍有效：%+v", got)
	}
}

// --- 会话：重命名、归档 ---

func TestSessionRenameAndArchive(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	id := h.draftedSession()

	renamed := h.sessionPatch(id, map[string]any{"title": "第 44 周华东周报"})
	if session, _ := renamed["session"].(map[string]any); session["title"] != "第 44 周华东周报" {
		t.Fatalf("重命名后应返回新标题：%+v", renamed)
	}
	if got := h.sessionSummaryOf(id, contract.RoleInitiator); got.Title != "第 44 周华东周报" {
		t.Fatalf("工作区列表里应是新标题：%+v", got)
	}
	if status, data := h.patch("/api/sessions/"+id, map[string]any{"title": "  "}); status != http.StatusBadRequest {
		t.Fatalf("空标题应被拒：%d %s", status, data)
	}

	h.sessionPatch(id, map[string]any{"archived": true})
	if listed := sessionListOf(h, ""); len(listed) != 0 {
		t.Fatalf("归档后默认列表里不应出现：%+v", listed)
	}
	if listed := sessionListOf(h, "?archived=1"); len(listed) != 1 {
		t.Fatalf("带 archived=1 时应列出归档的会话：%+v", listed)
	}
	if listed := h.workspacesAs(contract.RoleInitiator).Workspaces[0].Sessions; len(listed) != 0 {
		t.Fatalf("工作区列表默认不列归档的会话：%+v", listed)
	}
	h.sessionPatch(id, map[string]any{"archived": false})
	if listed := sessionListOf(h, ""); len(listed) != 1 {
		t.Fatalf("取消归档后应回到默认列表：%+v", listed)
	}
}

// sessionListOf GET /api/sessions（可以带查询参数），只取归档标记和状态点。
func sessionListOf(h *harness, query string) []struct {
	ID       string `json:"id"`
	Archived bool   `json:"archived"`
} {
	h.t.Helper()
	var listed []struct {
		ID       string `json:"id"`
		Archived bool   `json:"archived"`
	}
	getInto(h.t, h, "/api/sessions"+query, contract.RoleInitiator, &listed)
	return listed
}

// --- 记录：跨工作区、按状态分组 ---

// alwaysQC 质检不卡住：记录分组只关心任务停在哪个状态。
type alwaysQC struct{}

func (alwaysQC) Run(input contract.QcInput) []contract.QcCheckResult { return qc.NewEngine().Run(input) }

func TestRecordsGroupedByState(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	delivered, deliveredTask := h.submittedSession(a01Files(t))
	h.finish(deliveredTask.ID)
	supplement, _ := h.submittedSession([]upload{sampleFile(t, "销售明细.xlsx")})
	review, reviewTask := h.submittedSession(a01Files(t))
	terminated, terminatedTask := h.submittedSession([]upload{sampleFile(t, "销售明细.xlsx")})
	h.command(terminatedTask.ID, contract.RoleInitiator, map[string]any{"type": "terminate"})
	// 只有对话、没开工的会话不算记录
	h.createSession()
	h.draftedSession()

	list := h.records()
	// 记录里的状态点按读的人的角色算：待审核只等审核人，发起人看到的是空闲
	got := map[string][]string{}
	for _, group := range []string{"running", "supplement", "review", "returned", "delivered", "terminated"} {
		items, ok := list.group(group)
		if !ok {
			t.Fatalf("记录里应有这一组（空的也列出来）：%s：%+v", group, list)
		}
		got[group] = items
	}
	want := map[string][]string{
		"running":    {},
		"supplement": {"waiting/" + supplement},
		"review":     {"idle/" + review},
		"returned":   {},
		"delivered":  {"done/" + delivered},
		"terminated": {"done/" + terminated},
	}
	// 待审核的任务可能还在列表的“运行中”里（引擎刚换掉状态），组和状态点以前者为准
	for _, key := range []string{"supplement", "delivered", "terminated"} {
		if fmt.Sprint(got[key]) != fmt.Sprint(want[key]) {
			t.Fatalf("记录分组 %s = %v，want %v（全部：%v）", key, got[key], want[key], got)
		}
	}
	reviewed := append(append([]string{}, got["review"]...), got["running"]...)
	if !contains(reviewed, "idle/"+review) || len(reviewed) != 1 {
		t.Fatalf("待审核的任务刚好出现一次，对发起人是空闲（全部：%v）", got)
	}
	if len(got["returned"]) != 0 {
		t.Fatalf("没有退回的任务：%v", got["returned"])
	}
	var item recordItem
	found := false
	for _, g := range list.Groups {
		for _, candidate := range g.Items {
			if candidate.TaskID == reviewTask.ID {
				item, found = candidate, true
			}
		}
	}
	if !found || item.SessionID != review || item.WorkspaceName == "" || item.Employee != harnessEmployee ||
		item.StatusText == "" || item.Title == "" || item.UpdatedAt == "" {
		t.Fatalf("记录每项要带会话、工作区名、员工和状态：%+v", item)
	}
}

// contains 列表里有没有这一项。
func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestRecordsListRunningTasks(t *testing.T) {
	tool, holding := holdTool(contract.ToolValidateSubmission)
	h := newHarness(t, caseScript(t, "A-01"), holding)
	sessionID := h.draftedSession()
	h.uploadTo(sessionID, a01Files(t))
	reply := h.submit(sessionID, week44Form)
	taskID := reply.Session.TaskID
	waitFor(t, tool.entered, "准入校验开始")
	list := h.records()
	state := h.snapshot(taskID).State
	// 放行并等任务停稳：临时目录随后要清理，不能留着正在写的任务
	tool.open()
	h.settled(taskID)
	running, _ := list.group("running")
	if state != contract.StateAdmission || !contains(running, "running/"+sessionID) {
		t.Fatalf("准入校验中的任务应在进行中组，状态点是运行中（%s）：%v", state, running)
	}
}

func TestRecentTasksAndSessionsShareTheRecords(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	delivered, deliveredTask := h.submittedSession(a01Files(t))
	if _, err := h.engine.WaitForSettled(deliveredTask.ID); err != nil {
		t.Fatal(err)
	}
	h.command(deliveredTask.ID, contract.RoleReviewer, map[string]any{"type": "approve"})
	review, reviewTask := h.submittedSession([]upload{sampleFile(t, "销售明细.xlsx")})
	// 最近刚停下的任务：引擎换掉状态、任务列表还留着旧的一份，
	// 会话摘要按最新的快照算，记录里也一样，不会闪一下“运行中”
	got := h.sessionSummaryOf(review, contract.RoleInitiator)
	if got.StatusDot != "waiting" || got.TaskID != reviewTask.ID {
		t.Fatalf("刚停下的待补充任务，会话摘要应是等你处理：%+v", got)
	}
	list := h.records()
	item, found := list.find(reviewTask.ID)
	if !found || item.StatusDot != "waiting" {
		t.Fatalf("记录里同样的任务也是等你处理：%+v", item)
	}
	if done, _ := list.group("delivered"); !contains(done, "done/"+delivered) {
		t.Fatalf("已交付的任务在已交付组：%v", done)
	}
}

func TestRecordsShowTheTaskStateTheEngineReports(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	sessionID := h.draftedSession()
	h.uploadTo(sessionID, a01Files(t))
	reply := h.submit(sessionID, week44Form)
	taskID := reply.Session.TaskID
	task := h.settled(taskID)
	expectState(t, task, contract.StateAwaitingReview)
	list := h.records()
	item, found := list.find(taskID)
	if !found {
		t.Fatalf("待审核的任务也在记录里（归在它被读到的那一组）：%+v", list)
	}
	if item.State != string(task.State) || item.StatusText == "" || item.SessionID != sessionID || item.WorkspaceName == "" {
		t.Fatalf("记录要带任务的当前状态、会话和工作区名：%+v", item)
	}
	if reviewed, _ := list.group("review"); !contains(reviewed, "idle/"+sessionID) {
		t.Fatalf("读的人是发起人：待审核的任务对他是空闲：%v", reviewed)
	}
	// 审核人看到的是等你处理
	status, data := h.send(http.MethodGet, "/api/records", contract.RoleReviewer, "", nil)
	if status != http.StatusOK {
		t.Fatalf("读取记录失败：%d %s", status, data)
	}
	reviewer := decode[recordList](t, data)
	if reviewed, _ := reviewer.group("review"); !contains(reviewed, "waiting/"+sessionID) {
		t.Fatalf("审核人记录里待审核的任务应是等你处理：%v", reviewed)
	}
}