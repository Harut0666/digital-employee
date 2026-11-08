// 提交前检查：全部从 HTTP 接口验证（Spec 0005“提交前检查”）。
package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"digital-employee/internal/contract"
)

// preflightReply 提交前检查不通过时的响应。
type preflightReply struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error"`
	Message  string `json:"message"`
	Settings struct {
		Section string `json:"section"`
		Label   string `json:"label"`
	} `json:"settings"`
}

func (h *harness) preflight() preflightReply {
	h.t.Helper()
	status, data := h.send(http.MethodGet, "/api/preflight", "", "", nil)
	if status != http.StatusOK {
		h.t.Fatalf("提交前检查接口：%d %s", status, data)
	}
	return decode[preflightReply](h.t, data)
}

// submitRaw 提交草稿，返回未经解释的响应。
func (h *harness) submitRaw(taskID string, role contract.Role) (int, []byte) {
	h.t.Helper()
	return h.send(http.MethodPost, "/api/tasks/"+taskID+"/commands", role, "application/json",
		strings.NewReader(`{"type":"submit"}`))
}

func eventCount(t *testing.T, h *harness, taskID string) int {
	t.Helper()
	events := []json.RawMessage{}
	if err := json.Unmarshal([]byte(h.eventsJSON(taskID)), &events); err != nil {
		t.Fatal(err)
	}
	return len(events)
}

func keylessConnection() contract.ModelConnection {
	return contract.ModelConnection{Name: "fake", BaseURL: "http://127.0.0.1:1", Models: []string{"fake-model"}}
}

func expectBlockedBySettings(t *testing.T, reply preflightReply) {
	t.Helper()
	if reply.OK || reply.Error != contract.ErrModelNotConfigured {
		t.Fatalf("应该被提交前检查拦下：%+v", reply)
	}
	if !strings.Contains(reply.Message, "Key") || !strings.Contains(reply.Message, "fake") {
		t.Fatalf("说明应指出哪个连接缺 Key：%q", reply.Message)
	}
	if reply.Settings.Section != "models" || reply.Settings.Label == "" {
		t.Fatalf("应带设置入口：%+v", reply.Settings)
	}
}

func TestSubmitWithoutKeyIsBlockedAndWritesNothing(t *testing.T) {
	// 剧本模式下会话模型记在 scripted 连接上（检查通过），这里换成没配 Key 的连接，只看任务检查
	h := newHarness(t, nil, withScriptedModel(), withModelConnection(keylessConnection()))
	expectBlockedBySettings(t, h.preflight())

	task := h.createTask(a01Files(t))
	before := eventCount(t, h, task.ID)
	// T39：任务的会话模型（设置里最近选的模型）在这个没配 Key 的连接上
	h.setSessionModelOverride(contract.ModelRef{Connection: "fake", Model: "fake-model"})

	status, data := h.submitRaw(task.ID, initiator)
	if status != http.StatusPreconditionFailed {
		t.Fatalf("状态码 = %d：%s", status, data)
	}
	expectBlockedBySettings(t, decode[preflightReply](t, data))

	expectState(t, h.snapshot(task.ID), contract.StateDraft)
	if after := eventCount(t, h, task.ID); after != before {
		t.Fatalf("被拦下的提交不应写事件：%d → %d", before, after)
	}
}

func TestSessionSubmitWithoutKeyIsBlockedBeforeATaskExists(t *testing.T) {
	// 任务用按剧本跑的引擎；提交前检查看设置里的模型连接
	h := newHarness(t, nil, withScriptedModel())
	view := h.createSession()
	h.confirmed(view.ID, harnessEmployee)
	if status, data := h.uploadTo(view.ID, a01Files(t)); status != http.StatusOK {
		t.Fatalf("上传失败：%d %s", status, data)
	}
	// T39：会话模型在一个没配 Key 的连接上（之前配过 Key 选了它，后来 Key 被清掉了）
	h.useConnections(contract.ModelConnection{Name: "fake", BaseURL: "http://127.0.0.1:1", Models: []string{"fake-model"}})
	h.setSessionModelOverride(contract.ModelRef{Connection: "fake", Model: "fake-model"})

	status, data := h.trySubmit(view.ID, initiator, map[string]any{
		"period": map[string]any{"year": 2026, "week": 44},
	})
	if status != http.StatusPreconditionFailed {
		t.Fatalf("状态码 = %d：%s", status, data)
	}
	expectBlockedBySettings(t, decode[preflightReply](t, data))
	if count := h.taskCount(); count != 0 {
		t.Fatalf("被拦下的提交不应创建任务：%d", count)
	}
}

func TestSubmitPassesOnceKeyIsSaved(t *testing.T) {
	connection := keylessConnection()
	h := newHarness(t, nil, withScriptedModel(), withModelConnection(connection))
	task := h.createTask(a01Files(t))

	status, data := h.send(http.MethodPut, "/api/settings/connections/"+connection.Name, operator, "application/json",
		strings.NewReader(`{"baseUrl":"http://127.0.0.1:1","apiKey":"sk-saved","models":["fake-model"]}`))
	if status != http.StatusOK {
		t.Fatalf("保存连接失败：%d %s", status, data)
	}

	if reply := h.preflight(); !reply.OK {
		t.Fatalf("保存 Key 后应通过：%+v", reply)
	}
	if status, data := h.submitRaw(task.ID, initiator); status != http.StatusOK {
		t.Fatalf("保存 Key 后提交应被接受：%d %s", status, data)
	}
	h.settled(task.ID)
}

func TestEnvironmentKeyPassesPreflight(t *testing.T) {
	h := newHarness(t, nil,
		// 地址用本机假端口（不会真连出去）：DEEPSEEK_API_KEY 覆盖 deepseek 连接文件里的空 Key
		withModelConnection(contract.ModelConnection{Name: "deepseek", BaseURL: "http://127.0.0.1:1", Models: []string{"fake-model"}}),
		withEnv(map[string]string{"DEEPSEEK_API_KEY": "sk-from-env"}))
	// T39：会话模型在这个连接上
	h.setSessionModelOverride(contract.ModelRef{Connection: "deepseek", Model: "fake-model"})
	if reply := h.preflight(); !reply.OK {
		t.Fatalf("环境变量里的 Key 也算可用：%+v", reply)
	}
}

func TestScriptedModeAlwaysPassesPreflight(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	if reply := h.preflight(); !reply.OK {
		t.Fatalf("剧本模式应视为通过：%+v", reply)
	}
	task := h.createTask(a01Files(t))
	expectState(t, h.command(task.ID, initiator, cmd(contract.CmdSubmit)), contract.StateAwaitingReview)
}

// 这条提交本来就不合法：该得到的是越权提示，不是让审核人去配 Key。
func TestRejectedSubmitIsNotReportedAsPreflight(t *testing.T) {
	h := newHarness(t, nil, withModelConnection(keylessConnection()))
	task := h.createTask(a01Files(t))

	status, data := h.submitRaw(task.ID, reviewer)
	if status != http.StatusForbidden {
		t.Fatalf("审核人提交应是越权：%d %s", status, data)
	}
	if reply := decode[preflightReply](t, data); reply.Error != contract.ErrForbiddenRole {
		t.Fatalf("错误码 = %q", reply.Error)
	}
}
