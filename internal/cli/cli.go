// Package cli 是 Go 版命令行：run-case / run-cases / check / replay / fixtures / init / mcp-server。
// 对应 packages/cli/src/commands.ts。
//
// 引擎与工具客户端通过 Deps 注入（真实实现由 cmd 提供），CLI 只依赖 contract。
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"digital-employee/internal/contract"
)

// EngineOptions 创建引擎的入参；由 cmd 的适配器翻译成 internal/engine 的构造参数。
type EngineOptions struct {
	// ApplicationDir 应用目录：任务、设置、知识库都在这里（Spec 0006；单目录模式下就是用例的工作区目录）
	ApplicationDir string
	// WorkspaceDir 用例的工作区目录（单目录模式下和应用目录相同）
	WorkspaceDir   string
	EmployeesDir   string
	ToolClient     contract.ToolClient
	ScriptFile     string
	ModelSettings  contract.ModelSettings
	Env            map[string]string
	ConfigFallback func(employeeID string) (string, bool)
	// Workspaces 登记的工作区：引擎写交付文件夹时按“工作区 ID → 目录”查（T42，形状同
	// engine.Deps.Workspaces）；命令行没有登记，为空。
	Workspaces func(workspaceID string) (string, bool)
}

// Deps 命令行依赖。
type Deps struct {
	NewEngine     func(EngineOptions) (contract.Engine, error)
	NewToolClient func() (contract.ToolClient, error)
	// InitWorkspace 释放/准备工作区（init 子命令），返回工作区路径。
	InitWorkspace func() (string, error)
	// RunMCPServer mcp-server 子命令（Go 版不再需要 stdio 工具进程，可 stub）。
	RunMCPServer func() error
	Stdout       io.Writer
	Stderr       io.Writer
	Env          map[string]string
	Now          func() time.Time
}

// Env 值读取；Deps.Env 为 nil 时读进程环境。
func (d *Deps) env(key string) string {
	if d.Env != nil {
		if value, ok := d.Env[key]; ok {
			return value
		}
		return ""
	}
	return os.Getenv(key)
}

func (d *Deps) stdout() io.Writer {
	if d.Stdout != nil {
		return d.Stdout
	}
	return os.Stdout
}

func (d *Deps) stderr() io.Writer {
	if d.Stderr != nil {
		return d.Stderr
	}
	return os.Stderr
}

func (d *Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

const usageText = `用法：
  de run-case <case.json> [--scripted <script.json>] [--out <dir>]   按用例驱动引擎并保存结果
  de run-cases <glob> [--scripted|--real] [--out <dir>]              批量跑用例并核对判定要点，退出码=未通过条数
  de check <result> <expect>                                        核对一次运行的交付物（不重跑）
  de fixtures [--out <dir>] [--source <xlsx>]                       从公开样例生成变异数据（确定性）
  de replay <workspace> <taskId>                                    按时间回放任务事件
  de init                                                           只初始化工作区
  de mcp-server                                                     工具服务器（Go 版为进程内直连，保留占位）
`

// Run 命令行入口：跑完返回退出码；命令自身抛错时打印消息并返回 1（不再向调用方抛）。
func Run(argv []string, deps Deps) int {
	code, err := run(argv, deps)
	if err != nil {
		fmt.Fprintln(deps.stderr(), err.Error())
		return 1
	}
	return code
}

func run(argv []string, deps Deps) (int, error) {
	if len(argv) == 0 {
		fmt.Fprint(deps.stderr(), usageText)
		return 0, nil
	}
	command := argv[0]
	rest := argv[1:]
	switch command {
	case "replay":
		return replayCommand(rest, deps)
	case "run-case":
		return runCaseCommandArgs(rest, deps)
	case "run-cases":
		return runCasesCommandArgs(rest, deps)
	case "check":
		return checkCommand(rest, deps)
	case "fixtures":
		return fixturesCommand(rest, deps)
	case "init":
		if deps.InitWorkspace == nil {
			return 0, fmt.Errorf("init：未配置工作区初始化")
		}
		dir, err := deps.InitWorkspace()
		if err != nil {
			return 1, err
		}
		fmt.Fprintf(deps.stdout(), "工作区已就绪：%s\n", dir)
		return 0, nil
	case "mcp-server":
		if deps.RunMCPServer != nil {
			return 0, deps.RunMCPServer()
		}
		fmt.Fprintln(deps.stderr(), "mcp-server：Go 版工具在进程内直连，不再需要 stdio 子进程（占位实现）")
		return 0, nil
	default:
		return usageError(deps, "未知命令："+command)
	}
}

func usageError(deps Deps, problem string) (int, error) {
	if problem != "" {
		fmt.Fprintln(deps.stderr(), problem)
	}
	fmt.Fprint(deps.stderr(), usageText)
	if problem != "" {
		return 2, nil
	}
	return 0, nil
}

// parsedArgs 一个简单的参数解析结果：位置参数 + 值参数 + 布尔参数。
type parsedArgs struct {
	positionals []string
	values      map[string]string
	bools       map[string]bool
}

// parseArgs 解析参数；支持 --flag value / --flag=value / -flag 三种写法，位置参数可混在中间。
func parseArgs(args []string, valueFlags, boolFlags []string) (parsedArgs, error) {
	result := parsedArgs{values: map[string]string{}, bools: map[string]bool{}}
	isValue := func(name string) bool { return containsString(valueFlags, name) }
	isBool := func(name string) bool { return containsString(boolFlags, name) }
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			result.positionals = append(result.positionals, args[index+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			result.positionals = append(result.positionals, arg)
			continue
		}
		name := strings.TrimLeft(arg, "-")
		if equals := strings.Index(name, "="); equals >= 0 {
			key, value := name[:equals], name[equals+1:]
			if !isValue(key) {
				return result, fmt.Errorf("未知参数：--%s", key)
			}
			result.values[key] = value
			continue
		}
		if isBool(name) {
			result.bools[name] = true
			continue
		}
		if !isValue(name) {
			return result, fmt.Errorf("未知参数：--%s", name)
		}
		if index+1 >= len(args) {
			return result, fmt.Errorf("参数 --%s 缺少取值", name)
		}
		index++
		result.values[name] = args[index]
	}
	return result, nil
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
