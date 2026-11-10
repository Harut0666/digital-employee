package literature

import (
	"encoding/csv"
	"fmt"
	"os"
	"sort"
	"strings"
)

// ListRow 业务方清单里的一行。
type ListRow struct {
	Row       int    `json:"row"` // 数据行号，从 1 开始
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	Applicant string `json:"applicant,omitempty"`
	FilingDay string `json:"filingDate,omitempty"`
}

// ReadList 读取清单 CSV（UTF-8，可带 BOM）。必须有“专利编号”列；空行跳过。
func ReadList(path string) ([]ListRow, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打不开清单文件：%w", err)
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("清单不是合法的 CSV：%w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("清单是空的")
	}
	header := records[0]
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\xef\xbb\xbf")
	}
	columns := map[string]int{}
	for i, name := range header {
		columns[strings.TrimSpace(name)] = i
	}
	idColumn, ok := columns["专利编号"]
	if !ok {
		return nil, fmt.Errorf("清单缺少“专利编号”列（现有列：%s）", strings.Join(header, "、"))
	}
	cell := func(record []string, name string) string {
		if i, ok := columns[name]; ok && i < len(record) {
			return strings.TrimSpace(record[i])
		}
		return ""
	}
	rows := []ListRow{}
	for i, record := range records[1:] {
		id := ""
		if idColumn < len(record) {
			id = strings.ToUpper(strings.TrimSpace(record[idColumn]))
		}
		if id == "" {
			continue
		}
		rows = append(rows, ListRow{Row: i + 1, ID: id, Title: cell(record, "标题"), Applicant: cell(record, "申请人"), FilingDay: cell(record, "申请日")})
	}
	return rows, nil
}

// Invention 一项发明：同族专利合并后的代表。后面所有统计、引用都以发明为单位。
type Invention struct {
	ID             string   `json:"id"`
	Members        []string `json:"members"` // 清单里出现的同族编号，含代表专利
	Title          string   `json:"title"`
	Applicants     []string `json:"applicants"`
	ApplicantTypes []string `json:"applicantTypes"`
	FilingDate     string   `json:"filingDate"`
	LegalStatus    string   `json:"legalStatus"`
	IPC            string   `json:"ipc"`
	Abstract       string   `json:"abstract"`
	Claim          string   `json:"claim"`
	CitedBy        int      `json:"citedBy"`
	Transfer       string   `json:"transfer,omitempty"`
	// AbstractMissing 库里这条专利没有摘要：引用时要注明依据标题和主权项
	AbstractMissing bool `json:"abstractMissing,omitempty"`
	// HintRelevant 词表对“是否属于这个方向”的提示：yes / no / unknown（词表里没有这个方向）
	HintRelevant string `json:"hintRelevant"`
	// HintRoute 词表对路线的提示和依据，没有命中为空
	HintRoute string `json:"hintRoute,omitempty"`
	HintBasis string `json:"hintBasis,omitempty"`
}

// Excluded 一条被剔除的清单行。
type Excluded struct {
	ID     string `json:"id"`
	Row    int    `json:"row,omitempty"`
	Reason string `json:"reason"`
}

// FamilyMerge 一次同族合并。
type FamilyMerge struct {
	Family         string   `json:"family"`
	Representative string   `json:"representative"`
	Merged         []string `json:"merged"` // 被并入代表专利的其他编号
}

// ScreenResult 清单核对结果（design.md §11.2 的 1 到 3 步，4 由模型判断）。
type ScreenResult struct {
	Direction     string        `json:"direction"`
	ListFile      string        `json:"listFile"`
	Rows          int           `json:"rows"`
	Duplicates    []Excluded    `json:"duplicates"`
	NotFound      []Excluded    `json:"notFound"`
	FamilyMerged  []FamilyMerge `json:"familyMerged"`
	Inventions    []Invention   `json:"inventions"`
	KnownDirected bool          `json:"knownDirection"`
	// Routes 词表里这个方向的路线（没有词表的方向为空，由模型先提出不超过 5 条路线）
	Routes []Route `json:"routes"`
	// ListTheme 清单内容整体更像哪个方向（按 IPC 统计）
	ListTheme *ListTheme `json:"listTheme,omitempty"`
	// Library 查了哪些库、各有多少条，写进资料概况
	Library LibraryInfo `json:"library"`
	// DirectionHits 库里和这个方向有关的专利、论文各有多少（清单一件都查不到时用来说明库里也没有这个方向）
	DirectionHits DirectionHits `json:"directionHits"`
	// Gate 一件发明都没有时给出暂停原因（资料不足）；否则为空。工具输出里它以 ok/pause 的形式出现
	Gate *Gate `json:"-"`
}

