// Package session 是会话模块：人和前台 Agent 的对话，独立于任务引擎。
//
// 会话只通过 contract.Engine 和任务打交道。Agent 能产生的只有草稿和提议，
// 发送任务命令的路径只在人触发的 Submit 里，Agent 的工具拿到的是只读接口（见 frontdesk.go）。
package session

import (
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

// 会话状态。
const (
	StatusReception = "reception" // 接待中：还没确认员工
	StatusDrafting  = "drafting"  // 员工已接手，还没提交
	StatusTask      = "task"      // 任务进行中
	StatusClosed    = "closed"    // 任务已交付或已终止
)

// 业务错误码（contract 里的通用错误码之外）。
const (
	ErrSessionHasTask  = "session_has_task"
	ErrSessionReadOnly = "session_read_only"
	ErrSessionBusy     = "session_busy"
	ErrEmployeeMissing = "employee_required"
)

// Error 带错误码的业务错误，HTTP 层据此定状态码。
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func failure(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Employee 路由用的员工信息。
type Employee struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Deps 会话模块的依赖。
type Deps struct {
	// Dir 应用目录：会话日志在它的 sessions/ 下
	Dir    string
	Engine contract.Engine
	// Workspace 工作区 ID 是否已登记（登记项被取消后会话仍可查看）
	Workspace func(id string) bool
	// Model 取当前模型提供者（设置保存后会换）；nil 或返回 nil 都算模型不可用
	Model func() agent.ModelProvider
	// Employees 列出可接手的员工
	Employees func() ([]Employee, error)
	// LoadEmployee 读一个员工的完整配置
	LoadEmployee func(id string) (*contract.EmployeeConfig, error)
	// DefaultEmployee 设置里的默认员工；空串表示没设
	DefaultEmployee func() string
	// Execution 模型重试和超时的默认值
	Execution func() *contract.ExecutionSettings
	// Preflight 提交前检查：返回错误时任务不会创建
	Preflight func() error
	// Knowledge 知识库的只读工具；员工的 frontdesk.md 勾选了才开放给前台 Agent。nil 表示没有知识库
	Knowledge contract.ToolClient
	// Filing 入库卡片要用的知识库信息；nil 表示没有知识库，交付后不发入库卡片
	Filing FilingSource
	Now    func() time.Time
}

// Manager 会话模块的入口。
type Manager struct {
	deps  Deps
	store *store
	mu    sync.Mutex

	// jobs 每个会话正在跑的那一轮；buses 每个会话的事件订阅
	jobs  map[string]*turnJob
	buses map[string]*bus
	// turnSeq 每个会话已经开到第几轮
	turnSeq map[string]int64
}

// New 组装会话模块。
func New(deps Deps) *Manager {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Manager{
		deps: deps, store: newStore(deps.Dir, deps.Now),
		jobs: map[string]*turnJob{}, buses: map[string]*bus{}, turnSeq: map[string]int64{},
	}
}

// ErrWorkspaceUnknown 会话所属的工作区没有登记过，也查不到它的目录。
const ErrWorkspaceUnknown = "workspace_not_found"

// View 读取时返回的会话。
type View struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspaceId"`
	Status      string         `json:"status"`
	ReadOnly    bool           `json:"readOnly"`
	Title       string         `json:"title"`
	Employee    string         `json:"employee,omitempty"`
	TaskID      string         `json:"taskId,omitempty"`
	Draft       map[string]any `json:"draft,omitempty"`
	Attachments []Attachment   `json:"attachments"`
	CreatedAt   string         `json:"createdAt"`
	UpdatedAt   string         `json:"updatedAt"`
	// TaskState、StatusText、WaitingFor 来自任务摘要，没有任务时为空
	TaskState  contract.TaskState `json:"taskState,omitempty"`
	StatusText string             `json:"statusText,omitempty"`
	WaitingFor []contract.Role    `json:"waitingFor"`
	// Checklist 开工清单，读单个会话时才有；没有员工或员工没有清单时为空
	Checklist *Checklist `json:"checklist,omitempty"`
	// AgentRunning 前台 Agent 正在回复：列表的状态点显示“运行中”，发送按钮变成停止按钮（Spec 0006）
	AgentRunning bool `json:"agentRunning,omitempty"`
}

// Detail 读取单个会话时返回：会话加全部消息。
type Detail struct {
	View
	Messages []Message `json:"messages"`
}

func newID(now time.Time) string {
	buffer := make([]byte, 3)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("s-%d", now.UnixNano())
	}
	return fmt.Sprintf("s-%s-%x", now.UTC().Format("20060102150405"), buffer)
}

