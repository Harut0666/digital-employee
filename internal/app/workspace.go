// 资源释放（Spec 0006）：员工配置、公开样例、前端构建产物。
// 移植自 packages/app/src/{workspace,resources}.ts + packages/server/src/bootstrap.ts。
//
// 语义：
//   - 员工配置与样例释放到应用目录（所有工作区共用）；工作区目录里只放“交付”文件夹（和上传的“材料”）；
//   - 员工配置与样例：已有文件不覆盖（用户改过的配置要保留）；
//   - 前端构建产物与“默认员工配置”是内部资源：跟着版本走，整体覆盖；
//   - 首次启动写一份不含 API Key 的 settings.json（在应用目录里）。
package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	assets "digital-employee"
)

// runtimeDir 运行时资源目录（前端构建产物、默认员工配置副本）。
const runtimeDir = ".runtime"

// defaultSettingsTemplate 默认 settings.json：不预置任何厂商的连接（Spec 0007「设置与连接」）。
// 全新应用目录里模型设置没有任何连接、lastModel 为空，由人在设置页自己添加连接。
var defaultSettingsTemplate = map[string]any{
	"connections": []map[string]any{},
	"demo":        map[string]any{"toolFaults": map[string]any{}, "corruptTraceability": false, "crashOnNextStep": false},
}

// ReleaseOutcome 一次释放的结果。
type ReleaseOutcome struct {
	Written []string
	Skipped []string
}

// PreparedWorkspace 准备应用目录与工作区目录的结果。
type PreparedWorkspace struct {
	// WorkspaceDir 应用目录（单目录模式下就是工作区目录）
	WorkspaceDir string
	// AppDir 应用目录：员工配置、样例、前端产物、设置都在这里
	AppDir              string
	EmployeesDir        string
	DefaultEmployeesDir string
	WebDistDir          string
	Settings            string // written | kept
	Employees           ReleaseOutcome
	Samples             ReleaseOutcome
	Web                 ReleaseOutcome
	Defaults            ReleaseOutcome
	WorkspaceSamples    ReleaseOutcome
}

// PrepareOptions 准备应用目录的入参。
type PrepareOptions struct {
	// AppDir 应用目录：员工配置、样例、前端产物和设置都放在这里（单目录模式下就是工作区目录）。
	AppDir string
	// WorkspaceDir 兼容旧调用：AppDir 为空时把它当作应用目录。
	WorkspaceDir string
	// RepoRoot 仓库根（开发时的默认资源来源）。
	RepoRoot string
	// EmployeesDir 已指定的员工目录（DE_EMPLOYEES_DIR）：设置时不再初始化应用目录副本。
	EmployeesDir string
	// WebDistDir 已指定的前端产物目录（DE_WEB_DIST）。
	WebDistDir string
}

// PrepareAppDir 准备应用目录：释放员工配置、样例、前端产物与默认员工副本，写默认 settings.json。
func PrepareAppDir(options PrepareOptions) (*PreparedWorkspace, error) {
	if options.AppDir == "" {
		options.AppDir = options.WorkspaceDir
	}
	if options.AppDir == "" {
		return nil, errors.New("准备应用目录需要 AppDir")
	}
	return prepare(options)
}

// ReleaseFile 一个要释放的文件。
type ReleaseFile struct {
	// Path 工作区相对路径，统一用 / 分隔。
	Path string
	Data []byte
}

