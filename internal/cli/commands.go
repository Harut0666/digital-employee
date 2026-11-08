// de 命令行实现（对应 packages/cli/src/{run-case,run-cases,check,replay,fixtures}-command.ts）。
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"digital-employee/internal/contract"
	"digital-employee/internal/settings"
)

func newToolClient(deps Deps) (contract.ToolClient, error) {
	if deps.NewToolClient == nil {
		return nil, nil
	}
	return deps.NewToolClient()
}

// modelSettings 模型设置：单目录模式下从工作区目录读，否则从应用目录读。
func modelSettings(deps Deps) contract.ModelSettings {
	return settings.LoadModelSettings(CaseAppDir(deps.Env, ""), settings.Env(deps.Env))
}

// caseModelSettings 一条用例的模型设置：先看用例自己的应用目录，没有设置文件时用全局的。
func caseModelSettings(appDir string, deps Deps) contract.ModelSettings {
	if _, err := os.Stat(settings.Path(appDir)); err == nil {
		return settings.LoadModelSettings(appDir, settings.Env(deps.Env))
	}
	return modelSettings(deps)
}

// caseAppDir 一条用例的应用目录：设置了 DE_WORKSPACE 时是工作区目录下的用例子目录
// （布局和以前一样：<DE_WORKSPACE>/<用例 ID>/），否则是结果目录下干净的 app/。
func caseAppDir(deps Deps, outDir, caseID string) string {
	appDir := CaseAppDir(deps.Env, outDir)
	if deps.env("DE_WORKSPACE") != "" {
		return filepath.Join(appDir, caseID)
	}
	return appDir
}

func runCaseCommandArgs(args []string, deps Deps) (int, error) {
	parsed, err := parseArgs(args, []string{"scripted", "out"}, nil)
	if err != nil {
		return 2, err
	}
	if len(parsed.positionals) == 0 {
		return usageError(deps, "run-case 需要 <case.json>")
	}
	caseFile, err := filepath.Abs(parsed.positionals[0])
	if err != nil {
		caseFile = parsed.positionals[0]
	}
	def, err := ReadCaseFile(caseFile)
	if err != nil {
		return 1, err
	}
	outDir := parsed.values["out"]
	if outDir == "" {
		outDir = filepath.Join("out", def.ID)
	}
	outDir, err = filepath.Abs(outDir)
	if err != nil {
		outDir = parsed.values["out"]
	}
	appDir := caseAppDir(deps, outDir, "")
	if err := os.MkdirAll(filepath.Join(appDir, "tasks"), 0o755); err != nil {
		return 1, err
	}
	scriptFile := ""
	if value := parsed.values["scripted"]; value != "" {
		if scriptFile, err = filepath.Abs(value); err != nil {
			scriptFile = value
		}
	}
	if deps.NewEngine == nil {
		return 1, fmt.Errorf("未配置任务引擎")
	}
	toolClient, err := newToolClient(deps)
	if err != nil {
		return 1, err
	}
	if toolClient != nil {
		defer toolClient.Close()
	}
	engine, err := deps.NewEngine(EngineOptions{
		ApplicationDir: appDir,
		WorkspaceDir:   appDir,
		EmployeesDir:   ResolveEmployeesDir(deps.Env, RepoRoot()),
		ToolClient:     toolClient,
		ScriptFile:     scriptFile,
		ModelSettings:  modelSettings(deps),
		Env:            deps.Env,
	})
	if err != nil {
		return 1, err
	}
	result, err := RunCase(engine, def, RunCaseOptions{OutDir: outDir})
	if err != nil {
		return 1, err
	}
	status := "失败"
	if result.Passed {
		status = "通过"
	}
	fmt.Fprintf(deps.stdout(), "%s %s  状态 %s  任务 %s\n", result.CaseID, status, result.FinalState, result.TaskID)
	for _, failure := range result.Failures {
		fmt.Fprintf(deps.stdout(), "  - %s\n", failure)
	}
	for _, deliverable := range result.Deliverables {
		fmt.Fprintf(deps.stdout(), "  交付物 %s: %s\n", deliverable.Kind, deliverable.Path)
	}
	fmt.Fprintf(deps.stdout(), "  结果已写入 %s\n", outDir)
	if result.Passed {
		return 0, nil
	}
	return 1, nil
}

// RunMode run-cases 的模型模式。
type RunMode string

const (
	RunModeScripted RunMode = "scripted"
	RunModeReal     RunMode = "real"
)

// CaseRunOutcome 一条用例的运行结果：状态类期望与判定要点合在一起算“未通过项”。
type CaseRunOutcome struct {
	CaseID     string
	Title      string
	Passed     bool
	Failures   []string
	Checks     []CheckOutcome
	ModelCalls int
	DurationMS int64
	FinalState string
	Error      string
}

