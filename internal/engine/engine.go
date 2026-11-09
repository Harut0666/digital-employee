// T1 实现：任务引擎（状态机、事件日志、编排器、断点恢复）。
//
// 移植自 packages/core/src/engine/index.ts。Go 版与 TS 版的行为约定逐条对齐：
//   - 任务存在应用目录的 tasks/ 下，整个进程只有一个引擎（Spec 0006）；
//   - 状态只能走 contract.Transitions 表里的流转，表外一律拒绝并写 transition_rejected；
//   - 每条事件先落盘（fsync）再推进内存投影，seq 由投影的 LastSeq+1 得到；
//   - 编排在独立 goroutine 里跑（drive），命令返回后由 WaitForSettled 等它停在暂停态或终态；
//   - 判断步骤通过 agent.JudgmentRunner、质检通过 qc.Engine 执行，二者都可注入以便测试。
package engine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"digital-employee/internal/agent"
	"digital-employee/internal/config"
	"digital-employee/internal/contract"
	"digital-employee/internal/knowledge"
	"digital-employee/internal/qc"
	"digital-employee/internal/settings"
)

// ---------------------------------------------------------------- 常量

var commandTrigger = map[string]contract.TransitionTrigger{
	contract.CmdSubmit:                 contract.TriggerSubmit,
	contract.CmdSupplement:             contract.TriggerSupplement,
	contract.CmdContinueWithoutMinutes: contract.TriggerContinueWithoutMinutes,
	contract.CmdContinueWithPartial:    contract.TriggerContinueWithPartial,
	contract.CmdRetry:                  contract.TriggerRetry,
	contract.CmdTerminate:              contract.TriggerTerminate,
	contract.CmdApprove:                contract.TriggerApprove,
	contract.CmdReturn:                 contract.TriggerReturn,
}

// defaultFactsStep 未配置 factsStep 时，事实取自这个步骤（场景 A 的约定）。
const defaultFactsStep = "compute_metrics"

// contradictionsTool 用这个工具的计算步骤输出纪要矛盾，供“纪要矛盾覆盖”质检使用。
const contradictionsTool = "detect_contradictions"

// modelPointer 空的模型引用写成 nil（字段省略）。
func modelPointer(ref contract.ModelRef) *contract.ModelRef {
	if ref.Connection == "" && ref.Model == "" {
		return nil
	}
	copied := ref
	return &copied
}

// SimulatedCrash 模拟进程被杀：崩溃注入命中时返回。返回之后这个引擎实例不再写任何事件。
type SimulatedCrash struct {
	StepID string
}

func (c *SimulatedCrash) Error() string {
	return fmt.Sprintf("simulated crash at step %q", c.StepID)
}

// errTaskStopped 任务已终止，运行中的步骤写不进任何事件，只能收工。
var errTaskStopped = errors.New("任务已终止")

// judgmentRunner 判断步骤执行器（消费方接口，便于测试注入）。
type judgmentRunner interface {
	Run(in agent.JudgmentInput) (contract.JudgmentResult, error)
}

// qcEngine 质检引擎（消费方接口，便于测试注入）。
type qcEngine interface {
	Run(input contract.QcInput) []contract.QcCheckResult
}

// Deps 引擎依赖。
type Deps struct {
	// Dir 应用目录：设置、知识库、任务都在它下面。单目录模式下这个目录同时是唯一的工作区
	Dir string
	// EmployeesDir 员工配置根目录
	EmployeesDir string
	ToolClient   contract.ToolClient
	Model        agent.ModelProvider
	// DefaultModel 步骤没写死模型时的缺省模型（设置的 lastModel）；为空时由提供者自己的缺省值决定
	DefaultModel contract.ModelRef
	// Now 可注入的时钟（测试用）
	Now func() time.Time
	// Faults 运行时可变故障开关（服务端与引擎共享同一对象）
	Faults *contract.FaultHooks
	// ExitOnCrash 崩溃注入命中时真正退出进程（生产入口用）
	ExitOnCrash bool
	// ConfigFallback 任务目录里的员工配置副本读不出来时，按员工 id 另找一个配置目录（Spec 0006 应用目录模式
	// 下，员工配置在应用目录的 employees/ 里，任务目录里的副本与之同名）。nil 时不找。
	ConfigFallback func(employeeID string) (string, bool)
	// Workspaces 工作区 ID → 目录（Spec 0006、T42）：每个版本送审时把交付物复制到
	// <目录>/交付/ 下。nil，或者任务没有记下工作区、工作区也查不到目录时，不写交付文件夹。
	// 单目录模式的目录同时是应用目录，不在这里登记，所以单目录模式也不写。
	Workspaces func(workspaceID string) (string, bool)
}

// Options 测试可注入的内部实现；生产环境不传，使用 agent / qc 的默认实现。
type Options struct {
	Judgment judgmentRunner
	QC       qcEngine
}

// Engine 任务引擎实现（contract.Engine）。
type Engine struct {
	dir          string
	employeesDir string
	tasksDir     string
	toolClient   contract.ToolClient
	model        agent.ModelProvider
	// defaultModel 步骤没写死模型时的缺省模型（保存设置后的会话模型，Spec 0006）
	defaultModel contract.ModelRef
	now          func() time.Time
	faults       *contract.FaultHooks
	exitOnCrash  bool

	judgment judgmentRunner
	qcEngine qcEngine
	// configFallback 任务目录副本读不出来时的员工配置来源（见 Deps.ConfigFallback）
	configFallback func(employeeID string) (string, bool)
	// workspaces 工作区 ID → 目录，写交付文件夹用（见 Deps.Workspaces）
	workspaces func(workspaceID string) (string, bool)

	mu       sync.Mutex
	runtimes map[string]*taskRuntime
	dead     *SimulatedCrash
	// stopping 正在停下：不再接受命令；stopped 已停下：也不再写入（见 Stop）
	stopping bool
	stopped  bool
	// stopErr 进程退出时停下引擎的原因；已经建好的任务照常执行
	stopErr error
}

// New 创建任务引擎。
func New(deps Deps) (*Engine, error) {
	return NewWithOptions(deps, Options{})
}

// NewWithOptions 创建任务引擎，可注入判断步骤执行器与质检引擎（测试用）。
func NewWithOptions(deps Deps, options Options) (*Engine, error) {
	if deps.Dir == "" {
		return nil, errors.New("引擎缺少 Dir（应用目录）")
	}
	if deps.EmployeesDir == "" {
		return nil, errors.New("引擎缺少 EmployeesDir")
	}
	dir, err := filepath.Abs(deps.Dir)
	if err != nil {
		return nil, err
	}
	employeesDir, err := filepath.Abs(deps.EmployeesDir)
	if err != nil {
		return nil, err
	}
	faults := deps.Faults
	if faults == nil {
		faults = &contract.FaultHooks{}
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	// 知识库工具对所有员工都注册，哪个步骤能用由步骤白名单决定
	toolClient := knowledge.WithTools(knowledge.Open(dir), deps.ToolClient)
	engine := &Engine{
		dir:          dir,
		employeesDir: employeesDir,
		tasksDir:     filepath.Join(dir, "tasks"),
		toolClient:   toolClient,
		model:        deps.Model,
		defaultModel: deps.DefaultModel,
		now:          now,
		faults:       faults,
		exitOnCrash:  deps.ExitOnCrash,
		judgment:     options.Judgment,
		qcEngine:     options.QC,
		runtimes:     map[string]*taskRuntime{},

		configFallback: deps.ConfigFallback,
		workspaces:     deps.Workspaces,
	}
	if engine.judgment == nil {
		engine.judgment = agent.NewJudgmentRunner(agent.RunnerOptions{Execution: engine.executionDefaults})
	}
	if engine.qcEngine == nil {
		engine.qcEngine = qc.NewEngine()
	}
	if err := os.MkdirAll(engine.tasksDir, 0o755); err != nil {
		return nil, err
	}
	return engine, nil
}

// Dir 应用目录（任务在其下）。
func (e *Engine) Dir() string { return e.dir }

// SetModelProvider 热替换模型提供者（设置页保存模型连接后调用，无需重启）。
// TS 版的 onSettingsSaved 同样是重建 provider 后交给引擎，这里用读锁保护。
func (e *Engine) SetModelProvider(provider agent.ModelProvider) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.model = provider
}

func (e *Engine) modelProvider() agent.ModelProvider {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.model
}

// SetDefaultModel 换掉步骤没写死模型时用的缺省模型（保存设置后的会话模型）。
func (e *Engine) SetDefaultModel(ref contract.ModelRef) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.defaultModel = ref
}

func (e *Engine) defaultModelRef() contract.ModelRef {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.defaultModel
}

// SetTaskModel 换掉一个任务的会话模型（Spec 0006“会话模型”），从下一个判断步骤开始生效。
//
// 正在进行的模型调用不中断：判断步骤开始时才读任务当前的模型。
// 只在任务还没结束、模型确实变了的时候写一条“模型已切换”事件；任务已结束或不存在时什么也不做。
func (e *Engine) SetTaskModel(taskID string, ref contract.ModelRef) error {
	rt := e.openRuntime(taskID)
	if rt == nil {
		return fmt.Errorf("任务 %s 不存在", taskID)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.proj.Model != nil && *rt.proj.Model == ref {
		return nil
	}
	if contract.IsTerminal(rt.proj.State) {
		return nil
	}
	event := contract.TaskEvent{Type: contract.EventModelSwitched, Model: &ref}
	if rt.proj.Model != nil && ref != (contract.ModelRef{}) {
		from := *rt.proj.Model
		event.FromModel = &from
	}
	return e.appendLocked(rt, event)
}

// SetWorkspaces 替换工作区表（工作区登记变化后调用，写交付物时据此找目录）。
func (e *Engine) SetWorkspaces(resolve func(workspaceID string) (string, bool)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.workspaces = resolve
}

func (e *Engine) workspaceDir(workspaceID string) (string, bool) {
	e.mu.Lock()
	resolve := e.workspaces
	e.mu.Unlock()
	if resolve == nil || workspaceID == "" {
		return "", false
	}
	return resolve(workspaceID)
}

// ---------------------------------------------------------------- 运行时

type taskRuntime struct {
	id  string
	dir string
	// workspaceID 任务所属的工作区：建任务时写进发起事件，之后每条事件都带上
	workspaceID string
	config      *contract.EmployeeConfig
	proj        *projection
	events      []contract.TaskEvent
	logPath     string

	mu        sync.Mutex
	listeners []*listenerEntry
	driving   bool
	done      chan struct{}
	redrive   bool
	failure   error

	artifactCache map[string]any

	// signal 任务被终止时取消。已终止是终态，所以一个运行时只需要一个，不用重置。
	signal context.Context
	stop   context.CancelFunc
}

func (e *Engine) newRuntime(id, dir string, cfg *contract.EmployeeConfig) *taskRuntime {
	signal, stop := context.WithCancel(context.Background())
	return &taskRuntime{
		id: id, dir: dir, config: cfg, proj: newProjection(cfg),
		events: []contract.TaskEvent{}, logPath: filepath.Join(dir, "events.jsonl"),
		artifactCache: map[string]any{},
		signal:        signal, stop: stop,
	}
}

// view 在持有运行时锁的情况下读取投影（引擎的读操作都走这里）。
func view[T any](rt *taskRuntime, fn func(p *projection) T) T {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return fn(rt.proj)
}

// checkpointLookup 多返回值投影读取的载体（Go 的泛型不能承载元组返回值）。
type checkpointLookup struct {
	Checkpoint Checkpoint
	OK         bool
}

func (c checkpointLookup) values() (Checkpoint, bool) { return c.Checkpoint, c.OK }

type runningView struct {
	Index      int
	ModelCalls int
	ElapsedMS  int64
}

type judgeView struct {
	ReworkRound int
	ModelCalls  int
	ElapsedMS   int64
	Feedback    *contract.Feedback
	Checks      []contract.QcCheckResult
}

func (e *Engine) engineDead() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dead != nil || e.stopped
}

