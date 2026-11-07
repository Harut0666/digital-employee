package knowledge

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"digital-employee/internal/contract"
)

// 知识库的业务错误码（contract 里的通用错误码之外）。
const (
	// ErrNotDelivered 任务还没交付，它的文件不能入库
	ErrNotDelivered = "not_delivered"
)

// Error 带错误码的业务错误，HTTP 层据此定状态码。
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func failure(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Submission 一次入库：哪个任务的哪份交付物，人确认过的标题、描述和标签。
type Submission struct {
	Task *contract.TaskSnapshot
	// Kind 交付物类型；空串取任务的第一份交付物（检验记录不算）
	Kind contract.DeliverableKind
	// Title 空串时用文件名
	Title       string
	Description string
	Tags        []string
}

// deliverable 找要入库的交付物。检验记录是给审核人看的，不是交付给业务的文件。
func deliverable(task *contract.TaskSnapshot, kind contract.DeliverableKind) (*contract.DeliverableRef, error) {
	if kind == contract.DeliverableQCRecord {
		return nil, failure(contract.ErrInvalidInput, "检验记录不是交付物，不能放进知识库。")
	}
	for index := range task.Deliverables {
		ref := &task.Deliverables[index]
		if ref.Kind == contract.DeliverableQCRecord {
			continue
		}
		if kind == "" || ref.Kind == kind {
			return ref, nil
		}
	}
	return nil, failure(contract.ErrNotFound, "任务没有可入库的交付物：%s", kind)
}

func cleanTags(tags []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag != "" && !seen[tag] {
			seen[tag] = true
			out = append(out, tag)
		}
	}
	return out
}

func newID(now time.Time) string {
	buffer := make([]byte, 3)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("kb-%d", now.UnixNano())
	}
	return fmt.Sprintf("kb-%s-%x", now.UTC().Format("20060102150405"), buffer)
}

// Add 入库：复制交付物，抽取正文，写进索引。只接受已交付任务的交付物，描述不能为空。
func (s *Store) Add(submission Submission) (Entry, error) {
	task := submission.Task
	if task.State != contract.StateDelivered {
		return Entry{}, failure(ErrNotDelivered, "任务还没有交付，它的文件不能放进知识库。")
	}
	description := strings.TrimSpace(submission.Description)
	if description == "" {
		return Entry{}, failure(contract.ErrInvalidInput, "请写一句描述：员工先看描述再决定要不要读正文。")
	}
	ref, err := deliverable(task, submission.Kind)
	if err != nil {
		return Entry{}, err
	}
	content, err := os.ReadFile(ref.Path)
	if err != nil {
		return Entry{}, failure(contract.ErrNotFound, "交付物文件不见了：%s", filepath.Base(ref.Path))
	}
	text, err := extractText(filepath.Base(ref.Path), content)
	if err != nil {
		return Entry{}, failure(contract.ErrInvalidInput, "读不出交付物的正文：%v", err)
	}

	now := time.Now()
	fileName := filepath.Base(ref.Path)
	title := strings.TrimSpace(submission.Title)
	if title == "" {
		title = strings.TrimSuffix(fileName, filepath.Ext(fileName))
	}
	entry := Entry{
		ID: newID(now), Title: title, Description: description, Tags: cleanTags(submission.Tags),
		SourceTask: task.ID, SourceEmployee: task.Employee, CreatedAt: now.UTC().Format(time.RFC3339), FileName: fileName,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.readIndex()
	if err != nil {
		return Entry{}, err
	}
	dir := s.entryDir(entry.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Entry{}, err
	}
	written := func() error {
		if err := os.WriteFile(filepath.Join(dir, fileName), content, 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, textFile), []byte(text), 0o644); err != nil {
			return err
		}
		return s.writeIndex(append(entries, entry))
	}()
	if written != nil {
		_ = os.RemoveAll(dir)
		return Entry{}, written
	}
	return entry, nil
}
