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
	h := newHarness(t, nil, withModelConnection(keylessConnection()))
	expectBlockedBySettings(t, h.preflight())

	task := h.createTask(a01Files(t))
	before := eventCount(t, h, task.ID)

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

func TestSubmitPassesOnceKeyIsSaved(t *testing.T) {
	connection := keylessConnection()
	h := newHarness(t, nil, withModelConnection(connection))
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
		withModelConnection(contract.ModelConnection{Name: "deepseek", BaseURL: "http://127.0.0.1:1", Models: []string{"fake-model"}}),
		withEnv(map[string]string{"DEEPSEEK_API_KEY": "sk-from-env"}))
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
