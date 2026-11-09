// 前台 Agent 的搜索工具：find_files（按文件名找，对应 DeepSeek Harness 的 glob）和 search_content（按内容找，对应 grep）。
//
// 只读，所有权限模式都允许。路径边界和读文件一样：只在工作区目录内走，链接/junction 先解开，指到目录外的跳过。
// 参考 DSH 的行为，不复制代码；glob 和正则都用 Go 标准库实现，不依赖外部的 ripgrep。
package session

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"digital-employee/internal/agent"
)

const (
	// findMaxResults find_files 最多返回多少个文件。
	findMaxResults = 100
	// findScanCap find_files 最多扫描多少个文件（防止在巨大的目录里卡住）。
	findScanCap = 200000
	// searchMaxMatches search_content 最多返回多少条匹配。
	searchMaxMatches = 250
	// searchPreviewBytes 每条匹配的文本预览最多多少字节。
	searchPreviewBytes = 2000
	// searchMaxFileBytes 超过这个大小的文件不搜内容。
	searchMaxFileBytes = 10 << 20
)

// searchTimeout search_content 的总时限；测试里可以改小。
var searchTimeout = 30 * time.Second

// vcsDirs 版本控制目录：不进。
var vcsDirs = map[string]bool{".git": true, ".svn": true, ".hg": true, ".bzr": true, ".jj": true, "CVS": true}