func (e *Engine) deadError() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dead != nil {
		return e.dead
	}
	return errEngineStopped
}

// errEngineStopped 引擎已停下，不再写入。
var errEngineStopped = errors.New("引擎已停止")

// refusesCommands 正在停下或已停下的引擎不接受新命令。
func (e *Engine) refusesCommands() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stopping || e.stopped
}

// errEngineStopping 正在停下：新命令一律拒绝。
var errEngineStopping = errors.New("引擎正在停止")

// stopRefusal 新命令被拒时的错误码与说明。停下只针对新命令：已经建好的任务照常执行，
// 不会因为引擎停下而半路被打断。
func (e *Engine) stopRefusal() (code, message string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopErr == nil {
		return contract.ErrIllegalTransition, "引擎正在停止"
	}
	return codeOfEngineStop, "应用正在退出：已建好的任务会继续执行，不能再开新任务或发命令"
}

// codeOfEngineStop 应用退出时新命令的错误码。
const codeOfEngineStop = "engine_stopping"

// Stop 停下引擎（进程退出时用）：之后不接受新命令、不建任务。
//
// 有任务处于准入校验、执行中或质检中时拒绝，返回 *EngineBusyError，引擎照常工作。
// 先挡住新命令再检查状态：命令在持有任务锁时检查 stopping，所以检查时读到的状态不会再变成活跃。
func (e *Engine) Stop() (busy *contract.EngineBusyError) {
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return nil
	}
	e.stopping = true
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.stopping = false
		e.mu.Unlock()
	}()

	ids := []string{}
	for _, summary := range e.List() {
		if activeStates[summary.State] {
			ids = append(ids, summary.ID)
		}
	}
	if len(ids) > 0 {
		return &contract.EngineBusyError{TaskIDs: ids}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopped = true
	e.stopErr = errEngineStopping
	return nil
}

// append 编排写事件的入口：落盘、更新投影、通知订阅者。调用方不得持有 rt.mu。
// 任务一旦终止，迟到的步骤结果在这里被丢弃；命令走 appendLocked，不受影响。
func (e *Engine) append(rt *taskRuntime, event contract.TaskEvent) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.proj.State == contract.StateTerminated {
		return errTaskStopped
	}
	return e.appendLocked(rt, event)
}

// appendLocked 追加一条事件（调用方必须持有 rt.mu）。
func (e *Engine) appendLocked(rt *taskRuntime, event contract.TaskEvent) error {
	if e.engineDead() {
		return e.deadError()
	}
	event.Seq = rt.proj.LastSeq + 1
	event.TS = e.nowISO()
	event.TaskID = rt.id
	// 事件日志是给这个任务的所有读者看的：工作区每次都写全（只有一个标识，不影响对拍）
	event.WorkspaceID = rt.workspaceID
	if err := AppendEventLine(rt.logPath, event); err != nil {
		return err
	}
	rt.events = append(rt.events, event)
	rt.proj.Apply(event)
	for _, entry := range rt.listeners {
		if entry.active {
			entry.fn(event)
		}
	}
	return nil
}

// publishTransient 只推给订阅者：不写事件日志、不进投影、不占 seq。
// 判断步骤的模型输出增量走这里（Spec 0006“员工步骤在对话中的形式”）：正在看的人能边输出边看到，
// 刷新或重连之后不补发；落盘的仍是一次调用的完整响应（model_called）。
// 调用方不得持有 rt.mu。
func (e *Engine) publishTransient(rt *taskRuntime, event contract.TaskEvent) {
	if e.engineDead() {
		return
	}
	event.Seq = 0
	event.TS = e.nowISO()
	event.TaskID = rt.id
	event.WorkspaceID = rt.workspaceID
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.proj.State == contract.StateTerminated {
		return
	}
	for _, entry := range rt.listeners {
		if entry.active {
			entry.fn(event)
		}
	}
}

// hasActiveListener 现在有没有人在看这个任务的事件流。
// 判断步骤据此决定要不要流式推送增量：没人看时不请求流式，省掉没用的分片。
func (e *Engine) hasActiveListener(rt *taskRuntime) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, entry := range rt.listeners {
		if entry.active {
			return true
		}
	}
	return false
}

func (e *Engine) snapshotOf(rt *taskRuntime) *contract.TaskSnapshot {
	nowMS := e.now().UnixMilli()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	snapshot := rt.proj.Snapshot(rt.id, nowMS)
	return &snapshot
}

func (e *Engine) nowISO() string {
	return e.now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// transitionTarget 按流转表查目标状态（调用方持有 rt.mu）。retry 在 awaiting_operator 下有两个目标：
// 失败的是准入步骤（尚未完成）回到准入，否则回到执行。
func transitionTarget(rt *taskRuntime, from contract.TaskState, trigger contract.TransitionTrigger) (contract.TaskState, bool) {
	transition, ok := contract.FindTransition(from, trigger)
	if !ok {
		return "", false
	}
	if trigger == contract.TriggerRetry && rt.config.Workflow.AdmissionStep != "" {
		if _, admissionDone := rt.proj.done[rt.config.Workflow.AdmissionStep]; !admissionDone {
			return contract.StateAdmission, true
		}
	}
	return transition.To, true
}

// transition 按流转表推进状态。
func (e *Engine) transition(rt *taskRuntime, trigger contract.TransitionTrigger, actor contract.Role, pause *contract.PauseReason) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return e.transitionLocked(rt, trigger, actor, pause)
}

func (e *Engine) transitionLocked(rt *taskRuntime, trigger contract.TransitionTrigger, actor contract.Role, pause *contract.PauseReason) error {
	from := rt.proj.State
	to, ok := transitionTarget(rt, from, trigger)
	if !ok {
		return fmt.Errorf("流转表中没有 %s --%s-->", from, trigger)
	}
	return e.appendLocked(rt, contract.TaskEvent{
		Type: contract.EventStateChanged, From: from, To: to, Trigger: trigger, Actor: actor, Pause: pause,
	})
}

// ---------------------------------------------------------------- 工件

