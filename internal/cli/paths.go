// 命令行要用的目录解析（对应 packages/cli/src/paths.ts）：
// 员工配置与样例：DE_* 环境变量 > 应用目录里的副本（exe 释放的）> 仓库里的默认配置。
// 单目录模式：设置了 DE_WORKSPACE 时，这个目录同时是应用目录和唯一的工作区。
package cli

import (
	"os"
	"path/filepath"

	"digital-employee/internal/app"
)

// ResolveEmployeesDir 员工配置目录：DE_EMPLOYEES_DIR > 应用目录里的副本 > 仓库里的默认配置。
func ResolveEmployeesDir(env map[string]string, repoRoot string) string {
	if dir := valueOf(env, "DE_EMPLOYEES_DIR"); dir != "" {
		if absolute, err := filepath.Abs(dir); err == nil {
			return absolute
		}
		return dir
	}
	appEmployees := filepath.Join(app.ResolveAppDir(env), "employees")
	if hasEmployee(appEmployees) {
		return appEmployees
	}
	return filepath.Join(repoRoot, "employees")
}

// ResolveSamplesDir 样例目录：应用目录（单目录模式下就是工作区目录）里的副本 > 仓库里的 samples。
// “载入公开样例”、评测用例和命令行都从这里取；工作区目录里不再释放样例（T41）。
func ResolveSamplesDir(env map[string]string, repoRoot string) string {
	appSamples := filepath.Join(app.ResolveAppDir(env), "samples")
	if dirExists(filepath.Join(appSamples, "scenario-a")) {
		return appSamples
	}
	return filepath.Join(repoRoot, "samples")
}

// CaseAppDir run-case(s) 的每条用例都用一个干净的应用目录（Spec 0006 之后任务、设置、知识库都在应用目录里），
// 放在用例结果目录下。设置了 DE_WORKSPACE 时用那个目录，布局和以前一样。
func CaseAppDir(env map[string]string, caseOutDir string) string {
	if value := valueOf(env, "DE_WORKSPACE"); value != "" {
		if absolute, err := filepath.Abs(value); err == nil {
			return absolute
		}
		return value
	}
	if value := valueOf(env, "DE_APP_DIR"); value != "" {
		if absolute, err := filepath.Abs(value); err == nil {
			return absolute
		}
		return value
	}
	return filepath.Join(caseOutDir, "app")
}

// valueOf 读环境变量：注入的优先，没有注入时读进程环境。
func valueOf(env map[string]string, key string) string {
	if env != nil {
		if v, ok := env[key]; ok {
			return v
		}
		return ""
	}
	return os.Getenv(key)
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
