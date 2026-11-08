//go:build de_stub

// 占位接线（构建标签 de_stub）：占位引擎 + 占位工具客户端，用于不接真实引擎的 HTTP 契约联调。
// 默认构建走 runtime_full.go（真实引擎）。
package main

import (
	"digital-employee/internal/agent"
	"digital-employee/internal/app"
	"digital-employee/internal/cli"
	"digital-employee/internal/contract"
)

// fullMode 当前是否真实接线。
func fullMode() bool { return false }

// appEngineFactory 服务端模式的引擎工厂（占位）。
func appEngineFactory(deps app.EngineDeps) (contract.Engine, error) {
	return app.NewMockEngine(nil, deps.ApplicationDir), nil
}

// appToolFactory 服务端模式的工具客户端工厂（占位）。
func appToolFactory() (contract.ToolClient, error) {
	return &app.MockToolClient{}, nil
}

// cliEngineFactory CLI 模式的引擎工厂（占位）。
func cliEngineFactory(options cli.EngineOptions) (contract.Engine, error) {
	return app.NewMockEngine(nil, options.ApplicationDir), nil
}

// cliToolFactory CLI 模式的工具客户端工厂（占位）。
func cliToolFactory() (contract.ToolClient, error) {
	return &app.MockToolClient{}, nil
}

// frontdeskModel 前台 Agent 的模型提供者（占位：没有模型，会话按模型不可用处理）。
func frontdeskModel() agent.ModelProvider { return nil }

// onSettingsSaved 设置保存后重建模型提供者（占位：没有模型，直接成功）。
func onSettingsSaved(settings contract.ModelSettings) error {
	_ = settings
	return nil
}
