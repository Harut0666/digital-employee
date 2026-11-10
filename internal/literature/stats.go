package literature

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"digital-employee/internal/contract"
)

// RouteAssignment 模型把一件发明归入哪条路线（judge_routes 的输出项）。
type RouteAssignment struct {
	ID    string `json:"id"`
	Route string `json:"route"`
	Basis string `json:"basis"`
}

// StatsInput compute_literature_stats 的入参。
type StatsInput struct {
	Direction   string                `json:"direction"`
	Included    []Invention           `json:"included"`
	Assignments []RouteAssignment     `json:"assignments"`
	Config      contract.LiteratureQC `json:"config"`
}

// RepPatent 路线的代表专利。
type RepPatent struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	CitedBy int    `json:"citedBy"`
}

// YearCount 某一年的件数。
type YearCount struct {
	Year  int `json:"year"`
	Count int `json:"count"`
	// Incomplete 因发明专利公开滞后，这一年的数据不完整
	Incomplete bool `json:"incomplete,omitempty"`
}

// RouteStat 一条技术路线的统计。
type RouteStat struct {
	Name           string      `json:"name"`
	Count          int         `json:"count"`
	SharePct       float64     `json:"sharePct"`
	Representative []RepPatent `json:"representative"`
	// Members 这条路线的全部发明编号
	Members []string `json:"members"`
	// ByYear 这条路线按申请年份的件数
	ByYear []YearCount `json:"byYear"`
	// Recent 近几年（截止年份往前 RecentYears 年起）申请的件数及占这条路线的比例，看路线是否在升温
	Recent         int     `json:"recent"`
	RecentSharePct float64 `json:"recentSharePct"`
	// Basis 模型给的归类依据，按发明编号
	Basis map[string]string `json:"basis,omitempty"`
}

// ApplicantStat 一个申请人的申请量（按发明计数，联合申请分别计入每个申请人）。
type ApplicantStat struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Count int    `json:"count"`
	// Joint 其中联合申请的件数
	Joint int `json:"joint"`
}

// TypeCount 申请人类型的构成。
type TypeCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

// JointApplication 一件联合申请。
type JointApplication struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Applicants []string `json:"applicants"`
}

// Lead 一条转化线索：高校、科研院所的专利。
type Lead struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Applicants  []string `json:"applicants"`
	Types       []string `json:"types"`
	LegalStatus string   `json:"legalStatus"`
	CitedBy     int      `json:"citedBy"`
	Transfer    string   `json:"transfer,omitempty"`
}

// Stats 统计结果：数字全部由程序算出，简报里的数字只能取自这里或所引资料的原文。
type Stats struct {
	Direction string `json:"direction"`
	Included  int    `json:"included"`
	// Routes 按件数从多到少；RouteNames 是它们的名字，给检索步骤当检索词
	Routes     []RouteStat `json:"routes"`
	RouteNames []string    `json:"routeNames"`
	// Unassigned 模型没有给出归类的发明（按“未归类”计）
	Unassigned []string `json:"unassigned,omitempty"`
	// Trend 按申请年份，补齐中间没有申请的年份
	Trend      []YearCount `json:"trend"`
	TrendNote  string      `json:"trendNote"`
	CutoffDate string      `json:"cutoffDate"`
	// RecentFromYear 近几年的起始年份
	RecentFromYear int `json:"recentFromYear"`
	RecentSharePct float64 `json:"recentSharePct"`
	// Applicants 申请量前 5 名（第 5 名并列的全部列出）
	Applicants  []ApplicantStat    `json:"applicants"`
	Composition []TypeCount        `json:"composition"`
	Joint       []JointApplication `json:"joint"`
	// Leads 转化线索
	Transferred []Lead `json:"transferred"`
	WorthContact []Lead `json:"worthContact"`
}

const recentYears = 3

