// 状态点与记录（Spec 0006“工作区与记录接口”）。
//
// 状态点由后端按读的人的角色算出，前端只画不推导。优先级：等你处理 → 运行中 → 已完成未读 → 空闲。
// “运行中”里的“前台 Agent 正在回复”就是这一轮还在跑（这时人的操作会得到 session_busy，发消息排进这一轮）。
package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"digital-employee/internal/contract"
)

// 状态点。
const (
	StatusDotWaiting = "waiting" // 等你处理
	StatusDotRunning = "running" // 运行中
	StatusDotDone    = "done"    // 已完成未读
	StatusDotIdle    = "idle"    // 空闲
)

// 记录分组，按侧栏“记录”tab 的顺序。
const (
	RecordRunning    = "running"    // 进行中
	RecordSupplement = "supplement" // 待补充
	RecordReview     = "review"     // 待审核
	RecordReturned   = "returned"   // 已退回
	RecordDelivered  = "delivered"  // 已交付
	RecordTerminated = "terminated" // 已终止
)

// RecordGroupLabels 记录分组的显示名。
var RecordGroupLabels = map[string]string{
	RecordRunning:    "进行中",
	RecordSupplement: "待补充",
	RecordReview:     "待审核",
	RecordReturned:   "已退回",
	RecordDelivered:  "已交付",
	RecordTerminated: "已终止",
}

// RecordGroups 全部分组，顺序固定（空的组也列出来）。
var RecordGroups = []string{RecordRunning, RecordSupplement, RecordReview, RecordReturned, RecordDelivered, RecordTerminated}

// Summary 侧栏里的一条会话，带后端算出的状态点。
type Summary struct {
	View
	// StatusDot 等你处理 / 运行中 / 已完成未读 / 空闲
	StatusDot string `json:"statusDot"`
	// Unread 任务交付或终止之后还没打开过
	Unread bool `json:"unread"`
	// Archived 归档的会话默认不列
	Archived bool `json:"archived"`
	// WorkspaceName 工作区名；工作区已取消登记时是原来的名字
	WorkspaceName string `json:"workspaceName"`
}

// DotFor 会话现在对某个角色的状态点（记录 tab 按读的人现算；没绑任务的会话是空闲）。
func (m *Manager) DotFor(id string, role contract.Role) string {
	if id == "" {
		return StatusDotIdle
	}
	if role == contract.RoleInitiator && m.pendingPermission(id) {
		// 权限确认在等发起人答复
		return StatusDotWaiting
	}
	if m.runningOf(id) {
		// 前台 Agent 正在回复
		return StatusDotRunning
	}
	current, err := m.load(id)
	if err != nil {
		return StatusDotIdle
	}
	dot, _ := m.StatusDot(id, m.view(current), role)
	return dot
}

// SummaryOf 一个会话的摘要（按读的人的角色算状态点）；写操作之后回给它，界面不用再读一次列表。
func (m *Manager) SummaryOf(id string, role contract.Role) (*Summary, error) {
	if strings.HasPrefix(id, legacyPrefix) {
		detail, err := m.getLegacy(id)
		if err != nil {
			return nil, err
		}
		return &Summary{View: detail.View, StatusDot: StatusDotIdle}, nil
	}
	current, err := m.load(id)
	if err != nil {
		return nil, err
	}
	summary := m.summaryOf(m.stateOf(current), role, nil)
	return &summary, nil
}

// Record 记录 tab 里的一条：所有工作区里已开工的任务。
type Record struct {
	TaskID     string             `json:"taskId"`
	Group      string             `json:"group"`
	Title      string             `json:"title"`
	Employee   string             `json:"employee"`
	State      contract.TaskState `json:"state"`
	StatusText string             `json:"statusText"`
	StatusDot  string             `json:"statusDot"`
	// SessionID 任务所在的会话；直接从任务接口建的任务没有会话
	SessionID string `json:"sessionId,omitempty"`
	// WorkspaceID、WorkspaceName 任务属于哪个工作区
	WorkspaceID   string `json:"workspaceId"`
	WorkspaceName string `json:"workspaceName"`
	UpdatedAt     string `json:"updatedAt"`
}