// Create 在 workspaceID 工作区里开一个新会话；工作区必须登记过。
func (m *Manager) Create(workspaceID string) (*View, error) {
	if m.deps.Workspace == nil || !m.deps.Workspace(workspaceID) {
		return nil, failure(ErrWorkspaceUnknown, "工作区不存在：%s", workspaceID)
	}
	id := newID(m.deps.Now())
	if _, err := m.store.append(id, Message{Kind: kindCreated, WorkspaceID: workspaceID}); err != nil {
		return nil, err
	}
	current, err := m.load(id)
	if err != nil {
		return nil, err
	}
	return m.view(current), nil
}

// Get 读一个会话和它的全部消息。
func (m *Manager) Get(id string) (*Detail, error) {
	if strings.HasPrefix(id, legacyPrefix) {
		return m.getLegacy(id)
	}
	current, err := m.load(id)
	if err != nil {
		return nil, err
	}
	messages := visibleMessages(current.messages)
	if current.taskID != "" {
		if messages, err = m.withTask(messages, current.taskID, current); err != nil {
			return nil, err
		}
	} else {
		messages = orderMessages(messages)
	}
	view, err := m.fullView(current)
	if err != nil {
		return nil, err
	}
	return &Detail{View: *view, Messages: messages}, nil
}

// Reply 一次人的操作得到的结果：这次新写入的消息和最新的会话。
type Reply struct {
	OK       bool      `json:"ok"`
	Messages []Message `json:"messages"`
	Session  *View     `json:"session"`
}

// begin 占住会话的写锁：Agent 正在回复时人的操作会被拒绝，避免两边同时改草稿。
// 确认员工、提交、上传材料和给清单项打勾都是人的操作，模型调用不占这把锁。
//
// 写锁不可重入：这一轮的循环（turnLoop）自己已经拿着它，写日志时用无锁的 beginUnlocked。
func (m *Manager) begin(id string, role contract.Role) (*turn, func(), error) {
	return m.beginLocked(id, role, false)
}

// beginUnlocked 不用写锁、也不检查忙：只给发消息和已经占着这一轮写锁的循环用。
func (m *Manager) beginUnlocked(id string, role contract.Role) (*turn, func(), error) {
	return m.beginLocked(id, role, true)
}

func (m *Manager) beginLocked(id string, role contract.Role, unlocked bool) (*turn, func(), error) {
	if _, err := m.load(id); err != nil {
		return nil, nil, err
	}
	lock := m.store.lockOf(id)
	if unlocked {
		current, err := m.load(id)
		if err != nil {
			return nil, nil, err
		}
		return &turn{manager: m, current: current, role: role, tasks: m.deps.Engine}, func() {}, nil
	}
	// 写锁被别人拿着：前台 Agent 正在这一轮里干活，人的操作（确认员工、提交、给清单项打勾）要等它说完
	if !lock.TryLock(0) {
		return nil, nil, failure(ErrSessionBusy, "上一条消息还在处理，请稍候")
	}
	// 拿到锁之后才读，看到的才是上一条回复写完的状态
	current, err := m.load(id)
	if err != nil {
		lock.Unlock(0)
		return nil, nil, err
	}
	return &turn{manager: m, current: current, role: role, tasks: m.deps.Engine}, func() { lock.Unlock(0) }, nil
}
func (m *Manager) reply(t *turn) (*Reply, error) {
	messages := visibleMessages(t.out)
	if taskID := t.current.taskID; taskID != "" {
		events, err := m.deps.Engine.Events(taskID, 0)
		if err != nil {
			return nil, err
		}
		if err := m.attachActions(messages, taskID, events); err != nil {
			return nil, err
		}
	}
	view, err := m.fullView(t.current)
	if err != nil {
		return nil, err
	}
	return &Reply{OK: true, Messages: messages, Session: view}, nil
}

