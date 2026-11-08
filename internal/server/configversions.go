// 配置版本（docs/design.md §4.1、Spec 0002 的“配置版本”约定）。
// 移植自 packages/core/src/config/versions.ts：
//
// 每次保存前把当前配置复制到 <workspace>/config-versions/<员工>/<时间戳>-<哈希短码>/，
// 目录里放配置文件副本和 .version.json（时间、整份配置哈希、每个文件的哈希）。
// 回滚 = 先把当前内容留一个快照，再把目标版本的文件复制回来；回滚本身因此也产生一个新版本。
package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const versionMetaFile = ".version.json"

// configVersion 配置版本元数据（GET 返回的形状）。
type configVersion struct {
	ID        string `json:"id"`
	CreatedAt string `json:"createdAt"`
	Hash      string `json:"hash"`
}

// versionMeta 磁盘上的版本元数据：多一个 files 映射（相对路径 → 文件 sha256）。
type versionMeta struct {
	configVersion
	Files map[string]string `json:"files"`
}

// configDiffFile 一个文件的差异。
type configDiffFile struct {
	Path   string   `json:"path"`
	Status string   `json:"status"`
	Hunks  []string `json:"hunks"`
}

// configDiff 版本与当前配置的差异（GET .../diff 的响应）。
type configDiff struct {
	Version    configVersion    `json:"version"`
	HasChanges bool             `json:"hasChanges"`
	Files      []configDiffFile `json:"files"`
}

// restoreResult 回滚结果。
type restoreResult struct {
	Snapshot     configVersion `json:"snapshot"`
	RestoredFrom string        `json:"restoredFrom"`
}

func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func hashConfigFiles(files map[string]string) string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		hash.Write([]byte(path))
		hash.Write([]byte{0})
		hash.Write([]byte(files[path]))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// versionsDirFor 员工配置版本存放处：<应用目录>/config-versions/<员工 id>。所有工作区共用。
func versionsDirFor(appDir, employeeID string) string {
	return filepath.Join(filepath.Clean(appDir), "config-versions", employeeID)
}

// compactVersionStamp ISO 时间 → 目录名用的紧凑格式（不含冒号、点，Windows 也能当目录名）。
func compactVersionStamp(at time.Time) string {
	return strings.NewReplacer("-", "", ":", "", ".", "").Replace(at.UTC().Format("2006-01-02T15:04:05.000Z"))
}

func writeTextFile(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

func readVersionMeta(dir string) (*versionMeta, bool) {
	data, err := os.ReadFile(filepath.Join(dir, versionMetaFile))
	if err != nil {
		return nil, false
	}
	var meta versionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, false
	}
	if meta.ID == "" || meta.CreatedAt == "" || meta.Hash == "" || meta.Files == nil {
		return nil, false
	}
	return &meta, true
}

// createConfigVersion 给当前配置拍一个版本快照。
func createConfigVersion(appDir, employeeID, employeeDir string, now func() time.Time) (configVersion, error) {
	root := versionsDirFor(appDir, employeeID)
	files := readConfigFiles(employeeDir)
	hash := hashConfigFiles(files)
	at := now()
	stamp := compactVersionStamp(at)
	id := stamp + "-" + hash[:8]
	for index := 2; ; index++ {
		if _, err := os.Stat(filepath.Join(root, id)); os.IsNotExist(err) {
			break
		}
		id = fmt.Sprintf("%s-%s-%d", stamp, hash[:8], index)
	}
	meta := versionMeta{
		configVersion: configVersion{ID: id, CreatedAt: at.UTC().Format("2006-01-02T15:04:05.000Z"), Hash: hash},
		Files:         map[string]string{},
	}
	for path, text := range files {
		meta.Files[path] = sha256Hex(text)
	}
	dir := filepath.Join(root, id)
	for path, text := range files {
		if err := writeTextFile(filepath.Join(dir, filepath.FromSlash(path)), text); err != nil {
			return configVersion{}, err
		}
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return configVersion{}, err
	}
	if err := writeTextFile(filepath.Join(dir, versionMetaFile), string(data)); err != nil {
		return configVersion{}, err
	}
	return meta.configVersion, nil
}

// listConfigVersions 列出版本，新的在前。
func listConfigVersions(appDir, employeeID string) []configVersion {
	root := versionsDirFor(appDir, employeeID)
	entries, err := os.ReadDir(root)
	if err != nil {
		return []configVersion{}
	}
	versions := []configVersion{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if meta, ok := readVersionMeta(filepath.Join(root, entry.Name())); ok {
			versions = append(versions, meta.configVersion)
		}
	}
	sort.SliceStable(versions, func(i, j int) bool {
		if versions[i].CreatedAt == versions[j].CreatedAt {
			return versions[i].ID > versions[j].ID
		}
		return versions[i].CreatedAt > versions[j].CreatedAt
	})
	return versions
}

var versionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func versionDir(appDir, employeeID, versionID string) (string, bool) {
	if !versionIDPattern.MatchString(versionID) {
		return "", false
	}
	root := versionsDirFor(appDir, employeeID)
	dir := filepath.Join(root, versionID)
	if filepath.Clean(dir) != filepath.Join(root, versionID) {
		return "", false
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return dir, true
}

// removeConfigVersion 删除一个版本目录（保存写盘后整体校验失败时回滚用）。
func removeConfigVersion(appDir, employeeID, versionID string) bool {
	dir, ok := versionDir(appDir, employeeID, versionID)
	if !ok {
		return false
	}
	return os.RemoveAll(dir) == nil
}

// readConfigVersion 读回一个版本的全部文件内容；版本不存在时返回 false。
func readConfigVersion(appDir, employeeID, versionID string) (map[string]string, bool) {
	dir, ok := versionDir(appDir, employeeID, versionID)
	if !ok {
		return nil, false
	}
	meta, ok := readVersionMeta(dir)
	if !ok {
		return nil, false
	}
	files := map[string]string{}
	for path := range meta.Files {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
		if err != nil {
			continue
		}
		files[path] = string(data)
	}
	return files, true
}

// ---------------------------------------------------------------- 行级差异

type diffOp struct {
	Kind string
	Text string
}

func lineDiff(before, after string) []string {
	a := strings.Split(before, "\n")
	b := strings.Split(after, "\n")
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	ops := []diffOp{}
	i, j := 0, 0
	for i < n && j < m {
		if a[i] == b[j] {
			ops = append(ops, diffOp{" ", a[i]})
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			ops = append(ops, diffOp{"-", a[i]})
			i++
		} else {
			ops = append(ops, diffOp{"+", b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{"-", a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{"+", b[j]})
	}

	changed := []int{}
	for index, op := range ops {
		if op.Kind != " " {
			changed = append(changed, index)
		}
	}
	if len(changed) == 0 {
		return []string{}
	}
	const contextLines = 3
	type group struct{ from, to int }
	groups := []group{}
	start := changed[0] - contextLines
	if start < 0 {
		start = 0
	}
	end := changed[0] + contextLines
	if end > len(ops)-1 {
		end = len(ops) - 1
	}
	for _, index := range changed[1:] {
		if index-end > contextLines*2 {
			groups = append(groups, group{start, end})
			start = index - contextLines
			if start < 0 {
				start = 0
			}
		}
		end = index + contextLines
		if end > len(ops)-1 {
			end = len(ops) - 1
		}
	}
	groups = append(groups, group{start, end})
	out := []string{}
	for _, g := range groups {
		out = append(out, fmt.Sprintf("@@ -%d,%d @@", g.from+1, g.to-g.from+1))
		for _, op := range ops[g.from : g.to+1] {
			out = append(out, op.Kind+op.Text)
		}
	}
	return out
}

// diffConfigVersion 版本与当前配置的差异；版本不存在时返回 false。
func diffConfigVersion(appDir, employeeID, employeeDir, versionID string) (configDiff, bool) {
	dir, ok := versionDir(appDir, employeeID, versionID)
	if !ok {
		return configDiff{}, false
	}
	meta, ok := readVersionMeta(dir)
	if !ok {
		return configDiff{}, false
	}
	before, _ := readConfigVersion(appDir, employeeID, meta.ID)
	if before == nil {
		before = map[string]string{}
	}
	current := readConfigFiles(employeeDir)
	pathSet := map[string]bool{}
	for path := range before {
		pathSet[path] = true
	}
	for path := range current {
		pathSet[path] = true
	}
	paths := make([]string, 0, len(pathSet))
	for path := range pathSet {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	files := []configDiffFile{}
	for _, path := range paths {
		oldText, oldOK := before[path]
		newText, newOK := current[path]
		switch {
		case !oldOK && newOK:
			files = append(files, configDiffFile{Path: path, Status: "added", Hunks: lineDiff("", newText)})
		case oldOK && !newOK:
			files = append(files, configDiffFile{Path: path, Status: "removed", Hunks: lineDiff(oldText, "")})
		case oldOK && newOK && oldText != newText:
			files = append(files, configDiffFile{Path: path, Status: "modified", Hunks: lineDiff(oldText, newText)})
		}
	}
	return configDiff{Version: meta.configVersion, HasChanges: len(files) > 0, Files: files}, true
}

// restoreConfigVersion 回滚：先给当前配置留快照，再把版本内容复制回来（版本里没有的配置文件会删掉）。
func restoreConfigVersion(appDir, employeeID, employeeDir, versionID string, now func() time.Time) (restoreResult, bool) {
	dir, ok := versionDir(appDir, employeeID, versionID)
	if !ok {
		return restoreResult{}, false
	}
	meta, ok := readVersionMeta(dir)
	if !ok {
		return restoreResult{}, false
	}
	files, ok := readConfigVersion(appDir, employeeID, meta.ID)
	if !ok {
		return restoreResult{}, false
	}
	snapshot, err := createConfigVersion(appDir, employeeID, employeeDir, now)
	if err != nil {
		return restoreResult{}, false
	}
	for _, path := range listConfigFiles(employeeDir) {
		if _, keep := files[path]; !keep {
			_ = os.Remove(filepath.Join(employeeDir, filepath.FromSlash(path)))
		}
	}
	for path, text := range files {
		_ = writeTextFile(filepath.Join(employeeDir, filepath.FromSlash(path)), text)
	}
	return restoreResult{Snapshot: snapshot, RestoredFrom: meta.ID}, true
}
