// 应用主流程：资源 → 工作区 → 服务端（可选打开浏览器）。
// 对应 packages/app/src/serve.ts + packages/server/src/start.ts。
//
// 引擎 / 工具 / 渲染由调用方通过工厂注入（Go 单二进制里进程内直连），
// 这样 internal/app 不依赖尚未就绪的 internal/engine、internal/tools、internal/render。
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/server"
	"digital-employee/internal/settings"
)

// DefaultPort 默认端口（设计 §14）；被占用时从它开始往后找 20 个。
const DefaultPort = 8787

// portAttempts 首选端口被占用时往后试几个。
const portAttempts = 20

// EngineDeps 引擎需要的依赖；由启动入口的工厂翻译成 internal/engine 的构造参数。
type EngineDeps struct {
	WorkspaceDir  string
	EmployeesDir  string
	ToolClient    contract.ToolClient
	Faults        *contract.FaultHooks
	ScriptFile    string
	ModelSettings contract.ModelSettings
}

// Options 启动参数。
type Options struct {
	// RepoRoot 仓库根；缺省自动向上查找（含 employees/ 与 samples/）。
	RepoRoot string
	// WorkspaceDir 启动时的工作区；缺省 DE_WORKSPACE / 工作区列表里的当前工作区 / %APPDATA%/digital-employee。
	// 只有 DE_WORKSPACE 会让切换入口只读。
	WorkspaceDir string
	// EmployeesDir 覆盖员工目录（DE_EMPLOYEES_DIR）。
	EmployeesDir string
	// WebDistDir 覆盖前端构建产物目录（DE_WEB_DIST）。
	WebDistDir string
	// Port 首选端口；缺省 DE_PORT / PORT / 8787。
	Port int
	// PortAttempts 首选端口被占用时往后试几个；缺省 20。
	PortAttempts int
	// Hostname 监听地址；缺省全部接口（桌面模式传 127.0.0.1）。
	Hostname string
	// ScriptFile 剧本式假模型文件（DE_SCRIPTED）。
	ScriptFile string
	// NoOpen 不打开浏览器（DE_NO_OPEN=1 等价）。
	NoOpen bool
	// SkipPrepare 跳过工作区释放（测试/已准备好的工作区）。
	SkipPrepare bool
	// EngineFactory 引擎工厂；nil 时用占位引擎（联调前）。
	EngineFactory func(EngineDeps) (contract.Engine, error)
	// ToolFactory 工具客户端工厂；nil 时不创建（设置页故障下发跳过）。
	ToolFactory func() (contract.ToolClient, error)
	// LoadEmployee 员工配置加载器；nil 时用 server.LoadEmployeeConfig。
	LoadEmployee func(dir string) (*contract.EmployeeConfig, error)
	// OnSettingsSaved 设置保存后重建模型提供者。
	OnSettingsSaved func(contract.ModelSettings) error
	// Model 取前台 Agent 当前的模型提供者；nil 时会话按模型不可用处理。
	Model func() agent.ModelProvider
	// Faults 演示开关的运行时可变对象，与引擎、设置路由共享；nil 时新建。
	Faults *contract.FaultHooks
	// Env 环境变量；nil 时读进程环境。
	Env map[string]string
	// Logger 注入日志器；nil 时按 settings.json 的 logLevel 新建。
	Logger *server.Logger
	// LogLine 日志输出行（测试注入）；nil 时写标准输出/错误。
	LogLine func(line string)
	// AppVersion 诊断页版本号。
	AppVersion string
}

// Running 已启动的服务端。
type Running struct {
	URL    string
	Port   int
	Server *http.Server

	host       *workspaceHost
	listener   net.Listener
	closeOnce  sync.Once
	closeError error
}

func envValue(env map[string]string, key string) string {
	if env != nil {
		if value, ok := env[key]; ok {
			return value
		}
		return ""
	}
	return os.Getenv(key)
}

