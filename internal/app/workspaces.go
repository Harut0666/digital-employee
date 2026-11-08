// 应用目录与工作区登记（Spec 0006、ADR-0006）。
//
// 应用目录（DE_APP_DIR，缺省 %APPDATA%\digital-employee-app）放全局内容：settings.json、employees/、
// knowledge/、config-versions/、workspaces.json，以及所有工作区共用的 sessions/、tasks/。
// 工作区只是一个有名字的本机目录，登记在 workspaces.json 里；所有工作区同时可用，没有“当前工作区”。
//
// 单目录模式：设置了 DE_WORKSPACE（或命令行指定目录）时，这个目录同时是应用目录和唯一的工作区，
// 布局和 Spec 0005 之前一样，不读写 workspaces.json。
package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"digital-employee/internal/server"
)

// workspaceListFile 应用目录里的工作区登记文件名。
const workspaceListFile = "workspaces.json"

// Workspace 登记里的一个工作区。
type Workspace struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// workspaceRegistry 工作区登记文件的内容。T32 时文件里有 current 字段，读的时候忽略。
type workspaceRegistry struct {
	file       string
	Workspaces []Workspace `json:"workspaces"`
}

// Layout 一次启动的数据布局。
type Layout struct {
	// AppDir 应用目录：设置、员工、知识库、配置版本、会话、任务都在这里
	AppDir string
	// Single 单目录模式：目录同时是应用目录和唯一的工作区
	Single bool
	// Workspace 唯一的工作区（单目录模式）；应用目录模式下为空
	Workspace Workspace
	// EmployeesDir 员工配置目录
	EmployeesDir string
	// SessionsDir、TasksDir 会话和任务目录
	SessionsDir string
	TasksDir    string
}

// ResolveAppDir 应用目录：DE_WORKSPACE（单目录模式）> DE_APP_DIR > %APPDATA%/digital-employee-app。
func ResolveAppDir(env map[string]string) string {
	if dir := envValue(env, "DE_WORKSPACE"); dir != "" {
		return absolutePath(dir)
	}
	if dir := envValue(env, "DE_APP_DIR"); dir != "" {
		return absolutePath(dir)
	}
	return filepath.Join(appDataDir(env), "digital-employee-app")
}

// appDataDir %APPDATA%：注入的 APPDATA > 进程环境的 APPDATA > home/AppData/Roaming。
func appDataDir(env map[string]string) string {
	if env != nil {
		if appData := env["APPDATA"]; appData != "" {
			return appData
		}
	}
	if appData := os.Getenv("APPDATA"); appData != "" {
		return appData
	}
	if home := homeDir(env); home != "" {
		return filepath.Join(home, "AppData", "Roaming")
	}
	return "."
}

// homeDir 用户主目录：注入的 USERPROFILE / HOME > 进程环境 > UserHomeDir。
func homeDir(env map[string]string) string {
	if env != nil {
		if home := env["USERPROFILE"]; home != "" {
			return home
		}
		if home := env["HOME"]; home != "" {
			return home
		}
	}
	if home := os.Getenv("USERPROFILE"); home != "" {
		return home
	}
	if home := os.Getenv("HOME"); home != "" {
		return home
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}

// DefaultWorkspaceDir 首次启动建的工作区目录：<用户主目录>/Documents/数字员工。
func DefaultWorkspaceDir(env map[string]string) string {
	return filepath.Join(homeDir(env), "Documents", "数字员工")
}

// ResolveLayout 按环境变量决定布局。单目录模式只看环境变量，不读登记。
func ResolveLayout(env map[string]string) Layout {
	appDir := ResolveAppDir(env)
	layout := Layout{
		AppDir:       appDir,
		EmployeesDir: filepath.Join(appDir, "employees"),
		SessionsDir:  filepath.Join(appDir, "sessions"),
		TasksDir:     filepath.Join(appDir, "tasks"),
	}
	if envValue(env, "DE_WORKSPACE") != "" {
		layout.Single = true
		layout.Workspace = Workspace{
			ID:   server.SingleWorkspaceID,
			Name: workspaceBaseName(appDir),
			Path: appDir,
		}
	}
	return layout
}

// LoadRegistry 读应用目录里的工作区登记；文件不存在时是空登记。
func LoadRegistry(appDir string) (*workspaceRegistry, error) {
	registry := &workspaceRegistry{file: filepath.Join(appDir, workspaceListFile), Workspaces: []Workspace{}}
	raw, err := os.ReadFile(registry.file)
	if errors.Is(err, os.ErrNotExist) {
		return registry, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取工作区登记失败：%w", err)
	}
	if err := json.Unmarshal(raw, registry); err != nil {
		return nil, fmt.Errorf("工作区登记 %s 不是合法的 JSON：%w", registry.file, err)
	}
	if registry.Workspaces == nil {
		registry.Workspaces = []Workspace{}
	}
	return registry, nil
}

// save 先写临时文件再改名，写到一半崩溃也不会留下半个登记。
func (r *workspaceRegistry) save() error {
	data, err := marshalIndentJSON(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.file), 0o755); err != nil {
		return err
	}
	temp := r.file + ".tmp"
	if err := os.WriteFile(temp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(temp, r.file)
}

// find 按 ID 找登记项。
func (r *workspaceRegistry) find(id string) (Workspace, bool) {
	for _, item := range r.Workspaces {
		if item.ID == id {
			return item, true
		}
	}
	return Workspace{}, false
}

// findPath 按目录找登记项（Windows 不区分大小写）。
func (r *workspaceRegistry) findPath(path string) (Workspace, bool) {
	for _, item := range r.Workspaces {
		if samePath(item.Path, path) {
			return item, true
		}
	}
	return Workspace{}, false
}

// add 加一条登记：目录已是某个工作区时保留它的 ID；名称缺省取目录名。
func (r *workspaceRegistry) add(name, path string, now time.Time) Workspace {
	if found, ok := r.findPath(path); ok {
		return found
	}
	if name == "" {
		name = workspaceBaseName(path)
	}
	stamp := now.UTC().Format(stampLayout)
	added := Workspace{ID: NewWorkspaceID(), Name: name, Path: path, CreatedAt: stamp, UpdatedAt: stamp}
	r.Workspaces = append(r.Workspaces, added)
	return added
}

// dirs 登记项 → 服务端用的“工作区 ID → 目录”快照，按 ID 排序。
func (r *workspaceRegistry) dirs() []server.Workspace {
	out := make([]server.Workspace, 0, len(r.Workspaces))
	for _, item := range r.Workspaces {
		out = append(out, server.Workspace{ID: item.ID, Path: item.Path})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// NewWorkspaceID 新的工作区 ID。
func NewWorkspaceID() string {
	buffer := make([]byte, 4)
	_, _ = rand.Read(buffer)
	return "ws-" + hex.EncodeToString(buffer)
}

func workspaceBaseName(path string) string {
	name := filepath.Base(filepath.Clean(path))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return path
	}
	return name
}

// samePath 两个目录是否同一个（Windows 不区分大小写）。
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// absolutePath 转绝对路径；失败时原样返回。
func absolutePath(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute
	}
	return path
}
