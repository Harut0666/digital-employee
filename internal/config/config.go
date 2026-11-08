// Package config 加载员工配置（workflow.yaml / qc.yaml / form.schema.json / prompts / schemas）。
//
// 移植自 packages/core/src/config/{index,files,consistency}.ts。与 TS 版一致的约束：
//   - 配置目录里**全部**文件的内容参与 sha256（写入 task_created 的 configHash），
//     算法逐字节一致：按字典序遍历文件，依次 update(相对路径, "\0", 内容, "\0")；
//   - workflow.yaml / qc.yaml 缺省字段按契约补默认值（与 zod schema 的 default 相同）；
//   - 加载时做一致性检查，配置有问题直接报错（不会带病启动任务）。
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"digital-employee/internal/contract"

	"gopkg.in/yaml.v3"
)

var employeeIDPattern = regexp.MustCompile(contract.EmployeeIDPattern)

// IsEmployeeID 员工 id 即目录名：只允许小写字母、数字和连字符。
func IsEmployeeID(id string) bool { return employeeIDPattern.MatchString(id) }

// Load 加载一个员工配置目录。dir 可以是绝对路径或相对路径。
func Load(dir string) (*contract.EmployeeConfig, error) {
	return LoadEmployeeConfig(dir)
}

// LoadEmployeeConfig 与 Load 等价，名字对齐 TS 版的 loadEmployeeConfigSync。
func LoadEmployeeConfig(dir string) (*contract.EmployeeConfig, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("员工配置目录不存在：%s", root)
	}

	workflow, err := loadWorkflow(root)
	if err != nil {
		return nil, err
	}
	qc, err := loadQC(root)
	if err != nil {
		return nil, err
	}
	if problems := ConsistencyProblems(workflow); len(problems) > 0 {
		return nil, errors.New(problems[0])
	}

	prompts := map[string]string{}
	outputSchemas := map[string]map[string]any{}
	files, err := ListFiles(root)
	if err != nil {
		return nil, err
	}
	for _, rel := range files {
		switch {
		case strings.HasPrefix(rel, "prompts/") && strings.HasSuffix(rel, ".md"):
			text, err := readText(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				return nil, err
			}
			prompts[rel] = text
		case strings.HasPrefix(rel, "schemas/") && strings.HasSuffix(rel, ".json"):
			schema, err := readJSONObject(filepath.Join(root, filepath.FromSlash(rel)), rel)
			if err != nil {
				return nil, err
			}
			outputSchemas[rel] = schema
		}
	}
	for _, step := range workflow.Steps {
		if step.Kind != contract.StepKindJudge {
			continue
		}
		promptKey := NormalizeRef(step.Prompt)
		if _, ok := prompts[promptKey]; !ok {
			text, err := requireFile(root, step.Prompt)
			if err != nil {
				return nil, err
			}
			prompts[promptKey] = text
		}
		schemaKey := NormalizeRef(step.OutputSchema)
		if _, ok := outputSchemas[schemaKey]; !ok {
			text, err := requireFile(root, step.OutputSchema)
			if err != nil {
				return nil, err
			}
			schema, err := parseJSONObject(text, step.OutputSchema)
			if err != nil {
				return nil, err
			}
			outputSchemas[schemaKey] = schema
		}
	}

	hash, err := HashDirectory(root)
	if err != nil {
		return nil, err
	}

	cfg := &contract.EmployeeConfig{
		ID:            workflow.ID,
		Dir:           root,
		Hash:          hash,
		Workflow:      *workflow,
		QC:            *qc,
		FormSchema:    map[string]any{},
		Prompts:       prompts,
		OutputSchemas: outputSchemas,
	}
	if text, err := readText(filepath.Join(root, "SKILL.md")); err == nil {
		cfg.Skill = text
	}
	if text, err := readText(filepath.Join(root, "frontdesk.md")); err == nil {
		cfg.Frontdesk = text
	}
	formPath := filepath.Join(root, "form.schema.json")
	if _, err := os.Stat(formPath); err == nil {
		form, err := readJSONObject(formPath, "form.schema.json")
		if err != nil {
			return nil, err
		}
		cfg.FormSchema = form
	}
	formUIPath := filepath.Join(root, "form.ui.json")
	if _, err := os.Stat(formUIPath); err == nil {
		ui, err := readJSONObject(formUIPath, "form.ui.json")
		if err != nil {
			return nil, err
		}
		cfg.FormUI = ui
	}
	cfg.Checklist = []contract.ChecklistItem{}
	if text, err := readText(filepath.Join(root, ChecklistFile)); err == nil {
		checklist, problems := ParseChecklist(text, cfg.FormSchema)
		if len(problems) > 0 {
			return nil, errors.New(problems[0])
		}
		cfg.Checklist = checklist.Items
	}
	return cfg, nil
}

