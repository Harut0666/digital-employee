package server_test

import (
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

// 前台 Agent 停在权限确认卡（仅限工作区下运行命令要问）上时也能换模型（规格“会话里选模型不再有状态限制”）：
// 新选择写进会话，当前这一轮继续用旧模型，下一轮对话起才用新的。
func TestModelCanBeSelectedWhileTheAgentWaitsOnAPermissionCard(t *testing.T) {
	service := newRoutingModelService(t)
	service.script(map[string][]fakeModelTurn{
		"model-a": {
			{ToolCalls: []agent.ScriptedToolCall{{Name: contract.ToolShell, Arguments: shellCall("echo model-switch-ok")}}},
			{Text: "写好了。"},
		},
		"model-b": {{Text: "换成新模型回答。"}},
	})
	h := newHarness(t, nil, withModelConnections(service.connection("conn", "model-a", "model-b")))
	created := h.createSession()
	h.setModel(created.ID, "conn", "model-a")
	stream := h.openStream(created.ID)
	defer stream.close()

	turn := h.sayAsync(created.ID, contract.RoleInitiator, "跑个命令")
	card := h.waitPending(created.ID)

	// Agent 正停在权限确认卡上：换模型不被“上一条消息还在处理”挡住
	h.setModel(created.ID, "conn", "model-b")
	requireSessionModel(t, h, created.ID, "conn", "model-b")
	if last := h.lastModel(); last == nil || last.Model != "model-b" {
		t.Fatalf("lastModel 应跟着更新：%+v", last)
	}
	// 卡还在等，这一轮没被打断
	if pending := h.waitPending(created.ID); pending.RequestID != card.RequestID {
		t.Fatalf("换模型不该动权限确认卡：%+v", pending)
	}

	h.mustDecide(created.ID, card, session.DecisionAllowOnce)
	h.waitTurn(stream, turn)
	if !ranShell(h.readSession(created.ID)) {
		t.Fatal("允许之后命令应运行")
	}
	// 当前这一轮继续用旧模型：两次请求都是 model-a
	if got := service.modelsCalled(); len(got) != 2 || got[0] != "model-a" || got[1] != "model-a" {
		t.Fatalf("当前这一轮应一直用旧模型：%v", got)
	}

	// 下一轮对话起用新选择
	h.say(created.ID, contract.RoleInitiator, "再说一句")
	if got := service.modelsCalled(); len(got) != 3 || got[2] != "model-b" {
		t.Fatalf("下一轮应用新模型：%v", got)
	}
}

// 非发起人仍不能选模型；两个人同时选时第二个收到 session_busy 的约束不变（这里只验角色）。
func TestOnlyTheInitiatorCanSelectAModelWhileTheAgentIsBusy(t *testing.T) {
	service := newRoutingModelService(t)
	service.script(map[string][]fakeModelTurn{
		"model-a": {{ToolCalls: []agent.ScriptedToolCall{{Name: contract.ToolShell, Arguments: shellCall("echo busy-ok")}}}, {Text: "好。"}},
	})
	h := newHarness(t, nil, withModelConnections(service.connection("conn", "model-a", "model-b")))
	created := h.createSession()
	h.setModel(created.ID, "conn", "model-a")
	stream := h.openStream(created.ID)
	defer stream.close()
	turn := h.sayAsync(created.ID, contract.RoleInitiator, "写")
	card := h.waitPending(created.ID)

	status, _ := h.trySetModel(created.ID, contract.RoleReviewer, map[string]any{"connection": "conn", "model": "model-b"})
	if status != 403 {
		t.Fatalf("审核人选模型 = %d，want 403", status)
	}
	requireSessionModel(t, h, created.ID, "conn", "model-a")

	h.mustDecide(created.ID, card, session.DecisionDeny)
	h.waitTurn(stream, turn)
}