type batchContext struct {
	outBase      string
	mode         RunMode
	toolClient   contract.ToolClient
	employeesDir string
	deps         Deps
}

func caseNameOf(file string) string {
	base := filepath.Base(file)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// findCaseFiles 解析 glob 找用例文件。排序按“编号 → 名字长度 → 名字”，
// 所以 A-01 在 A-01-rework 前面，A-01-rework 又在 A-02 前面。
func findCaseFiles(pattern string) []string {
	if !strings.ContainsAny(pattern, "*?[{") {
		absolute, err := filepath.Abs(pattern)
		if err != nil {
			absolute = pattern
		}
		if fileExists(absolute) {
			return []string{absolute}
		}
		return nil
	}
	if !filepath.IsAbs(pattern) {
		if cwd, err := os.Getwd(); err == nil {
			pattern = filepath.Join(cwd, pattern)
		}
	}
	matches, _ := filepath.Glob(pattern)
	files := []string{}
	for _, match := range matches {
		if strings.HasSuffix(match, ".json") {
			if absolute, err := filepath.Abs(match); err == nil {
				files = append(files, absolute)
			} else {
				files = append(files, match)
			}
		}
	}
	sort.SliceStable(files, func(i, j int) bool {
		left, right := caseNameOf(files[i]), caseNameOf(files[j])
		leftGroup := leadingLetters(left)
		rightGroup := leadingLetters(right)
		if leftGroup != rightGroup {
			return leftGroup < rightGroup
		}
		leftNumber, rightNumber := firstNumber(left), firstNumber(right)
		if leftNumber != rightNumber {
			return leftNumber < rightNumber
		}
		if len(left) != len(right) {
			return len(left) < len(right)
		}
		return left < right
	})
	return files
}

func leadingLetters(name string) string {
	end := 0
	for end < len(name) {
		ch := name[end]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') {
			end++
			continue
		}
		break
	}
	return name[:end]
}

func firstNumber(name string) int {
	value := 0
	seen := false
	for _, r := range name {
		if r >= '0' && r <= '9' {
			value = value*10 + int(r-'0')
			seen = true
		} else if seen {
			break
		}
	}
	if !seen {
		return 1 << 30
	}
	return value
}

// findScript scripted 模式：剧本按用例文件名字放在同目录的 scripts/ 下。
func findScript(caseFile string, def *CaseDef) (string, error) {
	dir := filepath.Join(filepath.Dir(caseFile), "scripts")
	candidates := []string{
		filepath.Join(dir, caseNameOf(caseFile)+".json"),
		filepath.Join(dir, def.ID+".json"),
	}
	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("找不到剧本：--scripted 模式需要 %s（或用 --real 调真实模型）", candidates[0])
}

func runOneCase(caseFile string, context *batchContext) CaseRunOutcome {
	started := time.Now()
	outcome := CaseRunOutcome{Failures: []string{}, Checks: []CheckOutcome{}}
	def, err := ReadCaseFile(caseFile)
	if err != nil {
		outcome.CaseID = caseNameOf(caseFile)
		outcome.Title = outcome.CaseID
		outcome.Error = err.Error()
		outcome.DurationMS = time.Since(started).Milliseconds()
		return outcome
	}
	outcome.CaseID = def.ID
	outcome.Title = def.Title
	caseOutDir := filepath.Join(context.outBase, def.ID)
	if err := os.RemoveAll(caseOutDir); err != nil {
		outcome.Error = err.Error()
		return outcome
	}
	if err := os.MkdirAll(caseOutDir, 0o755); err != nil {
		outcome.Error = err.Error()
		return outcome
	}
	appDir := caseAppDir(context.deps, caseOutDir, def.ID)
	if err := os.MkdirAll(filepath.Join(appDir, "tasks"), 0o755); err != nil {
		outcome.Error = err.Error()
		return outcome
	}
	scriptFile := ""
	if context.mode == RunModeScripted {
		scriptFile, err = findScript(caseFile, def)
		if err != nil {
			outcome.Error = err.Error()
			outcome.DurationMS = time.Since(started).Milliseconds()
			return outcome
		}
	}
	if context.deps.NewEngine == nil {
		outcome.Error = "未配置任务引擎"
		return outcome
	}
	engine, err := context.deps.NewEngine(EngineOptions{
		ApplicationDir: appDir,
		WorkspaceDir:   appDir,
		EmployeesDir:   context.employeesDir,
		ToolClient:     context.toolClient,
		ScriptFile:     scriptFile,
		// 设置从这条用例自己的应用目录读（互不影响）；没有设置时退回全局
		ModelSettings: caseModelSettings(appDir, context.deps),
		Env:           context.deps.Env,
	})
	if err != nil {
		outcome.Error = err.Error()
		outcome.DurationMS = time.Since(started).Milliseconds()
		return outcome
	}
	result, err := RunCase(engine, def, RunCaseOptions{OutDir: caseOutDir})
	if err != nil {
		outcome.Error = err.Error()
		outcome.DurationMS = time.Since(started).Milliseconds()
		return outcome
	}
	outcome.DurationMS = time.Since(started).Milliseconds()
	outcome.FinalState = string(result.FinalState)
	outcome.Failures = append(outcome.Failures, result.Failures...)
	evidence, err := ReadRunEvidence(caseOutDir)
	if err != nil {
		outcome.Error = err.Error()
		return outcome
	}
	outcome.ModelCalls = evidence.ModelCalls
	checks := CaseChecks{}
	if def.Expect != nil && def.Expect.Checks != nil {
		checks = *def.Expect.Checks
	}
	outcome.Checks = EvaluateChecks(evidence, checks)
	for _, check := range outcome.Checks {
		if !check.Passed {
			outcome.Failures = append(outcome.Failures, fmt.Sprintf("核对未通过：%s %s：%s", check.Kind, check.Target, check.Detail))
		}
	}
	outcome.Passed = len(outcome.Failures) == 0
	return outcome
}

