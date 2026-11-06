//go:build de_full

// 真实接线（构建标签 de_full）：internal/engine / internal/tools 就绪后在这里接上。
//
// ============================ 交接约定（给 Lead） ============================
// 本文件由 Lead 在 engine/tools 落地后填写；默认构建（!de_full）走 runtime_stub.go，
// 因此 `go build ./cmd/digital-employee` 始终可编译、可用占位引擎联调 HTTP 契约。
//
// 需要在这里实现的四个函数（签名固定，不要改）：
//
//	func fullMode() bool
//	func appEngineFactory(deps app.EngineDeps) (contract.Engine, error)
//	func appToolFactory() (contract.ToolClient, error)
//	func cliEngineFactory(options cli.EngineOptions) (contract.Engine, error)
//	func cliToolFactory() (contract.ToolClient, error)
//
// 期望的构造器签名（若 engine/tools 实际签名不同，在下面做一层适配即可）：
//
//	// internal/tools
//	func NewRegistry() *Registry            // 实现 contract.ToolClient
//	// 或 func NewClient() (contract.ToolClient, error)
//
//	// internal/engine
//	type Options struct {
//	    WorkspaceDir  string
//	    EmployeesDir  string
//	    ToolClient    contract.ToolClient
//	    Faults        *contract.FaultHooks
//	    ScriptFile    string                  // 非空 → 剧本式模型（CLI --scripted / DE_SCRIPTED）
//	    ModelSettings contract.ModelSettings  // ScriptFile 为空时按它连真实模型
//	    ExitOnCrash   bool                    // 服务端/CLI 默认 true
//	}
//	func New(options Options) (*Engine, error) // *Engine 实现 contract.Engine
//
// 映射关系：
//
//	appEngineFactory(deps)   → engine.New(engine.Options{WorkspaceDir: deps.WorkspaceDir,
//	                          EmployeesDir: deps.EmployeesDir, ToolClient: deps.ToolClient,
//	                          Faults: deps.Faults, ScriptFile: deps.ScriptFile,
//	                          ModelSettings: deps.ModelSettings, ExitOnCrash: true})
//	appToolFactory()        → tools.NewRegistry()（没有错误分支时包一层 nil error）
//	cliEngineFactory(opts)  → engine.New(engine.Options{...同上，ExitOnCrash: true})
//	cliToolFactory()        → tools.NewRegistry()
//
// 还有一处服务端钩子：onSettingsSaved(settings contract.ModelSettings) error ——
// 设置页保存后由服务端调用，需要用新的 ModelSettings 重建模型提供者（engine 若暴露
// SetModelSettings/UpdateModel 之类的方法就在这里转调；拿不到 engine 引用时可以在
// appEngineFactory 里把 engine 存到包级变量）。占位实现返回 nil 即可。
//
// 构建方式：go build -tags de_full ./cmd/digital-employee
// ============================================================================
package main

import (
	"fmt"

	"digital-employee/internal/app"
	"digital-employee/internal/cli"
	"digital-employee/internal/contract"
)

// fullMode 当前是否真实接线。
func fullMode() bool { return true }

var errNotWired = fmt.Errorf("de_full 接线尚未完成：请在 cmd/digital-employee/runtime_full.go 里接入 internal/engine 与 internal/tools")

// appEngineFactory 服务端模式的引擎工厂。
func appEngineFactory(deps app.EngineDeps) (contract.Engine, error) {
	_ = deps
	return nil, errNotWired
}

// appToolFactory 服务端模式的工具客户端工厂。
func appToolFactory() (contract.ToolClient, error) {
	return nil, errNotWired
}

// cliEngineFactory CLI 模式的引擎工厂。
func cliEngineFactory(options cli.EngineOptions) (contract.Engine, error) {
	_ = options
	return nil, errNotWired
}

// cliToolFactory CLI 模式的工具客户端工厂。
func cliToolFactory() (contract.ToolClient, error) {
	return nil, errNotWired
}

// onSettingsSaved 设置保存后重建模型提供者（占位，接入 engine 后实现）。
func onSettingsSaved(settings contract.ModelSettings) error {
	_ = settings
	return nil
}
