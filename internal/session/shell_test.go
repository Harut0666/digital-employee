package session

import (
	"strings"
	"testing"
)

func TestScrubEnvDropsAppAndKeyVariables(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin", "DE_PORT=8787", "de_workspace=x", "OPENAI_API_KEY=k", "deepseek_api_key=k", "GITHUB_TOKEN=t",
		"MY_SECRET=s", "client_secret_id=s", "DB_PASSWORD=p", "HOME=/home/a", "=C:=C:\\", "NORMAL=1",
	}
	got := strings.Join(scrubEnv(environ), "|")
	if got != "PATH=/usr/bin|HOME=/home/a|=C:=C:\\|NORMAL=1" {
		t.Fatalf("scrubEnv = %q", got)
	}
}

func TestAnalyzeShellFlagsLoopbackAndPaths(t *testing.T) {
	root := t.TempDir()
	local := []string{"myhost.lan:8787"}
	cases := []struct {
		command  string
		loopback bool
		warn     string // 警告里应包含的字样；空表示不该有警告
	}{
		{"echo hello", false, ""},
		{"curl http://localhost:3000/", true, "本机服务地址"},
		{"curl http://127.0.0.1/api", true, "本机服务地址"},
		{"curl http://[::1]:80/", true, "本机服务地址"},
		{"curl http://myhost.lan/api", true, "本机服务地址"},
		{"Invoke-RestMethod -Uri http://example.com:8787/api", true, "本机服务地址"}, // 本程序的端口号
		{"echo 8787123", false, ""},                                            // 端口号只按完整数字认
		{`Copy-Item a.txt 交付\a.txt`, false, "交付"},
		{`ls 材料`, false, "材料"},
		{`cat C:\Windows\win.ini`, false, "工作区之外"},
		{`cat /etc/passwd`, false, "工作区之外"},
		{`cat ../../x`, false, ".."},
		{`cat ~/x`, false, "用户目录"},
		{"cat " + root + "/notes.md", false, ""}, // 工作区内的绝对路径没问题
	}
	for _, tc := range cases {
		risk := analyzeShell(tc.command, root, local)
		joined := strings.Join(risk.Warnings, "\n")
		if risk.Loopback != tc.loopback {
			t.Errorf("%q：Loopback = %v，want %v", tc.command, risk.Loopback, tc.loopback)
		}
		if tc.warn == "" && len(risk.Warnings) != 0 {
			t.Errorf("%q 不该有警告：%v", tc.command, risk.Warnings)
		}
		if tc.warn != "" && !strings.Contains(joined, tc.warn) {
			t.Errorf("%q 的警告里应有 %q：%v", tc.command, tc.warn, risk.Warnings)
		}
	}
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	buffer := &tailBuffer{limit: 10}
	_, _ = buffer.Write([]byte("0123456789"))
	_, _ = buffer.Write([]byte("abcde"))
	if buffer.text() != "56789abcde" || !buffer.truncated {
		t.Fatalf("尾部 = %q truncated=%v", buffer.text(), buffer.truncated)
	}
}

func TestFormatShellOutput(t *testing.T) {
	out, errs := &tailBuffer{limit: 100}, &tailBuffer{limit: 100}
	_, _ = out.Write([]byte("a\n"))
	if got := formatShellOutput(out, errs, 0, false, 1000); got != "a\n" {
		t.Fatalf("只有 stdout：%q", got)
	}
	_, _ = errs.Write([]byte("bad\n"))
	if got := formatShellOutput(out, errs, 2, false, 1000); got != "a\n[stderr]\nbad\n\n[exit code: 2]" && got != "a\n[stderr]\nbad\n[exit code: 2]" {
		t.Fatalf("stdout + stderr + 退出码：%q", got)
	}
	if got := formatShellOutput(out, errs, -1, true, 1500); !strings.HasSuffix(got, "[timed out after 1500ms]") {
		t.Fatalf("超时：%q", got)
	}
}

func TestShellCommandTruncationKeepsRuneBoundary(t *testing.T) {
	long := strings.Repeat("字", 3000) // 9000 字节
	got := truncateCommand(long)
	if !strings.Contains(got, "日志里只留前 4096 字节") {
		t.Fatal("应标注截断")
	}
	if short := truncateCommand("echo hi"); short != "echo hi" {
		t.Fatalf("短命令原样：%q", short)
	}
}
