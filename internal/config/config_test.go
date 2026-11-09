package config

import (
	"path/filepath"
	"testing"

	"digital-employee/internal/contract"
)

// 场景 A 的员工配置哈希由 packages/core 的 loadEmployeeConfigSync 算出，Go 版必须与它逐字节一致。
// 相对 TS 版最初的基准（c9c99c68…），配置目录里这些文件变过，哈希随之改变：
// T25 workflow.yaml 加 deliverableName、T29 form.ui.json 给统计周期配周选择器（ui:field）、
// T31 每个步骤加业务文案（activity）、T30 加 checklist.yaml 并改 frontdesk.md、
// T44 按 T35 第 6 项把 interpret_feedback 的提示词与 Schema 措辞改成“超范围意见回到待审核”
// （最后这一处是有意不再与冻结的 TS 版逐字节相同）。
// T44 已按新哈希更新 golden-a01 的 events/snapshot/qc_record 与 parity 基准，见 golden-a01/README.md。
// 哈希按检出内容的原始字节算（仓库 .gitattributes 统一 LF）：用 CRLF 检出或改写这些文件会得到不同的哈希。
const goldenWeeklyReportHash = "c46572325f72603eceb2d09a125074a4a1d582d06ebba811f10276ee964b9547"

func testEmployeeDir(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "employees", "weekly-report")
}

func TestLoadWeeklyReportConfig(t *testing.T) {
	cfg, err := Load(testEmployeeDir(t))
	if err != nil {
		t.Fatalf("加载员工配置失败：%v", err)
	}
	if cfg.ID != "weekly-report" {
		t.Fatalf("id = %q", cfg.ID)
	}
	if cfg.Hash != goldenWeeklyReportHash {
		t.Fatalf("configHash 与 TS 版不一致：\n got %s\nwant %s", cfg.Hash, goldenWeeklyReportHash)
	}
	if len(cfg.Workflow.Steps) != 10 {
		t.Fatalf("步骤数 = %d，want 10", len(cfg.Workflow.Steps))
	}
	if cfg.Workflow.AdmissionStep != "admission" {
		t.Fatalf("admissionStep = %q", cfg.Workflow.AdmissionStep)
	}
	if cfg.Workflow.FactsStep != "compute_metrics" {
		t.Fatalf("factsStep = %q", cfg.Workflow.FactsStep)
	}
	if cfg.Workflow.QCSources.Draft != "write_report" ||
		cfg.Workflow.QCSources.Minutes != "parse_minutes" ||
		cfg.Workflow.QCSources.Rendered != "render_document" {
		t.Fatalf("qcSources 默认值不对：%+v", cfg.Workflow.QCSources)
	}
	if cfg.Workflow.QCFixSteps.Causes != "match_causes" {
		t.Fatalf("qcFixSteps.causes 默认值 = %q", cfg.Workflow.QCFixSteps.Causes)
	}
	if cfg.Workflow.Limits.Task.ModelCalls != 30 || cfg.Workflow.Limits.Task.DurationSec != 600 {
		t.Fatalf("任务预算不对：%+v", cfg.Workflow.Limits.Task)
	}
	if cfg.Workflow.Limits.QCAutofix == nil || *cfg.Workflow.Limits.QCAutofix != 3 {
		t.Fatalf("qcAutofix = %v", cfg.Workflow.Limits.QCAutofix)
	}
	if len(cfg.Prompts) != 3 {
		t.Fatalf("提示词数 = %d，want 3：%v", len(cfg.Prompts), keysOf(cfg.Prompts))
	}
	if len(cfg.OutputSchemas) != 3 {
		t.Fatalf("输出 Schema 数 = %d，want 3", len(cfg.OutputSchemas))
	}
	if cfg.Skill == "" {
		t.Fatal("SKILL.md 没有加载")
	}
	if cfg.FormSchema["title"] != "经营周报任务" {
		t.Fatalf("formSchema 没有加载：%v", cfg.FormSchema["title"])
	}

	// 判断步骤的模型引用缺省值
	for _, step := range cfg.Workflow.Steps {
		if step.Kind != contract.StepKindJudge {
			continue
		}
		if step.Model == nil || step.Model.Connection != "default" || step.Model.Model != "default" {
			t.Fatalf("步骤 %s 的 model 默认值不对：%+v", step.ID, step.Model)
		}
		if step.Limits == nil || step.Limits.Rounds == nil {
			t.Fatalf("步骤 %s 缺少 rounds", step.ID)
		}
	}

	// 质检配置
	qc := cfg.QC
	if !qc.Builtin.Structure.Enabled || !qc.Builtin.Traceability.Enabled ||
		!qc.Builtin.AnomalyCoverage.Enabled || !qc.Builtin.QuoteIntegrity.Enabled ||
		!qc.Builtin.ContradictionCoverage.Enabled || !qc.Builtin.ForbiddenTerms.Enabled ||
		!qc.Builtin.Length.Enabled || !qc.Builtin.FeedbackResponse.Enabled {
		t.Fatalf("内置质检应全部开启：%+v", qc.Builtin)
	}
	if len(qc.Builtin.Structure.Sections) != 6 {
		t.Fatalf("章节数 = %d，want 6", len(qc.Builtin.Structure.Sections))
	}
	if qc.Contradiction.FlatTolerance != 0.05 || qc.Contradiction.DirectionMin != 0.02 ||
		qc.Contradiction.MagnitudeTolerance != 0.10 {
		t.Fatalf("矛盾阈值不对：%+v", qc.Contradiction)
	}
	if len(qc.Custom) != 0 {
		t.Fatalf("自定义规则数 = %d", len(qc.Custom))
	}
}

