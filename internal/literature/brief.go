package literature

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"digital-employee/internal/contract"
	"digital-employee/internal/render"
)

// Claim 简报里一句有出处的话：Cites 是专利编号或论文编号，角标由程序按出现顺序编号。
type Claim struct {
	Text  string   `json:"text"`
	Cites []string `json:"cites"`
	// Route 在“技术路线分类”章节里，这句话讲的是哪条路线
	Route string `json:"route,omitempty"`
}

// BriefSection 简报的一个章节（write_brief 的输出）。标题取自 qc.yaml 的 literature.sections。
type BriefSection struct {
	ID     string  `json:"id"`
	Claims []Claim `json:"claims"`
}

// BriefDraft 判断步骤写出的简报草稿：模型只写有出处的叙述，表格、数量、来源清单由程序生成。
type BriefDraft struct {
	Title         string                  `json:"title"`
	Sections      []BriefSection          `json:"sections"`
	RevisionNotes []contract.RevisionNote `json:"revisionNotes,omitempty"`
}

// Citation 简报里的一条引用。
type Citation struct {
	N               int    `json:"n"`
	ID              string `json:"id"`
	Kind            string `json:"kind"` // patent | paper | unknown
	Title           string `json:"title"`
	AbstractMissing bool   `json:"abstractMissing,omitempty"`
}

// Rendered 渲染结果：质检的输入。MDPath 是 engine 登记交付物时认的字段名。
type Rendered struct {
	MDPath         string     `json:"mdPath"`
	AssetsDir      string     `json:"assetsDir"`
	Charts         []string   `json:"charts"`
	ChartCount     int        `json:"chartCount"`
	EstimatedPages float64    `json:"estimatedPages"`
	Citations      []Citation `json:"citations"`
}

// RenderInput render_brief_md 的入参。
type RenderInput struct {
	Direction      string                `json:"direction"`
	Draft          BriefDraft            `json:"draft"`
	Stats          *Stats                `json:"stats"`
	Scope          *ScopeResult          `json:"scope"`
	Screen         *ScreenResult         `json:"screen"`
	Config         contract.LiteratureQC `json:"config"`
	OutDir         string                `json:"outDir"`
	IdempotencyKey string                `json:"idempotencyKey"`
	EmployeeDir    string                `json:"employeeDir"`
	// FontPaths 图表中文字体候选路径；空则按系统默认找
	FontPaths []string `json:"-"`
}

// charsPerPage 一页正文按多少字估算（五号字、普通页边距，含表格和图占位的折算）。
const charsPerPage = 1600.0

var unsafeNameRe = regexp.MustCompile(`[^\p{L}\p{N}_.-]+`)

// citer 按第一次出现的顺序给引用编号。
type citer struct {
	lib   *Library
	order []string
	n     map[string]int
}

func newCiter(lib *Library) *citer { return &citer{lib: lib, n: map[string]int{}} }

func (c *citer) number(id string) int {
	if n, ok := c.n[id]; ok {
		return n
	}
	c.order = append(c.order, id)
	c.n[id] = len(c.order)
	return len(c.order)
}

// mark 一组引用的角标；引用了库里缺摘要的专利时，程序自动加注“（依据标题和主权项）”。
func (c *citer) mark(ids []string) string {
	var sb strings.Builder
	missing := false
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		sb.WriteString("[" + strconv.Itoa(c.number(id)) + "]")
		if p, ok := c.lib.Patents[id]; ok && strings.TrimSpace(p.Abstract) == "" {
			missing = true
		}
	}
	if missing {
		sb.WriteString("（依据标题和主权项）")
	}
	return sb.String()
}