func (e *Engine) writeArtifact(rt *taskRuntime, value any) (Checkpoint, error) {
	if value == nil {
		value = nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return Checkpoint{}, err
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	dir := filepath.Join(rt.dir, "artifacts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Checkpoint{}, err
	}
	path := filepath.Join(dir, hash+".json")
	if _, statErr := os.Stat(path); statErr != nil {
		tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
		if err := os.WriteFile(tmp, raw, 0o644); err != nil {
			return Checkpoint{}, err
		}
		if err := os.Rename(tmp, path); err != nil {
			return Checkpoint{}, err
		}
	}
	var cached any
	if err := json.Unmarshal(raw, &cached); err != nil {
		cached = value
	}
	rt.mu.Lock()
	rt.artifactCache[path] = cached
	rt.mu.Unlock()
	return Checkpoint{Path: path, SHA256: hash}, nil
}

func (e *Engine) readArtifact(rt *taskRuntime, ref Checkpoint) (any, error) {
	rt.mu.Lock()
	if value, ok := rt.artifactCache[ref.Path]; ok {
		rt.mu.Unlock()
		return value, nil
	}
	rt.mu.Unlock()
	raw, err := os.ReadFile(ref.Path)
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	rt.mu.Lock()
	rt.artifactCache[ref.Path] = value
	rt.mu.Unlock()
	return value, nil
}

// stepOutput 某步骤最近一次完成的输出；重做或修正中被回退的步骤，取回退前的那一版。
func (e *Engine) stepOutput(rt *taskRuntime, stepID string) any {
	checkpoint, ok := view(rt, func(p *projection) checkpointLookup {
		if value, found := p.done[stepID]; found {
			return checkpointLookup{Checkpoint: value, OK: true}
		}
		value, found := p.prior[stepID]
		return checkpointLookup{Checkpoint: value, OK: found}
	}).values()
	if !ok {
		return nil
	}
	value, err := e.readArtifact(rt, checkpoint)
	if err != nil {
		return nil
	}
	return value
}

// optionalOutput 某步骤本轮已完成的输出（不回退到旧版）。
func (e *Engine) optionalOutput(rt *taskRuntime, stepID string) any {
	checkpoint, ok := view(rt, func(p *projection) checkpointLookup {
		value, found := p.done[stepID]
		return checkpointLookup{Checkpoint: value, OK: found}
	}).values()
	if !ok {
		return nil
	}
	value, err := e.readArtifact(rt, checkpoint)
	if err != nil {
		return nil
	}
	return value
}

func (e *Engine) factsStepID(rt *taskRuntime) string {
	if rt.config.Workflow.FactsStep != "" {
		return rt.config.Workflow.FactsStep
	}
	return defaultFactsStep
}

// contradictionsOf 程序判定纪要矛盾的步骤（工具为 detect_contradictions 的计算步骤）。
func (e *Engine) contradictionsOf(rt *taskRuntime) []contract.DetectedContradiction {
	var stepID string
	for _, step := range rt.config.Workflow.Steps {
		if step.Kind == contract.StepKindCompute && step.Tool == contradictionsTool {
			stepID = step.ID
			break
		}
	}
	if stepID == "" {
		return nil
	}
	output, ok := e.optionalOutput(rt, stepID).(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := output["contradictions"].([]any)
	if !ok {
		return nil
	}
	out := make([]contract.DetectedContradiction, 0, len(raw))
	for _, item := range raw {
		var contradiction contract.DetectedContradiction
		if err := decodeInto(item, &contradiction); err == nil {
			out = append(out, contradiction)
		}
	}
	return out
}

// renderedOutput 某步骤输出的报告草稿，用事实步骤的输出做槽位渲染。
//
// 与 TS 版逐字段对齐：`{...draft, title: render(title), sections: [...]}`，
// 其中每个章节是 `{...section, heading: render(heading), paragraphs: [...]}`，
// paragraphs **恒为数组**（为空也写 []），bullets 只在原有值时出现。
func (e *Engine) renderedOutput(rt *taskRuntime, stepID string) any {
	draftMap, ok := e.stepOutput(rt, stepID).(map[string]any)
	if !ok {
		return nil
	}
	factsMap, ok := e.stepOutput(rt, e.factsStepID(rt)).(map[string]any)
	if !ok {
		return nil
	}
	slots := qc.NewSlotsFromMap(factsMap)
	render := func(text string) string { return slots.Render(text).Text }

	out := make(map[string]any, len(draftMap))
	for key, value := range draftMap {
		out[key] = value
	}
	if title, ok := draftMap["title"].(string); ok {
		out["title"] = render(title)
	}
	sections := []any{}
	if rawSections, ok := draftMap["sections"].([]any); ok {
		for _, rawSection := range rawSections {
			section, ok := rawSection.(map[string]any)
			if !ok {
				continue
			}
			next := make(map[string]any, len(section))
			for key, value := range section {
				next[key] = value
			}
			if heading, ok := section["heading"].(string); ok {
				next["heading"] = render(heading)
			}
			paragraphs := []any{}
			if rawParagraphs, ok := section["paragraphs"].([]any); ok {
				for _, rawParagraph := range rawParagraphs {
					if text, ok := rawParagraph.(string); ok {
						paragraphs = append(paragraphs, render(text))
					}
				}
			}
			next["paragraphs"] = paragraphs
			if rawBullets, ok := section["bullets"].([]any); ok && len(rawBullets) > 0 {
				bullets := []any{}
				for _, rawBullet := range rawBullets {
					if text, ok := rawBullet.(string); ok {
						bullets = append(bullets, render(text))
					}
				}
				next["bullets"] = bullets
			}
			sections = append(sections, next)
		}
	}
	out["sections"] = sections
	return out
}

func decodeInto(value any, target any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

// ---------------------------------------------------------------- 设置

// executionDefaults 执行与预算的默认值（settings.execution，Spec 0004）。
// 每个判断步骤开始时读一次：设置页保存后对之后新建 / 新开始的步骤立即生效；
// 设置文件缺失或损坏时返回 nil，由运行器退回内置值。
func (e *Engine) executionDefaults() *contract.ExecutionSettings {
	document := settings.Read(e.dir)
	execution := document.Execution
	return &execution
}

// persistedCrashArmed settings.json 里持久化的一次性崩溃标记；读取失败不影响任务执行。
func (e *Engine) persistedCrashArmed() bool {
	return settings.LoadDemo(e.dir).CrashOnNextStep
}

// persistFaultCleared 一次性开关命中后同步清除持久化标记（崩溃时必须在进程退出之前完成）。
func (e *Engine) persistFaultCleared(patch contract.DemoSettings, setCrash, setCorrupt bool) {
	if _, err := settings.SaveDemo(e.dir, patch, false, setCorrupt, setCrash); err != nil {
		// 演示开关的持久化失败不应影响任务本身
		_ = err
	}
}

// ---------------------------------------------------------------- 编排

func (e *Engine) kick(rt *taskRuntime) {
	rt.mu.Lock()
	if rt.driving {
		rt.redrive = true
		rt.mu.Unlock()
		return
	}
	rt.driving = true
	done := make(chan struct{})
	rt.done = done
	rt.mu.Unlock()

	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				rt.mu.Lock()
				rt.failure = fmt.Errorf("任务执行时发生内部错误：%v", recovered)
				rt.mu.Unlock()
			}
			rt.mu.Lock()
			rt.driving = false
			rt.done = nil
			close(done)
			rt.mu.Unlock()
		}()
		for {
			rt.mu.Lock()
			rt.redrive = false
			rt.mu.Unlock()
			if err := e.drive(rt); err != nil {
				rt.mu.Lock()
				rt.failure = err
				rt.mu.Unlock()
			}
			rt.mu.Lock()
			again := rt.redrive
			rt.mu.Unlock()
			if !again {
				return
			}
		}
	}()
}

func (e *Engine) drive(rt *taskRuntime) (err error) {
	for {
		state := view(rt, func(p *projection) contract.TaskState { return p.State })
		if !activeStates[state] {
			return nil
		}
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("任务执行时发生内部错误：%v", recovered)
				}
			}()
			switch state {
			case contract.StateAdmission:
				err = e.stepAdmission(rt)
			case contract.StateRunning:
				err = e.stepRunning(rt)
			default:
				err = e.stepQc(rt)
			}
		}()
		if err != nil {
			var crash *SimulatedCrash
			if errors.As(err, &crash) || e.engineDead() {
				return err
			}
			// 终止之后步骤怎么结束都不算数，不转人工
			if view(rt, func(p *projection) contract.TaskState { return p.State }) == contract.StateTerminated {
				return nil
			}
			if pauseErr := e.unexpectedFailure(rt, err); pauseErr != nil {
				return pauseErr
			}
			err = nil
		}
	}
}

// unexpectedFailure 引擎自身遇到未预料的错误：留痕并转人工，不吞掉也不崩溃。
func (e *Engine) unexpectedFailure(rt *taskRuntime, failure error) error {
	message := "任务执行时发生内部错误。"
	detail := map[string]any{"error": failure.Error()}
	state := view(rt, func(p *projection) contract.TaskState { return p.State })
	switch state {
	case contract.StateAdmission:
		return e.pauseOperator(rt, contract.TriggerToolFailed, contract.PauseToolFailed, message, detail)
	case contract.StateRunning:
		return e.pauseOperator(rt, contract.TriggerToolFailed, contract.PauseToolFailed, message, detail)
	default:
		return e.pauseOperator(rt, contract.TriggerQcExhausted, contract.PauseToolFailed, message, detail)
	}
}

func (e *Engine) pauseOperator(rt *taskRuntime, trigger contract.TransitionTrigger, code contract.PauseCode, message string, detail map[string]any) error {
	return e.transition(rt, trigger, contract.RoleSystem, &contract.PauseReason{
		Code: code, Message: message, Options: pauseOptions(contract.StateAwaitingOperator, code), Detail: detail,
	})
}

func (e *Engine) progressText(rt *taskRuntime) string {
	doneCount := view(rt, func(p *projection) int { return len(p.done) })
	return fmt.Sprintf("已完成 %d/%d 步", doneCount, len(rt.config.Workflow.Steps))
}

func (e *Engine) stepAdmission(rt *taskRuntime) error {
	// 没配准入步骤（只有技能的员工）：没有材料要校验，直接通过
	if rt.config.Workflow.AdmissionStep == "" {
		return e.transition(rt, contract.TriggerAdmissionPassed, contract.RoleSystem, nil)
	}
	var step *contract.StepConfig
	for index := range rt.config.Workflow.Steps {
		if rt.config.Workflow.Steps[index].ID == rt.config.Workflow.AdmissionStep {
			step = &rt.config.Workflow.Steps[index]
			break
		}
	}
	if step == nil {
		return fmt.Errorf("admissionStep %q 不在步骤中", rt.config.Workflow.AdmissionStep)
	}
	done := view(rt, func(p *projection) bool {
		_, ok := p.done[step.ID]
		return ok
	})
	if !done {
		outcome, err := e.executeStep(rt, *step)
		if err != nil {
			return err
		}
		if outcome == outcomePaused {
			return nil
		}
	}
	output, _ := e.optionalOutput(rt, step.ID).(map[string]any)
	okValue := true
	if output != nil {
		if raw, present := output["ok"]; present {
			if flag, isBool := raw.(bool); isBool {
				okValue = flag
			}
		}
	}
	if !okValue {
		code := contract.PauseInvalidInput
		message := "准入校验不通过。"
		var detail map[string]any
		if pause, ok := output["pause"].(map[string]any); ok {
			if raw, ok := pause["code"].(string); ok && raw != "" {
				code = contract.PauseCode(raw)
			}
			if raw, ok := pause["message"].(string); ok && raw != "" {
				message = raw
			}
			if raw, ok := pause["detail"].(map[string]any); ok {
				detail = raw
			}
		}
		return e.transition(rt, contract.TriggerAdmissionFailed, contract.RoleSystem, &contract.PauseReason{
			Code: code, Message: message, Options: pauseOptions(contract.StateAwaitingInitiator, code), Detail: detail,
		})
	}
	return e.transition(rt, contract.TriggerAdmissionPassed, contract.RoleSystem, nil)
}

func (e *Engine) stepRunning(rt *taskRuntime) error {
	running := view(rt, func(p *projection) runningView {
		return runningView{
			Index: p.NextStepIndex(rt.config.Workflow.AdmissionStep), ModelCalls: p.ModelCalls,
			ElapsedMS: p.ElapsedMS(e.now().UnixMilli()),
		}
	})
	index, modelCalls, elapsed := running.Index, running.ModelCalls, running.ElapsedMS
	if index < 0 {
		return e.transition(rt, contract.TriggerStepsCompleted, contract.RoleSystem, nil)
	}
	step := rt.config.Workflow.Steps[index]
	limits := rt.config.Workflow.Limits.Task
	if elapsed >= int64(limits.DurationSec)*1000 {
		message := fmt.Sprintf("任务总时长已达上限（%d 秒），在步骤「%s」之前暂停。%s。",
			limits.DurationSec, step.Label, e.progressText(rt))
		return e.pauseOperator(rt, contract.TriggerLimitExceeded, contract.PauseLimitExceeded, message, map[string]any{
			"limit": "durationSec",
			"used":  elapsed,
		})
	}
	if step.Kind == contract.StepKindJudge && modelCalls >= limits.ModelCalls {
		message := fmt.Sprintf("模型调用次数已达上限（%d/%d），在步骤「%s」之前暂停。%s。",
			modelCalls, limits.ModelCalls, step.Label, e.progressText(rt))
		return e.pauseOperator(rt, contract.TriggerLimitExceeded, contract.PauseLimitExceeded, message, map[string]any{
			"limit": "modelCalls",
			"used":  modelCalls,
		})
	}
	_, err := e.executeStep(rt, step)
	return err
}

