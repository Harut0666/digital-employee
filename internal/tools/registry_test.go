package tools

// 工具注册表 / 故障注入的单测（对应 packages/tools/test/faults.test.ts）。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"digital-employee/internal/contract"
)

func sampleLoadArgs(t *testing.T) map[string]any {
	t.Helper()
	return map[string]any{
		"salesPath": filepath.Join(samplesDir(t), "销售明细.csv"),
		"outDir":    t.TempDir(),
	}
}

func setFaults(t *testing.T, r *Registry, modes map[string]any) contract.AdminSetFaultsOutput {
	t.Helper()
	res, err := r.Call(contract.ToolAdminSetFaults, map[string]any{"tools": modes})
	if err != nil {
		t.Fatalf("admin_set_faults 返回 error：%v", err)
	}
	if !res.OK {
		t.Fatalf("admin_set_faults 失败：%s", res.Error)
	}
	out, ok := res.Data.(contract.AdminSetFaultsOutput)
	if !ok {
		t.Fatalf("admin_set_faults 返回类型 = %T", res.Data)
	}
	return out
}

func TestRegistryListTools(t *testing.T) {
	r := NewRegistry()
	tools, err := r.ListTools()
	if err != nil {
		t.Fatalf("ListTools 失败：%v", err)
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
		if tool.Description == "" {
			t.Errorf("工具 %s 缺少 description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("工具 %s 缺少 inputSchema", tool.Name)
		}
	}
	want := []string{
		contract.ToolValidateSubmission, contract.ToolLoadSales, contract.ToolComputeMetrics,
		contract.ToolDetectContradictions, contract.ToolParseMinutes, contract.ToolRenderReportDocx,
		contract.ToolReadFile, contract.ToolWebSearch, contract.ToolAdminSetFaults,
	}
	if len(names) != len(want) {
		t.Fatalf("工具数量 = %d，期望 %d：%v", len(names), len(want), names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("工具顺序 [%d] = %s，期望 %s", i, names[i], want[i])
		}
	}
	for _, tool := range tools {
		if tool.Name == contract.ToolAdminSetFaults {
			if !strings.Contains(tool.Description, "服务端") || !strings.Contains(tool.Description, "不开放") {
				t.Errorf("admin_set_faults 描述应说明只由服务端调用：%s", tool.Description)
			}
		}
	}
}

// 前台 Agent 的目录读取工具在注册表里也能用（复用同一套解析，见 TestReadFileReusesParsers）。
func TestReadFileThroughRegistry(t *testing.T) {
	r := NewRegistry()
	path := filepath.Join(samplesDir(t), "销售明细.xlsx")
	res, err := r.Call(contract.ToolReadFile, map[string]any{"path": path})
	if err != nil {
		t.Fatalf("read_file 返回 error：%v", err)
	}
	if !res.OK {
		t.Fatalf("read_file 失败：%s", res.Error)
	}
	out, ok := res.Data.(contract.ReadFileOutput)
	if !ok {
		t.Fatalf("read_file 返回类型 = %T", res.Data)
	}
	if !strings.Contains(out.Text, "记录ID") || out.Truncated {
		t.Fatalf("read_file 应复用销售明细解析：%+v", out)
	}
}

func TestRegistryCallSuccess(t *testing.T) {
	r := NewRegistry()
	samples := samplesDir(t)
	res, err := r.Call(contract.ToolValidateSubmission, map[string]any{
		"period":      map[string]any{"year": 2026, "week": 44},
		"attachments": []any{map[string]any{"name": "销售明细.xlsx", "path": filepath.Join(samples, "销售明细.xlsx")}},
		// 引擎会把不存在的 $form 引用解析成 null
		"allowMissingMinutes": nil,
		"allowPartialPeriod":  nil,
	})
	if err != nil {
		t.Fatalf("Call 返回 error：%v", err)
	}
	if !res.OK {
		t.Fatalf("Call 失败：%s", res.Error)
	}
	out, ok := res.Data.(contract.ValidateSubmissionOutput)
	if !ok {
		t.Fatalf("Data 类型 = %T", res.Data)
	}
	if out.OK {
		t.Errorf("缺少纪要时应暂停：%+v", out)
	}
	// 引擎会把 Data 写成 step 产物 JSON
	if _, err := json.Marshal(res.Data); err != nil {
		t.Errorf("Data 不能序列化：%v", err)
	}
}

func TestRegistryFaultError(t *testing.T) {
	r := NewRegistry()
	applied := setFaults(t, r, map[string]any{contract.ToolLoadSales: "error"})
	if applied.Tools[contract.ToolLoadSales] != contract.FaultError {
		t.Fatalf("故障未生效：%+v", applied)
	}
	res, err := r.Call(contract.ToolLoadSales, sampleLoadArgs(t))
	if err != nil {
		t.Fatalf("Call 返回 error：%v", err)
	}
	if res.OK || !strings.Contains(res.Error, "故障注入") {
		t.Fatalf("期望注入报错，实际 %+v", res)
	}
	// 与 TS 版一致：工具错误默认可重试（引擎据此决定重试）
	if !res.Retryable {
		t.Errorf("故障错误应可重试")
	}
	if res.Data != nil {
		t.Errorf("失败时不应带 Data")
	}
}

func TestRegistryFaultTimeout(t *testing.T) {
	r := NewRegistry(Options{FaultTimeoutMs: 20})
	setFaults(t, r, map[string]any{contract.ToolLoadSales: "timeout"})
	started := time.Now()
	res, err := r.Call(contract.ToolLoadSales, sampleLoadArgs(t))
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("Call 返回 error：%v", err)
	}
	if res.OK || !strings.Contains(res.Error, "超时") {
		t.Fatalf("期望超时，实际 %+v", res)
	}
	if elapsed < 15*time.Millisecond {
		t.Errorf("timeout 故障应阻塞到 faultTimeoutMs，实际 %v", elapsed)
	}
	if !res.Retryable {
		t.Errorf("超时应可重试")
	}
}

