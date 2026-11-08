// 事件投影：任务的状态、各步骤的检查点、预算，全部由重放事件得到（design.md §8）。
// 移植自 packages/core/src/engine/projection.ts。
package engine

import (
	"time"

	"digital-employee/internal/contract"
)

// Checkpoint 已完成步骤的检查点。Args 是计算步骤的入参（稳定序列化），用于判断重做时能否复用。
type Checkpoint struct {
	Path   string  `json:"path"`
	SHA256 string  `json:"sha256"`
	Args   *string `json:"args,omitempty"`
}

// ModelResponse 断点恢复时重放的一条模型响应。
type ModelResponse struct {
	Turn     int
	Response any
}

var activeStates = map[contract.TaskState]bool{
	contract.StateAdmission: true,
	contract.StateRunning:   true,
	contract.StateQC:        true,
}

var pausedStates = map[contract.TaskState]bool{
	contract.StateAwaitingInitiator: true,
	contract.StateAwaitingOperator:  true,
	contract.StateAwaitingReview:    true,
}

// projection 任务投影：所有字段都由事件重放得到。
type projection struct {
	config *contract.EmployeeConfig

	Employee    string
	// WorkspaceID 任务属于哪个工作区（取自发起事件）
	WorkspaceID string
	// Model 任务当前的会话模型（发起事件给出，之后由“模型已切换”更新，Spec 0006）；nil 表示没记录
	Model *contract.ModelRef
	ConfigHash  string
	Form        map[string]any
	Attachments []contract.AttachmentRef
	State       contract.TaskState
	Pause       *contract.PauseReason
	ReworkRound int
	QC          []contract.QcCheckResult
	QCAttempts  int
	LastSeq     int64
	CreatedTS   string
	LastTS      string

	// done 当前一轮中已完成的步骤
	done map[string]Checkpoint
	// prior 被回退的步骤的旧检查点，计算步骤入参不变时直接复用
	prior map[string]Checkpoint
	// skipped 可选步骤失败后被跳过
	skipped map[string]bool
	failed  map[string]bool
	Running string

	startCounts  map[string]int
	lastToolArgs map[string]string
	// modelResponses 步骤自上次结束（完成或失败）以来记录的模型响应，断点恢复时重放
	modelResponses map[string][]ModelResponse

	// 预算窗口：任务开始，以及每次操作员重试、审核人退回后重新计算
	ModelCalls    int
	activeAccumMS int64
	activeSince   *int64
	AutofixCount  int

	Feedback      *contract.Feedback
	BaselineDraft *Checkpoint

	deliverables   []contract.DeliverableRef
	deliverableIdx map[contract.DeliverableKind]int
	versions       []versionRecord
	FaultsInjected map[string]bool
}

func newProjection(config *contract.EmployeeConfig) *projection {
	return &projection{
		config:         config,
		Form:           map[string]any{},
		Attachments:    []contract.AttachmentRef{},
		State:          contract.StateDraft,
		done:           map[string]Checkpoint{},
		prior:          map[string]Checkpoint{},
		skipped:        map[string]bool{},
		failed:         map[string]bool{},
		startCounts:    map[string]int{},
		lastToolArgs:   map[string]string{},
		modelResponses: map[string][]ModelResponse{},
		deliverables:   []contract.DeliverableRef{},
		deliverableIdx: map[contract.DeliverableKind]int{},
		FaultsInjected: map[string]bool{},
	}
}

func (p *projection) steps() []contract.StepConfig { return p.config.Workflow.Steps }

func (p *projection) stepIndex(id string) int {
	for index, step := range p.steps() {
		if step.ID == id {
			return index
		}
	}
	return -1
}

// FirstReworkIndex 退回后从哪个步骤重做：第一个标记了 rework 的判断步骤。
func (p *projection) FirstReworkIndex() int {
	for index, step := range p.steps() {
		if step.Kind == contract.StepKindJudge && step.Rework {
			return index
		}
	}
	for index, step := range p.steps() {
		if step.Kind == contract.StepKindJudge {
			return index
		}
	}
	return 0
}

// FixIndex 质检不通过后从哪个步骤修正：不通过的检查给出的 fixStep 中最靠前的一个。
func (p *projection) FixIndex() int {
	indexes := []int{}
	for _, check := range p.QC {
		if check.Passed || check.FixStep == "" {
			continue
		}
		if index := p.stepIndex(check.FixStep); index >= 0 {
			indexes = append(indexes, index)
		}
	}
	if len(indexes) == 0 {
		return p.FirstReworkIndex()
	}
	lowest := indexes[0]
	for _, index := range indexes[1:] {
		if index < lowest {
			lowest = index
		}
	}
	return lowest
}