// StatusDot 状态点。role 是读列表的人当前的角色（按它算“等你处理”）：
//  1. 等你处理：待确认员工、待填表提交、待补充、待审核或已退回，并且这个角色能处理；
//  2. 运行中：前台 Agent 正在回复，或任务处于准入校验、执行中、质检中；
//  3. 已完成未读：任务交付或终止之后还没打开过；
//  4. 空闲。
//
// 待人工处理不是发起人、审核人能推进的状态（只有工具故障、模型不可用时发起人能重试），
// 不点亮“等你处理”，由操作员看记录 tab。
func (m *Manager) StatusDot(id string, view *View, role contract.Role) (string, bool) {
	if role == contract.RoleInitiator && m.pendingPermission(id) {
		return StatusDotWaiting, false
	}
	if m.busy(id) {
		return StatusDotRunning, false
	}
	switch view.Status {
	case StatusReception, StatusDrafting:
		// 确认员工、填表提交都只接受发起人
		if role == contract.RoleInitiator {
			return StatusDotWaiting, false
		}
	case StatusTask:
		switch view.TaskState {
		case contract.StateAwaitingInitiator:
			if role == contract.RoleInitiator {
				return StatusDotWaiting, false
			}
		case contract.StateAwaitingReview:
			if role == contract.RoleReviewer {
				return StatusDotWaiting, false
			}
		}
		if runningStates[view.TaskState] {
			return StatusDotRunning, false
		}
	case StatusClosed:
		if view.Unread {
			return StatusDotDone, true
		}
	}
	return StatusDotIdle, false
}

// runningStates 任务在这些状态里时会话是“运行中”。
var runningStates = map[contract.TaskState]bool{
	contract.StateAdmission: true,
	contract.StateRunning:   true,
	contract.StateQC:        true,
}

// busy 前台 Agent 正在回复：这时再发消息排进这一轮。
func (m *Manager) busy(id string) bool {
	return m.runningOf(id)
}

// Summarize 把会话投影成侧栏里的摘要。
// workspaceName 工作区 ID → 名字；includeArchived 为 false 时去掉归档的会话。按更新时间倒序。
func (m *Manager) Summarize(role contract.Role, workspaceName func(id string) string, includeArchived bool) ([]Summary, error) {
	out := []Summary{}
	current, err := m.states()
	if err != nil {
		return nil, err
	}
	bound := map[string]bool{}
	for _, state := range current {
		bound[state.current.taskID] = true
		if state.current.archived && !includeArchived {
			continue
		}
		out = append(out, m.summaryOf(state, role, workspaceName))
	}
	// 由旧任务推导出的只读会话（Spec 0006 之前的任务没有工作区，算未归属）
	for _, summary := range m.deps.Engine.List() {
		if bound[summary.ID] {
			continue
		}
		out = append(out, m.summaryOf(sessionState{view: m.legacyStatus(summary)}, role, workspaceName))
	}
	sort.SliceStable(out, func(i, j int) bool { return parseTime(out[i].UpdatedAt).After(parseTime(out[j].UpdatedAt)) })
	return out, nil
}

// sessionState 一个会话回放后的状态和它的投影。
type sessionState struct {
	current *record
	view    *View
}

// states 全部会话的当前状态（不带工作区名，和工作区无关）。
func (m *Manager) states() ([]sessionState, error) {
	out := []sessionState{}
	for _, id := range m.visibleIDs() {
		current, err := m.store.read(id)
		if err != nil {
			return nil, err
		}
		if current == nil {
			continue
		}
		out = append(out, m.stateOf(current))
	}
	return out, nil
}

// stateOf 会话回放结果加上它的投影；状态点先用缓存，只对“已完成未读”这样随任务走的状态按快照现算。
func (m *Manager) stateOf(current *record) sessionState {
	view := m.cachedView(current)
	if view.Status == StatusClosed {
		view.Unread = current.readUpTo < current.lastSeq()
	}
	return sessionState{current: current, view: view}
}

// legacyStatus 由旧任务推导出的会话的状态：没有会话日志，已完成没打开过就仍是进行中（已完成未读）。
func (m *Manager) legacyStatus(summary contract.TaskSummary) *View {
	view := m.legacyView(summary)
	if contract.IsTerminal(view.TaskState) && !m.legacySeen(view.ID) {
		view.Status = StatusTask
		view.Unread = true
	}
	return view
}

// legacySeen 旧任务推导出的会话打开过没有（它们没有会话日志，打开记在内存里）。
func (m *Manager) legacySeen(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seenLegacy[id]
}