func TestRegistryFaultInvalidOutput(t *testing.T) {
	r := NewRegistry()
	setFaults(t, r, map[string]any{contract.ToolLoadSales: "invalid_output"})
	res, err := r.Call(contract.ToolLoadSales, sampleLoadArgs(t))
	if err != nil {
		t.Fatalf("Call 返回 error：%v", err)
	}
	if res.OK {
		t.Fatalf("非法输出不能被当作正常结果：%+v", res)
	}
	if !strings.Contains(res.Error, "非法输出") {
		t.Errorf("错误应说明非法输出：%s", res.Error)
	}
	if res.Data != nil {
		t.Errorf("非法输出不应带 Data：%v", res.Data)
	}
}

func TestRegistryFaultsAreScopedAndClearable(t *testing.T) {
	r := NewRegistry()
	setFaults(t, r, map[string]any{contract.ToolLoadSales: "error"})
	// 只影响列出的工具
	other, err := r.Call(contract.ToolParseMinutes, map[string]any{
		"paths": []string{filepath.Join(samplesDir(t), "周会纪要_2026W44.docx")},
	})
	if err != nil {
		t.Fatalf("Call 返回 error：%v", err)
	}
	if !other.OK {
		t.Fatalf("未被注入故障的工具不应失败：%s", other.Error)
	}
	// 清空后恢复
	cleared := setFaults(t, r, map[string]any{})
	if len(cleared.Tools) != 0 {
		t.Errorf("清空后 tools = %+v", cleared.Tools)
	}
	ok, err := r.Call(contract.ToolLoadSales, sampleLoadArgs(t))
	if err != nil {
		t.Fatalf("Call 返回 error：%v", err)
	}
	if !ok.OK {
		t.Fatalf("清空故障后应恢复正常：%s", ok.Error)
	}
}

func TestRegistryAdminSetFaultsNotFaultable(t *testing.T) {
	r := NewRegistry()
	applied := setFaults(t, r, map[string]any{contract.ToolAdminSetFaults: "error"})
	if _, ok := applied.Tools[contract.ToolAdminSetFaults]; ok {
		t.Errorf("admin_set_faults 自身不应被注入故障：%+v", applied.Tools)
	}
	// 再调用一次仍然可用
	cleared := setFaults(t, r, map[string]any{})
	if !cleared.OK {
		t.Errorf("admin_set_faults 被自己的故障开关影响了")
	}
}

func TestRegistryWebSearchNotOpen(t *testing.T) {
	r := NewRegistry()
	res, err := r.Call(contract.ToolWebSearch, map[string]any{"query": "青禾烘焙"})
	if err != nil {
		t.Fatalf("Call 返回 error：%v", err)
	}
	if res.OK || !strings.Contains(res.Error, "未开放") {
		t.Fatalf("web_search 应被拦下：%+v", res)
	}
}