func (p *projection) rewind(fromIndex int) {
	for index, step := range p.steps() {
		if index < fromIndex {
			continue
		}
		if checkpoint, ok := p.done[step.ID]; ok {
			p.prior[step.ID] = checkpoint
		}
		delete(p.done, step.ID)
		delete(p.skipped, step.ID)
	}
}

// restoreRewound 把被回退、这一轮没有重新完成的步骤恢复成旧检查点。
func (p *projection) restoreRewound() {
	for stepID, checkpoint := range p.prior {
		if _, done := p.done[stepID]; !done {
			p.done[stepID] = checkpoint
		}
	}
}

// ElapsedMS 任务活跃时长（暂停期间不计）。
func (p *projection) ElapsedMS(nowMS int64) int64 {
	elapsed := p.activeAccumMS
	if p.activeSince != nil && nowMS > *p.activeSince {
		elapsed += nowMS - *p.activeSince
	}
	return elapsed
}

// Deliverables 交付物（按登记顺序）。
func (p *projection) Deliverables() []contract.DeliverableRef {
	return append([]contract.DeliverableRef{}, p.deliverables...)
}

// Apply 应用一条事件。
func (p *projection) Apply(event contract.TaskEvent) {
	p.LastSeq = event.Seq
	p.LastTS = event.TS
	switch event.Type {
	case contract.EventTaskCreated:
		p.Employee = event.Employee
		p.WorkspaceID = event.WorkspaceID
		if event.Model != nil {
			p.Model = event.Model
		}
		p.ConfigHash = event.ConfigHash
		p.Form = event.Form
		p.Attachments = event.Attachments
		p.CreatedTS = event.TS
	case contract.EventModelSwitched:
		if event.Model != nil {
			p.Model = event.Model
		}
	case contract.EventFormUpdated:
		p.Form = event.Form
		p.Attachments = event.Attachments
	case contract.EventStepStarted:
		p.Running = event.StepID
		delete(p.failed, event.StepID)
		p.startCounts[event.StepID] = p.startCounts[event.StepID] + 1
	case contract.EventStepCompleted:
		args := (*string)(nil)
		if p.Running == event.StepID {
			if value, ok := p.lastToolArgs[event.StepID]; ok {
				args = &value
			}
		} else if prior, ok := p.prior[event.StepID]; ok {
			args = prior.Args
		}
		p.done[event.StepID] = Checkpoint{Path: event.OutputPath, SHA256: event.OutputSHA256, Args: args}
		p.finishStep(event.StepID)
	case contract.EventStepFailed:
		optional := false
		if index := p.stepIndex(event.StepID); index >= 0 {
			optional = p.steps()[index].Optional
		}
		if optional {
			p.skipped[event.StepID] = true
		} else {
			p.failed[event.StepID] = true
		}
		p.finishStep(event.StepID)
	case contract.EventToolCalled:
		if event.StepID != "" {
			p.lastToolArgs[event.StepID] = argsKey(event.Args)
		}
	case contract.EventModelCalled:
		p.ModelCalls++
		turn := 0
		if event.Turn != nil {
			turn = *event.Turn
		}
		p.modelResponses[event.StepID] = append(p.modelResponses[event.StepID], ModelResponse{Turn: turn, Response: event.Response})
	case contract.EventQCResult:
		p.QC = event.Checks
		p.QCAttempts++
	case contract.EventReviewDecision:
		if event.Decision == "return" {
			sections := event.Sections
			p.Feedback = &contract.Feedback{Comment: event.Comment, Sections: sections}
		}
	case contract.EventDeliverableCreated:
		ref := contract.DeliverableRef{Kind: contract.DeliverableKind(event.Kind), Path: event.Path, SHA256: event.SHA256}
		if index, ok := p.deliverableIdx[ref.Kind]; ok {
			p.deliverables[index] = ref
		} else {
			p.deliverableIdx[ref.Kind] = len(p.deliverables)
			p.deliverables = append(p.deliverables, ref)
		}
		p.recordVersion(ref, event.TS)
	case contract.EventFaultInjected:
		p.FaultsInjected[event.Target+":"+event.Mode] = true
	case contract.EventStateChanged:
		p.applyStateChange(event)
	}
}

func (p *projection) finishStep(stepID string) {
	if p.Running == stepID {
		p.Running = ""
	}
	delete(p.modelResponses, stepID)
}