// fullView 读单个会话时的样子：列表里的字段，加上开工清单。
func (m *Manager) fullView(current *record) (*View, error) {
	view := m.view(current)
	checklist, _, err := m.checklistOf(current, nil)
	if err != nil {
		return nil, err
	}
	view.Checklist = checklist
	return view, nil
}

// Confirm 人在卡片上确认由哪个员工接手。这是人的动作，Agent 没有对应的工具。
// 人说过话时紧接着开一轮，让 Agent 据此填表（这就是“下一步”）。
func (m *Manager) Confirm(id string, role contract.Role, employee string) (*Reply, error) {
	if role != contract.RoleInitiator {
		return nil, failure(contract.ErrForbiddenRole, "只有发起人可以确认员工")
	}
	t, done, err := m.begin(id, role)
	if err != nil {
	
		return nil, err
	}
	err = m.confirmLocked(t, employee)

	if err != nil {
		if _, ok := err.(*handoverPending); !ok {
			done()
			return nil, err
		}
		// 写锁还拿着就把填表那轮登记上：从这一刻起它就算在跑，紧接着发来的消息按插话送进这一轮。
		// 这一轮自己放锁后再去调模型，由它在下一步之前接着填表。
		var job *turnJob
		job, err = m.startTurnLocked(id)
		if err == nil {
			noteSteer(job)
		}
		done()
		if err != nil {
			return nil, err
		}
	}
	done()
	return m.reply(t)
}

// handoverPending 确认员工时人说过话：填表交给下一轮，不在这次请求里等模型。
type handoverPending struct{}

func (*handoverPending) Error() string { return "handover pending" }

// confirmLocked 写确认员工和接手说明；人说过话时返回 handoverPending。
func (m *Manager) confirmLocked(t *turn, employee string) error {
	if t.current.employee != "" {
		return failure(contract.ErrIllegalTransition, "这个会话已由员工接手")
	}
	employees, err := m.deps.Employees()
	if err != nil {
		return err
	}
	name := ""
	for _, candidate := range employees {
		if candidate.ID == employee {
			name = candidate.Name
		}
	}
	if name == "" {
		return failure(contract.ErrInvalidInput, "没有这个员工：%s", employee)
	}
	if err := t.write(Message{Kind: kindEmployeeSet, Employee: employee}); err != nil {
		return err
	}
	if err := t.write(Message{Kind: KindNotice, Text: name + "已接手。"}); err != nil {
		return err
	}
	if t.current.spoken() {
		// 人说过的话不用再说一遍：让 Agent 据此填表（这算一条插话，由新一轮在下一步之前送达）
		return &handoverPending{}
	}
	t.cards = append(t.cards, t.formCard())
	return t.flushCards()
}

// AddAttachments 把人上传的材料暂存进会话，提交时才进任务。
// 不占回复锁：人可以在 Agent 回复的时候继续传文件。
func (m *Manager) AddAttachments(id string, role contract.Role, files []contract.AttachmentInput) (*Reply, error) {
	if role != contract.RoleInitiator {
		return nil, failure(contract.ErrForbiddenRole, "只有发起人可以上传材料")
	}
	if len(files) == 0 {
		return nil, failure(contract.ErrInvalidInput, "没有收到文件")
	}
	current, err := m.load(id)
	if err != nil {
		return nil, err
	}
	if current.taskID != "" {
		return nil, failure(ErrSessionHasTask, "任务已经开始，请在暂停卡片上补充材料")
	}
	t := &turn{manager: m, current: current}
	names := make([]string, 0, len(files))
	for _, file := range files {
		size, err := m.store.saveAttachment(id, file)
		if err != nil {
			return nil, err
		}
		if err := t.write(Message{Kind: kindAttachmentAdded, Attachment: &Attachment{Name: file.Name, Size: size}}); err != nil {
			return nil, err
		}
		names = append(names, file.Name)
	}
	if err := t.write(Message{Kind: KindNotice, Text: "已收到材料：" + strings.Join(names, "、")}); err != nil {
		return nil, err
	}
	return m.reply(t)
}

