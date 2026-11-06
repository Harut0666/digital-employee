// 命令行要用的目录解析（对应 packages/cli/src/paths.ts）：
// 员工配置与样例：DE_* 环境变量 > 工作区里的副本（exe 释放的）> 仓库里的默认配置。
package cli

import (
	"os"
	"path/filepath"

	"digital-employee/internal/app"
)

// ResolveEmployeesDir 员工配置目录：DE_EMPLOYEES_DIR > 工作区里的副本 > 仓库里的默认配置。
func ResolveEmployeesDir(env map[string]string, repoRoot string) string {
	value := func(key string) string {
		if env != nil {
			if v, ok := env[key]; ok {
				return v
			}
			return ""
		}
		return os.Getenv(key)
	}
	if dir := value("DE_EMPLOYEES_DIR"); dir != "" {
		if absolute, err := filepath.Abs(dir); err == nil {
			return absolute
		}
		return dir
	}
	workspaceEmployees := filepath.Join(app.ResolveWorkspace(env), "employees")
	if hasEmployee(workspaceEmployees) {
		return workspaceEmployees
	}
	return filepath.Join(repoRoot, "employees")
}

// ResolveSamplesDir 样例目录：工作区里的副本 > 仓库里的 samples。
func ResolveSamplesDir(env map[string]string, repoRoot string) string {
	workspaceSamples := filepath.Join(app.ResolveWorkspace(env), "samples")
	if dirExists(filepath.Join(workspaceSamples, "scenario-a")) {
		return workspaceSamples
	}
	return filepath.Join(repoRoot, "samples")
}

func hasEmployee(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			if _, err := os.Stat(filepath.Join(dir, entry.Name(), "workflow.yaml")); err == nil {
				return true
			}
		}
	}
	return false
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// RepoRoot 仓库根：当前目录向上找（含 employees/ 与 samples/）；找不到时返回当前目录。
func RepoRoot() string {
	if root := app.FindRepoRoot(""); root != "" {
		return root
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}
