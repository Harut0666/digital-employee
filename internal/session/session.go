// Package session 是会话模块：人和前台 Agent 的对话，独立于任务引擎。
//
// 会话只通过 contract.Engine 和任务打交道。发送任务命令的路径只有开工（startEmployeeTask：员工表单卡上人确认，
// 或 run_employee 在 auto、yolo 下免确认直接开工）和旧会话兼容的 Submit，Agent 的工具拿到的任务接口是只读的（见 frontdesk.go）。
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
	// Settings 提交前检查不通过时带人去哪个设置页
	Settings *SettingsEntry
}

// SettingsEntry 设置页入口（提交前检查不通过时一起返回）。
type SettingsEntry struct {
	Section string `json:"section"`
	Label   string `json:"label"`
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
	// WorkspaceDir 工作区 ID → 目录。前台 Agent 读目录文件、上传写进“材料”都从这里找；找不到时返回空串
	WorkspaceDir func(id string) string
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
	// ModelSettings 当前的模型设置（连接列表 + 缺省模型），每次读设置文件
	ModelSettings func() contract.ModelSettings
	// SaveLastModel 会话里选了模型后记进设置（lastModel），新会话沿用它
	SaveLastModel func(contract.ModelRef) error
	// Preflight 提交前检查：检查 ref（会话模型）所在的连接有 Key、模型在列表里；返回错误时任务不会创建
	Preflight func(ref contract.ModelRef) error
	// Knowledge 知识库的只读工具；员工的 frontdesk.md 勾选了才开放给前台 Agent。nil 表示没有知识库
	Knowledge contract.ToolClient
	// Tools 工具客户端：前台 Agent 的 read_file 复用它的文件解析。nil 时 read_file 报没有工具
	Tools contract.ToolClient
	// Filing 入库卡片要用的知识库信息；nil 表示没有知识库，交付后不发入库卡片
	Filing FilingSource
	// Sample 应用目录里的公开样例（T49“载入公开样例”）；nil 或返回 nil 表示没有样例
	Sample func() *Sample
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
	// humanLocks 每个会话的人的操作锁：同一会话上两个人同时点确认、提交时按顺序处理。
	// 前台 Agent 的一轮用另一把写锁（sessionLock），人的操作与它互斥；这把锁只挡人跟人。
	humanLocks map[string]*sync.Mutex
	// turnSeq 每个会话已经开到第几轮
	turnSeq map[string]int64
	// sessions 中间隔多久看一次新会话日志目录
	scanEvery time.Duration
	// scanned 上一次看会话目录的时间、看到 会话 ID → 目录修改时间
	scannedAt time.Time
	seen      map[string]time.Time
	// seenLegacy 打开过的旧任务会话（它们没有会话日志，没有别的记录已读的地方）
	seenLegacy map[string]bool
	// legacyArchived 旧任务会话的归档状态（没有会话日志，单独存在应用目录的文件里）
	legacyArchived *legacyArchive
	// views 会话的投影缓存：消息条数和更新时间没变就沿用（侧栏每几秒读一次全部会话）
	views map[string]cachedView
	// recordsCache 记录 tab 的缓存：任务列表没变时不用再读每个会话的日志
	// waits 正在等人答复的权限确认（T48），permMu 保护它和里面的 settled
	permMu sync.Mutex
	waits  map[string]*permissionWait
	// forms 员工表单卡的等待（T49），formMu 保护这张表
	formMu       sync.Mutex
	forms        map[string]*formWait
	recordsMu    sync.Mutex
	recordsStamp string
	records      []Record
}

// New 组装会话模块。
func New(deps Deps) *Manager {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	// 第一次读会话目录之前也先看一眼：新建的会话马上就能出现在列表里，之后的扫描隔一会儿一次
	return &Manager{
		deps: deps, store: newStore(deps.Dir, deps.Now), legacyArchived: newLegacyArchive(deps.Dir),
		jobs: map[string]*turnJob{}, buses: map[string]*bus{}, turnSeq: map[string]int64{},
		humanLocks: map[string]*sync.Mutex{}, waits: map[string]*permissionWait{}, forms: map[string]*formWait{},
		scanEvery: time.Second, scannedAt: deps.Now().Add(-time.Hour),
	}
}

