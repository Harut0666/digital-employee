package tools

// 工具注册表：进程内直连的 contract.ToolClient 实现
// （移植自 packages/tools/src/server.ts，MCP 层换成普通 Go 接口）。
//
// 故障注入：timeout / error / invalid_output，可用 admin_set_faults 在运行时修改。
// 工具白名单由引擎侧控制（这里不拦越权）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"digital-employee/internal/contract"
)

// RenderFunc 转调 internal/render 的 render_report_docx。
// 由入口包注入，避免 internal/tools 反向依赖渲染实现。
type RenderFunc func(input contract.RenderReportInput) (contract.RenderReportOutput, error)

// Options 注册表选项。
type Options struct {
	// Render render_report_docx 的实现；为 nil 时调用该工具会报“未接入”。
	Render RenderFunc
	// Faults 创建时的故障配置。
	Faults contract.FaultConfig
	// FaultTimeoutMs timeout 故障模式下工具卡住多久（毫秒，默认 30000）。
	FaultTimeoutMs int
}

// Registry 工具注册表。
type Registry struct {
	mu        sync.RWMutex
	render    RenderFunc
	faults    map[string]contract.FaultMode
	faultTime time.Duration
	// chartFontPaths 场景 B 图表的中文字体候选路径；空则按系统默认找
	chartFontPaths []string
}

var _ contract.ToolClient = (*Registry)(nil)

// defaultFaultTimeout 与 TS 版默认值一致。
const defaultFaultTimeout = 30 * time.Second

// NewRegistry 创建工具注册表。render_report_docx 默认转调 internal/render，
// 可用 Options.Render 或 SetRenderer 覆盖（传 nil 表示不接入）。
func NewRegistry(opts ...Options) *Registry {
	r := &Registry{
		faults:    make(map[string]contract.FaultMode),
		faultTime: defaultFaultTimeout,
		render:    DefaultRenderer,
	}
	if len(opts) > 0 {
		o := opts[0]
		if o.Render != nil {
			r.render = o.Render
		}
		if o.FaultTimeoutMs > 0 {
			r.faultTime = time.Duration(o.FaultTimeoutMs) * time.Millisecond
		}
		r.setFaultModes(o.Faults.Tools)
	}
	return r
}

// SetRenderer 运行时注入 render_report_docx 的实现。
func (r *Registry) SetRenderer(fn RenderFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.render = fn
}

// SetFaults 替换故障配置。
func (r *Registry) SetFaults(cfg contract.FaultConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setFaultModes(cfg.Tools)
}

func (r *Registry) setFaultModes(modes map[string]contract.FaultMode) {
	next := make(map[string]contract.FaultMode, len(modes))
	for name, mode := range modes {
		// 管理工具自身不允许被注入故障，否则设置页就再也关不掉故障了
		if name == contract.ToolAdminSetFaults {
			continue
		}
		if validFaultMode(mode) {
			next[name] = mode
		}
	}
	r.faults = next
}

func validFaultMode(mode contract.FaultMode) bool {
	switch mode {
	case contract.FaultTimeout, contract.FaultError, contract.FaultInvalidOutput:
		return true
	}
	return false
}

// Faults 当前故障配置。
func (r *Registry) Faults() contract.FaultConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tools := make(map[string]contract.FaultMode, len(r.faults))
	for name, mode := range r.faults {
		tools[name] = mode
	}
	return contract.FaultConfig{Tools: tools}
}

func (r *Registry) faultOf(name string) (contract.FaultMode, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	mode, ok := r.faults[name]
	return mode, ok
}

func (r *Registry) renderer() RenderFunc {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.render
}