func envInt(env map[string]string, keys ...string) int {
	for _, key := range keys {
		if raw := envValue(env, key); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

// Start 准备并启动服务端；返回后可继续做别的事（例如打开桌面窗口）。
func Start(options Options) (*Running, error) {
	env := options.Env
	repoRoot := options.RepoRoot
	if repoRoot == "" && !options.SkipPrepare {
		repoRoot = FindRepoRoot("")
	}
	scriptFile := options.ScriptFile
	if scriptFile == "" {
		scriptFile = envValue(env, "DE_SCRIPTED")
	}
	host := &workspaceHost{
		options: options, env: env, repoRoot: repoRoot, scriptFile: scriptFile,
		locked: envValue(env, "DE_WORKSPACE") != "",
	}

	// 启动时的工作区：参数 > DE_WORKSPACE > 工作区列表里的当前工作区 > 默认目录
	workspaceDir := options.WorkspaceDir
	if workspaceDir == "" && host.locked {
		workspaceDir = envValue(env, "DE_WORKSPACE")
	}
	if !host.locked {
		registry, err := loadWorkspaceRegistry(ResolveAppDir(env))
		if err != nil {
			return nil, err
		}
		host.registry = registry
		if workspaceDir == "" {
			workspaceDir = registry.currentPath()
		}
	}
	if workspaceDir == "" {
		workspaceDir = ResolveWorkspace(env)
	}
	workspaceDir = absolutePath(workspaceDir)
	if host.registry != nil {
		host.registry.Current = host.registry.ensure(workspaceDir).ID
		if err := host.registry.save(); err != nil {
			return nil, fmt.Errorf("保存工作区列表失败：%w", err)
		}
	}
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return nil, err
	}

	// 诊断日志级别：启动时按 settings.json 生效
	doc := settings.Read(workspaceDir)
	var logger *server.Logger
	if options.Logger != nil {
		logger = options.Logger
	} else if options.LogLine != nil {
		sink := func(_ string, line string) { options.LogLine(line) }
		logger = server.NewLogger(doc.Diagnostics.LogLevel, sink)
	} else {
		logger = server.NewLogger(doc.Diagnostics.LogLevel, nil)
	}
	logger.Debugf("设置文档：v%d revision %d；日志级别 %s", doc.Version, doc.Revision, doc.Diagnostics.LogLevel)
	host.logger = logger

	initial, err := host.build(workspaceDir, options.Faults)
	if err != nil {
		return nil, err
	}
	host.current.Store(initial)
	host.resume(initial)

	startPort := options.Port
	if startPort == 0 {
		startPort = envInt(env, "DE_PORT", "PORT")
	}
	if startPort == 0 {
		startPort = DefaultPort
	}
	attempts := options.PortAttempts
	if attempts == 0 {
		attempts = portAttempts
	}
	port, err := ChoosePort(startPort, attempts, IsPortAvailable)
	if err != nil {
		initial.close()
		return nil, err
	}
	address := fmt.Sprintf(":%d", port)
	if options.Hostname != "" {
		address = fmt.Sprintf("%s:%d", options.Hostname, port)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		initial.close()
		return nil, err
	}
	actualPort := port
	if tcpAddr, ok := listener.Addr().(*net.TCPAddr); ok {
		actualPort = tcpAddr.Port
	}
	httpServer := &http.Server{Handler: http.HandlerFunc(host.ServeHTTP), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if serveErr := httpServer.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Errorf("HTTP 服务异常退出：%v", serveErr)
		}
	}()

	urlHost := options.Hostname
	if urlHost == "" || urlHost == "0.0.0.0" {
		urlHost = "localhost"
	}
	url := fmt.Sprintf("http://%s:%d", urlHost, actualPort)
	scriptInfo := ""
	if scriptFile != "" {
		scriptInfo = "，剧本式模型 " + scriptFile
	}
	logger.Infof("digital-employee 服务已启动：%s（工作区 %s%s）", url, workspaceDir, scriptInfo)
	logger.Debugf("监听端口 %d（首选 %d，最多尝试 %d 个）", actualPort, startPort, attempts)

	if !options.NoOpen && envValue(env, "DE_NO_OPEN") != "1" {
		if err := server.OpenBrowser(url); err != nil {
			logger.Warnf("打开浏览器失败（可以从控制台复制地址）：%v", err)
		}
	}

	return &Running{URL: url, Port: actualPort, Server: httpServer, host: host, listener: listener}, nil
}

// Close 停止 HTTP（释放端口），再关当前工作区自己起的工具客户端；注入的客户端由调用方负责。
func (r *Running) Close() error {
	r.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr := r.Server.Shutdown(ctx)
		if shutdownErr != nil {
			_ = r.Server.Close()
		}
		if current := r.host.current.Load(); current != nil {
			current.close()
		}
		r.closeError = shutdownErr
	})
	return r.closeError
}

// WaitForSignals 等待 Ctrl-C / SIGTERM（桌面模式由窗口关闭代替）。
func (r *Running) WaitForSignals() {
	channel := make(chan os.Signal, 1)
	signal.Notify(channel, os.Interrupt, syscall.SIGTERM)
	<-channel
	signal.Stop(channel)
}

// Serve 启动服务端并阻塞到收到信号；返回时已收尾。
func Serve(options Options) error {
	running, err := Start(options)
	if err != nil {
		return err
	}
	running.WaitForSignals()
	return running.Close()
}
