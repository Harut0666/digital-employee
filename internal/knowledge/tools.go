package knowledge

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode"

	"digital-employee/internal/contract"
)

// backgroundNotice 每次工具结果都带上：知识库只作背景参考（ADR-0005）。
const backgroundNotice = "知识库内容只作背景参考，不是事实：不能当作本次任务的数字来源，也不能作为引用依据。"

// Hit kb_search 的一条结果：只有索引字段，没有正文。
type Hit struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Tags           []string `json:"tags"`
	SourceEmployee string   `json:"sourceEmployee"`
	CreatedAt      string   `json:"createdAt"`
}

// Search 用关键词匹配标题、描述和标签，命中分数高的在前，同样高时新的在前。查询为空时列出全部条目。
func (s *Store) Search(query string) ([]Hit, error) {
	entries, err := s.List()
	if err != nil {
		return nil, err
	}
	keywords := strings.Fields(strings.ToLower(query))
	type scored struct {
		hit   Hit
		score int
		order int
	}
	matched := []scored{}
	for order, entry := range entries {
		haystack := strings.ToLower(entry.Title + "\n" + entry.Description + "\n" + strings.Join(entry.Tags, "\n"))
		score := 0
		for _, keyword := range keywords {
			score += keywordScore(haystack, keyword)
		}
		if len(keywords) > 0 && score == 0 {
			continue
		}
		matched = append(matched, scored{order: order, score: score, hit: Hit{
			ID: entry.ID, Title: entry.Title, Description: entry.Description, Tags: entry.Tags,
			SourceEmployee: entry.SourceEmployee, CreatedAt: entry.CreatedAt,
		}})
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].score != matched[j].score {
			return matched[i].score > matched[j].score
		}
		return matched[i].order > matched[j].order
	})
	hits := make([]Hit, 0, len(matched))
	for _, item := range matched {
		hits = append(hits, item.hit)
	}
	return hits, nil
}

// wholeKeyword 整个关键词命中的分数，远高于零散的两字片段。
const wholeKeyword = 10

// keywordScore 一个关键词的命中分数。中文不按空格分词，模型常把整句当关键词：
// 整句没命中时，按相邻两个字（含汉字的片段）逐一匹配，每命中一段记 1 分。
func keywordScore(haystack, keyword string) int {
	if strings.Contains(haystack, keyword) {
		return wholeKeyword
	}
	runes := []rune(keyword)
	seen := map[string]bool{}
	score := 0
	for index := 0; index+1 < len(runes); index++ {
		if !unicode.Is(unicode.Han, runes[index]) && !unicode.Is(unicode.Han, runes[index+1]) {
			continue
		}
		pair := string(runes[index : index+2])
		if !seen[pair] && strings.Contains(haystack, pair) {
			score++
		}
		seen[pair] = true
	}
	return score
}

// Descriptors 两个知识库工具的描述，判断步骤和前台 Agent 共用。
func Descriptors() []contract.ToolDescriptor {
	return []contract.ToolDescriptor{
		{
			Name:        contract.ToolKBSearch,
			Description: "搜索知识库索引：用关键词匹配条目的标题、描述和标签，只返回索引字段，不返回正文。需要正文时再用 kb_read 按 ID 读取。" + backgroundNotice,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "两三个短关键词，用空格分开，例如“周报 44”，不要整句；留空列出全部条目"},
				},
			},
		},
		{
			Name:        contract.ToolKBRead,
			Description: "按条目 ID 读取知识库正文。" + backgroundNotice,
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"id"},
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "kb_search 返回的条目 ID"},
				},
			},
		},
	}
}

// IsTool 是不是知识库工具。
func IsTool(name string) bool { return name == contract.ToolKBSearch || name == contract.ToolKBRead }

// Call 执行一次知识库工具调用。都是只读的，失败不可重试。
func (s *Store) Call(name string, args any) (contract.ToolCallResult, error) {
	var input struct {
		Query string `json:"query"`
		ID    string `json:"id"`
	}
	if raw, err := json.Marshal(args); err == nil {
		_ = json.Unmarshal(raw, &input)
	}
	fail := func(err error) (contract.ToolCallResult, error) {
		var known *Error
		if !errors.As(err, &known) {
			return contract.ToolCallResult{}, err
		}
		return contract.ToolCallResult{OK: false, Error: known.Message}, nil
	}
	switch name {
	case contract.ToolKBSearch:
		hits, err := s.Search(input.Query)
		if err != nil {
			return fail(err)
		}
		return contract.ToolCallResult{OK: true, Data: map[string]any{"entries": hits, "notice": backgroundNotice}}, nil
	case contract.ToolKBRead:
		entry, text, err := s.Read(input.ID)
		if err != nil {
			return fail(err)
		}
		return contract.ToolCallResult{OK: true, Data: map[string]any{
			"id": entry.ID, "title": entry.Title, "text": text, "notice": backgroundNotice,
		}}, nil
	}
	return contract.ToolCallResult{OK: false, Error: "未知的知识库工具：" + name}, nil
}

// withTools 在已有工具之外加上两个知识库工具。
type withTools struct {
	store *Store
	inner contract.ToolClient
}

// WithTools 把知识库工具接到已有的工具客户端上。哪个步骤能用仍由步骤白名单决定。
func WithTools(store *Store, inner contract.ToolClient) contract.ToolClient {
	return &withTools{store: store, inner: inner}
}

func (c *withTools) ListTools() ([]contract.ToolDescriptor, error) {
	descriptors := []contract.ToolDescriptor{}
	if c.inner != nil {
		inner, err := c.inner.ListTools()
		if err != nil {
			return nil, err
		}
		descriptors = append(descriptors, inner...)
	}
	return append(descriptors, Descriptors()...), nil
}

func (c *withTools) Call(name string, args any) (contract.ToolCallResult, error) {
	if IsTool(name) {
		return c.store.Call(name, args)
	}
	if c.inner == nil {
		return contract.ToolCallResult{OK: false, Error: "未知工具：" + name, Retryable: true}, nil
	}
	return c.inner.Call(name, args)
}

func (c *withTools) Close() error {
	if c.inner == nil {
		return nil
	}
	return c.inner.Close()
}
