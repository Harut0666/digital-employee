package main

import (
	"errors"
	"strconv"
	"strings"

	"digital-employee/internal/app"
)

// launchMode 这次启动要做什么。
type launchMode int

const (
	// modeWindow 起内嵌服务端并开原生窗口（双击的默认行为）；窗口关闭整个程序退出
	modeWindow launchMode = iota
	// modeBrowser 起服务端并用默认浏览器打开（0007 之前的默认行为）
	modeBrowser
	// modeServeOnly 只起服务端，不开浏览器也不开窗口
	modeServeOnly
	// modeCLI 命令行子命令（run-cases、replay、init……），交给 internal/cli
	modeCLI
	// modeVersion 打印版本后退出
	modeVersion
	// modeHelp 打印用法后退出
	modeHelp
)

// launchConfig 解析后的启动配置。
type launchConfig struct {
	mode launchMode
	// options 服务端启动参数（modeWindow / modeBrowser / modeServeOnly 用）
	options app.Options
	// argv 原始参数（modeCLI 整个交给 internal/cli）
	argv []string
}

// usageProblem 参数写错了：problem 是原因，输出用法后以退出码 2 结束。
type usageProblem struct{ problem string }

func (u *usageProblem) Error() string { return u.problem }

// needsConsole 这次启动是不是要在终端里看到输出：
// 命令行子命令、--version、-h、--browser、--serve-only 都是；参数写错了也是（要显示用法）。
// 双击（无参数）和 --window 不需要，它们的信息都写日志文件。
func (c launchConfig) needsConsole(parseErr error) bool {
	if parseErr != nil {
		return true
	}
	return c.mode != modeWindow
}

// isCLISubcommand 第一个参数是不是命令行子命令。
func isCLISubcommand(name string) bool {
	switch name {
	case "run-case", "run-cases", "check", "replay", "fixtures", "init", "mcp-server":
		return true
	}
	return false
}

// valueFlags 后面跟一个值的参数（--port 8788 或 --port=8788）。
var valueFlags = map[string]string{
	"--port":          "--port 需要端口号",
	"--port-attempts": "--port-attempts 需要次数",
	"--host":          "--host 需要地址",
	"--workspace":     "--workspace 需要目录",
	"--employees-dir": "--employees-dir 需要目录",
	"--web-dist":      "--web-dist 需要目录",
	"--scripted":      "--scripted 需要文件",
}

// parseLaunch 解析命令行。getenv 读环境变量（测试注入）。
//
// 无参数 = modeWindow；DE_NO_OPEN=1 时无模式参数等价 --serve-only（脚本和冒烟测试靠它起纯服务端）。
// 窗口模式缺省只监听 127.0.0.1：窗口和服务端在同一个进程里，不需要对局域网开放。
func parseLaunch(argv []string, getenv func(string) string) (launchConfig, error) {
	config := launchConfig{argv: argv}
	if len(argv) > 0 && isCLISubcommand(argv[0]) {
		config.mode = modeCLI
		return config, nil
	}

	explicit := map[launchMode]bool{}
	for index := 0; index < len(argv); index++ {
		arg := argv[index]
		name, inline, hasInline := strings.Cut(arg, "=")
		if missing, isValueFlag := valueFlags[name]; isValueFlag {
			raw := inline
			if !hasInline {
				if index+1 >= len(argv) {
					return config, &usageProblem{missing}
				}
				index++
				raw = argv[index]
			}
			if problem := applyValueFlag(&config.options, name, raw); problem != "" {
				return config, &usageProblem{problem}
			}
			continue
		}
		switch name {
		case "--window":
			explicit[modeWindow] = true
		case "--browser":
			explicit[modeBrowser] = true
		case "--serve-only", "--no-open":
			explicit[modeServeOnly] = true
		case "--version":
			config.mode = modeVersion
			return config, nil
		case "-h", "--help":
			config.mode = modeHelp
			return config, nil
		default:
			return config, &usageProblem{"未知参数：" + arg}
		}
	}

	if len(explicit) > 1 {
		return config, &usageProblem{"--window、--browser、--serve-only 只能选一个"}
	}
	switch {
	case explicit[modeBrowser]:
		config.mode = modeBrowser
	case explicit[modeServeOnly]:
		config.mode = modeServeOnly
	case explicit[modeWindow]:
		config.mode = modeWindow
	case getenv != nil && getenv("DE_NO_OPEN") == "1":
		config.mode = modeServeOnly
	default:
		config.mode = modeWindow
	}

	switch config.mode {
	case modeWindow:
		config.options.NoOpen = true
		if config.options.Hostname == "" {
			config.options.Hostname = "127.0.0.1"
		}
	case modeServeOnly:
		config.options.NoOpen = true
	}
	return config, nil
}

// applyValueFlag 把带值的参数写进服务端选项；返回非空串表示值不合法。
func applyValueFlag(options *app.Options, name, raw string) string {
	switch name {
	case "--port":
		port, err := strconv.Atoi(raw)
		if err != nil || port <= 0 {
			return "--port 需要正整数端口号"
		}
		options.Port = port
	case "--port-attempts":
		attempts, err := strconv.Atoi(raw)
		if err != nil || attempts <= 0 {
			return "--port-attempts 需要正整数"
		}
		options.PortAttempts = attempts
	case "--host":
		options.Hostname = raw
	case "--workspace":
		// 单目录模式：这个目录同时是应用目录和唯一的工作区（Spec 0006 之后没有切换）
		options.WorkspaceDir = raw
	case "--employees-dir":
		options.EmployeesDir = raw
	case "--web-dist":
		options.WebDistDir = raw
	case "--scripted":
		options.ScriptFile = raw
	}
	return ""
}

// asUsageProblem 判断 err 是不是参数错误。
func asUsageProblem(err error) (*usageProblem, bool) {
	var problem *usageProblem
	if errors.As(err, &problem) {
		return problem, true
	}
	return nil, false
}
