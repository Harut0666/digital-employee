// shell：前台 Agent 的命令行工具。
//
// ┌─────────────────────────────────────────────────────────────────────────────────────────┐
// │ 诚实说明：shell 在 Windows 上没有沙箱。                                                │
// │ 命令以本程序的用户身份运行：能读写工作区之外的任何地方，能改“交付”“材料”文件夹，        │
// │ 还能 curl 本机服务的 HTTP 接口、自带 x-role: reviewer 请求头去“审核通过”。              │
// │ 也就是说，shell 打破了 ADR-0007 里“写操作只限工作区、审核只有人能做”的硬边界。          │
// │ 下面做的是缓解，不是防护：                                                              │
// │  (a) 命令里出现本服务的监听地址/端口或 localhost/127.0.0.1 时，确认卡加醒目警告，        │
// │      完全权限（full）模式下也改为询问；                                                 │
// │  (b) 命令明显指向“交付”“材料”或工作区外的绝对路径时，确认卡加警告（尽力而为的提示，      │
// │      靠字符串识别，换个写法就能绕过，不是安全边界）；                                    │
// │  (c) 服务端对会改变审核/退回/终止状态的接口额外要求浏览器的同源标记（internal/server/origin.go），│
// │      命令行 curl 默认不带——只是提高门槛，伪造这个头很容易。                              │
// │ 真正的边界只有一条：权限模式。read_only 拒绝、workspace 每次问、full 才放行；            │
// │ 说话人不是发起人时任何模式都拒绝。把 shell 交给谁，就是把这台机器上该用户的权限交给谁。    │
// └─────────────────────────────────────────────────────────────────────────────────────────┘
//
// 参考 DeepSeek Harness 的 pwsh 工具的行为，不复制代码。每次调用新进程；工作目录固定在工作区（或工作区内的 workdir）；
// 超时则整个进程树被杀；本期不做后台任务。
package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

const (
	// shellDefaultTimeoutMS 默认超时；shellMaxTimeoutMS 上限。
	shellDefaultTimeoutMS = 120000
	shellMaxTimeoutMS     = 600000
	// shellOutputKeep 每路输出最多保留多少字节（超出保留尾部）。
	shellOutputKeep = 64000
	// shellLogCommandBytes 会话日志里的命令最多留多少字节。
	shellLogCommandBytes = 4096
)

// ActionShell 运行命令行。
const ActionShell = "run_command"

// shellRequest 一次 shell 调用解析后的样子。
type shellRequest struct {
	description string
	command     string
	timeoutMS   int
	// workdir 真实路径；workdirRel 相对工作区（工作区本身是 "."）
	workdir    string
	workdirRel string
}

// parseShell 校验参数并解析工作目录。工作目录必须在工作区内（链接解开后仍在，junction 也一样）。
func (t *turn) parseShell(arguments map[string]any) (shellRequest, error) {
	request := shellRequest{}
	request.description = strings.TrimSpace(asString(arguments["description"]))
	request.command = asString(arguments["command"])
	if request.description == "" {
		return request, denied("缺少 description：用一句话说明这条命令要做什么（必填）。")
	}
	if strings.TrimSpace(request.command) == "" {
		return request, denied("缺少 command：要运行的命令。")
	}
	request.timeoutMS = shellDefaultTimeoutMS
	if raw, present := arguments["timeout_ms"]; present && raw != nil {
		value, ok := wholeNumber(raw)
		if !ok || value < 1 {
			return request, denied("timeout_ms 必须是正整数（毫秒），收到 %v。", raw)
		}
		request.timeoutMS = min(value, shellMaxTimeoutMS)
	}
	root := t.workspaceDir()
	if root == "" {
		return request, denied("工作区目录不存在，没有地方可以运行命令。")
	}
	rawDir := strings.TrimSpace(asString(arguments["workdir"]))
	dir, rel, err := resolveInWorkspace(root, rawDir)
	if err != nil {
		return request, denied("workdir 必须在工作区目录内：%s。", err.Error())
	}
	info, statErr := os.Stat(dir)
	if statErr != nil || !info.IsDir() {
		return request, denied("workdir 不是工作区里存在的文件夹：%s。", displayPath(rel))
	}
	request.workdir, request.workdirRel = dir, rel
	return request, nil
}

