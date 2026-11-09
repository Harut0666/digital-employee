package main

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"runtime/debug"
	"strings"

	"digital-employee/internal/app"
	"digital-employee/internal/desktop"
	"digital-employee/internal/server"
)

// webViewDownloadURL WebView2 Runtime 的官方下载页。
const webViewDownloadURL = "https://developer.microsoft.com/microsoft-edge/webview2/"

// failureTitle 启动失败消息框的标题。
const failureTitle = "迷你数字员工 - 启动失败"

// runWindowMode 默认模式（双击）：单实例检查 → 日志文件 → 检查 WebView → 内嵌服务端 → 原生窗口。
// 窗口关闭后服务端优雅关闭，整个进程退出。
func runWindowMode(config launchConfig) (code int) {
	options := serverOptions(config)
	appDir := app.ResolveAppDirFor(options)

	instance, first := desktop.AcquireInstance(appDir)
	if !first {
		// 同一个应用目录已经有实例在运行：把它的窗口带到前台，自己静默退出
		desktop.ActivateExisting(appDir)
		return 0
	}
	defer instance.Release()

	rolling, logPath := openWindowLog(appDir)
	if rolling != nil {
		defer rolling.Close()
		log.SetOutput(rolling) // MyGo 和标准库的 log 输出也进文件
	}
	defer app.RedirectCrashOutput(appDir)()
	writeLine := func(line string) {
		if rolling != nil {
			rolling.WriteLine(line)
		}
		if consoleAvailable {
			fmt.Fprintln(os.Stdout, line)
		}
	}
	fail := func(err error) int {
		reportStartupFailure(err, appDir, logPath, writeLine)
		return 1
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			code = fail(fmt.Errorf("程序内部错误：%v\n%s", recovered, debug.Stack()))
		}
	}()

	writeLine(fmt.Sprintf("digital-employee %s 启动（窗口模式，应用目录 %s）", appVersion, appDir))
	if !fullMode() {
		writeLine("[warn] 正在使用占位引擎（internal/engine 尚未接入）：只提供 HTTP 契约联调，不执行真实步骤")
	}

	// 先查 WebView，再起服务端：没有 WebView 时不要白白恢复未完成的任务
	if err := desktop.CheckAvailable(); err != nil {
		return fail(err)
	}

	options.LogLine = func(line string) { writeLine(line) }
	running, err := app.Start(options)
	if err != nil {
		return fail(err)
	}

	windowOptions := desktop.DefaultOptions(running.URL)
	windowOptions.Debug = false
	windowOptions.AppDir = appDir
	windowOptions.Instance = instance
	windowErr := desktop.Run(windowOptions)

	if closeErr := running.Close(); closeErr != nil {
		writeLine(fmt.Sprintf("[warn] 服务端关闭时有未完成的连接，已强制断开：%v", closeErr))
	}
	if windowErr != nil {
		return fail(windowErr)
	}
	writeLine("窗口已关闭，程序退出")
	return 0
}

// openWindowLog 打开应用目录里的滚动日志；打不开（目录不可写）时返回 nil，程序照常运行，
// 真正的原因会在后面的启动失败消息框里说明。
func openWindowLog(appDir string) (*app.RollingLog, string) {
	path := app.LogFilePath(appDir)
	rolling, err := app.OpenRollingLog(path, app.DefaultLogMaxBytes, app.DefaultLogKeep)
	if err != nil {
		return nil, path
	}
	return rolling, path
}

// reportStartupFailure 写日志、（有终端时）打到标准错误，并弹原生消息框。
// 缺 WebView2 时消息框带“是否打开下载页”的询问。
func reportStartupFailure(err error, appDir, logPath string, writeLine func(string)) {
	title, text, openURL := describeStartupFailure(err, appDir)
	writeLine("[error] " + strings.ReplaceAll(err.Error(), "\n", " | "))
	if consoleAvailable {
		fmt.Fprintln(os.Stderr, text)
	}
	if _, statErr := os.Stat(logPath); statErr == nil {
		text += "\n\n日志文件：" + logPath
	}
	if openURL != "" {
		if desktop.AskYesNo(title, text+"\n\n要现在打开下载页面吗？") {
			_ = server.OpenBrowser(openURL)
		}
		return
	}
	desktop.ShowError(title, text)
}

// describeStartupFailure 把启动错误翻译成给用户看的原因和处理办法；
// openURL 非空表示处理办法是去那个页面（消息框会问要不要打开）。纯函数，便于测试。
func describeStartupFailure(err error, appDir string) (title, text, openURL string) {
	title = failureTitle
	message := err.Error()
	var opError *net.OpError
	switch {
	case errors.Is(err, desktop.ErrNoWebView):
		text = "无法打开窗口：这台电脑缺少 Microsoft Edge WebView2 Runtime。\n\n" +
			"处理办法：\n" +
			"1. 安装 Microsoft Edge WebView2 Runtime（选 Evergreen 版）：\n   " + webViewDownloadURL + "\n" +
			"2. 或者先用浏览器方式：在命令行运行 digital-employee.exe --browser"
		openURL = webViewDownloadURL
	case strings.Contains(message, "都被占用"):
		text = "无法启动：" + message + "\n\n" +
			"处理办法：关闭占用这些端口的程序，或在命令行用 --port <端口> 指定一个空闲端口（也可设置环境变量 DE_PORT）。"
	case errors.As(err, &opError) && opError.Op == "listen":
		text = "无法启动：监听本机端口失败。\n" + message + "\n\n" +
			"处理办法：关闭占用端口的程序，或在命令行用 --port <端口> 指定一个空闲端口（也可设置环境变量 DE_PORT）。"
	case errors.Is(err, fs.ErrPermission):
		text = "无法启动：应用目录不可写。\n" + message + "\n\n" +
			"应用目录：" + appDir + "\n" +
			"处理办法：检查这个目录的权限（或被杀毒软件拦截），或设置环境变量 DE_APP_DIR 指向一个可写的目录。"
	default:
		text = "启动失败：" + message + "\n\n" +
			"应用目录：" + appDir + "\n" +
			"可以用 --browser 在浏览器里使用，或把下面的日志文件发给开发者排查。"
	}
	return title, text, openURL
}
