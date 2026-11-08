// 会话日志：每个会话一份 <应用目录>/sessions/<id>/log.jsonl，追加写，每条落盘。
// 会话的状态（工作区、员工、草稿、任务 ID）都由日志回放得到，没有第二份存储。
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

	kindCreated = "created"
	// kindModelSelected 人在会话里选了模型（Spec 0006“会话模型”）；Model 记下选的是哪个
	kindModelSelected   = "model_selected"
	kindEmployeeSet     = "employee_set"
	kindDraftSet        = "draft_set"
	kindAttachmentAdded = "attachment_added"
	kindTaskBound       = "task_bound"
	// kindFilingDrafted 模型为入库卡片起草的描述；起草失败时 Text 为空、Reason 写原因
	kindFilingDrafted = "filing_drafted"
	// kindChecklistSkipped 发起人确认跳过一项开工清单（Item），同时写一条说明留痕
	kindChecklistSkipped = "checklist_skipped"
	// KindTurnText 前台 Agent 一轮里的文字增量：同一轮边到边追加进同一条。一轮结束时另写完整回复；
	// 读取会话时把它们按轮次还回原来在对话里的位置，所以来晚的客户端也能逐字看到已经说出的部分。
	KindTurnText = "turn_text"
	// kindRename 人改了会话标题
	kindRename = "rename"
	// kindArchive 人归档或取消归档会话
	kindArchive = "archive"
	// kindRead 人打开了会话：写到最后一个已读的消息序号（UpTo）
	kindRead = "read"
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
	// WorkspaceID 只在第一行（created）里：会话属于哪个工作区
	WorkspaceID string         `json:"workspaceId,omitempty"`
	Employee    string         `json:"employee,omitempty"`
	TaskID      string         `json:"taskId,omitempty"`
	Draft       map[string]any `json:"draft,omitempty"`
	Attachment  *Attachment    `json:"attachment,omitempty"`
	// ConfirmedPath 这份材料是人在哪张“使用目录文件”卡片上确认的（目录里的相对路径）
	ConfirmedPath string `json:"confirmedPath,omitempty"`
	// Item 开工清单项的 id
	Item string `json:"item,omitempty"`
	// Turn 前台 Agent 回复的轮次编号（T40）：一轮结束时写入的消息都带着它。
	// 文字增量在事件流里推给前端，但会话读取按日志重画对话，所以增量也按轮次记一份，
	// 来晚的客户端和重连之后的客户端能看到完整的那一轮。
	Turn int64 `json:"turn,omitempty"`
	// Model 会话模型：model_selected 选的是哪个；selected 为 true 表示会话里明确选过
	Model    *contract.ModelRef `json:"model,omitempty"`
	Selected bool               `json:"selected,omitempty"`
	// Title 人给会话起的标题（rename）
	Title string `json:"title,omitempty"`
	// Archived 会话是否归档（archive）
	Archived bool `json:"archived,omitempty"`
	// UpTo 已读到哪一条消息（read）
	UpTo int64 `json:"upTo,omitempty"`
}

// visible 读取历史时是否返回（状态变更已体现在会话字段上）。
func (m Message) visible() bool {
	switch m.Kind {
	case kindCreated, kindModelSelected, kindEmployeeSet, kindDraftSet, kindAttachmentAdded, kindTaskBound, kindFilingDrafted, kindChecklistSkipped,
		kindRename, kindArchive, kindRead, KindTurnText:
		return false
	}
	return true
}

// record 一个会话回放后的完整状态。
type record struct {
	id string
	// workspaceID 会话属于哪个工作区
	workspaceID string
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
	// skipped 发起人确认跳过的清单项
	skipped map[string]bool
	// model 会话里最后一次选的模型；nil 表示没选过，沿用设置的 lastModel
	model *contract.ModelRef
	// title 人改过的标题；空串表示取第一句话
	title string
	// archived 归档的会话默认不在列表里出现
	archived bool
	// readUpTo 已读到哪一条消息：最后一条消息的序号比它大就是有新消息
	readUpTo int64
}

