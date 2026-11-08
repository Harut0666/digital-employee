// 一次性迁移：旧默认工作区 %APPDATA%\digital-employee 和 T32 登记过的工作区里的数据搬进应用目录。
package app_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"digital-employee/internal/app"
	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

// writeLegacyEmployee 旧工作区里的员工配置：最小的合法配置（含 workflow.yaml 与 qc.yaml）。
func writeLegacyEmployee(t *testing.T, employeesDir string) {
	t.Helper()
	writeTestFile(t, filepath.Join(employeesDir, "weekly-report", "workflow.yaml"),
		"id: weekly-report\nname: 经营周报员工\nlimits:\n  task: { modelCalls: 10, durationSec: 600 }\n  toolRetry: { attempts: 1, backoffMs: [0] }\nsteps:\n  - { id: only, label: 唯一, kind: judge, prompt: prompts/p.md, outputSchema: s.json, tools: [], limits: { rounds: 1 } }\n")
	writeTestFile(t, filepath.Join(employeesDir, "weekly-report", "prompts", "p.md"), "写周报")
	writeTestFile(t, filepath.Join(employeesDir, "weekly-report", "s.json"), `{"type":"object"}`)
	writeTestFile(t, filepath.Join(employeesDir, "weekly-report", "qc.yaml"), "custom: []\n")
}

// legacyTaskLog 手写的旧任务事件日志（发起事件没有工作区字段）。
func legacyTaskLog(taskID string) string {
	return strings.Join([]string{
		`{"seq":1,"ts":"2026-10-01T00:00:00.000Z","taskId":"` + taskID + `","type":"task_created","employee":"weekly-report","configHash":"old","form":{"period":{"year":2026,"week":44}},"attachments":[]}`,
		`{"seq":2,"ts":"2026-10-01T00:00:01.000Z","taskId":"` + taskID + `","type":"state_changed","from":"draft","to":"admission","trigger":"submit","actor":"initiator"}`,
	}, "\n") + "\n"
}

// legacySessionLog 手写的旧会话日志（只有人说的话）。
func legacySessionLog() string {
	return strings.Join([]string{
		`{"id":"s1","seq":1,"ts":"2026-10-01T00:00:00.000Z","kind":"created"}`,
		`{"id":"s2","seq":2,"ts":"2026-10-01T00:00:01.000Z","kind":"user","role":"initiator","text":"帮我出第 44 周的周报"}`,
	}, "\n") + "\n"
}

// taskEvents GET /api/tasks/{id}/events。
func (a *switchingApp) taskEvents(taskID string) []contract.TaskEvent {
	a.t.Helper()
	status, data := a.call(http.MethodGet, "/api/tasks/"+taskID+"/events", nil)
	if status != http.StatusOK {
		a.t.Fatalf("读取任务 %s 的事件失败：%d %s", taskID, status, data)
	}
	return decodeAs[[]contract.TaskEvent](a.t, data)
}

// seedLegacy 伪造旧默认工作区和 T32 登记过的一个工作区，返回它们的目录。
func seedLegacy(t *testing.T, appData string) (legacy, t32 string) {
	t.Helper()
	legacy = filepath.Join(appData, "digital-employee")
	writeLegacyEmployee(t, filepath.Join(legacy, "employees"))
	writeTestFile(t, filepath.Join(legacy, "settings.json"), `{"version":2,"revision":3,"connections":[{"name":"旧专线","baseUrl":"http://127.0.0.1:9/v1","apiKey":"sk-legacy","models":["m1"]}],"defaultConnection":"旧专线","defaultModel":"m1"}`+"\n")
	writeTestFile(t, filepath.Join(legacy, "knowledge", "kb-1", "text.txt"), "旧知识库的正文\n")
	writeTestFile(t, filepath.Join(legacy, "knowledge", "index.json"), `[{"id":"kb-1","title":"旧周报","description":"旧知识库条目"}]`+"\n")
	writeLegacyEmployee(t, filepath.Join(legacy, "tasks", "old-task", "config"))
	writeTestFile(t, filepath.Join(legacy, "tasks", "old-task", "events.jsonl"), legacyTaskLog("old-task"))
	writeTestFile(t, filepath.Join(legacy, "sessions", "s-old", "log.jsonl"), legacySessionLog())

	t32 = filepath.Join(appData, "T32工作区")
	writeLegacyEmployee(t, filepath.Join(t32, "tasks", "t32-task", "config"))
	writeTestFile(t, filepath.Join(t32, "tasks", "t32-task", "events.jsonl"), legacyTaskLog("t32-task"))
	writeTestFile(t, filepath.Join(t32, "sessions", "s-t32", "log.jsonl"), legacySessionLog())
	writeTestFile(t, filepath.Join(appData, "digital-employee-app", "workspaces.json"), `{"current":"ws-t32","workspaces":[{"id":"ws-t32","name":"T32工作区","path":`+quote(t32)+`}]}`+"\n")
	return legacy, t32
}