func TestConsistencyProblems(t *testing.T) {
	base := contract.EmployeeWorkflow{
		ID:            "demo",
		Name:          "演示",
		Limits:        contract.WorkflowLimits{Task: contract.TaskLimits{ModelCalls: 1, DurationSec: 1}, ToolRetry: contract.ToolRetryLimits{Attempts: 1}},
		AdmissionStep: "admission",
		QCSources:     contract.QCSources{Draft: "write", Minutes: "minutes", Rendered: "render"},
		QCFixSteps:    contract.QCFixSteps{Write: "write", Causes: "causes"},
		Steps: []contract.StepConfig{
			{ID: "admission", Label: "准入", Kind: contract.StepKindCompute, Tool: "validate_submission"},
			{ID: "write", Label: "写作", Kind: contract.StepKindJudge},
			{ID: "causes", Label: "原因", Kind: contract.StepKindJudge},
			{ID: "minutes", Label: "纪要", Kind: contract.StepKindCompute, Tool: "parse_minutes"},
			{ID: "render", Label: "渲染", Kind: contract.StepKindCompute, Tool: "render_report_docx"},
			{ID: "qc", Label: "质检", Kind: contract.StepKindQC},
		},
	}
	if problems := ConsistencyProblems(&base); len(problems) != 0 {
		t.Fatalf("合法配置不应有问题：%v", problems)
	}

	duplicated := base
	duplicated.Steps = append(append([]contract.StepConfig{}, base.Steps...), contract.StepConfig{ID: "write", Label: "重复", Kind: contract.StepKindQC})
	if problems := ConsistencyProblems(&duplicated); len(problems) == 0 {
		t.Fatal("步骤 id 重复应报错")
	}

	notLast := base
	notLast.Steps = []contract.StepConfig{
		{ID: "admission", Label: "准入", Kind: contract.StepKindCompute, Tool: "validate_submission"},
		{ID: "qc", Label: "质检", Kind: contract.StepKindQC},
		{ID: "write", Label: "写作", Kind: contract.StepKindJudge},
	}
	found := false
	for _, problem := range ConsistencyProblems(&notLast) {
		if problem == "质检步骤必须是最后一个步骤" {
			found = true
		}
	}
	if !found {
		t.Fatal("质检步骤不在最后应报错")
	}

	badAdmission := base
	badAdmission.AdmissionStep = "nope"
	if problems := ConsistencyProblems(&badAdmission); len(problems) == 0 {
		t.Fatal("admissionStep 不存在应报错")
	}
}

// 开工清单的判定来源：旧的 upload 照旧可用，新的 material 与它等价（T41“已提供材料”）。
func TestChecklistSources(t *testing.T) {
	schema := map[string]any{"properties": map[string]any{"remark": map[string]any{"type": "string"}}}
	item := func(source string, accept []string) contract.ChecklistItem {
		return contract.ChecklistItem{ID: "sales", Label: "销售明细", Required: true, Source: source, Accept: accept}
	}
	for _, source := range []string{"upload", "material"} {
		if problems := ChecklistProblems([]contract.ChecklistItem{item(source, []string{".xlsx"})}, schema); len(problems) != 0 {
			t.Fatalf("source=%s 应合法：%v", source, problems)
		}
	}
	if problems := ChecklistProblems([]contract.ChecklistItem{item("material", nil)}, schema); len(problems) == 0 {
		t.Fatal("判定来源是已提供材料时 accept 至少要有一个扩展名")
	}
	if problems := ChecklistProblems([]contract.ChecklistItem{item("attachment", []string{".xlsx"})}, schema); len(problems) == 0 {
		t.Fatal("未知的 source 应报错")
	}
}

func TestCollectWritingRequirements(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"remark": map[string]any{"type": "string", "title": "备注", "x-purpose": "writing_requirement"},
			"empty":  map[string]any{"type": "string", "x-purpose": "writing_requirement"},
			"period": map[string]any{"type": "object", "x-purpose": "param:period"},
		},
	}
	form := map[string]any{"remark": "摘要先写区域情况"}
	got := CollectWritingRequirements(schema, form)
	if len(got) != 1 {
		t.Fatalf("写作要求数 = %d，want 1：%+v", len(got), got)
	}
	if got[0].Text != "备注：摘要先写区域情况" {
		t.Fatalf("text = %q", got[0].Text)
	}
	if got[0].Field != "remark" || got[0].Title != "备注" {
		t.Fatalf("字段不对：%+v", got[0])
	}

	errors := ValidateFormSchemaPurposes(map[string]any{
		"properties": map[string]any{
			"a": map[string]any{"x-purpose": "nope"},
			"b": map[string]any{"x-purpose": "param:unknown"},
		},
	})
	if len(errors) != 2 {
		t.Fatalf("用途校验错误数 = %d，want 2：%v", len(errors), errors)
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	return out
}
