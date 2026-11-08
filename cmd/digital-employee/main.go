// digital-employee 命令入口：无参数启动服务端（浏览器打开界面），或用子命令跑用例 / 初始化工作区。
//
// 子命令（与 TS 版 exe 一致）：
//
//	无参数 / --serve-only / --window  → 启动服务端
//	init                              → 只初始化工作区
//	mcp-server                        → 工具服务器（Go 版为进程内直连，保留占位）
//	run-case / run-cases / check / replay / fixtures → 交给 internal/cli
//
// 引擎/工具的真实接线在 runtime_*.go（构建标签 de_full）；默认构建用占位实现，
// internal/engine 与 internal/tools 就绪后把默认切到真实接线即可。
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"digital-employee/internal/app"
	"digital-employee/internal/cli"
	"digital-employee/internal/desktop"
)

const appVersion = "0.1.0-go"

const usageText = `用法：
  digital-employee                 启动服务端并打开浏览器
  digital-employee --serve-only    启动服务端（不打开浏览器；DE_NO_OPEN=1 等价）
  digital-employee --window        启动服务端并打开原生窗口
  digital-employee init            只初始化工作区
  digital-employee run-case <case.json> [--scripted <script.json>] [--out <dir>]
  digital-employee run-cases <glob> [--scripted|--real] [--out <dir>]
  digital-employee check <result> <expect>
  digital-employee replay <workspace> <taskId>
  digital-employee fixtures [--out <dir>] [--source <xlsx>]
  digital-employee mcp-server

环境变量：DE_WORKSPACE / DE_PORT / PORT / DE_NO_OPEN=1 / DE_EMPLOYEES_DIR / DE_WEB_DIST / DE_SCRIPTED / DE_APP_DIR
  DE_APP_DIR 是应用目录（缺省 %APPDATA%\digital-employee-app）：设置、员工、知识库和所有工作区的会话、任务都在这里
  DE_WORKSPACE 指定单目录模式：这个目录同时是应用目录和唯一的工作区（命令行用例就是这种用法）
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	if len(argv) > 0 && isCLISubcommand(argv[0]) {
		deps := cli.Deps{
			NewEngine:     cliEngineFactory,
			NewToolClient: cliToolFactory,
			InitWorkspace: initWorkspace,
			Stdout:        os.Stdout,
			Stderr:        os.Stderr,
		}
		if !fullMode() && !isReadOnlyCommand(argv[0]) {
			fmt.Fprintln(os.Stderr, "[warn] 正在使用占位引擎/工具（internal/engine、internal/tools 尚未接入），只提供 HTTP 契约联调")
		}
		return cli.Run(argv, deps)
	}
	return serve(argv)
}

func isCLISubcommand(name string) bool {
	switch name {
	case "run-case", "run-cases", "check", "replay", "fixtures", "init", "mcp-server":
		return true
	}
	return false
}

func isReadOnlyCommand(name string) bool {
	switch name {
	case "init", "replay", "check":
		return true
	}
	return false
}

func initWorkspace() (string, error) {
	// 单目录模式（DE_WORKSPACE）准备这个目录；否则准备应用目录（DE_APP_DIR / %APPDATA%/digital-employee-app）
	prepared, err := app.PrepareAppDir(app.PrepareOptions{
		AppDir:   app.ResolveAppDir(nil),
		RepoRoot: app.FindRepoRoot(""),
	})
	if err != nil {
		return "", err
	}
	return prepared.WorkspaceDir, nil
}

func serve(argv []string) int {
	options := app.Options{
		EngineFactory:   appEngineFactory,
		ToolFactory:     appToolFactory,
		OnSettingsSaved: onSettingsSaved,
		Model:           frontdeskModel,
		AppVersion:      appVersion,
	}
	window := false
	for index := 0; index < len(argv); index++ {
		arg := argv[index]
		takeValue := func() (string, bool) {
			if index+1 >= len(argv) {
				return "", false
			}
			index++
			return argv[index], true
		}
		name, inlineValue, hasInline := strings.Cut(arg, "=")
		switch name {
		case "--window":
			window = true
			options.NoOpen = true
		case "--serve-only", "--no-open":
			options.NoOpen = true
		case "--port":
			raw := inlineValue
			if !hasInline {
				value, ok := takeValue()
				if !ok {
					return usageError("--port 需要端口号")
				}
				raw = value
			}
			port, err := strconv.Atoi(raw)
			if err != nil || port <= 0 {
				return usageError("--port 需要正整数端口号")
			}
			options.Port = port
		case "--port-attempts":
			raw := inlineValue
			if !hasInline {
				value, ok := takeValue()
				if !ok {
					return usageError("--port-attempts 需要次数")
				}
				raw = value
			}
			attempts, err := strconv.Atoi(raw)
			if err != nil || attempts <= 0 {
				return usageError("--port-attempts 需要正整数")
			}
			options.PortAttempts = attempts
		case "--host":
			raw := inlineValue
			if !hasInline {
				value, ok := takeValue()
				if !ok {
					return usageError("--host 需要地址")
				}
				raw = value
			}
			options.Hostname = raw
		case "--workspace":
			// 单目录模式：这个目录同时是应用目录和唯一的工作区（Spec 0006 之后没有切换）
			raw := inlineValue
			if !hasInline {
				value, ok := takeValue()
				if !ok {
					return usageError("--workspace 需要目录")
				}
				raw = value
			}
			options.WorkspaceDir = raw
		case "--employees-dir":
			raw := inlineValue
			if !hasInline {
				value, ok := takeValue()
				if !ok {
					return usageError("--employees-dir 需要目录")
				}
				raw = value
			}
			options.EmployeesDir = raw
		case "--web-dist":
			raw := inlineValue
			if !hasInline {
				value, ok := takeValue()
				if !ok {
					return usageError("--web-dist 需要目录")
				}
				raw = value
			}
			options.WebDistDir = raw
		case "--scripted":
			raw := inlineValue
			if !hasInline {
				value, ok := takeValue()
				if !ok {
					return usageError("--scripted 需要文件")
				}
				raw = value
			}
			options.ScriptFile = raw
		case "--version":
			fmt.Println("digital-employee " + appVersion)
			return 0
		case "-h", "--help":
			fmt.Print(usageText)
			return 0
		default:
			return usageError("未知参数：" + arg)
		}
	}

	if !fullMode() && !window {
		fmt.Fprintln(os.Stderr, "[warn] 正在使用占位引擎（internal/engine 尚未接入）：只提供 HTTP 契约联调，不执行真实步骤")
	}
	if window {
		running, err := app.Start(options)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			return 1
		}
		windowOptions := desktop.DefaultOptions(running.URL)
		windowOptions.Debug = false
		windowErr := desktop.Run(windowOptions)
		_ = running.Close()
		if windowErr != nil {
			fmt.Fprintln(os.Stderr, windowErr.Error())
			return 1
		}
		return 0
	}
	if err := app.Serve(options); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	return 0
}

func usageError(problem string) int {
	if problem != "" {
		fmt.Fprintln(os.Stderr, problem)
	}
	fmt.Fprint(os.Stderr, usageText)
	return 2
}
