package cli

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"digital-employee/internal/contract"
)

// caseEngine 测试用内存引擎：submit → awaiting_review，approve → delivered（带交付物）。
type caseEngine struct {
	mu          sync.Mutex
	tasks       map[string]*contract.TaskSnapshot
	events      map[string][]contract.TaskEvent
	deliverable bool
	failSubmit  bool
}

func newCaseEngine() *caseEngine {
	return &caseEngine{tasks: map[string]*contract.TaskSnapshot{}, events: map[string][]contract.TaskEvent{}}
}

func (e *caseEngine) emitLocked(id string, event contract.TaskEvent) {
	task := e.tasks[id]
	task.LastSeq++
	event.Seq = task.LastSeq
	event.TS = "2026-01-01T00:00:00.000Z"
	event.TaskID = id
	e.events[id] = append(e.events[id], event)
}

func (e *caseEngine) Create(input contract.CreateTaskInput) (*contract.TaskSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	id := fmt.Sprintf("task-%d", len(e.tasks)+1)
	form := input.Form
	if form == nil {
		form = map[string]any{}
	}
	snapshot := &contract.TaskSnapshot{ID: id, Employee: input.Employee, Form: form, State: contract.StateDraft}
	e.tasks[id] = snapshot
	e.emitLocked(id, contract.TaskEvent{Type: contract.EventTaskCreated, Employee: input.Employee, Form: form})
	copied := *snapshot
	return &copied, nil
}

func (e *caseEngine) Command(id contract.TaskID, command contract.TaskCommand, actor contract.Role) contract.CommandResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	task, ok := e.tasks[id]
	if !ok {
		return contract.CommandResult{OK: false, Error: contract.ErrNotFound, Message: "任务不存在"}
	}
	if task.State == contract.StateDraft && command.Type == contract.CmdSubmit {
		if e.failSubmit {
			return contract.CommandResult{OK: false, Error: contract.ErrIllegalTransition, Message: "剧本拒绝"}
		}
		e.emitLocked(id, contract.TaskEvent{Type: contract.EventStateChanged, From: task.State, To: contract.StateAwaitingReview, Trigger: contract.TriggerSubmit, Actor: actor})
		task.State = contract.StateAwaitingReview
		task.Pause = &contract.PauseReason{Code: contract.PauseAwaitingReview, Message: "等待审核"}
		copied := *task
		return contract.CommandResult{OK: true, Snapshot: &copied}
	}
	if task.State == contract.StateAwaitingReview && command.Type == contract.CmdApprove {
		e.emitLocked(id, contract.TaskEvent{Type: contract.EventStateChanged, From: task.State, To: contract.StateDelivered, Trigger: contract.TriggerApprove, Actor: actor})
		task.State = contract.StateDelivered
		task.Pause = nil
		if e.deliverable {
			task.Deliverables = []contract.DeliverableRef{{Kind: contract.DeliverableDocx, Path: "/tmp/report.docx", SHA256: "sha"}}
		}
		copied := *task
		return contract.CommandResult{OK: true, Snapshot: &copied}
	}
	return contract.CommandResult{OK: false, Error: contract.ErrIllegalTransition, Message: "非法流转"}
}

func (e *caseEngine) Get(id contract.TaskID) (*contract.TaskSnapshot, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	task, ok := e.tasks[id]
	if !ok {
		return nil, false
	}
	copied := *task
	return &copied, true
}

func (e *caseEngine) List() []contract.TaskSummary { return []contract.TaskSummary{} }

func (e *caseEngine) Events(id contract.TaskID, after int64) ([]contract.TaskEvent, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.tasks[id]; !ok {
		return nil, fmt.Errorf("任务不存在")
	}
	out := []contract.TaskEvent{}
	for _, event := range e.events[id] {
		if event.Seq > after {
			out = append(out, event)
		}
	}
	return out, nil
}

func (e *caseEngine) Subscribe(id contract.TaskID, listener func(contract.TaskEvent)) func() {
	return func() {}
}

