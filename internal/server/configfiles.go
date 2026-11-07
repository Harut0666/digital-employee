// 员工配置文件清单、路径规则、markdown 中间层与保存前校验。
// 移植自 packages/core/src/config/{files,markdown,form,write}.ts。
//
// 白名单集中在这里：路由与版本快照用同一份清单，配置页只能读写成这些文件。
package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"digital-employee/internal/config"
	"digital-employee/internal/contract"

	"gopkg.in/yaml.v3"
)

// configRootFiles 员工目录根下的配置文件；可选文件不存在时不出现。
var configRootFiles = []string{"SKILL.md", "workflow.yaml", "qc.yaml", "form.schema.json", "form.ui.json", "frontdesk.md", config.ChecklistFile}

var configSubdirFiles = []struct {
	Dir string
	Ext string
}{
	{"prompts", ".md"},
	{"schemas", ".json"},
}

// formParamNames 程序已支持的表单参数（顺序与 TS 的 FORM_PARAM_NAMES 一致）。
var formParamNames = []string{"period", "allowMissingMinutes", "allowPartialPeriod"}

// qcTemplates 质检规则模板（6 种），顺序与 TS 的 QC_TEMPLATES 一致。
var qcTemplates = []string{
	contract.TemplateForbiddenTerms, contract.TemplateRequiredTerms,
	contract.TemplateSectionMustContain, contract.TemplateMaxItems,
	contract.TemplateMaxPages, contract.TemplateNumberRange,
}

// normalizeConfigPath 规范化配置页给出的文件路径：返回相对员工目录的路径（正斜杠），不合法返回 false。
// 只允许白名单内的文件，拒绝绝对路径与 .. 穿越。
func normalizeConfigPath(raw string) (string, bool) {
	normalized := strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/"), "./")
	if normalized == "" {
		return "", false
	}
	if strings.HasPrefix(normalized, "/") || regexp.MustCompile(`^[a-zA-Z]:`).MatchString(normalized) {
		return "", false
	}
	parts := strings.Split(normalized, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
	}
	if len(parts) == 1 {
		for _, name := range configRootFiles {
			if parts[0] == name {
				return parts[0], true
			}
		}
		return "", false
	}
	if len(parts) != 2 {
		return "", false
	}
	for _, sub := range configSubdirFiles {
		if sub.Dir == parts[0] && strings.HasSuffix(parts[1], sub.Ext) {
			return normalized, true
		}
	}
	return "", false
}

// listConfigFiles 员工目录里实际存在的配置文件，按“根文件在前、子目录按字典序”排列。
func listConfigFiles(dir string) []string {
	out := []string{}
	for _, name := range configRootFiles {
		if isFile(filepath.Join(dir, name)) {
			out = append(out, name)
		}
	}
	for _, sub := range configSubdirFiles {
		subdir := filepath.Join(dir, sub.Dir)
		entries, err := os.ReadDir(subdir)
		if err != nil {
			continue
		}
		names := []string{}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), sub.Ext) {
				continue
			}
			names = append(names, entry.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			out = append(out, sub.Dir+"/"+name)
		}
	}
	return out
}

// readConfigFiles 读取员工目录里全部配置文件的内容，键为相对路径。
func readConfigFiles(dir string) map[string]string {
	out := map[string]string{}
	for _, rel := range listConfigFiles(dir) {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		out[rel] = string(data)
	}
	return out
}

// readConfigFileText 读一个配置文件；不存在或路径不合法时返回 ok=false。
func readConfigFileText(dir, rawPath string) (string, string, bool) {
	rel, ok := normalizeConfigPath(rawPath)
	if !ok {
		return "", "", false
	}
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if !isFile(path) {
		return rel, "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return rel, "", false
	}
	return rel, string(data), true
}

// configFileInfo 一个配置文件在配置页里的样子：原文 + 解析结果。
type configFileInfo struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
	Parsed  any    `json:"parsed,omitempty"`
	Error   string `json:"error,omitempty"`
}