func TestRegistryUnknownToolAndBadArgs(t *testing.T) {
	r := NewRegistry()
	unknown, err := r.Call("no_such_tool", map[string]any{})
	if err != nil {
		t.Fatalf("Call 返回 error：%v", err)
	}
	if unknown.OK || !strings.Contains(unknown.Error, "未知工具") {
		t.Fatalf("未知工具应报错：%+v", unknown)
	}
	bad, err := r.Call(contract.ToolLoadSales, map[string]any{"salesPath": 42})
	if err != nil {
		t.Fatalf("Call 返回 error：%v", err)
	}
	if bad.OK || !strings.Contains(bad.Error, "入参") {
		t.Fatalf("入参类型错误应报错：%+v", bad)
	}
}

func TestRegistryExecutionFailureRetryable(t *testing.T) {
	r := NewRegistry()
	res, err := r.Call(contract.ToolComputeMetrics, map[string]any{
		"datasetPath":      filepath.Join(t.TempDir(), "不存在.json"),
		"period":           map[string]any{"year": 2026, "week": 44},
		"anomalyThreshold": 0.1,
	})
	if err != nil {
		t.Fatalf("Call 返回 error：%v", err)
	}
	if res.OK {
		t.Fatalf("不存在的数据集应失败：%+v", res)
	}
	if !res.Retryable {
		t.Errorf("工具执行失败应可重试（与 TS 版 MCP 客户端一致）")
	}
}

func TestRegistryRenderInjected(t *testing.T) {
	var got contract.RenderReportInput
	r := NewRegistry(Options{Render: func(input contract.RenderReportInput) (contract.RenderReportOutput, error) {
		got = input
		return contract.RenderReportOutput{DocxPath: "out.docx", SHA256: "abc", EstimatedPages: 2, ChartCount: 2}, nil
	}})
	res, err := r.Call(contract.ToolRenderReportDocx, map[string]any{
		"report":         map[string]any{"title": "t", "sections": []any{}},
		"facts":          map[string]any{"company": map[string]any{"revenueCents": 1}},
		"outDir":         t.TempDir(),
		"idempotencyKey": "k",
	})
	if err != nil {
		t.Fatalf("Call 返回 error：%v", err)
	}
	if !res.OK {
		t.Fatalf("Call 失败：%s", res.Error)
	}
	if got.IdempotencyKey != "k" {
		t.Errorf("渲染器收到的入参 = %+v", got)
	}
	out, ok := res.Data.(contract.RenderReportOutput)
	if !ok || out.ChartCount != 2 {
		t.Fatalf("Data = %#v", res.Data)
	}
}

func TestRegistryRenderUnwired(t *testing.T) {
	r := NewRegistry()
	r.SetRenderer(nil)
	res, err := r.Call(contract.ToolRenderReportDocx, map[string]any{
		"report":         map[string]any{"title": "t", "sections": []any{}},
		"facts":          map[string]any{},
		"outDir":         t.TempDir(),
		"idempotencyKey": "k",
	})
	if err != nil {
		t.Fatalf("Call 返回 error：%v", err)
	}
	if res.OK || !strings.Contains(res.Error, "未接入") {
		t.Fatalf("渲染器未接入时应报错：%+v", res)
	}
}

// 默认构造器已经接上 internal/render，cmd 里直接 tools.NewRegistry() 即可。
func TestRegistryDefaultRendererWired(t *testing.T) {
	r := NewRegistry()
	if r.renderer() == nil {
		t.Fatal("NewRegistry() 默认应接入 internal/render")
	}
}

// 引擎会把工具失败的 retryable 记下来：故障与执行错误都必须可重试。
func TestRegistryFaultResultsAlwaysHaveError(t *testing.T) {
	for _, mode := range []contract.FaultMode{contract.FaultError, contract.FaultInvalidOutput} {
		r := NewRegistry()
		setFaults(t, r, map[string]any{contract.ToolLoadSales: string(mode)})
		res, err := r.Call(contract.ToolLoadSales, sampleLoadArgs(t))
		if err != nil {
			t.Fatalf("Call 返回 error：%v", err)
		}
		if res.OK || res.Error == "" {
			t.Errorf("%s 应返回非空 error", mode)
		}
		if !res.Retryable {
			t.Errorf("%s 应可重试", mode)
		}
	}
}