// RenderBrief 把简报草稿、统计和核对结果渲染成 Markdown，图表 PNG 放在旁边的 assets 文件夹，用相对路径引用。
func RenderBrief(in RenderInput) (*Rendered, error) {
	if strings.TrimSpace(in.OutDir) == "" {
		return nil, fmt.Errorf("render_brief_md: outDir 不能为空")
	}
	if in.Stats == nil || in.Scope == nil || in.Screen == nil {
		return nil, fmt.Errorf("render_brief_md: 缺少统计或清单核对结果")
	}
	lib, err := LoadLibrary(LibraryDir(in.EmployeeDir))
	if err != nil {
		return nil, err
	}
	cfg := in.Config
	cite := newCiter(lib)
	stats, scope, screen := in.Stats, in.Scope, in.Screen

	assetsDir := filepath.Join(in.OutDir, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		return nil, err
	}
	charts := []string{}
	chartNames := map[string]string{}
	writeChart := func(kind string, chart render.BarChart) error {
		png, err := render.RenderBarPNG(chart, in.FontPaths)
		if err != nil {
			return fmt.Errorf("绘制图表失败：%w", err)
		}
		sum := sha256.Sum256(png)
		name := kind + "-" + hex.EncodeToString(sum[:])[:10] + ".png"
		path := filepath.Join(assetsDir, name)
		if _, statErr := os.Stat(path); statErr != nil {
			if err := writeAtomic(path, png); err != nil {
				return err
			}
		}
		charts = append(charts, path)
		chartNames[kind] = name
		return nil
	}
	if len(stats.Routes) > 0 {
		chart := render.BarChart{Title: "技术路线分布（发明数）"}
		for _, r := range stats.Routes {
			chart.Labels = append(chart.Labels, r.Name)
			chart.Values = append(chart.Values, r.Count)
		}
		if err := writeChart("routes", chart); err != nil {
			return nil, err
		}
	}
	if len(stats.Trend) > 0 {
		chart := render.BarChart{Title: "申请趋势（按申请年份的发明数）", Legend: "数据完整", FadedLegend: "公开滞后，数据不完整"}
		for _, y := range stats.Trend {
			chart.Labels = append(chart.Labels, strconv.Itoa(y.Year))
			chart.Values = append(chart.Values, y.Count)
			chart.Faded = append(chart.Faded, y.Incomplete)
		}
		if err := writeChart("trend", chart); err != nil {
			return nil, err
		}
	}

	claimsOf := func(id string) []Claim {
		for _, s := range in.Draft.Sections {
			if s.ID == id {
				return s.Claims
			}
		}
		return nil
	}
	claimLine := func(cl Claim) string {
		text := strings.TrimSpace(cl.Text)
		if len(cl.Cites) == 0 {
			return text
		}
		return text + cite.mark(cl.Cites)
	}

	var md strings.Builder
	title := strings.TrimSpace(in.Draft.Title)
	if title == "" {
		title = "技术研判简报：" + in.Direction
	}
	md.WriteString("# " + title + "\n\n")
	md.WriteString(fmt.Sprintf("> 资料范围：专利库 %d 条、论文库 %d 篇（模拟数据，截至 %s）；结论只取自这两个库，不引用库外资料。方括号数字对应文末来源清单。\n\n",
		screen.Library.Patents, screen.Library.Papers, stats.CutoffDate))

	heading := map[string]string{}
	order := []string{}
	for _, s := range cfg.Sections {
		heading[s.ID] = s.Heading
		order = append(order, s.ID)
	}
	paperCited := map[string]bool{}
	for _, s := range in.Draft.Sections {
		for _, cl := range s.Claims {
			for _, id := range cl.Cites {
				if _, ok := lib.Papers[strings.TrimSpace(id)]; ok {
					paperCited[strings.TrimSpace(id)] = true
				}
			}
		}
	}

	for _, id := range order {
		md.WriteString("## " + heading[id] + "\n\n")
		claims := claimsOf(id)
		switch id {
		case "summary":
			for i, cl := range claims {
				md.WriteString(fmt.Sprintf("%d. %s\n", i+1, claimLine(cl)))
			}
			md.WriteString("\n")

		case "overview":
			md.WriteString(fmt.Sprintf("业务方清单《%s》共 %d 行，技术方向：%s。核对结果：\n\n", screen.ListFile, scope.Counts.Rows, in.Direction))
			md.WriteString(fmt.Sprintf("- 重复行：%d 条%s，同一编号只保留第一行。\n", len(screen.Duplicates), idList(excludedIDs(screen.Duplicates))))
			md.WriteString(fmt.Sprintf("- 库中查不到：%d 个%s，已剔除，也不会被引用。\n", len(screen.NotFound), idList(excludedIDs(screen.NotFound))))
			merges := []string{}
			for _, m := range screen.FamilyMerged {
				merges = append(merges, fmt.Sprintf("%s（并入 %s）", m.Representative, strings.Join(m.Merged, "、")))
			}
			md.WriteString(fmt.Sprintf("- 同族合并：%d 组%s，少算 %d 件（同族专利视为一项发明，代表专利优先授权、其次申请日最早）。\n",
				len(screen.FamilyMerged), idList(merges), scope.Counts.FamilyMerged))
			md.WriteString(fmt.Sprintf("- 与方向无关：%d 件。\n", len(scope.Irrelevant)))
			for _, r := range scope.Irrelevant {
				md.WriteString(fmt.Sprintf("  - %s %s：%s\n", r.ID, r.Title, r.Reason))
			}
			md.WriteString(fmt.Sprintf("- **最终纳入分析：%d 项发明。**\n", scope.Counts.Included))
			if len(paperCited) > 0 {
				md.WriteString(fmt.Sprintf("- 补充检索：按方向和路线检索了论文库，补充 %d 篇论文作为佐证，均已列入来源清单。\n", len(paperCited)))
			} else {
				md.WriteString("- 补充检索：检索了论文库，未引用论文。\n")
			}
			md.WriteString("\n")
			writeClaims(&md, claims, claimLine)

		case "routes":
			if name, ok := chartNames["routes"]; ok {
				md.WriteString("![技术路线分布](assets/" + name + ")\n\n")
			}
			md.WriteString("| 技术路线 | 专利数量 | 占比 | 代表专利 |\n| --- | ---: | ---: | --- |\n")
			for _, r := range stats.Routes {
				reps := []string{}
				for _, rep := range r.Representative {
					reps = append(reps, fmt.Sprintf("%s %s", mdEscape(rep.Title), cite.mark([]string{rep.ID})))
				}
				md.WriteString(fmt.Sprintf("| %s | %d | %.1f%% | %s |\n", r.Name, r.Count, r.SharePct, strings.Join(reps, "；")))
			}
			md.WriteString("\n")
			named := map[string]bool{}
			for _, r := range stats.Routes {
				named[r.Name] = true
			}
			for _, r := range stats.Routes {
				md.WriteString(fmt.Sprintf("**%s**（%d 件，占 %.1f%%）\n\n", r.Name, r.Count, r.SharePct))
				for _, cl := range claims {
					if cl.Route == r.Name {
						md.WriteString("- " + claimLine(cl) + "\n")
					}
				}
				md.WriteString("\n")
			}
			for _, cl := range claims {
				if !named[cl.Route] {
					md.WriteString("- " + claimLine(cl) + "\n")
				}
			}
			md.WriteString("\n")

		case "trend":
			if name, ok := chartNames["trend"]; ok {
				md.WriteString("![申请趋势](assets/" + name + ")\n\n")
			}
			md.WriteString("| 申请年份 | 发明数 | 说明 |\n| ---: | ---: | --- |\n")
			for _, y := range stats.Trend {
				note := ""
				if y.Incomplete {
					note = "因公开滞后不完整"
				}
				md.WriteString(fmt.Sprintf("| %d | %d | %s |\n", y.Year, y.Count, note))
			}
			md.WriteString("\n" + stats.TrendNote + "\n\n")
			writeClaims(&md, claims, claimLine)

		case "applicants":
			md.WriteString("按发明计数，联合申请分别计入每个申请人；第 5 名有并列的全部列出。\n\n")
			md.WriteString("| 申请人 | 排名 | 类型 | 发明数 | 其中联合申请 |\n| --- | ---: | --- | ---: | ---: |\n")
			rank := 0
			for i, a := range stats.Applicants {
				if i == 0 || a.Count != stats.Applicants[i-1].Count {
					rank = i + 1
				}
				md.WriteString(fmt.Sprintf("| %s | %d | %s | %d | %d |\n", a.Name, rank, a.Type, a.Count, a.Joint))
			}
			md.WriteString("\n")
			parts := []string{}
			for _, t := range stats.Composition {
				parts = append(parts, fmt.Sprintf("%s %d 项", t.Type, t.Count))
			}
			if len(parts) > 0 {
				md.WriteString("申请人构成（按申请人计）：" + strings.Join(parts, "、") + "。\n\n")
			}
			if len(stats.Joint) > 0 {
				md.WriteString("联合申请：\n\n")
				for _, j := range stats.Joint {
					md.WriteString(fmt.Sprintf("- %s（%s）%s\n", mdEscape(j.Title), strings.Join(j.Applicants, "、"), cite.mark([]string{j.ID})))
				}
				md.WriteString("\n")
			} else {
				md.WriteString("纳入分析的发明中没有联合申请。\n\n")
			}
			writeClaims(&md, claims, claimLine)

		case "leads":
			md.WriteString("**已发生转让或许可的高校、科研院所专利**\n\n")
			if len(stats.Transferred) == 0 {
				md.WriteString("- 没有。\n")
			}
			for _, l := range stats.Transferred {
				md.WriteString(fmt.Sprintf("- %s（%s，%s）：%s %s\n", mdEscape(l.Title), strings.Join(l.Applicants, "、"), l.LegalStatus, l.Transfer, cite.mark([]string{l.ID})))
			}
			md.WriteString("\n**值得接洽**（高校、科研院所，法律状态为有效或实质审查中，尚未转让，被引次数最多的前 3 件）\n\n")
			if len(stats.WorthContact) == 0 {
				md.WriteString("- 没有符合条件的专利。\n")
			}
			for _, l := range stats.WorthContact {
				md.WriteString(fmt.Sprintf("- %s（%s，%s，被引 %d 次）%s\n", mdEscape(l.Title), strings.Join(l.Applicants, "、"), l.LegalStatus, l.CitedBy, cite.mark([]string{l.ID})))
			}
			md.WriteString("\n")
			writeClaims(&md, claims, claimLine)

		case "risks":
			for _, line := range autoRisks(in, lib, paperCited) {
				md.WriteString("- " + line + "\n")
			}
			for _, cl := range claims {
				md.WriteString("- " + claimLine(cl) + "\n")
			}
			md.WriteString("\n")

		default:
			writeClaims(&md, claims, claimLine)
		}
	}

	// 来源清单：正文里每个 [n] 都对应一条
	md.WriteString("## 来源清单\n\n")
	citations := []Citation{}
	for i, id := range cite.order {
		n := i + 1
		c := Citation{N: n, ID: id, Kind: "unknown"}
		switch lib.Kind(id) {
		case "patent":
			p := lib.Patents[id]
			c.Kind, c.Title, c.AbstractMissing = "patent", p.Title, strings.TrimSpace(p.Abstract) == ""
			md.WriteString(fmt.Sprintf("[%d] %s %s（专利；%s；申请日 %s；%s）\n\n", n, id, p.Title, p.Applicant, p.FilingDate, p.LegalStatus))
		case "paper":
			p := lib.Papers[id]
			c.Kind, c.Title = "paper", p.Title
			md.WriteString(fmt.Sprintf("[%d] %s %s（论文；%s；%d 年）\n\n", n, id, p.Title, p.Affiliation, p.Year))
		default:
			md.WriteString(fmt.Sprintf("[%d] %s（专利库、论文库中查不到该编号）\n\n", n, id))
		}
		citations = append(citations, c)
	}

	body := md.String()
	pages := float64(len([]rune(body))) / charsPerPage
	out := &Rendered{AssetsDir: assetsDir, Charts: charts, ChartCount: len(charts), EstimatedPages: float64(int(pages*100+0.5)) / 100, Citations: citations}

	keySum := sha256.Sum256([]byte(in.IdempotencyKey))
	name := "brief-" + safeName(in.IdempotencyKey) + "-" + hex.EncodeToString(keySum[:])[:12] + ".md"
	out.MDPath = filepath.Join(in.OutDir, name)
	if err := writeAtomic(out.MDPath, []byte(body)); err != nil {
		return nil, err
	}
	return out, nil
}

