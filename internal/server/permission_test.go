// 权限模式与文件写入工具（T48）：全部从主接缝验证，模型走剧本，不联网。
//
// 观察点只有 HTTP：会话视图里的模式、会话消息（权限确认卡和留痕）、会话事件流、PUT 模式、
// POST 权限决定，以及工作区目录里文件的实际内容。
// 运行员工、目录文件确认这两类动作的关口判定在 internal/session 的单元测试里用假工具验证（run_employee 在 T49 接入）。
package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

// ---------------------------------------------------------------- 装置补充

// setMode 发起人在输入框左下角选权限模式。
func (h *harness) setMode(sessionID string, role contract.Role, mode string) (int, []byte) {
	h.t.Helper()
	raw, err := json.Marshal(map[string]any{"mode": mode})
	if err != nil {
		h.t.Fatal(err)
	}
	return h.send(http.MethodPut, "/api/sessions/"+sessionID+"/permission-mode", role, "application/json", bytes.NewReader(raw))
}

// mustSetMode 选模式，要求成功。
func (h *harness) mustSetMode(sessionID string, mode string) session.Reply {
	h.t.Helper()
	status, data := h.setMode(sessionID, contract.RoleInitiator, mode)
	if status != http.StatusOK {
		h.t.Fatalf("切换权限模式为 %s 失败：%d %s", mode, status, data)
	}
	return decode[session.Reply](h.t, data)
}

// decide 人在权限确认卡上作答。
func (h *harness) decide(sessionID, requestID string, role contract.Role, decision string) (int, []byte) {
	h.t.Helper()
	return h.postJSON("/api/sessions/"+sessionID+"/permissions/"+requestID, role, map[string]any{"decision": decision})
}

// mustDecide 作答，要求成功。
func (h *harness) mustDecide(sessionID string, card session.Card, decision string) {
	h.t.Helper()
	if status, data := h.decide(sessionID, card.RequestID, contract.RoleInitiator, decision); status != http.StatusOK {
		h.t.Fatalf("答复 %s 失败：%d %s", decision, status, data)
	}
}

