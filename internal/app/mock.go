// 占位任务引擎：internal/engine 就绪前的联调替身。
//
// 只实现 contract.Engine 的最小语义：建草稿、提交时产生 state_changed 事件（前端与 SSE 联调用）、
// 其它命令一律按非法流转拒绝。真实引擎接入后由启动入口的 EngineFactory 替换，本文件不会进入生产路径。
package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"digital-employee/internal/contract"
	"digital-employee/internal/server"
)

// MockEngine 内存任务引擎。
type MockEngine struct {
	mu      sync.Mutex
	logger  *server.Logger
	tasks   map[string]*mockTask
	order   []string
	nextSub int
}

type mockTask struct {
	snapshot  contract.TaskSnapshot
	events    []contract.TaskEvent
	listeners map[int]func(contract.TaskEvent)
	updatedAt string
}

// NewMockEngine 创建占位引擎。
func NewMockEngine(logger *server.Logger) *MockEngine {
	return &MockEngine{logger: logger, tasks: map[string]*mockTask{}, order: []string{}}
}

func eventTS() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

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
	task.events = append(task.events, event)
	task.updatedAt = event.TS
	for _, listener := range task.listeners {
		listener(event)
	}
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
	task := &mockTask{snapshot: snapshot, listeners: map[int]func(contract.TaskEvent){}}
	e.tasks[id] = task
	e.order = append(e.order, id)
	e.emit(task, contract.TaskEvent{
		Type: contract.EventTaskCreated, Employee: input.Employee,
		Form: form, Attachments: attachments,
	})
	copied := task.snapshot
	return &copied, nil
}

// Command 命令：只实现 submit 的占位流转。
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
			Code:    contract.PauseInvalidInput,
			Message: "占位引擎：internal/engine 尚未接入，真实执行流程待接入后可用",
			Options: []string{contract.CmdTerminate},
		}
		task.snapshot.State = contract.StateAwaitingInitiator
		task.snapshot.Pause = pause
		e.emit(task, contract.TaskEvent{
			Type: contract.EventStateChanged, From: from, To: contract.StateAwaitingInitiator,
			Trigger: contract.TriggerAdmissionFailed, Actor: contract.RoleSystem, Pause: pause,
		})
		copied := task.snapshot
		return contract.CommandResult{OK: true, Snapshot: &copied}
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
	copied := task.snapshot
	return &copied, true
}

// List 任务列表（新的在前）。
func (e *MockEngine) List() []contract.TaskSummary {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]contract.TaskSummary, 0, len(e.order))
	for index := len(e.order) - 1; index >= 0; index-- {
		task := e.tasks[e.order[index]]
		out = append(out, contract.TaskSummary{
			ID: task.snapshot.ID, Employee: task.snapshot.Employee,
			Title: task.snapshot.Employee, State: task.snapshot.State, UpdatedAt: task.updatedAt,
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
