// Package desktop 是 MyGo 桌面外壳：只负责开一个原生窗口加载本机 HTTP 地址。
//
// 迁移策略：Go 后端仍然是一个标准 net/http 服务（rest + SSE），窗口加载
// http://127.0.0.1:<port>，因此 React 前端与数据层零改动，也不依赖 MyGo 的
// 绑定/IPC。这样同一份后端可以：命令行跑用例、浏览器打开、窗口里打开。
package desktop

import (
	"fmt"
	"log"

	"github.com/egoist/mygo"
)

// Options 窗口选项。
type Options struct {
	// URL 要加载的地址，例如 http://127.0.0.1:8787/
	URL string
	// Title 窗口标题
	Title string
	// Width/Height 初始尺寸（DIP）
	Width, Height int
	// Debug 打开开发者工具（仅开发构建）
	Debug bool
	// AppDir 应用目录：窗口状态（window-state.json）存在这里；为空则不记忆窗口状态
	AppDir string
	// Instance 单实例句柄：窗口建好后把窗口句柄登记给它，让后启动的实例能把窗口带到前台
	Instance *Instance
}

// DefaultOptions 默认窗口尺寸与标题。
func DefaultOptions(url string) Options {
	return Options{
		URL:    url,
		Title:  WindowTitle,
		Width:  DefaultWidth,
		Height: DefaultHeight,
	}
}

// Run 打开窗口并阻塞到应用退出。必须在主 goroutine 调用。
func Run(opts Options) error {
	if opts.URL == "" {
		return fmt.Errorf("desktop: URL 不能为空")
	}
	if opts.Title == "" {
		opts.Title = WindowTitle
	}
	if opts.Width <= 0 {
		opts.Width = DefaultWidth
	}
	if opts.Height <= 0 {
		opts.Height = DefaultHeight
	}

	mygo.App.SetName("迷你数字员工")
	mygo.App.SetVersion(appVersion())
	installStateStore(opts)

	// 创建窗口时 MyGo 遇到问题（没有 WebView2 等）会 panic：接住，让 Run 返回错误由调用方弹框
	var windowErr error
	mygo.App.WhenReady(func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				windowErr = fmt.Errorf("desktop: 创建窗口失败: %v", recovered)
				mygo.App.Quit()
			}
		}()
		windowOptions := mygo.WindowOptions{
			Title:     opts.Title,
			URL:       opts.URL,
			Width:     opts.Width,
			Height:    opts.Height,
			MinWidth:  MinWidth,
			MinHeight: MinHeight,
		}
		applyShell(&windowOptions, opts)
		win := mygo.NewWindow(windowOptions)
		attachShell(win, opts)
		if opts.Debug {
			win.Page().OpenDevTools()
		}
	})

	if err := mygo.App.Run(); err != nil {
		return fmt.Errorf("desktop: 启动窗口失败: %w", err)
	}
	if windowErr != nil {
		return windowErr
	}
	log.Println("desktop: 窗口已关闭")
	return nil
}

// appVersion 由构建期 -ldflags 注入；默认开发版本。
var version = "0.1.0-dev"

func appVersion() string { return version }