// sessionScanEvery 会话目录隔多久重新列一次（新建的会话最多这么久之后出现在列表里）。
const sessionScanEvery = time.Second

// cachedView 一次会话回放的投影缓存：at 是当时的消息条数，updatedAt 是当时的更新时间。
type cachedView struct {
	at        int
	updatedAt string
	// task 当时会话当前任务的指纹（见 taskStamp）
	task string
	view View
}

// busyText 上一条回复还没结束时又发来消息：说明一声。
const busyText = "上一条消息还在处理，这条没有发出去，请稍后再发。"

// ErrWorkspaceUnknown 会话所属的工作区没有登记过，也查不到它的目录。
const ErrWorkspaceUnknown = "workspace_not_found"

// View 读取时返回的会话。
type View struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	Status      string `json:"status"`
	ReadOnly    bool   `json:"readOnly"`
	Title       string `json:"title"`
	Employee    string `json:"employee,omitempty"`
	// TaskID 当前任务（会话里最新的一个）；TaskIDs 按开工顺序的全部任务
	TaskID      string         `json:"taskId,omitempty"`
	TaskIDs     []string       `json:"taskIds"`
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
	// Model 会话模型（Spec 0006）：会话里最后一次选择的；没选过时为空，沿用设置的 lastModel。
	// Selected 表示会话里明确选过。
	Model    *contract.ModelRef `json:"model,omitempty"`
	Selected bool               `json:"modelSelected,omitempty"`
	// Archived 归档的会话默认不在列表里出现
	Archived bool `json:"archived"`
	// Unread 任务交付或终止之后还没打开过
	Unread bool `json:"unread"`
	// PermissionMode 权限模式（T48）：plan、ask、accept_edits、auto、yolo；新会话总是 ask
	PermissionMode string `json:"permissionMode"`
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

// WorkspaceOf 会话属于哪个工作区。
func (m *Manager) WorkspaceOf(id string) (string, error) {
	current, err := m.load(id)
	if err != nil {
		return "", err
	}
	return current.workspaceID, nil
}

// Create 在 workspaceID 工作区里开一个新会话。model 是可选的新对话页选的模型
// （Spec 0007「会话模型的选择」）：校验规则和会话里选模型一样，选中的模型决定这个会话从第一句话起用哪个；
// nil 时沿用设置：先按 lastModel，再按第一个可用模型（没人明确选过，不写 lastModel）。
func (m *Manager) Create(workspaceID string, model *contract.ModelRef) (*View, error) {
	if m.deps.Workspace == nil || !m.deps.Workspace(workspaceID) {
		return nil, failure(ErrWorkspaceUnknown, "工作区不存在：%s", workspaceID)
	}
	var chosen *contract.ModelRef
	if model != nil {
		ref := contract.ModelRef{Connection: strings.TrimSpace(model.Connection), Model: strings.TrimSpace(model.Model)}
		if ref.Connection == "" || ref.Model == "" {
			return nil, failure(contract.ErrInvalidInput, "选模型要同时给出连接和模型")
		}
		if err := m.checkSelectable(ref); err != nil {
			return nil, err
		}
		chosen = &ref
	}
	id := newID(m.deps.Now())
	if _, err := m.store.append(id, Message{Kind: kindCreated, WorkspaceID: workspaceID}); err != nil {
		return nil, err
	}
	if chosen != nil {
		if _, err := m.store.append(id, Message{Kind: kindModelSelected, Model: chosen, Selected: true}); err != nil {
			return nil, err
		}
		// 和会话里选模型一样记进 lastModel，新会话沿用它
		if err := m.saveLastModel(*chosen); err != nil {
			return nil, err
		}
	}
	current, err := m.load(id)
	if err != nil {
		return nil, err
	}
	return m.view(current), nil
}

// Get 读一个会话和它的全部消息，同时写“已读”。
func (m *Manager) Get(id string) (*Detail, error) {
	// 已读只是顺手记一下，写不进去不影响读会话
	_, _ = m.MarkRead(id)
	return m.read(id)
}

