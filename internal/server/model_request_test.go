// 真实模型路径：断言发给模型服务的 HTTP 请求内容。剧本模型不走这条路，看不出这里的问题。
package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"digital-employee/internal/contract"
)

type chatRequestBody struct {
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

// firstTextLine 配置文件里第一行正文，用来确认它进了请求。
func firstTextLine(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	t.Fatalf("%s 是空文件", path)
	return ""
}

// 人在设置里把连接的模型换掉、在会话里选了它以后，任务要用新模型；“测试连接”能通过的模型，任务也必须能用。
func TestRunUsesTheModelTheConnectionNowLists(t *testing.T) {
	service := newFakeModelService(t, caseScript(t, "A-01"), 0, 0)
	h := newHarness(t, nil, withModelConnection(connectionTo(service.server.URL, 0)))

	payload := fmt.Sprintf(`{"baseUrl":%q,"models":["model-v2"]}`, service.server.URL)
	status, data := h.send(http.MethodPut, "/api/settings/connections/fake", operator, "application/json", strings.NewReader(payload))
	if status != http.StatusOK {
		t.Fatalf("保存连接失败：%d %s", status, data)
	}
	// T39：任务从会话发起，会话模型就是连接现在列出的 model-v2
	session := h.openedSession()
	h.setModel(session.ID, "fake", "model-v2")
	reply := h.submit(session.ID, week44Form)
	expectState(t, h.settled(reply.Session.TaskID), contract.StateAwaitingReview)

	if len(service.bodies) == 0 {
		t.Fatal("任务没有调用模型")
	}
	for index, body := range service.bodies {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatalf("第 %d 个请求不是合法 JSON：%v", index, err)
		}
		if request.Model != "model-v2" {
			t.Fatalf("第 %d 个请求用的模型是 %q，应该是连接里现在列出的 model-v2", index, request.Model)
		}
	}
}

func TestJudgmentRequestCarriesSkillAndStepPrompt(t *testing.T) {
	service := newFakeModelService(t, caseScript(t, "A-01"), 0, 0)
	h := newHarness(t, nil, withModelConnection(connectionTo(service.server.URL, 0)))
	task := h.createTask(a01Files(t))
	expectState(t, h.command(task.ID, initiator, cmd(contract.CmdSubmit)), contract.StateAwaitingReview)

	skill := firstTextLine(t, repoPath("employees", harnessEmployee, "SKILL.md"))
	for index, prompt := range []string{"match_causes.md", "write_report.md"} {
		var request chatRequestBody
		if err := json.Unmarshal(service.bodies[index], &request); err != nil {
			t.Fatalf("第 %d 个请求不是合法 JSON：%v", index, err)
		}
		if len(request.Messages) == 0 || request.Messages[0].Role != "system" {
			t.Fatalf("%s 的请求没有系统提示词：%s", prompt, service.bodies[index])
		}
		system := request.Messages[0].Content
		if !strings.Contains(system, skill) {
			t.Fatalf("%s 的系统提示词缺少岗位要求 %q", prompt, skill)
		}
		step := firstTextLine(t, repoPath("employees", harnessEmployee, "prompts", prompt))
		if !strings.Contains(system, step) {
			t.Fatalf("%s 的系统提示词缺少步骤提示词 %q", prompt, step)
		}
	}
}
