// 判定要点（expect.checks）的解析与核对，格式见 samples/cases/README.md。
// 移植自 packages/cli/src/checks.ts。
//
// 输入是一次运行的产物目录：result.json、events.jsonl、交付的 Word。逐项给出通过或不通过及原因，
// 不抛错——写错的路径属于“这一项没核对通过”，不是程序异常。
package cli

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"digital-employee/internal/contract"
)

// NumberCheck 数字核对项。Path 指向事实（facts:）或周报正文的表格单元（table:）。
type NumberCheck struct {
	Path      string   `json:"path"`
	Expected  float64  `json:"expected"`
	Tolerance *float64 `json:"tolerance,omitempty"`
}

// PauseCheck 暂停核对项。
type PauseCheck struct {
	Code            string `json:"code"`
	MessageContains string `json:"messageContains,omitempty"`
}

// CaseChecks 判定要点。
type CaseChecks struct {
	Numbers        []NumberCheck `json:"numbers,omitempty"`
	MustContain    []string      `json:"mustContain,omitempty"`
	MustContainAny [][]string    `json:"mustContainAny,omitempty"`
	MustNotContain []string      `json:"mustNotContain,omitempty"`
	Anomalies      []string      `json:"anomalies,omitempty"`
	Pause          *PauseCheck   `json:"pause,omitempty"`
}

// CheckOutcome 一项核对的结果。
type CheckOutcome struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

// DocxEvidence 交付的 Word：正文纯文本（表格单元按单元格边界拼接）与按出现顺序解析出来的表格。
type DocxEvidence struct {
	Path   string
	Text   string
	Tables [][][]string
}

// PauseRecord 一次运行出现过的暂停。
type PauseRecord struct {
	Code    string
	Message string
}

// RunEvidence 一次运行的产物。
type RunEvidence struct {
	ResultDir  string
	CaseID     string
	FinalState contract.TaskState
	PauseCode  string
	Facts      map[string]any
	Docx       *DocxEvidence
	Pauses     []PauseRecord
	ModelCalls int
}

var pauseCodes = []string{
	"missing_sales", "missing_dimensions", "period_out_of_range", "period_incomplete",
	"minutes_missing", "minutes_week_mismatch", "invalid_input", "list_direction_mismatch",
	"insufficient_material", "material_issue", "tool_failed", "limit_exceeded", "qc_exhausted",
	"rework_needs_human", "awaiting_review",
}

func badChecks(where, problem string) error {
	return fmt.Errorf("%s：%s", where, problem)
}

func nonEmptyStrings(raw json.RawMessage, where, field string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var items []any
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, badChecks(where, field+" 必须是数组")
	}
	out := make([]string, 0, len(items))
	for index, item := range items {
		text, ok := item.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, badChecks(where, fmt.Sprintf("%s[%d] 必须是非空字符串", field, index))
		}
		out = append(out, text)
	}
	return out, nil
}

func parseNumberCheck(raw json.RawMessage, where string, index int) (NumberCheck, error) {
	at := fmt.Sprintf("%s.numbers[%d]", where, index)
	var item map[string]any
	if err := json.Unmarshal(raw, &item); err != nil {
		return NumberCheck{}, badChecks(at, "必须是对象")
	}
	path, ok := item["path"].(string)
	if !ok {
		return NumberCheck{}, badChecks(at, "缺少 path")
	}
	prefix, selector, _ := strings.Cut(path, ":")
	if prefix == "facts" {
		if selector == "" {
			return NumberCheck{}, badChecks(at, fmt.Sprintf("path 缺少事实路径：%q", path))
		}
	} else if prefix == "table" {
		slash := strings.Index(selector, "/")
		if slash <= 0 || slash == len(selector)-1 {
			return NumberCheck{}, badChecks(at, fmt.Sprintf("table: 的 path 要写成 table:<行>/<列>：%q", path))
		}
	} else {
		return NumberCheck{}, badChecks(at, fmt.Sprintf("path 必须以 facts: 或 table: 开头：%q", path))
	}
	expected, ok := item["expected"].(float64)
	if !ok || math.IsInf(expected, 0) || math.IsNaN(expected) {
		return NumberCheck{}, badChecks(at, "expected 必须是数字")
	}
	check := NumberCheck{Path: path, Expected: expected}
	if rawTolerance, present := item["tolerance"]; present {
		tolerance, ok := rawTolerance.(float64)
		if !ok || math.IsInf(tolerance, 0) || math.IsNaN(tolerance) || tolerance <= 0 {
			return NumberCheck{}, badChecks(at, "tolerance 必须是正数")
		}
		check.Tolerance = &tolerance
	}
	return check, nil
}