// waitPending 等一张还没作答的权限确认卡出现在会话里，返回它。
func (h *harness) waitPending(sessionID string) session.Card {
	h.t.Helper()
	deadline := time.Now().Add(stopTimeout)
	for {
		detail := h.readSession(sessionID)
		for _, message := range detail.Messages {
			if card := message.Card; card != nil && card.Type == session.CardPermission && card.Pending {
				return *card
			}
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("等不到权限确认卡：%+v", detail.Messages)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// permissionCards 会话里的权限确认卡（不论是否已作答），按出现顺序。
func permissionCards(detail session.Detail) []session.Card {
	out := []session.Card{}
	for _, message := range detail.Messages {
		if card := message.Card; card != nil && card.Type == session.CardPermission {
			out = append(out, *card)
		}
	}
	return out
}

// workspaceText 工作区目录里一个文件的内容。
func (h *harness) workspaceText(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(h.workspace, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("读工作区文件 %s 失败：%v", rel, err)
	}
	return string(raw)
}

// workspaceHas 工作区目录里有没有这个文件。
func (h *harness) workspaceHas(rel string) bool {
	_, err := os.Stat(filepath.Join(h.workspace, filepath.FromSlash(rel)))
	return err == nil
}

// 16 位十六进制的权限确认编号。
var requestIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// deniedTools 会话日志里被拒绝的工具调用，按写入顺序。
func deniedTools(detail session.Detail) []session.Message {
	return entriesOf(detail.Messages, session.KindToolDenied)
}

// toolResultTexts 模型在最后一次请求里收到的全部工具结果文字。
func toolResultTexts(h *harness) []string {
	requests := frontdeskRequests(h)
	out := []string{}
	for _, message := range requests[len(requests)-1].Request.Messages {
		for _, block := range message.Content {
			if block.Type == agent.ContentToolResult {
				out = append(out, block.Text)
			}
		}
	}
	return out
}

// assistantAfter 会话里指定序号之后的第一条 Agent 回复（拒绝之后它换办法说话）。
func assistantAfter(detail session.Detail, seq int64) string {
	for _, message := range detail.Messages {
		if message.Seq > seq && message.Kind == session.KindAssistant {
			return message.Text
		}
	}
	return ""
}

// prefaceWithEmployee 让会话由经营周报员工接手、还没提交（接手那一步不调模型，剧本的第一轮留给之后的对话）。
func (h *harness) prefaceWithEmployee() session.View {
	h.t.Helper()
	created := h.createSession()
	h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)
	return created
}

// writeAndEditScript 前台 Agent 依次调 write_file、edit_file：先新建 notes/周会要点.md，再改其中一段。
func writeAndEditScript() agent.ModelScript {
	return frontdeskScript(
		modelCalls(contract.ToolWriteFile, map[string]any{"path": "notes/周会要点.md", "content": "# 周会要点\n- 华南环比下降\n- 华东持平\n"}),
		modelCalls(contract.ToolEditFile, map[string]any{"path": "notes/周会要点.md", "oldText": "华东持平", "newText": "华东小幅上升"}),
		modelSays("要点已写进 notes/周会要点.md。"),
	)
}

const (
	noteAfterWrite = "# 周会要点\n- 华南环比下降\n- 华东持平\n"
	noteAfterEdit  = "# 周会要点\n- 华南环比下降\n- 华东小幅上升\n"
)

// ---------------------------------------------------------------- 新会话总是 ask，只有发起人能改

func TestNewSessionStartsInAskMode(t *testing.T) {
	h := newHarness(t, frontdeskScript(modelSays("好的。")))
	first := h.createSession()
	if first.PermissionMode != session.PermissionAsk {
		t.Fatalf("新会话的模式 = %q，want ask", first.PermissionMode)
	}
	h.mustSetMode(first.ID, session.PermissionYolo)

	second := h.createSession()
	if second.PermissionMode != session.PermissionAsk {
		t.Fatalf("再开一个会话的模式 = %q，want ask（不沿用上一次的选择）", second.PermissionMode)
	}
	// 列表里也带着模式；第一个会话记住自己的选择
	modes := map[string]string{}
	for _, view := range h.listSessions() {
		modes[view.ID] = view.PermissionMode
	}
	if modes[first.ID] != session.PermissionYolo || modes[second.ID] != session.PermissionAsk {
		t.Fatalf("列表里的模式 = %v", modes)
	}
}

func TestOnlyTheInitiatorCanChooseTheMode(t *testing.T) {
	h := newHarness(t, frontdeskScript(modelSays("好的。")))
	created := h.createSession()

	for _, role := range []contract.Role{contract.RoleReviewer, contract.RoleOperator, ""} {
		if status, data := h.setMode(created.ID, role, session.PermissionPlan); status != http.StatusForbidden {
			t.Fatalf("角色 %q 改模式应被拒绝：%d %s", role, status, data)
		}
	}
	for _, mode := range []string{"", "god", "ASK"} {
		if status, data := h.setMode(created.ID, contract.RoleInitiator, mode); status != http.StatusBadRequest {
			t.Fatalf("模式 %q 应被拒绝：%d %s", mode, status, data)
		}
	}
	if status, data := h.setMode("s-missing", contract.RoleInitiator, session.PermissionPlan); status != http.StatusNotFound {
		t.Fatalf("不存在的会话改模式：%d %s", status, data)
	}
	// 一个也没改成功：会话还是 ask，日志里没有“选择权限模式”
	detail := h.readSession(created.ID)
	if detail.PermissionMode != session.PermissionAsk {
		t.Fatalf("会话模式 = %q", detail.PermissionMode)
	}
	if got := entriesOf(detail.Messages, session.KindPermissionModeSet); len(got) != 0 {
		t.Fatalf("被拒绝的修改不该写进会话日志：%+v", got)
	}
}

func TestPermissionModeChangeIsRecorded(t *testing.T) {
	h := newHarness(t, frontdeskScript(modelSays("好的。")))
	created := h.createSession()

	reply := h.mustSetMode(created.ID, session.PermissionAuto)
	if reply.Session == nil || reply.Session.PermissionMode != session.PermissionAuto {
		t.Fatalf("切换模式之后的会话 = %+v", reply)
	}
	detail := h.readSession(created.ID)
	recorded := entriesOf(detail.Messages, session.KindPermissionModeSet)
	if len(recorded) != 1 || recorded[0].Mode != session.PermissionAuto {
		t.Fatalf("会话日志里的“选择权限模式” = %+v", recorded)
	}
	if detail.PermissionMode != session.PermissionAuto {
		t.Fatalf("会话模式 = %q", detail.PermissionMode)
	}
}

// ---------------------------------------------------------------- 五个模式和两只文件工具

// 新建或修改文件这一类：plan 拒绝，ask 问，其余允许；结果带路径、新建还是修改、字节数，edit_file 另给差异。
func TestFileWritesFollowThePermissionMode(t *testing.T) {
	cases := []struct {
		mode string
		want string // allow / ask / deny
	}{
		{session.PermissionPlan, "deny"},
		{session.PermissionAsk, "ask"},
		{session.PermissionAcceptEdits, "allow"},
		{session.PermissionAuto, "allow"},
		{session.PermissionYolo, "allow"},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			h := newHarness(t, writeAndEditScript())
			created := h.prefaceWithEmployee()
			if tc.mode != session.PermissionAsk {
				h.mustSetMode(created.ID, tc.mode)
			}
			stream := h.openStream(created.ID)
			defer stream.close()
			turn := h.sayAsync(created.ID, contract.RoleInitiator, "把周会要点整理成一个文件")

			if tc.want == "ask" {
				card := h.waitPending(created.ID)
				if card.Action != session.ActionWriteFile || card.Tool != contract.ToolWriteFile || card.Path != "notes/周会要点.md" || card.Object != "notes/周会要点.md" {
					t.Fatalf("第一张权限确认卡 = %+v", card)
				}
				if !requestIDPattern.MatchString(card.RequestID) || card.ActionLabel != "写入文件" || card.Detail == "" {
					t.Fatalf("确认卡缺少编号、动作说法或说明：%+v", card)
				}
				// 三个按钮由程序配好
				decisions := []string{}
				for _, choice := range card.Choices {
					decisions = append(decisions, choice.Decision)
				}
				if fmt.Sprint(decisions) != fmt.Sprint([]string{session.DecisionAllowOnce, session.DecisionAlwaysAllow, session.DecisionDeny}) {
					t.Fatalf("按钮 = %v", decisions)
				}
				h.mustDecide(created.ID, card, session.DecisionAllowOnce)
				card = h.waitPending(created.ID)
				if card.Tool != contract.ToolEditFile || card.Path != "notes/周会要点.md" {
					t.Fatalf("第二张权限确认卡 = %+v", card)
				}
				h.mustDecide(created.ID, card, session.DecisionAllowOnce)
			}
			events := h.waitTurn(stream, turn)
			detail := h.readSession(created.ID)

			switch tc.want {
			case "allow", "ask":
				if got := h.workspaceText(t, "notes/周会要点.md"); got != noteAfterEdit {
					t.Fatalf("文件内容 = %q", got)
				}
				written, _ := loggedToolResult(t, detail, contract.ToolWriteFile).(map[string]any)
				if written["path"] != "notes/周会要点.md" || written["action"] != "created" || written["bytes"] != float64(len(noteAfterWrite)) {
					t.Fatalf("write_file 的结果 = %+v", written)
				}
				edited, _ := loggedToolResult(t, detail, contract.ToolEditFile).(map[string]any)
				if edited["path"] != "notes/周会要点.md" || edited["action"] != "modified" || edited["bytes"] != float64(len(noteAfterEdit)) {
					t.Fatalf("edit_file 的结果 = %+v", edited)
				}
				if diff, _ := edited["diff"].(string); !strings.Contains(diff, "-- 华东持平") || !strings.Contains(diff, "+- 华东小幅上升") {
					t.Fatalf("edit_file 的差异摘要 = %q", diff)
				}
			case "deny":
				if h.workspaceHas("notes/周会要点.md") {
					t.Fatal("plan 模式下不该写出文件")
				}
				denied := deniedTools(detail)
				if len(denied) != 2 || denied[0].Tool != contract.ToolWriteFile || denied[1].Tool != contract.ToolEditFile {
					t.Fatalf("拒绝留痕 = %+v", denied)
				}
				for _, message := range denied {
					if !strings.Contains(message.Reason, "计划模式") {
						t.Fatalf("拒绝原因 = %q", message.Reason)
					}
				}
				// 模型收到拒绝后换办法说话
				if got := assistantAfter(detail, denied[1].Seq); got != "要点已写进 notes/周会要点.md。" {
					t.Fatalf("拒绝之后的回复 = %q", got)
				}
			}

			cards := permissionCards(detail)
			allowed := entriesOf(detail.Messages, session.KindPermissionAllowed)
			switch tc.want {
			case "ask":
				// 每次答复都在会话里：两张已作答的确认卡；事件流里推送过确认卡
				if len(cards) != 2 || cards[0].Pending || cards[1].Pending || cards[0].Decision != session.DecisionAllowOnce || len(cards[0].Choices) != 0 {
					t.Fatalf("权限确认卡 = %+v", cards)
				}
				pushed := false
				for _, event := range events {
					if event.Type == session.EventMessage && event.Msg != nil && event.Msg.Card != nil && event.Msg.Card.Type == session.CardPermission {
						pushed = true
					}
				}
				if !pushed {
					t.Fatalf("事件流里没有推送权限确认卡：%s", eventTypes(events))
				}
				if len(allowed) != 0 {
					t.Fatalf("问过的动作不该再记自动放行：%+v", allowed)
				}
			case "allow":
				// 免问放行也留痕：每次写都有一条
				if len(cards) != 0 || len(allowed) != 2 || allowed[0].Tool != contract.ToolWriteFile || allowed[0].Reason == "" {
					t.Fatalf("免问放行的留痕 = %+v，确认卡 = %+v", allowed, cards)
				}
			case "deny":
				if len(cards) != 0 || len(allowed) != 0 {
					t.Fatalf("拒绝的动作不该有确认卡或放行留痕：%+v %+v", cards, allowed)
				}
			}
			// 会话视图里带着当前模式
			if detail.PermissionMode != tc.mode {
				t.Fatalf("会话模式 = %q，want %q", detail.PermissionMode, tc.mode)
			}
		})
	}
}

