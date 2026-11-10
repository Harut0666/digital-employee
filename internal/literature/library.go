// Package literature 是技术文献研判员工（场景 B，docs/design.md §11）的计算部分：
// 本地专利库与论文库、清单核对、路线词表、统计、检索、简报渲染和质检。
// 判断（相关性、路线归类、写简报）由模型做，这里的一切都是确定性的程序判定。
package literature

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// LibraryDirName 员工配置目录里放资料库的文件夹。
const LibraryDirName = "library"

// Patent 专利库里的一条记录（专利库.jsonl，字段名是中文）。
type Patent struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Applicant     string `json:"applicant"`
	ApplicantType string `json:"applicantType"`
	PatentType    string `json:"patentType"`
	FilingDate    string `json:"filingDate"`
	PubDate       string `json:"pubDate"`
	LegalStatus   string `json:"legalStatus"`
	IPC           string `json:"ipc"`
	Abstract      string `json:"abstract"`
	Claim         string `json:"claim"`
	CitedBy       int    `json:"citedBy"`
	FamilyID      string `json:"familyId"`
	FamilySize    int    `json:"familySize"`
	Transfer      string `json:"transfer"`
}

// Applicants 拆开联合申请的申请人（“; ”分隔），与 ApplicantTypes 一一对应。
func (p *Patent) Applicants() []string { return splitList(p.Applicant) }

// ApplicantTypes 与 Applicants 一一对应的申请人类型（企业 / 高校 / 科研院所）。
func (p *Patent) ApplicantTypes() []string { return splitList(p.ApplicantType) }

// FilingYear 申请年份。
func (p *Patent) FilingYear() int { return yearOf(p.FilingDate) }

// Paper 论文库里的一条记录（论文库.jsonl）。
type Paper struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Affiliation string `json:"affiliation"`
	Journal     string `json:"journal"`
	Year        int    `json:"year"`
	Abstract    string `json:"abstract"`
	Keywords    string `json:"keywords"`
	CitedBy     int    `json:"citedBy"`
}

type rawPatent struct {
	ID            string `json:"专利编号"`
	Title         string `json:"标题"`
	Applicant     string `json:"申请人"`
	ApplicantType string `json:"申请人类型"`
	PatentType    string `json:"专利类型"`
	FilingDate    string `json:"申请日"`
	PubDate       string `json:"公开日"`
	LegalStatus   string `json:"法律状态"`
	IPC           string `json:"IPC主分类"`
	Abstract      string `json:"摘要"`
	Claim         string `json:"主权项"`
	CitedBy       int    `json:"被引次数"`
	FamilyID      string `json:"同族编号"`
	FamilySize    int    `json:"同族数量"`
	Transfer      string `json:"转让许可记录"`
}

type rawPaper struct {
	ID          string `json:"论文编号"`
	Title       string `json:"标题"`
	Affiliation string `json:"作者单位"`
	Journal     string `json:"期刊"`
	Year        int    `json:"年份"`
	Abstract    string `json:"摘要"`
	Keywords    string `json:"关键词"`
	CitedBy     int    `json:"被引次数"`
}

// Library 本地资料库：简报只能引用这里的资料。
type Library struct {
	Patents     map[string]*Patent
	PatentOrder []string
	Papers      map[string]*Paper
	PaperOrder  []string
}

// Kind 编号属于哪个库：patent、paper，或空（库里没有）。
func (l *Library) Kind(id string) string {
	if _, ok := l.Patents[id]; ok {
		return "patent"
	}
	if _, ok := l.Papers[id]; ok {
		return "paper"
	}
	return ""
}

// TitleOf 编号对应的标题；库里没有返回空。
func (l *Library) TitleOf(id string) string {
	if p, ok := l.Patents[id]; ok {
		return p.Title
	}
	if p, ok := l.Papers[id]; ok {
		return p.Title
	}
	return ""
}

var (
	libraryMu    sync.Mutex
	libraryCache = map[string]*cachedLibrary{}
)

type cachedLibrary struct {
	stamp string
	lib   *Library
}

// LibraryDir 员工配置目录下的资料库目录。
func LibraryDir(employeeDir string) string { return filepath.Join(employeeDir, LibraryDirName) }

// LoadLibrary 读取资料库目录（patents.jsonl、papers.jsonl）。按文件大小和修改时间缓存，文件没变不重读。
func LoadLibrary(dir string) (*Library, error) {
	patentPath := filepath.Join(dir, "patents.jsonl")
	paperPath := filepath.Join(dir, "papers.jsonl")
	stamp, err := stampOf(patentPath, paperPath)
	if err != nil {
		return nil, fmt.Errorf("资料库不可用：%w", err)
	}
	libraryMu.Lock()
	defer libraryMu.Unlock()
	if cached, ok := libraryCache[dir]; ok && cached.stamp == stamp {
		return cached.lib, nil
	}
	lib := &Library{Patents: map[string]*Patent{}, Papers: map[string]*Paper{}}
	if err := readJSONL(patentPath, func(line []byte) error {
		var raw rawPatent
		if err := json.Unmarshal(line, &raw); err != nil {
			return err
		}
		id := strings.TrimSpace(raw.ID)
		if id == "" {
			return nil
		}
		lib.Patents[id] = &Patent{
			ID: id, Title: raw.Title, Applicant: raw.Applicant, ApplicantType: raw.ApplicantType, PatentType: raw.PatentType,
			FilingDate: raw.FilingDate, PubDate: raw.PubDate, LegalStatus: raw.LegalStatus, IPC: raw.IPC,
			Abstract: raw.Abstract, Claim: raw.Claim, CitedBy: raw.CitedBy, FamilyID: raw.FamilyID,
			FamilySize: raw.FamilySize, Transfer: raw.Transfer,
		}
		lib.PatentOrder = append(lib.PatentOrder, id)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("读取专利库失败：%w", err)
	}
	if err := readJSONL(paperPath, func(line []byte) error {
		var raw rawPaper
		if err := json.Unmarshal(line, &raw); err != nil {
			return err
		}
		id := strings.TrimSpace(raw.ID)
		if id == "" {
			return nil
		}
		lib.Papers[id] = &Paper{
			ID: id, Title: raw.Title, Affiliation: raw.Affiliation, Journal: raw.Journal, Year: raw.Year,
			Abstract: raw.Abstract, Keywords: raw.Keywords, CitedBy: raw.CitedBy,
		}
		lib.PaperOrder = append(lib.PaperOrder, id)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("读取论文库失败：%w", err)
	}
	libraryCache[dir] = &cachedLibrary{stamp: stamp, lib: lib}
	return lib, nil
}

func stampOf(paths ...string) (string, error) {
	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("%d@%d", info.Size(), info.ModTime().UnixNano()))
	}
	return strings.Join(parts, "|"), nil
}

func readJSONL(path string, each func(line []byte) error) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	first := true
	for scanner.Scan() {
		line := scanner.Bytes()
		if first {
			line = []byte(strings.TrimPrefix(string(line), "\xef\xbb\xbf"))
			first = false
		}
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		if err := each(line); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func splitList(text string) []string {
	out := []string{}
	for _, part := range strings.Split(text, ";") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func yearOf(date string) int {
	if len(date) < 4 {
		return 0
	}
	year := 0
	for _, r := range date[:4] {
		if r < '0' || r > '9' {
			return 0
		}
		year = year*10 + int(r-'0')
	}
	return year
}
