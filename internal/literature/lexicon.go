package literature

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// RoutesFileName 员工配置目录里的路线词表文件（design.md §4、§11.3）。
const RoutesFileName = "routes.yaml"

// Route 一条技术路线：词表里的关键词只是给模型和程序的提示，归类由模型做、数量由程序数。
type Route struct {
	ID       string   `json:"id" yaml:"id"`
	Name     string   `json:"name" yaml:"name"`
	Keywords []string `json:"keywords" yaml:"keywords"`
}

// Direction 一个技术方向的词表：别名、相关的 IPC 分类前缀、路线。
type Direction struct {
	Name    string   `json:"name" yaml:"name"`
	Aliases []string `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	// IPC 属于这个方向的 IPC 主分类前缀，用来给“是否相关”和“清单更像哪个方向”做提示
	IPC    []string `json:"ipc" yaml:"ipc"`
	Routes []Route  `json:"routes" yaml:"routes"`
}

// Lexicon routes.yaml 的内容。
type Lexicon struct {
	Directions []Direction `json:"directions" yaml:"directions"`
}

// LoadLexicon 读取员工目录下的 routes.yaml；没有这个文件就是空词表（所有方向都由模型提出路线）。
func LoadLexicon(employeeDir string) (*Lexicon, error) {
	data, err := os.ReadFile(filepath.Join(employeeDir, RoutesFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return &Lexicon{}, nil
		}
		return nil, err
	}
	var lex Lexicon
	if err := yaml.Unmarshal(data, &lex); err != nil {
		return nil, fmt.Errorf("%s 不是合法的 YAML：%v", RoutesFileName, err)
	}
	return &lex, nil
}

// normalizeDirection 比较方向名时忽略空白和常见的“研判 / 方向”后缀。
func normalizeDirection(text string) string {
	text = strings.Join(strings.Fields(text), "")
	for _, suffix := range []string{"研判简报", "研判", "方向"} {
		text = strings.TrimSuffix(text, suffix)
	}
	return strings.ToLower(text)
}

// Find 按名称或别名找方向（忽略空白；填写的方向包含词表里的名称也算，如“固态电池电解质方向”）。
func (l *Lexicon) Find(direction string) *Direction {
	want := normalizeDirection(direction)
	if want == "" {
		return nil
	}
	for i := range l.Directions {
		d := &l.Directions[i]
		names := append([]string{d.Name}, d.Aliases...)
		for _, name := range names {
			if normalizeDirection(name) == want {
				return d
			}
		}
	}
	for i := range l.Directions {
		d := &l.Directions[i]
		names := append([]string{d.Name}, d.Aliases...)
		for _, name := range names {
			n := normalizeDirection(name)
			if n != "" && (strings.Contains(want, n) || strings.Contains(n, want)) {
				return d
			}
		}
	}
	return nil
}

// IPCMatches 专利的 IPC 主分类是否属于这个方向。
func (d *Direction) IPCMatches(ipc string) bool {
	for _, prefix := range d.IPC {
		if prefix != "" && strings.HasPrefix(ipc, prefix) {
			return true
		}
	}
	return false
}

// HintRoute 词表给的路线提示：先看标题里最早出现的关键词，标题里没有再看摘要和主权项。没有命中返回空。
func (d *Direction) HintRoute(p *Patent) (route string, basis string) {
	pick := func(text string) (string, string) {
		best, bestAt, bestWord := "", -1, ""
		for _, r := range d.Routes {
			for _, word := range r.Keywords {
				if word == "" {
					continue
				}
				if at := strings.Index(text, word); at >= 0 && (bestAt < 0 || at < bestAt) {
					best, bestAt, bestWord = r.Name, at, word
				}
			}
		}
		return best, bestWord
	}
	if name, word := pick(p.Title); name != "" {
		return name, "标题含“" + word + "”"
	}
	if name, word := pick(p.Abstract + " " + p.Claim); name != "" {
		return name, "摘要或主权项含“" + word + "”"
	}
	return "", ""
}

// RouteNames 方向的路线名，按词表顺序。
func (d *Direction) RouteNames() []string {
	names := make([]string, 0, len(d.Routes))
	for _, r := range d.Routes {
		names = append(names, r.Name)
	}
	return names
}

// ListTheme 清单内容整体更像哪个方向（按 IPC 主分类统计），用来说明“清单与方向不符”的依据。
type ListTheme struct {
	Direction string `json:"direction"`
	Count     int    `json:"count"`
	Total     int    `json:"total"`
}

// ThemeOf 统计一批专利按词表各方向的 IPC 命中数，返回命中最多的方向；一个都没命中返回 nil。
func (l *Lexicon) ThemeOf(patents []*Patent) *ListTheme {
	type tally struct {
		name  string
		count int
	}
	tallies := []tally{}
	for i := range l.Directions {
		d := &l.Directions[i]
		count := 0
		for _, p := range patents {
			if d.IPCMatches(p.IPC) {
				count++
			}
		}
		tallies = append(tallies, tally{d.Name, count})
	}
	sort.SliceStable(tallies, func(a, b int) bool { return tallies[a].count > tallies[b].count })
	if len(tallies) == 0 || tallies[0].count == 0 {
		return nil
	}
	return &ListTheme{Direction: tallies[0].name, Count: tallies[0].count, Total: len(patents)}
}
