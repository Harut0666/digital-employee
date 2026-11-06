// 员工配置加载（移植自 packages/core/src/config/index.ts 的 loadEmployeeConfigSync）。
//
// 本文件是服务端的默认加载器：internal/config（engine 负责）就绪后可由启动入口注入，
// 服务端只依赖 contract.EmployeeConfig。哈希算法与 TS 版逐字节一致：
// 递归列出目录内全部文件（字典序），对每个文件 update(相对路径, "\0", 文件字节, "\0") 后再 sha256。
package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"digital-employee/internal/contract"

	"gopkg.in/yaml.v3"
)

var employeeIDPattern = regexp.MustCompile(contract.EmployeeIDPattern)

// IsEmployeeID 员工 id 即目录名：只允许小写字母、数字和连字符。
func IsEmployeeID(id string) bool { return employeeIDPattern.MatchString(id) }

// listFiles 递归列出目录内的全部文件，返回相对路径（正斜杠），按字典序。
func listFiles(root string) ([]string, error) {
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
	sort.Strings(out)
	return out, nil
}

func hashDirectory(dir string) (string, error) {
	files, err := listFiles(dir)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		hash.Write([]byte(rel))
		hash.Write([]byte{0})
		hash.Write(data)
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func parseYAMLFile(dir, name string) (any, error) {
	path := filepath.Join(dir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("员工配置缺少文件 %s（目录 %s）", name, dir)
	}
	var value any
	if err := yaml.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("%s 不是合法的 YAML：%v", name, err)
	}
	return value, nil
}

func parseJSONText(data []byte, name string) (map[string]any, error) {
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("%s 不是合法的 JSON：%v", name, err)
	}
	return value, nil
}

// applyWorkflowDefaults 补齐契约 schema 里的默认值（qcSources / qcFixSteps）。
// TS 的 zod schema 在读入时注入这些默认值，Go 侧在加载与保存校验时同样补齐。
func applyWorkflowDefaults(workflow *contract.EmployeeWorkflow) {
	if workflow.QCSources.Draft == "" {
		workflow.QCSources.Draft = "write_report"
	}
	if workflow.QCSources.Minutes == "" {
		workflow.QCSources.Minutes = "parse_minutes"
	}
	if workflow.QCSources.Rendered == "" {
		workflow.QCSources.Rendered = "render_document"
	}
	if workflow.QCFixSteps.Causes == "" {
		workflow.QCFixSteps.Causes = "match_causes"
	}
}

// workflowConsistencyProblems 工作流配置的一致性检查；空数组表示没有问题。
func workflowConsistencyProblems(workflow contract.EmployeeWorkflow) []string {
	problems := []string{}
	ids := make([]string, 0, len(workflow.Steps))
	seen := map[string]bool{}
	for _, step := range workflow.Steps {
		if seen[step.ID] {
			problems = append(problems, "workflow.yaml 中的步骤 id 不唯一")
		}
		seen[step.ID] = true
		ids = append(ids, step.ID)
	}
	var admission *contract.StepConfig
	for index := range workflow.Steps {
		if workflow.Steps[index].ID == workflow.AdmissionStep {
			admission = &workflow.Steps[index]
			break
		}
	}
	if admission == nil {
		problems = append(problems, fmt.Sprintf("admissionStep %q 不在 workflow.yaml 的步骤中", workflow.AdmissionStep))
	} else if admission.Kind != contract.StepKindCompute {
		problems = append(problems, fmt.Sprintf("admissionStep %q 必须是计算步骤", admission.ID))
	}
	qcSteps := []contract.StepConfig{}
	for _, step := range workflow.Steps {
		if step.Kind == contract.StepKindQC {
			qcSteps = append(qcSteps, step)
		}
	}
	if len(qcSteps) > 1 {
		problems = append(problems, "workflow.yaml 最多只能有一个质检步骤")
	}
	if len(qcSteps) == 1 && len(workflow.Steps) > 0 && workflow.Steps[len(workflow.Steps)-1].ID != qcSteps[0].ID {
		problems = append(problems, "质检步骤必须是最后一个步骤")
	}
	if len(qcSteps) > 0 {
		fixSteps := []struct {
			key string
			id  string
		}{
			{"write", workflow.QCFixSteps.Write},
			{"causes", workflow.QCFixSteps.Causes},
		}
		if fixSteps[0].id == "" {
			fixSteps[0].id = workflow.QCSources.Draft
		}
		for _, fix := range fixSteps {
			if !seen[fix.id] {
				problems = append(problems, fmt.Sprintf("qcFixSteps.%s 解析为 %q，但不在 workflow.yaml 的步骤中；请显式配置 qcFixSteps.%s", fix.key, fix.id, fix.key))
			}
		}
	}
	return problems
}

func normalizeRef(ref string) string {
	return strings.TrimPrefix(strings.ReplaceAll(ref, "\\", "/"), "./")
}

