package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"digital-employee/internal/contract"
)

func sampleEvent(seq int64, eventType string) contract.TaskEvent {
	return contract.TaskEvent{
		Seq:    seq,
		TS:     "2026-11-06T02:47:16.123Z",
		TaskID: "task-1",
		Type:   eventType,
	}
}

func TestEventLogAppendAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	for index := 1; index <= 3; index++ {
		if err := AppendEventLine(path, sampleEvent(int64(index), contract.EventStepStarted)); err != nil {
			t.Fatalf("追加事件失败：%v", err)
		}
	}
	events, err := ReadEventLog(path)
	if err != nil {
		t.Fatalf("读取事件失败：%v", err)
	}
	if len(events) != 3 {
		t.Fatalf("事件数 = %d，want 3", len(events))
	}
	for index, event := range events {
		if event.Seq != int64(index+1) {
			t.Fatalf("第 %d 条 seq = %d", index+1, event.Seq)
		}
		if event.TaskID != "task-1" {
			t.Fatalf("taskId 未落盘：%q", event.TaskID)
		}
	}

	// 文件不存在时读空
	missing, err := ReadEventLog(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil || len(missing) != 0 {
		t.Fatalf("不存在的日志应返回空：%v %v", missing, err)
	}
}

func TestEventLogDropsTornFinalLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := AppendEventLine(path, sampleEvent(1, contract.EventTaskCreated)); err != nil {
		t.Fatal(err)
	}
	// 模拟进程在写入中途被杀：最后一行只有半行
	half := `{"seq":2,"ts":"2026-11-06T02:47:16.123Z","taskId":"task-1","ty`
	if err := os.WriteFile(path, []byte(string(mustRead(t, path))+half), 0o644); err != nil {
		t.Fatal(err)
	}

	events, err := ReadEventLog(path)
	if err != nil {
		t.Fatalf("半行应被丢弃而不是报错：%v", err)
	}
	if len(events) != 1 {
		t.Fatalf("事件数 = %d，want 1", len(events))
	}
	// 文件应被整理成干净状态：追加不会接在半行后面
	if err := AppendEventLine(path, sampleEvent(2, contract.EventStateChanged)); err != nil {
		t.Fatal(err)
	}
	events, err = ReadEventLog(path)
	if err != nil {
		t.Fatalf("整理后的日志应可读：%v", err)
	}
	if len(events) != 2 {
		t.Fatalf("事件数 = %d，want 2：%s", len(events), mustRead(t, path))
	}
}

func TestEventLogCorruptionInMiddleIsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	content := `{"seq":1,"type":"task_created"}` + "\n" + `not-json` + "\n" + `{"seq":3,"type":"state_changed"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadEventLog(path)
	if err == nil || !strings.Contains(err.Error(), "事件日志损坏") {
		t.Fatalf("中间坏行应报损坏，实际 %v", err)
	}
	// 损坏时不得改动文件
	if got := mustRead(t, path); got != content {
		t.Fatalf("损坏的日志不应被改写：%q", got)
	}

	// salvage 只读，返回已解析的前缀与错误说明
	events, corruption := ReadEventLogSalvage(path)
	if len(events) != 1 || corruption == "" {
		t.Fatalf("salvage = %v / %q", events, corruption)
	}
}

func TestEventLogJSONFieldNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	event := contract.TaskEvent{
		Type: contract.EventToolResult, CallID: "call-1", OK: contract.BoolPtr(false),
		Error: "工具失败", Seq: 1, TS: "2026-11-06T02:47:16.123Z", TaskID: "task-1",
	}
	if err := AppendEventLine(path, event); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(mustRead(t, path))
	// 字段名必须与 TS 版一致（前端与回放都按名字读）
	for _, want := range []string{`"seq":1`, `"ts":"2026-11-06T02:47:16.123Z"`, `"taskId":"task-1"`,
		`"type":"tool_result"`, `"callId":"call-1"`, `"ok":false`, `"error":"工具失败"`} {
		if !strings.Contains(line, want) {
			t.Fatalf("事件行缺少 %s：%s", want, line)
		}
	}
	// 指针字段的 false / 0 不能被 omitempty 吞掉
	if strings.Contains(line, `"from":""`) {
		t.Fatalf("空字段应省略：%s", line)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", path, err)
	}
	return string(raw)
}
