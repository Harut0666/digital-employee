// 并发：两个任务同时跑到渲染步骤（配合 go test -race 运行）。
package server_test

import (
	"context"
	"sync"
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

// twice 同一份剧本够两个任务各用一次。
func twice(script agent.ModelScript) agent.ModelScript {
	doubled := agent.ModelScript{}
	for step, turns := range script {
		doubled[step] = append(append([]agent.ScriptedTurn{}, turns...), turns...)
	}
	return doubled
}

// rendezvous 让若干任务在某个判断步骤的模型调用处汇合，到齐后一起放行。
// 不汇合的话两个任务到达渲染的时间差比渲染本身还长，测试碰不到并发。
type rendezvous struct {
	agent.ModelProvider
	step string

	mu      sync.Mutex
	waiting int
	open    chan struct{}
}

func (r *rendezvous) Resolve(stepID string, ref *contract.ModelRef) (*agent.ResolvedStepModel, error) {
	resolved, err := r.ModelProvider.Resolve(stepID, ref)
	if err != nil || stepID != r.step {
		return resolved, err
	}
	stream := resolved.Stream
	resolved.Stream = func(ctx context.Context, model agent.Model, request agent.StreamRequest, options agent.StreamOptions) (*agent.AssistantMessage, error) {
		r.arrive(ctx)
		return stream(ctx, model, request, options)
	}
	return resolved, nil
}

func (r *rendezvous) arrive(ctx context.Context) {
	r.mu.Lock()
	r.waiting--
	if r.waiting == 0 {
		close(r.open)
	}
	r.mu.Unlock()
	select {
	case <-r.open:
	case <-ctx.Done():
	}
}

func TestTwoTasksRenderAtTheSameTime(t *testing.T) {
	// write_report 紧挨着渲染步骤
	meet := withModelWrapper(func(model agent.ModelProvider) agent.ModelProvider {
		return &rendezvous{ModelProvider: model, step: "write_report", waiting: 2, open: make(chan struct{})}
	})
	h := newHarness(t, twice(caseScript(t, "A-01")), meet)
	first := h.createTask(a01Files(t))
	second := h.createTask(a01Files(t))

	for _, task := range []contract.TaskSnapshot{first, second} {
		if reply := h.tryCommand(task.ID, initiator, cmd(contract.CmdSubmit)); !reply.OK {
			t.Fatalf("提交被拒绝：%+v", reply)
		}
	}
	for _, task := range []contract.TaskSnapshot{first, second} {
		settled := h.settled(task.ID)
		expectState(t, settled, contract.StateAwaitingReview)
		if len(settled.Deliverables) == 0 {
			t.Fatalf("任务 %s 没有交付物", task.ID)
		}
	}
}