// LibraryInfo 资料库概况。
type LibraryInfo struct {
	Patents int `json:"patents"`
	Papers  int `json:"papers"`
}

// DirectionHits 库里与方向相关的资料数量。
type DirectionHits struct {
	Patents int `json:"patents"`
	Papers  int `json:"papers"`
}

// Gate 步骤要求暂停：引擎按 Code 转待发起人补充。
type Gate struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Detail  map[string]any `json:"detail,omitempty"`
}

// Screen 清单核对：去重 → 库中查不到的剔除 → 同族合并。与方向是否相关由模型逐条判断，这里只给提示。
func Screen(rows []ListRow, lib *Library, lex *Lexicon, direction, listFile string) *ScreenResult {
	result := &ScreenResult{
		Direction: direction, ListFile: listFile, Rows: len(rows),
		Duplicates: []Excluded{}, NotFound: []Excluded{}, FamilyMerged: []FamilyMerge{}, Inventions: []Invention{}, Routes: []Route{},
		Library: LibraryInfo{Patents: len(lib.Patents), Papers: len(lib.Papers)},
	}
	dir := lex.Find(direction)
	if dir != nil {
		result.KnownDirected = true
		result.Routes = append(result.Routes, dir.Routes...)
	}

	seen := map[string]bool{}
	found := []*Patent{}
	foundRow := map[string]int{}
	for _, row := range rows {
		if seen[row.ID] {
			result.Duplicates = append(result.Duplicates, Excluded{ID: row.ID, Row: row.Row, Reason: "清单里重复出现，只保留第一行"})
			continue
		}
		seen[row.ID] = true
		patent, ok := lib.Patents[row.ID]
		if !ok {
			result.NotFound = append(result.NotFound, Excluded{ID: row.ID, Row: row.Row, Reason: "专利库中查不到，不得引用"})
			continue
		}
		found = append(found, patent)
		foundRow[row.ID] = row.Row
	}

	// 同族合并：同族编号相同的视为一项发明；代表专利优先授权（B），其次申请日最早
	families := map[string][]*Patent{}
	order := []string{}
	for _, p := range found {
		key := p.FamilyID
		if key == "" {
			key = "single:" + p.ID
		}
		if _, ok := families[key]; !ok {
			order = append(order, key)
		}
		families[key] = append(families[key], p)
	}
	for _, key := range order {
		members := families[key]
		sort.SliceStable(members, func(a, b int) bool {
			ga, gb := strings.HasSuffix(members[a].ID, "B"), strings.HasSuffix(members[b].ID, "B")
			if ga != gb {
				return ga
			}
			if members[a].FilingDate != members[b].FilingDate {
				return members[a].FilingDate < members[b].FilingDate
			}
			return members[a].ID < members[b].ID
		})
		rep := members[0]
		memberIDs := make([]string, 0, len(members))
		for _, m := range members {
			memberIDs = append(memberIDs, m.ID)
		}
		if len(members) > 1 {
			result.FamilyMerged = append(result.FamilyMerged, FamilyMerge{Family: rep.FamilyID, Representative: rep.ID, Merged: memberIDs[1:]})
		}
		result.Inventions = append(result.Inventions, inventionOf(rep, memberIDs, dir))
	}
	// 发明按清单里第一次出现的位置排序，输出稳定
	sort.SliceStable(result.Inventions, func(a, b int) bool {
		return firstRow(result.Inventions[a], foundRow) < firstRow(result.Inventions[b], foundRow)
	})

	if dir != nil {
		result.ListTheme = lex.ThemeOf(found)
	} else if len(lex.Directions) > 0 {
		result.ListTheme = lex.ThemeOf(found)
	}
	result.DirectionHits = directionHits(lib, dir, direction)
	if len(result.Inventions) == 0 {
		result.Gate = insufficientNone(result)
	}
	return result
}