// LoadEmployeeConfig 加载一个员工目录的完整配置（失败时返回中文说明）。
func LoadEmployeeConfig(dir string) (*contract.EmployeeConfig, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("员工配置目录不存在：%s", root)
	}
	workflowRaw, err := parseYAMLFile(root, "workflow.yaml")
	if err != nil {
		return nil, err
	}
	workflowData, _ := json.Marshal(workflowRaw)
	var workflow contract.EmployeeWorkflow
	if err := json.Unmarshal(workflowData, &workflow); err != nil {
		return nil, fmt.Errorf("workflow.yaml 不符合契约：%v", err)
	}
	if workflow.ID == "" {
		return nil, fmt.Errorf("workflow.yaml 不符合契约：缺少 id")
	}
	qcRaw, err := parseYAMLFile(root, "qc.yaml")
	if err != nil {
		return nil, err
	}
	qcData, _ := json.Marshal(qcRaw)
	var qc contract.QCConfig
	if err := json.Unmarshal(qcData, &qc); err != nil {
		return nil, fmt.Errorf("qc.yaml 不符合契约：%v", err)
	}
	applyWorkflowDefaults(&workflow)
	if problems := workflowConsistencyProblems(workflow); len(problems) > 0 {
		return nil, fmt.Errorf("%s", problems[0])
	}

	hash, err := hashDirectory(root)
	if err != nil {
		return nil, err
	}
	config := &contract.EmployeeConfig{
		ID:     workflow.ID,
		Dir:    root,
		Hash:   hash,
		Workflow: workflow,
		QC:     qc,
		Prompts:       map[string]string{},
		OutputSchemas: map[string]map[string]any{},
	}
	if data, err := os.ReadFile(filepath.Join(root, "SKILL.md")); err == nil {
		config.Skill = string(data)
	}
	if data, err := os.ReadFile(filepath.Join(root, "frontdesk.md")); err == nil {
		config.Frontdesk = string(data)
	}
	files, err := listFiles(root)
	if err != nil {
		return nil, err
	}
	for _, rel := range files {
		if strings.HasPrefix(rel, "prompts/") && strings.HasSuffix(rel, ".md") {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				return nil, err
			}
			config.Prompts[rel] = string(data)
		}
		if strings.HasPrefix(rel, "schemas/") && strings.HasSuffix(rel, ".json") {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				return nil, err
			}
			parsed, err := parseJSONText(data, rel)
			if err != nil {
				return nil, err
			}
			config.OutputSchemas[rel] = parsed
		}
	}
	for _, step := range workflow.Steps {
		if step.Kind != contract.StepKindJudge {
			continue
		}
		promptKey := normalizeRef(step.Prompt)
		if _, ok := config.Prompts[promptKey]; !ok {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(step.Prompt)))
			if err != nil {
				return nil, fmt.Errorf("员工配置缺少文件 %s（目录 %s）", step.Prompt, root)
			}
			config.Prompts[promptKey] = string(data)
		}
		schemaKey := normalizeRef(step.OutputSchema)
		if _, ok := config.OutputSchemas[schemaKey]; !ok {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(step.OutputSchema)))
			if err != nil {
				return nil, fmt.Errorf("员工配置缺少文件 %s（目录 %s）", step.OutputSchema, root)
			}
			parsed, err := parseJSONText(data, step.OutputSchema)
			if err != nil {
				return nil, err
			}
			config.OutputSchemas[schemaKey] = parsed
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "form.schema.json")); err == nil {
		parsed, err := parseJSONText(data, "form.schema.json")
		if err != nil {
			return nil, err
		}
		config.FormSchema = parsed
	} else {
		config.FormSchema = map[string]any{}
	}
	if data, err := os.ReadFile(filepath.Join(root, "form.ui.json")); err == nil {
		parsed, err := parseJSONText(data, "form.ui.json")
		if err != nil {
			return nil, err
		}
		config.FormUI = parsed
	}
	return config, nil
}

// employeeInfo GET /api/employees 的列表项。
type employeeInfo struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Description    string                 `json:"description"`
	FormSchema     map[string]any         `json:"formSchema"`
	FormUI         map[string]any         `json:"formUi,omitempty"`
	ReviewSections []contract.SectionSpec `json:"reviewSections,omitempty"`
}

// listEmployees 列出含 workflow.yaml 的合法员工目录（按名称排序）。
func (s *Server) listEmployees() ([]employeeInfo, error) {
	entries, err := os.ReadDir(s.opts.EmployeesDir)
	if err != nil {
		return []employeeInfo{}, nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || strings.HasPrefix(name, ".") || name == "node_modules" || !IsEmployeeID(name) {
			continue
		}
		if !isFile(filepath.Join(s.opts.EmployeesDir, name, "workflow.yaml")) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]employeeInfo, 0, len(names))
	for _, name := range names {
		config, err := s.opts.LoadEmployee(filepath.Join(s.opts.EmployeesDir, name))
		if err != nil {
			return nil, err
		}
		info := employeeInfo{
			ID:          config.ID,
			Name:        config.Workflow.Name,
			Description: config.Workflow.Description,
			FormSchema:  config.FormSchema,
			FormUI:      config.FormUI,
		}
		if info.FormSchema == nil {
			info.FormSchema = map[string]any{}
		}
		info.ReviewSections = config.QC.Builtin.Structure.Sections
		result = append(result, info)
	}
	return result, nil
}

func (s *Server) handleListEmployees(w http.ResponseWriter, r *http.Request) {
	employees, err := s.listEmployees()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, employees)
}
