// 占位任务引擎：internal/engine 就绪前的联调替身。
//
// 只实现 contract.Engine 的最小语义：建草稿、提交和终止时产生 state_changed 事件（前端与 SSE 联调用）、
// 其它命令一律按非法流转拒绝。真实引擎接入后由启动入口的 EngineFactory 替换，本文件不会进入生产路径。
package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"digital-employee/internal/contract"
	"digital-employee/internal/server"
)

// MockEngine 内存任务引擎。
type MockEngine struct {
	mu     sync.Mutex
	logger *server.Logger
	tasks  map[string]*mockTask
	order  []string
	// appDir 应用目录：内存里的任务也按它落一份最小的任务目录，重启后的列表和事件照旧可读
	appDir  string
	nextSub int
}

type mockTask struct {
	snapshot  contract.TaskSnapshot
	events    []contract.TaskEvent
	listeners map[int]func(contract.TaskEvent)
	updatedAt string
	// workspaceID 任务属于哪个工作区；persisted 任务目录已经建好
	workspaceID string
	persisted   bool
}

// NewMockEngine 创建占位引擎；appDir 非空时把任务的发起事件写到 <应用目录>/tasks/<id>/events.jsonl。
func NewMockEngine(logger *server.Logger, appDir string) *MockEngine {
	return &MockEngine{logger: logger, tasks: map[string]*mockTask{}, order: []string{}, appDir: appDir}
}

func eventTS() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

// mockActions 占位引擎真正支持的操作：只有发起人的提交和终止。
func mockActions(state contract.TaskState) []contract.TaskAction {
	initiator := []contract.Role{contract.RoleInitiator}
	switch state {
	case contract.StateDraft:
		return []contract.TaskAction{
			{Command: contract.CmdSubmit, Label: "提交", Roles: initiator, Style: contract.ActionPrimary},
			{Command: contract.CmdTerminate, Label: "放弃草稿", Roles: initiator, Style: contract.ActionDanger},
		}
	case contract.StateAwaitingInitiator:
		return []contract.TaskAction{{Command: contract.CmdTerminate, Label: "终止", Roles: initiator, Style: contract.ActionDanger}}
	}
	return []contract.TaskAction{}
}

func mockWaitingFor(actions []contract.TaskAction) []contract.Role {
	if len(actions) == 0 {
		return []contract.Role{}
	}
	return []contract.Role{contract.RoleInitiator}
}

// present 对外的快照：和真实引擎一样，操作和等待角色在读取时算出。
func (t *mockTask) present() *contract.TaskSnapshot {
	copied := t.snapshot
	copied.Actions = mockActions(copied.State)
	copied.WaitingFor = mockWaitingFor(copied.Actions)
	return &copied
}

func newTaskID() string {
	buffer := make([]byte, 3)
	_, _ = rand.Read(buffer)
	return time.Now().UTC().Format("20060102150405") + "-" + hex.EncodeToString(buffer)
}

func (e *MockEngine) emit(task *mockTask, event contract.TaskEvent) {
	task.snapshot.LastSeq++
	event.Seq = task.snapshot.LastSeq
	event.TS = eventTS()
	event.TaskID = task.snapshot.ID
	event.WorkspaceID = task.workspaceID
	task.events = append(task.events, event)
	task.updatedAt = event.TS
	e.persist(task, event)
	for _, listener := range task.listeners {
		listener(event)
	}
}

// persist 把事件追加到 <应用目录>/tasks/<id>/events.jsonl，让会话模块重启后还能看到任务。
// 占位引擎的任务只在内存里，写失败不影响联调。
func (e *MockEngine) persist(task *mockTask, event contract.TaskEvent) {
	if e.appDir == "" || task.persisted {
		return
	}
	dir := filepath.Join(e.appDir, "tasks", task.snapshot.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	// 引擎重启时按配置文件读任务：没有员工配置的任务目录会被跳过，不影响会话
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return
	}
	task.persisted = true
}

// Create 建草稿任务。
func (e *MockEngine) Create(input contract.CreateTaskInput) (*contract.TaskSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	id := newTaskID()
	attachments := make([]contract.AttachmentRef, 0, len(input.Attachments))
	for _, attachment := range input.Attachments {
		attachments = append(attachments, contract.AttachmentRef{Name: attachment.Name, Path: attachment.SourcePath})
	}
	form := input.Form
	if form == nil {
		form = map[string]any{}
	}
	snapshot := contract.TaskSnapshot{
		ID: id, Employee: input.Employee, ConfigHash: "",
		State: contract.StateDraft, Form: form, Attachments: attachments,
		Steps: []contract.StepStatus{}, Deliverables: []contract.DeliverableRef{},
	}
	task := &mockTask{snapshot: snapshot, listeners: map[int]func(contract.TaskEvent){}, workspaceID: input.WorkspaceID}
	e.tasks[id] = task
	e.order = append(e.order, id)
	e.emit(task, contract.TaskEvent{
		Type: contract.EventTaskCreated, Employee: input.Employee,
		Form: form, Attachments: attachments, WorkspaceID: input.WorkspaceID,
	})
	return task.present(), nil
}