// ChecklistFile 开工清单的配置文件名。
const ChecklistFile = "checklist.yaml"

// ParseChecklist 解析并校验 checklist.yaml；formSchema 用来检查引用的表单字段是否存在。
func ParseChecklist(text string, formSchema map[string]any) (*contract.ChecklistConfig, []string) {
	var checklist contract.ChecklistConfig
	if err := yaml.Unmarshal([]byte(text), &checklist); err != nil {
		return nil, []string{"checklist.yaml 不是合法的 YAML：" + err.Error()}
	}
	if checklist.Items == nil {
		checklist.Items = []contract.ChecklistItem{}
	}
	return &checklist, ChecklistProblems(checklist.Items, formSchema)
}

// ChecklistProblems 开工清单的契约校验。
func ChecklistProblems(items []contract.ChecklistItem, formSchema map[string]any) []string {
	properties, _ := formSchema["properties"].(map[string]any)
	hasField := func(name string) bool {
		_, ok := properties[name]
		return ok
	}
	problems := []string{}
	seen := map[string]bool{}
	for index, item := range items {
		where := fmt.Sprintf("checklist.yaml：items[%d]", index)
		if item.ID == "" {
			problems = append(problems, where+" 缺少 id")
		} else if seen[item.ID] {
			problems = append(problems, fmt.Sprintf("checklist.yaml：id %q 重复", item.ID))
		}
		seen[item.ID] = true
		if strings.TrimSpace(item.Label) == "" {
			problems = append(problems, where+" 缺少 label")
		}
		switch item.Source {
		case contract.ChecklistSourceMaterial, contract.ChecklistSourceUpload:
			if len(item.Accept) == 0 {
				problems = append(problems, where+" 判定来源是已提供材料，accept 至少要有一个扩展名")
			}
			for _, extension := range item.Accept {
				if !strings.HasPrefix(extension, ".") {
					problems = append(problems, fmt.Sprintf("%s 的扩展名 %q 要以点开头，如 .xlsx", where, extension))
				}
			}
		case contract.ChecklistSourceField:
			if item.Field == "" {
				problems = append(problems, where+" 判定来源是表单字段，必须写 field")
			} else if !hasField(item.Field) {
				problems = append(problems, fmt.Sprintf("%s 的 field %q 不在表单字段里", where, item.Field))
			}
		default:
			problems = append(problems, where+" 的 source 必须是 material、upload（旧写法）或 field")
		}
		if item.SkipField != "" {
			if item.Required {
				problems = append(problems, where+" 是必需项，不能配 skipField")
			} else if !hasField(item.SkipField) {
				problems = append(problems, fmt.Sprintf("%s 的 skipField %q 不在表单字段里", where, item.SkipField))
			}
		}
	}
	return problems
}

// NormalizeRef 把配置里写的相对路径统一成配置表里的键（正斜杠，去掉开头的 ./）。
func NormalizeRef(ref string) string {
	return strings.TrimPrefix(strings.ReplaceAll(ref, "\\", "/"), "./")
}

