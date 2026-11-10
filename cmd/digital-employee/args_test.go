package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"strings"
	"testing"

	"digital-employee/internal/desktop"
)

func noEnv(string) string { return "" }

func TestParseLaunchDefaultsToWindowWithLoopbackOnly(t *testing.T) {
	config, err := parseLaunch(nil, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if config.mode != modeWindow {
		t.Fatalf("无参数应该是窗口模式，得到 %v", config.mode)
	}
	if !config.options.NoOpen {
		t.Fatalf("窗口模式不该再开浏览器")
	}
	if config.options.Hostname != "127.0.0.1" {
		t.Fatalf("窗口模式只监听本机回环，得到 %q", config.options.Hostname)
	}
	if config.needsConsole(nil) {
		t.Fatalf("双击（无参数）不需要控制台")
	}
}

func TestParseLaunchModeFlags(t *testing.T) {
	cases := []struct {
		name        string
		argv        []string
		env         map[string]string
		mode        launchMode
		noOpen      bool
		host        string
		needConsole bool
	}{
		{"--window 等同默认", []string{"--window"}, nil, modeWindow, true, "127.0.0.1", false},
		{"--browser 是原来的默认", []string{"--browser"}, nil, modeBrowser, false, "", true},
		{"--serve-only", []string{"--serve-only"}, nil, modeServeOnly, true, "", true},
		{"--no-open 等价 --serve-only", []string{"--no-open"}, nil, modeServeOnly, true, "", true},
		{"DE_NO_OPEN=1 且没有模式参数 = 只起服务端", nil, map[string]string{"DE_NO_OPEN": "1"}, modeServeOnly, true, "", true},
		{"显式 --window 压过 DE_NO_OPEN", []string{"--window"}, map[string]string{"DE_NO_OPEN": "1"}, modeWindow, true, "127.0.0.1", false},
		{"--browser 压过 DE_NO_OPEN", []string{"--browser"}, map[string]string{"DE_NO_OPEN": "1"}, modeBrowser, false, "", true},
		{"只给端口仍是窗口", []string{"--port", "8788"}, nil, modeWindow, true, "127.0.0.1", false},
		{"窗口模式尊重显式 --host", []string{"--host", "0.0.0.0"}, nil, modeWindow, true, "0.0.0.0", false},
	}
	for _, item := range cases {
		config, err := parseLaunch(item.argv, func(key string) string { return item.env[key] })
		if err != nil {
			t.Errorf("%s：%v", item.name, err)
			continue
		}
		if config.mode != item.mode || config.options.NoOpen != item.noOpen || config.options.Hostname != item.host {
			t.Errorf("%s：mode=%v noOpen=%v host=%q，想要 mode=%v noOpen=%v host=%q",
				item.name, config.mode, config.options.NoOpen, config.options.Hostname, item.mode, item.noOpen, item.host)
		}
		if config.needsConsole(nil) != item.needConsole {
			t.Errorf("%s：needsConsole=%v，想要 %v", item.name, config.needsConsole(nil), item.needConsole)
		}
	}
}

func TestParseLaunchCLISubcommandsAreUntouched(t *testing.T) {
	for _, name := range []string{"run-case", "run-cases", "check", "replay", "fixtures", "init", "mcp-server"} {
		argv := []string{name, "samples/cases/*.json", "--scripted", "--out", "x"}
		config, err := parseLaunch(argv, noEnv)
		if err != nil {
			t.Fatalf("%s：%v", name, err)
		}
		if config.mode != modeCLI || len(config.argv) != len(argv) || config.argv[0] != name {
			t.Fatalf("%s 应该整个交给 CLI：%+v", name, config)
		}
		if !config.needsConsole(nil) {
			t.Fatalf("%s 要在终端里看到输出", name)
		}
	}
}

func TestParseLaunchVersionAndHelp(t *testing.T) {
	for argv, want := range map[string]launchMode{"--version": modeVersion, "-h": modeHelp, "--help": modeHelp} {
		config, err := parseLaunch([]string{argv}, noEnv)
		if err != nil || config.mode != want {
			t.Fatalf("%s：mode=%v err=%v", argv, config.mode, err)
		}
		if !config.needsConsole(nil) {
			t.Fatalf("%s 要在终端里看到输出", argv)
		}
	}
}

func TestParseLaunchServerOptions(t *testing.T) {
	config, err := parseLaunch([]string{
		"--port=9000", "--port-attempts", "3", "--host", "127.0.0.1",
		"--workspace", "D:/ws", "--employees-dir=E", "--web-dist", "W", "--scripted", "S.json", "--browser",
	}, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	options := config.options
	if options.Port != 9000 || options.PortAttempts != 3 || options.Hostname != "127.0.0.1" ||
		options.WorkspaceDir != "D:/ws" || options.EmployeesDir != "E" || options.WebDistDir != "W" || options.ScriptFile != "S.json" {
		t.Fatalf("选项解析不对：%+v", options)
	}
}

func TestParseLaunchRejectsBadArguments(t *testing.T) {
	bad := [][]string{
		{"--bogus"},
		{"--port"},
		{"--port", "abc"},
		{"--port=0"},
		{"--port-attempts", "-1"},
		{"--workspace"},
		{"--window", "--browser"},
		{"--browser", "--serve-only"},
	}
	for _, argv := range bad {
		config, err := parseLaunch(argv, noEnv)
		if err == nil {
			t.Errorf("%v 应该报参数错误", argv)
			continue
		}
		if _, ok := asUsageProblem(err); !ok {
			t.Errorf("%v 的错误应是用法问题：%v", argv, err)
		}
		if !config.needsConsole(err) {
			t.Errorf("%v 参数写错了要能在终端里看到用法", argv)
		}
	}
}

func TestDescribeStartupFailure(t *testing.T) {
	appDir := `D:\data\app`
	title, text, openURL := describeStartupFailure(fmt.Errorf("打开窗口：%w", desktop.ErrNoWebView), appDir)
	if title == "" || openURL != webViewDownloadURL {
		t.Fatalf("缺 WebView2 应给下载页：title=%q url=%q", title, openURL)
	}
	for _, want := range []string{"WebView2", webViewDownloadURL, "--browser"} {
		if !strings.Contains(text, want) {
			t.Errorf("缺 WebView2 的提示缺少 %q：%s", want, text)
		}
	}

	_, text, openURL = describeStartupFailure(errors.New("端口 8787-8806 都被占用，请用 DE_PORT 指定一个空闲端口"), appDir)
	if openURL != "" || !strings.Contains(text, "--port") || !strings.Contains(text, "DE_PORT") {
		t.Errorf("端口占用的提示不对：%s", text)
	}
	_, text, _ = describeStartupFailure(&net.OpError{Op: "listen", Net: "tcp", Err: errors.New("bind: address already in use")}, appDir)
	if !strings.Contains(text, "端口") || !strings.Contains(text, "--port") {
		t.Errorf("监听失败应按端口问题提示：%s", text)
	}

	_, text, _ = describeStartupFailure(&fs.PathError{Op: "mkdir", Path: appDir, Err: fs.ErrPermission}, appDir)
	if !strings.Contains(text, "不可写") || !strings.Contains(text, appDir) || !strings.Contains(text, "DE_APP_DIR") {
		t.Errorf("应用目录不可写的提示不对：%s", text)
	}

	_, text, _ = describeStartupFailure(errors.New("别的错误"), appDir)
	if !strings.Contains(text, "别的错误") || !strings.Contains(text, "--browser") {
		t.Errorf("通用提示不对：%s", text)
	}
}
