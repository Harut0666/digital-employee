package desktop

import (
	_ "embed"
	"errors"

	"github.com/egoist/mygo"
)

// 窗口外壳的行为细节（标题、尺寸、状态记忆、外链、右键菜单、图标、单实例接线）。
// window.go 只负责开窗口，这些都放在这里，避免两边互相改动。

// WindowTitle 窗口标题（单实例按它兜底找窗口）。
const WindowTitle = "迷你数字员工"

// 默认和最小窗口尺寸（DIP）。
const (
	DefaultWidth  = 1280
	DefaultHeight = 860
	MinWidth      = 960
	MinHeight     = 640
)

//go:embed icon.png
var iconPNG []byte

// ErrNoWebView 系统里没有 WebView2 Runtime（Windows）或等价的系统 WebView，窗口开不起来。
var ErrNoWebView = errors.New("desktop: 没有找到系统 WebView（Windows 需要 Microsoft Edge WebView2 Runtime）")

// CheckAvailable 开窗口前的检查：没有系统 WebView 时返回 ErrNoWebView。可以在启动服务端之前调用。
func CheckAvailable() error {
	if !mygo.WebViewAvailable() {
		return ErrNoWebView
	}
	return nil
}

// applyShell 在窗口选项上补上外壳需要的设置：记住窗口状态、普通用户用不到的开发者工具关掉。
func applyShell(window *mygo.WindowOptions, opts Options) {
	if opts.AppDir != "" {
		window.StateKey = stateKey
	}
	if !opts.Debug {
		window.Page.DevTools = mygo.DevToolsDisabled
	}
}

// installStateStore 把窗口状态存到应用目录（必须在创建窗口之前）。
func installStateStore(opts Options) {
	if opts.AppDir == "" {
		return
	}
	mygo.App.SetWindowStateStore(&fileStateStore{
		path:      StatePath(opts.AppDir),
		minimum:   Size{Width: MinWidth, Height: MinHeight},
		workAreas: currentWorkAreas,
	})
}

// attachShell 窗口建好之后的接线：图标、外链交给系统浏览器、精简右键菜单、登记窗口给单实例用。
func attachShell(window *mygo.Window, opts Options) {
	if len(iconPNG) > 0 {
		_ = window.SetIcon(iconPNG)
	}
	page := window.Page()
	page.OnWillNavigate(func(event *mygo.NavigateEvent) {
		if IsExternalURL(event.URL) {
			event.PreventDefault()
			target := event.URL
			go func() { _ = mygo.Shell.OpenExternal(target) }()
		}
	})
	if !opts.Debug {
		page.OnContextMenu(trimContextMenu)
	}
	if opts.Instance != nil {
		opts.Instance.PublishWindow(opts.AppDir, window.NativeHandle())
	}
}
