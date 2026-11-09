// 单目录模式：设置了 DE_WORKSPACE 时，这个目录同时是应用目录和唯一的工作区，布局和以前一样。
package app_test

import (
	"path/filepath"
	"testing"

	"digital-employee/internal/app"
)

func TestResolveAppDir(t *testing.T) {
	// 单目录模式：DE_WORKSPACE 优先，它同时是应用目录
	if got := app.ResolveAppDir(map[string]string{"DE_WORKSPACE": `D:\tmp\ws`, "DE_APP_DIR": `D:\tmp\app`}); got != `D:\tmp\ws` {
		t.Fatalf("DE_WORKSPACE 应优先：%q", got)
	}
	if got := app.ResolveAppDir(map[string]string{"DE_APP_DIR": `D:\tmp\app`}); got != `D:\tmp\app` {
		t.Fatalf("DE_APP_DIR 其次：%q", got)
	}
	if got := app.ResolveAppDir(map[string]string{"APPDATA": `C:\AppData`}); got != filepath.Join(`C:\AppData`, "digital-employee-app") {
		t.Fatalf("应回落到 APPDATA 下的 digital-employee-app：%q", got)
	}
}

func TestSingleDirectoryLayout(t *testing.T) {
	dir := t.TempDir()
	layout := app.ResolveLayout(map[string]string{"DE_WORKSPACE": dir})
	if layout.AppDir != dir || !layout.Single || layout.Workspace.ID == "" || layout.Workspace.Path != dir {
		t.Fatalf("DE_WORKSPACE 指定的目录应同时是应用目录和唯一的工作区：%+v", layout)
	}
	if layout.Workspace.Name != filepath.Base(dir) {
		t.Fatalf("工作区名称取目录名：%+v", layout.Workspace)
	}
	if layout.EmployeesDir != filepath.Join(dir, "employees") {
		t.Fatalf("员工配置在目录里：%q", layout.EmployeesDir)
	}
	if layout.SessionsDir != filepath.Join(dir, "sessions") || layout.TasksDir != filepath.Join(dir, "tasks") {
		t.Fatalf("会话和任务在目录里：%+v", layout)
	}

	// 应用目录模式：设置、员工、知识库、会话、任务都在应用目录，工作区只是目录
	appDir := t.TempDir()
	workspace := t.TempDir()
	mode := app.ResolveLayout(map[string]string{"DE_APP_DIR": appDir})
	if mode.Single || mode.AppDir != appDir || mode.Workspace.Path != "" {
		t.Fatalf("没有指定工作区时由登记决定：%+v", mode)
	}
	only := app.ResolveLayout(map[string]string{"DE_APP_DIR": appDir, "DE_WORKSPACE": workspace})
	if !only.Single || only.AppDir != workspace || only.Workspace.Path != workspace {
		t.Fatalf("DE_WORKSPACE 优先于 DE_APP_DIR：%+v", only)
	}
}

func TestSingleDirectoryModeKeepsTheLayout(t *testing.T) {
	dir := t.TempDir()
	// 目录里已经有以前的设置、员工和样例：单目录模式直接沿用
	writeTestFile(t, filepath.Join(dir, "settings.json"), `{"custom": true}`)
	a := startApp(t, dir, "", withAppEnv(map[string]string{"DE_WORKSPACE": dir}))

	list := a.workspaces()
	if !list.ReadOnly || len(list.Workspaces) != 1 || !sameDir(list.Workspaces[0].Path, dir) {
		t.Fatalf("单目录模式只列出这个目录，且只读：%+v", list)
	}
	// 不传 workspaceId 时取唯一的工作区
	created := a.createSessionIn("")
	if created.WorkspaceID != list.Workspaces[0].ID {
		t.Fatalf("会话应归到唯一的工作区：%+v", created)
	}
	if !hasFile(filepath.Join(dir, "sessions", created.ID, "log.jsonl")) {
		t.Fatal("单目录模式的会话在 sessions/ 下")
	}
	// 不传 workspaceId 时取唯一的工作区
	taskID := a.createDraftTask("")
	if !hasFile(filepath.Join(dir, "tasks", taskID, "events.jsonl")) {
		t.Fatal("单目录模式的任务在 tasks/ 下")
	}
}
