// 前台 Agent 的一轮：发出去的消息立即返回（HTTP 202），回复在后台流式跑，事件走会话事件流（Spec 0006、T40）。
//
// 同一会话同一时间只跑一轮，一轮里的模型调用和工具依次执行（与以前一次直接调用等价）。
// “下一步”指这一轮里下一次调模型：运行中收到的新消息在下一步之前插进对话。
// 停止时取消正在进行的模型调用：已输出的部分以“已中断”保存，没开始的工具调用记“已取消”。
package session

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"digital-employee/internal/contract"
)

// 一轮的结局（内部状态）。
const (
	turnCompleted = iota // 说完了
	turnStopped          // 人点了停止
	turnErrored          // 模型不可用
)

// turnJob 一个会话的一轮回复。
type turnJob struct {
	manager *Manager
	session string
	id      int64
	role    contract.Role
	cancel  context.CancelFunc

	mu sync.Mutex
	// mode 一轮的结局（loop 结束时才写）
	mode int
	// steer 插话的条数；included 已经送进模型请求的条数，steer 比它多说明还有话没送达
	steer    int
	included int

	// floor 登记这一轮时会话日志写到第几条：之后写进来的人的话按插话处理
	floor int64
	// delivered 已经送进模型请求的人的话（日志序号）
	delivered map[int64]bool
}

// Accepted 发消息的响应：这一轮（或插话将进入的那一轮）的编号。
type Accepted struct {
	OK   bool  `json:"ok"`
	Turn int64 `json:"turn"`
}

// StopResult 停止的响应。
type StopResult struct {
	OK      bool  `json:"ok"`
	Stopped bool  `json:"stopped"`
	Turn    int64 `json:"turn,omitempty"`
}

// Send 收一条人的消息，立即返回：有回复在跑就排进“下一步之前”，否则开新一轮。
//
// 顺序是固定的：先把这一句写进日志，再记作插话，最后确保有一轮在跑。
// 正在跑的那一轮拿到写锁后才读日志，写在这之前的话一定能在下一次调模型之前带上；
// 这一轮恰好已经结束时，插话让下一轮一开始就把它带走（多记的一条在下一轮里看作“没有新的话”）。
func (m *Manager) Send(id string, role contract.Role, text string) (*Accepted, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, failure(contract.ErrInvalidInput, "消息不能为空")
	}
	if _, err := m.load(id); err != nil {
		return nil, err
	}
	if _, err := m.writeUser(id, role, text); err != nil {
		return nil, err
	}
	m.mu.Lock()
	existing := m.jobs[id]

	m.mu.Unlock()
	if existing != nil {
		noteSteer(existing)
		return &Accepted{OK: true, Turn: existing.id}, nil
	}
	m.mu.Lock()
	job, err := m.startTurnLocked(id)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	noteSteer(job)
	return &Accepted{OK: true, Turn: job.id}, nil
}

// noteSteer 记下一条插话。循环结束时还没送进模型的插话（停止之后）会紧接着开一轮。
func noteSteer(job *turnJob) {
	job.mu.Lock()
	job.steer++
	job.mu.Unlock()
}

// turnOf 会话当前这一轮；没有就开新一轮。
func (m *Manager) turnOf(id string) (*turnJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startTurnLocked(id)
}

// Stop 中断这一轮。没有在回复时什么也不做，返回 stopped=false。
func (m *Manager) Stop(id string) (*StopResult, error) {
	if _, err := m.load(id); err != nil {
		return nil, err
	}
	m.mu.Lock()
	job := m.jobs[id]
	m.mu.Unlock()
	if job == nil {
		return &StopResult{OK: true}, nil
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	if job.cancel == nil {
		return &StopResult{OK: true}, nil
	}
	job.cancel()
	return &StopResult{OK: true, Stopped: true, Turn: job.id}, nil
}

// Activity 会话现在有没有一轮在跑，以及这一轮已经输出的内容、发出的卡片和排着的插话。
func (m *Manager) Activity(id string) (*AgentActivity, error) {
	if _, err := m.load(id); err != nil {
		return nil, err
	}
	m.mu.Lock()
	job := m.jobs[id]
	m.mu.Unlock()
	b := m.bus(id)
	activity := b.activity()
	if job != nil {
		job.mu.Lock()
		if job.steer > job.included {
			activity.Running, activity.Turn = true, job.id
		}
		job.mu.Unlock()
	}
	return &activity, nil
}

// Subscribe 订阅一个会话之后的事件；返回取消订阅的函数。notify 在后台一直等到有事件才调用。
func (m *Manager) Subscribe(id string, notify func([]AgentEvent)) (func(), error) {
	if _, err := m.load(id); err != nil {
		return nil, err
	}
	sub, cancel := m.bus(id).subscribe()
	stop := make(chan struct{})
	go func() {
		b := m.bus(id)
		for {
			select {
			case <-sub.notify:
				if events := b.drain(sub); len(events) > 0 {
					notify(events)
				}
			case <-stop:
				return
			}
		}
	}()
	return func() {
		close(stop)
		cancel()
	}, nil
}

// ---------------------------------------------------------------- 轮次的登记与调度

// bus 一个会话的事件订阅者。调用时不能已经拿着 m.mu（看 busLocked）。
func (m *Manager) bus(id string) *bus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.busLocked(id)
}