// loggedToolResult 会话日志里某个工具最后一次的结果。
func loggedToolResult(t *testing.T, detail session.Detail, tool string) any {
	t.Helper()
	for index := len(detail.Messages) - 1; index >= 0; index-- {
		message := detail.Messages[index]
		if message.Tool == tool && message.Kind == session.KindToolCalled {
			return message.Result
		}
	}
	t.Fatalf("会话日志里没有 %s 的调用", tool)
	return nil
}

// 读始终允许：plan 模式下也能列目录、读文件，不弹确认。
func TestReadingIsAlwaysAllowed(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("list_files", map[string]any{}),
		modelCalls("read_file", map[string]any{"path": "资料/说明.txt"}),
		modelSays("看完了。"),
	))
	h.writeWorkspaceFile(t, "资料/说明.txt", []byte("华南环比下降"))
	created := h.prefaceWithEmployee()
	h.mustSetMode(created.ID, session.PermissionPlan)

	h.say(created.ID, contract.RoleInitiator, "看看资料")

	detail := h.readSession(created.ID)
	if got := toolNamesOf(detail.Messages, session.KindToolCalled); fmt.Sprint(got) != "[list_files read_file]" {
		t.Fatalf("plan 模式下读应被允许：%v，拒绝 %+v", got, deniedTools(detail))
	}
	if len(permissionCards(detail)) != 0 || len(deniedTools(detail)) != 0 {
		t.Fatalf("读不该出现权限确认或拒绝：%+v", detail.Messages)
	}
}

