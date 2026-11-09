// 权限关口的单元测试（T48）：运行员工和把目录文件用作材料这两类动作的处理。
//
// 真正的 run_employee 在 T49 接入，这里用测试里的假工具走同一个关口（turnLoop.call → gate），
// 和 HTTP 主接缝（internal/server/permission_test.go）里写文件的测试互补。
package session

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

// 矩阵本身：Spec 0007 的表。
func TestDecideFollowsTheMatrix(t *testing.T) {
	want := map[string]map[string]Verdict{
		ActionWriteFile: {
			PermissionPlan: VerdictDeny, PermissionAsk: VerdictAsk, PermissionAcceptEdits: VerdictAllow, PermissionAuto: VerdictAllow, PermissionYolo: VerdictAllow,
		},
		ActionRunEmployee: {
			PermissionPlan: VerdictDeny, PermissionAsk: VerdictAsk, PermissionAcceptEdits: VerdictAsk, PermissionAuto: VerdictAllow, PermissionYolo: VerdictAllow,
		},
		ActionUseFolderFile: {
			PermissionPlan: VerdictDeny, PermissionAsk: VerdictAsk, PermissionAcceptEdits: VerdictAsk, PermissionAuto: VerdictAsk, PermissionYolo: VerdictAllow,
		},
		"": {
			PermissionPlan: VerdictAllow, PermissionAsk: VerdictAllow, PermissionAcceptEdits: VerdictAllow, PermissionAuto: VerdictAllow, PermissionYolo: VerdictAllow,
		},
	}
	for action, row := range want {
		for mode, verdict := range row {
			if got := Decide(mode, action); got != verdict {
				t.Errorf("Decide(%s, %q) = %s，want %s", mode, action, got, verdict)
			}
		}
	}
}

// 档位是单调的：越往松，免问的越多，不存在高档位反而问得更多的动作。
func TestModesAreMonotonic(t *testing.T) {
	rank := map[Verdict]int{VerdictDeny: 0, VerdictAsk: 1, VerdictAllow: 2}
	for _, action := range []string{ActionWriteFile, ActionRunEmployee, ActionUseFolderFile} {
		previous := -1
		for _, mode := range PermissionModes {
			current := rank[Decide(mode, action)]
			if current < previous {
				t.Errorf("%s 在 %s 下比更严的档位问得更多", action, mode)
			}
			previous = current
		}
	}
}

// gateFixture 一个会话和它的一轮：不经过模型，直接让假工具走关口。
type gateFixture struct {
	t       *testing.T
	manager *Manager
	id      string
	loop    *turnLoop
	ran     atomic.Int32
}