// releaseFiles 把文件释放到目标目录；默认不覆盖已有文件。
func releaseFiles(files []ReleaseFile, targetRoot string, overwrite bool) (ReleaseOutcome, error) {
	outcome := ReleaseOutcome{Written: []string{}, Skipped: []string{}}
	for _, file := range files {
		rel, err := safeRelativePath(file.Path)
		if err != nil {
			return outcome, err
		}
		target := filepath.Join(targetRoot, rel)
		if !overwrite {
			if _, err := os.Stat(target); err == nil {
				outcome.Skipped = append(outcome.Skipped, file.Path)
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return outcome, err
		}
		if err := os.WriteFile(target, file.Data, 0o644); err != nil {
			return outcome, err
		}
		outcome.Written = append(outcome.Written, file.Path)
	}
	return outcome, nil
}

// safeRelativePath 释放路径必须是目标目录内的相对路径：绝对路径、盘符、.. 段一律拒绝。
func safeRelativePath(raw string) (string, error) {
	normalized := strings.ReplaceAll(raw, "\\", "/")
	if normalized == "" || strings.HasPrefix(normalized, "/") || len(normalized) > 1 && normalized[1] == ':' {
		return "", fmt.Errorf("释放资源失败：非法路径 %q", raw)
	}
	parts := []string{}
	for _, part := range strings.Split(normalized, "/") {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", fmt.Errorf("释放资源失败：非法路径 %q", raw)
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("释放资源失败：非法路径 %q", raw)
	}
	return filepath.Join(parts...), nil
}

func collectDir(source, prefix string, out *[]ReleaseFile) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if prefix != "" {
			key = prefix + "/" + key
		}
		*out = append(*out, ReleaseFile{Path: key, Data: data})
		return nil
	})
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// prepare 释放员工配置、样例、前端产物与默认员工副本，写默认 settings.json。
func prepare(options PrepareOptions) (*PreparedWorkspace, error) {
	workspaceDir := options.AppDir
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return nil, err
	}
	prepared := &PreparedWorkspace{WorkspaceDir: workspaceDir, AppDir: workspaceDir}
	repoRoot := options.RepoRoot

	// 资源来源：仓库目录优先（开发态可直接改），找不到仓库时回退到内嵌资源（单 exe 自包含）。
	workspaceEmployees := filepath.Join(workspaceDir, "employees")
	repoEmployeesDir := ""
	if repoRoot != "" && dirExists(filepath.Join(repoRoot, "employees")) {
		repoEmployeesDir = filepath.Join(repoRoot, "employees")
	}
	employeeFiles := []ReleaseFile{}
	if repoEmployeesDir != "" {
		if err := collectEmployeeDirs(repoEmployeesDir, &employeeFiles); err != nil {
			return nil, err
		}
	} else if err := collectEmbeddedEmployeeDirs(assets.Employees, "employees", &employeeFiles); err != nil {
		return nil, err
	}
	if options.EmployeesDir != "" {
		prepared.EmployeesDir = options.EmployeesDir
	} else {
		outcome, err := releaseFiles(employeeFiles, workspaceEmployees, false)
		if err != nil {
			return nil, err
		}
		prepared.Employees = outcome
		prepared.EmployeesDir = workspaceEmployees
	}

	// 公开样例：samples/scenario-a 与 samples/cases（每个子目录独立回退到内嵌资源）
	samples, err := releaseSamples(workspaceDir, repoRoot)
	if err != nil {
		return nil, err
	}
	prepared.Samples = samples

	// 前端构建产物：内部资源，整体覆盖
	prepared.WebDistDir = os.Getenv("DE_WEB_DIST")
	if options.WebDistDir != "" {
		prepared.WebDistDir = options.WebDistDir
	}
	if prepared.WebDistDir == "" {
		webFiles := []ReleaseFile{}
		if repoRoot != "" && dirExists(filepath.Join(repoRoot, "packages", "web", "dist")) {
			if err := collectDir(filepath.Join(repoRoot, "packages", "web", "dist"), "", &webFiles); err != nil {
				return nil, err
			}
		} else if err := collectEmbedded(assets.WebDist, "packages/web/dist", "", &webFiles); err != nil {
			return nil, err
		}
		target := filepath.Join(workspaceDir, runtimeDir, "web")
		outcome, err := releaseFiles(webFiles, target, true)
		if err != nil {
			return nil, err
		}
		prepared.Web = outcome
		prepared.WebDistDir = target
	}

	// 默认员工配置副本：设置页“恢复默认员工配置”的来源，整体覆盖
	prepared.DefaultEmployeesDir = filepath.Join(workspaceDir, runtimeDir, "default-employees")
	outcome, err := releaseFiles(employeeFiles, prepared.DefaultEmployeesDir, true)
	if err != nil {
		return nil, err
	}
	prepared.Defaults = outcome

	// 默认 settings.json（不含任何 API Key）；已有则原样保留
	settingsFile := filepath.Join(workspaceDir, "settings.json")
	if _, err := os.Stat(settingsFile); err == nil {
		prepared.Settings = "kept"
	} else {
		if err := writeDefaultSettings(settingsFile); err != nil {
			return nil, err
		}
		prepared.Settings = "written"
	}
	return prepared, nil
}