func (m *Manager) busLocked(id string) *bus {
	b := m.buses[id]
	if b == nil {
		b = newBus()
		m.buses[id] = b
	}
	return b
}

// startTurnLocked 在 m.mu 内调用：登记新一轮并起后台协程。
func (m *Manager) startTurnLocked(id string) (*turnJob, error) {
	if _, err := m.load(id); err != nil {
		return nil, err
	}
	if job := m.jobs[id]; job != nil {
		return job, nil
	}
	// 轮次编号接着日志里上一轮的号，进程重启后也不会倒退
	last := int64(0)
	if current, err := m.load(id); err == nil {
		last = lastTurn(current)
	}
	if m.turnSeq[id] < last {
		m.turnSeq[id] = last
	}
	m.turnSeq[id]++
	ctx, cancel := context.WithCancel(context.Background())
	job := &turnJob{manager: m, session: id, id: m.turnSeq[id], cancel: cancel, floor: last, delivered: map[int64]bool{}}

	m.jobs[id] = job
	b := m.busLocked(id)
	b.started(job.id)
	// 一轮开始的推送要早于任何等待方（测试订阅事件流）可能错过它
	b.publish(AgentEvent{Turn: job.id, Type: EventTurnStart})
	go m.runTurn(ctx, job, b)
	return job, nil
}

// runTurn 跑一轮，结束后看还有没有插话：有就接着开新一轮。
func (m *Manager) runTurn(ctx context.Context, job *turnJob, b *bus) {
	outcome := m.runOnce(ctx, job)
	job.mu.Lock()
	job.mode = outcome.mode
	job.cancel = nil
	// 还有没说出口的插话：停止或出错时它们属于下一轮，正常说完时它们已经在最后一次调模型之前送达
	pending := job.steer > job.included
	job.mu.Unlock()

	reason := outcome.reason
	if ctx.Err() != nil {
		reason = TurnStopped
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.jobs[job.session] == job {
		delete(m.jobs, job.session)
	}

	// 这一轮的写入和推送都已落地（b.finished 与它们在同一把锁里按顺序进行）。
	// 还有没说出口的插话时，先开新一轮再宣布这一轮结束：同时开出来的两轮一定按顺序排。
	if pending {
		_, _ = m.startTurnLocked(job.session)
	}
	b.finished(job.id, reason)
}

// turnOutcome 一轮的结局。
type turnOutcome struct {
	mode   int
	reason string
}

// runOnce 一轮里的循环：调模型 → 执行工具 → 再看有没有插话，直到说完、停止或出错。
func (m *Manager) runOnce(ctx context.Context, job *turnJob) turnOutcome {
	loop := newTurnLoop(job)
	outcome, err := loop.run(ctx)
	if err != nil {
		// 会话日志写不进去：这一轮只能停下
		return turnOutcome{mode: turnErrored, reason: TurnError}
	}
	return outcome
}

// writeUser 写一条人的消息（不占这一轮的锁）。
func (m *Manager) writeUser(id string, role contract.Role, text string) (Message, error) {
	// 这一句的写入和“算作一条插话”是一起的：写完立刻记账，正在跑的那一轮不会漏掉它
	// （写锁由 store.append 自己占，这里不能再拿一次）
	t, done, err := m.beginUnlocked(id, role)
	if err != nil {
		return Message{}, err
	}
	defer done()
	if err := t.write(Message{Kind: KindUser, Role: role, Text: text}); err != nil {
		return Message{}, err
	}
	m.mu.Lock()
	job := m.jobs[id]
	m.mu.Unlock()
	if job != nil {
		noteSteer(job)
	}
	return t.out[len(t.out)-1], nil
}

// lockOf 一个会话的写锁。
func (m *Manager) lockOf(id string) *sessionLock {
	return m.store.lockOf(id)
}

// humanLock 一个会话上人的操作的互斥锁：两个人同时点确认、提交时只有一个人进去。
func (m *Manager) humanLock(id string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock := m.humanLocks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		m.humanLocks[id] = lock
	}
	return lock
}

// lastTurn 日志里上一轮的编号（进程重启后接着往下编）。
func lastTurn(current *record) int64 {
	var last int64
	for _, message := range current.messages {
		if message.Turn > last {
			last = message.Turn
		}
	}
	return last
}

var _ = json.Marshal
var _ = strings.TrimSpace