// describeConfigFiles 配置页用的文件清单；解析失败不抛错，只在 error 里说明。
func describeConfigFiles(dir string) []configFileInfo {
	files := []configFileInfo{}
	for _, rel := range listConfigFiles(dir) {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		kind := "markdown"
		if strings.HasSuffix(rel, ".yaml") {
			kind = "yaml"
		} else if strings.HasSuffix(rel, ".json") {
			kind = "json"
		}
		info := configFileInfo{Path: rel, Kind: kind, Content: string(data)}
		switch kind {
		case "yaml":
			var parsed any
			if err := yaml.Unmarshal(data, &parsed); err != nil {
				info.Error = "YAML 解析失败：" + err.Error()
			} else {
				info.Parsed = parsed
			}
		case "json":
			var parsed any
			if err := json.Unmarshal(data, &parsed); err != nil {
				info.Error = "JSON 解析失败：" + err.Error()
			} else {
				info.Parsed = parsed
			}
		}
		files = append(files, info)
	}
	return files
}

// ---------------------------------------------------------------- SKILL.md 三个区域

// skillSections SKILL.md 的三个可编辑区域（配置页“岗位说明”三个文本框）。
type skillSections struct {
	Duties   string `json:"duties"`
	Writing  string `json:"writing"`
	Cautions string `json:"cautions"`
}

// frontdeskDoc frontdesk.md 拆成的人设正文与只读工具。
type frontdeskDoc struct {
	Body  string   `json:"body"`
	Tools []string `json:"tools"`
}

const (
	dutiesHeading         = "岗位职责"
	cautionsHeading       = "注意事项"
	frontdeskToolsHeading = contract.FrontdeskToolsHeading
)

func headingName(line string) (string, bool) {
	trimmed := strings.TrimRight(strings.TrimSuffix(line, "\r"), " \t")
	if !strings.HasPrefix(trimmed, "## ") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")), true
}

func findHeading(lines []string, name string) int {
	for index, line := range lines {
		if heading, ok := headingName(line); ok && heading == name {
			return index
		}
	}
	return -1
}

func nextHeading(lines []string, from int) int {
	for index := from; index < len(lines); index++ {
		if _, ok := headingName(lines[index]); ok {
			return index
		}
	}
	return len(lines)
}

// splitSkill SKILL.md → 三个可编辑区域。
func splitSkill(markdown string) skillSections {
	lines := strings.Split(markdown, "\n")
	dutiesIndex := findHeading(lines, dutiesHeading)
	cautionsIndex := findHeading(lines, cautionsHeading)
	var duties string
	writingStart := 0
	if dutiesIndex >= 0 {
		dutiesEnd := nextHeading(lines, dutiesIndex+1)
		duties = strings.Join(lines[dutiesIndex+1:dutiesEnd], "\n")
		writingStart = dutiesEnd
	}
	writingEnd := len(lines)
	if cautionsIndex >= 0 && cautionsIndex >= writingStart {
		writingEnd = cautionsIndex
	}
	if writingStart > writingEnd {
		writingStart = writingEnd
	}
	writing := strings.Join(lines[writingStart:writingEnd], "\n")
	cautions := ""
	if cautionsIndex >= 0 {
		cautions = strings.Join(lines[cautionsIndex+1:], "\n")
	}
	return skillSections{Duties: duties, Writing: writing, Cautions: cautions}
}