func (e *caseEngine) WaitForSettled(id contract.TaskID) (*contract.TaskSnapshot, error) {
	snapshot, ok := e.Get(id)
	if !ok {
		return nil, fmt.Errorf("任务不存在")
	}
	return snapshot, nil
}

func (e *caseEngine) ResumeAll() error { return nil }

const sampleCaseJSON = `{
  "id": "T-01",
  "title": "测试用例",
  "employee": "weekly-report",
  "form": {"period": {"year": 2026, "week": 44}},
  "attachments": [{"name": "销售明细.xlsx", "path": "../data/销售明细.xlsx"}],
  "steps": [
    {"actor": "initiator", "command": {"type": "submit"}, "expect": {"state": "awaiting_review", "pauseCode": "awaiting_review"}},
    {"actor": "reviewer", "command": {"type": "approve"}, "expect": {"state": "delivered"}}
  ],
  "expect": {"state": "delivered", "checks": {"numbers": [{"path": "facts:company.deliveryShare", "expected": 31.8}]}}
}`

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseCaseAndRunCase(t *testing.T) {
	dir := t.TempDir()
	caseFile := writeTemp(t, dir, "case.json", sampleCaseJSON)
	def, err := ReadCaseFile(caseFile)
	if err != nil {
		t.Fatal(err)
	}
	if def.ID != "T-01" || len(def.Steps) != 2 || def.Expect == nil || def.Expect.Checks == nil {
		t.Fatalf("用例解析不对：%+v", def)
	}
	if len(def.Attachments) != 1 || !filepath.IsAbs(def.Attachments[0].SourcePath) {
		t.Fatalf("附件路径应按用例文件所在目录解析：%+v", def.Attachments)
	}
	if len(def.Expect.Checks.Numbers) != 1 || def.Expect.Checks.Numbers[0].Path != "facts:company.deliveryShare" {
		t.Fatalf("判定要点未解析：%+v", def.Expect.Checks)
	}

	engine := newCaseEngine()
	outDir := filepath.Join(dir, "out")
	result, err := RunCase(engine, def, RunCaseOptions{OutDir: outDir})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.FinalState != contract.StateDelivered {
		t.Fatalf("用例应通过：%+v", result)
	}
	if len(result.Steps) != 2 || result.Steps[0].State != string(contract.StateAwaitingReview) || result.Steps[1].State != string(contract.StateDelivered) {
		t.Fatalf("步骤结果不对：%+v", result.Steps)
	}
	for _, name := range []string{"snapshot.json", "events.jsonl", "result.json"} {
		if !fileExists(filepath.Join(outDir, name)) {
			t.Fatalf("缺少产物 %s", name)
		}
	}
	data, _ := os.ReadFile(filepath.Join(outDir, "result.json"))
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"caseId", "title", "passed", "taskId", "finalState", "steps", "deliverables", "failures"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("result.json 缺字段 %s：%s", key, data)
		}
	}
	if decoded["pauseCode"] != nil {
		t.Fatalf("delivered 时不应有 pauseCode：%s", data)
	}
	events, err := ReadTaskEvents(filepath.Join(outDir, "workspace"), result.TaskID)
	if err != nil {
		// run-case 的工作区不是 outDir（命令行里单独传），这里产物目录里的 events.jsonl 已足够
		events = nil
	}
	_ = events
	eventsData, _ := os.ReadFile(filepath.Join(outDir, "events.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(eventsData)), "\n")
	if len(lines) != 3 {
		t.Fatalf("事件条数不对：%d %s", len(lines), eventsData)
	}
	if !strings.Contains(lines[0], `"type":"task_created"`) {
		t.Fatalf("首条事件应为 task_created：%s", lines[0])
	}
}

func TestRunCaseFailureStopsAtFirstBadStep(t *testing.T) {
	dir := t.TempDir()
	caseFile := writeTemp(t, dir, "case.json", `{
	  "id": "T-02", "employee": "weekly-report",
	  "steps": [
	    {"actor": "initiator", "command": {"type": "submit"}, "expect": {"state": "delivered"}},
	    {"actor": "reviewer", "command": {"type": "approve"}}
	  ]
	}`)
	def, err := ReadCaseFile(caseFile)
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunCase(newCaseEngine(), def, RunCaseOptions{OutDir: filepath.Join(dir, "out")})
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed {
		t.Fatal("期望不满足时用例应失败")
	}
	if len(result.Steps) != 1 {
		t.Fatalf("应在第一步失败后停止：%+v", result.Steps)
	}
	if len(result.Failures) == 0 || !strings.Contains(result.Failures[0], "期望状态 delivered") {
		t.Fatalf("失败原因不对：%+v", result.Failures)
	}
}

func TestParseCaseValidation(t *testing.T) {
	dir := t.TempDir()
	cases := []string{
		`{"employee":"weekly-report","steps":[{"actor":"initiator","command":{"type":"submit"}}]}`,
		`{"id":"x","employee":"weekly-report","steps":[]}`,
		`{"id":"x","employee":"weekly-report","steps":[{"actor":"hacker","command":{"type":"submit"}}]}`,
		`{"id":"x","employee":"weekly-report","steps":[{"actor":"initiator","command":{"type":"explode"}}]}`,
		`{"id":"x","employee":"weekly-report","steps":[{"actor":"reviewer","command":{"type":"return"}}]}`,
		`{"id":"x","employee":"weekly-report","steps":[{"actor":"initiator","command":{"type":"submit"},"expect":{"checks":{}}}]}`,
	}
	for index, content := range cases {
		path := writeTemp(t, dir, fmt.Sprintf("bad-%d.json", index), content)
		if _, err := ReadCaseFile(path); err == nil {
			t.Fatalf("第 %d 个非法用例应报错：%s", index, content)
		}
	}
}

func TestParseChecksValidation(t *testing.T) {
	valid := `{"numbers":[{"path":"table:全公司/实收（元）","expected":573635},{"path":"facts:company.deliveryShare","expected":31.8,"tolerance":0.01}],
	  "mustContain":["正文"],"mustContainAny":[["说法 A","说法 B"]],"mustNotContain":["禁词"],"anomalies":["region:华东"],
	  "pause":{"code":"awaiting_review","messageContains":"审核"}}`
	checks, err := parseChecks(json.RawMessage(valid), "checks")
	if err != nil {
		t.Fatal(err)
	}
	if len(checks.Numbers) != 2 || checks.Numbers[0].Tolerance != nil || checks.Numbers[1].Tolerance == nil {
		t.Fatalf("数字核对解析不对：%+v", checks.Numbers)
	}
	if checks.Pause == nil || checks.Pause.Code != "awaiting_review" {
		t.Fatalf("暂停核对解析不对：%+v", checks.Pause)
	}

	invalid := []string{
		`{"unknown":1}`,
		`{"numbers":[{"path":"nope:1","expected":1}]}`,
		`{"numbers":[{"path":"table:无斜杠","expected":1}]}`,
		`{"numbers":[{"path":"facts:company.deliveryShare"}]}`,
		`{"numbers":[{"path":"facts:a.b","expected":1,"tolerance":0}]}`,
		`{"mustContain":[""]}`,
		`{"mustContainAny":[[]]}`,
		`{"pause":{"code":"nope"}}`,
	}
	for index, content := range invalid {
		if _, err := parseChecks(json.RawMessage(content), "checks"); err == nil {
			t.Fatalf("第 %d 个非法 checks 应报错：%s", index, content)
		}
	}
}

func TestResolveFactPathFormats(t *testing.T) {
	facts := map[string]any{
		"company": map[string]any{"deliveryShare": 0.318, "revenueCents": 57363500.0},
		"quality": map[string]any{"returns": map[string]any{"count": 11.0, "revenueCents": -26000.0}},
		"stores": []any{
			map[string]any{"code": "HZ01", "wow": -0.383},
			map[string]any{"code": "SH02", "wow": -0.179},
		},
	}
	cases := []struct {
		path     string
		expected float64
	}{
		{"company.deliveryShare", 31.8},
		{"company.revenueCents", 573635},
		{"quality.returns.count", 11},
		{"quality.returns.revenueCents", -260},
		{"stores.HZ01.wow", -38.3},
	}
	for _, item := range cases {
		value, problem := resolveFactPath(facts, item.path)
		if problem != "" {
			t.Fatalf("%s 解析失败：%s", item.path, problem)
		}
		if value != item.expected {
			t.Fatalf("%s 应为 %v，实际 %v", item.path, item.expected, value)
		}
	}
	if _, problem := resolveFactPath(facts, "stores.NOPE.wow"); problem == "" {
		t.Fatal("不存在的门店应报错")
	}
	if _, problem := resolveFactPath(nil, "company.deliveryShare"); problem == "" {
		t.Fatal("没有事实时应报错")
	}
}

// buildDocx 用最小 OOXML 构造一个 docx（正文段落 + 一张表）。
func buildDocx(t *testing.T, path string, paragraphs []string, table [][]string) {
	t.Helper()
	namespace := "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	var body strings.Builder
	for _, text := range paragraphs {
		body.WriteString(fmt.Sprintf(`<w:p><w:r><w:t xml:space="preserve">%s</w:t></w:r></w:p>`, text))
	}
	if len(table) > 0 {
		body.WriteString("<w:tbl>")
		for _, row := range table {
			body.WriteString("<w:tr>")
			for _, cell := range row {
				body.WriteString(fmt.Sprintf(`<w:tc><w:p><w:r><w:t>%s</w:t></w:r></w:p></w:tc>`, cell))
			}
			body.WriteString("</w:tr>")
		}
		body.WriteString("</w:tbl>")
	}
	document := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="%s"><w:body>%s</w:body></w:document>`, namespace, body.String())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	writer := zip.NewWriter(file)
	entry, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte(document)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDocxParsing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.docx")
	buildDocx(t, path, []string{"经营周报 第 44 周纪要", "以数据为准 生椰拿铁"}, [][]string{
		{"全公司", "实收（元）", "环比", "毛利率"},
		{"全公司", "573,635", "-9.3%", "61.1%"},
		{"华东", "197,618", "-12.0%", "89.8%"},
	})
	evidence, err := readDocx(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(evidence.Text, "第 44 周纪要") || !strings.Contains(evidence.Text, "以数据为准") {
		t.Fatalf("正文提取不对：%q", evidence.Text)
	}
	if len(evidence.Tables) != 1 || len(evidence.Tables[0]) != 3 {
		t.Fatalf("表格解析不对：%+v", evidence.Tables)
	}
	if evidence.Tables[0][0][1] != "实收（元）" || evidence.Tables[0][1][1] != "573,635" {
		t.Fatalf("表格内容不对：%+v", evidence.Tables[0])
	}
	if value, problem := resolveTableCell(evidence, "华东/环比"); problem != "" || value != -12.0 {
		t.Fatalf("表格单元解析不对：%v %s", value, problem)
	}
}

func TestCheckRunEndToEnd(t *testing.T) {
	dir := t.TempDir()
	resultDir := filepath.Join(dir, "result")
	docxPath := filepath.Join(dir, "report.docx")
	buildDocx(t, docxPath, []string{"第 44 周纪要 以数据为准"}, [][]string{
		{"全公司", "实收（元）", "环比"},
		{"全公司", "573,635", "-9.3%"},
	})
	facts := map[string]any{
		"period":    map[string]any{"year": 2026, "week": 44},
		"regions":   map[string]any{"华东": map[string]any{"wow": -0.12}},
		"company":   map[string]any{"deliveryShare": 0.318},
		"anomalies": []any{map[string]any{"id": "region:华东"}},
	}
	factsPath := filepath.Join(dir, "facts.json")
	data, _ := json.Marshal(facts)
	if err := os.WriteFile(factsPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	result := map[string]any{
		"caseId": "T", "finalState": "delivered",
		"deliverables": []any{map[string]any{"kind": "docx", "path": docxPath}},
	}
	resultData, _ := json.Marshal(result)
	if err := os.MkdirAll(resultDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(resultDir, "result.json"), resultData, 0o644)
	events := []string{
		fmt.Sprintf(`{"type":"step_completed","stepId":"compute_metrics","outputPath":%q,"seq":1}`, factsPath),
		`{"type":"state_changed","from":"running","to":"awaiting_review","pause":{"code":"awaiting_review","message":"等待审核"},"seq":2}`,
	}
	os.WriteFile(filepath.Join(resultDir, "events.jsonl"), []byte(strings.Join(events, "\n")+"\n"), 0o644)

	checks := CaseChecks{
		Numbers: []NumberCheck{
			{Path: "table:全公司/实收（元）", Expected: 573635},
			{Path: "facts:company.deliveryShare", Expected: 31.8},
		},
		MustContain: []string{"第 44 周纪要", "以数据为准"},
		Anomalies:   []string{"region:华东"},
		Pause:       &PauseCheck{Code: "awaiting_review", MessageContains: "等待"},
	}
	report, err := CheckRun(resultDir, checks)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed {
		t.Fatalf("核对应全部通过：%+v", report.Outcomes)
	}
	if len(report.Outcomes) != 6 {
		t.Fatalf("核对项数不对：%+v", report.Outcomes)
	}
}

func TestFindCaseFilesOrder(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"A-02.json", "A-01-rework.json", "A-01.json", "S-01.json", "README.md"} {
		writeTemp(t, dir, name, "{}")
	}
	files := findCaseFiles(filepath.Join(dir, "*.json"))
	names := []string{}
	for _, file := range files {
		names = append(names, caseNameOf(file))
	}
	expected := []string{"A-01", "A-01-rework", "A-02", "S-01"}
	if strings.Join(names, ",") != strings.Join(expected, ",") {
		t.Fatalf("用例排序不对：%v", names)
	}
	if len(findCaseFiles(filepath.Join(dir, "not-exists", "*.json"))) != 0 {
		t.Fatal("不存在的 glob 应返回空")
	}
}

func TestBuildSummary(t *testing.T) {
	outcomes := []CaseRunOutcome{
		{CaseID: "A-01", Title: "正常任务", Passed: true, Checks: []CheckOutcome{{Passed: true}}, ModelCalls: 5, DurationMS: 1200},
		{CaseID: "A-02", Title: "缺材料", Passed: false, Failures: []string{"核对未通过：numbers facts:x：实际 1，期望 2"}, ModelCalls: 3, DurationMS: 800},
	}
	summary := BuildSummary(outcomes, RunModeScripted, "out/cases", timeNow())
	for _, probe := range []string{"# 自测用例运行结果", "scripted（剧本式假模型", "| A-01 | 正常任务 | ✅ 通过 |", "| A-02 | 缺材料 | ❌ 未通过 |", "## 未通过明细", "核对未通过"} {
		if !strings.Contains(summary, probe) {
			t.Fatalf("汇总里缺少 %q：%s", probe, summary)
		}
	}
}

// timeNow 固定时间，避免汇总里的生成时间影响断言。
func timeNow() (at time.Time) { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

func TestReplayLines(t *testing.T) {
	dir := t.TempDir()
	taskID := "20260101000000-abcdef"
	eventsPath := filepath.Join(dir, "tasks", taskID, "events.jsonl")
	writeTemp(t, dir, filepath.Join("tasks", taskID, "events.jsonl"), strings.Join([]string{
		`{"type":"task_created","employee":"weekly-report","configHash":"c9c99c68","attachments":[],"seq":1,"ts":"2026-01-01T00:00:01.000Z","taskId":"` + taskID + `"}`,
		`{"type":"state_changed","from":"draft","to":"admission","trigger":"submit","actor":"initiator","seq":2,"ts":"2026-01-01T00:00:02.000Z","taskId":"` + taskID + `"}`,
	}, "\n")+"\n")
	if !fileExists(eventsPath) {
		t.Fatalf("测试事件日志未写入：%s", eventsPath)
	}
	lines, err := ReplayLines(dir, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || !strings.Contains(lines[0], "员工 weekly-report") || !strings.Contains(lines[1], "draft -> admission") {
		t.Fatalf("回放输出不对：%v", lines)
	}
	if _, err := ReplayLines(dir, "nope"); err == nil {
		t.Fatal("不存在的事件日志应报错")
	}
}