// Command 命令：只实现 submit 和 terminate 的占位流转。
func (e *MockEngine) Command(taskID contract.TaskID, command contract.TaskCommand, actor contract.Role) contract.CommandResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	task, ok := e.tasks[taskID]
	if !ok {
		return contract.CommandResult{OK: false, Error: contract.ErrNotFound, Message: "任务不存在：" + taskID}
	}
	if task.snapshot.State == contract.StateDraft && command.Type == contract.CmdSubmit {
		from := task.snapshot.State
		task.snapshot.State = contract.StateAdmission
		e.emit(task, contract.TaskEvent{
			Type: contract.EventStateChanged, From: from, To: contract.StateAdmission,
			Trigger: contract.TriggerSubmit, Actor: actor,
		})
		from = task.snapshot.State
		pause := &contract.PauseReason{
			Code:       contract.PauseInvalidInput,
			Title:      "占位引擎未接入执行流程",
			Message:    "占位引擎：internal/engine 尚未接入，真实执行流程待接入后可用",
			Resolution: "用默认构建运行真实引擎，或终止这个任务。",
			Options:    []string{contract.CmdTerminate},
		}
		task.snapshot.State = contract.StateAwaitingInitiator
		task.snapshot.Pause = pause
		e.emit(task, contract.TaskEvent{
			Type: contract.EventStateChanged, From: from, To: contract.StateAwaitingInitiator,
			Trigger: contract.TriggerAdmissionFailed, Actor: contract.RoleSystem, Pause: pause,
		})
		return contract.CommandResult{OK: true, Snapshot: task.present()}
	}
	if command.Type == contract.CmdTerminate && len(mockActions(task.snapshot.State)) > 0 {
		from := task.snapshot.State
		task.snapshot.State = contract.StateTerminated
		task.snapshot.Pause = nil
		e.emit(task, contract.TaskEvent{
			Type: contract.EventStateChanged, From: from, To: contract.StateTerminated,
			Trigger: contract.TriggerTerminate, Actor: actor,
		})
		return contract.CommandResult{OK: true, Snapshot: task.present()}
	}
	return contract.CommandResult{
		OK: false, Error: contract.ErrIllegalTransition,
		Message: fmt.Sprintf("占位引擎：状态 %s 不接受命令 %s", task.snapshot.State, command.Type),
	}
}

// Get 读快照。
func (e *MockEngine) Get(taskID contract.TaskID) (*contract.TaskSnapshot, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	task, ok := e.tasks[taskID]
	if !ok {
		return nil, false
	}
	return task.present(), true
}

// List 任务列表（新的在前）。
func (e *MockEngine) List() []contract.TaskSummary {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]contract.TaskSummary, 0, len(e.order))
	for index := len(e.order) - 1; index >= 0; index-- {
		task := e.tasks[e.order[index]]
		statusText := contract.StateLabels[task.snapshot.State]
		if task.snapshot.Pause != nil {
			statusText = task.snapshot.Pause.Title
		}
		out = append(out, contract.TaskSummary{
			ID: task.snapshot.ID, Employee: task.snapshot.Employee,
			Title: task.snapshot.Employee, State: task.snapshot.State, UpdatedAt: task.updatedAt,
			StatusText: statusText, WaitingFor: task.present().WaitingFor,
		})
	}
	return out
}

// Events 读 after 之后的事件。
func (e *MockEngine) Events(taskID contract.TaskID, afterSeq int64) ([]contract.TaskEvent, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	task, ok := e.tasks[taskID]
	if !ok {
		return nil, fmt.Errorf("任务不存在：%s", taskID)
	}
	out := []contract.TaskEvent{}
	for _, event := range task.events {
		if event.Seq > afterSeq {
			out = append(out, event)
		}
	}
	return out, nil
}

// Subscribe 订阅实时事件。
func (e *MockEngine) Subscribe(taskID contract.TaskID, listener func(contract.TaskEvent)) func() {
	e.mu.Lock()
	defer e.mu.Unlock()
	task, ok := e.tasks[taskID]
	if !ok {
		return func() {}
	}
	e.nextSub++
	id := e.nextSub
	task.listeners[id] = listener
	return func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		delete(task.listeners, id)
	}
}

// WaitForSettled 占位引擎里命令是同步完成的，直接返回当前快照。
func (e *MockEngine) WaitForSettled(taskID contract.TaskID) (*contract.TaskSnapshot, error) {
	snapshot, ok := e.Get(taskID)
	if !ok {
		return nil, fmt.Errorf("任务不存在：%s", taskID)
	}
	return snapshot, nil
}

// ResumeAll 占位引擎没有断点恢复。
func (e *MockEngine) ResumeAll() error { return nil }

// MockToolClient 占位工具客户端。
type MockToolClient struct{}

// ListTools 占位：没有工具。
func (c *MockToolClient) ListTools() ([]contract.ToolDescriptor, error) {
	return []contract.ToolDescriptor{}, nil
}

// Call 占位：一律失败并说明未就绪。
func (c *MockToolClient) Call(name string, args any) (contract.ToolCallResult, error) {
	return contract.ToolCallResult{OK: false, Error: "占位工具客户端：internal/tools 尚未接入（" + name + "）"}, nil
}

// Close 占位：没有需要释放的资源。
func (c *MockToolClient) Close() error { return nil }