// ---------------------------------------------------------------- 风险提示（尽力而为，不是安全边界）

var (
	loopbackPattern  = regexp.MustCompile(`(?i)localhost|127\.\d+\.\d+\.\d+|0\.0\.0\.0|\[?::1\]?|\[::\]`)
	driveAbsPattern  = regexp.MustCompile(`(?i)(?:^|[\s"'=(,;|&])([a-z]:[\\/][^\s"'<>|;&)]*)`)
	uncPattern       = regexp.MustCompile(`(?:^|[\s"'=(,;|&])(\\\\[^\s"'<>|;&)]+)`)
	posixAbsPattern  = regexp.MustCompile(`(?:^|[\s"'=(,;|&])(/(?:[A-Za-z0-9_.-]+/?)+)`)
	parentPattern    = regexp.MustCompile(`(?:^|[\s"'=/\\])\.\.(?:[\\/]|$|[\s"'])`)
	homePattern      = regexp.MustCompile(`(?i)(?:^|[\s"'=(,;|&])~(?:[\\/]|\s|$)|\$home|\$env:(?:userprofile|appdata|localappdata|homepath|temp|tmp|windir|programfiles)|%(?:userprofile|appdata|localappdata|homepath|temp|tmp|windir)%`)
	portNumberFormat = `(?:^|[^0-9])%s(?:[^0-9]|$)`
)

// shellRisk 对一条命令的提示。
type shellRisk struct {
	// Loopback 命令里出现了本机服务地址：full 也要问
	Loopback bool
	Warnings []string
}

// analyzeShell 看命令字符串里有没有明显的高风险写法。local 是本服务的地址（Host:Port）。
// 这是字符串识别：base64、变量拼接、别名之类的写法都能绕过，只是给人看的提示，不是安全边界。
func analyzeShell(command, root string, local []string) shellRisk {
	risk := shellRisk{}
	loopback := loopbackPattern.MatchString(command)
	var ports []string
	for _, address := range local {
		host, port := address, ""
		if colon := strings.LastIndex(address, ":"); colon >= 0 && !strings.Contains(address[colon:], "]") {
			host, port = address[:colon], address[colon+1:]
		}
		if host != "" && !strings.EqualFold(host, "localhost") && strings.Contains(strings.ToLower(command), strings.ToLower(host)) {
			loopback = true
		}
		if port != "" {
			ports = append(ports, port)
		}
	}
	for _, port := range ports {
		if regexp.MustCompile(fmt.Sprintf(portNumberFormat, regexp.QuoteMeta(port))).MatchString(command) {
			loopback = true
		}
	}
	if loopback {
		risk.Loopback = true
		risk.Warnings = append(risk.Warnings, "命令里出现了本机服务地址或端口（localhost / 127.0.0.1 / 本程序的监听端口）。命令行可以直接调用本程序的 HTTP 接口，绕过界面上“审核只有人能做”的限制。请确认这条命令确实没有在调用本程序的接口；完全权限模式下这类命令也必须由你确认。")
	}
	if strings.Contains(command, "交付") || strings.Contains(command, "材料") {
		risk.Warnings = append(risk.Warnings, "命令提到了“交付”或“材料”文件夹。这两处由程序写入，Agent 的文件工具不能写，命令行没有这个限制。")
	}
	outside := []string{}
	for _, pattern := range []*regexp.Regexp{driveAbsPattern, uncPattern, posixAbsPattern} {
		for _, match := range pattern.FindAllStringSubmatch(command, -1) {
			path := strings.TrimRight(match[1], `.,)`)
			if root != "" && withinRoot(root, filepath.FromSlash(path)) {
				continue
			}
			if pattern == posixAbsPattern && runtime.GOOS == "windows" && len(path) < 3 {
				continue
			}
			outside = append(outside, path)
		}
	}
	if len(outside) > 0 {
		risk.Warnings = append(risk.Warnings, "命令里有指向工作区之外的绝对路径："+strings.Join(uniqueStrings(outside, 3), "、")+"。命令行能读写工作区之外的地方。")
	} else if parentPattern.MatchString(command) || homePattern.MatchString(command) {
		risk.Warnings = append(risk.Warnings, "命令里有 .. 或用户目录、系统目录这类写法，可能访问工作区之外的地方。")
	}
	return risk
}