// ---------------------------------------------------------------- 三种答复

func TestThreeAnswersToAPermissionRequest(t *testing.T) {
	t.Run("拒绝：把用户拒绝交给模型，不写文件", func(t *testing.T) {
		h := newHarness(t, frontdeskScript(
			modelCalls(contract.ToolWriteFile, map[string]any{"path": "a.md", "content": "甲"}),
			modelSays("好的，那我不写了。"),
		))
		created := h.prefaceWithEmployee()
		stream := h.openStream(created.ID)
		defer stream.close()
		turn := h.sayAsync(created.ID, contract.RoleInitiator, "写一个文件")
		card := h.waitPending(created.ID)
		h.mustDecide(created.ID, card, session.DecisionDeny)
		h.waitTurn(stream, turn)

		if h.workspaceHas("a.md") {
			t.Fatal("拒绝之后文件不该出现")
		}
		// 模型拿到的工具结果是“用户拒绝”，并写明被拒绝的对象
		results := toolResultTexts(h)
		if len(results) != 1 || !strings.Contains(results[0], "用户拒绝") || !strings.Contains(results[0], "a.md") {
			t.Fatalf("交给模型的拒绝 = %v", results)
		}
		detail := h.readSession(created.ID)
		cards := permissionCards(detail)
		if len(cards) != 1 || cards[0].Decision != session.DecisionDeny || cards[0].Pending {
			t.Fatalf("确认卡 = %+v", cards)
		}
		// 拒绝也写进会话日志
		if denied := deniedTools(detail); len(denied) != 1 || denied[0].Tool != contract.ToolWriteFile || !strings.Contains(denied[0].Reason, "用户拒绝") {
			t.Fatalf("拒绝留痕 = %+v", denied)
		}
		if got := lastAssistant(detail.Messages); got != "好的，那我不写了。" {
			t.Fatalf("回复 = %q", got)
		}
	})

	t.Run("允许一次：只放行这一次，下一次再问", func(t *testing.T) {
		h := newHarness(t, frontdeskScript(
			modelCalls(contract.ToolWriteFile, map[string]any{"path": "a.md", "content": "甲"}),
			modelCalls(contract.ToolWriteFile, map[string]any{"path": "b.md", "content": "乙"}),
			modelSays("都写好了。"),
		))
		created := h.prefaceWithEmployee()
		stream := h.openStream(created.ID)
		defer stream.close()
		turn := h.sayAsync(created.ID, contract.RoleInitiator, "写两个文件")
		first := h.waitPending(created.ID)
		h.mustDecide(created.ID, first, session.DecisionAllowOnce)
		second := h.waitPending(created.ID)
		if second.RequestID == first.RequestID || second.Path != "b.md" {
			t.Fatalf("第二次应重新问：%+v", second)
		}
		h.mustDecide(created.ID, second, session.DecisionAllowOnce)
		h.waitTurn(stream, turn)
		if h.workspaceText(t, "a.md") != "甲" || h.workspaceText(t, "b.md") != "乙" {
			t.Fatal("两次都允许之后两个文件都该写出来")
		}
	})

	t.Run("本会话总是允许这一类：同类不再问，写进日志；换模式清空", func(t *testing.T) {
		h := newHarness(t, frontdeskScript(
			modelCalls(contract.ToolWriteFile, map[string]any{"path": "a.md", "content": "甲"}),
			modelCalls(contract.ToolWriteFile, map[string]any{"path": "b.md", "content": "乙"}),
			modelSays("两个都写好了。"),
			modelCalls(contract.ToolEditFile, map[string]any{"path": "a.md", "oldText": "甲", "newText": "甲甲"}),
			modelSays("a 改好了。"),
			// 换模式之后这一类不再被记住：又问一次，这次拒绝
			modelCalls(contract.ToolWriteFile, map[string]any{"path": "d.md", "content": "丁"}),
			modelSays("那 d 不写了。"),
		))
		created := h.prefaceWithEmployee()
		stream := h.openStream(created.ID)
		defer stream.close()
		turn := h.sayAsync(created.ID, contract.RoleInitiator, "写两个文件")
		first := h.waitPending(created.ID)
		h.mustDecide(created.ID, first, session.DecisionAlwaysAllow)
		h.waitTurn(stream, turn)
		if h.workspaceText(t, "a.md") != "甲" || h.workspaceText(t, "b.md") != "乙" {
			t.Fatal("总是允许之后，同类的第二个文件不再问，直接写出")
		}
		detail := h.readSession(created.ID)
		if cards := permissionCards(detail); len(cards) != 1 || cards[0].Decision != session.DecisionAlwaysAllow {
			t.Fatalf("只该问过一次：%+v", cards)
		}
		if allowed := entriesOf(detail.Messages, session.KindPermissionAllowed); len(allowed) != 1 || allowed[0].Tool != contract.ToolWriteFile || !strings.Contains(allowed[0].Reason, "总是允许") {
			t.Fatalf("总是允许放行的留痕 = %+v", allowed)
		}

		// 同一会话、同一模式：edit_file 与 write_file 同属“写入文件”这一类，也不再问
		turn = h.sayAsync(created.ID, contract.RoleInitiator, "改一下 a")
		h.waitTurn(stream, turn)
		if h.workspaceText(t, "a.md") != "甲甲" {
			t.Fatal("本会话总是允许之后，同类的修改不再问")
		}

		// 换模式（同一档也换）：清空
		h.mustSetMode(created.ID, session.PermissionAsk)
		turn = h.sayAsync(created.ID, contract.RoleInitiator, "再写一个")
		card := h.waitPending(created.ID)
		if card.Path != "d.md" {
			t.Fatalf("换模式之后应重新问：%+v", card)
		}
		h.mustDecide(created.ID, card, session.DecisionDeny)
		h.waitTurn(stream, turn)
		if h.workspaceHas("d.md") {
			t.Fatal("拒绝之后 d.md 不该出现")
		}
	})
}