func trimTrailingEmpty(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// joinSkill 三个文本框拼回 SKILL.md；原文没有的章节、而文本框里写了内容时追加到末尾。
func joinSkill(sections skillSections, original string) string {
	lines := strings.Split(original, "\n")
	dutiesIndex := findHeading(lines, dutiesHeading)
	cautionsIndex := findHeading(lines, cautionsHeading)
	out := []string{}
	if dutiesIndex >= 0 {
		out = append(out, lines[:dutiesIndex+1]...)
	}
	// 写作要求先放进去，再把岗位职责内容插到“## 岗位职责”之后
	out = append(out, strings.Split(sections.Writing, "\n")...)
	if dutiesIndex >= 0 {
		head := append([]string{}, out[:dutiesIndex+1]...)
		tail := append([]string{}, out[dutiesIndex+1:]...)
		head = append(head, strings.Split(sections.Duties, "\n")...)
		out = append(head, tail...)
	} else if strings.TrimSpace(sections.Duties) != "" {
		out = trimTrailingEmpty(out)
		out = append(out, "", "## "+dutiesHeading, "")
		out = append(out, strings.Split(strings.TrimRight(sections.Duties, " \t\n\r"), "\n")...)
		out = append(out, "")
	}
	if cautionsIndex >= 0 {
		out = append(out, lines[cautionsIndex])
		out = append(out, strings.Split(sections.Cautions, "\n")...)
	} else if strings.TrimSpace(sections.Cautions) != "" {
		out = trimTrailingEmpty(out)
		out = append(out, "", "## "+cautionsHeading, "")
		out = append(out, strings.Split(strings.TrimRight(sections.Cautions, " \t\n\r"), "\n")...)
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

// frontdeskToolLabels 前台 Agent 可勾选的只读工具写回 frontdesk.md 时的说明（工具表见 contract.FrontdeskReadonlyTools）。
var frontdeskToolLabels = map[string]string{
	contract.ToolQueryTask: "只读查询任务",
	contract.ToolKBSearch:  "搜索知识库索引",
	contract.ToolKBRead:    "读取知识库正文",
}

func defaultFrontdeskTools() []string {
	return append([]string{}, contract.FrontdeskDefaultTools...)
}

func isFrontdeskTool(name string) bool { return contract.IsFrontdeskReadonlyTool(name) }

func normalizeFrontdeskBody(lines []string) string {
	text := strings.TrimRight(strings.Join(lines, "\n"), " \t\n\r")
	if text == "" {
		return ""
	}
	return text + "\n"
}

func frontdeskToolLabel(name string) string {
	if label, ok := frontdeskToolLabels[name]; ok {
		return label
	}
	return name
}

// parseFrontdesk frontdesk.md → 人设正文 + 只读工具。工具勾选的读法和会话里的前台 Agent 共用 contract.FrontdeskTools。
func parseFrontdesk(text string) frontdeskDoc {
	lines := strings.Split(text, "\n")
	tools := contract.FrontdeskTools(text)
	start, end, ok := contract.FrontdeskToolsSection(lines)
	if !ok {
		return frontdeskDoc{Body: normalizeFrontdeskBody(lines), Tools: tools}
	}
	bodyLines := append([]string{}, lines[:start]...)
	bodyLines = append(bodyLines, lines[end:]...)
	return frontdeskDoc{Body: normalizeFrontdeskBody(bodyLines), Tools: tools}
}

// formatFrontdesk 人设 + 工具勾选拼回 frontdesk.md。
func formatFrontdesk(doc frontdeskDoc) string {
	body := strings.TrimRight(doc.Body, " \t\n\r")
	tools := doc.Tools
	if len(tools) == 0 {
		tools = defaultFrontdeskTools()
	}
	items := make([]string, 0, len(tools))
	for _, name := range tools {
		items = append(items, "- "+name+"："+frontdeskToolLabel(name))
	}
	prefix := ""
	if body != "" {
		prefix = body + "\n\n"
	}
	return prefix + "## " + frontdeskToolsHeading + "\n\n" + strings.Join(items, "\n") + "\n"
}

// ---------------------------------------------------------------- x-purpose 校验

// validateFormSchemaPurposes 保存前的用途校验（与 internal/config 共用同一份实现）。
func validateFormSchemaPurposes(formSchema any) []string {
	return config.ValidateFormSchemaPurposes(formSchema)
}

// applyWorkflowDefaults 补齐契约 schema 里的默认值（qcSources / qcFixSteps）。
// TS 的 zod schema 在读入时注入这些默认值，Go 侧在保存校验时同样补齐。
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

func isRecordValue(value any) (map[string]any, bool) {
	record, ok := value.(map[string]any)
	return record, ok
}

// ---------------------------------------------------------------- 保存前校验

// configFileInput 配置写入入参：原文（源文件视图）或结构化数据（表单视图）二选一。
type configFileInput struct {
	Content *string
	Data    any
	HasData bool
	// FormSchema 员工当前的表单 Schema：开工清单引用的字段要在里面
	FormSchema map[string]any
}

func failErrors(errors ...string) (string, []string) { return "", errors }

// prepareConfigText 校验一次配置写入并给出应当落盘的文本。
// currentText 是当前文件内容；文件不存在时为 nil。
func prepareConfigText(relPath string, input configFileInput, currentText *string) (string, []string) {
	path, ok := normalizeConfigPath(relPath)
	if !ok {
		return failErrors("不允许写入配置文件：" + relPath)
	}
	hasContent := input.Content != nil
	if hasContent && input.HasData {
		return failErrors("content 与 data 只能二选一")
	}
	if !hasContent && !input.HasData {
		return failErrors("缺少 content 或 data")
	}
	switch {
	case path == "workflow.yaml":
		return prepareWorkflow(input, currentText)
	case path == "qc.yaml":
		return prepareQC(input, currentText)
	case path == "form.schema.json" || path == "form.ui.json":
		return prepareFormSchema(input, path)
	case path == config.ChecklistFile:
		return prepareChecklist(input, currentText)
	case path == "SKILL.md":
		if hasContent {
			return *input.Content, nil
		}
		sections, ok := parseSkillSectionsData(input.Data)
		if !ok {
			return failErrors("SKILL.md 需要 duties / writing / cautions 三个字符串")
		}
		original := ""
		if currentText != nil {
			original = *currentText
		}
		return joinSkill(sections, original), nil
	case path == "frontdesk.md":
		if hasContent {
			return *input.Content, nil
		}
		doc, ok := parseFrontdeskData(input.Data)
		if !ok {
			return failErrors("frontdesk.md 需要 body（字符串）和 tools（字符串数组）")
		}
		bad := []string{}
		for _, tool := range doc.Tools {
			if !isFrontdeskTool(tool) {
				bad = append(bad, tool)
			}
		}
		if len(bad) > 0 {
			return failErrors("前台 Agent 不能开放这些工具：" + strings.Join(bad, "、") + "（只读工具由契约列出）")
		}
		return formatFrontdesk(doc), nil
	case strings.HasPrefix(path, "prompts/"):
		if !hasContent {
			return failErrors("提示词文件只接受原文 content")
		}
		return *input.Content, nil
	default:
		return prepareOutputSchema(input, path)
	}
}

func parseSkillSectionsData(data any) (skillSections, bool) {
	m, ok := isRecordValue(data)
	if !ok {
		return skillSections{}, false
	}
	duties, ok1 := m["duties"].(string)
	writing, ok2 := m["writing"].(string)
	cautions, ok3 := m["cautions"].(string)
	if !ok1 || !ok2 || !ok3 {
		return skillSections{}, false
	}
	return skillSections{Duties: duties, Writing: writing, Cautions: cautions}, true
}

func parseFrontdeskData(data any) (frontdeskDoc, bool) {
	m, ok := isRecordValue(data)
	if !ok {
		return frontdeskDoc{}, false
	}
	body, ok := m["body"].(string)
	if !ok {
		return frontdeskDoc{}, false
	}
	rawTools, ok := m["tools"].([]any)
	if !ok {
		return frontdeskDoc{}, false
	}
	tools := make([]string, 0, len(rawTools))
	for _, item := range rawTools {
		text, ok := item.(string)
		if !ok {
			return frontdeskDoc{}, false
		}
		tools = append(tools, text)
	}
	return frontdeskDoc{Body: body, Tools: tools}, true
}

func parseYAMLData(content string) (any, error) {
	var data any
	if err := yaml.Unmarshal([]byte(content), &data); err != nil {
		return nil, err
	}
	return data, nil
}

func validateWorkflow(data any) []string {
	raw, ok := isRecordValue(data)
	if !ok {
		return []string{"workflow.yaml：顶层必须是对象"}
	}
	errors := []string{}
	id, _ := raw["id"].(string)
	if !IsEmployeeID(id) {
		errors = append(errors, "workflow.yaml：员工 id 只允许小写字母、数字和连字符")
	}
	if _, ok := raw["name"].(string); !ok {
		errors = append(errors, "workflow.yaml：缺少 name")
	}
	if _, ok := raw["admissionStep"].(string); !ok {
		errors = append(errors, "workflow.yaml：缺少 admissionStep")
	}
	limits, _ := isRecordValue(raw["limits"])
	if limits == nil {
		errors = append(errors, "workflow.yaml：缺少 limits")
	} else {
		task, _ := isRecordValue(limits["task"])
		if task == nil {
			errors = append(errors, "workflow.yaml：缺少 limits.task")
		} else if calls, ok := jsonNumber(task["modelCalls"]); !ok || calls <= 0 {
			errors = append(errors, "workflow.yaml：limits.task.modelCalls 必须是正整数")
		}
		retry, _ := isRecordValue(limits["toolRetry"])
		if retry == nil {
			errors = append(errors, "workflow.yaml：缺少 limits.toolRetry")
		} else if attempts, ok := jsonNumber(retry["attempts"]); !ok || attempts <= 0 {
			errors = append(errors, "workflow.yaml：limits.toolRetry.attempts 必须是正整数")
		}
	}
	steps, ok := raw["steps"].([]any)
	if !ok || len(steps) == 0 {
		errors = append(errors, "workflow.yaml：steps 必须是非空数组")
	} else {
		for index, item := range steps {
			step, ok := isRecordValue(item)
			if !ok {
				errors = append(errors, fmt.Sprintf("workflow.yaml：steps[%d] 必须是对象", index))
				continue
			}
			kind, _ := step["kind"].(string)
			if kind != contract.StepKindCompute && kind != contract.StepKindJudge && kind != contract.StepKindQC {
				errors = append(errors, fmt.Sprintf("workflow.yaml：steps[%d].kind 必须是 compute / judge / qc", index))
			}
			if _, ok := step["id"].(string); !ok {
				errors = append(errors, fmt.Sprintf("workflow.yaml：steps[%d] 缺少 id", index))
			}
		}
	}
	if len(errors) == 0 {
		var workflow contract.EmployeeWorkflow
		data, _ := json.Marshal(raw)
		if err := json.Unmarshal(data, &workflow); err != nil {
			return []string{"workflow.yaml：" + err.Error()}
		}
		applyWorkflowDefaults(&workflow)
		errors = append(errors, config.ConsistencyProblems(&workflow)...)
	}
	return errors
}

func jsonNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		value, err := n.Float64()
		return value, err == nil
	default:
		return 0, false
	}
}

func validateQC(data any) []string {
	raw, ok := isRecordValue(data)
	if !ok {
		return []string{"qc.yaml：顶层必须是对象"}
	}
	errors := []string{}
	builtin, _ := isRecordValue(raw["builtin"])
	if builtin == nil {
		errors = append(errors, "qc.yaml：缺少 builtin")
	} else {
		structure, _ := isRecordValue(builtin["structure"])
		if structure == nil {
			errors = append(errors, "qc.yaml：缺少 builtin.structure")
		} else {
			if _, ok := structure["sections"].([]any); !ok {
				errors = append(errors, "qc.yaml：builtin.structure.sections 必须是数组")
			}
			if n, ok := jsonNumber(structure["summaryMaxSentences"]); !ok || n <= 0 {
				errors = append(errors, "qc.yaml：builtin.structure.summaryMaxSentences 必须是正整数")
			}
			if n, ok := jsonNumber(structure["nextWeekMaxItems"]); !ok || n <= 0 {
				errors = append(errors, "qc.yaml：builtin.structure.nextWeekMaxItems 必须是正整数")
			}
		}
		anomaly, _ := isRecordValue(builtin["anomaly_coverage"])
		if anomaly == nil {
			errors = append(errors, "qc.yaml：缺少 builtin.anomaly_coverage")
		} else if n, ok := jsonNumber(anomaly["threshold"]); !ok || n <= 0 {
			errors = append(errors, "qc.yaml：builtin.anomaly_coverage.threshold 必须是正数")
		}
		length, _ := isRecordValue(builtin["length"])
		if length == nil {
			errors = append(errors, "qc.yaml：缺少 builtin.length")
		} else if n, ok := jsonNumber(length["maxPages"]); !ok || n <= 0 {
			errors = append(errors, "qc.yaml：builtin.length.maxPages 必须是正数")
		}
	}
	contradiction, _ := isRecordValue(raw["contradiction"])
	if contradiction == nil {
		errors = append(errors, "qc.yaml：缺少 contradiction")
	}
	if custom, ok := raw["custom"]; ok {
		if _, ok := custom.([]any); !ok {
			errors = append(errors, "qc.yaml：custom 必须是数组")
		}
	}
	return errors
}

func prepareWorkflow(input configFileInput, currentText *string) (string, []string) {
	var data any
	if input.Content != nil {
		parsed, err := parseYAMLData(*input.Content)
		if err != nil {
			return failErrors("workflow.yaml 不是合法的 YAML：" + err.Error())
		}
		data = parsed
	} else {
		data = input.Data
	}
	if errors := validateWorkflow(data); len(errors) > 0 {
		return failErrors(errors...)
	}
	if input.Content != nil {
		return *input.Content, nil
	}
	if currentText != nil {
		if merged, ok := applyYAMLData(*currentText, data); ok {
			return merged, nil
		}
	}
	text, err := yaml.Marshal(data)
	if err != nil {
		return failErrors("workflow.yaml 序列化失败：" + err.Error())
	}
	return string(text), nil
}

// applyYAMLData 把结构化数据合进已有的 YAML 文档（对应 TS 的 applyYamlData）：
// 只替换变化的节点，尽量保留文件里的注释与格式，避免表单页保存把注释洗掉。
func applyYAMLData(currentText string, data any) (string, bool) {
	next, ok := isRecordValue(data)
	if !ok {
		return "", false
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(currentText), &document); err != nil {
		return "", false
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) == 0 {
		return "", false
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", false
	}
	if !mergeYAMLNode(root, next) {
		return "", false
	}
	encoded, err := yaml.Marshal(&document)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func sortedRecordKeys(record map[string]any) []string {
	keys := make([]string, 0, len(record))
	for key := range record {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func yamlNodeOf(value any) (*yaml.Node, bool) {
	encoded, err := yaml.Marshal(value)
	if err != nil {
		return nil, false
	}
	var document yaml.Node
	if err := yaml.Unmarshal(encoded, &document); err != nil {
		return nil, false
	}
	if document.Kind == yaml.DocumentNode && len(document.Content) > 0 {
		return document.Content[0], true
	}
	return &document, true
}

func mergeableYAMLNode(node *yaml.Node, data any) bool {
	switch node.Kind {
	case yaml.MappingNode:
		_, ok := isRecordValue(data)
		return ok
	case yaml.SequenceNode:
		_, ok := data.([]any)
		return ok
	}
	return false
}

// mergeYAMLNode 递归合并：映射按 key 合并/删除，序列长度一致且元素可合并时逐项合并，否则整体替换。
func mergeYAMLNode(node *yaml.Node, data any) bool {
	switch node.Kind {
	case yaml.MappingNode:
		record, ok := isRecordValue(data)
		if !ok {
			return false
		}
		// 先删掉数据里没有的键（保持其余键的顺序）
		kept := make([]*yaml.Node, 0, len(node.Content))
		for index := 0; index+1 < len(node.Content); index += 2 {
			if _, present := record[node.Content[index].Value]; present {
				kept = append(kept, node.Content[index], node.Content[index+1])
			}
		}
		node.Content = kept
		for _, key := range sortedRecordKeys(record) {
			value := record[key]
			existing := -1
			for index := 0; index+1 < len(node.Content); index += 2 {
				if node.Content[index].Value == key {
					existing = index
					break
				}
			}
			if existing >= 0 {
				child := node.Content[existing+1]
				if mergeableYAMLNode(child, value) {
					if !mergeYAMLNode(child, value) {
						return false
					}
					continue
				}
			}
			child, ok := yamlNodeOf(value)
			if !ok {
				return false
			}
			if existing >= 0 {
				node.Content[existing+1] = child
			} else {
				node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"}, child)
			}
		}
		return true
	case yaml.SequenceNode:
		items, ok := data.([]any)
		if !ok {
			return false
		}
		if len(items) == len(node.Content) {
			mergeable := true
			for index := range items {
				if !mergeableYAMLNode(node.Content[index], items[index]) {
					mergeable = false
					break
				}
			}
			if mergeable {
				for index := range items {
					if !mergeYAMLNode(node.Content[index], items[index]) {
						return false
					}
				}
				return true
			}
		}
		replacement, ok := yamlNodeOf(data)
		if !ok {
			return false
		}
		*node = *replacement
		return true
	default:
		replacement, ok := yamlNodeOf(data)
		if !ok {
			return false
		}
		*node = *replacement
		return true
	}
}

func prepareQC(input configFileInput, currentText *string) (string, []string) {
	var data any
	if input.Content != nil {
		parsed, err := parseYAMLData(*input.Content)
		if err != nil {
			return failErrors("qc.yaml 不是合法的 YAML：" + err.Error())
		}
		data = parsed
	} else {
		data = input.Data
	}
	if errors := validateQC(data); len(errors) > 0 {
		return failErrors(errors...)
	}
	if input.Content != nil {
		return *input.Content, nil
	}
	if currentText != nil {
		if merged, ok := applyYAMLData(*currentText, data); ok {
			return merged, nil
		}
	}
	text, err := yaml.Marshal(data)
	if err != nil {
		return failErrors("qc.yaml 序列化失败：" + err.Error())
	}
	return string(text), nil
}

func prepareFormSchema(input configFileInput, file string) (string, []string) {
	var data any
	if input.Content != nil {
		var parsed any
		if err := json.Unmarshal([]byte(*input.Content), &parsed); err != nil {
			return failErrors(file + " 不是合法的 JSON：" + err.Error())
		}
		data = parsed
	} else {
		data = input.Data
	}
	if _, ok := isRecordValue(data); !ok {
		return failErrors(file + " 必须是 JSON 对象")
	}
	if file == "form.schema.json" {
		if errors := validateFormSchemaPurposes(data); len(errors) > 0 {
			return failErrors(errors...)
		}
	}
	if input.Content != nil {
		return *input.Content, nil
	}
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return failErrors(file + " 序列化失败：" + err.Error())
	}
	return string(encoded) + "\n", nil
}

// prepareChecklist 开工清单：校验每一项的判定来源和引用的表单字段。表单视图保存时尽量保留注释。
func prepareChecklist(input configFileInput, currentText *string) (string, []string) {
	text := ""
	if input.Content != nil {
		text = *input.Content
	} else {
		encoded, err := yaml.Marshal(input.Data)
		if err != nil {
			return failErrors("checklist.yaml 序列化失败：" + err.Error())
		}
		text = string(encoded)
	}
	if _, problems := config.ParseChecklist(text, input.FormSchema); len(problems) > 0 {
		return failErrors(problems...)
	}
	if input.Content == nil && currentText != nil {
		if merged, ok := applyYAMLData(*currentText, input.Data); ok {
			return merged, nil
		}
	}
	return text, nil
}

func prepareOutputSchema(input configFileInput, file string) (string, []string) {
	var data any
	if input.Content != nil {
		var parsed any
		if err := json.Unmarshal([]byte(*input.Content), &parsed); err != nil {
			return failErrors(file + " 不是合法的 JSON：" + err.Error())
		}
		data = parsed
	} else {
		data = input.Data
	}
	if _, ok := isRecordValue(data); !ok {
		return failErrors(file + " 必须是 JSON 对象")
	}
	if input.Content != nil {
		return *input.Content, nil
	}
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return failErrors(file + " 序列化失败：" + err.Error())
	}
	return string(encoded) + "\n", nil
}
