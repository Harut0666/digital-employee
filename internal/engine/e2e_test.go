// 端到端回归：剧本模式跑通 A-01（真实工具 + 真实渲染 + 剧本式模型），
// 并把事件类型序列与 TS 版 golden 对拍。
//
// 这个测试是“引擎侧 A-01 验收线”的守门人：
//   go test ./internal/engine -run TestRunCaseA01Scripted -v
package engine_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/engine"
	"digital-employee/internal/tools"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..")
}

func readJSON(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", path, err)
	}
	return raw
}

// newA01Engine 组装一个与命令行 run-case 等价的引擎：临时工作区 + 真实工具 + 剧本式模型。
func newA01Engine(t *testing.T) (*engine.Engine, *agent.ScriptedProvider) {
	t.Helper()
	root := repoRoot(t)
	script := agent.ModelScript{}
	if err := json.Unmarshal(readJSON(t, filepath.Join(root, "samples", "cases", "scripts", "A-01.json")), &script); err != nil {
		t.Fatalf("解析剧本失败：%v", err)
	}
	provider := agent.NewScriptedModelProvider(script, agent.ScriptedProviderOptions{})
	registry := tools.NewRegistry()
	t.Cleanup(func() { _ = registry.Close() })
	eng, err := engine.New(engine.Deps{
		WorkspaceDir: t.TempDir(),
		EmployeesDir: filepath.Join(root, "employees"),
		ToolClient:   registry,
		Model:        provider,
	})
	if err != nil {
		t.Fatalf("创建引擎失败：%v", err)
	}
	return eng, provider
}

func createA01Task(t *testing.T, eng *engine.Engine) *contract.TaskSnapshot {
	t.Helper()
	scenario := filepath.Join(repoRoot(t), "samples", "scenario-a")
	created, err := eng.Create(contract.CreateTaskInput{
		Employee: "weekly-report",
		Form: map[string]any{
			"period": map[string]any{"year": float64(2026), "week": float64(44)},
		},
		Attachments: []contract.AttachmentInput{
			{Name: "销售明细.xlsx", SourcePath: filepath.Join(scenario, "销售明细.xlsx")},
			{Name: "周会纪要_2026W44.docx", SourcePath: filepath.Join(scenario, "周会纪要_2026W44.docx")},
		},
		Actor: contract.RoleInitiator,
	})
	if err != nil {
		t.Fatalf("创建任务失败：%v", err)
	}
	if created.State != contract.StateDraft {
		t.Fatalf("新任务状态 = %s，want draft", created.State)
	}
	return created
}

func TestRunCaseA01Scripted(t *testing.T) {
	eng, provider := newA01Engine(t)
	task := createA01Task(t, eng)

	// ---- 发起人提交 ----
	result := eng.Command(task.ID, contract.TaskCommand{Type: contract.CmdSubmit}, contract.RoleInitiator)
	if !result.OK {
		t.Fatalf("submit 被拒绝：%s（%s）", result.Error, result.Message)
	}
	snapshot, err := eng.WaitForSettled(task.ID)
	if err != nil {
		t.Fatalf("等待任务停稳失败：%v", err)
	}
	if snapshot.State != contract.StateAwaitingReview {
		events, _ := eng.Events(task.ID, 0)
		t.Fatalf("状态 = %s，want awaiting_review；事件尾部：%s", snapshot.State, tailEventTypes(events, 6))
	}
	if snapshot.Pause == nil || snapshot.Pause.Code != contract.PauseAwaitingReview {
		t.Fatalf("暂停原因 = %+v，want awaiting_review", snapshot.Pause)
	}
	if len(snapshot.QC) != 7 {
		t.Fatalf("质检项 = %d，want 7：%+v", len(snapshot.QC), snapshot.QC)
	}
	for _, check := range snapshot.QC {
		if !check.Passed {
			t.Fatalf("质检项 %s 未通过：%v", check.ID, check.Issues)
		}
	}

	// ---- 审核人通过 ----
	result = eng.Command(task.ID, contract.TaskCommand{Type: contract.CmdApprove}, contract.RoleReviewer)
	if !result.OK {
		t.Fatalf("approve 被拒绝：%s（%s）", result.Error, result.Message)
	}
	snapshot, err = eng.WaitForSettled(task.ID)
	if err != nil {
		t.Fatalf("等待任务停稳失败：%v", err)
	}
	if snapshot.State != contract.StateDelivered {
		t.Fatalf("状态 = %s，want delivered", snapshot.State)
	}
	kinds := map[contract.DeliverableKind]bool{}
	for _, deliverable := range snapshot.Deliverables {
		kinds[deliverable.Kind] = true
		if _, statErr := os.Stat(deliverable.Path); statErr != nil {
			t.Fatalf("交付物 %s 不存在：%s", deliverable.Kind, deliverable.Path)
		}
	}
	if !kinds[contract.DeliverableDocx] || !kinds[contract.DeliverableQCRecord] {
		t.Fatalf("交付物种类不齐：%+v", snapshot.Deliverables)
	}

	// ---- 事件类型序列与 TS 版 golden 对拍 ----
	events, err := eng.Events(task.ID, 0)
	if err != nil {
		t.Fatalf("读取事件失败：%v", err)
	}
	for index, event := range events {
		if event.Seq != int64(index+1) {
			t.Fatalf("事件 seq 不连续：第 %d 条 seq=%d", index+1, event.Seq)
		}
	}
	gotTypes := eventTypes(events)
	wantTypes := goldenEventTypes(t)
	if strings.Join(gotTypes, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("事件类型序列与 golden 不一致：\n got (%d) %v\nwant (%d) %v",
			len(gotTypes), gotTypes, len(wantTypes), wantTypes)
	}

	// 剧本式模型必须按步骤消费剧本：A-01 只有 match_causes、write_report 两个判断步骤会调用模型
	if got := len(provider.Requests()); got != 2 {
		t.Fatalf("剧本式模型调用次数 = %d，want 2", got)
	}

	// 把本版事件与 golden 写出来，方便人工 diff（不写入仓库）
	dump := os.Getenv("DE_DUMP_EVENTS")
	if dump != "" {
		writeJSONLines(t, dump, events)
		t.Logf("事件已写入 %s", dump)
	}
}

func goldenEventTypes(t *testing.T) []string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "golden-a01", "events.normalized.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 golden 事件失败：%v", err)
	}
	types := []string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("解析 golden 事件失败：%v", err)
		}
		value, _ := event["type"].(string)
		types = append(types, value)
	}
	return types
}

func eventTypes(events []contract.TaskEvent) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func tailEventTypes(events []contract.TaskEvent, count int) string {
	start := 0
	if len(events) > count {
		start = len(events) - count
	}
	lines := make([]string, 0, count)
	for _, event := range events[start:] {
		lines = append(lines, event.Type+"/"+(event.StepID)+" "+(event.Error))
	}
	return strings.Join(lines, " | ")
}

func writeJSONLines(t *testing.T, path string, events []contract.TaskEvent) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("创建 %s 失败：%v", path, err)
	}
	defer file.Close()
	for _, event := range events {
		line, err := engine.MarshalEvent(event)
		if err != nil {
			t.Fatalf("序列化事件失败：%v", err)
		}
		if _, err := file.Write(append(line, '\n')); err != nil {
			t.Fatalf("写事件失败：%v", err)
		}
	}
}
