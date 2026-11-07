package knowledge

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"digital-employee/internal/contract"
)

// textFile 条目目录里抽取的正文。
const textFile = "text.txt"

// Store 一个工作区的知识库。同一进程里服务端和引擎各持一个，写入只发生在服务端。
type Store struct {
	dir string
	mu  sync.Mutex
}

// Open 打开工作区里的知识库；目录在第一次入库时才创建。
func Open(workspaceDir string) *Store {
	return &Store{dir: filepath.Join(workspaceDir, "knowledge")}
}

func (s *Store) indexPath() string { return filepath.Join(s.dir, "index.json") }

// entryDir 条目目录。ID 由本包生成，只在索引里查到后才拼路径，不会越出知识库目录。
func (s *Store) entryDir(id string) string { return filepath.Join(s.dir, id) }

// readIndex 读索引；还没有入库过时返回空表。
func (s *Store) readIndex() ([]Entry, error) {
	raw, err := os.ReadFile(s.indexPath())
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	entries := []Entry{}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// writeIndex 先写临时文件再改名，写到一半崩溃也不会留下半个索引。
func (s *Store) writeIndex(entries []Entry) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	temporary := s.indexPath() + ".tmp"
	if err := os.WriteFile(temporary, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, s.indexPath())
}

// List 全部条目，按入库顺序。
func (s *Store) List() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readIndex()
}

// FiledFrom 某个任务最近一次入库的条目 ID；没入库过时为空。
func (s *Store) FiledFrom(taskID string) (string, error) {
	entries, err := s.List()
	if err != nil {
		return "", err
	}
	for index := len(entries) - 1; index >= 0; index-- {
		if entries[index].SourceTask == taskID {
			return entries[index].ID, nil
		}
	}
	return "", nil
}

// find 在索引里按 ID 找条目。
func find(entries []Entry, id string) (int, bool) {
	for index, entry := range entries {
		if entry.ID == id {
			return index, true
		}
	}
	return -1, false
}

func notFound(id string) error {
	return failure(contract.ErrNotFound, "知识库里没有这个条目：%s", id)
}

// Read 按 ID 取条目和正文。
func (s *Store) Read(id string) (Entry, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.readIndex()
	if err != nil {
		return Entry{}, "", err
	}
	index, ok := find(entries, id)
	if !ok {
		return Entry{}, "", notFound(id)
	}
	text, err := os.ReadFile(filepath.Join(s.entryDir(id), textFile))
	if err != nil {
		return Entry{}, "", err
	}
	return entries[index], string(text), nil
}

// Delete 删除条目：先从索引去掉，再删文件。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.readIndex()
	if err != nil {
		return err
	}
	index, ok := find(entries, id)
	if !ok {
		return notFound(id)
	}
	if err := s.writeIndex(append(entries[:index:index], entries[index+1:]...)); err != nil {
		return err
	}
	return os.RemoveAll(s.entryDir(id))
}