func TestPermissionAnswersAreOnlyFromTheInitiatorAndOnce(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls(contract.ToolWriteFile, map[string]any{"path": "a.md", "content": "甲"}),
		modelSays("写好了。"),
	))
	created := h.prefaceWithEmployee()
	stream := h.openStream(created.ID)
	defer stream.close()
	turn := h.sayAsync(created.ID, contract.RoleInitiator, "写一个文件")
	card := h.waitPending(created.ID)

	for _, role := range []contract.Role{contract.RoleReviewer, contract.RoleOperator, ""} {
		if status, data := h.decide(created.ID, card.RequestID, role, session.DecisionAllowOnce); status != http.StatusForbidden {
			t.Fatalf("角色 %q 作答应被拒绝：%d %s", role, status, data)
		}
	}
	for _, decision := range []string{"", "maybe", "yes", session.DecisionCancelled} {
		if status, data := h.decide(created.ID, card.RequestID, contract.RoleInitiator, decision); status != http.StatusBadRequest {
			t.Fatalf("决定 %q 应被拒绝：%d %s", decision, status, data)
		}
	}
	if status, data := h.decide(created.ID, "0123456789abcdef", contract.RoleInitiator, session.DecisionAllowOnce); status != http.StatusNotFound {
		t.Fatalf("不存在的确认：%d %s", status, data)
	}
	// 上面的错误答复都没生效：卡还在等，文件没写
	if still := h.waitPending(created.ID); still.RequestID != card.RequestID || h.workspaceHas("a.md") {
		t.Fatalf("确认卡应仍在等待：%+v", still)
	}
	h.mustDecide(created.ID, card, session.DecisionAllowOnce)
	if status, data := h.decide(created.ID, card.RequestID, contract.RoleInitiator, session.DecisionDeny); status != http.StatusConflict {
		t.Fatalf("同一张卡答两次：%d %s", status, data)
	}
	h.waitTurn(stream, turn)
	if h.workspaceText(t, "a.md") != "甲" {
		t.Fatal("允许之后文件应写出")
	}
	// 答复的是别的会话的确认：找不到
	other := h.createSession()
	if status, data := h.decide(other.ID, card.RequestID, contract.RoleInitiator, session.DecisionAllowOnce); status != http.StatusNotFound {
		t.Fatalf("拿别的会话的编号作答：%d %s", status, data)
	}
}

