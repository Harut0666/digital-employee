// 会话日志：每个会话一份 sessions/<id>/log.jsonl，追加写，每条落盘。
// 会话的状态（员工、草稿、任务 ID）都由日志回放得到，没有第二份存储。
package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"digital-employee/internal/contract"
)

// 消息类型。前四种是人能看到的对话，其次三种是留痕，最后几种只改变会话状态。
const (
	KindUser      = "user"
	KindAssistant = "assistant"
	KindNotice    = "notice" // 程序写的说明，不经过模型
	KindCard      = "card"

	KindToolCalled  = "tool_called"
	KindToolDenied  = "tool_denied"
	KindModelFailed = "model_failed"

	kindCreated         = "created"
	kindEmployeeSet     = "employee_set"
	kindDraftSet        = "draft_set"
	kindAttachmentAdded = "attachment_added"
	kindTaskBound       = "task_bound"
	// kindFilingDrafted 模型为入库卡片起草的描述；起草失败时 Text 为空、Reason 写原因
	kindFilingDrafted = "filing_drafted"
)

// Attachment 暂存在会话里的一份材料，提交时才复制进任务。
type Attachment struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Message 会话日志的一行，也是读取历史时返回的一条消息。
type Message struct {
	ID   string `json:"id"`
	Seq  int64  `json:"seq"`
	TS   string `json:"ts"`
	Kind string `json:"kind"`

	// Role 说话的人（user）
	Role contract.Role `json:"role,omitempty"`
	Text string        `json:"text,omitempty"`
	Card *Card         `json:"card,omitempty"`

	// 工具留痕
	Tool   string `json:"tool,omitempty"`
	Args   any    `json:"args,omitempty"`
	Result any    `json:"result,omitempty"`
	Reason string `json:"reason,omitempty"`

	// 状态变更
	Employee   string         `json:"employee,omitempty"`
	TaskID     string         `json:"taskId,omitempty"`
	Draft      map[string]any `json:"draft,omitempty"`
	Attachment *Attachment    `json:"attachment,omitempty"`
}

// visible 读取历史时是否返回（状态变更已体现在会话字段上）。
func (m Message) visible() bool {
	switch m.Kind {
	case kindCreated, kindEmployeeSet, kindDraftSet, kindAttachmentAdded, kindTaskBound, kindFilingDrafted:
		return false
	}
	return true
}

// record 一个会话回放后的完整状态。
type record struct {
	id          string
	employee    string
	taskID      string
	draft       map[string]any
	attachments []Attachment
	messages    []Message
	createdAt   string
	updatedAt   string
	// filingDraft 入库卡片上模型起草的描述；filingFailed 起草失败，由人手写
	filingDraft  string
	filingFailed bool
}

func (r *record) apply(message Message) {
	r.messages = append(r.messages, message)
	if r.createdAt == "" {
		r.createdAt = message.TS
	}
	r.updatedAt = message.TS
	switch message.Kind {
	case kindEmployeeSet:
		r.employee = message.Employee
	case kindDraftSet:
		r.draft = message.Draft
	case kindTaskBound:
		r.taskID = message.TaskID
	case kindFilingDrafted:
		r.filingDraft = message.Text
		r.filingFailed = message.Text == ""
	case kindAttachmentAdded:
		if message.Attachment == nil {
			return
		}
		for index := range r.attachments {
			if r.attachments[index].Name == message.Attachment.Name {
				r.attachments[index] = *message.Attachment
				return
			}
		}
		r.attachments = append(r.attachments, *message.Attachment)
	}
}

// spoken 是否已有人说过话。
func (r *record) spoken() bool {
	for _, message := range r.messages {
		if message.Kind == KindUser {
			return true
		}
	}
	return false
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// store 工作区里的会话日志。
type store struct {
	dir   string
	now   func() time.Time
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newStore(workspace string, now func() time.Time) *store {
	return &store{dir: filepath.Join(workspace, "sessions"), now: now, locks: map[string]*sync.Mutex{}}
}

func (s *store) sessionDir(id string) string { return filepath.Join(s.dir, id) }

func (s *store) attachmentPath(id, name string) string {
	return filepath.Join(s.sessionDir(id), "attachments", name)
}

func (s *store) logPath(id string) string { return filepath.Join(s.sessionDir(id), "log.jsonl") }

func (s *store) lockOf(id string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock := s.locks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		s.locks[id] = lock
	}
	return lock
}

// append 追加一行并落盘，返回写入的消息（带序号和时间）。
func (s *store) append(id string, message Message) (Message, error) {
	lock := s.lockOf(id)
	lock.Lock()
	defer lock.Unlock()

	current, err := s.read(id)
	if err != nil {
		return Message{}, err
	}
	last := int64(0)
	if current != nil && len(current.messages) > 0 {
		last = current.messages[len(current.messages)-1].Seq
	}
	message.Seq = last + 1
	message.ID = fmt.Sprintf("s%d", message.Seq)
	message.TS = s.now().UTC().Format(time.RFC3339Nano)

	if err := os.MkdirAll(s.sessionDir(id), 0o755); err != nil {
		return Message{}, err
	}
	if err := s.dropTornTail(id); err != nil {
		return Message{}, err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(message); err != nil {
		return Message{}, err
	}
	file, err := os.OpenFile(s.logPath(id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return Message{}, err
	}
	defer file.Close()
	if _, err := file.Write(buffer.Bytes()); err != nil {
		return Message{}, err
	}
	return message, file.Sync()
}

// dropTornTail 截掉末尾没写完的半行，后面追加的内容才不会接在半行上。
func (s *store) dropTornTail(id string) error {
	raw, err := os.ReadFile(s.logPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(raw) == 0 || raw[len(raw)-1] == '\n' {
		return nil
	}
	return os.Truncate(s.logPath(id), int64(bytes.LastIndexByte(raw, '\n')+1))
}

// read 回放一个会话；不存在时返回 nil。
// 末尾的半行是写入中途被杀留下的，丢弃；中间的坏行是日志损坏，报错。
func (s *store) read(id string) (*record, error) {
	raw, err := os.ReadFile(s.logPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\r\n"), "\n")
	current := &record{id: id}
	for index, line := range lines {
		var message Message
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			if index == len(lines)-1 {
				break
			}
			return nil, fmt.Errorf("会话日志损坏：%s 第 %d 行无法解析", s.logPath(id), index+1)
		}
		current.apply(message)
	}
	return current, nil
}

// saveAttachment 把一份材料复制进会话目录，同名覆盖，返回大小。
func (s *store) saveAttachment(id string, file contract.AttachmentInput) (int64, error) {
	source, err := os.Open(file.SourcePath)
	if err != nil {
		return 0, err
	}
	defer source.Close()
	target := s.attachmentPath(id, file.Name)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	out, err := os.Create(target)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	return io.Copy(out, source)
}

// ids 工作区里全部会话的 ID。
func (s *store) ids() []string {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			out = append(out, entry.Name())
		}
	}
	return out
}