// seenLegacy 打开过的旧任务会话。
func (m *Manager) markLegacySeen(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seenLegacy == nil {
		m.seenLegacy = map[string]bool{}
	}
	m.seenLegacy[id] = true
}

func (m *Manager) summaryOf(state sessionState, role contract.Role, workspaceName func(id string) string) Summary {
	view := *state.view
	if state.current != nil && state.current.taskID == "" {
		view.Unread = false
	}
	dot, unread := m.StatusDot(state.view.ID, &view, role)
	out := Summary{View: view, StatusDot: dot, Unread: unread}
	if state.current != nil {
		out.Archived = state.current.archived
	}
	if workspaceName != nil && view.WorkspaceID != "" {
		out.WorkspaceName = workspaceName(view.WorkspaceID)
	}
	if out.Archived {
		// 归档的会话不再是“已完成未读”
		out.StatusDot = StatusDotIdle
		out.Unread = false
	}
	return out
}

// visibleIDs 会话目录里的全部会话：隔一会儿重新读目录，新建的会话才看得到。
func (m *Manager) visibleIDs() []string {
	now := m.deps.Now()
	m.mu.Lock()
	if m.seen != nil && now.Sub(m.scannedAt) < m.scanEvery {
		defer m.mu.Unlock()
		return sortedKeys(m.seen)
	}
	m.seen = map[string]time.Time{}
	for _, entry := range m.store.entries() {
		m.seen[entry.ID] = entry.ModTime
	}
	m.scannedAt = now
	m.scanEvery = sessionScanEvery
	m.mu.Unlock()
	return sortedKeys(m.seen)
}

// cachedView 会话的投影：消息条数没变就沿用上一次的（侧栏每一两秒读一次全部会话，不必每次回放日志）。
func (m *Manager) cachedView(current *record) *View {
	m.mu.Lock()
	cached, seen := m.views[current.id]
	m.mu.Unlock()
	if seen && cached.at == len(current.messages) && cached.updatedAt == current.updatedAt {
		return &cached.view
	}
	view := m.view(current)
	m.mu.Lock()
	if m.views == nil {
		m.views = map[string]cachedView{}
	}
	// 列表里只留最近的这一份：消息条数决定它还能不能用，不必都留着
	m.views[current.id] = cachedView{at: len(current.messages), updatedAt: current.updatedAt, view: *view}
	m.mu.Unlock()
	return view
}