// ListFiles 递归列出目录内的全部文件，返回相对路径（正斜杠），按字典序。
// 顺序与 TS 版的 listFiles 一致：同一层按名字排序，遇到子目录立即递归。
func ListFiles(root string) ([]string, error) {
	out := []string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// HashDirectory 计算整个配置目录的 sha256（与 TS 版逐字节一致）。
func HashDirectory(dir string) (string, error) {
	files, err := ListFiles(dir)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, rel := range files {
		content, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		hash.Write([]byte(rel))
		hash.Write([]byte{0})
		hash.Write(content)
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// ---------------------------------------------------------------- 读取与解析

func readText(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func requireFile(dir, name string) (string, error) {
	path := filepath.Join(dir, filepath.FromSlash(name))
	text, err := readText(path)
	if err != nil {
		return "", fmt.Errorf("员工配置缺少文件 %s（目录 %s）", name, dir)
	}
	return text, nil
}

func parseJSONObject(text, name string) (map[string]any, error) {
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return nil, fmt.Errorf("%s 不是合法的 JSON：%v", name, err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s 的顶层必须是 JSON 对象", name)
	}
	return object, nil
}

func readJSONObject(path, name string) (map[string]any, error) {
	text, err := readText(path)
	if err != nil {
		return nil, err
	}
	return parseJSONObject(text, name)
}

func loadWorkflow(dir string) (*contract.EmployeeWorkflow, error) {
	text, err := requireFile(dir, "workflow.yaml")
	if err != nil {
		return nil, err
	}
	var workflow contract.EmployeeWorkflow
	if err := yaml.Unmarshal([]byte(text), &workflow); err != nil {
		return nil, fmt.Errorf("workflow.yaml 不是合法的 YAML：%v", err)
	}
	applyWorkflowDefaults(&workflow)
	if problems := validateWorkflow(&workflow); len(problems) > 0 {
		return nil, fmt.Errorf("workflow.yaml 不符合契约：%s", strings.Join(problems, "；"))
	}
	return &workflow, nil
}

func loadQC(dir string) (*contract.QCConfig, error) {
	text, err := requireFile(dir, "qc.yaml")
	if err != nil {
		return nil, err
	}
	var qc contract.QCConfig
	if err := yaml.Unmarshal([]byte(text), &qc); err != nil {
		return nil, fmt.Errorf("qc.yaml 不是合法的 YAML：%v", err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(text), &raw); err != nil {
		return nil, fmt.Errorf("qc.yaml 不是合法的 YAML：%v", err)
	}
	applyQCDefaults(&qc, raw)
	return &qc, nil
}

// applyWorkflowDefaults 补 zod schema 里的缺省值。
func applyWorkflowDefaults(w *contract.EmployeeWorkflow) {
	if w.QCSources.Draft == "" {
		w.QCSources.Draft = "write_report"
	}
	if w.QCSources.Minutes == "" {
		w.QCSources.Minutes = "parse_minutes"
	}
	if w.QCSources.Rendered == "" {
		w.QCSources.Rendered = "render_document"
	}
	if w.QCFixSteps.Causes == "" {
		w.QCFixSteps.Causes = "match_causes"
	}
	for i := range w.Steps {
		step := &w.Steps[i]
		if step.Input == nil {
			step.Input = map[string]any{}
		}
		if step.Tools == nil {
			step.Tools = []string{}
		}
		if step.When == "" {
			step.When = "always"
		}
		if step.Kind == contract.StepKindJudge && step.Model == nil {
			step.Model = &contract.ModelRef{Connection: "default", Model: "default"}
		}
		if step.Kind == contract.StepKindJudge && step.Model != nil {
			if step.Model.Connection == "" {
				step.Model.Connection = "default"
			}
			if step.Model.Model == "" {
				step.Model.Model = "default"
			}
		}
	}
}

// applyQCDefaults 补 zod schema 里的缺省值：内置检查不可删除，缺省即开启（enabled 缺省 true）。
func applyQCDefaults(qc *contract.QCConfig, raw map[string]any) {
	if qc.Custom == nil {
		qc.Custom = []contract.CustomRule{}
	}
	if qc.Builtin.Structure.Sections == nil {
		qc.Builtin.Structure.Sections = []contract.SectionSpec{}
	}
	if qc.Builtin.ForbiddenTerms.Terms == nil {
		qc.Builtin.ForbiddenTerms.Terms = []string{}
	}
	builtin, _ := raw["builtin"].(map[string]any)
	enabled := func(section string) bool {
		node, ok := builtin[section].(map[string]any)
		if !ok {
			return true // 整节缺省：按开启处理
		}
		value, present := node["enabled"]
		if !present {
			return true
		}
		flag, ok := value.(bool)
		if !ok {
			return true // 手改坏值：读路径不因坏值失败
		}
		return flag
	}
	qc.Builtin.Structure.Enabled = enabled("structure")
	qc.Builtin.Traceability.Enabled = enabled("traceability")
	qc.Builtin.AnomalyCoverage.Enabled = enabled("anomaly_coverage")
	qc.Builtin.QuoteIntegrity.Enabled = enabled("quote_integrity")
	qc.Builtin.ContradictionCoverage.Enabled = enabled("contradiction_coverage")
	qc.Builtin.ForbiddenTerms.Enabled = enabled("forbidden_terms")
	qc.Builtin.Length.Enabled = enabled("length")
	qc.Builtin.FeedbackResponse.Enabled = enabled("feedback_response")
}

// validateWorkflow 做契约层面的必填/取值校验（对应 zod schema）。
func validateWorkflow(w *contract.EmployeeWorkflow) []string {
	problems := []string{}
	if !IsEmployeeID(w.ID) {
		problems = append(problems, fmt.Sprintf("员工 id %q 只允许小写字母、数字和连字符", w.ID))
	}
	if w.Name == "" {
		problems = append(problems, "缺少 name")
	}
	if len(w.Steps) == 0 {
		problems = append(problems, "steps 至少需要一个步骤")
	}
	if w.Limits.Task.ModelCalls <= 0 {
		problems = append(problems, "limits.task.modelCalls 必须是正整数")
	}
	if w.Limits.Task.DurationSec <= 0 {
		problems = append(problems, "limits.task.durationSec 必须是正整数")
	}
	if w.Limits.ToolRetry.Attempts <= 0 {
		problems = append(problems, "limits.toolRetry.attempts 必须是正整数")
	}
	if w.Limits.QCAutofix != nil && *w.Limits.QCAutofix < 0 {
		problems = append(problems, "limits.qcAutofix 必须是非负整数")
	}
	for i := range w.Steps {
		step := &w.Steps[i]
		where := fmt.Sprintf("steps[%d]", i)
		if step.ID == "" {
			problems = append(problems, where+" 缺少 id")
		}
		if step.Label == "" {
			problems = append(problems, where+" 缺少 label")
		}
		switch step.Kind {
		case contract.StepKindCompute:
			if step.Tool == "" {
				problems = append(problems, where+" 是计算步骤，必须有 tool")
			}
		case contract.StepKindJudge:
			if step.Prompt == "" {
				problems = append(problems, where+" 是判断步骤，必须有 prompt")
			}
			if step.OutputSchema == "" {
				problems = append(problems, where+" 是判断步骤，必须有 outputSchema")
			}
			if step.Limits == nil || step.Limits.Rounds == nil || *step.Limits.Rounds <= 0 {
				problems = append(problems, where+" 是判断步骤，limits.rounds 必须是正整数")
			} else if step.Limits.DurationSec != nil && *step.Limits.DurationSec <= 0 {
				problems = append(problems, where+" 的 limits.durationSec 必须是正数")
			}
			if step.When != "" && step.When != "always" && step.When != "rework" {
				problems = append(problems, where+" 的 when 必须是 always 或 rework")
			}
		case contract.StepKindQC:
			// 质检步骤没有额外字段
		default:
			problems = append(problems, fmt.Sprintf("%s 的 kind %q 必须是 compute / judge / qc", where, step.Kind))
		}
	}
	return problems
}

// ConsistencyProblems 工作流配置的一致性检查（docs/design.md §4、ADR-0001），
// 加载员工配置与保存 workflow.yaml 时共用。
func ConsistencyProblems(w *contract.EmployeeWorkflow) []string {
	problems := []string{}
	ids := make([]string, 0, len(w.Steps))
	seen := map[string]bool{}
	duplicated := false
	for _, step := range w.Steps {
		if seen[step.ID] {
			duplicated = true
		}
		seen[step.ID] = true
		ids = append(ids, step.ID)
	}
	if duplicated {
		problems = append(problems, "workflow.yaml 中的步骤 id 不唯一")
	}

	// 准入步骤可以不配：只有技能的员工（一个判断步骤）没有材料要校验
	var admission *contract.StepConfig
	for i := range w.Steps {
		if w.Steps[i].ID == w.AdmissionStep {
			admission = &w.Steps[i]
			break
		}
	}
	if w.AdmissionStep == "" {
		// 不配准入步骤
	} else if admission == nil {
		problems = append(problems, fmt.Sprintf("admissionStep %q 不在 workflow.yaml 的步骤中", w.AdmissionStep))
	} else if admission.Kind != contract.StepKindCompute {
		problems = append(problems, fmt.Sprintf("admissionStep %q 必须是计算步骤", admission.ID))
	}

	qcSteps := []contract.StepConfig{}
	for _, step := range w.Steps {
		if step.Kind == contract.StepKindQC {
			qcSteps = append(qcSteps, step)
		}
	}
	if len(qcSteps) > 1 {
		problems = append(problems, "workflow.yaml 最多只能有一个质检步骤")
	}
	if len(qcSteps) == 1 && w.Steps[len(w.Steps)-1].ID != qcSteps[0].ID {
		problems = append(problems, "质检步骤必须是最后一个步骤")
	}

	if len(qcSteps) > 0 {
		writeStep := w.QCFixSteps.Write
		if writeStep == "" {
			writeStep = w.QCSources.Draft
		}
		fixSteps := []struct{ key, id string }{
			{"write", writeStep},
			{"causes", w.QCFixSteps.Causes},
		}
		for _, fix := range fixSteps {
			if !seen[fix.id] {
				problems = append(problems, fmt.Sprintf(
					"qcFixSteps.%s 解析为 %q，但不在 workflow.yaml 的步骤中；请显式配置 qcFixSteps.%s",
					fix.key, fix.id, fix.key))
			}
		}
	}
	return problems
}