func newGateFixture(t *testing.T) *gateFixture {
	t.Helper()
	manager := New(Deps{Dir: t.TempDir(), Workspace: func(string) bool { return true }})
	view, err := manager.Create("ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	current, err := manager.load(view.ID)
	if err != nil {
		t.Fatal(err)
	}
	job := &turnJob{manager: manager, session: view.ID, id: 1, role: contract.RoleInitiator, delivered: map[int64]bool{}}
	manager.mu.Lock()
	manager.jobs[view.ID] = job
	manager.mu.Unlock()
	loop := &turnLoop{job: job, bus: manager.bus(view.ID), turn: &turn{manager: manager, current: current, role: contract.RoleInitiator}}
	return &gateFixture{t: t, manager: manager, id: view.ID, loop: loop}
}

// fake 一个假工具：只数自己跑了几次。
func (f *gateFixture) fake(name, action string) map[string]tool {
	return map[string]tool{name: {
		spec:   agent.ToolSpec{Name: name},
		action: action,
		describe: func(arguments map[string]any) (string, string) {
			object, _ := arguments["object"].(string)
			return object, "一句话说明"
		},
		run: func(map[string]any) (any, error) {
			f.ran.Add(1)
			return map[string]any{"ok": true}, nil
		},
	}}
}

func (f *gateFixture) mustMode(mode string) {
	f.t.Helper()
	if _, err := f.manager.SetPermissionMode(f.id, contract.RoleInitiator, mode); err != nil {
		f.t.Fatal(err)
	}
}

// pending 等权限确认卡出现，返回它的编号。
func (f *gateFixture) pending() Card {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		current, err := f.manager.load(f.id)
		if err != nil {
			f.t.Fatal(err)
		}
		for _, message := range f.manager.projectPermissions(visibleMessages(current.messages), current) {
			if message.Card != nil && message.Card.Type == CardPermission && message.Card.Pending {
				return *message.Card
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	f.t.Fatal("等不到权限确认卡")
	return Card{}
}

func (f *gateFixture) messages(kind string) []Message {
	current, err := f.manager.load(f.id)
	if err != nil {
		f.t.Fatal(err)
	}
	out := []Message{}
	for _, message := range current.messages {
		if message.Kind == kind {
			out = append(out, message)
		}
	}
	return out
}

// 运行员工、把目录文件用作材料：五个模式下的处理（拒绝 / 问 / 免问）。
func TestGateHandlesRunEmployeeAndFolderFileInEveryMode(t *testing.T) {
	cases := []struct {
		action string
		want   map[string]Verdict
	}{
		{ActionRunEmployee, map[string]Verdict{
			PermissionPlan: VerdictDeny, PermissionAsk: VerdictAsk, PermissionAcceptEdits: VerdictAsk, PermissionAuto: VerdictAllow, PermissionYolo: VerdictAllow,
		}},
		{ActionUseFolderFile, map[string]Verdict{
			PermissionPlan: VerdictDeny, PermissionAsk: VerdictAsk, PermissionAcceptEdits: VerdictAsk, PermissionAuto: VerdictAsk, PermissionYolo: VerdictAllow,
		}},
	}
	for _, tc := range cases {
		for _, mode := range PermissionModes {
			t.Run(tc.action+"/"+mode, func(t *testing.T) {
				f := newGateFixture(t)
				f.mustMode(mode)
				open := f.fake("fake_tool", tc.action)
				call := agent.ContentBlock{Type: agent.ContentToolCall, ID: "c1", Name: "fake_tool", Arguments: map[string]any{"object": "经营周报"}}

				switch tc.want[mode] {
				case VerdictDeny:
					block, err := f.loop.call(context.Background(), open, call)
					if err != nil || !block.IsError || !strings.Contains(block.Text, PermissionModeLabels[mode]+"模式") {
						t.Fatalf("应被拒绝：%+v %v", block, err)
					}
					if f.ran.Load() != 0 || len(f.messages(KindToolDenied)) != 1 {
						t.Fatalf("拒绝时工具不该执行，要留痕：跑了 %d 次", f.ran.Load())
					}
				case VerdictAllow:
					block, err := f.loop.call(context.Background(), open, call)
					if err != nil || block.IsError || f.ran.Load() != 1 {
						t.Fatalf("应免问放行：%+v %v，跑了 %d 次", block, err, f.ran.Load())
					}
					if got := f.messages(KindPermissionAllowed); len(got) != 1 || got[0].Tool != "fake_tool" {
						t.Fatalf("免问放行要留痕：%+v", got)
					}
				case VerdictAsk:
					done := make(chan agent.ContentBlock, 1)
					go func() {
						block, _ := f.loop.call(context.Background(), open, call)
						done <- block
					}()
					card := f.pending()
					if card.Action != tc.action || card.ActionLabel != ActionLabels[tc.action] || card.Object != "经营周报" || card.Tool != "fake_tool" {
						t.Fatalf("权限确认卡写明动作和对象：%+v", card)
					}
					if f.ran.Load() != 0 {
						t.Fatal("等确认时工具不该先执行")
					}
					if _, err := f.manager.DecidePermission(f.id, contract.RoleInitiator, card.RequestID, DecisionAllowOnce); err != nil {
						t.Fatal(err)
					}
					if block := <-done; block.IsError || f.ran.Load() != 1 {
						t.Fatalf("允许之后应继续执行：%+v", block)
					}
				}
			})
		}
	}
}

// 问的时候拒绝：工具不执行，模型拿到“用户拒绝”。
func TestGateDenialByThePersonIsReturnedToTheModel(t *testing.T) {
	f := newGateFixture(t)
	open := f.fake("fake_tool", ActionRunEmployee)
	call := agent.ContentBlock{Type: agent.ContentToolCall, ID: "c1", Name: "fake_tool", Arguments: map[string]any{"object": "经营周报"}}
	done := make(chan agent.ContentBlock, 1)
	go func() {
		block, _ := f.loop.call(context.Background(), open, call)
		done <- block
	}()
	card := f.pending()
	if _, err := f.manager.DecidePermission(f.id, contract.RoleInitiator, card.RequestID, DecisionDeny); err != nil {
		t.Fatal(err)
	}
	block := <-done
	if !block.IsError || !strings.Contains(block.Text, "用户拒绝") || !strings.Contains(block.Text, "经营周报") || f.ran.Load() != 0 {
		t.Fatalf("拒绝的结果 = %+v，跑了 %d 次", block, f.ran.Load())
	}
}

// “本会话总是允许这一类”按动作类别记：运行员工点了总是允许，不影响目录文件那一类。
func TestAlwaysAllowIsPerActionClass(t *testing.T) {
	f := newGateFixture(t)
	run := f.fake("fake_run", ActionRunEmployee)
	folder := f.fake("fake_folder", ActionUseFolderFile)
	call := func(name string) agent.ContentBlock {
		return agent.ContentBlock{Type: agent.ContentToolCall, ID: "c-" + name, Name: name, Arguments: map[string]any{}}
	}
	done := make(chan agent.ContentBlock, 1)
	go func() {
		block, _ := f.loop.call(context.Background(), run, call("fake_run"))
		done <- block
	}()
	if _, err := f.manager.DecidePermission(f.id, contract.RoleInitiator, f.pending().RequestID, DecisionAlwaysAllow); err != nil {
		t.Fatal(err)
	}
	<-done
	// 同一类再来：不问
	if block, err := f.loop.call(context.Background(), run, call("fake_run")); err != nil || block.IsError {
		t.Fatalf("总是允许之后不该再问：%+v %v", block, err)
	}
	if f.ran.Load() != 2 {
		t.Fatalf("两次都该执行：%d", f.ran.Load())
	}
	// 另一类仍然要问
	go func() {
		block, _ := f.loop.call(context.Background(), folder, call("fake_folder"))
		done <- block
	}()
	card := f.pending()
	if card.Action != ActionUseFolderFile {
		t.Fatalf("另一类动作仍要问：%+v", card)
	}
	if _, err := f.manager.DecidePermission(f.id, contract.RoleInitiator, card.RequestID, DecisionDeny); err != nil {
		t.Fatal(err)
	}
	<-done
}

// 进程重启后没有人在等的权限确认：读出来记为已取消，不再显示按钮。
func TestOrphanedPermissionCardReadsAsCancelled(t *testing.T) {
	f := newGateFixture(t)
	card := Card{Type: CardPermission, RequestID: "00112233445566ff", Action: ActionWriteFile, Pending: true, Choices: permissionButtons()}
	if _, err := f.manager.store.append(f.id, Message{Kind: KindCard, Card: &card}); err != nil {
		t.Fatal(err)
	}
	detail, err := f.manager.read(f.id)
	if err != nil {
		t.Fatal(err)
	}
	var got *Card
	for _, message := range detail.Messages {
		if message.Card != nil && message.Card.Type == CardPermission {
			got = message.Card
		}
	}
	if got == nil || got.Pending || got.Decision != DecisionCancelled || len(got.Choices) != 0 {
		t.Fatalf("没人等的确认卡 = %+v", got)
	}
	if _, err := f.manager.DecidePermission(f.id, contract.RoleInitiator, card.RequestID, DecisionAllowOnce); err == nil {
		t.Fatal("没人等的确认不能再答复")
	} else if code, _ := CodeOf(err); code != ErrPermissionSettled {
		t.Fatalf("错误码 = %q", code)
	}
}

// 停止（取消 context）时等确认的一轮醒来，卡片记为已取消，工具没执行。
func TestCancelWhileAskingMarksTheCardCancelled(t *testing.T) {
	f := newGateFixture(t)
	open := f.fake("fake_tool", ActionRunEmployee)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan agent.ContentBlock, 1)
	go func() {
		block, _ := f.loop.call(ctx, open, agent.ContentBlock{Type: agent.ContentToolCall, ID: "c1", Name: "fake_tool", Arguments: map[string]any{}})
		done <- block
	}()
	card := f.pending()
	cancel()
	if block := <-done; !block.IsError || !strings.Contains(block.Text, "已取消") || f.ran.Load() != 0 {
		t.Fatalf("停止的结果 = %+v", block)
	}
	decided := f.messages(kindPermissionDecided)
	if len(decided) != 1 || decided[0].RequestID != card.RequestID || decided[0].Decision != DecisionCancelled {
		t.Fatalf("取消要写进日志：%+v", decided)
	}
	if _, err := f.manager.DecidePermission(f.id, contract.RoleInitiator, card.RequestID, DecisionAllowOnce); err == nil {
		t.Fatal("已取消的确认不能再答复")
	}
}

// 换模式之后写进日志的“选择权限模式”清空总是允许。
func TestChangingModeClearsAlwaysAllowed(t *testing.T) {
	f := newGateFixture(t)
	open := f.fake("fake_tool", ActionRunEmployee)
	done := make(chan agent.ContentBlock, 1)
	go func() {
		block, _ := f.loop.call(context.Background(), open, agent.ContentBlock{Type: agent.ContentToolCall, ID: "c1", Name: "fake_tool", Arguments: map[string]any{}})
		done <- block
	}()
	if _, err := f.manager.DecidePermission(f.id, contract.RoleInitiator, f.pending().RequestID, DecisionAlwaysAllow); err != nil {
		t.Fatal(err)
	}
	<-done
	current, _ := f.manager.load(f.id)
	if !current.alwaysAllowed[ActionRunEmployee] {
		t.Fatalf("总是允许应记在会话里：%v", current.alwaysAllowed)
	}
	f.mustMode(PermissionAsk)
	current, _ = f.manager.load(f.id)
	if len(current.alwaysAllowed) != 0 {
		t.Fatalf("换模式应清空：%v", current.alwaysAllowed)
	}
}
