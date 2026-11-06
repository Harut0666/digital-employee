//go:build !de_full

// 默认构建的接线：占位引擎 + 占位工具客户端。
// internal/engine、internal/tools 接入后，用 -tags de_full 构建由 runtime_full.go 接管，
// 或把本文件删除并把 runtime_full.go 的构建标签去掉。
package main

import (
	"digital-employee/internal/app"
	"digital-employee/internal/cli"
	"digital-employee/internal/contract"
)

// fullMode 当前是否真实接线。
func fullMode() bool { return false }

// appEngineFactory 服务端模式的引擎工厂（占位）。
func appEngineFactory(deps app.EngineDeps) (contract.Engine, error) {
	return app.NewMockEngine(nil), nil
}

// appToolFactory 服务端模式的工具客户端工厂（占位）。
func appToolFactory() (contract.ToolClient, error) {
	return &app.MockToolClient{}, nil
}

// cliEngineFactory CLI 模式的引擎工厂（占位）。
func cliEngineFactory(options cli.EngineOptions) (contract.Engine, error) {
	return app.NewMockEngine(nil), nil
}

// cliToolFactory CLI 模式的工具客户端工厂（占位）。
func cliToolFactory() (contract.ToolClient, error) {
	return &app.MockToolClient{}, nil
}

// onSettingsSaved 设置保存后重建模型提供者（占位：没有模型，直接成功）。
func onSettingsSaved(settings contract.ModelSettings) error {
	_ = settings
	return nil
}
