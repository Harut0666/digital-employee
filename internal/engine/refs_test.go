package engine

import (
	"encoding/json"
	"testing"
)

func TestResolveInput(t *testing.T) {
	stepOutputs := map[string]any{
		"admission": map[string]any{
			"sales": map[string]any{"path": "D:/ws/销售明细.xlsx"},
			"minutes": []any{
				map[string]any{"path": "D:/ws/W44.docx"},
				map[string]any{"path": "D:/ws/W43.docx"},
			},
		},
		"compute": map[string]any{"company": map[string]any{"revenueCents": float64(57363487)}},
	}
	rendered := map[string]any{"write": map[string]any{"title": "周报"}}
	ctx := RefContext{
		Form:           map[string]any{"period": map[string]any{"year": float64(2026), "week": float64(44)}},
		TaskDir:        "D:/ws/tasks/1",
		Attachments:    []any{map[string]any{"name": "a.xlsx", "path": "D:/ws/a.xlsx"}},
		Feedback:       map[string]any{"comment": "太长", "sections": []any{"summary"}},
		QCConfig:       map[string]any{"builtin": map[string]any{"anomaly_coverage": map[string]any{"threshold": 0.1}}},
		StepOutput:     func(id string) any { return stepOutputs[id] },
		RenderedOutput: func(id string) any { return rendered[id] },
		IdempotencyKey: "\x00key",
	}
	input := map[string]any{
		"period":    "$form.period",
		"fromForm":  "$form.period.week",
		"matches":   "$form.period.year",
		"salesPath": "$steps.admission.sales.path",
		"paths":     "$steps.admission.minutes[*].path",
		"facts":     "$steps.compute",
		"title":     "$rendered.write.title",
		"dir":       "$task.dir",
		"files":     "$task.attachments",
		"comment":   "$task.feedback.comment",
		"key":       "$task.idempotencyKey",
		"threshold": "$config.builtin.anomaly_coverage.threshold",
		"missing":   "$steps.not_run.something",
		"plain":     "hello",
		"number":    float64(1),
		"nested":    map[string]any{"value": "$form.period"},
		"list":      []any{"$form.period", "x"},
	}

	resolved, ok := ResolveInput(input, ctx).(map[string]any)
	if !ok {
		t.Fatalf("解析结果类型不对：%T", ResolveInput(input, ctx))
	}
	if got := resolved["period"].(map[string]any)["week"]; got != float64(44) {
		t.Errorf("$form.period = %v", resolved["period"])
	}
	if resolved["fromForm"] != float64(44) {
		t.Errorf("$form.period.week = %v", resolved["fromForm"])
	}
	if resolved["salesPath"] != "D:/ws/销售明细.xlsx" {
		t.Errorf("$steps.admission.sales.path = %v", resolved["salesPath"])
	}
	paths, ok := resolved["paths"].([]any)
	if !ok || len(paths) != 2 || paths[0] != "D:/ws/W44.docx" || paths[1] != "D:/ws/W43.docx" {
		t.Errorf("$steps.admission.minutes[*].path = %v", resolved["paths"])
	}
	if resolved["title"] != "周报" {
		t.Errorf("$rendered.write.title = %v", resolved["title"])
	}
	if resolved["dir"] != "D:/ws/tasks/1" {
		t.Errorf("$task.dir = %v", resolved["dir"])
	}
	if resolved["comment"] != "太长" {
		t.Errorf("$task.feedback.comment = %v", resolved["comment"])
	}
	if resolved["key"] != "\x00key" {
		t.Errorf("$task.idempotencyKey = %v", resolved["key"])
	}
	if resolved["threshold"] != 0.1 {
		t.Errorf("$config.builtin.anomaly_coverage.threshold = %v", resolved["threshold"])
	}
	if resolved["missing"] != nil {
		t.Errorf("未执行的步骤引用应解析为 null，实际 %v", resolved["missing"])
	}
	if resolved["plain"] != "hello" || resolved["number"] != float64(1) {
		t.Errorf("非引用值应原样传入：%v / %v", resolved["plain"], resolved["number"])
	}
	nested, _ := resolved["nested"].(map[string]any)
	if nested["value"] == nil {
		t.Errorf("嵌套对象里的引用未解析：%v", resolved["nested"])
	}
	list, _ := resolved["list"].([]any)
	if len(list) != 2 || list[0] == nil || list[1] != "x" {
		t.Errorf("数组里的引用未解析：%v", resolved["list"])
	}

	// 引用不存在（含步骤尚未执行）解析为 null
	if value := ResolveInput("$task.nope", ctx); value != nil {
		t.Errorf("未知 task 字段应解析为 null：%v", value)
	}
	if value := ResolveInput("$nope.x", ctx); value != "$nope.x" {
		t.Errorf("不是引用的字符串应原样返回：%v", value)
	}
}

func TestReplaceString(t *testing.T) {
	placeholder := "\x00idempotencyKey:render"
	value := map[string]any{
		"key":   placeholder,
		"other": "keep",
		"list":  []any{placeholder, "x"},
		"deep":  map[string]any{"inner": placeholder, "not": placeholder + "x"},
	}
	replaced, _ := ReplaceString(value, placeholder, "task-1-render-r0-abc").(map[string]any)
	if replaced["key"] != "task-1-render-r0-abc" {
		t.Errorf("key = %v", replaced["key"])
	}
	if replaced["other"] != "keep" {
		t.Errorf("other = %v", replaced["other"])
	}
	list := replaced["list"].([]any)
	if list[0] != "task-1-render-r0-abc" || list[1] != "x" {
		t.Errorf("list = %v", list)
	}
	deep := replaced["deep"].(map[string]any)
	if deep["inner"] != "task-1-render-r0-abc" {
		t.Errorf("deep.inner = %v", deep["inner"])
	}
	if deep["not"] != placeholder+"x" {
		t.Errorf("只替换完全相等的字符串：%v", deep["not"])
	}
}

func TestArgsKeyIsStable(t *testing.T) {
	first := map[string]any{"b": float64(1), "a": map[string]any{"y": "2", "x": "1"}}
	second := map[string]any{"a": map[string]any{"x": "1", "y": "2"}, "b": float64(1)}
	if argsKey(first) != argsKey(second) {
		t.Fatalf("键顺序不同的同一入参应得到相同 key：%q vs %q", argsKey(first), argsKey(second))
	}
	if argsKey(nil) != "null" {
		t.Fatalf("argsKey(nil) = %q", argsKey(nil))
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(argsKey(first)), &decoded); err != nil {
		t.Fatalf("argsKey 不是合法 JSON：%v", err)
	}
}