func uniqueStrings(items []string, limit int) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
		if len(out) == limit {
			break
		}
	}
	return out
}

// truncateCommand 会话日志里的命令最多 4KB。
func truncateCommand(command string) string {
	if len(command) <= shellLogCommandBytes {
		return command
	}
	cut := shellLogCommandBytes
	for cut > 0 && !utf8.RuneStart(command[cut]) {
		cut--
	}
	return command[:cut] + fmt.Sprintf("…（命令共 %d 字节，日志里只留前 4096 字节）", len(command))
}

// redactShellArgs 写进会话日志的参数：命令截断到 4KB。
func redactShellArgs(arguments map[string]any) map[string]any {
	command, ok := arguments["command"].(string)
	if !ok || len(command) <= shellLogCommandBytes {
		return arguments
	}
	copied := make(map[string]any, len(arguments))
	for key, value := range arguments {
		copied[key] = value
	}
	copied["command"] = truncateCommand(command)
	return copied
}

// ---------------------------------------------------------------- 运行

// scrubEnv 去掉本程序和模型 Key 相关的环境变量：DE_*、*API_KEY*、*TOKEN*、*SECRET*、*PASSWORD*、*CREDENTIAL*。
func scrubEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, entry := range environ {
		name := entry
		if equals := strings.Index(entry[min(1, len(entry)):], "="); equals >= 0 {
			name = entry[:equals+min(1, len(entry))]
		}
		upper := strings.ToUpper(name)
		switch {
		case strings.HasPrefix(upper, "DE_"),
			strings.Contains(upper, "API_KEY"), strings.Contains(upper, "APIKEY"),
			strings.Contains(upper, "TOKEN"), strings.Contains(upper, "SECRET"),
			strings.Contains(upper, "PASSWORD"), strings.Contains(upper, "CREDENTIAL"):
			continue
		}
		out = append(out, entry)
	}
	return out
}

// tailBuffer 只保留最后 limit 字节的输出，并记下是否丢过内容。
type tailBuffer struct {
	mu        sync.Mutex
	limit     int
	data      []byte
	truncated bool
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > b.limit {
		b.truncated = true
		b.data = append([]byte{}, b.data[len(b.data)-b.limit:]...)
	}
	return len(p), nil
}

// text 保留下来的内容；从尾部截时可能切在字符中间，开头不完整的字符去掉。
func (b *tailBuffer) text() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	data := b.data
	if b.truncated {
		for len(data) > 0 && !utf8.RuneStart(data[0]) {
			data = data[1:]
		}
	}
	return strings.ToValidUTF8(string(data), "�")
}

// shellProgram 这个平台上运行命令的程序和参数。Windows 优先 pwsh，找不到退回 powershell.exe；其他平台 bash -lc。
func shellProgram(command string) (string, []string, error) {
	if runtime.GOOS == "windows" {
		program, err := exec.LookPath("pwsh")
		if err != nil {
			program, err = exec.LookPath("powershell.exe")
		}
		if err != nil {
			return "", nil, errors.New("找不到 pwsh，也找不到 powershell.exe")
		}
		// 先把输出编码设成 UTF-8（Windows 默认是系统代码页，中文会乱码），再运行命令
		wrapped := "[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false); " + command
		return program, []string{"-NoProfile", "-NonInteractive", "-Command", wrapped}, nil
	}
	program, err := exec.LookPath("bash")
	if err != nil {
		return "", nil, errors.New("找不到 bash")
	}
	return program, []string{"-lc", command}, nil
}

// shellResult 一次运行的结果。
type shellResult struct {
	Output    string
	ExitCode  int
	TimedOut  bool
	Truncated bool
	Duration  time.Duration
}