const (
	outcomeDone   = "done"
	outcomePaused = "paused"
)

// executeStep 执行一个计算步骤或判断步骤。失败时已完成状态流转（或跳过可选步骤）。
func (e *Engine) executeStep(rt *taskRuntime, step contract.StepConfig) (string, error) {
	if step.Kind == contract.StepKindQC {
		return "", errors.New("质检步骤不由此处执行")
	}
	isAdmission := step.ID == rt.config.Workflow.AdmissionStep
	reworkRound := view(rt, func(p *projection) int { return p.ReworkRound })

	placeholder := "\x00idempotencyKey:" + step.ID
	form := view(rt, func(p *projection) map[string]any { return p.Form })
	attachments := view(rt, func(p *projection) []contract.AttachmentRef { return p.Attachments })
	feedback := view(rt, func(p *projection) *contract.Feedback { return p.Feedback })

	ctx := RefContext{
		Form:        form,
		TaskDir:     rt.dir,
		Attachments: attachments,
		QCConfig:    jsonShape(rt.config.QC),
		StepOutput:  func(id string) any { return e.stepOutput(rt, id) },
		RenderedOutput: func(id string) any {
			return e.renderedOutput(rt, id)
		},
		IdempotencyKey: placeholder,
	}
	if feedback != nil {
		ctx.Feedback = feedback
	}

	var args any
	var resolveErr error
	resolved := ResolveInput(step.Input, ctx)
	if resolved == nil {
		resolveErr = errors.New("入参无法解析")
	} else {
		// 幂等键 = 任务 + 步骤 + 重做轮次 + 入参哈希（入参哈希不含键本身）
		key := fmt.Sprintf("%s-%s-r%d-%s", rt.id, step.ID, reworkRound, sha256Hex(argsKey(resolved))[:16])
		args = ReplaceString(resolved, placeholder, key)
	}

	// 重做：计算步骤的入参和上次相同，直接复用已有输出，不重新执行
	if resolveErr == nil && step.Kind == contract.StepKindCompute && !isAdmission {
		prior, ok := view(rt, func(p *projection) checkpointLookup {
			value, found := p.prior[step.ID]
			return checkpointLookup{Checkpoint: value, OK: found}
		}).values()
		if ok && prior.Args != nil && *prior.Args == argsKey(args) {
			return outcomeDone, e.append(rt, contract.TaskEvent{
				Type: contract.EventStepCompleted, StepID: step.ID, ReworkRound: contract.IntPtr(reworkRound),
				OutputPath: prior.Path, OutputSHA256: prior.SHA256,
			})
		}
	}

	attempt := view(rt, func(p *projection) int { return p.startCounts[step.ID] }) + 1
	if err := e.append(rt, contract.TaskEvent{
		Type: contract.EventStepStarted, StepID: step.ID,
		Attempt: contract.IntPtr(attempt), ReworkRound: contract.IntPtr(reworkRound),
	}); err != nil {
		return "", err
	}
	crashArmed := e.crashArmed(step.ID)
	if crashArmed && step.Kind == contract.StepKindCompute {
		return "", e.crash(rt, step.ID)
	}
	if resolveErr != nil {
		message := fmt.Sprintf("步骤「%s」的输入无法解析：%v。%s。", step.Label, resolveErr, e.progressText(rt))
		return e.failStep(rt, step, contract.TriggerToolFailed, message, message, nil)
	}
	if step.Kind == contract.StepKindCompute {
		return e.runCompute(rt, step, args)
	}
	return e.runJudge(rt, step, args, crashArmed)
}

func (e *Engine) crashArmed(stepID string) bool {
	e.mu.Lock()
	faults := *e.faults
	e.mu.Unlock()
	return faults.CrashAtStep == stepID || faults.CrashOnNextStep || e.persistedCrashArmed()
}

// crash 模拟进程崩溃：先写 fault_injected，再清除一次性标记，最后退出进程。
func (e *Engine) crash(rt *taskRuntime, stepID string) error {
	if err := e.append(rt, contract.TaskEvent{
		Type: contract.EventFaultInjected, Target: stepID, Mode: "crash",
	}); err != nil {
		return err
	}
	// 一次性：内存开关与 settings.json 里的标记都清掉，重启后 resumeAll 不会再次崩溃
	e.mu.Lock()
	e.faults.CrashOnNextStep = false
	e.mu.Unlock()
	e.persistFaultCleared(contract.DemoSettings{CrashOnNextStep: false}, true, false)
	crash := &SimulatedCrash{StepID: stepID}
	e.mu.Lock()
	e.dead = crash
	e.mu.Unlock()
	if e.exitOnCrash {
		os.Exit(1)
	}
	return crash
}

// failStep 步骤失败：写 step_failed；可选步骤直接跳过，否则转暂停。
// stepError 是给轨迹看的技术说明，message 是给人看的暂停说明，工具名和失败代码放进 detail。
func (e *Engine) failStep(rt *taskRuntime, step contract.StepConfig, code contract.TransitionTrigger, stepError, message string, detail map[string]any) (string, error) {
	if err := e.append(rt, contract.TaskEvent{
		Type: contract.EventStepFailed, StepID: step.ID, Error: stepError,
	}); err != nil {
		return "", err
	}
	if step.Optional {
		return outcomeDone, nil
	}
	state := view(rt, func(p *projection) contract.TaskState { return p.State })
	trigger := code
	pauseCode := contract.PauseCode(code)
	if state == contract.StateAdmission {
		// 准入步骤的工具本身故障也是工具故障（转操作员），不是缺材料
		trigger = contract.TriggerToolFailed
	}
	return outcomePaused, e.pauseOperator(rt, trigger, pauseCode, message, detail)
}

// failForMaterial 工具报告材料问题：写 step_failed，转待发起人补充。可选步骤照常跳过。
func (e *Engine) failForMaterial(rt *taskRuntime, step contract.StepConfig, result contract.ToolCallResult) (string, error) {
	issue := result.MaterialIssue
	if err := e.append(rt, contract.TaskEvent{
		Type: contract.EventStepFailed, StepID: step.ID, Error: result.Error,
	}); err != nil {
		return "", err
	}
	if step.Optional {
		return outcomeDone, nil
	}
	return outcomePaused, e.transition(rt, contract.TriggerMaterialIssue, contract.RoleSystem, &contract.PauseReason{
		Code:    contract.PauseMaterialIssue,
		Message: fmt.Sprintf("步骤「%s」发现材料问题：%s", step.Label, issue.Message),
		Options: pauseOptions(contract.StateAwaitingInitiator, contract.PauseMaterialIssue),
		Detail:  map[string]any{"tool": step.Tool, "path": issue.Path, "error": result.Error},
	})
}

func (e *Engine) completeStep(rt *taskRuntime, step contract.StepConfig, output any) error {
	artifact, err := e.writeArtifact(rt, output)
	if err != nil {
		return err
	}
	reworkRound := view(rt, func(p *projection) int { return p.ReworkRound })
	return e.append(rt, contract.TaskEvent{
		Type: contract.EventStepCompleted, StepID: step.ID, ReworkRound: contract.IntPtr(reworkRound),
		OutputPath: artifact.Path, OutputSHA256: artifact.SHA256,
	})
}

func (e *Engine) runCompute(rt *taskRuntime, step contract.StepConfig, args any) (string, error) {
	retry := rt.config.Workflow.Limits.ToolRetry
	backoffMS := agent.ResolveToolRetryBackoffMS(retry.BackoffMS, e.executionDefaults())
	result := contract.ToolCallResult{OK: false, Error: "未调用", Retryable: false}
	tried := 0
	for attempt := 1; attempt <= retry.Attempts; attempt++ {
		tried = attempt
		callID := newUUID()
		if err := e.append(rt, contract.TaskEvent{
			Type: contract.EventToolCalled, StepID: step.ID, CallID: callID, Tool: step.Tool,
			Args: args, Attempt: contract.IntPtr(attempt),
		}); err != nil {
			return "", err
		}
		called, err := e.toolClient.Call(step.Tool, args)
		if err != nil {
			result = contract.ToolCallResult{OK: false, Error: err.Error(), Retryable: true}
		} else {
			result = called
		}
		if e.engineDead() {
			return "", e.deadError()
		}
		resultEvent := contract.TaskEvent{
			Type: contract.EventToolResult, CallID: callID, OK: contract.BoolPtr(result.OK),
		}
		if result.OK {
			resultEvent.ResultSHA256 = sha256Hex(jsonText(result.Data))
		} else {
			resultEvent.Error = result.Error
		}
		if err := e.append(rt, resultEvent); err != nil {
			return "", err
		}
		if result.OK || !result.Retryable || attempt == retry.Attempts {
			break
		}
		wait := 0
		if len(backoffMS) > 0 {
			position := attempt - 1
			if position >= len(backoffMS) {
				position = len(backoffMS) - 1
			}
			wait = backoffMS[position]
		}
		if wait > 0 {
			timer := time.NewTimer(time.Duration(wait) * time.Millisecond)
			select {
			case <-timer.C:
			case <-rt.signal.Done():
				timer.Stop()
				return "", errTaskStopped
			}
		}
	}
	if !result.OK && result.MaterialIssue != nil {
		return e.failForMaterial(rt, step, result)
	}
	if !result.OK {
		return e.failStep(rt, step, contract.TriggerToolFailed,
			fmt.Sprintf("工具 %s 调用失败（共尝试 %d 次）：%s。%s。", step.Tool, tried, result.Error, e.progressText(rt)),
			fmt.Sprintf("步骤「%s」执行失败，已尝试 %d 次。%s。", step.Label, tried, e.progressText(rt)),
			map[string]any{
				"tool": step.Tool, "attempts": tried, "error": result.Error,
				"completedSteps": view(rt, func(p *projection) int { return len(p.done) }),
				"totalSteps":     len(rt.config.Workflow.Steps),
			})
	}
	return outcomeDone, e.completeStep(rt, step, result.Data)
}

// judgmentPause 判断失败原因 → 暂停触发器。
var judgmentPause = map[contract.JudgmentFailure]contract.TransitionTrigger{
	contract.FailStepRounds:    contract.TriggerLimitExceeded,
	contract.FailStepDuration:  contract.TriggerLimitExceeded,
	contract.FailTaskCalls:     contract.TriggerLimitExceeded,
	contract.FailTaskDuration:  contract.TriggerLimitExceeded,
	contract.FailModelError:    contract.TriggerToolFailed,
	contract.FailNoOutput:      contract.TriggerToolFailed,
	contract.FailInvalidOutput: contract.TriggerToolFailed,

	contract.FailModelUnavailable: contract.TriggerModelUnavailable,
}

