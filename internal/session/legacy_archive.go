// 旧任务推导出的只读会话的归档状态。
//
// 这类会话（ID 以 task- 开头）没有会话日志，归档标记没处写，所以单独记在应用目录的
// legacy-archived.json 里（和 sessions/ 目录并排）：{"archived":["task-…", …]}。
// 进程内用一把锁串行读写，落盘走“临时文件 + 原子替换”，写到一半失败时原文件不受影响。
package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// legacyArchiveFile 旧任务会话归档状态的文件名。
const legacyArchiveFile = "legacy-archived.json"

type legacyArchive struct {
	path string
	mu   sync.Mutex
	// ids 已归档的旧任务会话 ID；nil 表示还没读过文件
	ids map[string]bool
}

func newLegacyArchive(dir string) *legacyArchive {
	return &legacyArchive{path: filepath.Join(dir, legacyArchiveFile)}
}

// load 第一次用到时读文件；文件不存在或内容损坏都当作没有归档过（下一次写会整份覆盖）。
// 调用时必须已持有 mu。
func (a *legacyArchive) load() map[string]bool {
	if a.ids != nil {
		return a.ids
	}
	a.ids = map[string]bool{}
	raw, err := readFileRetry(a.path)
	if err != nil {
		return a.ids
	}
	var doc struct {
		Archived []string `json:"archived"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return a.ids
	}
	for _, id := range doc.Archived {
		a.ids[id] = true
	}
	return a.ids
}

// has 这个旧任务会话是不是已归档。
func (a *legacyArchive) has(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.load()[id]
}

// set 归档或取消归档；已经是这个状态时什么也不写。落盘失败时内存里的状态不变。
func (a *legacyArchive) set(id string, archived bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	current := a.load()
	if current[id] == archived {
		return nil
	}
	next := make(map[string]bool, len(current)+1)
	for key := range current {
		next[key] = true
	}
	if archived {
		next[id] = true
	} else {
		delete(next, id)
	}
	ids := make([]string, 0, len(next))
	for key := range next {
		ids = append(ids, key)
	}
	sort.Strings(ids)
	data, err := json.MarshalIndent(map[string][]string{"archived": ids}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(a.path, append(data, '\n')); err != nil {
		return err
	}
	a.ids = next
	return nil
}
