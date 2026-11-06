package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChoosePort(t *testing.T) {
	port, err := ChoosePort(9000, 5, func(port int) bool { return port >= 9002 })
	if err != nil || port != 9002 {
		t.Fatalf("应选第一个可用端口 9002：%d %v", port, err)
	}
	if _, err := ChoosePort(9000, 2, func(int) bool { return false }); err == nil {
		t.Fatal("全部占用时应报错")
	}
	if port, err := ChoosePort(9000, 0, func(port int) bool { return port == 9000 }); err != nil || port != 9000 {
		t.Fatalf("attempts=0 应按缺省 20 处理：%d %v", port, err)
	}
}

func TestResolveWorkspace(t *testing.T) {
	if got := ResolveWorkspace(map[string]string{"DE_WORKSPACE": `D:\tmp\de`}); got != `D:\tmp\de` {
		t.Fatalf("DE_WORKSPACE 应优先：%q", got)
	}
	got := ResolveWorkspace(map[string]string{"APPDATA": `C:\AppData`})
	if got != filepath.Join(`C:\AppData`, "digital-employee") {
		t.Fatalf("应回落到 APPDATA：%q", got)
	}
}

func TestPrepareWorkspace(t *testing.T) {
	repo := t.TempDir()
	// 假仓库：employees/mini（含 workflow.yaml）、samples/scenario-a、packages/web/dist
	employeeDir := filepath.Join(repo, "employees", "mini")
	if err := os.MkdirAll(filepath.Join(employeeDir, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(employeeDir, "workflow.yaml"), "id: mini\n")
	writeFile(t, filepath.Join(employeeDir, "SKILL.md"), "# 原版\n")
	writeFile(t, filepath.Join(repo, "samples", "scenario-a", "数据说明.md"), "样例\n")
	writeFile(t, filepath.Join(repo, "samples", "cases", "A-01.json"), "{}")
	writeFile(t, filepath.Join(repo, "packages", "web", "dist", "index.html"), "<html>v1</html>")
	// employees/ 下非员工目录不应被释放
	if err := os.MkdirAll(filepath.Join(repo, "employees", "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}

	workspace := filepath.Join(t.TempDir(), "ws")
	prepared, err := PrepareWorkspace(PrepareOptions{WorkspaceDir: workspace, RepoRoot: repo})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.EmployeesDir != filepath.Join(workspace, "employees") {
		t.Fatalf("员工目录不对：%s", prepared.EmployeesDir)
	}
	if !fileExists(filepath.Join(workspace, "employees", "mini", "workflow.yaml")) {
		t.Fatal("员工配置未释放")
	}
	if fileExists(filepath.Join(workspace, "employees", "node_modules")) {
		t.Fatal("非员工目录不应释放")
	}
	if !fileExists(filepath.Join(workspace, "samples", "cases", "A-01.json")) {
		t.Fatal("样例未释放")
	}
	if !fileExists(filepath.Join(workspace, ".runtime", "web", "index.html")) {
		t.Fatal("前端产物未释放")
	}
	if !fileExists(filepath.Join(workspace, ".runtime", "default-employees", "mini", "workflow.yaml")) {
		t.Fatal("默认员工副本未释放")
	}
	if prepared.Settings != "written" {
		t.Fatalf("应写默认 settings.json：%s", prepared.Settings)
	}
	if !fileExists(filepath.Join(workspace, "settings.json")) {
		t.Fatal("settings.json 不存在")
	}

	// 用户改过的员工配置与样例第二次释放不覆盖；web 产物覆盖
	writeFile(t, filepath.Join(workspace, "employees", "mini", "SKILL.md"), "# 用户改过\n")
	writeFile(t, filepath.Join(workspace, "settings.json"), `{"custom": true}`)
	writeFile(t, filepath.Join(repo, "packages", "web", "dist", "index.html"), "<html>v2</html>")
	prepared, err = PrepareWorkspace(PrepareOptions{WorkspaceDir: workspace, RepoRoot: repo})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "employees", "mini", "SKILL.md"))
	if err != nil || string(data) != "# 用户改过\n" {
		t.Fatalf("员工配置不应被覆盖：%q %v", string(data), err)
	}
	data, _ = os.ReadFile(filepath.Join(workspace, ".runtime", "web", "index.html"))
	if string(data) != "<html>v2</html>" {
		t.Fatalf("前端产物应覆盖为最新：%q", string(data))
	}
	if prepared.Settings != "kept" {
		t.Fatalf("settings.json 已有应保留：%s", prepared.Settings)
	}
	data, _ = os.ReadFile(filepath.Join(workspace, "settings.json"))
	if string(data) != `{"custom": true}` {
		t.Fatalf("settings.json 不应被覆盖：%s", string(data))
	}
}

func TestFindRepoRoot(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "employees"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "samples"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindRepoRoot(deep); got != repo {
		t.Fatalf("应向上找到仓库根：%q != %q", got, repo)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