// parseChecks 校验并规范化 expect.checks；格式错误直接返回错误。
func parseChecks(raw json.RawMessage, where string) (CaseChecks, error) {
	checks := CaseChecks{}
	if len(raw) == 0 || string(raw) == "null" {
		return checks, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return checks, badChecks(where, "必须是对象")
	}
	known := map[string]bool{"numbers": true, "mustContain": true, "mustContainAny": true, "mustNotContain": true, "anomalies": true, "pause": true}
	unknown := []string{}
	for key := range fields {
		if !known[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		return checks, badChecks(where, fmt.Sprintf("有未知字段：%s（只支持 numbers / mustContain / mustContainAny / mustNotContain / anomalies / pause）", strings.Join(unknown, "、")))
	}
	if rawNumbers, present := fields["numbers"]; present {
		var items []json.RawMessage
		if err := json.Unmarshal(rawNumbers, &items); err != nil {
			return checks, badChecks(where, "numbers 必须是数组")
		}
		for index, item := range items {
			check, err := parseNumberCheck(item, where, index)
			if err != nil {
				return checks, err
			}
			checks.Numbers = append(checks.Numbers, check)
		}
	}
	var err error
	if checks.MustContain, err = nonEmptyStrings(fields["mustContain"], where, "mustContain"); err != nil {
		return checks, err
	}
	if checks.MustNotContain, err = nonEmptyStrings(fields["mustNotContain"], where, "mustNotContain"); err != nil {
		return checks, err
	}
	if checks.Anomalies, err = nonEmptyStrings(fields["anomalies"], where, "anomalies"); err != nil {
		return checks, err
	}
	if rawGroups, present := fields["mustContainAny"]; present {
		var groups []json.RawMessage
		if err := json.Unmarshal(rawGroups, &groups); err != nil {
			return checks, badChecks(where, "mustContainAny 必须是数组，数组里每一项是一组等价说法")
		}
		for index, group := range groups {
			at := fmt.Sprintf("mustContainAny[%d]", index)
			alternatives, err := nonEmptyStrings(group, where, at)
			if err != nil {
				return checks, err
			}
			if len(alternatives) == 0 {
				return checks, badChecks(where, at+" 不能是空数组")
			}
			checks.MustContainAny = append(checks.MustContainAny, alternatives)
		}
	}
	if rawPause, present := fields["pause"]; present {
		var item map[string]any
		if err := json.Unmarshal(rawPause, &item); err != nil {
			return checks, badChecks(where+".pause", "必须是对象")
		}
		code, ok := item["code"].(string)
		if !ok || !containsString(pauseCodes, code) {
			return checks, badChecks(where+".pause", "code 必须是 "+strings.Join(pauseCodes, " / ")+" 之一")
		}
		pause := &PauseCheck{Code: code}
		if rawMessage, present := item["messageContains"]; present {
			text, ok := rawMessage.(string)
			if !ok || text == "" {
				return checks, badChecks(where+".pause", "messageContains 必须是非空字符串")
			}
			pause.MessageContains = text
		}
		checks.Pause = pause
	}
	return checks, nil
}

// ---- 读结果目录 ----

type storedEvent struct {
	Type       string `json:"type"`
	StepID     string `json:"stepId"`
	OutputPath string `json:"outputPath"`
	Pause      *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"pause"`
}

type storedResult struct {
	CaseID       string `json:"caseId"`
	FinalState   string `json:"finalState"`
	PauseCode    string `json:"pauseCode"`
	Deliverables []struct {
		Kind string `json:"kind"`
		Path string `json:"path"`
	} `json:"deliverables"`
}

func readJSONFile(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func isRecordMap(value any) (map[string]any, bool) {
	m, ok := value.(map[string]any)
	return m, ok
}

func looksLikeFacts(value any) bool {
	m, ok := isRecordMap(value)
	if !ok {
		return false
	}
	_, company := isRecordMap(m["company"])
	_, regions := isRecordMap(m["regions"])
	_, period := isRecordMap(m["period"])
	return company && regions && period
}

func readFacts(events []storedEvent) map[string]any {
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Type != contract.EventStepCompleted || event.OutputPath == "" {
			continue
		}
		value, err := readJSONFile(event.OutputPath)
		if err != nil {
			continue
		}
		if facts, ok := isRecordMap(value); ok && looksLikeFacts(value) {
			return facts
		}
	}
	return nil
}

// ReadRunEvidence 读一次运行的产物目录；缺 result.json 说明目录不对，直接报错。
func ReadRunEvidence(resultDir string) (*RunEvidence, error) {
	resultPath := filepath.Join(resultDir, "result.json")
	resultData, err := os.ReadFile(resultPath)
	if err != nil {
		return nil, fmt.Errorf("结果目录里没有 result.json：%s", resultDir)
	}
	var result storedResult
	if err := json.Unmarshal(resultData, &result); err != nil {
		return nil, fmt.Errorf("result.json 不是合法的 JSON：%v", err)
	}
	events := []storedEvent{}
	if data, err := os.ReadFile(filepath.Join(resultDir, "events.jsonl")); err == nil {
		for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var event storedEvent
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				continue
			}
			events = append(events, event)
		}
	}
	pauses := []PauseRecord{}
	for _, event := range events {
		if event.Type == contract.EventStateChanged && event.Pause != nil && event.Pause.Code != "" {
			pauses = append(pauses, PauseRecord{Code: event.Pause.Code, Message: event.Pause.Message})
		}
	}
	evidence := &RunEvidence{
		ResultDir: resultDir, CaseID: result.CaseID,
		FinalState: contract.TaskState(result.FinalState), PauseCode: result.PauseCode,
		Facts: readFacts(events), Pauses: pauses,
	}
	for _, event := range events {
		if event.Type == contract.EventModelCalled {
			evidence.ModelCalls++
		}
	}
	for index := len(result.Deliverables) - 1; index >= 0; index-- {
		deliverable := result.Deliverables[index]
		if deliverable.Kind != string(contract.DeliverableDocx) {
			continue
		}
		if deliverable.Path == "" || !fileExists(deliverable.Path) {
			continue
		}
		docx, err := readDocx(deliverable.Path)
		if err != nil {
			continue
		}
		evidence.Docx = docx
		break
	}
	return evidence, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// ---- 核对 ----

const defaultTolerance = 0.005

func fmtNum(value float64) string {
	return fmt.Sprintf("%v", math.Round(value*100)/100)
}

func squash(text string) string {
	return strings.Join(strings.Fields(text), "")
}

func normalizeCell(text string) string {
	return strings.TrimSpace(squash(text))
}

// toDisplay 事实字段名决定展示口径（与槽位渲染一致）：分 → 元，比例 → 百分数。
func toDisplay(value float64, leaf string) (float64, bool) {
	format, ok := contract.SlotFormats[leaf]
	if !ok {
		return 0, false
	}
	switch format {
	case contract.SlotYuan:
		return value / 100, true
	case contract.SlotPercent1:
		return value * 100, true
	default:
		return value, true
	}
}

var arrayBusinessKeys = []string{"code", "key", "id", "name", "category", "store", "product"}

func resolveFactPath(facts map[string]any, dotted string) (float64, string) {
	if facts == nil {
		return 0, "这次运行没有事实数据（没跑到计算步骤）"
	}
	segments := strings.Split(dotted, ".")
	var current any = facts
	for index, segment := range segments {
		trail := strings.Join(segments[:index+1], ".")
		switch node := current.(type) {
		case []any:
			var found any
			for _, item := range node {
				record, ok := isRecordMap(item)
				if !ok {
					continue
				}
				matched := false
				for _, key := range arrayBusinessKeys {
					if text, ok := record[key].(string); ok && text == segment {
						matched = true
						break
					}
				}
				if matched {
					found = record
					break
				}
			}
			if found == nil {
				return 0, "事实里没有 " + trail
			}
			current = found
		case map[string]any:
			value, ok := node[segment]
			if !ok {
				return 0, "事实里没有 " + trail
			}
			current = value
		default:
			return 0, "事实里没有 " + trail
		}
	}
	number, ok := current.(float64)
	if !ok {
		return 0, fmt.Sprintf("事实 %s 不是数字", dotted)
	}
	leaf := segments[len(segments)-1]
	display, ok := toDisplay(number, leaf)
	if !ok {
		return 0, fmt.Sprintf("不知道字段 %s 的展示口径（不在 SLOT_FORMATS 里），请在 path 上换一个字段", leaf)
	}
	return display, ""
}

func parseCellNumber(text string) (float64, bool) {
	cleaned := strings.TrimSuffix(strings.ReplaceAll(strings.ReplaceAll(text, "，", ""), ",", ""), "%")
	cleaned = strings.TrimSuffix(strings.TrimSpace(cleaned), "％")
	var value float64
	if _, err := fmt.Sscanf(cleaned, "%g", &value); err != nil {
		return 0, false
	}
	// 必须整串都是数字，避免 "12abc" 被解析成 12
	if fmt.Sprintf("%v", value) != strings.TrimSpace(cleaned) {
		// 允许 12.0 vs 12 之类的差异：重新严格校验字符集
		for _, r := range strings.TrimSpace(cleaned) {
			if (r < '0' || r > '9') && r != '.' && r != '-' && r != '+' {
				return 0, false
			}
		}
	}
	return value, true
}

func resolveTableCell(docx *DocxEvidence, selector string) (float64, string) {
	if docx == nil {
		return 0, fmt.Sprintf("没有交付的 Word（docx），无法读取表格单元 %s", selector)
	}
	slash := strings.Index(selector, "/")
	if slash < 0 {
		return 0, fmt.Sprintf("表格单元路径不合法：%s", selector)
	}
	rowLabel := strings.TrimSpace(selector[:slash])
	columnLabel := strings.TrimSpace(selector[slash+1:])
	reasons := []string{}
	for index, table := range docx.Tables {
		header := []string{}
		if len(table) > 0 {
			header = table[0]
		}
		column := -1
		for cellIndex, cell := range header {
			if normalizeCell(cell) == normalizeCell(columnLabel) {
				column = cellIndex
				break
			}
		}
		if column < 0 {
			reasons = append(reasons, fmt.Sprintf("第 %d 个表没有「%s」列", index+1, columnLabel))
			continue
		}
		rowIndex := -1
		for rowCursor := 1; rowCursor < len(table); rowCursor++ {
			cells := table[rowCursor]
			first := ""
			if len(cells) > 0 {
				first = cells[0]
			}
			if normalizeCell(first) == normalizeCell(rowLabel) {
				rowIndex = rowCursor
				break
			}
		}
		if rowIndex < 0 {
			reasons = append(reasons, fmt.Sprintf("第 %d 个表没有「%s」行", index+1, rowLabel))
			continue
		}
		cell := ""
		if column < len(table[rowIndex]) {
			cell = table[rowIndex][column]
		}
		number, ok := parseCellNumber(cell)
		if !ok {
			empty := cell
			if empty == "" {
				empty = "空"
			}
			return 0, fmt.Sprintf("表格单元「%s / %s」不是数字：%s", rowLabel, columnLabel, empty)
		}
		return number, ""
	}
	if len(docx.Tables) == 0 {
		return 0, fmt.Sprintf("周报里没有表格，无法读取表格单元 %s", selector)
	}
	return 0, fmt.Sprintf("周报里找不到表格单元「%s」：%s", selector, strings.Join(reasons, "；"))
}

func resolveNumber(evidence *RunEvidence, path string) (float64, string) {
	prefix, selector, _ := strings.Cut(path, ":")
	if prefix == "facts" {
		return resolveFactPath(evidence.Facts, selector)
	}
	if prefix == "table" {
		return resolveTableCell(evidence.Docx, selector)
	}
	return 0, fmt.Sprintf("不认识的 path 前缀：%q", prefix)
}

func checkNumber(evidence *RunEvidence, check NumberCheck) CheckOutcome {
	value, problem := resolveNumber(evidence, check.Path)
	if problem != "" {
		return CheckOutcome{Kind: "numbers", Target: check.Path, Passed: false, Detail: problem}
	}
	tolerance := defaultTolerance
	if check.Tolerance != nil {
		tolerance = *check.Tolerance
	}
	diff := math.Abs(value - check.Expected)
	allowed := math.Abs(check.Expected) * tolerance
	if diff <= allowed+1e-9 {
		return CheckOutcome{Kind: "numbers", Target: check.Path, Passed: true, Detail: fmt.Sprintf("实际 %s（期望 %s）", fmtNum(value), fmtNum(check.Expected))}
	}
	relative := "偏差无法按比例衡量"
	if check.Expected != 0 {
		relative = fmt.Sprintf("偏差 %.2f%%", diff/math.Abs(check.Expected)*100)
	}
	return CheckOutcome{
		Kind: "numbers", Target: check.Path, Passed: false,
		Detail: fmt.Sprintf("实际 %s，期望 %s（%s，容差 %.1f%%）", fmtNum(value), fmtNum(check.Expected), relative, tolerance*100),
	}
}

func checkMustContain(evidence *RunEvidence, text string) CheckOutcome {
	if evidence.Docx == nil {
		return CheckOutcome{Kind: "mustContain", Target: text, Passed: false, Detail: "没有交付的 Word（docx），无法核对正文"}
	}
	hit := strings.Contains(squash(evidence.Docx.Text), squash(text))
	detail := "正文里有"
	if !hit {
		detail = fmt.Sprintf("正文里没有「%s」", text)
	}
	return CheckOutcome{Kind: "mustContain", Target: text, Passed: hit, Detail: detail}
}

func checkMustContainAny(evidence *RunEvidence, alternatives []string) CheckOutcome {
	target := strings.Join(alternatives, " / ")
	if evidence.Docx == nil {
		return CheckOutcome{Kind: "mustContainAny", Target: target, Passed: false, Detail: "没有交付的 Word（docx），无法核对正文"}
	}
	body := squash(evidence.Docx.Text)
	hit := ""
	for _, alternative := range alternatives {
		if strings.Contains(body, squash(alternative)) {
			hit = alternative
			break
		}
	}
	if hit == "" {
		return CheckOutcome{Kind: "mustContainAny", Target: target, Passed: false, Detail: fmt.Sprintf("正文里没有以下任一说法：「%s」", strings.Join(alternatives, "」「"))}
	}
	return CheckOutcome{Kind: "mustContainAny", Target: target, Passed: true, Detail: fmt.Sprintf("正文里有「%s」", hit)}
}

func checkMustNotContain(evidence *RunEvidence, text string) CheckOutcome {
	if evidence.Docx == nil {
		return CheckOutcome{Kind: "mustNotContain", Target: text, Passed: true, Detail: "没有交付的 Word，视为正文里没有"}
	}
	hit := strings.Contains(squash(evidence.Docx.Text), squash(text))
	detail := "正文里没有"
	if hit {
		detail = fmt.Sprintf("正文里出现了「%s」", text)
	}
	return CheckOutcome{Kind: "mustNotContain", Target: text, Passed: !hit, Detail: detail}
}

func factAnomalyIDs(facts map[string]any) []string {
	ids := []string{}
	if facts == nil {
		return ids
	}
	items, ok := facts["anomalies"].([]any)
	if !ok {
		return ids
	}
	for _, item := range items {
		if record, ok := isRecordMap(item); ok {
			if id, ok := record["id"].(string); ok {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func checkAnomaly(evidence *RunEvidence, id string) CheckOutcome {
	if evidence.Facts == nil {
		return CheckOutcome{Kind: "anomalies", Target: id, Passed: false, Detail: "这次运行没有事实数据（没跑到计算步骤）"}
	}
	ids := factAnomalyIDs(evidence.Facts)
	if containsString(ids, id) {
		return CheckOutcome{Kind: "anomalies", Target: id, Passed: true, Detail: "已判出"}
	}
	seen := strings.Join(ids, "、")
	if seen == "" {
		seen = "无"
	}
	return CheckOutcome{Kind: "anomalies", Target: id, Passed: false, Detail: fmt.Sprintf("没有判出 %s（事实里的异常：%s）", id, seen)}
}

func checkPause(evidence *RunEvidence, pause PauseCheck) CheckOutcome {
	found := ""
	for index := len(evidence.Pauses) - 1; index >= 0; index-- {
		if evidence.Pauses[index].Code == pause.Code {
			found = evidence.Pauses[index].Message
			break
		}
	}
	if found == "" {
		seen := []string{}
		for _, record := range evidence.Pauses {
			seen = append(seen, record.Code)
		}
		text := strings.Join(seen, "、")
		if text == "" {
			text = "无"
		}
		return CheckOutcome{Kind: "pause", Target: pause.Code, Passed: false, Detail: fmt.Sprintf("这次运行没有 %s 暂停（出现过：%s）", pause.Code, text)}
	}
	if pause.MessageContains != "" && !strings.Contains(squash(found), squash(pause.MessageContains)) {
		return CheckOutcome{Kind: "pause", Target: pause.Code, Passed: false, Detail: fmt.Sprintf("%s 的提示里没有「%s」，实际提示：%s", pause.Code, pause.MessageContains, found)}
	}
	return CheckOutcome{Kind: "pause", Target: pause.Code, Passed: true, Detail: fmt.Sprintf("出现过 %s 暂停", pause.Code)}
}

// EvaluateChecks 逐项核对，顺序与 checks 里各类的书写顺序一致。
func EvaluateChecks(evidence *RunEvidence, checks CaseChecks) []CheckOutcome {
	outcomes := []CheckOutcome{}
	for _, check := range checks.Numbers {
		outcomes = append(outcomes, checkNumber(evidence, check))
	}
	for _, text := range checks.MustContain {
		outcomes = append(outcomes, checkMustContain(evidence, text))
	}
	for _, alternatives := range checks.MustContainAny {
		outcomes = append(outcomes, checkMustContainAny(evidence, alternatives))
	}
	for _, text := range checks.MustNotContain {
		outcomes = append(outcomes, checkMustNotContain(evidence, text))
	}
	for _, id := range checks.Anomalies {
		outcomes = append(outcomes, checkAnomaly(evidence, id))
	}
	if checks.Pause != nil {
		outcomes = append(outcomes, checkPause(evidence, *checks.Pause))
	}
	return outcomes
}

// CheckReport 一次核对的结果。
type CheckReport struct {
	Passed   bool
	Outcomes []CheckOutcome
}

// CheckRun 读结果目录并核对；有未通过项时 Passed=false。
func CheckRun(resultDir string, checks CaseChecks) (CheckReport, error) {
	evidence, err := ReadRunEvidence(resultDir)
	if err != nil {
		return CheckReport{}, err
	}
	outcomes := EvaluateChecks(evidence, checks)
	passed := true
	for _, outcome := range outcomes {
		if !outcome.Passed {
			passed = false
		}
	}
	return CheckReport{Passed: passed, Outcomes: outcomes}, nil
}

// ReadChecksFromCaseFile 从用例文件（或单独的 {"checks": {...}} 文件）里读 checks。
func ReadChecksFromCaseFile(file string) (CaseChecks, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return CaseChecks{}, fmt.Errorf("找不到用例文件：%s", file)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return CaseChecks{}, fmt.Errorf("%s 不是合法的用例文件：顶层必须是对象", file)
	}
	checksRaw, present := raw["checks"]
	if !present {
		if expectRaw, ok := raw["expect"]; ok {
			var expect map[string]json.RawMessage
			if err := json.Unmarshal(expectRaw, &expect); err == nil {
				if value, ok := expect["checks"]; ok {
					checksRaw = value
					present = true
				}
			}
		}
	}
	if !present || len(checksRaw) == 0 {
		return CaseChecks{}, fmt.Errorf("%s 里没有 checks 段（expect.checks 或顶层 checks）", file)
	}
	return parseChecks(checksRaw, file+" 的 checks")
}
