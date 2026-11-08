package app

// 迁移的内部检查：复制来的任务和会话日志要能在真实引擎里读出来（发起事件、会话头里补上了工作区）。

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"digital-employee/internal/config"
	"digital-employee/internal/contract"
	"digital-employee/internal/engine"
	"digital-employee/internal/tools"
)

// writeEmployeeFixture 写一份最小的员工配置（含 workflow.yaml），引擎建任务时会校验它。
func writeEmployeeFixture(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"workflow.yaml": "id: demo\nname: 演示员工\nlimits:\n  task: { modelCalls: 10, durationSec: 600 }\n  toolRetry: { attempts: 1, backoffMs: [0] }\nsteps:\n  - { id: only, label: 唯一, kind: judge, prompt: prompts/p.md, outputSchema: s.json, tools: [], limits: { rounds: 1 } }\n",
		"prompts/p.md":  "写",
		"s.json":        `{"type":"object"}`,
		"qc.yaml":       "custom: []\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// 任务列表里的任务需要能读出员工配置：迁移来的旧任务照旧可读。
func TestMigratedLogsAreReadableByTheRealEngine(t *testing.T) {
	t.Setenv("DE_WORKSPACE", "")
	appData := t.TempDir()
	legacy := filepath.Join(appData, "digital-employee")
	writeEmployeeFixture(t, filepath.Join(legacy, "employees", "demo"))
	taskDir := filepath.Join(legacy, "tasks", "old-task")
	if err := os.MkdirAll(filepath.Join(taskDir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeEmployeeFixture(t, filepath.Join(taskDir, "config"))
	events := `{"seq":1,"ts":"2026-10-01T00:00:00.000Z","taskId":"old-task","type":"task_created","employee":"demo","configHash":"old","form":{},"attachments":[]}
{"seq":2,"ts":"2026-10-01T00:00:01.000Z","taskId":"old-task","type":"state_changed","from":"draft","to":"admission","trigger":"submit","actor":"initiator"}
`
	if err := os.WriteFile(filepath.Join(taskDir, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	// 一份损坏的日志：迁移不能碰它，引擎照常启动
	broken := filepath.Join(legacy, "tasks", "broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "events.jsonl"), []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(legacy, "sessions", "s-old")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := `{"id":"s1","seq":1,"ts":"2026-10-01T00:00:00.000Z","kind":"created"}
{"id":"s2","seq":2,"ts":"2026-10-01T00:00:01.000Z","kind":"user","role":"initiator","text":"帮我出第 44 周的周报"}
`
	if err := os.WriteFile(filepath.Join(sessionDir, "log.jsonl"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	appDir := filepath.Join(appData, "digital-employee-app")
	env := map[string]string{"USERPROFILE": home, "APPDATA": appData}
	if _, err := Migrate(Options{Env: env, AppDir: appDir}); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	registry, err := LoadRegistry(appDir)
	if err != nil || len(registry.Workspaces) != 1 {
		t.Fatalf("迁移后应登记一个工作区：%+v %v", registry, err)
	}
	mine := registry.Workspaces[0]
	// 迁移来的任务目录要能被配置加载器读出来（引擎跳过读不出的任务）
	if _, err := config.Load(filepath.Join(appDir, "tasks", "old-task", "config")); err != nil {
		t.Fatalf("任务配置读不出来：%v", err)
	}

	// 真实引擎能读出迁移来的任务，发起事件里带着工作区
	registryClient := tools.NewRegistry()
	t.Cleanup(func() { _ = registryClient.Close() })
	eng, err := engine.New(engine.Deps{Dir: appDir, EmployeesDir: filepath.Join(appDir, "employees"), ToolClient: registryClient})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, summary := range eng.List() {
		if summary.ID != "old-task" {
			continue
		}
		found = true
		if summary.WorkspaceID != mine.ID || summary.State != contract.StateAdmission {
			t.Fatalf("迁移来的任务：%+v", summary)
		}
	}
	if !found {
		t.Fatalf("引擎里没有迁移来的任务：%+v", eng.List())
	}
	if err := eng.ResumeAll(); err != nil {
		t.Fatalf("引擎启动不能因为损坏的日志失败：%v", err)
	}

	// 会话日志头补上了工作区，原来的行都在
	raw, err := os.ReadFile(filepath.Join(appDir, "sessions", "s-old", "log.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := splitLines(string(raw))
	if len(lines) != 2 {
		t.Fatalf("会话日志行数 = %d：%s", len(lines), raw)
	}
	header := map[string]any{}
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil || header["workspaceId"] != mine.ID {
		t.Fatalf("会话日志头 = %s", lines[0])
	}
	// 原目录不动
	original, err := os.ReadFile(filepath.Join(sessionDir, "log.jsonl"))
	if err != nil || string(original) != log {
		t.Fatalf("原会话日志被改动：%s", original)
	}

	// 再跑一次：有标记文件，什么都不做
	changed, err := Migrate(Options{Env: env, AppDir: appDir})
	if err != nil || changed {
		t.Fatalf("重复迁移：changed=%v err=%v", changed, err)
	}
	again, err := LoadRegistry(appDir)
	if err != nil || len(again.Workspaces) != 1 || again.Workspaces[0].ID != mine.ID {
		t.Fatalf("重复迁移不应改动登记：%+v", again)
	}
}

func splitLines(text string) []string {
	out := []string{}
	current := ""
	for _, ch := range text {
		if ch == '\n' {
			if current != "" {
				out = append(out, current)
			}
			current = ""
			continue
		}
		current += string(ch)
	}
	if current != "" {
		out = append(out, current)
	}
	return out
}

var _ = errors.Is
