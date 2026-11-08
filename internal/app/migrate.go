// 一次性迁移（Spec 0006）：把 Spec 0005 的数据搬进应用目录。
//
//   - 原默认工作区 %APPDATA%\digital-employee 的设置、员工、知识库，在应用目录里还没有对应内容时复制过去；
//   - 原默认工作区和 T32 登记过的其他工作区的 tasks/、sessions/ 复制进应用目录，归到“我的工作区”；
//   - 原目录只读不删。
//
// 迁移完成写标记文件 <应用目录>/migrated.json；有标记就跳过，所以可以重复运行、结果不变。
// 单目录模式不做迁移（布局本来就和以前一样）。每个文件不同名、任务和会话 ID 全进程唯一，
// 所以复制时不覆盖已有文件：中途失败重跑会补齐缺的部分，不会产生重复。
package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// migrationMarker 迁移完成标记文件名。
const migrationMarker = "migrated.json"

// myWorkspaceName 迁移来的数据归到的工作区名称。
const myWorkspaceName = "我的工作区"

// migrationRecord 标记文件的内容。
type migrationRecord struct {
	MigratedAt  string   `json:"migratedAt"`
	WorkspaceID string   `json:"workspaceId"`
	From        []string `json:"from"`
}

// legacyDefaultWorkspace Spec 0005 的默认工作区目录。
func legacyDefaultWorkspace(env map[string]string) string {
	return filepath.Join(appDataDir(env), "digital-employee")
}

// t32Workspaces T32 登记过的工作区目录（转移之前先读出来）。
// 原登记文件就在应用目录里，迁移时应用目录里的 workspaces.json 会被重新整理。
func t32Workspaces(appDir string) []Workspace {
	registry, err := LoadRegistry(appDir)
	if err != nil {
		return nil
	}
	return registry.Workspaces
}

// Migrate 执行一次性迁移；已完成或单目录模式时什么都不做。返回是否真的做了迁移。
func Migrate(options Options) (bool, error) {
	env := options.Env
	if envValue(env, "DE_WORKSPACE") != "" {
		return false, nil
	}
	appDir := options.AppDir
	if appDir == "" {
		appDir = ResolveAppDir(env)
	}
	if _, err := os.Stat(filepath.Join(appDir, migrationMarker)); err == nil {
		return false, nil
	}
	logger := options.Logger
	logf := func(level, format string, args ...any) {
		if logger == nil {
			return
		}
		switch level {
		case "warn":
			logger.Warnf(format, args...)
		case "debug":
			logger.Debugf(format, args...)
		default:
			logger.Infof(format, args...)
		}
	}

	legacy := legacyDefaultWorkspace(env)
	legacyExists := dirExists(legacy)
	t32 := t32Workspaces(appDir)
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		return false, err
	}

	// 1. 全局内容：旧默认工作区的设置、员工、知识库（应用目录里还没有对应内容时）
	if legacyExists {
		if err := copyIfMissing(filepath.Join(legacy, "settings.json"), filepath.Join(appDir, "settings.json")); err != nil {
			return false, err
		}
		if err := copyDirIfMissing(filepath.Join(legacy, "employees"), filepath.Join(appDir, "employees")); err != nil {
			return false, err
		}
		if err := copyDirIfMissing(filepath.Join(legacy, "knowledge"), filepath.Join(appDir, "knowledge")); err != nil {
			return false, err
		}
		// 审计和诊断记录跟着一起搬，便于核对
		_ = copyIfMissing(filepath.Join(legacy, "audit.jsonl"), filepath.Join(appDir, "audit.jsonl"))
		_ = copyDirIfMissing(filepath.Join(legacy, "diagnostics"), filepath.Join(appDir, "diagnostics"))
	}

	// 2. 任务和会话：旧默认工作区与 T32 的工作区，都归到“我的工作区”
	sources := []string{}
	if legacyExists {
		sources = append(sources, legacy)
	}
	for _, item := range t32 {
		if dirExists(item.Path) && !samePath(item.Path, legacy) && !samePath(item.Path, appDir) {
			sources = append(sources, item.Path)
		}
	}

	if len(sources) == 0 {
		// 没有旧数据：只留标记，不再处理
		return writeMigrationMarker(appDir, "", nil)
	}

	workspaceDir := DefaultWorkspaceDir(env)
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return false, err
	}
	registry, err := LoadRegistry(appDir)
	if err != nil {
		return false, err
	}
	// T32 登记过的工作区按原样保留（名称、ID、时间），只是去掉“当前工作区”
	if len(t32) > 0 {
		registry.Workspaces = t32
	}
	mine := registry.add(myWorkspaceName, workspaceDir, time.Now().UTC())
	// T32 登记里可能已经有一个同目录的工作区（重名不算重复）：迁移来的数据归到它名下
	if err := registry.save(); err != nil {
		return false, err
	}

	for _, source := range sources {
		if err := copyDirIfMissing(filepath.Join(source, "tasks"), filepath.Join(appDir, "tasks")); err != nil {
			return false, err
		}
		if err := copyDirIfMissing(filepath.Join(source, "sessions"), filepath.Join(appDir, "sessions")); err != nil {
			return false, err
		}
	}
	// 旧的任务和会话日志里没有工作区：补写进应用目录里的副本（原目录不动）
	stampTasks(filepath.Join(appDir, "tasks"), mine.ID)
	stampSessions(filepath.Join(appDir, "sessions"), mine.ID)
	logf("info", "已迁移旧数据到应用目录 %s，任务和会话归到工作区“%s”（%s）", appDir, mine.Name, workspaceDir)
	return writeMigrationMarker(appDir, mine.ID, sources)
}