// ComputeStats 路线数量、申请趋势、主要申请人、转化线索（design.md §11.3）。
func ComputeStats(in StatsInput) *Stats {
	cfg := in.Config
	defaults := contract.DefaultLiteratureQC()
	if cfg.CutoffDate == "" {
		cfg.CutoffDate = defaults.CutoffDate
	}
	if cfg.IncompleteYears <= 0 {
		cfg.IncompleteYears = defaults.IncompleteYears
	}
	cutoffYear := yearOf(cfg.CutoffDate)
	stats := &Stats{Direction: in.Direction, Included: len(in.Included), CutoffDate: cfg.CutoffDate,
		Routes: []RouteStat{}, Trend: []YearCount{}, Applicants: []ApplicantStat{}, Composition: []TypeCount{},
		Joint: []JointApplication{}, Transferred: []Lead{}, WorthContact: []Lead{}}

	// 路线
	assigned := map[string]RouteAssignment{}
	for _, a := range in.Assignments {
		assigned[strings.TrimSpace(a.ID)] = a
	}
	byRoute := map[string][]Invention{}
	routeOrder := []string{}
	for _, inv := range in.Included {
		a, ok := assigned[inv.ID]
		name := strings.TrimSpace(a.Route)
		if !ok || name == "" {
			name = "未归类"
			stats.Unassigned = append(stats.Unassigned, inv.ID)
		}
		if _, seen := byRoute[name]; !seen {
			routeOrder = append(routeOrder, name)
		}
		byRoute[name] = append(byRoute[name], inv)
	}
	stats.RecentFromYear = cutoffYear - recentYears
	total := len(in.Included)
	for _, name := range routeOrder {
		members := byRoute[name]
		rs := RouteStat{Name: name, Count: len(members), SharePct: pct(len(members), total), Representative: []RepPatent{}, Members: []string{}, Basis: map[string]string{}}
		sorted := append([]Invention{}, members...)
		sort.SliceStable(sorted, func(a, b int) bool {
			if sorted[a].CitedBy != sorted[b].CitedBy {
				return sorted[a].CitedBy > sorted[b].CitedBy
			}
			if sorted[a].FilingDate != sorted[b].FilingDate {
				return sorted[a].FilingDate < sorted[b].FilingDate
			}
			return sorted[a].ID < sorted[b].ID
		})
		for i, inv := range sorted {
			if i < 3 {
				rs.Representative = append(rs.Representative, RepPatent{ID: inv.ID, Title: inv.Title, CitedBy: inv.CitedBy})
			}
		}
		years := map[int]int{}
		for _, inv := range members {
			rs.Members = append(rs.Members, inv.ID)
			if a, ok := assigned[inv.ID]; ok && strings.TrimSpace(a.Basis) != "" {
				rs.Basis[inv.ID] = a.Basis
			}
			y := yearOf(inv.FilingDate)
			years[y]++
			if y >= stats.RecentFromYear {
				rs.Recent++
			}
		}
		rs.RecentSharePct = pct(rs.Recent, len(members))
		rs.ByYear = yearSeries(years, cutoffYear, cfg.IncompleteYears)
		stats.Routes = append(stats.Routes, rs)
	}
	sort.SliceStable(stats.Routes, func(a, b int) bool {
		if stats.Routes[a].Count != stats.Routes[b].Count {
			return stats.Routes[a].Count > stats.Routes[b].Count
		}
		return stats.Routes[a].Name < stats.Routes[b].Name
	})

	stats.RouteNames = []string{}
	for _, r := range stats.Routes {
		stats.RouteNames = append(stats.RouteNames, r.Name)
	}

	// 趋势
	years := map[int]int{}
	recent := 0
	for _, inv := range in.Included {
		y := yearOf(inv.FilingDate)
		years[y]++
		if y >= stats.RecentFromYear {
			recent++
		}
	}
	stats.Trend = yearSeries(years, cutoffYear, cfg.IncompleteYears)
	stats.RecentSharePct = pct(recent, total)
	incompleteFrom := cutoffYear - cfg.IncompleteYears
	stats.TrendNote = fmt.Sprintf("数据截至 %s；发明专利一般在申请日起 18 个月公开，所以 %d–%d 年的申请量统计不完整，判断趋势时要留意。",
		cfg.CutoffDate, incompleteFrom, cutoffYear)

	// 申请人：按发明计数，联合申请分别计入每个申请人
	type tally struct {
		typ          string
		count, joint int
	}
	byApplicant := map[string]*tally{}
	composition := map[string]int{}
	for _, inv := range in.Included {
		seen := map[string]bool{}
		for i, name := range inv.Applicants {
			if seen[name] {
				continue
			}
			seen[name] = true
			typ := "未知"
			if i < len(inv.ApplicantTypes) {
				typ = inv.ApplicantTypes[i]
			}
			t := byApplicant[name]
			if t == nil {
				t = &tally{typ: typ}
				byApplicant[name] = t
			}
			t.count++
			if len(inv.Applicants) > 1 {
				t.joint++
			}
			composition[typ]++
		}
		if len(inv.Applicants) > 1 {
			stats.Joint = append(stats.Joint, JointApplication{ID: inv.ID, Title: inv.Title, Applicants: inv.Applicants})
		}
	}
	all := []ApplicantStat{}
	for name, t := range byApplicant {
		all = append(all, ApplicantStat{Name: name, Type: t.typ, Count: t.count, Joint: t.joint})
	}
	sort.SliceStable(all, func(a, b int) bool {
		if all[a].Count != all[b].Count {
			return all[a].Count > all[b].Count
		}
		return all[a].Name < all[b].Name
	})
	for i, a := range all {
		// 前 5 名；第 5 名有并列的全部列出
		if i < 5 || a.Count == all[4].Count {
			stats.Applicants = append(stats.Applicants, a)
		}
	}
	for _, typ := range []string{"企业", "高校", "科研院所"} {
		if n, ok := composition[typ]; ok {
			stats.Composition = append(stats.Composition, TypeCount{Type: typ, Count: n})
			delete(composition, typ)
		}
	}
	rest := []string{}
	for typ := range composition {
		rest = append(rest, typ)
	}
	sort.Strings(rest)
	for _, typ := range rest {
		stats.Composition = append(stats.Composition, TypeCount{Type: typ, Count: composition[typ]})
	}

	// 转化线索：高校、科研院所的专利
	for _, inv := range in.Included {
		if !academic(inv) {
			continue
		}
		lead := Lead{ID: inv.ID, Title: inv.Title, Applicants: inv.Applicants, Types: inv.ApplicantTypes,
			LegalStatus: inv.LegalStatus, CitedBy: inv.CitedBy, Transfer: inv.Transfer}
		if strings.TrimSpace(inv.Transfer) != "" {
			stats.Transferred = append(stats.Transferred, lead)
			continue
		}
		if inv.LegalStatus == "有效" || inv.LegalStatus == "实质审查中" {
			stats.WorthContact = append(stats.WorthContact, lead)
		}
	}
	sort.SliceStable(stats.WorthContact, func(a, b int) bool {
		if stats.WorthContact[a].CitedBy != stats.WorthContact[b].CitedBy {
			return stats.WorthContact[a].CitedBy > stats.WorthContact[b].CitedBy
		}
		return stats.WorthContact[a].ID < stats.WorthContact[b].ID
	})
	if len(stats.WorthContact) > 3 {
		stats.WorthContact = stats.WorthContact[:3]
	}
	return stats
}

func academic(inv Invention) bool {
	for _, t := range inv.ApplicantTypes {
		if t == "高校" || t == "科研院所" {
			return true
		}
	}
	return false
}

func yearSeries(counts map[int]int, cutoffYear, incompleteYears int) []YearCount {
	if len(counts) == 0 {
		return []YearCount{}
	}
	first, last := math.MaxInt, 0
	for y := range counts {
		if y < first {
			first = y
		}
		if y > last {
			last = y
		}
	}
	out := []YearCount{}
	for y := first; y <= last; y++ {
		out = append(out, YearCount{Year: y, Count: counts[y], Incomplete: y >= cutoffYear-incompleteYears})
	}
	return out
}

func pct(part, whole int) float64 {
	if whole <= 0 {
		return 0
	}
	return math.Round(float64(part)/float64(whole)*1000) / 10
}
