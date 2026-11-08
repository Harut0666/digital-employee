// 命令行路径（Spec 0006）：set DE_WORKSPACE = 单目录模式，目录同时是应用目录和唯一的工作区。

package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCaseAppDir(t *testing.T) {
	out := t.TempDir()
	// 没设置 DE_WORKSPACE：每条用例在结果目录下用一个干净的应用目录
	if got := caseAppDir(Deps{Env: map[string]string{}}, out, "A-01"); got != filepath.Join(out, "app") {
		t.Fatalf("应用目录模式的应用目录 = %q", got)
	}
	// DE_APP_DIR 优先于结果目录
	app := t.TempDir()
	if got := caseAppDir(Deps{Env: map[string]string{"DE_APP_DIR": app}}, out, "A-01"); got != app {
		t.Fatalf("DE_APP_DIR 指定的应用目录 = %q", got)
	}
	// 单目录模式布局和以前一样：<DE_WORKSPACE>/<用例 ID>/
	workspace := t.TempDir()
	got := caseAppDir(Deps{Env: map[string]string{"DE_WORKSPACE": workspace, "DE_APP_DIR": app}}, out, "A-01")
	if got != filepath.Join(workspace, "A-01") {
		t.Fatalf("单目录模式的用例子目录 = %q", got)
	}
}

func TestResolveDirsPreferTheAppDir(t *testing.T) {
	app := t.TempDir()
	workspace := t.TempDir()
	repo := t.TempDir()
	// 应用目录里没有样例时用仓库里的
	if got := ResolveSamplesDir(map[string]string{"DE_APP_DIR": app}, repo); got != filepath.Join(repo, "samples") {
		t.Fatalf("应用目录里没有样例时用仓库里的：%q", got)
	}
	// 单目录模式：工作区目录里的副本优先（首次启动会释放到这里）
	if err := os.MkdirAll(filepath.Join(workspace, "samples", "scenario-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ResolveSamplesDir(map[string]string{"DE_WORKSPACE": workspace}, repo); got != filepath.Join(workspace, "samples") {
		t.Fatalf("单目录模式的样例目录 = %q", got)
	}
	// 员工目录：应用目录里的副本优先于仓库
	if err := os.MkdirAll(filepath.Join(app, "employees", "mini"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "employees", "mini", "workflow.yaml"), []byte("id: mini\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolveEmployeesDir(map[string]string{"DE_APP_DIR": app}, repo); got != filepath.Join(app, "employees") {
		t.Fatalf("员工目录 = %q", got)
	}
}
