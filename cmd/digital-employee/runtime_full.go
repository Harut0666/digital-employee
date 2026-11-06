//go:build !de_stub

// 真实接线（默认构建）：internal/engine + internal/tools + internal/render。
//
// 这份文件由 Lead 维护；占位实现见 runtime_stub.go（构建标签 de_stub），
// 用 `go build -tags de_stub` 可得到不带真实引擎的联调版本。
//
// 接线内容：
//   - 工具客户端：internal/tools.Registry，render_report_docx 转调 internal/render
//   - 引擎：internal/engine.New，模型提供者按 --scripted / DE_SCRIPTED 选择剧本式或真实
//   - 设置页保存后的模型热替换：引擎实现 SetModelProvider 时自动生效，否则下次启动生效
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"digital-employee/internal/agent"
	"digital-employee/internal/app"
	"digital-employee/internal/cli"
	"digital-employee/internal/contract"
	"digital-employee/internal/engine"
	"digital-employee/internal/render"
	"digital-employee/internal/tools"
)

// fullMode 当前是否真实接线。
func fullMode() bool { return true }

var (
	engineMu     sync.RWMutex
	activeEngine *engine.Engine
	// lastModelSettings 最近一次保存的模型设置。
	lastModelSettings contract.ModelSettings
)

// newToolClient 构造工具注册表：render_report_docx 走纯 Go 渲染实现。
func newToolClient() (contract.ToolClient, error) {
	return tools.NewRegistry(tools.Options{
		Render: func(input contract.RenderReportInput) (contract.RenderReportOutput, error) {
			return render.RenderReportDocx(input, render.Options{})
		},
	}), nil
}

// newModelProvider 按剧本文件或模型设置构造模型提供者。
func newModelProvider(scriptFile string, modelSettings contract.ModelSettings) (agent.ModelProvider, error) {
	if scriptFile != "" {
		raw, err := os.ReadFile(scriptFile)
		if err != nil {
			return nil, fmt.Errorf("读取剧本文件 %s 失败: %w", scriptFile, err)
		}
		var script agent.ModelScript
		if err := json.Unmarshal(raw, &script); err != nil {
			return nil, fmt.Errorf("解析剧本文件 %s 失败: %w", scriptFile, err)
		}
		return agent.NewScriptedModelProvider(script, agent.ScriptedProviderOptions{}), nil
	}
	return agent.NewModelProvider(modelSettings), nil
}

// newEngine 按统一参数构造真实引擎。
func newEngine(options cli.EngineOptions, faults *contract.FaultHooks) (contract.Engine, error) {
	toolClient := options.ToolClient
	if toolClient == nil {
		var err error
		toolClient, err = newToolClient()
		if err != nil {
			return nil, err
		}
	}
	model, err := newModelProvider(options.ScriptFile, options.ModelSettings)
	if err != nil {
		return nil, err
	}
	eng, err := engine.New(engine.Deps{
		WorkspaceDir: options.WorkspaceDir,
		EmployeesDir: options.EmployeesDir,
		ToolClient:   toolClient,
		Model:        model,
		Faults:       faults,
		ExitOnCrash:  true,
	})
	if err != nil {
		return nil, err
	}
	engineMu.Lock()
	activeEngine = eng
	lastModelSettings = options.ModelSettings
	engineMu.Unlock()
	return eng, nil
}

// appEngineFactory 服务端模式的引擎工厂。
func appEngineFactory(deps app.EngineDeps) (contract.Engine, error) {
	return newEngine(cli.EngineOptions{
		WorkspaceDir:  deps.WorkspaceDir,
		EmployeesDir:  deps.EmployeesDir,
		ToolClient:    deps.ToolClient,
		ScriptFile:    deps.ScriptFile,
		ModelSettings: deps.ModelSettings,
	}, deps.Faults)
}

// appToolFactory 服务端模式的工具客户端工厂。
func appToolFactory() (contract.ToolClient, error) { return newToolClient() }

// cliEngineFactory CLI 模式的引擎工厂。
func cliEngineFactory(options cli.EngineOptions) (contract.Engine, error) {
	return newEngine(options, nil)
}

// cliToolFactory CLI 模式的工具客户端工厂。
func cliToolFactory() (contract.ToolClient, error) { return newToolClient() }

// modelSwapper 引擎若支持模型热替换则实现它（engine 的 SetModelProvider）。
type modelSwapper interface {
	SetModelProvider(agent.ModelProvider)
}

// onSettingsSaved 设置保存后重建模型提供者。
// 剧本模式（DE_SCRIPTED / --scripted）下不覆盖剧本；否则按新设置重建。
func onSettingsSaved(settings contract.ModelSettings) error {
	engineMu.Lock()
	eng := activeEngine
	lastModelSettings = settings
	engineMu.Unlock()

	if eng == nil {
		return nil // 还没建引擎（例如命令行 init），下次启动生效
	}
	provider, err := newModelProvider(scriptFileIfScripted(), settings)
	if err != nil {
		return err
	}
	if swapper, ok := any(eng).(modelSwapper); ok {
		swapper.SetModelProvider(provider)
		return nil
	}
	// 引擎暂不支持热替换：不报错，保留设置，重启后生效。
	fmt.Fprintln(os.Stderr, "[info] 模型设置已保存；当前引擎不支持热替换，新设置从下次启动开始生效")
	return nil
}

// scriptFileIfScripted 返回当前进程的剧本文件（--scripted 会设置 DE_SCRIPTED）。
func scriptFileIfScripted() string {
	if v := os.Getenv("DE_SCRIPTED"); v != "" {
		return v
	}
	if v := os.Getenv("DE_SCRIPT_FILE"); v != "" {
		return v
	}
	return ""
}