func (e *Engine) runJudge(rt *taskRuntime, step contract.StepConfig, context any, crashArmed bool) (string, error) {
	input, ok := context.(map[string]any)
	if !ok {
		input = map[string]any{"value": context}
	}
	prompt := rt.config.Prompts[config.NormalizeRef(step.Prompt)]
	outputSchema := rt.config.OutputSchemas[config.NormalizeRef(step.OutputSchema)]
	if outputSchema == nil {
		outputSchema = map[string]any{}
	}

	judgeState := view(rt, func(p *projection) judgeView {
		return judgeView{
			ReworkRound: p.ReworkRound, ModelCalls: p.ModelCalls, ElapsedMS: p.ElapsedMS(e.now().UnixMilli()),
			Feedback: p.Feedback, Checks: p.QC,
		}
	})
	reworkRound, modelCalls, elapsed := judgeState.ReworkRound, judgeState.ModelCalls, judgeState.ElapsedMS
	feedback, qcChecks := judgeState.Feedback, judgeState.Checks
	responses := view(rt, func(p *projection) []ModelResponse {
		return append([]ModelResponse{}, p.modelResponses[step.ID]...)
	})
	sort.SliceStable(responses, func(a, b int) bool { return responses[a].Turn < responses[b].Turn })
	replay := make([]any, 0, len(responses))
	for _, response := range responses {
		replay = append(replay, response.Response)
	}

	corrections := []string{}
	if reworkRound > 0 && feedback != nil {
		sections := ""
		if len(feedback.Sections) > 0 {
			sections = "（涉及章节：" + strings.Join(feedback.Sections, "、") + "）"
		}
		corrections = append(corrections, "审核人退回意见"+sections+"："+feedback.Comment)
	}
	// 质检问题只交给 fixStep 指向本步骤的那些判断步骤，其余步骤不需要看到与自己无关的问题
	for _, check := range qcChecks {
		if check.Passed || check.FixStep != step.ID {
			continue
		}
		for _, issue := range check.Issues {
			corrections = append(corrections, fmt.Sprintf("质检「%s」：%s", check.Label, issue))
		}
	}

	form := view(rt, func(p *projection) map[string]any { return p.Form })
	writingRequirements := config.CollectWritingRequirements(rt.config.FormSchema, form)
	judgmentContext := input
	if len(writingRequirements) > 0 {
		judgmentContext = map[string]any{}
		for key, value := range input {
			judgmentContext[key] = value
		}
		judgmentContext["writingRequirements"] = writingRequirements
	}

	limits := rt.config.Workflow.Limits.Task
	remaining := limits.ModelCalls - modelCalls
	if remaining < 0 {
		remaining = 0
	}
	remaining += len(replay)
	available := int64(limits.DurationSec)*1000 - elapsed
	if available < 0 {
		available = 0
	}
	deadline := time.UnixMilli(e.now().UnixMilli() + available)

	// 任务当前的会话模型：每个判断步骤开始时读一次，运行中切换的模型从这里生效（Spec 0006）
	taskModel := view(rt, func(p *projection) contract.ModelRef {
		if p.Model != nil {
			return *p.Model
		}
		return contract.ModelRef{}
	})

	// 有人正在看事件流时才把判断步骤的模型输出逐片推出去（不落盘）；没人看时保持一次性调用
	var onModelDelta func(agent.ModelDeltaEvent)
	if e.hasActiveListener(rt) {
		onModelDelta = func(event agent.ModelDeltaEvent) {
			e.publishTransient(rt, contract.TaskEvent{
				Type: contract.EventModelDelta, StepID: event.StepID, Turn: contract.IntPtr(event.Turn), Text: event.Text,
			})
		}
	}

	var crashErr error
	result, runErr := e.judgment.Run(agent.JudgmentInput{
		TaskID:       rt.id,
		Step:         step,
		SystemPrompt: joinPrompt(rt.config.Skill, prompt),
		Context:      judgmentContext,
		Corrections:  corrections,
		ToolClient:   e.toolClient,
		Model:        e.modelProvider(),
		ModelRef:     &taskModel,
		OutputSchema: outputSchema,
		Budget:       agent.Budget{RemainingModelCalls: remaining, Deadline: deadline},
		Replay:       replay,
		Signal:       rt.signal,
		OnModelCalled: func(event agent.ModelCalledEvent) error {
			seen := view(rt, func(p *projection) bool {
				for _, response := range p.modelResponses[step.ID] {
					if response.Turn == event.Turn {
						return true
					}
				}
				return false
			})
			if !seen {
				if err := e.append(rt, contract.TaskEvent{
					Type: contract.EventModelCalled, StepID: step.ID, Turn: contract.IntPtr(event.Turn),
					Response: event.Response, Usage: event.Usage, DurationMS: contract.Int64Ptr(event.DurationMS),
					Connection: event.Connection, ModelName: event.Model,
				}); err != nil {
					crashErr = err
					return err
				}
			}
			if crashArmed {
				crashErr = e.crash(rt, step.ID)
				return crashErr
			}
			return nil
		},
		OnModelDelta: onModelDelta,
		OnToolCalled: func(event agent.ToolCalledEvent) error {
			callID := newUUID()
			if err := e.append(rt, contract.TaskEvent{
				Type: contract.EventToolCalled, StepID: step.ID, CallID: callID, Tool: event.Tool,
				Args: event.Args, Attempt: contract.IntPtr(1),
			}); err != nil {
				crashErr = err
				return err
			}
			resultEvent := contract.TaskEvent{
				Type: contract.EventToolResult, CallID: callID, OK: contract.BoolPtr(event.OK),
			}
			if !event.OK {
				resultEvent.Error = event.Error
			}
			if err := e.append(rt, resultEvent); err != nil {
				crashErr = err
				return err
			}
			return nil
		},
		OnToolDenied: func(event agent.ToolDeniedEvent) error {
			if err := e.append(rt, contract.TaskEvent{
				Type: contract.EventToolDenied, StepID: step.ID, Tool: event.Tool, Reason: event.Reason,
			}); err != nil {
				crashErr = err
				return err
			}
			return nil
		},
	})
	if runErr != nil {
		var crash *SimulatedCrash
		if errors.As(runErr, &crash) {
			return "", runErr
		}
		if crashErr != nil {
			return "", crashErr
		}
	}
	if e.engineDead() {
		return "", e.deadError()
	}
	if crashErr != nil {
		return "", crashErr
	}
	if crashArmed {
		return "", e.crash(rt, step.ID)
	}
	if !result.OK {
		trigger, known := judgmentPause[result.Failure]
		if !known {
			trigger = contract.TriggerToolFailed
		}
		detail := map[string]any{
			"failure":        string(result.Failure),
			"error":          result.Message,
			"completedSteps": view(rt, func(p *projection) int { return len(p.done) }),
			"totalSteps":     len(rt.config.Workflow.Steps),
		}
		if result.ModelError != "" {
			detail["modelError"] = string(result.ModelError)
		}
		return e.failStep(rt, step, trigger,
			fmt.Sprintf("步骤「%s」未能完成（%s）：%s。%s。", step.Label, result.Failure, result.Message, e.progressText(rt)),
			fmt.Sprintf("步骤「%s」未能完成。%s。", step.Label, e.progressText(rt)),
			detail)
	}

	output := result.Output
	if outputMap, ok := output.(map[string]any); ok {
		if needsHuman, _ := outputMap["needsHuman"].(bool); needsHuman {
			reason := ""
			if text, ok := outputMap["needsHumanReason"].(string); ok {
				reason = text
			}
			if reason == "" {
				reason = "该意见超出自动处理范围"
			}
			message := fmt.Sprintf("步骤「%s」判断需要人工处理：%s。%s。", step.Label, reason, e.progressText(rt))
			if !view(rt, func(p *projection) bool { return len(p.deliverables) > 0 }) {
				// 还没有交付物，待审核无物可审，只能按步骤失败处理
				return e.failStep(rt, step, contract.TriggerToolFailed, message, message, map[string]any{"output": output})
			}
			if err := e.append(rt, contract.TaskEvent{
				Type: contract.EventStepFailed, StepID: step.ID, Error: message,
			}); err != nil {
				return "", err
			}
			// 超范围意见回到待审核：原交付物和版本保持不变，由审核人修改意见、直接通过或终止
			return outcomePaused, e.transition(rt, contract.TriggerNeedsHuman, contract.RoleSystem, &contract.PauseReason{
				Code:    contract.PauseReworkNeedsHuman,
				Message: fmt.Sprintf("退回意见无法自动处理：%s。原交付物保持不变。", strings.TrimRight(reason, "。")),
				Options: pauseOptions(contract.StateAwaitingReview, contract.PauseReworkNeedsHuman),
				Detail:  map[string]any{"reason": reason, "step": step.ID, "output": output},
			})
		}
	}

	// 数据溯源故障：只对下一个写作步骤生效一次，命中后立即清除运行时与持久化开关
	e.mu.Lock()
	corrupt := e.faults.CorruptTraceability
	e.mu.Unlock()
	if corrupt && step.ID == rt.config.Workflow.QCSources.Draft {
		alreadyInjected := view(rt, func(p *projection) bool {
			return p.FaultsInjected[step.ID+":corruptTraceability"]
		})
		if !alreadyInjected {
			output = corruptFirstParagraph(output)
			if err := e.append(rt, contract.TaskEvent{
				Type: contract.EventFaultInjected, Target: step.ID, Mode: "corruptTraceability",
			}); err != nil {
				return "", err
			}
			e.mu.Lock()
			e.faults.CorruptTraceability = false
			e.mu.Unlock()
			e.persistFaultCleared(contract.DemoSettings{CorruptTraceability: false}, false, true)
		}
	}
	return outcomeDone, e.completeStep(rt, step, output)
}

