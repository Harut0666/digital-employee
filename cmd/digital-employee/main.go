// digital-employee 命令入口：无参数（双击）启动内嵌服务端并打开原生窗口，或用子命令跑用例 / 初始化工作区。
//
// 启动方式：
//
//	无参数 / --window                 → 内嵌服务端（127.0.0.1）+ 原生窗口，窗口关闭整个程序退出
//	--browser                         → 启动服务端并用默认浏览器打开（0007 之前的默认行为）
//	--serve-only                      → 只启动服务端
//	init                              → 只初始化工作区
//	mcp-server                        → 工具服务器（Go 版为进程内直连，保留占位）
//	run-case / run-cases / check / replay / fixtures → 交给 internal/cli
//
// Windows 发行版用 GUI 子系统构建（-H=windowsgui）：双击没有控制台窗口。命令行用途（子命令、--version、
// -h、--browser、--serve-only）启动时接到父进程的控制台上，见 console_windows.go。
//
// 引擎/工具的真实接线在 runtime_*.go（构建标签 de_full）；默认构建用占位实现，
// internal/engine 与 internal/tools 就绪后把默认切到真实接线即可。
package main

import (
	"fmt"
	"os"

	"digital-employee/internal/app"
	"digital-employee/internal/cli"
	"digital-employee/internal/desktop"
)

// appVersion 版本号；发行构建用 -ldflags "-X main.appVersion=..." 覆盖。
var appVersion = "0.1.0-go"

const usageText = `用法：
  digital-employee                 启动服务端并打开原生窗口（双击就是这个；窗口关闭即退出）
  digital-employee --window        同上
  digital-employee --browser       启动服务端并用默认浏览器打开（不开窗口）
  digital-employee --serve-only    启动服务端（不开窗口也不开浏览器；DE_NO_OPEN=1 等价）
  digital-employee init            只初始化工作区
  digital-employee run-case <case.json> [--scripted <script.json>] [--out <dir>]
  digital-employee run-cases <glob> [--scripted|--real] [--out <dir>]
  digital-employee check <result> <expect>
  digital-employee replay <workspace> <taskId>
  digital-employee fixtures [--out <dir>] [--source <xlsx>]
  digital-employee mcp-server

窗口模式没有控制台，日志写到应用目录的 logs\digital-employee.log；启动失败会弹消息框。
同一个应用目录只允许一个窗口实例，再次启动会把已有窗口带到前台。

环境变量：DE_WORKSPACE / DE_PORT / PORT / DE_NO_OPEN=1 / DE_EMPLOYEES_DIR / DE_WEB_DIST / DE_SCRIPTED / DE_APP_DIR
  DE_APP_DIR 是应用目录（缺省 %APPDATA%\digital-employee-app）：设置、员工、知识库和所有工作区的会话、任务都在这里
  DE_WORKSPACE 指定单目录模式：这个目录同时是应用目录和唯一的工作区（命令行用例就是这种用法）
`

// consoleAvailable 终端里能不能看到输出（双击启动的 GUI 进程没有）。
var consoleAvailable = detectConsole()

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	config, parseErr := parseLaunch(argv, os.Getenv)
	if config.needsConsole(parseErr) {
		consoleAvailable = ensureConsole()
	}
	if parseErr != nil {
		if problem, ok := asUsageProblem(parseErr); ok {
			return usageError(problem.problem)
		}
		return usageError(parseErr.Error())
	}

	switch config.mode {
	case modeCLI:
		return runCLI(config.argv)
	case modeVersion:
		fmt.Println("digital-employee " + appVersion)
		return 0
	case modeHelp:
		fmt.Print(usageText)
		return 0
	case modeWindow:
		return runWindowMode(config)
	default:
		return runServerMode(config)
	}
}

func runCLI(argv []string) int {
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

// serverOptions 把解析出的启动选项补上引擎、工具等接线。
func serverOptions(config launchConfig) app.Options {
	options := config.options
	options.EngineFactory = appEngineFactory
	options.ToolFactory = appToolFactory
	options.OnSettingsSaved = onSettingsSaved
	options.Model = frontdeskModel
	options.AppVersion = appVersion
	return options
}

// runServerMode --browser / --serve-only：服务端 + 控制台（Ctrl+C 退出）。
func runServerMode(config launchConfig) int {
	options := serverOptions(config)
	if !fullMode() {
		fmt.Fprintln(os.Stderr, "[warn] 正在使用占位引擎（internal/engine 尚未接入）：只提供 HTTP 契约联调，不执行真实步骤")
	}
	if err := app.Serve(options); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		if !consoleAvailable {
			// 没有控制台（例如从快捷方式带参数启动）：错误只能弹框
			title, text, _ := describeStartupFailure(err, app.ResolveAppDirFor(options))
			desktop.ShowError(title, text)
		}
		return 1
	}
	return 0
}

func usageError(problem string) int {
	if problem != "" {
		fmt.Fprintln(os.Stderr, problem)
	}
	fmt.Fprint(os.Stderr, usageText)
	if !consoleAvailable {
		desktop.ShowError("迷你数字员工 - 参数错误", problem+"\n\n"+usageText)
	}
	return 2
}