// runShell 运行一条命令：新进程、固定工作目录、超时杀整个进程树、两路输出各留 64000 字节尾部。
// ctx 被取消（停止按钮）时同样杀进程树并返回 ctx.Err()。
func runShell(ctx context.Context, request shellRequest) (shellResult, error) {
	program, args, err := shellProgram(request.command)
	if err != nil {
		return shellResult{}, err
	}
	timeout := time.Duration(request.timeoutMS) * time.Millisecond
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.Command(program, args...)
	cmd.Dir = request.workdir
	cmd.Env = scrubEnv(os.Environ())
	cmd.Stdin = nil
	stdout, stderr := &tailBuffer{limit: shellOutputKeep}, &tailBuffer{limit: shellOutputKeep}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	configureProcess(cmd)
	// 杀掉进程树之后管道可能还被漏网的孙进程握着：最多再等 2 秒就放弃等输出
	cmd.WaitDelay = 2 * time.Second

	started := time.Now()
	if err := cmd.Start(); err != nil {
		return shellResult{}, fmt.Errorf("启动命令行失败：%v", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	var waitErr error
	timedOut, cancelled := false, false
	select {
	case waitErr = <-waited:
	case <-runCtx.Done():
		killProcessTree(cmd)
		waitErr = <-waited
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			timedOut = true
		} else {
			cancelled = true
		}
	}
	// 进程树里漏网的孙进程可能还握着管道：Wait 已经返回，不再等它们
	result := shellResult{Duration: time.Since(started), TimedOut: timedOut, Truncated: stdout.truncated || stderr.truncated}
	if cancelled {
		return result, ctx.Err()
	}
	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if !timedOut {
			return result, fmt.Errorf("命令行运行出错：%v", waitErr)
		}
	}
	if timedOut {
		exitCode = -1
	}
	result.ExitCode = exitCode
	result.Output = formatShellOutput(stdout, stderr, exitCode, timedOut, request.timeoutMS)
	return result, nil
}

// formatShellOutput 输出文本 = stdout，加标 [stderr] 的 stderr 段，再加 [exit code: N]（0 不加）；超时标 [timed out after …]。
func formatShellOutput(stdout, stderr *tailBuffer, exitCode int, timedOut bool, timeoutMS int) string {
	var out bytes.Buffer
	section := func(buffer *tailBuffer, name string) string {
		text := buffer.text()
		if buffer.truncated {
			text = fmt.Sprintf("（%s 已截断：只保留最后 %d 字节）\n%s", name, shellOutputKeep, text)
		}
		return text
	}
	out.WriteString(section(stdout, "stdout"))
	if stderr.text() != "" || stderr.truncated {
		if out.Len() > 0 && !bytes.HasSuffix(out.Bytes(), []byte("\n")) {
			out.WriteString("\n")
		}
		out.WriteString("[stderr]\n")
		out.WriteString(section(stderr, "stderr"))
	}
	if timedOut {
		if out.Len() > 0 && !bytes.HasSuffix(out.Bytes(), []byte("\n")) {
			out.WriteString("\n")
		}
		out.WriteString(fmt.Sprintf("[timed out after %dms]", timeoutMS))
	} else if exitCode != 0 {
		if out.Len() > 0 && !bytes.HasSuffix(out.Bytes(), []byte("\n")) {
			out.WriteString("\n")
		}
		out.WriteString("[exit code: " + strconv.Itoa(exitCode) + "]")
	}
	return out.String()
}