func (e *Engine) stepQc(rt *taskRuntime) error {
	var qcStep *contract.StepConfig
	for index := range rt.config.Workflow.Steps {
		if rt.config.Workflow.Steps[index].Kind == contract.StepKindQC {
			qcStep = &rt.config.Workflow.Steps[index]
			break
		}
	}
	if qcStep == nil {
		return e.finishQcPassed(rt, []contract.QcCheckResult{})
	}
	done := view(rt, func(p *projection) bool {
		_, ok := p.done[qcStep.ID]
		return ok
	})
	if !done {
		attempt := view(rt, func(p *projection) int { return p.startCounts[qcStep.ID] }) + 1
		reworkRound := view(rt, func(p *projection) int { return p.ReworkRound })
		if err := e.append(rt, contract.TaskEvent{
			Type: contract.EventStepStarted, StepID: qcStep.ID,
			Attempt: contract.IntPtr(attempt), ReworkRound: contract.IntPtr(reworkRound),
		}); err != nil {
			return err
		}
		if e.crashArmed(qcStep.ID) {
			return e.crash(rt, qcStep.ID)
		}

		checks, err := e.runQcChecks(rt)
		if err != nil {
			stepError := fmt.Sprintf("质检引擎出错：%v。%s。", err, e.progressText(rt))
			if appendErr := e.append(rt, contract.TaskEvent{
				Type: contract.EventStepFailed, StepID: qcStep.ID, Error: stepError,
			}); appendErr != nil {
				return appendErr
			}
			return e.pauseOperator(rt, contract.TriggerQcExhausted, contract.PauseToolFailed,
				fmt.Sprintf("质检未能完成。%s。", e.progressText(rt)), map[string]any{"error": err.Error()})
		}
		allPassed := true
		for _, check := range checks {
			if !check.Passed {
				allPassed = false
				break
			}
		}
		qcAttempts := view(rt, func(p *projection) int { return p.QCAttempts })
		if err := e.append(rt, contract.TaskEvent{
			Type: contract.EventQCResult, Attempt: contract.IntPtr(qcAttempts + 1),
			Passed: contract.BoolPtr(allPassed), Checks: checks,
		}); err != nil {
			return err
		}
		if err := e.completeStep(rt, *qcStep, checks); err != nil {
			return err
		}
	}

	checksRaw := e.optionalOutput(rt, qcStep.ID)
	var checks []contract.QcCheckResult
	if err := decodeInto(checksRaw, &checks); err != nil {
		return err
	}
	failed := []contract.QcCheckResult{}
	for _, check := range checks {
		if !check.Passed {
			failed = append(failed, check)
		}
	}
	if len(failed) == 0 {
		return e.finishQcPassed(rt, checks)
	}
	issues := []string{}
	for _, check := range failed {
		for _, issue := range check.Issues {
			issues = append(issues, check.Label+"："+issue)
		}
	}
	fixable := true
	for _, check := range failed {
		if check.FixStep == "" {
			fixable = false
			break
		}
		found := false
		for _, step := range rt.config.Workflow.Steps {
			if step.ID == check.FixStep {
				found = true
				break
			}
		}
		if !found {
			fixable = false
			break
		}
	}
	// 自动修正上限：员工配置写了就以它为准，否则用设置里的 execution.qcAutofix（内置 3）
	limit := agent.ResolveQcAutofix(rt.config.Workflow.Limits.QCAutofix, e.executionDefaults())
	autofixCount := view(rt, func(p *projection) int { return p.AutofixCount })
	if fixable && autofixCount < limit {
		return e.transition(rt, contract.TriggerQcAutofix, contract.RoleSystem, nil)
	}
	why := "存在无法自动修正的问题"
	if fixable {
		why = fmt.Sprintf("自动修正已达上限（%d 次）", limit)
	}
	return e.pauseOperator(rt, contract.TriggerQcExhausted, contract.PauseQcExhausted,
		"质检未通过，"+why+"："+strings.Join(issues, "；"),
		map[string]any{"checks": failed})
}

func (e *Engine) runQcChecks(rt *taskRuntime) ([]contract.QcCheckResult, error) {
	sources := rt.config.Workflow.QCSources
	var rendered *contract.RenderedStats
	if raw, ok := e.optionalOutput(rt, sources.Rendered).(map[string]any); ok {
		estimatedPages, pagesOK := asFloat(raw["estimatedPages"])
		chartCount, chartsOK := asFloat(raw["chartCount"])
		if pagesOK && chartsOK {
			rendered = &contract.RenderedStats{EstimatedPages: estimatedPages, ChartCount: int(chartCount)}
		}
	}
	var draft contract.DraftReport
	if err := decodeInto(e.optionalOutput(rt, sources.Draft), &draft); err != nil {
		return nil, err
	}
	var facts contract.WeeklyFacts
	if err := decodeInto(e.optionalOutput(rt, e.factsStepID(rt)), &facts); err != nil {
		return nil, err
	}
	var minutes contract.ParseMinutesOutput
	if err := decodeInto(e.optionalOutput(rt, sources.Minutes), &minutes); err != nil {
		return nil, err
	}

	reworkRound := view(rt, func(p *projection) int { return p.ReworkRound })
	feedback := view(rt, func(p *projection) *contract.Feedback { return p.Feedback })
	baseline := view(rt, func(p *projection) *Checkpoint { return p.BaselineDraft })

	var previousDraft *contract.DraftReport
	if reworkRound > 0 && baseline != nil {
		var previous contract.DraftReport
		if raw, err := e.readArtifact(rt, *baseline); err == nil {
			if decodeErr := decodeInto(raw, &previous); decodeErr == nil {
				previousDraft = &previous
			}
		}
	}

	fixSteps := &contract.QCFixSteps{
		Write:  rt.config.Workflow.QCFixSteps.Write,
		Causes: rt.config.Workflow.QCFixSteps.Causes,
	}
	if fixSteps.Write == "" {
		fixSteps.Write = sources.Draft
	}
	feedbackValue := (*contract.Feedback)(nil)
	if reworkRound > 0 {
		feedbackValue = feedback
	}
	input := contract.QcInput{
		Config:         rt.config.QC,
		Draft:          draft,
		Facts:          facts,
		Minutes:        minutes,
		Contradictions: e.contradictionsOf(rt),
		Rendered:       rendered,
		PreviousDraft:  previousDraft,
		Feedback:       feedbackValue,
		FixSteps:       fixSteps,
	}
	return e.qcEngine.Run(input), nil
}

func (e *Engine) finishQcPassed(rt *taskRuntime, checks []contract.QcCheckResult) error {
	if err := e.createDeliverables(rt, checks); err != nil {
		return err
	}
	// Options 写进事件后属于 A-01 对拍基准，这里保持原样；快照读取时按 availableActions 重算
	if err := e.transition(rt, contract.TriggerQcPassed, contract.RoleSystem, &contract.PauseReason{
		Code:    contract.PauseAwaitingReview,
		Message: "已通过质检，等待审核人决定。",
		Options: []string{contract.CmdApprove, contract.CmdReturn},
	}); err != nil {
		return err
	}
	// 进入待审核后，这一版在所属工作区目录的“交付”文件夹里留一份（T42）。写不进去不影响任务，只记一条说明
	e.fileDeliverables(rt)
	return nil
}

type qcRecord struct {
	TaskID      string                   `json:"taskId"`
	Employee    string                   `json:"employee"`
	ConfigHash  string                   `json:"configHash"`
	ReworkRound int                      `json:"reworkRound"`
	GeneratedAt string                   `json:"generatedAt"`
	Passed      bool                     `json:"passed"`
	Checks      []contract.QcCheckResult `json:"checks"`
}

// createDeliverables 把渲染步骤产出的文件登记为交付物，并写检验记录。
func (e *Engine) createDeliverables(rt *taskRuntime, checks []contract.QcCheckResult) error {
	dir := filepath.Join(rt.dir, "deliverables")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	kind := contract.DeliverableKind("")
	sourcePath := ""
	for _, step := range rt.config.Workflow.Steps {
		output, ok := e.optionalOutput(rt, step.ID).(map[string]any)
		if !ok {
			continue
		}
		if value, ok := output["docxPath"].(string); ok {
			kind = contract.DeliverableDocx
			sourcePath = value
			continue
		}
		if value, ok := output["mdPath"].(string); ok {
			kind = contract.DeliverableMD
			sourcePath = value
		}
	}
	if kind != "" {
		if _, err := os.Stat(sourcePath); err != nil {
			return fmt.Errorf("交付物文件不存在：%s", sourcePath)
		}
		content, err := os.ReadFile(sourcePath)
		if err != nil {
			return err
		}
		hash := sha256HexBytes(content)
		name := filepath.Base(sourcePath)
		target := filepath.Join(dir, name)
		if _, statErr := os.Stat(target); statErr == nil {
			existing, readErr := os.ReadFile(target)
			if readErr != nil {
				return readErr
			}
			if sha256HexBytes(existing) != hash {
				target = uniquePath(dir, name)
			}
		}
		if _, statErr := os.Stat(target); statErr != nil {
			if err := copyFile(sourcePath, target); err != nil {
				return err
			}
		}
		if err := e.append(rt, contract.TaskEvent{
			Type: contract.EventDeliverableCreated, Kind: string(kind), Path: target, SHA256: hash,
		}); err != nil {
			return err
		}
	}

	reworkRound := view(rt, func(p *projection) int { return p.ReworkRound })
	employee := view(rt, func(p *projection) string { return p.Employee })
	configHash := view(rt, func(p *projection) string { return p.ConfigHash })
	allPassed := true
	for _, check := range checks {
		if !check.Passed {
			allPassed = false
			break
		}
	}
	record := qcRecord{
		TaskID: rt.id, Employee: employee, ConfigHash: configHash, ReworkRound: reworkRound,
		GeneratedAt: e.nowISO(), Passed: allPassed, Checks: checks,
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	recordPath := filepath.Join(dir, fmt.Sprintf("qc_record-r%d.json", reworkRound))
	if err := os.WriteFile(recordPath, raw, 0o644); err != nil {
		return err
	}
	return e.append(rt, contract.TaskEvent{
		Type: contract.EventDeliverableCreated, Kind: string(contract.DeliverableQCRecord),
		Path: recordPath, SHA256: sha256HexBytes(raw),
	})
}

func corruptFirstParagraph(output any) any {
	raw, err := json.Marshal(output)
	if err != nil {
		return output
	}
	var clone map[string]any
	if err := json.Unmarshal(raw, &clone); err != nil || clone == nil {
		return output
	}
	if sections, ok := clone["sections"].([]any); ok && len(sections) > 0 {
		if section, ok := sections[0].(map[string]any); ok {
			if paragraphs, ok := section["paragraphs"].([]any); ok && len(paragraphs) > 0 {
				if text, ok := paragraphs[0].(string); ok {
					paragraphs[0] = text + " 环比 +12.3%"
					return clone
				}
			}
		}
	}
	if title, ok := clone["title"].(string); ok {
		clone["title"] = title + " 环比 +12.3%"
	}
	return clone
}

// ---------------------------------------------------------------- 任务目录与附件

func uniquePath(dir, name string) string {
	return uniquePathFrom(dir, name, 2)
}

// uniquePathFrom 同名文件已存在时，从 from 开始找 <名字>-<序号>.<扩展名>。
func uniquePathFrom(dir, name string, from int) string {
	candidate := filepath.Join(dir, name)
	for index := from; ; index++ {
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
		extension := filepath.Ext(name)
		base := strings.TrimSuffix(name, extension)
		if extension == "" || base == "" {
			candidate = filepath.Join(dir, fmt.Sprintf("%s-%d", name, index))
			continue
		}
		candidate = filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, index, extension))
	}
}