func formatDuration(milliseconds int64) string {
	return fmt.Sprintf("%.1fs", float64(milliseconds)/1000)
}

func formatTime(at time.Time) string {
	return at.Format("2006-01-02 15:04:05")
}

// describeOutDir 结果目录在汇总里尽量显示成相对当前目录的路径。
func describeOutDir(dir string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return filepath.ToSlash(dir)
	}
	rel, err := filepath.Rel(cwd, dir)
	if err != nil {
		return filepath.ToSlash(dir)
	}
	if rel == "" || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return filepath.ToSlash(dir)
	}
	return filepath.ToSlash(rel)
}

// BuildSummary 汇总 Markdown（对应 run-cases-command.ts 的 buildSummary）。
func BuildSummary(outcomes []CaseRunOutcome, mode RunMode, outDir string, generatedAt time.Time) string {
	failed := 0
	for _, outcome := range outcomes {
		if !outcome.Passed {
			failed++
		}
	}
	modeLabel := "scripted（剧本式假模型，不调用真实模型）"
	if mode == RunModeReal {
		modeLabel = "real（真实模型）"
	}
	lines := []string{
		"# 自测用例运行结果",
		"",
		"- 模式：" + modeLabel,
		"- 生成时间：" + formatTime(generatedAt),
		fmt.Sprintf("- 用例：%d 条，通过 %d 条，未通过 %d 条", len(outcomes), len(outcomes)-failed, failed),
		"- 结果目录：`" + describeOutDir(outDir) + "`（每条用例一个子目录，含 result.json、events.jsonl、workspace/）",
		"",
		"| 用例 | 标题 | 结果 | 核对 | 模型调用 | 用时 |",
		"|---|---|---|---|---|---|",
	}
	for _, outcome := range outcomes {
		passedChecks := 0
		for _, check := range outcome.Checks {
			if check.Passed {
				passedChecks++
			}
		}
		checksCell := "—"
		if len(outcome.Checks) > 0 {
			checksCell = fmt.Sprintf("%d/%d", passedChecks, len(outcome.Checks))
		}
		resultCell := "✅ 通过"
		if !outcome.Passed {
			resultCell = "❌ 未通过"
		}
		lines = append(lines, fmt.Sprintf("| %s | %s | %s | %s | %d | %s |",
			outcome.CaseID, outcome.Title, resultCell, checksCell, outcome.ModelCalls, formatDuration(outcome.DurationMS)))
	}
	if failed == 0 {
		lines = append(lines, "", "全部用例通过。")
	} else {
		lines = append(lines, "", "## 未通过明细", "")
		for _, outcome := range outcomes {
			if outcome.Passed {
				continue
			}
			lines = append(lines, fmt.Sprintf("### %s · %s", outcome.CaseID, outcome.Title), "")
			if outcome.Error != "" {
				lines = append(lines, "- 运行失败："+outcome.Error)
			}
			for _, failure := range outcome.Failures {
				lines = append(lines, "- "+failure)
			}
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func runCasesCommandArgs(args []string, deps Deps) (int, error) {
	parsed, err := parseArgs(args, []string{"out"}, []string{"scripted", "real"})
	if err != nil {
		return 2, err
	}
	if len(parsed.positionals) == 0 {
		return usageError(deps, "run-cases 需要 <glob>，例如 \"samples/cases/*.json\"")
	}
	if parsed.bools["scripted"] && parsed.bools["real"] {
		return usageError(deps, "--scripted 与 --real 只能选一个")
	}
	mode := RunModeScripted
	if parsed.bools["real"] {
		mode = RunModeReal
	}
	pattern := parsed.positionals[0]
	files := findCaseFiles(pattern)
	if len(files) == 0 {
		return 1, fmt.Errorf("没有匹配到用例文件：%s（glob 由命令行自己解析，例：\"samples/cases/*.json\"）", pattern)
	}
	outBase := parsed.values["out"]
	if outBase == "" {
		outBase = filepath.Join("out", "cases")
	}
	outBase, err = filepath.Abs(outBase)
	if err != nil {
		outBase = parsed.values["out"]
	}
	if err := os.MkdirAll(outBase, 0o755); err != nil {
		return 1, err
	}
	employeesDir := ResolveEmployeesDir(deps.Env, RepoRoot())
	toolClient, err := newToolClient(deps)
	if err != nil {
		return 1, err
	}
	if toolClient != nil {
		defer toolClient.Close()
	}
	context := &batchContext{outBase: outBase, mode: mode, toolClient: toolClient, employeesDir: employeesDir, deps: deps}
	outcomes := []CaseRunOutcome{}
	for index, file := range files {
		fmt.Fprintf(deps.stdout(), "[%d/%d] %s\n", index+1, len(files), caseNameOf(file))
		outcome := runOneCase(file, context)
		outcomes = append(outcomes, outcome)
		passedChecks := 0
		for _, check := range outcome.Checks {
			if check.Passed {
				passedChecks++
			}
		}
		status := "失败"
		if outcome.Passed {
			status = "通过"
		}
		fmt.Fprintf(deps.stdout(), "  %s  核对 %d/%d  模型调用 %d  用时 %s\n",
			status, passedChecks, len(outcome.Checks), outcome.ModelCalls, formatDuration(outcome.DurationMS))
		for _, failure := range outcome.Failures {
			fmt.Fprintf(deps.stdout(), "  - %s\n", failure)
		}
		if outcome.Error != "" {
			fmt.Fprintf(deps.stdout(), "  - 运行失败：%s\n", outcome.Error)
		}
	}
	summaryPath := filepath.Join(outBase, "summary.md")
	if err := os.WriteFile(summaryPath, []byte(BuildSummary(outcomes, mode, outBase, deps.now())), 0o644); err != nil {
		return 1, err
	}
	failed := 0
	for _, outcome := range outcomes {
		if !outcome.Passed {
			failed++
		}
	}
	fmt.Fprintf(deps.stdout(), "共 %d 条用例：通过 %d 条，未通过 %d 条\n", len(outcomes), len(outcomes)-failed, failed)
	fmt.Fprintf(deps.stdout(), "汇总已写入 %s\n", summaryPath)
	return failed, nil
}

func checkCommand(args []string, deps Deps) (int, error) {
	parsed, err := parseArgs(args, nil, nil)
	if err != nil {
		return 2, err
	}
	if len(parsed.positionals) < 2 {
		return usageError(deps, "check 需要 <result> 和 <expect>")
	}
	resultDir, _ := filepath.Abs(parsed.positionals[0])
	expectFile, _ := filepath.Abs(parsed.positionals[1])
	checks, err := ReadChecksFromCaseFile(expectFile)
	if err != nil {
		return 1, err
	}
	report, err := CheckRun(resultDir, checks)
	if err != nil {
		return 1, err
	}
	failed := 0
	fmt.Fprintf(deps.stdout(), "核对 %s\n", resultDir)
	for _, outcome := range report.Outcomes {
		mark := "✓"
		if !outcome.Passed {
			mark = "✗"
			failed++
		}
		fmt.Fprintf(deps.stdout(), "  %s %s %s  %s\n", mark, outcome.Kind, outcome.Target, outcome.Detail)
	}
	fmt.Fprintf(deps.stdout(), "%d 项核对：%d 项通过，%d 项未通过\n", len(report.Outcomes), len(report.Outcomes)-failed, failed)
	if report.Passed {
		return 0, nil
	}
	return 1, nil
}

func replayCommand(args []string, deps Deps) (int, error) {
	parsed, err := parseArgs(args, nil, nil)
	if err != nil {
		return 2, err
	}
	if len(parsed.positionals) < 2 {
		return usageError(deps, "replay 需要 <workspace> 和 <taskId>")
	}
	lines, err := ReplayLines(parsed.positionals[0], parsed.positionals[1])
	if err != nil {
		return 1, err
	}
	for _, line := range lines {
		fmt.Fprintln(deps.stdout(), line)
	}
	return 0, nil
}

// fixturesCommand 生成变异数据（见 fixtures.go）。
func fixturesCommand(args []string, deps Deps) (int, error) {
	parsed, err := parseArgs(args, []string{"out", "source"}, nil)
	if err != nil {
		return 2, err
	}
	return runFixtures(parsed.values["out"], parsed.values["source"], deps)
}

var _ = json.Marshal