func sortedKeys[V any](items map[string]V) []string {
	out := make([]string, 0, len(items))
	for key := range items {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// MarkRead 会话打开了：已读写进会话日志。任务交付或终止之后的新内容才算没读，
// 打开一次写一条，之后再打开不重复写。
func (m *Manager) MarkRead(id string) (*Summary, error) {
	if strings.HasPrefix(id, legacyPrefix) {
		detail, err := m.getLegacy(id)
		if err != nil {
			return nil, err
		}
		m.markLegacySeen(id)
		return &Summary{View: detail.View, StatusDot: StatusDotIdle}, nil
	}
	current, err := m.load(id)
	if err != nil {
		return nil, err
	}
	read := current.readUpTo
	if item, ok := m.recordOf(current.taskID); ok && read < item.LastSeq {
		read = item.LastSeq
	}
	if read > current.readUpTo {
		written, err := m.store.append(id, Message{Kind: kindRead, UpTo: read})
		if err != nil {
			return nil, err
		}
		current.apply(written)
	}
	summary := m.summaryOf(m.stateOf(current), contract.RoleInitiator, nil)
	return &summary, nil
}

// recordOf 会话绑定的任务的快照。
func (m *Manager) recordOf(taskID string) (*contract.TaskSnapshot, bool) {
	if taskID == "" {
		return nil, false
	}
	return m.deps.Engine.Get(taskID)
}

// titleLimit 会话标题的字数上限。
const titleLimit = 60

// Rename 人给会话起标题。
func (m *Manager) Rename(id string, role contract.Role, title string) (*Summary, error) {
	t, done, err := m.begin(id, role)
	if err != nil {
		return nil, err
	}
	defer done()
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, failure(contract.ErrInvalidInput, "标题不能为空")
	}
	if runes := []rune(title); len(runes) > titleLimit {
		return nil, failure(contract.ErrInvalidInput, "标题最多 %d 个字", titleLimit)
	}
	if err := t.write(Message{Kind: kindRename, Title: title}); err != nil {
		return nil, err
	}
	current, err := m.load(id)
	if err != nil {
		return nil, err
	}
	summary := m.summaryOf(sessionState{current: current, view: m.view(current)}, role, nil)
	return &summary, nil
}

// Archive 人归档或取消归档会话。归档不改任务，只让会话默认不出现在列表里。
func (m *Manager) Archive(id string, role contract.Role, archived bool) (*Summary, error) {
	t, done, err := m.begin(id, role)
	if err != nil {
		return nil, err
	}
	defer done()
	if err := t.write(Message{Kind: kindArchive, Archived: archived}); err != nil {
		return nil, err
	}
	current, err := m.load(id)
	if err != nil {
		return nil, err
	}
	summary := m.summaryOf(sessionState{current: current, view: m.view(current)}, role, nil)
	return &summary, nil
}

// Records 记录 tab：所有工作区里已开工的任务（任务、状态、所在会话；状态点按读的人的角色由 DotFor 现算）。
// 任务列表和绑定的会话都没变时返回上次算好的结果，不必每次回放会话日志。
func (m *Manager) Records() []Record {
	summaries := m.deps.Engine.List()
	bound := m.sessionsByTask()
	stamp := recordsStamp(summaries, bound)
	m.recordsMu.Lock()
	defer m.recordsMu.Unlock()
	if m.records != nil && m.recordsStamp == stamp {
		return copyRecords(m.records)
	}
	records := make([]Record, 0, len(summaries))
	for _, summary := range summaries {
		records = append(records, Record{
			TaskID: summary.ID, Group: recordGroupOf(summary.State), Title: summary.Title, Employee: summary.Employee,
			State: summary.State, StatusText: summary.StatusText,
			SessionID: bound[summary.ID].id, WorkspaceID: summary.WorkspaceID, UpdatedAt: summary.UpdatedAt,
		})
	}
	sort.SliceStable(records, func(i, j int) bool {
		return parseTime(records[i].UpdatedAt).After(parseTime(records[j].UpdatedAt))
	})
	m.records, m.recordsStamp = records, stamp
	return copyRecords(records)
}

func copyRecords(records []Record) []Record {
	return append([]Record{}, records...)
}

// recordsStamp 记录的指纹：任务的状态、标题、更新时间，以及绑定到它的会话变了就算变了。
// 每个任务单独算一份再排序拼起来：引擎给出的顺序变了（没排序）也不影响指纹。
func recordsStamp(summaries []contract.TaskSummary, bound map[string]boundSession) string {
	parts := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		// 没有会话的任务（直接从任务接口建的）用空串
		sessionID, sessionUpdated := "", ""
		if boundTo, ok := bound[summary.ID]; ok && boundTo.record != nil {
			sessionID, sessionUpdated = boundTo.id, boundTo.record.updatedAt
		}
		hash := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s",
			summary.ID, summary.State, summary.Title, summary.UpdatedAt, summary.WorkspaceID, sessionID, sessionUpdated)))
		parts = append(parts, hex.EncodeToString(hash[:]))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// recordGroupOf 任务状态 → 记录分组。
func recordGroupOf(state contract.TaskState) string {
	switch state {
	case contract.StateDraft, contract.StateAdmission, contract.StateRunning, contract.StateQC, contract.StateAwaitingOperator:
		return RecordRunning
	case contract.StateAwaitingInitiator:
		return RecordSupplement
	case contract.StateAwaitingReview:
		return RecordReview
	case contract.StateDelivered:
		return RecordDelivered
	default:
		return RecordTerminated
	}
}

// boundSession 任务所在的会话。
type boundSession struct {
	id     string
	record *record
}

// sessionsByTask 任务 → 绑定了它的会话；同一任务绑在多个会话上时取最新的会话。
func (m *Manager) sessionsByTask() map[string]boundSession {
	out := map[string]boundSession{}
	for _, id := range m.visibleIDs() {
		current, err := m.store.read(id)
		if err != nil || current == nil || current.taskID == "" {
			continue
		}
		if existing, ok := out[current.taskID]; ok && parseTime(existing.record.updatedAt).After(parseTime(current.updatedAt)) {
			continue
		}
		out[current.taskID] = boundSession{id: id, record: current}
	}
	return out
}