// Submit 人在表单卡片上点提交：建任务，发提交命令。
// 这是会话里唯一发任务命令的地方；form 为 nil 时用草稿，否则以人改过的内容为准。
func (m *Manager) Submit(id string, role contract.Role, form map[string]any) (*Reply, error) {
	if role != contract.RoleInitiator {
		return nil, failure(contract.ErrForbiddenRole, "只有发起人可以提交")
	}
	t, done, err := m.begin(id, role)
	if err != nil {
		return nil, err
	}
	defer done()
	if t.current.taskID != "" {
		return nil, failure(ErrSessionHasTask, "这个会话已有任务，要做新任务请开新会话")
	}
	if t.current.employee == "" {
		return nil, failure(ErrEmployeeMissing, "请先确认由哪个员工接手")
	}
	checklist, items, err := m.checklistOf(t.current, form)
	if err != nil {
		return nil, err
	}
	if checklist != nil && !checklist.Ready {
		return nil, failure(ErrChecklistIncomplete, "开工清单还缺：%s。备齐后才能开工。", strings.Join(checklist.Missing, "、"))
	}
	if m.deps.Preflight != nil {
		if err := m.deps.Preflight(); err != nil {
			return nil, err
		}
	}
	if form != nil {
		if err := t.write(Message{Kind: kindDraftSet, Draft: form}); err != nil {
			return nil, err
		}
	}
	inputs := make([]contract.AttachmentInput, 0, len(t.current.attachments))
	for _, attachment := range t.current.attachments {
		inputs = append(inputs, contract.AttachmentInput{Name: attachment.Name, SourcePath: m.store.attachmentPath(id, attachment.Name)})
	}
	snapshot, err := m.deps.Engine.Create(contract.CreateTaskInput{
		Employee: t.current.employee, Form: applySkips(t.current.draft, checklist, items), Attachments: inputs, Actor: role,
		WorkspaceID: t.current.workspaceID,
	})
	if err != nil {
		return nil, err
	}
	if err := t.write(Message{Kind: kindTaskBound, TaskID: snapshot.ID}); err != nil {
		return nil, err
	}
	if result := m.deps.Engine.Command(snapshot.ID, contract.TaskCommand{Type: contract.CmdSubmit}, role); !result.OK {
		return nil, failure(result.Error, "%s", result.Message)
	}
	return m.reply(t)
}

// legacyPrefix 由任务事件推导出的只读会话的 ID 前缀。
const legacyPrefix = "task-"

// load 读一个可以写入的会话。
func (m *Manager) load(id string) (*record, error) {
	if !idPattern.MatchString(id) {
		return nil, notFound(id)
	}
	if strings.HasPrefix(id, legacyPrefix) {
		if _, found := m.deps.Engine.Get(strings.TrimPrefix(id, legacyPrefix)); found {
			return nil, failure(ErrSessionReadOnly, "这是由旧任务推导出的只读会话，要继续工作请开新会话")
		}
		return nil, notFound(id)
	}
	current, err := m.store.read(id)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, notFound(id)
	}
	return current, nil
}

// legacyView 没有会话的旧任务：由事件推导出只读会话。
func (m *Manager) legacyView(summary contract.TaskSummary) *View {
	view := &View{
		ID: legacyPrefix + summary.ID, WorkspaceID: summary.WorkspaceID,
		Status: StatusTask, ReadOnly: true, Title: summary.Title,
		Employee: summary.Employee, TaskID: summary.ID, Attachments: []Attachment{},
		CreatedAt: summary.UpdatedAt, UpdatedAt: summary.UpdatedAt, WaitingFor: []contract.Role{},
	}
	m.applySummary(view, summary)
	return view
}

