// Package knowledge 是知识库：已交付的文件经人确认后放进工作区，供之后的员工查询（ADR-0005）。
//
// 存储在 <工作区>/knowledge 下：index.json 是索引，每个条目一个目录，放原文件和抽取的正文 text.txt。
// 知识库内容只作背景参考，不是事实。
package knowledge

// Entry 索引里的一个条目。正文不在索引里，按 ID 另读。
type Entry struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Tags           []string `json:"tags"`
	SourceTask     string   `json:"sourceTask"`
	SourceEmployee string   `json:"sourceEmployee"`
	CreatedAt      string   `json:"createdAt"`
	// FileName 原文件名
	FileName string `json:"fileName"`
}