// 等确认时人还没答复，状态点显示“等你处理”。
func TestPendingPermissionLightsTheStatusDot(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls(contract.ToolWriteFile, map[string]any{"path": "a.md", "content": "甲"}),
		modelSays("写好了。"),
	))
	created := h.prefaceWithEmployee()
	stream := h.openStream(created.ID)
	defer stream.close()
	turn := h.sayAsync(created.ID, contract.RoleInitiator, "写一个文件")
	card := h.waitPending(created.ID)

	dots := map[contract.Role]string{}
	for _, role := range []contract.Role{contract.RoleInitiator, contract.RoleReviewer} {
		status, data := h.send(http.MethodGet, "/api/workspaces", role, "", nil)
		if status != http.StatusOK {
			t.Fatalf("读工作区列表：%d %s", status, data)
		}
		for _, summary := range findSessionSummaries(t, data) {
			if summary.ID == created.ID {
				dots[role] = summary.StatusDot
			}
		}
	}
	if dots[contract.RoleInitiator] != session.StatusDotWaiting || dots[contract.RoleReviewer] == session.StatusDotWaiting {
		t.Fatalf("状态点 = %v", dots)
	}
	h.mustDecide(created.ID, card, session.DecisionAllowOnce)
	h.waitTurn(stream, turn)
}

// findSessionSummaries 工作区列表里全部会话摘要。
func findSessionSummaries(t *testing.T, data []byte) []session.Summary {
	t.Helper()
	var view struct {
		Workspaces []struct {
			Sessions []session.Summary `json:"sessions"`
		} `json:"workspaces"`
		Unassigned []session.Summary `json:"unassigned"`
	}
	if err := json.Unmarshal(data, &view); err != nil {
		t.Fatalf("工作区列表不是预期的 JSON：%v\n%s", err, data)
	}
	out := append([]session.Summary{}, view.Unassigned...)
	for _, workspace := range view.Workspaces {
		out = append(out, workspace.Sessions...)
	}
	return out
}

// ---------------------------------------------------------------- 停止

func TestStopWhileWaitingForPermission(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls(contract.ToolWriteFile, map[string]any{"path": "a.md", "content": "甲"}),
		modelSays("写好了。"),
	))
	created := h.prefaceWithEmployee()
	stream := h.openStream(created.ID)
	defer stream.close()
	turn := h.sayAsync(created.ID, contract.RoleInitiator, "写一个文件")
	card := h.waitPending(created.ID)

	if status, data := h.postJSON("/api/sessions/"+created.ID+"/stop", contract.RoleInitiator, map[string]any{}); status != http.StatusOK {
		t.Fatalf("停止：%d %s", status, data)
	}
	h.waitTurn(stream, turn)
	h.waitIdle(created.ID)

	detail := h.readSession(created.ID)
	cards := permissionCards(detail)
	if len(cards) != 1 || cards[0].Pending || cards[0].Decision != session.DecisionCancelled || len(cards[0].Choices) != 0 {
		t.Fatalf("停止之后未决的确认记为已取消：%+v", cards)
	}
	if h.workspaceHas("a.md") {
		t.Fatal("停止之后文件不该出现")
	}
	if got := cancelledTools(detail.Messages); !containsString(got, contract.ToolWriteFile) {
		t.Fatalf("没执行的写文件记“已取消”：%v", got)
	}
	// 这一轮结束了：后面的剧本（说“写好了”）没有被消费
	if got := lastAssistant(detail.Messages); got == "写好了。" {
		t.Fatalf("停止之后不该继续往下说：%q", got)
	}
	// 取消的卡不能再作答
	if status, data := h.decide(created.ID, card.RequestID, contract.RoleInitiator, session.DecisionAllowOnce); status != http.StatusConflict {
		t.Fatalf("已取消的确认再作答：%d %s", status, data)
	}
	if h.workspaceHas("a.md") {
		t.Fatal("作答已取消的确认不该写出文件")
	}
}

// ---------------------------------------------------------------- 始终适用的边界