// getLegacy 读一个只读会话和它的全部消息。
func (m *Manager) getLegacy(id string) (*Detail, error) {
	taskID := strings.TrimPrefix(id, legacyPrefix)
	for _, summary := range m.deps.Engine.List() {
		if summary.ID != taskID {
			continue
		}
		messages, err := m.withTask(nil, taskID, nil)
		if err != nil {
			return nil, err
		}
		return &Detail{View: *m.legacyView(summary), Messages: messages}, nil
	}
	return nil, notFound(id)
}

// List 列出全部会话，最新的在前；已归属会话的任务不再单独列出。
// workspaceID 非空时只列这个工作区的会话（空串表示所有工作区）。
func (m *Manager) List(workspaceID string) ([]*View, error) {
	out := []*View{}
	bound := map[string]bool{}
	for _, id := range m.store.ids() {
		current, err := m.store.read(id)
		if err != nil {
			return nil, err
		}
		if current == nil {
			continue
		}
		bound[current.taskID] = true
		if workspaceID != "" && current.workspaceID != workspaceID {
			continue
		}
		out = append(out, m.view(current))
	}
	// 由旧任务推导出的只读会话；发起事件里没有工作区的旧任务（Spec 0006 之前）算作未归属，不带过滤时也列
	for _, summary := range m.deps.Engine.List() {
		if bound[summary.ID] {
			continue
		}
		if workspaceID != "" && summary.WorkspaceID != workspaceID {
			continue
		}
		out = append(out, m.legacyView(summary))
	}
	sort.SliceStable(out, func(i, j int) bool { return parseTime(out[i].UpdatedAt).After(parseTime(out[j].UpdatedAt)) })
	return out, nil
}

func notFound(id string) error {
	return failure(contract.ErrNotFound, "会话不存在：%s", id)
}

func (m *Manager) view(current *record) *View {
	view := &View{
		ID: current.id, WorkspaceID: current.workspaceID, Status: StatusReception, Title: titleOf(current),
		Employee: current.employee, TaskID: current.taskID, Draft: current.draft,
		Attachments: append([]Attachment{}, current.attachments...),
		CreatedAt:   current.createdAt, UpdatedAt: current.updatedAt,
		WaitingFor: []contract.Role{},
	}
	if current.employee != "" {
		view.Status = StatusDrafting
	}
	if current.taskID != "" {
		view.Status = StatusTask
		m.applyTask(view, current.taskID)
	}
	// 前台 Agent 正在回复：列表上的状态点显示“运行中”（Spec 0006）
	view.AgentRunning = m.runningOf(current.id)
	return view
}

// runningOf 会话现在有没有一轮在跑。
func (m *Manager) runningOf(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobs[id] != nil
}

// applyTask 把任务摘要投影到会话上。
func (m *Manager) applyTask(view *View, taskID string) {
	for _, summary := range m.deps.Engine.List() {
		if summary.ID == taskID {
			m.applySummary(view, summary)
		}
	}
}

func (m *Manager) applySummary(view *View, summary contract.TaskSummary) {
	view.TaskState = summary.State
	view.StatusText = summary.StatusText
	view.WaitingFor = summary.WaitingFor
	if contract.IsTerminal(summary.State) {
		view.Status = StatusClosed
	}
	if parseTime(summary.UpdatedAt).After(parseTime(view.UpdatedAt)) {
		view.UpdatedAt = summary.UpdatedAt
	}
}

// titleOf 会话标题：第一句话。
func titleOf(current *record) string {
	for _, message := range current.messages {
		if message.Kind == KindUser {
			runes := []rune(strings.TrimSpace(message.Text))
			if len(runes) > 30 {
				return string(runes[:30]) + "…"
			}
			return string(runes)
		}
	}
	return "新对话"
}

func visibleMessages(messages []Message) []Message {
	out := []Message{}
	for _, message := range messages {
		if message.visible() {
			out = append(out, message)
		}
	}
	return out
}

// CodeOf 取业务错误码；不是业务错误时返回空串。
func CodeOf(err error) (code string, message string) {
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Code, typed.Message
	}
	return "", ""
}