// globToRegexp 把 glob 写成正则：** 跨目录，* 和 ? 不跨 /，{a,b} 是备选，[abc] 是字符集。
func globToRegexp(pattern string) (*regexp.Regexp, error) {
	pattern = strings.ReplaceAll(strings.TrimSpace(pattern), `\`, "/")
	pattern = strings.TrimPrefix(pattern, "./")
	if pattern == "" {
		return nil, errors.New("pattern 不能为空")
	}
	var out strings.Builder
	out.WriteString("^")
	braces := 0
	runes := []rune(pattern)
	for index := 0; index < len(runes); index++ {
		switch r := runes[index]; r {
		case '*':
			if index+1 < len(runes) && runes[index+1] == '*' {
				index++
				if index+1 < len(runes) && runes[index+1] == '/' {
					index++
					out.WriteString("(?:.*/)?")
				} else {
					out.WriteString(".*")
				}
			} else {
				out.WriteString("[^/]*")
			}
		case '?':
			out.WriteString("[^/]")
		case '{':
			braces++
			out.WriteString("(?:")
		case '}':
			if braces == 0 {
				out.WriteString(`\}`)
			} else {
				braces--
				out.WriteString(")")
			}
		case ',':
			if braces > 0 {
				out.WriteString("|")
			} else {
				out.WriteString(",")
			}
		case '[':
			end := index + 1
			for end < len(runes) && runes[end] != ']' {
				end++
			}
			if end >= len(runes) {
				out.WriteString(`\[`)
				continue
			}
			class := string(runes[index+1 : end])
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			out.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
			index = end
		default:
			out.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	if braces > 0 {
		return nil, errors.New("pattern 里的 { 没有配对的 }")
	}
	out.WriteString("$")
	prefix := ""
	if runtime.GOOS == "windows" {
		prefix = "(?i)"
	}
	compiled, err := regexp.Compile(prefix + out.String())
	if err != nil {
		return nil, fmt.Errorf("pattern 不是合法的 glob：%v", err)
	}
	return compiled, nil
}

// globMatcher 一个 glob：不含 / 的写法（*.md）在任意深度匹配文件名，含 / 的写法按相对路径匹配。
type globMatcher struct {
	re       *regexp.Regexp
	basename bool
}

func newGlobMatcher(pattern string) (globMatcher, error) {
	re, err := globToRegexp(pattern)
	if err != nil {
		return globMatcher{}, err
	}
	return globMatcher{re: re, basename: !strings.Contains(strings.ReplaceAll(pattern, `\`, "/"), "/")}, nil
}

func (g globMatcher) matches(rel string) bool {
	if g.basename {
		if slash := strings.LastIndex(rel, "/"); slash >= 0 {
			rel = rel[slash+1:]
		}
	}
	return g.re.MatchString(rel)
}

// scanned 走过的一个文件。
type scanned struct {
	rel     string
	path    string
	size    int64
	modTime time.Time
}

// walkWorkspaceFiles 从 base 开始递归走文件（只给文件），跳过版本控制目录；链接解开后要仍在 root 内。
// visit 返回 false 停止。返回 true 表示被 visit 或 ctx 提前停止。
func walkWorkspaceFiles(ctx context.Context, root, base, baseRel string, visit func(scanned) bool) bool {
	seen := &dirSeen{}
	if info, err := os.Stat(base); err == nil {
		seen.enter(info)
	}
	var walk func(dir, prefix string, depth int) bool
	walk = func(dir, prefix string, depth int) bool {
		if depth > maxWalkDepth {
			return false
		}
		children, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		for _, child := range children {
			if ctx.Err() != nil {
				return true
			}
			name := child.Name()
			if vcsDirs[name] {
				continue
			}
			path := filepath.Join(dir, name)
			info, ok := entryInfo(root, path)
			if !ok {
				continue
			}
			rel := prefix + name
			if info.IsDir() {
				if !seen.enter(info) {
					continue
				}
				if walk(path, rel+"/", depth+1) {
					return true
				}
				continue
			}
			if !info.Mode().IsRegular() {
				continue
			}
			if !visit(scanned{rel: rel, path: path, size: info.Size(), modTime: info.ModTime()}) {
				return true
			}
		}
		return false
	}
	prefix := ""
	if baseRel != "" && baseRel != "." {
		prefix = baseRel + "/"
	}
	return walk(base, prefix, 0)
}

// resolveSearchBase 解析 path 参数：工作区内的目录或文件；不填就是工作区目录本身。
func (t *turn) resolveSearchBase(raw string) (path, rel string, info fs.FileInfo, err error) {
	root := t.workspaceDir()
	if root == "" {
		return "", "", nil, errors.New("工作区目录不存在")
	}
	raw = strings.TrimSpace(raw)
	path, rel, err = resolveInWorkspace(root, raw)
	if err != nil {
		return "", "", nil, denied("%s。我只能搜工作区目录里的文件。", err.Error())
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		return "", "", nil, toolFailure("path_missing", "路径不存在：%s。", displayPath(rel))
	}
	return path, rel, info, nil
}

// searchTools find_files 和 search_content。
func (t *turn) searchTools() []tool {
	find := tool{
		spec: agent.ToolSpec{
			Name: "find_files",
			Description: "按文件名找工作区目录里的文件（glob，支持 **）。只返回文件，按修改时间从新到旧，最多 100 个，超出会提示总数；不进 .git 等版本控制目录。" +
				"写法：不含 / 的（如 *.md）在任意深度匹配文件名；含 / 的（如 资料/**/*.xlsx）按相对路径匹配。结果路径相对工作区目录。找文件优先用它，不要用命令行 ls / dir / find。",
			Parameters: map[string]any{"type": "object", "required": []any{"pattern"}, "properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "glob，如 **/*.md、周报*.docx、资料/**/*.xlsx"},
				"path":    map[string]any{"type": "string", "description": "只在这个子目录里找，相对工作区目录；不填就是整个工作区目录"},
			}},
		},
		run: func(arguments map[string]any) (any, error) {
			pattern, _ := arguments["pattern"].(string)
			matcher, err := newGlobMatcher(pattern)
			if err != nil {
				return nil, toolFailure("invalid_pattern", "%s", err.Error())
			}
			raw, _ := arguments["path"].(string)
			base, baseRel, info, err := t.resolveSearchBase(raw)
			if err != nil {
				return nil, err
			}
			if !info.IsDir() {
				return nil, toolFailure("not_a_directory", "%s 不是文件夹，find_files 的 path 要给文件夹。", displayPath(baseRel))
			}
			// pattern 按“相对 path 的路径”匹配
			found := []scanned{}
			scannedCount := 0
			walkWorkspaceFiles(context.Background(), t.workspaceDir(), base, "", func(item scanned) bool {
				scannedCount++
				if matcher.matches(item.rel) {
					found = append(found, item)
				}
				return scannedCount < findScanCap
			})
			if len(found) == 0 {
				return map[string]any{"files": []string{}, "total": 0, "text": "没有找到文件"}, nil
			}
			sort.SliceStable(found, func(i, j int) bool {
				if !found[i].modTime.Equal(found[j].modTime) {
					return found[i].modTime.After(found[j].modTime)
				}
				return found[i].rel < found[j].rel
			})
			shown := found
			if len(shown) > findMaxResults {
				shown = shown[:findMaxResults]
			}
			files := make([]string, 0, len(shown))
			prefix := ""
			if baseRel != "." && baseRel != "" {
				prefix = baseRel + "/"
			}
			for _, item := range shown {
				files = append(files, prefix+item.rel)
			}
			result := map[string]any{"files": files, "total": len(found)}
			if len(found) > len(shown) {
				result["note"] = fmt.Sprintf("共 %d 个文件，只显示最新的 %d 个。请缩小 pattern 或 path。", len(found), len(shown))
			}
			if scannedCount >= findScanCap {
				result["scanLimited"] = true
			}
			return result, nil
		},
	}
	search := tool{
		spec: agent.ToolSpec{
			Name: "search_content",
			Description: "按内容搜工作区目录里的文本文件（Go 正则）。输出按文件分组，每条是“Line N: 文本”，头部写总匹配数；最多 250 条，每行预览最多 2000 字节，超出会提示。" +
				"跳过二进制文件和超过 10MB 的文件，不进 .git 等目录，最多搜 30 秒。要精确的字面量请自己转义正则特殊字符。搜内容优先用它，不要用命令行 grep / findstr。",
			Parameters: map[string]any{"type": "object", "required": []any{"pattern"}, "properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Go 正则（RE2），如 环比.*下降、(?i)error"},
				"path":    map[string]any{"type": "string", "description": "文件或子目录，相对工作区目录；不填就搜整个工作区目录"},
				"include": map[string]any{"type": "string", "description": "只搜文件名匹配这个 glob 的文件（单个），如 *.md、资料/**/*.csv"},
			}},
		},
		run: func(arguments map[string]any) (any, error) {
			pattern, _ := arguments["pattern"].(string)
			if pattern == "" {
				return nil, toolFailure("invalid_pattern", "pattern 不能为空")
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				return nil, toolFailure("invalid_pattern", "pattern 不是合法的正则（Go RE2 语法）：%v", err)
			}
			var include *globMatcher
			if text, _ := arguments["include"].(string); strings.TrimSpace(text) != "" {
				matcher, err := newGlobMatcher(text)
				if err != nil {
					return nil, toolFailure("invalid_include", "include %s", err.Error())
				}
				include = &matcher
			}
			raw, _ := arguments["path"].(string)
			base, baseRel, info, err := t.resolveSearchBase(raw)
			if err != nil {
				return nil, err
			}
			ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
			defer cancel()
			search := &contentSearch{re: re, ctx: ctx, include: include}
			if info.IsDir() {
				walkWorkspaceFiles(ctx, t.workspaceDir(), base, baseRel, search.visit)
			} else if info.Mode().IsRegular() {
				search.visit(scanned{rel: baseRel, path: base, size: info.Size()})
			} else {
				return nil, toolFailure("not_a_file", "%s 不是普通文件。", displayPath(baseRel))
			}
			return search.result(), nil
		},
	}
	return []tool{find, search}
}

// contentSearch 一次内容搜索的累计。
type contentSearch struct {
	re      *regexp.Regexp
	ctx     context.Context
	include *globMatcher
	// groups 按文件分组的匹配（只存前 searchMaxMatches 条）
	groups   []searchGroup
	total    int
	kept     int
	skipped  int
	timedOut bool
}

type searchGroup struct {
	file  string
	lines []string
}

func (s *contentSearch) visit(item scanned) bool {
	if s.ctx.Err() != nil {
		s.timedOut = true
		return false
	}
	if s.include != nil && !s.include.matches(item.rel) {
		return true
	}
	if item.size > searchMaxFileBytes {
		s.skipped++
		return true
	}
	file, err := os.Open(item.path)
	if err != nil {
		return true
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 64*1024)
	head, _ := reader.Peek(readBinaryProbe)
	if bytes.IndexByte(head, 0) >= 0 {
		s.skipped++
		return true
	}
	group := searchGroup{file: item.rel}
	for lineNo := 1; ; lineNo++ {
		line, readErr := readWholeLine(reader)
		if len(line) > 0 || readErr == nil {
			if !utf8.Valid(line) {
				// 不是合法 UTF-8 的文件当二进制处理，已经收的匹配保留
				s.skipped++
				break
			}
			text := strings.TrimSuffix(string(line), "\r")
			if s.re.MatchString(text) {
				s.total++
				if s.kept < searchMaxMatches {
					s.kept++
					group.lines = append(group.lines, fmt.Sprintf("Line %d: %s", lineNo, previewText(text)))
				}
			}
		}
		if readErr != nil {
			break
		}
		if lineNo%2000 == 0 && s.ctx.Err() != nil {
			s.timedOut = true
			break
		}
	}
	if len(group.lines) > 0 {
		s.groups = append(s.groups, group)
	}
	return s.ctx.Err() == nil
}

// readWholeLine 读一整行（不含 \n）；超长的行也读完。
func readWholeLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		line = append(line, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimSuffix(line, []byte("\n")), err
	}
}

// previewText 每条匹配的文本预览：最多 2000 字节，在字符边界截断。
func previewText(text string) string {
	if len(text) <= searchPreviewBytes {
		return text
	}
	cut := searchPreviewBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…（该行预览已截断，超过 2000 字节）"
}

func (s *contentSearch) result() map[string]any {
	if s.total == 0 {
		text := "没有找到匹配"
		if s.timedOut {
			text += "（搜索超过 30 秒被中止，结果不完整，请缩小 path 或 include）"
		}
		return map[string]any{"text": text, "total": 0, "timedOut": s.timedOut}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "共找到 %d 处匹配", s.total)
	if s.total > s.kept {
		fmt.Fprintf(&out, "，只显示前 %d 条。请缩小 pattern、path 或 include", s.kept)
	}
	out.WriteString("\n")
	for _, group := range s.groups {
		out.WriteString("\n" + group.file + ":\n")
		for _, line := range group.lines {
			out.WriteString("  " + line + "\n")
		}
	}
	if s.timedOut {
		out.WriteString("\n（搜索超过 30 秒被中止，结果不完整，请缩小 path 或 include）\n")
	}
	if s.skipped > 0 {
		fmt.Fprintf(&out, "\n（跳过了 %d 个二进制或过大的文件）\n", s.skipped)
	}
	return map[string]any{"text": strings.TrimRight(out.String(), "\n"), "total": s.total, "shown": s.kept, "timedOut": s.timedOut}
}