func quote(text string) string {
	raw, _ := json.Marshal(text)
	return string(raw)
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// copyTree 递归复制目录。
func copyTree(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, rel)
		if info.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, content, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// snapshotTree 目录里每个文件的内容，用来核对迁移前后原目录没被动过。
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// sortedKeys 便于比较的键列表。
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func TestMigrationCopiesLegacyDefaultWorkspaceAndT32Workspaces(t *testing.T) {
	appData := t.TempDir()
	legacy, t32 := seedLegacy(t, appData)
	before := map[string]map[string]string{legacy: snapshotTree(t, legacy), t32: snapshotTree(t, t32)}
	appDir := filepath.Join(appData, "digital-employee-app")
	home := t.TempDir()

	a := startApp(t, appDir, "", withAppEnv(map[string]string{"USERPROFILE": home, "APPDATA": appData}))

	// 设置、员工、知识库来自旧默认工作区
	if !a.hasKey("旧专线") || !slices.Contains(a.connectionNames(), "旧专线") {
		t.Fatalf("旧设置没有迁移：%v", a.connectionNames())
	}
	if names := a.employeeNames(); !slices.Contains(names, "经营周报员工") {
		t.Fatalf("旧员工配置没有迁移：%v", names)
	}
	if titles := a.knowledgeTitles(); !slices.Contains(titles, "旧周报") {
		t.Fatalf("旧知识库没有迁移：%v", titles)
	}
	// 任务和会话来自旧默认工作区和 T32 工作区，都归到“我的工作区”；T32 工作区本身也保留登记
	mine := a.byName("我的工作区")
	if mine.ID == "" {
		t.Fatalf("迁移后应有“我的工作区”：%+v", a.workspaces())
	}
	for _, want := range []string{"old-task", "t32-task"} {
		// 任务列表读的是真实引擎：任务目录里的配置副本和发起事件都要能读出来
		got := a.taskEvents(want)
		if len(got) == 0 {
			t.Fatalf("旧任务 %s 没有迁移（读不到事件）：%+v", want, a.taskIDs())
		}
		if got[0].Type != contract.EventTaskCreated || got[0].WorkspaceID != mine.ID {
			t.Fatalf("迁移来的任务 %s 的发起事件：%+v", want, got[0])
		}
	}
	if !slices.ContainsFunc(a.workspaces().Workspaces, func(item registered) bool { return item.Name == "T32工作区" }) {
		t.Fatalf("T32 登记过的工作区应保留：%+v", a.workspaces())
	}
	fromMine := a.sessionsWith(mine.ID)
	for _, want := range []string{"s-old", "s-t32"} {
		if _, ok := fromMine[want]; !ok {
			t.Fatalf("迁移来的会话 %s 应归到“我的工作区”：%v", want, fromMine)
		}
	}
	for _, item := range a.sessions("") {
		if item.ID == "s-old" || item.ID == "s-t32" {
			if item.WorkspaceID != mine.ID {
				t.Fatalf("迁移来的会话应归到我的工作区：%+v", item)
			}
			if item.Title != "帮我出第 44 周的周报" {
				t.Fatalf("迁移来的会话应可读：%+v", item)
			}
		}
	}
	// 原目录只读不删：迁移时一个字节都没变（启动时新工单会往工作区目录补样例，不算改动记录）
	for dir, files := range before {
		after := snapshotTree(t, dir)
		for name, content := range files {
			if current, ok := after[name]; !ok || current != content {
				t.Fatalf("原目录 %s 的文件 %s 被改动或删除", dir, name)
			}
		}
	}
}

func TestMigrationRunsOnceAndRepeatsTheSameResult(t *testing.T) {
	appData := t.TempDir()
	seedLegacy(t, appData)
	appDir := filepath.Join(appData, "digital-employee-app")
	home := t.TempDir()
	// 用环境变量里的 home 和 APPDATA：迁移找旧默认工作区靠的就是 APPDATA
	env := withAppEnv(map[string]string{"USERPROFILE": home, "APPDATA": appData})

	first := startApp(t, appDir, "", env)
	firstTasks := first.taskIDs()
	firstSessions := first.sessions("")
	firstWorkspaces := first.workspaces().Workspaces
	firstEmployees := first.employeeNames()
	if err := first.running.Close(); err != nil {
		t.Fatal(err)
	}

	second := startApp(t, appDir, "", env)
	if !slices.Equal(sortedStrings(second.taskIDs()), sortedStrings(firstTasks)) {
		t.Fatalf("再跑一次迁移不应多出任务：%v → %v", firstTasks, second.taskIDs())
	}
	if !slices.Equal(sortedStrings(sessionIDsOf(second.sessions(""))), sortedStrings(sessionIDsOf(firstSessions))) {
		t.Fatalf("再跑一次迁移不应多出会话：%v", sessionIDsOf(second.sessions("")))
	}
	if !slices.Equal(sortedStrings(namesOf(second.workspaces().Workspaces)), sortedStrings(namesOf(firstWorkspaces))) {
		t.Fatalf("再跑一次迁移不应多出工作区：%+v", second.workspaces())
	}
	if !slices.Equal(sortedStrings(second.employeeNames()), sortedStrings(firstEmployees)) {
		t.Fatalf("再跑一次迁移不应多出员工：%v", second.employeeNames())
	}
	// 关掉应用再调一次迁移也什么都不会变（标记文件在，直接跳过）
	if err := second.running.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Migrate(app.Options{Env: map[string]string{"USERPROFILE": home, "APPDATA": appData}}); err != nil {
		t.Fatalf("迁移应可重复运行：%v", err)
	}
	if !hasFile(filepath.Join(appDir, "migrated.json")) {
		t.Fatal("迁移后应留下标记文件")
	}
}

func sortedStrings(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

func sessionIDsOf(items []session.View) []string {
	out := []string{}
	for _, item := range items {
		out = append(out, item.ID+"@"+item.WorkspaceID)
	}
	return out
}

func namesOf(items []registered) []string {
	out := []string{}
	for _, item := range items {
		out = append(out, item.Name+"@"+item.ID)
	}
	return out
}