// shellTool shell。动作类别是“运行命令”：read_only 拒绝，workspace 每次问，full 放行并写日志
// （命令里出现本机服务地址时 full 也问）；“本会话总是允许”不提供；说话人不是发起人时任何模式都拒绝。
func (loop *turnLoop) shellTool() tool {
	t := func() *turn { return loop.turn }
	return tool{
		spec: agent.ToolSpec{
			Name: contract.ToolShell,
			Description: "在工作区目录里运行一条命令行命令（Windows 上是 pwsh，其他平台是 bash）。每次调用是新进程，不保留状态；工作目录是工作区（或工作区内的 workdir）。" +
				"输出是 stdout，加 [stderr] 段，非零退出码写 [exit code: N]（不是工具错误，你自己判断）；超时会杀掉整个进程树。" +
				"优先用专用工具：找文件用 find_files，搜内容用 search_content，读文件用 read_file，写文件用 write_file / edit_file，不要用命令行 ls / cat / grep / echo >。" +
				"命令行没有沙箱，每次运行都要发起人确认（完全权限模式除外）；仅可查看模式下不能用，请把打算运行的命令写在方案里。" +
				"不要用命令行调用本程序自己的 HTTP 接口。",
			Parameters: map[string]any{"type": "object", "required": []any{"description", "command"}, "properties": map[string]any{
				"description": map[string]any{"type": "string", "description": "一句话说明这条命令要做什么（必填，会显示在确认卡上）"},
				"command":     map[string]any{"type": "string", "description": "要运行的命令"},
				"timeout_ms":  map[string]any{"type": "integer", "description": "超时毫秒数，默认 120000，上限 600000"},
				"workdir":     map[string]any{"type": "string", "description": "工作目录，必须在工作区内，相对工作区目录；不填就是工作区目录"},
			}},
		},
		action:   ActionShell,
		noAlways: true,
		redact:   redactShellArgs,
		guard: func(arguments map[string]any) error {
			_, err := t().parseShell(arguments)
			return err
		},
		describe: func(arguments map[string]any) (string, string) {
			return "运行命令", strings.TrimSpace(asString(arguments["description"]))
		},
		decorate: func(card *Card, arguments map[string]any) {
			request, err := t().parseShell(arguments)
			if err != nil {
				return
			}
			card.Command, card.Workdir = request.command, request.workdir
			risk := analyzeShell(request.command, t().workspaceDir(), loop.job.manager.localAddrs())
			card.Warnings = risk.Warnings
		},
		forceAsk: func(arguments map[string]any) (bool, string) {
			request, err := t().parseShell(arguments)
			if err != nil {
				return false, ""
			}
			risk := analyzeShell(request.command, t().workspaceDir(), loop.job.manager.localAddrs())
			if risk.Loopback {
				return true, "命令里出现本机服务地址或端口，完全权限模式下也要发起人确认"
			}
			return false, ""
		},
		runCtx: func(ctx context.Context, arguments map[string]any) (any, error) {
			// 人可能等了很久才点允许：参数和工作目录要再检查一遍
			request, err := t().parseShell(arguments)
			if err != nil {
				return nil, err
			}
			result, err := runShell(ctx, request)
			if err != nil {
				if ctx.Err() != nil {
					return nil, errTurnCancelled
				}
				return nil, toolFailure("shell_failed", "%s", err.Error())
			}
			return map[string]any{
				"output": result.Output, "exitCode": result.ExitCode, "timedOut": result.TimedOut,
				"truncated": result.Truncated, "durationMs": result.Duration.Milliseconds(),
			}, nil
		},
	}
}

// localAddrs 本服务的地址（Host:Port），shell 的风险提示用。
func (m *Manager) localAddrs() []string {
	if m.deps.LocalAddrs == nil {
		return nil
	}
	return m.deps.LocalAddrs()
}

// shellModeLine 系统提示词里说明 shell 在当前模式下的处理。
func shellModeLine(mode string) string {
	switch mode {
	case PermissionReadOnly:
		return "shell（命令行）在仅可查看模式下会被直接拒绝：不要调用它，把打算运行的命令和理由写进方案里，等发起人切换模式。"
	case PermissionFull:
		return "shell（命令行）在完全权限模式下不用确认，但命令里出现本机服务地址（localhost、127.0.0.1、本程序端口）时仍会弹确认卡；你不应该用命令行调用本程序自己的 HTTP 接口。命令行没有沙箱，务必只做用户要求的事。"
	}
	return "shell（命令行）每次运行都会弹确认卡，由发起人看过命令后允许一次或拒绝（没有“总是允许”）；被拒绝后换办法，不要原样再试。命令行没有沙箱，不要用它去碰工作区之外的东西，也不要调用本程序自己的 HTTP 接口。"
}
