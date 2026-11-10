package literature

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// SearchInput search_library 的入参。
type SearchInput struct {
	// Direction 技术方向：关键词里含方向名的论文排在前面
	Direction string `json:"direction"`
	// Queries 检索词（方向名、路线名等），按 BM25 打分
	Queries []string `json:"queries"`
	// Scope papers | patents | all
	Scope string `json:"scope"`
	TopK  int    `json:"topK"`
	// Exclude 不要再返回的编号（清单里已有的专利等）
	Exclude []string `json:"exclude"`
}

// Hit 一条检索结果。
type Hit struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"` // patent | paper
	Title    string  `json:"title"`
	Year     int     `json:"year,omitempty"`
	Keywords string  `json:"keywords,omitempty"`
	Abstract string  `json:"abstract,omitempty"`
	CitedBy  int     `json:"citedBy"`
	Score    float64 `json:"score"`
}

// SearchOutput 检索结果：写明查了哪些库，简报的“资料概况”据此说明是否补充检索。
type SearchOutput struct {
	Searched []string `json:"searched"`
	Queries  []string `json:"queries"`
	Hits     []Hit    `json:"hits"`
	Library  LibraryInfo `json:"library"`
}

type document struct {
	id, kind string
	tokens   []string
	paper    *Paper
	patent   *Patent
}

// tokenize 中文按相邻两字切（bigram），英文数字按词切，全部转小写。
func tokenize(text string) []string {
	out := []string{}
	var han []rune
	var word []rune
	flushHan := func() {
		switch len(han) {
		case 0:
		case 1:
			out = append(out, string(han))
		default:
			for i := 0; i+1 < len(han); i++ {
				out = append(out, string(han[i:i+2]))
			}
		}
		han = han[:0]
	}
	flushWord := func() {
		if len(word) > 0 {
			out = append(out, strings.ToLower(string(word)))
		}
		word = word[:0]
	}
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			flushWord()
			han = append(han, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushHan()
			word = append(word, r)
		default:
			flushHan()
			flushWord()
		}
	}
	flushHan()
	flushWord()
	return out
}

// Search 在本地资料库里做 BM25 关键词检索（标题、摘要、关键词、主权项）。不接网络：简报不得引用库外资料。
func Search(lib *Library, in SearchInput) *SearchOutput {
	topK := in.TopK
	if topK <= 0 {
		topK = 6
	}
	scope := in.Scope
	if scope == "" {
		scope = "papers"
	}
	exclude := map[string]bool{}
	for _, id := range in.Exclude {
		exclude[strings.TrimSpace(id)] = true
	}
	docs := []document{}
	searched := []string{}
	if scope == "papers" || scope == "all" {
		searched = append(searched, "论文库")
		for _, id := range lib.PaperOrder {
			p := lib.Papers[id]
			// 标题和关键词各重复，让它们比摘要更重
			text := strings.Join([]string{p.Title, p.Title, p.Keywords, p.Keywords, p.Abstract}, " ")
			docs = append(docs, document{id: id, kind: "paper", tokens: tokenize(text), paper: p})
		}
	}
	if scope == "patents" || scope == "all" {
		searched = append(searched, "专利库")
		for _, id := range lib.PatentOrder {
			p := lib.Patents[id]
			text := strings.Join([]string{p.Title, p.Title, p.Abstract, p.Claim}, " ")
			docs = append(docs, document{id: id, kind: "patent", tokens: tokenize(text), patent: p})
		}
	}

	queries := []string{}
	for _, q := range in.Queries {
		if q = strings.TrimSpace(q); q != "" {
			queries = append(queries, q)
		}
	}
	if d := strings.TrimSpace(in.Direction); d != "" {
		queries = append([]string{d}, queries...)
	}
	queryTokens := map[string]int{}
	for _, q := range queries {
		for _, t := range tokenize(q) {
			queryTokens[t]++
		}
	}

	// BM25
	const k1, b = 1.5, 0.75
	n := float64(len(docs))
	avg := 0.0
	df := map[string]int{}
	for _, d := range docs {
		avg += float64(len(d.tokens))
		seen := map[string]bool{}
		for _, t := range d.tokens {
			if !seen[t] {
				seen[t] = true
				df[t]++
			}
		}
	}
	if n > 0 {
		avg /= n
	}
	want := normalizeDirection(in.Direction)
	hits := []Hit{}
	for _, d := range docs {
		if exclude[d.id] {
			continue
		}
		tf := map[string]int{}
		for _, t := range d.tokens {
			tf[t]++
		}
		score := 0.0
		for t, qn := range queryTokens {
			f := float64(tf[t])
			if f == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(df[t])+0.5)/(float64(df[t])+0.5))
			score += float64(qn) * idf * (f * (k1 + 1)) / (f + k1*(1-b+b*float64(len(d.tokens))/math.Max(avg, 1)))
		}
		// 论文关键词里直接标了这个方向的，排在最前
		if d.paper != nil && want != "" {
			for _, kw := range splitList(d.paper.Keywords) {
				if normalizeDirection(kw) == want {
					score += 1000
					break
				}
			}
		}
		if score <= 0 {
			continue
		}
		h := Hit{ID: d.id, Kind: d.kind, Score: math.Round(score*100) / 100}
		if d.paper != nil {
			h.Title, h.Year, h.Keywords, h.Abstract, h.CitedBy = d.paper.Title, d.paper.Year, d.paper.Keywords, trimRunes(d.paper.Abstract, 160), d.paper.CitedBy
		} else {
			h.Title, h.Year, h.Abstract, h.CitedBy = d.patent.Title, d.patent.FilingYear(), trimRunes(d.patent.Abstract, 160), d.patent.CitedBy
		}
		hits = append(hits, h)
	}
	sort.SliceStable(hits, func(a, b int) bool {
		if hits[a].Score != hits[b].Score {
			return hits[a].Score > hits[b].Score
		}
		if hits[a].CitedBy != hits[b].CitedBy {
			return hits[a].CitedBy > hits[b].CitedBy
		}
		return hits[a].ID < hits[b].ID
	})
	if len(hits) > topK {
		hits = hits[:topK]
	}
	return &SearchOutput{Searched: searched, Queries: queries, Hits: hits, Library: LibraryInfo{Patents: len(lib.Patents), Papers: len(lib.Papers)}}
}

func trimRunes(text string, n int) string {
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return string(runes[:n]) + "…"
}
