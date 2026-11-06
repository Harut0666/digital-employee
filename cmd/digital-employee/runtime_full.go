//go:build de_full

// 真实接线（构建标签 de_full）：internal/engine / internal/tools 就绪后在这里接上。
//
// 需要接入的构造器（由 engine-dev / tools-data 提供，签名确定后照这里映射）：
//   - engine.New(...) 或等价构造函数：接收工作区、员工目录、contract.ToolClient、FaultHooks、
//     模型提供者（剧本模式读 ScriptFile，否则按 ModelSettings 连真实模型）；
//   - tools.NewRegistry() 或等价构造函数：实现 contract.ToolClient。
//
// 在接入完成前，用 -tags de_full 构建会得到明确的错误，而不是静默使用占位实现。
package main

import (
	"fmt"

	"digital-employee/internal/app"
	"digital-employee/internal/cli"
	"digital-employee/internal/contract"
)

// fullMode 当前是否真实接线。
func fullMode() bool { return true }

var errNotWired = fmt.Errorf("真实接线尚未完成：请在 cmd/digital-employee/runtime_full.go 里接入 internal/engine 与 internal/tools")

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