func (p *projection) applyStateChange(event contract.TaskEvent) {
	ts := parseEventTime(event.TS)
	previousPause := p.Pause

	switch event.Trigger {
	case contract.TriggerSupplement, contract.TriggerContinueWithoutMinutes, contract.TriggerContinueWithPartial:
		p.rewind(0)
	case contract.TriggerQcAutofix:
		p.AutofixCount++
		p.rewind(p.FixIndex())
	case contract.TriggerRetry:
		if previousPause != nil && previousPause.Code == contract.PauseQcExhausted {
			p.rewind(p.FixIndex())
		}
		p.resetWindow()
	case contract.TriggerReturn:
		// 保留重做前的草稿版本，供“意见响应”质检对比
		if checkpoint, ok := p.done[p.config.Workflow.QCSources.Draft]; ok {
			copied := checkpoint
			p.BaselineDraft = &copied
		}
		p.rewind(p.FirstReworkIndex())
		p.ReworkRound++
		p.resetWindow()
	case contract.TriggerNeedsHuman:
		// 回到待审核时原交付物不变，步骤也恢复到送审时；旧日志里转待人工处理的任务靠重试重跑，不恢复
		if event.To == contract.StateAwaitingReview {
			p.restoreRewound()
		}
	}

	if p.activeSince != nil {
		if ts > *p.activeSince {
			p.activeAccumMS += ts - *p.activeSince
		}
		p.activeSince = nil
	}
	if activeStates[event.To] {
		value := ts
		p.activeSince = &value
	}

	p.State = event.To
	if pausedStates[event.To] {
		p.Pause = event.Pause
	} else {
		p.Pause = nil
	}
	p.Running = ""
}

func (p *projection) resetWindow() {
	p.ModelCalls = 0
	p.activeAccumMS = 0
	p.activeSince = nil
	p.AutofixCount = 0
}

// skippedByWhen when: rework 的判断步骤在首轮（未被退回过）不执行，记为 skipped。由状态推导，不需要事件。
func (p *projection) skippedByWhen(step contract.StepConfig) bool {
	return step.Kind == contract.StepKindJudge && step.When == "rework" && p.ReworkRound == 0
}

// NextStepIndex 第一个尚未完成的步骤（不含质检步骤）；全部完成返回 -1。
func (p *projection) NextStepIndex(excludeID string) int {
	for index, step := range p.steps() {
		if step.Kind == contract.StepKindQC {
			continue
		}
		if excludeID != "" && step.ID == excludeID {
			continue
		}
		if _, done := p.done[step.ID]; done {
			continue
		}
		if p.skipped[step.ID] || p.skippedByWhen(step) {
			continue
		}
		return index
	}
	return -1
}

// StepStatuses 各步骤状态（快照用）。
func (p *projection) StepStatuses() []contract.StepStatus {
	out := make([]contract.StepStatus, 0, len(p.steps()))
	for _, step := range p.steps() {
		status := contract.StepPending
		switch {
		case p.skipped[step.ID] || p.skippedByWhen(step):
			status = contract.StepSkipped
		case func() bool { _, ok := p.done[step.ID]; return ok }():
			status = contract.StepDone
		case p.Running == step.ID && activeStates[p.State]:
			status = contract.StepRunning
		case p.failed[step.ID]:
			status = contract.StepFailed
		}
		out = append(out, contract.StepStatus{ID: step.ID, Label: step.Label, Kind: step.Kind, Status: status, Activity: step.Activity})
	}
	return out
}

// Snapshot 任务快照：`GET /api/tasks/:id` 的响应体。
func (p *projection) Snapshot(taskID string, nowMS int64) contract.TaskSnapshot {
	limits := p.config.Workflow.Limits.Task
	steps := p.StepStatuses()
	deliverables := p.Deliverables()
	attachments := p.Attachments
	if attachments == nil {
		attachments = []contract.AttachmentRef{}
	}
	form := p.Form
	if form == nil {
		form = map[string]any{}
	}
	actions := availableActions(p.State, p.Pause)
	p.loadRevisionNotes()
	return contract.TaskSnapshot{
		ID:          taskID,
		Employee:    p.Employee,
		ConfigHash:  p.ConfigHash,
		State:       p.State,
		Pause:       presentPause(p.State, p.Pause, actions),
		Form:        form,
		Attachments: attachments,
		Steps:       steps,
		Budget: contract.Budget{
			ModelCalls: contract.BudgetItem{Used: int64(p.ModelCalls), Limit: int64(limits.ModelCalls)},
			ElapsedMs:  contract.BudgetItem{Used: p.ElapsedMS(nowMS), Limit: int64(limits.DurationSec) * 1000},
		},
		ReworkRound:  p.ReworkRound,
		QC:           p.QC,
		Deliverables: deliverables,
		LastSeq:      p.LastSeq,
		Model:        p.Model,
		Actions:      actions,
		WaitingFor:   waitingForRoles(p.State, actions),
		Versions:     p.Versions(),
	}
}

// parseEventTime 把事件里的 ISO 时间解析成毫秒时间戳。
func parseEventTime(text string) int64 {
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return time.Now().UnixMilli()
	}
	return parsed.UnixMilli()
}
