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
}

// DefaultOptions 默认窗口尺寸与标题。
func DefaultOptions(url string) Options {
	return Options{
		URL:    url,
		Title:  "迷你数字员工",
		Width:  1280,
		Height: 860,
	}
}

// Run 打开窗口并阻塞到应用退出。必须在主 goroutine 调用。
func Run(opts Options) error {
	if opts.URL == "" {
		return fmt.Errorf("desktop: URL 不能为空")
	}
	if opts.Title == "" {
		opts.Title = "迷你数字员工"
	}
	if opts.Width <= 0 {
		opts.Width = 1280
	}
	if opts.Height <= 0 {
		opts.Height = 860
	}

	mygo.App.SetName("迷你数字员工")
	mygo.App.SetVersion(appVersion())

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     opts.Title,
			URL:       opts.URL,
			Width:     opts.Width,
			Height:    opts.Height,
			MinWidth:  960,
			MinHeight: 640,
		})
		if opts.Debug {
			win.Page().OpenDevTools()
		}
	})

	if err := mygo.App.Run(); err != nil {
		return fmt.Errorf("desktop: 启动窗口失败: %w", err)
	}
	log.Println("desktop: 窗口已关闭")
	return nil
}

// appVersion 由构建期 -ldflags 注入；默认开发版本。
var version = "0.1.0-dev"

func appVersion() string { return version }