// writeMigrationMarker 写迁移标记文件。
func writeMigrationMarker(appDir, workspaceID string, sources []string) (bool, error) {
	record := migrationRecord{
		MigratedAt: time.Now().UTC().Format(time.RFC3339), WorkspaceID: workspaceID, From: sources,
	}
	if record.From == nil {
		record.From = []string{}
	}
	data, err := marshalIndentJSON(record)
	if err != nil {
		return false, err
	}
	marker := filepath.Join(appDir, migrationMarker)
	temp := marker + ".tmp"
	if err := os.WriteFile(temp, data, 0o644); err != nil {
		return false, err
	}
	if err := os.Rename(temp, marker); err != nil {
		return false, err
	}
	return len(sources) > 0, nil
}

// copyIfMissing 目标不存在时复制单个文件。
func copyIfMissing(source, target string) error {
	info, err := os.Stat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return copyDirIfMissing(source, target)
	}
	if _, err := os.Stat(target); err == nil {
		return nil
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// 保留修改时间，方便按原有先后排序
	return os.Chtimes(target, time.Now(), info.ModTime())
}

// copyDirIfMissing 递归复制目录里目标还没有的文件；原目录只读，不改动任何东西。
func copyDirIfMissing(source, target string) error {
	if !dirExists(source) {
		return nil
	}
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
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
		return copyIfMissing(path, destination)
	})
}

// stampTasks 迁移来的任务：发起事件里没有工作区时补上，之后读任务时列表和会话都能归到它。
func stampTasks(tasksDir, workspaceID string) {
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		_ = stampTaskLog(filepath.Join(tasksDir, entry.Name(), "events.jsonl"), workspaceID)
	}
}

// stampTaskLog 补写一条任务日志的发起事件：任务属于 workspaceID。
//
// 日志第一行必须是发起事件；第一行不是合法 JSON 或已经不是发起事件时不碰
// （损坏的日志保持原样，由引擎的带错摘要处理）。
func stampTaskLog(path, workspaceID string) error {
	return rewriteLog(path, "type", "task_created", func(record map[string]any) {
		record["workspaceId"] = workspaceID
	})
}

// stampSessions 迁移来的会话：日志头（created 行）里补上工作区，之后按工作区过滤时也能找到它们。
func stampSessions(sessionsDir, workspaceID string) {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		_ = rewriteLog(filepath.Join(sessionsDir, entry.Name(), "log.jsonl"), "kind", "created", func(record map[string]any) {
			record["workspaceId"] = workspaceID
		})
	}
}

// rewriteLog 逐行读一份 JSONL 日志：第一行的 key 字段等于 kind、且还没有 workspaceId 时补上并落盘；
// 已经有、或者第一行不是这类记录时什么都不做。原文件只读不改。
func rewriteLog(path, key, kind string, patch func(map[string]any)) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(raw)
	lines := strings.Split(strings.TrimRight(text, "\r\n"), "\n")
	if len(lines) == 0 {
		return nil
	}
	first := map[string]any{}
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil || first[key] != kind {
		return err
	}
	if _, ok := first["workspaceId"]; ok && first["workspaceId"] != "" {
		return nil
	}
	patch(first)
	data, err := json.Marshal(first)
	if err != nil {
		return err
	}
	lines[0] = string(data)
	output := strings.Join(lines, "\n")
	if strings.HasSuffix(text, "\n") {
		output += "\n"
	}
	temp := fmt.Sprintf("%s.migrate.tmp", path)
	if err := os.WriteFile(temp, []byte(output), 0o644); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