// 不管哪个模式都不能写的地方和方法；每种都留痕、不弹确认。
func TestWritesOutsideTheAllowedAreaAreAlwaysDenied(t *testing.T) {
	h := newHarness(t, frontdeskScript(modelSays("占位")))
	// 工作区外面的一个目录，工作区里的链接指向它；另有一个指向不存在位置的链接
	outside := filepath.Join(filepath.Dir(h.workspace), "外面")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	type attempt struct {
		path, content, reason string
	}
	attempts := []attempt{
		{"../外面/越界.md", "越界", "工作区目录之外"},
		{"交付/周报.md", "冒名", "交付"},
		{"材料/备忘.md", "混进材料", "材料"},
		{".env", "S=1", "隐藏"},
		{"notes/.keep/说明.md", "藏在隐藏目录里", "隐藏"},
		{filepath.Join(h.workspace, "绝对.md"), "绝对路径", "绝对路径"},
		{"big.md", strings.Repeat("字", (1<<20)/3+1), "1 MB"},
		{"bin.dat", "a\x00b", "文本"},
	}
	checkAbsent := []string{"外面/越界.md", "交付/周报.md", "材料/备忘.md", ".env", "notes/.keep/说明.md", "绝对.md", "big.md", "bin.dat"}
	if err := os.Symlink(outside, filepath.Join(h.workspace, "链接")); err == nil {
		attempts = append(attempts, attempt{"链接/顺着链接跑.md", "跑出去", "工作区目录之外"})
		checkAbsent = append(checkAbsent, "链接/顺着链接跑.md")
	} else {
		t.Logf("这个平台建不了符号链接，跳过链接逃逸：%v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "不存在.md"), filepath.Join(h.workspace, "悬空.md")); err == nil {
		attempts = append(attempts, attempt{"悬空.md", "写进悬空链接", "链接"})
	}
	calls := []agent.ScriptedTurn{}
	for _, a := range attempts {
		calls = append(calls, modelCalls(contract.ToolWriteFile, map[string]any{"path": a.path, "content": a.content}))
	}
	h.setFrontdeskScript(frontdeskScript(append(calls, modelSays("这些地方我写不了。"))...))
	created := h.prefaceWithEmployee()
	// 最松的模式：这些仍然被拒绝，也不会弹确认
	h.mustSetMode(created.ID, session.PermissionYolo)

	h.say(created.ID, contract.RoleInitiator, "随便写")

	detail := h.readSession(created.ID)
	denied := deniedTools(detail)
	if len(denied) != len(attempts) {
		t.Fatalf("拒绝留痕 = %d 条，want %d：%+v", len(denied), len(attempts), denied)
	}
	for index, a := range attempts {
		args, _ := denied[index].Args.(map[string]any)
		if args["path"] != a.path || denied[index].Tool != contract.ToolWriteFile || !strings.Contains(denied[index].Reason, a.reason) {
			t.Fatalf("第 %d 条拒绝（%s）的留痕 = %+v", index+1, a.path, denied[index])
		}
	}
	for _, rel := range checkAbsent {
		if h.workspaceHas(rel) {
			t.Fatalf("%s 不该被写出来", rel)
		}
	}
	for _, name := range []string{"越界.md", "顺着链接跑.md", "不存在.md"} {
		if _, err := os.Stat(filepath.Join(outside, name)); !os.IsNotExist(err) {
			t.Fatalf("工作区外面不该出现 %s：%v", name, err)
		}
	}
	if len(permissionCards(detail)) != 0 {
		t.Fatal("yolo 模式下这些也不该问，直接拒绝")
	}
}

// 边界先于权限模式：ask 模式下越界的写直接拒绝，不弹确认。
func TestBoundaryDenialComesBeforeTheQuestion(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls(contract.ToolWriteFile, map[string]any{"path": "交付/周报.md", "content": "冒名"}),
		modelCalls(contract.ToolEditFile, map[string]any{"path": "../外面.md", "oldText": "甲", "newText": "乙"}),
		modelSays("不行。"),
	))
	created := h.prefaceWithEmployee()

	h.say(created.ID, contract.RoleInitiator, "写")

	detail := h.readSession(created.ID)
	if len(permissionCards(detail)) != 0 || len(deniedTools(detail)) != 2 {
		t.Fatalf("越界的写不该弹确认：%+v", detail.Messages)
	}
}

// edit_file：找不到原文、有多处原文分别报不同的错；原文件不动。
func TestEditFileReportsNoMatchAndMultipleMatches(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls(contract.ToolEditFile, map[string]any{"path": "notes.md", "oldText": "不存在的一句", "newText": "随便"}),
		modelCalls(contract.ToolEditFile, map[string]any{"path": "notes.md", "oldText": "重复的一句", "newText": "改成一句"}),
		modelCalls(contract.ToolEditFile, map[string]any{"path": "没有这个文件.md", "oldText": "甲", "newText": "乙"}),
		modelSays("这几处都改不了。"),
	))
	h.writeWorkspaceFile(t, "notes.md", []byte("第一行\n重复的一句\n重复的一句\n"))
	created := h.prefaceWithEmployee()
	h.mustSetMode(created.ID, session.PermissionAcceptEdits)

	h.say(created.ID, contract.RoleInitiator, "改几处")

	failures := []session.Message{}
	for _, message := range entriesOf(h.readSession(created.ID).Messages, session.KindToolCalled) {
		if message.Tool == contract.ToolEditFile {
			failures = append(failures, message)
		}
	}
	if len(failures) != 3 {
		t.Fatalf("edit_file 的调用 = %+v", failures)
	}
	codes := []string{}
	for _, message := range failures {
		fields, _ := message.Result.(map[string]any)
		codes = append(codes, fmt.Sprint(fields["error"]))
		if message.Reason == "" {
			t.Fatalf("失败的 edit_file 要写明原因：%+v", message)
		}
	}
	if fmt.Sprint(codes) != "[not_found ambiguous file_missing]" {
		t.Fatalf("三次失败的错误码 = %v", codes)
	}
	if !strings.Contains(failures[0].Reason, "没有找到") || !strings.Contains(failures[1].Reason, "2 处") {
		t.Fatalf("零匹配和多处匹配的说法 = %q / %q", failures[0].Reason, failures[1].Reason)
	}
	// 模型收到的也是同样的说明，并带着“是失败”的标记
	results := toolResultTexts(h)
	if len(results) != 3 || !strings.Contains(results[0], "没有找到") || !strings.Contains(results[1], "2 处") || !strings.Contains(results[2], "write_file") {
		t.Fatalf("交给模型的结果 = %v", results)
	}
	if got := h.workspaceText(t, "notes.md"); got != "第一行\n重复的一句\n重复的一句\n" {
		t.Fatalf("原文件不该被改动：%q", got)
	}
	if h.workspaceHas("没有这个文件.md") {
		t.Fatal("edit_file 不该新建文件")
	}
}

