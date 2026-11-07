// 真实模型路径：断言发给模型服务的 HTTP 请求内容。剧本模型不走这条路，看不出这里的问题。
package server_test

import (
	"encoding/json"
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