func (r *record) apply(message Message) {
	r.messages = append(r.messages, message)
	if r.createdAt == "" {
		r.createdAt = message.TS
	}
	r.updatedAt = message.TS
	switch message.Kind {
	case kindCreated:
		r.workspaceID = message.WorkspaceID
	case kindModelSelected:
		r.model = message.Model
	case kindEmployeeSet:
		r.employee = message.Employee
	case kindDraftSet:
		r.draft = message.Draft
	case kindTaskBound:
		r.taskID = message.TaskID
	case kindChecklistSkipped:
		if r.skipped == nil {
			r.skipped = map[string]bool{}
		}
		r.skipped[message.Item] = true
	case kindFilingDrafted:
		r.filingDraft = message.Text
		r.filingFailed = message.Text == ""
	case kindRename:
		r.title = message.Title
	case kindArchive:
		r.archived = message.Archived
	case kindRead:
		// 只有已读之后又来新消息，或者读的位置往前移（读取时补写的已读反过来），才动记录时间：
		// 打开会话不该把会话排到最前面
		if message.UpTo > r.lastSeq() || message.UpTo < r.readUpTo {
			r.updatedAt = recentUpdatedAt(r.messages)
		}
		r.readUpTo = message.UpTo
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

// recentUpdatedAt 不算已读的那条，最近一次有内容的更新时间。
func recentUpdatedAt(messages []Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Kind != kindRead {
			return messages[index].TS
		}
	}
	return ""
}

// lastSeq 最后一条消息的序号。
func (r *record) lastSeq() int64 {
	if len(r.messages) == 0 {
		return 0
	}
	return r.messages[len(r.messages)-1].Seq
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

// store 应用目录里的会话日志：所有工作区的会话都在这里，各自在日志第一行记下工作区。
type store struct {
	dir   string
	now   func() time.Time
	mu    sync.Mutex
	locks map[string]*sessionLock
}

// sessionLock 一个会话的写锁，可重入：前台 Agent 的一轮拿着它跑整轮，期间它自己写日志（append、update）
// 会再进来一次；人的操作用 TryLock，拿到不到就说明这一轮在跑。
type sessionLock struct {
	mu    sync.Mutex
	owner int64
	depth int
}

// TryLock 由 v 试着占住；已经由 v 占着时只加深一层。
func (l *sessionLock) TryLock(v int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owner == v {
		l.depth++
		return true
	}
	if l.owner != 0 {
		return false
	}
	l.owner, l.depth = v, 1
	return true
}

// Lock 由 v 占住，等着也要占到。
func (l *sessionLock) Lock(v int64) {
	for !l.TryLock(v) {
		time.Sleep(2 * time.Millisecond)
	}
}

// Unlock 由 v 放掉一层；不是 v 拿着时什么也不做。
func (l *sessionLock) Unlock(v int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owner != v {
		return
	}
	l.depth--
	if l.depth == 0 {
		l.owner = 0
	}
}

func newStore(dir string, now func() time.Time) *store {
	return &store{dir: filepath.Join(dir, "sessions"), now: now, locks: map[string]*sessionLock{}}
}

func (s *store) sessionDir(id string) string { return filepath.Join(s.dir, id) }

func (s *store) attachmentPath(id, name string) string {
	return filepath.Join(s.sessionDir(id), "attachments", name)
}

func (s *store) logPath(id string) string { return filepath.Join(s.sessionDir(id), "log.jsonl") }

func (s *store) lockOf(id string) *sessionLock {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock := s.locks[id]
	if lock == nil {
		lock = &sessionLock{}
		s.locks[id] = lock
	}
	return lock
}

// background 后台任务（Agent 的一轮）的锁身份：人的操作没有身份（0），与它们互斥。
const background = -1

// append 追加一行并落盘，返回写入的消息（带序号和时间）。
func (s *store) append(id string, message Message) (Message, error) {
	lock := s.lockOf(id)
	lock.Lock(background)
	defer lock.Unlock(background)

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
	line, err := encodeLine(message)
	if err != nil {
		return Message{}, err
	}
	file, err := os.OpenFile(s.logPath(id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return Message{}, err
	}
	defer file.Close()
	if _, err := file.Write([]byte(line)); err != nil {
		return Message{}, err
	}
	return message, file.Sync()
}

// update 改写一行（文字增量追加到同一轮的那一条），返回改写后的消息；序号、ID 不变，时间取现在。
func (s *store) update(id string, seq int64, change func(*Message)) (Message, error) {
	lock := s.lockOf(id)
	lock.Lock(background)
	defer lock.Unlock(background)

	raw, err := os.ReadFile(s.logPath(id))
	if err != nil {
		return Message{}, err
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\r\n"), "\n")
	for index, line := range lines {
		var message Message
		if err := json.Unmarshal([]byte(line), &message); err != nil || message.Seq != seq {
			continue
		}
		change(&message)
		message.TS = s.now().UTC().Format(time.RFC3339Nano)
		encoded, err := encodeLine(message)
		if err != nil {
			return Message{}, err
		}
		lines[index] = strings.TrimRight(encoded, "\n")
		return message, os.WriteFile(s.logPath(id), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	}
	return Message{}, fmt.Errorf("会话日志里没有第 %d 条", seq)
}

// encodeLine 一条消息编成日志的一行。
func encodeLine(message Message) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(message); err != nil {
		return "", err
	}
	return buffer.String(), nil
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

// note 顺手在会话日志里留一条说明；会话不存在或写不进去时什么也不做。
func (s *store) note(id string, text string) error {
	current, err := s.read(id)
	if err != nil || current == nil {
		return err
	}
	_, err = s.append(id, Message{Kind: KindNotice, Text: text})
	return err
}

// entry 应用目录里的一个会话。
type entry struct {
	ID string
	// ModTime 会话目录的修改时间（列表时用来判断要不要重读）
	ModTime time.Time
}

// entries 应用目录里全部会话。
func (s *store) entries() []entry {
	dirs, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	out := make([]entry, 0, len(dirs))
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		item := entry{ID: dir.Name()}
		if info, err := dir.Info(); err == nil {
			item.ModTime = info.ModTime()
		}
		out = append(out, item)
	}
	return out
}

// ids 应用目录里全部会话的 ID。
func (s *store) ids() []string {
	out := []string{}
	for _, item := range s.entries() {
		out = append(out, item.ID)
	}
	return out
}