func writeClaims(md *strings.Builder, claims []Claim, line func(Claim) string) {
	if len(claims) == 0 {
		return
	}
	for _, cl := range claims {
		md.WriteString("- " + line(cl) + "\n")
	}
	md.WriteString("\n")
}

// autoRisks 程序能确定的风险与不确定性：模型写的在这些之后。
func autoRisks(in RenderInput, lib *Library, paperCited map[string]bool) []string {
	stats, scope := in.Stats, in.Scope
	risks := []string{}
	if len(stats.Trend) > 0 {
		from := yearOf(stats.CutoffDate) - in.Config.IncompleteYears
		risks = append(risks, fmt.Sprintf("近年申请量不完整：%d–%d 年的发明专利可能尚未公开，申请趋势在这几年偏低，不代表真实下滑。", from, yearOf(stats.CutoffDate)))
	}
	if scope.SmallSample {
		risks = append(risks, fmt.Sprintf("样本过少：清单核对后只纳入 %d 项发明，发起人已确认按现有资料继续，结论的可靠性有限。", scope.Counts.Included))
	}
	missing := []string{}
	for _, inv := range scope.Included {
		if inv.AbstractMissing {
			missing = append(missing, inv.ID)
		}
	}
	if len(missing) > 0 {
		risks = append(risks, fmt.Sprintf("个别专利在专利库中缺摘要（%s），相关判断依据标题和主权项，细节可能不全。", strings.Join(missing, "、")))
	}
	if len(stats.Unassigned) > 0 {
		risks = append(risks, fmt.Sprintf("有 %d 项发明没有得到技术路线归类（%s），按“未归类”计。", len(stats.Unassigned), strings.Join(stats.Unassigned, "、")))
	}
	if len(scope.Irrelevant) > 0 {
		risks = append(risks, fmt.Sprintf("清单里有 %d 件被判为与方向无关而剔除，判断依据是 IPC 分类和技术内容，边界情形可能有分歧。", len(scope.Irrelevant)))
	}
	risks = append(risks, "资料全部来自本地专利库与论文库，且为模拟数据，不能作为真实技术或转化决策的依据；库外的进展没有纳入。")
	return risks
}

func excludedIDs(items []Excluded) []string {
	ids := make([]string, 0, len(items))
	for _, e := range items {
		ids = append(ids, e.ID)
	}
	return ids
}

func idList(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return "（" + strings.Join(ids, "、") + "）"
}

func mdEscape(text string) string { return strings.ReplaceAll(text, "|", "\\|") }

func safeName(key string) string {
	s := unsafeNameRe.ReplaceAllString(key, "_")
	r := []rune(s)
	if len(r) > 60 {
		r = r[:60]
	}
	return string(r)
}

func writeAtomic(path string, data []byte) error {
	tmp := path + "." + strconv.Itoa(os.Getpid()) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