func copyFile(source, target string) error {
	sourceFile, err := os.Open(source)
	if err != nil {
		return err
	}
	defer sourceFile.Close()
	targetFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer targetFile.Close()
	if _, err := io.Copy(targetFile, sourceFile); err != nil {
		return err
	}
	return targetFile.Sync()
}

func copyDir(source, target string) error {
	files, err := config.ListFiles(source)
	if err != nil {
		return err
	}
	for _, rel := range files {
		from := filepath.Join(source, filepath.FromSlash(rel))
		to := filepath.Join(target, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := copyFile(from, to); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) copyAttachments(rt *taskRuntime, inputs []contract.AttachmentInput) ([]contract.AttachmentRef, error) {
	dir := filepath.Join(rt.dir, "attachments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	refs := make([]contract.AttachmentRef, 0, len(inputs))
	for _, input := range inputs {
		target := uniquePath(dir, filepath.Base(input.Name))
		if err := copyFile(input.SourcePath, target); err != nil {
			return nil, err
		}
		refs = append(refs, contract.AttachmentRef{Name: input.Name, Path: target})
	}
	return refs, nil
}

// ---------------------------------------------------------------- contract.Engine

// Create 建草稿任务：校验员工目录、复制配置与附件、写 task_created。
func (e *Engine) Create(input contract.CreateTaskInput) (*contract.TaskSnapshot, error) {
	if e.refusesCommands() {
		_, message := e.stopRefusal()
		return nil, errors.New(message + "，任务没有创建")
	}
	if input.Actor != contract.RoleInitiator && input.Actor != contract.RoleSystem {
		return nil, fmt.Errorf("角色 %s 不能创建任务", input.Actor)
	}
	// employee 来自不可信输入（HTTP 请求）：先校验再做任何文件操作
	if !config.IsEmployeeID(input.Employee) {
		return nil, fmt.Errorf("员工 id 不合法：%q（只允许小写字母、数字和连字符）", input.Employee)
	}
	source := filepath.Join(e.employeesDir, input.Employee)
	// 兜底：即使将来放宽 id 规则，解析后的路径也必须仍在员工配置目录里
	if filepath.Dir(source) != e.employeesDir {
		return nil, fmt.Errorf("员工 id 不合法：%q（超出员工配置目录）", input.Employee)
	}
	info, err := os.Stat(source)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("员工配置目录不存在：%s", source)
	}
	if _, err := os.Stat(filepath.Join(source, "workflow.yaml")); err != nil {
		return nil, fmt.Errorf("员工配置目录缺少 workflow.yaml：%s", source)
	}
	for _, attachment := range input.Attachments {
		if _, err := os.Stat(attachment.SourcePath); err != nil {
			return nil, fmt.Errorf("附件不存在：%s", attachment.SourcePath)
		}
	}

	stamp := e.now().UTC().Format("20060102150405")
	id := stamp + "-" + newUUID()[:6]
	dir := filepath.Join(e.tasksDir, id)
	for _, sub := range []string{"attachments", "artifacts", "deliverables"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	// 启动时把员工配置复制进任务目录，运行中的任务始终使用这一版
	if err := copyDir(source, filepath.Join(dir, "config")); err != nil {
		return nil, err
	}
	cfg, err := config.Load(filepath.Join(dir, "config"))
	if err != nil {
		return nil, err
	}

	rt := e.newRuntime(id, dir, cfg)
	// 发起事件记下任务属于哪个工作区
	rt.workspaceID = input.WorkspaceID
	e.mu.Lock()
	e.runtimes[id] = rt
	e.mu.Unlock()

	refs, err := e.copyAttachments(rt, input.Attachments)
	if err != nil {
		return nil, err
	}
	form := input.Form
	if form == nil {
		form = map[string]any{}
	}
	// 发起事件记下任务的会话模型：之后运行中换模型，从“模型已切换”事件接上。
	// 调用方没给时（命令行等）用引擎的缺省模型；两个都为空就不写（剧本模式）。
	model := input.Model
	if model.Connection == "" && model.Model == "" {
		model = e.defaultModel
	}
	rt.proj.Model = modelPointer(model)
	if err := e.append(rt, contract.TaskEvent{
		Type: contract.EventTaskCreated, Employee: input.Employee, ConfigHash: cfg.Hash,
		Form: form, Attachments: refs, WorkspaceID: input.WorkspaceID,
		Model: modelPointer(model),
	}); err != nil {
		return nil, err
	}
	return e.snapshotOf(rt), nil
}

// Command 执行一条业务命令。
func (e *Engine) Command(taskID string, command contract.TaskCommand, actor contract.Role) contract.CommandResult {
	rt := e.openRuntime(taskID)
	if rt == nil {
		return contract.CommandResult{OK: false, Error: contract.ErrNotFound, Message: fmt.Sprintf("任务 %s 不存在", taskID)}
	}
	trigger, known := commandTrigger[command.Type]
	if !known {
		return contract.CommandResult{OK: false, Error: contract.ErrInvalidInput, Message: fmt.Sprintf("未知命令：%s", command.Type)}
	}

	rt.mu.Lock()
	// 持有任务锁时检查，Stop 读到的任务状态因此不会再被这条命令改成活跃
	if e.refusesCommands() {
		code, message := e.stopRefusal()
		rt.mu.Unlock()
		return contract.CommandResult{OK: false, Error: code, Message: message}
	}
	from := rt.proj.State
	action, available := findAction(availableActions(from, rt.proj.Pause), command.Type)
	if !available {
		reason := illegalCommandReason(from, command.Type)
		if err := e.appendLocked(rt, contract.TaskEvent{
			Type: contract.EventTransitionRejected, From: from, Trigger: trigger, Actor: actor, Reason: reason,
		}); err != nil {
			rt.mu.Unlock()
			return contract.CommandResult{OK: false, Error: contract.ErrIllegalTransition, Message: reason}
		}
		rt.mu.Unlock()
		return contract.CommandResult{OK: false, Error: contract.ErrIllegalTransition, Message: reason}
	}
	if !actionAllows(action, actor) {
		reason := forbiddenRoleReason(from, action, actor)
		_ = e.appendLocked(rt, contract.TaskEvent{
			Type: contract.EventTransitionRejected, From: from, Trigger: trigger, Actor: actor, Reason: reason,
		})
		rt.mu.Unlock()
		return contract.CommandResult{OK: false, Error: contract.ErrForbiddenRole, Message: reason}
	}

	// 命令相关的业务写入
	switch command.Type {
	case contract.CmdSupplement:
		for _, attachment := range command.Attachments {
			if _, err := os.Stat(attachment.SourcePath); err != nil {
				rt.mu.Unlock()
				return contract.CommandResult{OK: false, Error: contract.ErrInvalidInput,
					Message: fmt.Sprintf("附件不存在：%s", attachment.SourcePath)}
			}
		}
		added, err := e.copyAttachments(rt, command.Attachments)
		if err != nil {
			rt.mu.Unlock()
			return contract.CommandResult{OK: false, Error: contract.ErrInvalidInput, Message: err.Error()}
		}
		merged := mergeForm(rt.proj.Form, command.Form)
		attachments := append(attachmentsToKeep(rt.proj), added...)
		if err := e.appendLocked(rt, contract.TaskEvent{
			Type: contract.EventFormUpdated, Form: merged, Attachments: attachments, Actor: actor,
		}); err != nil {
			rt.mu.Unlock()
			return contract.CommandResult{OK: false, Error: contract.ErrInvalidInput, Message: err.Error()}
		}
	case contract.CmdContinueWithoutMinutes:
		merged := mergeForm(rt.proj.Form, map[string]any{"allowMissingMinutes": true})
		attachments := append([]contract.AttachmentRef{}, rt.proj.Attachments...)
		if err := e.appendLocked(rt, contract.TaskEvent{
			Type: contract.EventFormUpdated, Form: merged, Attachments: attachments, Actor: actor,
		}); err != nil {
			rt.mu.Unlock()
			return contract.CommandResult{OK: false, Error: contract.ErrInvalidInput, Message: err.Error()}
		}
	case contract.CmdContinueWithPartial:
		merged := mergeForm(rt.proj.Form, map[string]any{"allowPartialPeriod": true})
		attachments := append([]contract.AttachmentRef{}, rt.proj.Attachments...)
		if err := e.appendLocked(rt, contract.TaskEvent{
			Type: contract.EventFormUpdated, Form: merged, Attachments: attachments, Actor: actor,
		}); err != nil {
			rt.mu.Unlock()
			return contract.CommandResult{OK: false, Error: contract.ErrInvalidInput, Message: err.Error()}
		}
	case contract.CmdApprove:
		if err := e.appendLocked(rt, contract.TaskEvent{
			Type: contract.EventReviewDecision, Decision: "approve",
		}); err != nil {
			rt.mu.Unlock()
			return contract.CommandResult{OK: false, Error: contract.ErrInvalidInput, Message: err.Error()}
		}
	case contract.CmdReturn:
		comment := strings.TrimSpace(command.Comment)
		if comment == "" {
			rt.mu.Unlock()
			return contract.CommandResult{OK: false, Error: contract.ErrInvalidInput, Message: "退回必须填写意见。"}
		}
		if err := e.appendLocked(rt, contract.TaskEvent{
			Type: contract.EventReviewDecision, Decision: "return", Comment: comment, Sections: command.Sections,
		}); err != nil {
			rt.mu.Unlock()
			return contract.CommandResult{OK: false, Error: contract.ErrInvalidInput, Message: err.Error()}
		}
	}
	if err := e.transitionLocked(rt, trigger, actor, nil); err != nil {
		rt.mu.Unlock()
		return contract.CommandResult{OK: false, Error: contract.ErrInvalidInput, Message: err.Error()}
	}
	if trigger == contract.TriggerTerminate {
		rt.stop()
	}
	snapshot := rt.proj.Snapshot(rt.id, e.now().UnixMilli())
	active := activeStates[rt.proj.State]
	rt.mu.Unlock()

	if active {
		e.kick(rt)
	}
	return contract.CommandResult{OK: true, Snapshot: &snapshot}
}

// attachmentsToKeep 补充材料时保留的附件。材料问题点名的文件要移除，否则解析步骤还会在同一个文件上失败。
func attachmentsToKeep(p *projection) []contract.AttachmentRef {
	bad := ""
	if p.Pause != nil && p.Pause.Code == contract.PauseMaterialIssue {
		bad, _ = p.Pause.Detail["path"].(string)
	}
	kept := make([]contract.AttachmentRef, 0, len(p.Attachments))
	for _, attachment := range p.Attachments {
		if bad == "" || attachment.Path != bad {
			kept = append(kept, attachment)
		}
	}
	return kept
}

func mergeForm(base map[string]any, patch map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range base {
		out[key] = value
	}
	for key, value := range patch {
		out[key] = value
	}
	return out
}

// Get 取任务快照。
func (e *Engine) Get(taskID string) (*contract.TaskSnapshot, bool) {
	rt := e.openRuntime(taskID)
	if rt == nil {
		return nil, false
	}
	return e.snapshotOf(rt), true
}

// List 任务列表（按 updatedAt 倒序）。
func (e *Engine) List() []contract.TaskSummary {
	summaries := []contract.TaskSummary{}
	entries, err := os.ReadDir(e.tasksDir)
	if err != nil {
		return summaries
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		if cached := e.cachedRuntime(id); cached != nil {
			summaries = append(summaries, e.summaryOf(cached))
			continue
		}
		rt, err := e.loadRuntime(id)
		if err != nil {
			if salvaged, ok := e.salvagedSummary(id, err); ok {
				summaries = append(summaries, salvaged)
			}
			continue
		}
		if rt == nil {
			continue
		}
		// 只保留活跃任务的完整 Runtime；空闲任务（草稿/暂停/终态）按需重读，
		// 否则列一次任务就会把所有任务的全部事件与配置永久缓存下来。
		if activeStates[view(rt, func(p *projection) contract.TaskState { return p.State })] {
			rt = e.cacheRuntime(rt)
		}
		summaries = append(summaries, e.summaryOf(rt))
	}
	sort.SliceStable(summaries, func(a, b int) bool { return summaries[a].UpdatedAt > summaries[b].UpdatedAt })
	return summaries
}

// Events 读取任务事件（afterSeq 之后）。
func (e *Engine) Events(taskID string, afterSeq int64) ([]contract.TaskEvent, error) {
	rt := e.openRuntime(taskID)
	if rt == nil {
		return []contract.TaskEvent{}, nil
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	out := make([]contract.TaskEvent, 0, len(rt.events))
	for _, event := range rt.events {
		if event.Seq > afterSeq {
			out = append(out, event)
		}
	}
	return out, nil
}

// listenerEntry 一个事件订阅者；取消时只把 active 置 false，不按下标删。
type listenerEntry struct {
	fn     func(contract.TaskEvent)
	active bool
}

// Subscribe 订阅任务事件；返回取消函数。
//
// 通知在引擎的运行时锁内同步发出：订阅者必须只做“入队/写响应”，
// 不能回调引擎（否则会与写事件的锁互相等待）。
func (e *Engine) Subscribe(taskID string, listener func(contract.TaskEvent)) func() {
	rt := e.openRuntime(taskID)
	if rt == nil {
		return func() {}
	}
	entry := &listenerEntry{fn: listener, active: true}
	rt.mu.Lock()
	rt.listeners = append(rt.listeners, entry)
	rt.mu.Unlock()
	cancelled := false
	return func() {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		if cancelled {
			return
		}
		cancelled = true
		entry.active = false
		for index, item := range rt.listeners {
			if item == entry {
				rt.listeners = append(rt.listeners[:index], rt.listeners[index+1:]...)
				break
			}
		}
	}
}

// WaitForSettled 等任务停在暂停态或终态（或执行失败）。
func (e *Engine) WaitForSettled(taskID string) (*contract.TaskSnapshot, error) {
	rt := e.openRuntime(taskID)
	if rt == nil {
		return nil, fmt.Errorf("任务 %s 不存在", taskID)
	}
	for {
		rt.mu.Lock()
		failure := rt.failure
		pending := rt.done
		rt.mu.Unlock()
		if failure != nil {
			return nil, failure
		}
		if pending == nil {
			break
		}
		<-pending
	}
	rt.mu.Lock()
	failure := rt.failure
	rt.mu.Unlock()
	if failure != nil {
		return nil, failure
	}
	return e.snapshotOf(rt), nil
}

// ResumeAll 进程启动时恢复所有活跃任务。
func (e *Engine) ResumeAll() error {
	entries, err := os.ReadDir(e.tasksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		cached := e.cachedRuntime(id)
		rt := cached
		if rt == nil {
			loaded, err := e.loadRuntime(id)
			if err != nil {
				// 单个任务日志损坏不能挡住服务启动；它会在 List 里以带 error 的摘要出现
				continue
			}
			rt = loaded
		}
		if rt == nil {
			continue
		}
		if !activeStates[view(rt, func(p *projection) contract.TaskState { return p.State })] {
			continue
		}
		// 只有需要恢复执行的活跃任务才留在内存里；暂停/终态任务读完就丢，和 List 一致
		rt = e.cacheRuntime(rt)
		e.kick(rt)
	}
	return nil
}

// ---------------------------------------------------------------- 运行时缓存

func (e *Engine) cachedRuntime(id string) *taskRuntime {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runtimes[id]
}

// cacheRuntime 缓存一个运行时并返回缓存里的唯一实例。
//
// HTTP 层是并发的：两个请求可能同时读盘得到两个不同实例，这里做了“谁先缓存谁生效”，
// 保证同一个任务永远只有一个 runtime（也就只有一个 drive goroutine 在推它）。
func (e *Engine) cacheRuntime(rt *taskRuntime) *taskRuntime {
	e.mu.Lock()
	defer e.mu.Unlock()
	if existing, ok := e.runtimes[rt.id]; ok {
		return existing
	}
	e.runtimes[rt.id] = rt
	return rt
}

// openRuntime 取任务的 Runtime：命中缓存则复用，否则读盘并缓存（并发安全，见 cacheRuntime）。
func (e *Engine) openRuntime(id string) *taskRuntime {
	if cached := e.cachedRuntime(id); cached != nil {
		return cached
	}
	rt, err := e.loadRuntime(id)
	if err != nil || rt == nil {
		return nil
	}
	return e.cacheRuntime(rt)
}

// loadRuntime 从磁盘读出一个任务的完整 Runtime，不写缓存（List 用）。
func (e *Engine) loadRuntime(id string) (*taskRuntime, error) {
	dir := filepath.Join(e.tasksDir, id)
	logPath := filepath.Join(dir, "events.jsonl")
	if _, err := os.Stat(logPath); err != nil {
		return nil, nil
	}
	events, err := ReadEventLog(logPath)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, nil
	}
	cfg, err := e.configOf(dir, events)
	if err != nil {
		return nil, err
	}
	rt := e.newRuntime(id, dir, cfg)
	for _, event := range events {
		rt.proj.Apply(event)
	}
	rt.events = events
	rt.workspaceID = rt.proj.WorkspaceID
	return rt, nil
}

// configOf 读任务目录里的员工配置副本；读不出来时按发起事件里的员工 id 找后备配置（见 Deps.ConfigFallback）。
func (e *Engine) configOf(dir string, events []contract.TaskEvent) (*contract.EmployeeConfig, error) {
	cfg, err := config.Load(filepath.Join(dir, "config"))
	if err == nil || e.configFallback == nil {
		return cfg, err
	}
	employee := ""
	for _, event := range events {
		if event.Type == contract.EventTaskCreated {
			employee = event.Employee
			break
		}
	}
	if employee == "" || !config.IsEmployeeID(employee) {
		return nil, err
	}
	fallbackDir, ok := e.configFallback(employee)
	if !ok {
		return nil, err
	}
	if cfg, fallbackErr := config.Load(fallbackDir); fallbackErr == nil {
		return cfg, nil
	}
	return nil, err
}

func (e *Engine) titleOf(rt *taskRuntime) string {
	base := rt.config.Workflow.Name
	form := view(rt, func(p *projection) map[string]any { return p.Form })
	year, week := 0.0, 0.0
	if period, ok := form["period"].(map[string]any); ok {
		if value, ok := asFloat(period["year"]); ok {
			year = value
		}
		if value, ok := asFloat(period["week"]); ok {
			week = value
		}
	}
	if year == 0 {
		if value, ok := asFloat(form["year"]); ok {
			year = value
		}
	}
	if week == 0 {
		if value, ok := asFloat(form["week"]); ok {
			week = value
		}
	}
	if year != 0 && week != 0 {
		return fmt.Sprintf("%s · %d 年第 %d 周", base, int(year), int(week))
	}
	return base
}

func (e *Engine) summaryOf(rt *taskRuntime) contract.TaskSummary {
	summary := view(rt, func(p *projection) contract.TaskSummary {
		return contract.TaskSummary{
			ID: rt.id, Employee: p.Employee, WorkspaceID: p.WorkspaceID, State: p.State, UpdatedAt: p.LastTS,
			StatusText: statusText(p.State, p.Pause),
			WaitingFor: waitingForRoles(p.State, availableActions(p.State, p.Pause)),
		}
	})
	summary.Title = e.titleOf(rt)
	return summary
}

// salvagedSummary 日志损坏的任务也要出现在列表里：按可解析的前缀给出最后已知状态，并带上错误说明。
func (e *Engine) salvagedSummary(id string, failure error) (contract.TaskSummary, bool) {
	dir := filepath.Join(e.tasksDir, id)
	events, corruption := ReadEventLogSalvage(filepath.Join(dir, "events.jsonl"))
	if len(events) == 0 {
		return contract.TaskSummary{}, false
	}
	cfg, err := e.configOf(dir, events)
	if err != nil {
		return contract.TaskSummary{}, false
	}
	rt := e.newRuntime(id, dir, cfg)
	for _, event := range events {
		rt.proj.Apply(event)
	}
	rt.events = events
	rt.workspaceID = rt.proj.WorkspaceID
	summary := e.summaryOf(rt)
	if corruption != "" {
		summary.Error = corruption
	} else {
		summary.Error = failure.Error()
	}
	return summary, true
}

// ---------------------------------------------------------------- 小工具

func asFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		return number, true
	}
	return 0, false
}

func jsonShape(value any) any {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func jsonText(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(raw)
}

func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func sha256HexBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func joinPrompt(skill, prompt string) string {
	parts := []string{}
	if skill != "" {
		parts = append(parts, skill)
	}
	if prompt != "" {
		parts = append(parts, prompt)
	}
	return strings.Join(parts, "\n\n")
}

// newUUID 生成 v4 UUID（与 TS 的 randomUUID 形状一致）。
func newUUID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buffer[0:4], buffer[4:6], buffer[6:8], buffer[8:10], buffer[10:16])
}
