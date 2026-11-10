// tl2：shell（命令行）工具的权限矩阵、确认卡、运行与输出、风险提示，以及改变状态的接口要求浏览器同源标记。
// shell 没有沙箱（ADR-0007 增补）：这里测的是缓解措施和权限矩阵，不是“安全”。
package server_test

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

func shellCall(command string, extra ...map[string]any) map[string]any {
	arguments := map[string]any{"description": "测试用的一句话说明", "command": command}
	for _, more := range extra {
		for key, value := range more {
			arguments[key] = value
		}
	}
	return arguments
}

func shellResult(t *testing.T, detail session.Detail) map[string]any {
	t.Helper()
	calls := toolMessages(detail, contract.ToolShell)
	if len(calls) == 0 {
		t.Fatalf("会话日志里没有 shell 的调用：%+v", detail.Messages)
	}
	return resultMap(t, calls[len(calls)-1])
}

// 矩阵：read_only 拒绝；workspace 每次问；full 允许（并留痕，含完整命令）。
func TestShellFollowsThePermissionMatrix(t *testing.T) {
	cases := []struct{ mode, want string }{
		{session.PermissionReadOnly, "deny"},
		{session.PermissionWorkspace, "ask"},
		{session.PermissionFull, "allow"},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			command := "echo matrix-ok"
			h := newHarness(t, frontdeskScript(modelCalls(contract.ToolShell, shellCall(command)), modelSays("跑完了。")))
			created := h.prefaceWithEmployee()
			if tc.mode != session.PermissionWorkspace {
				h.mustSetMode(created.ID, tc.mode)
			}
			stream := h.openStream(created.ID)
			defer stream.close()
			turn := h.sayAsync(created.ID, contract.RoleInitiator, "跑个命令")

			if tc.want == "ask" {
				card := h.waitPending(created.ID)
				if card.Action != session.ActionShell || card.Tool != contract.ToolShell || card.Command != command || card.Detail != "测试用的一句话说明" {
					t.Fatalf("确认卡必须原样显示命令和 description：%+v", card)
				}
				if card.Workdir == "" || !strings.EqualFold(strings.ReplaceAll(card.Workdir, `\`, "/"), strings.ReplaceAll(h.workspace, `\`, "/")) {
					t.Fatalf("确认卡要写工作目录：%q，工作区 %q", card.Workdir, h.workspace)
				}
				// 只有“允许一次”和“拒绝”，没有“总是允许”
				decisions := []string{}
				for _, choice := range card.Choices {
					decisions = append(decisions, choice.Decision)
				}
				if fmt.Sprint(decisions) != fmt.Sprint([]string{session.DecisionAllowOnce, session.DecisionDeny}) {
					t.Fatalf("shell 确认卡的按钮 = %v", decisions)
				}
				// 硬塞一个 always_allow 也不行
				if status, data := h.decide(created.ID, card.RequestID, contract.RoleInitiator, session.DecisionAlwaysAllow); status != http.StatusBadRequest {
					t.Fatalf("shell 的 always_allow 应被拒绝：%d %s", status, data)
				}
				h.mustDecide(created.ID, card, session.DecisionAllowOnce)
			}
			h.waitTurn(stream, turn)
			detail := h.readSession(created.ID)

			switch tc.want {
			case "deny":
				denied := deniedTools(detail)
				if len(denied) != 1 || denied[0].Tool != contract.ToolShell || !strings.Contains(denied[0].Reason, "仅可查看模式") {
					t.Fatalf("read_only 下应拒绝：%+v", denied)
				}
				if len(toolMessages(detail, contract.ToolShell)) != 0 {
					t.Fatal("read_only 下命令不该运行")
				}
			default:
				out := fmt.Sprint(shellResult(t, detail)["output"])
				if !strings.Contains(out, "matrix-ok") {
					t.Fatalf("命令输出 = %q", out)
				}
			}
			allowed := entriesOf(detail.Messages, session.KindPermissionAllowed)
			if tc.want == "allow" {
				if len(allowed) != 1 || allowed[0].Tool != contract.ToolShell {
					t.Fatalf("full 自动放行要留痕：%+v", allowed)
				}
				args, _ := allowed[0].Args.(map[string]any)
				if args["command"] != command {
					t.Fatalf("留痕里要有完整命令：%+v", allowed[0].Args)
				}
			} else if len(allowed) != 0 {
				t.Fatalf("不是自动放行：%+v", allowed)
			}
			if len(permissionCards(detail)) != map[string]int{"ask": 1}[tc.want] {
				t.Fatalf("确认卡数量 = %d", len(permissionCards(detail)))
			}
		})
	}
}

func TestShellAskedAndDeniedByInitiator(t *testing.T) {
	h := newHarness(t, frontdeskScript(modelCalls(contract.ToolShell, shellCall("echo should-not-run")), modelSays("好吧。")))
	created := h.prefaceWithEmployee()
	stream := h.openStream(created.ID)
	defer stream.close()
	turn := h.sayAsync(created.ID, contract.RoleInitiator, "跑")
	card := h.waitPending(created.ID)

	h.mustDecide(created.ID, card, session.DecisionDeny)
	h.waitTurn(stream, turn)

	detail := h.readSession(created.ID)
	if len(toolMessages(detail, contract.ToolShell)) != 0 {
		t.Fatal("拒绝后命令不该运行")
	}
	if got := deniedTools(detail); len(got) != 1 || got[0].Decision != session.DecisionDeny || !strings.Contains(got[0].Reason, "发起人拒绝") {
		t.Fatalf("拒绝留痕 = %+v", got)
	}
}

// 说话人不是发起人时任何模式都拒绝（含 full），也不弹确认卡。
func TestShellIsRefusedForNonInitiatorsInEveryMode(t *testing.T) {
	for _, mode := range session.PermissionModes {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, frontdeskScript(modelCalls(contract.ToolShell, shellCall("echo no")), modelSays("不行。")))
			created := h.prefaceWithEmployee()
			h.mustSetMode(created.ID, mode)

			h.say(created.ID, contract.RoleReviewer, "审核人让你跑命令")

			detail := h.readSession(created.ID)
			if got := deniedTools(detail); len(got) != 1 || !strings.Contains(got[0].Reason, "审核人") {
				t.Fatalf("应拒绝：%+v", got)
			}
			if len(permissionCards(detail)) != 0 || len(toolMessages(detail, contract.ToolShell)) != 0 {
				t.Fatal("不该弹确认也不该运行")
			}
		})
	}
}

func TestShellOutputFormat(t *testing.T) {
	stderrCmd := "echo to-out; echo to-err >&2; exit 3"
	if runtime.GOOS == "windows" {
		stderrCmd = "echo to-out; [Console]::Error.WriteLine('to-err'); exit 3"
	}
	h := newHarness(t, frontdeskScript(
		modelCalls(contract.ToolShell, shellCall("echo plain")),
		modelCalls(contract.ToolShell, shellCall(stderrCmd)),
		modelSays("好。"),
	))
	created := h.prefaceWithEmployee()
	h.mustSetMode(created.ID, session.PermissionFull)

	h.say(created.ID, contract.RoleInitiator, "跑")

	calls := toolMessages(h.readSession(created.ID), contract.ToolShell)
	if len(calls) != 2 {
		t.Fatalf("调用 %d 次", len(calls))
	}
	first := resultMap(t, calls[0])
	if strings.TrimSpace(fmt.Sprint(first["output"])) != "plain" || first["exitCode"] != float64(0) {
		t.Fatalf("退出码 0 不加标记：%+v", first)
	}
	second := resultMap(t, calls[1])
	out := fmt.Sprint(second["output"])
	if !strings.Contains(out, "to-out") || !strings.Contains(out, "[stderr]") || !strings.Contains(out, "to-err") || !strings.HasSuffix(strings.TrimSpace(out), "[exit code: 3]") {
		t.Fatalf("stdout + [stderr] + [exit code: N]：%q", out)
	}
	// 非零退出码不是工具错误
	if calls[1].Reason != "" || second["exitCode"] != float64(3) {
		t.Fatalf("非零退出码不该算工具错误：%+v", calls[1])
	}
}

func TestShellTimeoutKillsTheCommandAndSaysSo(t *testing.T) {
	h := newHarness(t, frontdeskScript(modelCalls(contract.ToolShell, shellCall("echo before; sleep 30", map[string]any{"timeout_ms": 2500})), modelSays("超时了。")))
	created := h.prefaceWithEmployee()
	h.mustSetMode(created.ID, session.PermissionFull)

	started := time.Now()
	h.say(created.ID, contract.RoleInitiator, "跑个慢的")

	if time.Since(started) > 20*time.Second {
		t.Fatalf("超时没有生效，花了 %s", time.Since(started))
	}
	result := shellResult(t, h.readSession(created.ID))
	if result["timedOut"] != true || !strings.Contains(fmt.Sprint(result["output"]), "[timed out after 2500ms]") {
		t.Fatalf("超时结果 = %+v", result)
	}
}

func TestShellStopButtonKillsTheCommand(t *testing.T) {
	h := newHarness(t, frontdeskScript(modelCalls(contract.ToolShell, shellCall("sleep 60")), modelSays("不该说到这。")))
	created := h.prefaceWithEmployee()
	h.mustSetMode(created.ID, session.PermissionFull)
	stream := h.openStream(created.ID)
	defer stream.close()
	turn := h.sayAsync(created.ID, contract.RoleInitiator, "跑个很慢的")
	time.Sleep(1500 * time.Millisecond)

	started := time.Now()
	if status, data := h.postJSON("/api/sessions/"+created.ID+"/stop", contract.RoleInitiator, map[string]any{}); status != http.StatusOK {
		t.Fatalf("停止：%d %s", status, data)
	}
	h.waitTurn(stream, turn)
	if time.Since(started) > 15*time.Second {
		t.Fatalf("停止后命令没有被杀：%s", time.Since(started))
	}
	if got := lastAssistant(h.readSession(created.ID).Messages); got == "不该说到这。" {
		t.Fatal("停止之后不该继续往下说")
	}
}

func TestShellWorkdirMustStayInsideTheWorkspace(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls(contract.ToolShell, shellCall("echo hi", map[string]any{"workdir": "../"})),
		modelCalls(contract.ToolShell, shellCall("echo hi", map[string]any{"workdir": "不存在的夹"})),
		modelCalls(contract.ToolShell, shellCall("echo hi", map[string]any{"workdir": "子目录"})),
		modelCalls(contract.ToolShell, map[string]any{"command": "echo 没有说明"}),
		modelSays("好。"),
	), withAppMode())
	h.writeWorkspaceFile(t, "子目录/占位.txt", []byte("x"))
	created := h.prefaceWithEmployee()
	h.mustSetMode(created.ID, session.PermissionFull)

	h.say(created.ID, contract.RoleInitiator, "跑")

	detail := h.readSession(created.ID)
	denied := deniedTools(detail)
	if len(denied) != 3 || !strings.Contains(denied[0].Reason, "工作区") || !strings.Contains(denied[1].Reason, "不是工作区里存在的文件夹") || !strings.Contains(denied[2].Reason, "description") {
		t.Fatalf("拒绝留痕 = %+v", denied)
	}
	if out := strings.TrimSpace(fmt.Sprint(shellResult(t, detail)["output"])); out != "hi" {
		t.Fatalf("workdir 在工作区内应能运行：%q", out)
	}
}

func TestShellDoesNotLeakSecretsInTheEnvironment(t *testing.T) {
	t.Setenv("DE_SHELL_PROBE", "leak1")
	t.Setenv("MY_PROVIDER_API_KEY", "leak2")
	t.Setenv("GITHUB_TOKEN", "leak3")
	t.Setenv("SOME_SECRET_VALUE", "leak4")
	t.Setenv("DE_SHELL_KEEP_ME_NOT", "leak5")
	t.Setenv("PLAIN_VISIBLE_VAR", "visible")
	command := `echo "[$DE_SHELL_PROBE][$MY_PROVIDER_API_KEY][$GITHUB_TOKEN][$SOME_SECRET_VALUE][$DE_SHELL_KEEP_ME_NOT][$PLAIN_VISIBLE_VAR]"`
	if runtime.GOOS == "windows" {
		command = `echo "[$env:DE_SHELL_PROBE][$env:MY_PROVIDER_API_KEY][$env:GITHUB_TOKEN][$env:SOME_SECRET_VALUE][$env:DE_SHELL_KEEP_ME_NOT][$env:PLAIN_VISIBLE_VAR]"`
	}
	h := newHarness(t, frontdeskScript(modelCalls(contract.ToolShell, shellCall(command)), modelSays("好。")), withSerial())
	created := h.prefaceWithEmployee()
	h.mustSetMode(created.ID, session.PermissionFull)

	h.say(created.ID, contract.RoleInitiator, "看环境变量")

	if out := strings.TrimSpace(fmt.Sprint(shellResult(t, h.readSession(created.ID))["output"])); out != "[][][][][][visible]" {
		t.Fatalf("环境变量没有去干净：%q", out)
	}
}

func TestShellKeepsOnlyTheTailOfHugeOutput(t *testing.T) {
	command := `1..9000 | ForEach-Object { "line-$_-padpadpadpadpad" }`
	if runtime.GOOS != "windows" {
		command = `for i in $(seq 1 9000); do echo "line-$i-padpadpadpadpad"; done`
	}
	h := newHarness(t, frontdeskScript(modelCalls(contract.ToolShell, shellCall(command)), modelSays("好。")))
	created := h.prefaceWithEmployee()
	h.mustSetMode(created.ID, session.PermissionFull)

	h.say(created.ID, contract.RoleInitiator, "大量输出")

	result := shellResult(t, h.readSession(created.ID))
	out := fmt.Sprint(result["output"])
	if result["truncated"] != true || !strings.Contains(out, "stdout 已截断") || !strings.Contains(out, "line-9000-") || strings.Contains(out, "line-1-pad") {
		t.Fatalf("应保留尾部并标已截断：truncated=%v 长度 %d", result["truncated"], len(out))
	}
	if len(out) > 64000+300 {
		t.Fatalf("每路最多保留 64000 字节：%d", len(out))
	}
}

func TestShellLogKeepsOnlyTheFirst4KBOfTheCommand(t *testing.T) {
	long := "echo " + strings.Repeat("a", 6000)
	h := newHarness(t, frontdeskScript(modelCalls(contract.ToolShell, shellCall(long)), modelSays("好。")))
	created := h.prefaceWithEmployee()
	h.mustSetMode(created.ID, session.PermissionFull)

	h.say(created.ID, contract.RoleInitiator, "长命令")

	detail := h.readSession(created.ID)
	for _, message := range append(toolMessages(detail, contract.ToolShell), entriesOf(detail.Messages, session.KindPermissionAllowed)...) {
		args, _ := message.Args.(map[string]any)
		command := fmt.Sprint(args["command"])
		if len(command) > 4096+200 || !strings.Contains(command, "日志里只留前 4096 字节") {
			t.Fatalf("日志里的命令应截断到 4KB：%d 字节", len(command))
		}
	}
}

// ---------------------------------------------------------------- 缓解措施 (a)(b)

func TestShellLoopbackCommandsAskEvenInFull(t *testing.T) {
	// 命令里出现本机服务地址：full 也不放行，弹确认卡并加警告；人拒绝后命令不运行
	command := `curl -H "x-role: reviewer" -X POST http://127.0.0.1:8787/api/tasks/x/commands`
	h := newHarness(t, frontdeskScript(modelCalls(contract.ToolShell, shellCall(command)), modelSays("好。")))
	created := h.prefaceWithEmployee()
	h.mustSetMode(created.ID, session.PermissionFull)
	stream := h.openStream(created.ID)
	defer stream.close()
	turn := h.sayAsync(created.ID, contract.RoleInitiator, "审核通过")

	card := h.waitPending(created.ID)
	if card.Command != command || len(card.Warnings) == 0 || !strings.Contains(strings.Join(card.Warnings, ""), "本机服务地址") {
		t.Fatalf("full 下遇到本机服务地址应弹带警告的确认卡：%+v", card)
	}
	h.mustDecide(created.ID, card, session.DecisionDeny)
	h.waitTurn(stream, turn)

	detail := h.readSession(created.ID)
	if len(toolMessages(detail, contract.ToolShell)) != 0 || len(entriesOf(detail.Messages, session.KindPermissionAllowed)) != 0 {
		t.Fatal("拒绝后命令不该运行，也不该有自动放行的留痕")
	}
}

func TestShellWarnsAboutOutOfWorkspaceAndProtectedFolders(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls(contract.ToolShell, shellCall(`Copy-Item 周报.docx 交付\周报.docx`)),
		modelCalls(contract.ToolShell, shellCall(`Get-Content C:\Users\someone\secret.txt`)),
		modelCalls(contract.ToolShell, shellCall("echo 无风险")),
		modelSays("好。"),
	), withAppMode())
	created := h.prefaceWithEmployee()
	stream := h.openStream(created.ID)
	defer stream.close()
	turn := h.sayAsync(created.ID, contract.RoleInitiator, "跑")

	warnings := [][]string{}
	for index := 0; index < 3; index++ {
		card := h.waitPending(created.ID)
		warnings = append(warnings, card.Warnings)
		h.mustDecide(created.ID, card, session.DecisionDeny)
		// 等这张卡变成已答复，下一张才会出现
		deadline := time.Now().Add(stopTimeout)
		for time.Now().Before(deadline) {
			pending := false
			for _, message := range h.readSession(created.ID).Messages {
				if message.Card != nil && message.Card.Type == session.CardPermission && message.Card.Pending && message.Card.RequestID == card.RequestID {
					pending = true
				}
			}
			if !pending {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	h.waitTurn(stream, turn)
	if !strings.Contains(strings.Join(warnings[0], ""), "交付") {
		t.Fatalf("提到“交付”应有警告：%v", warnings[0])
	}
	if !strings.Contains(strings.Join(warnings[1], ""), "工作区之外") {
		t.Fatalf("工作区外的绝对路径应有警告：%v", warnings[1])
	}
	if len(warnings[2]) != 0 {
		t.Fatalf("无风险的命令不该有警告：%v", warnings[2])
	}
}

// ---------------------------------------------------------------- 缓解措施 (c)：状态接口要求浏览器同源标记

func (h *harness) sendRaw(method, path string, headers map[string]string, body string) (int, string) {
	h.t.Helper()
	request, err := http.NewRequest(method, h.baseURL+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close()
	data := make([]byte, 4096)
	count, _ := response.Body.Read(data)
	return response.StatusCode, string(data[:count])
}

func TestStateChangingEndpointsNeedABrowserPageOrigin(t *testing.T) {
	h := newHarness(t, frontdeskScript(modelSays("好。")))
	created := h.createSession()
	host := strings.TrimPrefix(h.baseURL, "http://")
	endpoints := []struct{ method, path, body string }{
		{http.MethodPost, "/api/tasks/t-none/commands", `{"command":{"type":"approve"}}`},
		{http.MethodPost, "/api/sessions/" + created.ID + "/permissions/0123456789abcdef", `{"decision":"allow_once"}`},
		{http.MethodPost, "/api/sessions/" + created.ID + "/questions/0123456789abcdef", `{"answers":[]}`},
		{http.MethodPost, "/api/sessions/" + created.ID + "/employee-forms/0123456789abcdef", `{"decision":"reject"}`},
		{http.MethodPut, "/api/sessions/" + created.ID + "/permission-mode", `{"mode":"full"}`},
	}
	for _, endpoint := range endpoints {
		bare := map[string]string{"x-role": "reviewer", "Content-Type": "application/json"}
		// 命令行 curl：只带 x-role，被拒绝
		if status, body := h.sendRaw(endpoint.method, endpoint.path, bare, endpoint.body); status != http.StatusForbidden || !strings.Contains(body, "browser_origin_required") {
			t.Fatalf("%s %s 不带同源标记应 403 browser_origin_required：%d %s", endpoint.method, endpoint.path, status, body)
		}
		// 别的站点的 Origin 也不行
		foreign := map[string]string{"x-role": "reviewer", "Content-Type": "application/json", "Origin": "https://evil.example"}
		if status, body := h.sendRaw(endpoint.method, endpoint.path, foreign, endpoint.body); status != http.StatusForbidden || !strings.Contains(body, "browser_origin_required") {
			t.Fatalf("%s %s 外站 Origin 应 403：%d %s", endpoint.method, endpoint.path, status, body)
		}
		// 页面发出的（Sec-Fetch-Site 或与 Host 相同的 Origin）通过这一关，之后按各自的规则处理（不再是 browser_origin_required）
		for _, ok := range []map[string]string{
			{"x-role": "reviewer", "Content-Type": "application/json", "Sec-Fetch-Site": "same-origin"},
			{"x-role": "reviewer", "Content-Type": "application/json", "Origin": "http://" + host},
		} {
			if _, body := h.sendRaw(endpoint.method, endpoint.path, ok, endpoint.body); strings.Contains(body, "browser_origin_required") {
				t.Fatalf("%s %s 带页面标记不该被来源检查拦下：%s", endpoint.method, endpoint.path, body)
			}
		}
	}
	// 读取类接口不受影响
	if status, _ := h.sendRaw(http.MethodGet, "/api/sessions/"+created.ID, nil, ""); status != http.StatusOK {
		t.Fatalf("GET 不该要求来源标记：%d", status)
	}
}