// write_file 覆盖已有文件：结果写明是修改。
func TestWriteFileOverwritesAnExistingFile(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls(contract.ToolWriteFile, map[string]any{"path": "notes.md", "content": "新内容"}),
		modelSays("覆盖好了。"),
	))
	h.writeWorkspaceFile(t, "notes.md", []byte("旧内容"))
	created := h.prefaceWithEmployee()
	h.mustSetMode(created.ID, session.PermissionAcceptEdits)

	h.say(created.ID, contract.RoleInitiator, "覆盖")

	if h.workspaceText(t, "notes.md") != "新内容" {
		t.Fatal("文件应被覆盖")
	}
	result, _ := loggedToolResult(t, h.readSession(created.ID), contract.ToolWriteFile).(map[string]any)
	if result["action"] != "modified" || result["bytes"] != float64(len("新内容")) {
		t.Fatalf("write_file 的结果 = %+v", result)
	}
}

// ---------------------------------------------------------------- Agent 的权力边界

// 任何模式都不能让 Agent 审核、重试、终止：这些命令没有对应的工具，任务不受影响。
func TestNoModeLetsTheAgentDecideTaskTransitions(t *testing.T) {
	forbidden := []string{"approve", "return", "retry", "terminate", "submit", "command", "run_command", "skip_checklist_item"}
	for _, mode := range session.PermissionModes {
		t.Run(mode, func(t *testing.T) {
			turns := []agent.ScriptedTurn{}
			for _, name := range forbidden {
				turns = append(turns, modelCalls(name, map[string]any{"comment": "直接通过"}))
			}
			h := newHarness(t, frontdeskScript(append(turns, modelSays("这些我做不了。"))...), withScriptedModel())
			opened, before := pausedSession(h)
			eventsBefore := h.eventsJSON(before.ID)
			h.mustSetMode(opened.ID, mode)

			h.say(opened.ID, contract.RoleInitiator, "直接给我通过、重试、终止")

			after := h.snapshot(before.ID)
			if after.State != before.State || after.LastSeq != before.LastSeq || h.eventsJSON(before.ID) != eventsBefore {
				t.Fatalf("任务被改动：%s → %s", before.State, after.State)
			}
			refused := toolNamesOf(h.readSession(opened.ID).Messages, session.KindToolDenied)
			if fmt.Sprint(refused) != fmt.Sprint(forbidden) {
				t.Fatalf("越权调用的拒绝留痕 = %v", refused)
			}
			names := toolNames(frontdeskRequests(h)[0].Request.Tools)
			for _, name := range forbidden {
				if containsString(names, name) {
					t.Fatalf("前台 Agent 不该有 %s：%v", name, names)
				}
			}
			for _, want := range []string{contract.ToolWriteFile, contract.ToolEditFile, "list_files", "read_file", "query_task"} {
				if !containsString(names, want) {
					t.Fatalf("前台 Agent 应开放 %s：%v", want, names)
				}
			}
		})
	}
}

// 系统提示词写明当前模式：plan 下只读、被拒绝后换办法。
func TestSystemPromptStatesThePermissionMode(t *testing.T) {
	for _, mode := range session.PermissionModes {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, frontdeskScript(modelSays("好的。")))
			created := h.prefaceWithEmployee()
			h.mustSetMode(created.ID, mode)

			h.say(created.ID, contract.RoleInitiator, "看看能干点什么")

			requests := frontdeskRequests(h)
			first := requests[len(requests)-1].Request.Messages[0]
			if first.Role != "system" {
				t.Fatalf("第一条不是系统提示词：%+v", first)
			}
			prompt := first.Content[0].Text
			if !strings.Contains(prompt, session.PermissionModeLabels[mode]+"（"+mode+"）") {
				t.Fatalf("系统提示词没写明 %s 模式：%s", mode, prompt)
			}
			if mode == session.PermissionPlan && !strings.Contains(prompt, "只读") {
				t.Fatalf("plan 模式要写明只读：%s", prompt)
			}
			if !strings.Contains(prompt, "换个办法") || !strings.Contains(prompt, "1 MB") {
				t.Fatalf("系统提示词要写明被拒绝后换办法和写入边界：%s", prompt)
			}
		})
	}
}