// decodeArgs 把调用方给的入参解码进具体结构体（引擎传 map，也可能传结构体）。
func decodeArgs(args any, out any) error {
	if args == nil {
		return nil
	}
	switch v := args.(type) {
	case json.RawMessage:
		if len(v) == 0 {
			return nil
		}
		return json.Unmarshal(v, out)
	case []byte:
		if len(v) == 0 {
			return nil
		}
		return json.Unmarshal(v, out)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// faultResult 按故障模式返回失败结果（与 TS 版一样，故障都是可重试的工具错误）。
func (r *Registry) faultResult(name string, mode contract.FaultMode) contract.ToolCallResult {
	switch mode {
	case contract.FaultError:
		return contract.ToolCallResult{
			OK:        false,
			Error:     fmt.Sprintf("工具 %s 执行失败（故障注入：报错）", name),
			Retryable: true,
		}
	case contract.FaultTimeout:
		time.Sleep(r.faultTime)
		return contract.ToolCallResult{
			OK:        false,
			Error:     fmt.Sprintf("工具 %s 调用超时（故障注入：超时）", name),
			Retryable: true,
		}
	case contract.FaultInvalidOutput:
		// 返回的数据不符合出参契约，引擎不应把它当作正常结果
		return contract.ToolCallResult{
			OK:        false,
			Error:     fmt.Sprintf("工具 %s 返回的数据不符合输出契约（故障注入：非法输出）", name),
			Retryable: true,
		}
	}
	return contract.ToolCallResult{OK: false, Error: fmt.Sprintf("工具 %s 执行失败（未知故障模式）", name), Retryable: true}
}

// materialError 工具读不了发起人上传的文件：这是材料问题，不是工具故障。
type materialError struct {
	path string
	err  error
}

func (e *materialError) Error() string { return e.err.Error() }
func (e *materialError) Unwrap() error { return e.err }

func failure(name string, err error) contract.ToolCallResult {
	result := contract.ToolCallResult{
		OK:        false,
		Error:     fmt.Sprintf("工具 %s 执行失败：%v", name, err),
		Retryable: true,
	}
	var material *materialError
	if errors.As(err, &material) {
		result.Retryable = false
		result.MaterialIssue = &contract.MaterialIssue{
			Path:    material.path,
			Message: fmt.Sprintf("文件「%s」无法读取，可能已损坏。请重新上传后继续。", filepath.Base(material.path)),
		}
	}
	return result
}

func decodeFailure(name string, err error) contract.ToolCallResult {
	return contract.ToolCallResult{
		OK:        false,
		Error:     fmt.Sprintf("工具 %s 的入参不正确：%v", name, err),
		Retryable: true,
	}
}

// Call 执行一次工具调用。
func (r *Registry) Call(name string, args any) (contract.ToolCallResult, error) {
	switch name {
	case contract.ToolAdminSetFaults:
		var input contract.AdminSetFaultsInput
		if err := decodeArgs(args, &input); err != nil {
			return decodeFailure(name, err), nil
		}
		r.mu.Lock()
		r.setFaultModes(input.Tools)
		tools := make(map[string]contract.FaultMode, len(r.faults))
		for tool, mode := range r.faults {
			tools[tool] = mode
		}
		r.mu.Unlock()
		return contract.ToolCallResult{OK: true, Data: contract.AdminSetFaultsOutput{OK: true, Tools: tools}}, nil

	case contract.ToolWebSearch:
		return contract.ToolCallResult{
			OK:        false,
			Error:     "web_search 未开放：该工具不对任何数字员工开放。",
			Retryable: true,
		}, nil

	case contract.ToolValidateSubmission:
		var input contract.ValidateSubmissionInput
		if err := decodeArgs(args, &input); err != nil {
			return decodeFailure(name, err), nil
		}
		if mode, ok := r.faultOf(name); ok {
			return r.faultResult(name, mode), nil
		}
		out, err := ValidateSubmission(input)
		if err != nil {
			return failure(name, err), nil
		}
		return contract.ToolCallResult{OK: true, Data: out}, nil

	case contract.ToolLoadSales:
		var input contract.LoadSalesInput
		if err := decodeArgs(args, &input); err != nil {
			return decodeFailure(name, err), nil
		}
		if mode, ok := r.faultOf(name); ok {
			return r.faultResult(name, mode), nil
		}
		out, err := LoadSales(input.SalesPath, input.OutDir)
		if err != nil {
			return failure(name, err), nil
		}
		return contract.ToolCallResult{OK: true, Data: out}, nil

	case contract.ToolComputeMetrics:
		var input contract.ComputeMetricsInput
		if err := decodeArgs(args, &input); err != nil {
			return decodeFailure(name, err), nil
		}
		if mode, ok := r.faultOf(name); ok {
			return r.faultResult(name, mode), nil
		}
		out, err := ComputeMetrics(input)
		if err != nil {
			return failure(name, err), nil
		}
		return contract.ToolCallResult{OK: true, Data: out}, nil

	case contract.ToolDetectContradictions:
		var input contract.DetectContradictionsInput
		if err := decodeArgs(args, &input); err != nil {
			return decodeFailure(name, err), nil
		}
		if mode, ok := r.faultOf(name); ok {
			return r.faultResult(name, mode), nil
		}
		out, err := DetectContradictions(input)
		if err != nil {
			return failure(name, err), nil
		}
		return contract.ToolCallResult{OK: true, Data: out}, nil

	case contract.ToolParseMinutes:
		var input contract.ParseMinutesInput
		if err := decodeArgs(args, &input); err != nil {
			return decodeFailure(name, err), nil
		}
		if mode, ok := r.faultOf(name); ok {
			return r.faultResult(name, mode), nil
		}
		out, err := ParseMinutes(input.Paths)
		if err != nil {
			return failure(name, err), nil
		}
		return contract.ToolCallResult{OK: true, Data: out}, nil

	case contract.ToolReadFile:
		var input contract.ReadFileInput
		if err := decodeArgs(args, &input); err != nil {
			return decodeFailure(name, err), nil
		}
		out, err := ReadFile(input.Path)
		if err != nil {
			return failure(name, err), nil
		}
		return contract.ToolCallResult{OK: true, Data: out}, nil

	case contract.ToolRenderReportDocx:
		var input contract.RenderReportInput
		if err := decodeArgs(args, &input); err != nil {
			return decodeFailure(name, err), nil
		}
		if mode, ok := r.faultOf(name); ok {
			return r.faultResult(name, mode), nil
		}
		render := r.renderer()
		if render == nil {
			return failure(name, fmt.Errorf("渲染器未接入（internal/render 尚未就绪）")), nil
		}
		out, err := render(input)
		if err != nil {
			return failure(name, err), nil
		}
		return contract.ToolCallResult{OK: true, Data: out}, nil
	}

	if result, handled := r.callLiterature(name, args); handled {
		return result, nil
	}

	return contract.ToolCallResult{
		OK:        false,
		Error:     fmt.Sprintf("未知工具：%s", name),
		Retryable: true,
	}, nil
}

// ListTools 返回全部工具描述（与 TS 版注册顺序一致）。
func (r *Registry) ListTools() ([]contract.ToolDescriptor, error) {
	return []contract.ToolDescriptor{
		{
			Name:        contract.ToolValidateSubmission,
			Description: "表单与附件校验：按固定顺序只报最根本的一个原因（超出范围、周次不完整、缺销售明细、缺维表、纪要缺失或周次不符）。",
			InputSchema: schemaValidateSubmission,
		},
		{
			Name:        contract.ToolLoadSales,
			Description: "读取销售明细（xlsx 或 csv），清洗后写成 JSON 数据集；文件名取内容哈希，已存在则直接返回。",
			InputSchema: schemaLoadSales,
		},
		{
			Name:        contract.ToolComputeMetrics,
			Description: "按周报口径计算指标与异常，输出 WeeklyFacts（金额为整数分，比例为小数）。",
			InputSchema: schemaComputeMetrics,
		},
		{
			Name:        contract.ToolDetectContradictions,
			Description: "判定纪要里的可核验说法与数据是否矛盾：持平、方向相反、幅度偏差三条规则，阈值由调用方给定；对象在事实里找不到的说法跳过并计数。",
			InputSchema: schemaDetectContradictions,
		},
		{
			Name:        contract.ToolParseMinutes,
			Description: "解析周会纪要：从正文标题识别周次，切分章节与段落，段落 ID 形如 W44-p07。",
			InputSchema: schemaParseMinutes,
		},
		{
			Name:        contract.ToolRenderReportDocx,
			Description: "生成 Word 周报（标题、章节、核心指标表、至少 2 张图）；同一幂等键的文件已存在则直接返回。",
			InputSchema: schemaRenderReport,
		},
		{
			Name:        contract.ToolReadFile,
			Description: "把工作区目录里的一个文件读成文本（csv、xlsx、docx、txt、md），超过 2 万字截断。",
			InputSchema: schemaReadFile,
		},
		{
			Name:        contract.ToolWebSearch,
			Description: "联网搜索。不开放给任何员工，仅用于演示越权拦截。",
			InputSchema: schemaWebSearch,
		},
		{
			Name:        contract.ToolAdminSetFaults,
			Description: "设置页下发的工具故障开关（管理工具，不开放给任何员工，只有服务端可以调用）。",
			InputSchema: schemaAdminSetFaults,
		},
	}, nil
}

// Close 释放资源（进程内直连，无资源需要释放）。
func (r *Registry) Close() error { return nil }
