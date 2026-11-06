// Package assets 用 go:embed 把单 exe 运行所需的资源打进二进制，让 exe 不依赖仓库目录。
//
// 取舍与 TS 版一致：前端构建产物、默认员工配置、公开样例（scenario-a + cases）内嵌；
// 体积大的 samples/fixtures（11.7MB，自测夹具）不内嵌，需要时用 `digital-employee fixtures` 生成。
//
// 释放语义在 internal/app/workspace.go：仓库里能找到资源时优先用仓库（开发态可改，
// 不必每次重新编译）；找不到仓库时（exe 被单独复制到任意目录）回退到内嵌资源。
package assets

import "embed"

// WebDist 前端构建产物（相对 packages/web/dist 的路径）。
//
//go:embed all:packages/web/dist
var WebDist embed.FS

// Employees 默认员工配置（相对 employees/ 的路径，含 workflow.yaml 的员工目录）。
//
//go:embed all:employees
var Employees embed.FS

// ScenarioA 场景 A 公开样例（相对 samples/scenario-a 的路径，含销售明细与周会纪要）。
//
//go:embed all:samples/scenario-a
var ScenarioA embed.FS

// SamplesCases 公开用例与剧本（相对 samples/cases 的路径，含 scripts/）。
//
//go:embed all:samples/cases
var SamplesCases embed.FS