// read 读一个会话和它的全部消息。
func (m *Manager) read(id string) (*Detail, error) {
	if strings.HasPrefix(id, legacyPrefix) {
		return m.getLegacy(id)
	}
	current, err := m.load(id)
	if err != nil {
		return nil, err
	}
	messages := m.projectCards(visibleMessages(current.messages), current)

	if len(current.taskIDs) > 0 {
		if messages, err = m.withTasks(messages, current.taskIDs, current); err != nil {
			return nil, err
		}
	} else {
		messages = orderMessages(attachFolderFileActions(messages, m.workspaceDir(current.workspaceID)))
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
// 确认员工、提交、上传材料和给清单项打勾都是人的操作；发消息例外（它走 beginUnlocked，
// 把话排成插话送进正在跑的那一轮）。
//
// 写锁可重入（见 sessionLock）：这一轮的循环（turnLoop）自己已经拿着它，写日志时再进来一层。
func (m *Manager) begin(id string, role contract.Role) (*turn, func(), error) {
	return m.beginLocked(id, role, false)
}

// beginUnlocked 不用写锁、也不检查忙：只给发消息和已经占着这一轮写锁的循环用。
func (m *Manager) beginUnlocked(id string, role contract.Role) (*turn, func(), error) {
	return m.beginLocked(id, role, true)
}

// beginAside 人的操作不用等这一轮：只占“人的操作”那把锁（两个人同时点只有第一个进去），
// 不占前台 Agent 这一轮的写锁。给不改草稿、材料、任务的操作用（选模型）：Agent 正在回复或停在权限确认、
// 员工表单卡上时也能做，写进日志的选择从下一轮对话起生效，当前这一轮继续用它开始时选定的模型。
func (m *Manager) beginAside(id string) (*turn, func(), error) {
	if _, err := m.load(id); err != nil {
		return nil, nil, err
	}
	human := m.humanLock(id)
	if !human.TryLock() {
		return nil, nil, failure(ErrSessionBusy, "上一个操作还在处理，请稍候")
	}
	current, err := m.load(id)
	if err != nil {
		human.Unlock()
		return nil, nil, err
	}
	return &turn{manager: m, current: current, role: contract.RoleInitiator, tasks: m.deps.Engine}, human.Unlock, nil
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
	// 同一会话上两个人同时操作时只有第一个人进去，第二个人立刻收到 session_busy：
	// 两个人同时点确认、提交会读到同一份旧状态、各写一遍
	human := m.humanLock(id)
	if !human.TryLock() {
		_ = m.store.note(id, busyText)
		return nil, nil, failure(ErrSessionBusy, "上一条消息还在处理，请稍候")
	}
	// 写锁被前台 Agent 拿着：它正在这一轮里干活，人的操作（确认员工、提交、给清单项打勾）要等它说完
	if !lock.TryLock(0) {
		human.Unlock()
		// 回一条说明留痕，打开会话时能看到；不改变状态，也不用模型
		_ = m.store.note(id, busyText)
		return nil, nil, failure(ErrSessionBusy, "上一条消息还在处理，请稍候")
	}
	// 拿到锁之后才读，看到的才是上一条回复写完的状态
	current, err := m.load(id)
	if err != nil {
		lock.Unlock(0)
		human.Unlock()
		return nil, nil, err
	}
	return &turn{manager: m, current: current, role: role, tasks: m.deps.Engine}, func() {
		lock.Unlock(0)
		human.Unlock()
	}, nil
}
func (m *Manager) reply(t *turn) (*Reply, error) {
	messages := m.projectCards(visibleMessages(t.out), t.current)
	// t.current 是写入之前的快照：补上这次新写进去的草稿和材料，清单按写完的样子显示
	for _, message := range t.out {
		if message.Kind == kindAttachmentAdded || message.Kind == kindDraftSet {
			t.current.apply(message)
		}
	}
	if taskID := t.current.taskID; taskID != "" {
		events, err := m.deps.Engine.Events(taskID, 0)
		if err != nil {
			return nil, err
		}
		if err := m.attachActions(messages, taskID, events); err != nil {
			return nil, err
		}
	} else {
		messages = attachFolderFileActions(messages, m.workspaceDir(t.current.workspaceID))
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
	view.Model = m.currentModel(current)
	view.Selected = current.model != nil
	return view, nil
}

// Confirm 旧会话兼容（T49 之前的“确认员工”卡）：把会话记到这个员工名下。新界面不再调用它：
// 员工由 Agent 调 run_employee 时带上，人在员工表单卡上确认。这是人的动作，Agent 没有对应的工具。
func (m *Manager) Confirm(id string, role contract.Role, employee string) (*Reply, error) {
	if role != contract.RoleInitiator {
		return nil, failure(contract.ErrForbiddenRole, "只有发起人可以确认员工")
	}
	t, done, err := m.begin(id, role)
	if err != nil {
		return nil, err
	}
	defer done()
	if err := m.confirmLocked(t, employee); err != nil {
		return nil, err
	}
	return m.reply(t)
}

// confirmLocked 写确认员工和接手说明。
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
	return t.write(Message{Kind: KindNotice, Text: name + "已接手。"})
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
	if m.openTask(current) != nil {
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
	if err := m.refuseNewTask(t.current); err != nil {
		return nil, err
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
	// 提交前检查按会话模型：它所在的连接有 Key，并且模型在连接的模型列表里
	model := m.effectiveModel(t.current)
	if m.deps.Preflight != nil {
		if err := m.deps.Preflight(model); err != nil {
			return nil, err
		}
	}
	if model.Connection == "" {
		// 还没选过模型：任务照样能建（剧本模式、旧任务路径不看它），记下检查时用的缺省模型
		model = m.effectiveModelSettings().Default
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
		// 发起任务时会话模型写进“发起”事件
		Model: model,
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
		Employee: summary.Employee, TaskID: summary.ID, TaskIDs: []string{summary.ID}, Attachments: []Attachment{},
		CreatedAt: summary.UpdatedAt, UpdatedAt: summary.UpdatedAt, WaitingFor: []contract.Role{}, PermissionMode: PermissionAsk,
		Archived: m.legacyArchived.has(legacyPrefix + summary.ID),
	}
	m.applySummary(view, summary)
	if snapshot, found := m.deps.Engine.Get(summary.ID); found && snapshot.Model != nil {
		view.Model = snapshot.Model
		view.Selected = true
	}
	return view
}

// getLegacy 读一个只读会话和它的全部消息。
func (m *Manager) getLegacy(id string) (*Detail, error) {
	taskID := strings.TrimPrefix(id, legacyPrefix)
	for _, summary := range m.deps.Engine.List() {
		if summary.ID != taskID {
			continue
		}
		messages, err := m.withTasks(nil, []string{taskID}, nil)
		if err != nil {
			return nil, err
		}
		return &Detail{View: *m.legacyView(summary), Messages: messages}, nil
	}
	return nil, notFound(id)
}

// List 列出全部会话，最新的在前；已归属会话的任务不再单独列出。
// workspaceID 非空时只列这个工作区的会话（空串表示所有工作区）；归档的会话默认不列。
func (m *Manager) List(workspaceID string, includeArchived bool) ([]*View, error) {
	out := []*View{}
	bound := map[string]bool{}
	for _, id := range m.visibleIDs() {
		current, err := m.store.read(id)
		if err != nil {
			return nil, err
		}
		if current == nil {
			continue
		}
		for _, taskID := range current.taskIDs {
			bound[taskID] = true
		}
		if workspaceID != "" && current.workspaceID != workspaceID {
			continue
		}
		if current.archived && !includeArchived {
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
		view := m.legacyStatus(summary)
		if view.Archived && !includeArchived {
			continue
		}
		out = append(out, view)
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
		Employee: current.employee, TaskID: current.taskID, TaskIDs: append([]string{}, current.taskIDs...), Draft: current.draft,
		Attachments: append([]Attachment{}, current.attachments...),
		CreatedAt:   current.createdAt, UpdatedAt: current.updatedAt,
		WaitingFor: []contract.Role{}, Archived: current.archived, PermissionMode: current.mode(),
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
	if contract.IsTerminal(view.TaskState) && current.readUpTo < current.lastSeq() {
		view.Unread = true
	}
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

// titleOf 会话标题：人改过的标题，否则是第一句话。
func titleOf(current *record) string {
	if current.title != "" {
		return current.title
	}
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