func firstRow(inv Invention, rows map[string]int) int {
	best := 1 << 30
	for _, id := range inv.Members {
		if r, ok := rows[id]; ok && r < best {
			best = r
		}
	}
	return best
}

func inventionOf(p *Patent, members []string, dir *Direction) Invention {
	inv := Invention{
		ID: p.ID, Members: members, Title: p.Title, Applicants: p.Applicants(), ApplicantTypes: p.ApplicantTypes(),
		FilingDate: p.FilingDate, LegalStatus: p.LegalStatus, IPC: p.IPC, Abstract: p.Abstract, Claim: p.Claim,
		CitedBy: p.CitedBy, Transfer: p.Transfer, AbstractMissing: strings.TrimSpace(p.Abstract) == "",
		HintRelevant: "unknown",
	}
	if dir != nil {
		inv.HintRelevant = "no"
		if dir.IPCMatches(p.IPC) {
			inv.HintRelevant = "yes"
		}
		inv.HintRoute, inv.HintBasis = dir.HintRoute(p)
		if inv.HintRoute != "" && inv.HintRelevant == "no" && !dir.IPCMatches(p.IPC) {
			// 标题或摘要里出现路线词，但 IPC 不属于这个方向：不当作相关，交给模型判断
			inv.HintRoute, inv.HintBasis = "", ""
		}
	}
	return inv
}

// directionHits 库里与方向相关的资料：专利按 IPC 前缀，论文按关键词里是否有方向名。
func directionHits(lib *Library, dir *Direction, direction string) DirectionHits {
	hits := DirectionHits{}
	want := normalizeDirection(direction)
	if dir != nil {
		for _, id := range lib.PatentOrder {
			if dir.IPCMatches(lib.Patents[id].IPC) {
				hits.Patents++
			}
		}
		want = normalizeDirection(dir.Name)
	}
	if want == "" {
		return hits
	}
	for _, id := range lib.PaperOrder {
		paper := lib.Papers[id]
		for _, keyword := range splitList(paper.Keywords) {
			if n := normalizeDirection(keyword); n != "" && (n == want || strings.Contains(want, n) && len([]rune(n)) >= 4) {
				hits.Papers++
				break
			}
		}
	}
	return hits
}

// insufficientNone 清单里没有一个编号能在专利库中查到：资料不足，只能补充资料或终止。
func insufficientNone(r *ScreenResult) *Gate {
	lines := []string{
		fmt.Sprintf("资料不足，无法完成研判：清单共 %d 行，其中 %d 个编号在专利库（共 %d 条）中查不到", r.Rows, len(r.NotFound), r.Library.Patents),
	}
	if r.Library.Papers > 0 {
		if r.DirectionHits.Papers == 0 && r.DirectionHits.Patents == 0 {
			lines = append(lines, fmt.Sprintf("论文库（共 %d 篇）和专利库里也没有“%s”方向的资料", r.Library.Papers, r.Direction))
		} else {
			lines = append(lines, fmt.Sprintf("库内与“%s”相关的资料：专利 %d 件、论文 %d 篇，但清单里的编号都不在其中", r.Direction, r.DirectionHits.Patents, r.DirectionHits.Papers))
		}
	}
	lines = append(lines, "请补充资料（换一份清单或补充编号）后继续，或终止任务。")
	return &Gate{
		Code:    "insufficient_material",
		Message: strings.Join(lines, "。"),
		Detail: map[string]any{
			"included": 0, "rows": r.Rows, "notFound": len(r.NotFound),
			"libraryPatents": r.Library.Patents, "libraryPapers": r.Library.Papers,
			"directionPatents": r.DirectionHits.Patents, "directionPapers": r.DirectionHits.Papers,
		},
	}
}