// collectEmbeddedEmployeeDirs 从内嵌 FS 只收集含 workflow.yaml 的员工目录
// （与仓库路径的 collectEmployeeDirs 一致，不释放 employees/package.json、test 等非员工内容）。
func collectEmbeddedEmployeeDirs(fsys fs.FS, root string, out *[]ReleaseFile) error {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := root + "/" + entry.Name()
		if _, err := fs.Stat(fsys, dir+"/workflow.yaml"); err != nil {
			continue
		}
		if err := collectEmbedded(fsys, dir, entry.Name(), out); err != nil {
			return err
		}
	}
	return nil
}

// collectEmbedded 从内嵌 FS 读出目录下的全部文件：key 相对 root（prefix 非空时前置）。
// 用于单 exe 找不到仓库目录时的资源释放。
func collectEmbedded(fsys fs.FS, root, prefix string, out *[]ReleaseFile) error {
	return fs.WalkDir(fsys, root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(path, root), "/")
		key := rel
		if prefix != "" {
			key = prefix + "/" + rel
		}
		*out = append(*out, ReleaseFile{Path: key, Data: data})
		return nil
	})
}

// collectEmployeeDirs 收集 employees/ 下含 workflow.yaml 的员工目录（不收集 node_modules、test 等）。
func collectEmployeeDirs(employeesRoot string, out *[]ReleaseFile) error {
	entries, err := os.ReadDir(employeesRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(employeesRoot, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, "workflow.yaml")); err != nil {
			continue
		}
		if err := collectDir(dir, entry.Name(), out); err != nil {
			return err
		}
	}
	return nil
}

func writeDefaultSettings(path string) error {
	data, err := marshalIndentJSON(defaultSettingsTemplate)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ResolveWorkspace 单目录模式的工作区目录：DE_WORKSPACE > DE_APP_DIR > %APPDATA%/digital-employee-app。
// Spec 0006 之后工作区只是目录，应用级数据在 ResolveAppDir 指定的地方。
func ResolveWorkspace(env map[string]string) string {
	return ResolveAppDir(env)
}

// PrepareWorkspaceDir 准备一个工作区目录：用户工作目录里只放用得上的东西——“交付”文件夹
// （交付物在 T42 写进这里）。公开样例和评测用例只放在应用目录（T41），不再往工作区目录里释放 samples/。
// 目录不存在时新建；调用方要区分“新建”和“重新打开”时自己先判断。
func PrepareWorkspaceDir(dir string) (ReleaseOutcome, error) {
	if err := os.MkdirAll(filepath.Join(dir, DeliverDirName), 0o755); err != nil {
		return ReleaseOutcome{}, err
	}
	return ReleaseOutcome{Written: []string{}, Skipped: []string{}}, nil
}

// releaseSamples 把公开样例释放到目录的 samples/ 下（已有文件不覆盖）。
func releaseSamples(dir, repoRoot string) (ReleaseOutcome, error) {
	files := []ReleaseFile{}
	sources := []struct {
		name string
		root string
		fsys fs.FS
	}{
		{"scenario-a", "samples/scenario-a", assets.ScenarioA},
		{"scenario-b", "samples/scenario-b", assets.ScenarioB},
		{"cases", "samples/cases", assets.SamplesCases},
	}
	for _, source := range sources {
		repoSample := ""
		if repoRoot != "" {
			candidate := filepath.Join(repoRoot, "samples", source.name)
			if dirExists(candidate) {
				repoSample = candidate
			}
		}
		if repoSample != "" {
			if err := collectDir(repoSample, source.name, &files); err != nil {
				return ReleaseOutcome{}, err
			}
			continue
		}
		if err := collectEmbedded(source.fsys, source.root, source.name, &files); err != nil {
			return ReleaseOutcome{}, err
		}
	}
	if len(files) == 0 {
		return ReleaseOutcome{Written: []string{}, Skipped: []string{}}, nil
	}
	return releaseFiles(files, filepath.Join(dir, "samples"), false)
}

// DeliverDirName 工作区目录里放交付物的文件夹。
const DeliverDirName = "交付"

// FindRepoRoot 仓库根：DE_REPO_ROOT > 当前目录向上找（含 employees/ 与 samples/）。
func FindRepoRoot(start string) string {
	if value := os.Getenv("DE_REPO_ROOT"); value != "" {
		return value
	}
	dir := start
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return ""
		}
		dir = cwd
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if dirExists(filepath.Join(absolute, "employees")) && dirExists(filepath.Join(absolute, "samples")) {
			return absolute
		}
		parent := filepath.Dir(absolute)
		if parent == absolute {
			return ""
		}
		absolute = parent
	}
}
