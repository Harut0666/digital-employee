// 工作区列表：保存在所有工作区之外的应用级文件 <应用目录>/workspaces.json。
//
// 应用目录：DE_APP_DIR > %APPDATA%/digital-employee-app。
// 环境变量 DE_WORKSPACE 指定工作区时不读写这个文件，切换入口只读。
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
	"strings"
)

// workspaceListFile 应用目录里的工作区列表文件名。
const workspaceListFile = "workspaces.json"

// Workspace 列表里的一个工作区。
type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// workspaceRegistry 工作区列表文件的内容。
type workspaceRegistry struct {
	file       string
	Current    string      `json:"current"`
	Workspaces []Workspace `json:"workspaces"`
}

// ResolveAppDir 应用目录：DE_APP_DIR > %APPDATA%/digital-employee-app。
func ResolveAppDir(env map[string]string) string {
	if dir := envValue(env, "DE_APP_DIR"); dir != "" {
		return dir
	}
	appData := envValue(env, "APPDATA")
	if appData == "" {
		if home, err := os.UserHomeDir(); err == nil {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
	}
	if appData == "" {
		appData = "."
	}
	return filepath.Join(appData, "digital-employee-app")
}

// loadWorkspaceRegistry 读应用目录里的工作区列表；文件不存在时是空列表。
func loadWorkspaceRegistry(appDir string) (*workspaceRegistry, error) {
	registry := &workspaceRegistry{file: filepath.Join(appDir, workspaceListFile), Workspaces: []Workspace{}}
	raw, err := os.ReadFile(registry.file)
	if errors.Is(err, os.ErrNotExist) {
		return registry, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取工作区列表失败：%w", err)
	}
	if err := json.Unmarshal(raw, registry); err != nil {
		return nil, fmt.Errorf("工作区列表 %s 不是合法的 JSON：%w", registry.file, err)
	}
	if registry.Workspaces == nil {
		registry.Workspaces = []Workspace{}
	}
	return registry, nil
}

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

// currentPath 当前工作区的目录；没有时为空。
func (r *workspaceRegistry) currentPath() string {
	if found, ok := r.find(r.Current); ok {
		return found.Path
	}
	return ""
}

func (r *workspaceRegistry) find(id string) (Workspace, bool) {
	for _, item := range r.Workspaces {
		if item.ID == id {
			return item, true
		}
	}
	return Workspace{}, false
}

func (r *workspaceRegistry) findPath(path string) (Workspace, bool) {
	for _, item := range r.Workspaces {
		if samePath(item.Path, path) {
			return item, true
		}
	}
	return Workspace{}, false
}

// ensure 目录已在列表里就返回它，否则以目录名为名称加进去。
func (r *workspaceRegistry) ensure(path string) Workspace {
	if found, ok := r.findPath(path); ok {
		return found
	}
	added := Workspace{ID: newWorkspaceID(), Name: workspaceBaseName(path), Path: path}
	r.Workspaces = append(r.Workspaces, added)
	return added
}

func newWorkspaceID() string {
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
