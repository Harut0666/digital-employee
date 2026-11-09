// 员工当工具（Spec 0007、T49）：run_employee 与“员工表单”卡片。
//
// 前台 Agent 把对话、目录文件和已有材料整理成一个员工的输入（表单字段 + 材料）调用 run_employee。
// 输入定义直接用员工配置里的表单定义和开工清单，不另设一份：
//   - 字段值不合法（类型、范围、不存在的字段、文件字段）：把具体问题作为工具错误交回 Agent，不开表单；
//   - 通过权限判断后，弹出员工表单卡，由人核对、补充、确认（“确认开工”或“不是这个员工”）。
//     workspace 一定弹；full 在表单完整有效、必需开工清单项备齐时免确认直接开工，否则弹。
//     workspace 下目录文件用作材料要人确认；full 自动确认；
//   - 确认开工由人触发（AnswerEmployeeForm），程序按现有路径做准入校验并创建任务；目录文件在确认时复制进会话材料，
//     之后与上传的材料等效，改原文件不影响任务。run_employee 的结果是任务 ID 和当前状态，不等任务跑完。
//
// 模式只管 Agent 的动作，不管流程里人的决定：开工清单必需项没备齐，任何模式都不能开工；
// 可跳过的项只有人在表单卡上确认才算跳过，并在会话里留痕（规则同 Spec 0005）。
package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

// ToolRunEmployee 前台 Agent 运行员工的工具名。
const ToolRunEmployee = "run_employee"

// 人对员工表单卡的答复（请求体里的 decision）。
const (
	AnswerConfirm = "confirm" // 确认开工
	AnswerReject  = "reject"  // 不是这个员工
)

// 员工表单卡答复之后卡片上的 Decision（停止时是 DecisionCancelled）。
const (
	DecisionFormConfirmed = "confirmed"
	DecisionFormRejected  = "rejected"
)

// ErrFormSettled 这张员工表单已经答复过（或已取消）。
const ErrFormSettled = "form_settled"

// kindEmployeeFormDecided 员工表单卡有了答复（RequestID、Decision、TaskID）；只改变会话状态，卡片上体现。
const kindEmployeeFormDecided = "employee_form_decided"

// rejectedReason “不是这个员工”交回 Agent 的话。
const rejectedReason = "发起人拒绝，原因：选错员工。发起人认为这项工作不该由这个员工做，请重新判断该由哪个员工做，或问问发起人想让谁做。"

// Sample 应用目录里的公开样例：员工、预填的表单字段、样例材料文件。
type Sample struct {
	ID       string
	Title    string
	Employee string
	Form     map[string]any
	Files    []SampleFile
}

// SampleFile 一份样例材料；Path 是应用目录里的真实路径，不返回给浏览器。
type SampleFile struct {
	Name string
	Path string
	Size int64
}

// formResult 人答复之后交给等在 run_employee 里的那一轮的结果。
type formResult struct {
	rejected bool
	taskID   string
	state    contract.TaskState
}

// formWait 一张员工表单卡。mu 让同一张卡上的答复依次处理；settled 表示已经有了结果。
// result 为 nil 表示没有一轮在等它（载入公开样例弹出的表单）。
type formWait struct {
	id      string
	session string
	mu      sync.Mutex
	settled bool
	result  chan formResult
}

func (m *Manager) registerForm(wait *formWait) {
	m.formMu.Lock()
	defer m.formMu.Unlock()
	m.forms[wait.id] = wait
}

func (m *Manager) dropForm(id string) {
	m.formMu.Lock()
	defer m.formMu.Unlock()
	delete(m.forms, id)
}

// formWaiting 这张表单现在有没有在等人答复。
func (m *Manager) formWaiting(requestID, sessionID string) bool {
	m.formMu.Lock()
	defer m.formMu.Unlock()
	wait := m.forms[requestID]
	return wait != nil && wait.session == sessionID
}

// pendingForm 会话里有没有员工表单在等人答复（状态点显示“等你处理”）。
func (m *Manager) pendingForm(id string) bool {
	m.formMu.Lock()
	defer m.formMu.Unlock()
	for _, wait := range m.forms {
		if wait.session == id {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- 输入校验

// agentSchema 员工表单 Schema 里 Agent 能填的部分：去掉只能由人决定的字段（隐藏字段、文件字段）。
func agentSchema(config *contract.EmployeeConfig) map[string]any {
	out := map[string]any{}
	for key, value := range config.FormSchema {
		out[key] = value
	}
	properties, _ := config.FormSchema["properties"].(map[string]any)
	kept := map[string]any{}
	for name, schema := range properties {
		if !humanOnlyField(config.FormUI, name) {
			kept[name] = schema
		}
	}
	out["properties"] = kept
	if list, ok := config.FormSchema["required"].([]any); ok {
		required := []any{}
		for _, raw := range list {
			if name, _ := raw.(string); name != "" && kept[name] != nil {
				required = append(required, raw)
			}
		}
		out["required"] = required
	}
	return out
}

// withoutRequired 去掉 Schema 里所有层级的 required：值的类型和范围对不对，与有没有填全是两件事。
func withoutRequired(node any) any {
	switch typed := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			if key == "required" {
				if _, isList := value.([]any); isList {
					continue
				}
			}
			out[key] = withoutRequired(value)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, value := range typed {
			out[index] = withoutRequired(value)
		}
		return out
	}
	return node
}

// fieldProblems Agent 给的字段里不合法的地方（字段名、是不是人才能填的、类型和范围），每个问题一句话。
// 没填全不算不合法：缺的由表单卡让人补。
func fieldProblems(config *contract.EmployeeConfig, fields map[string]any) []string {
	properties, _ := config.FormSchema["properties"].(map[string]any)
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	problems := []string{}
	for _, name := range names {
		raw, known := properties[name]
		switch {
		case !known:
			problems = append(problems, fmt.Sprintf("表单里没有字段 %s", name))
		case humanOnlyField(config.FormUI, name) && (fieldWidget(config.FormUI, name) == "file" || fieldWidget(config.FormUI, name) == "files"):
			problems = append(problems, fmt.Sprintf("字段 %s 是文件字段，文件请放进 materials，不要写进 fields", name))
		case humanOnlyField(config.FormUI, name):
			problems = append(problems, fmt.Sprintf("字段 %s 只能由人在表单卡上决定，不能写进 fields", name))
		default:
			schema, _ := withoutRequired(raw).(map[string]any)
			if errs := agent.NewValidator(schema).Validate(fields[name]); len(errs) > 0 {
				problems = append(problems, fmt.Sprintf("字段 %s 不合法：%s", name, agent.ErrorText(errs)))
			}
		}
	}
	return problems
}

// completenessProblems 表单有没有填全、填对（含必填项）；只看 Agent 能填的字段。
func completenessProblems(config *contract.EmployeeConfig, form map[string]any) []string {
	if form == nil {
		form = map[string]any{}
	}
	return agent.NewValidator(agentSchema(config)).Validate(form)
}

// readyForStart 表单完整有效，并且必需的开工清单项都备齐（names 是材料文件名）。
func readyForStart(config *contract.EmployeeConfig, form map[string]any, names []string) bool {
	if len(completenessProblems(config, form)) > 0 {
		return false
	}
	if len(config.Checklist) == 0 {
		return true
	}
	attachments := make([]Attachment, 0, len(names))
	for _, name := range names {
		attachments = append(attachments, Attachment{Name: name})
	}
	return evaluateChecklist(config.Checklist, config.FormSchema, form, attachments, nil).Ready
}

// ---------------------------------------------------------------- run_employee

func employeeNameOf(employees []Employee, id string) string {
	for _, employee := range employees {
		if employee.ID == id {
			return employee.Name
		}
	}
	return ""
}

func prefilledNames(fields map[string]any) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// resolveMaterials 把 Agent 给的材料解析成表单上的材料：先认会话里已有的材料（按文件名），再认工作区目录里的文件（按相对路径）。
// 同名的两份材料会互相覆盖，不允许。
func (m *Manager) resolveMaterials(current *record, raw any) ([]FormMaterial, error) {
	list, _ := raw.([]any)
	if raw != nil && list == nil {
		return nil, toolFailure("invalid_materials", "materials 必须是文件名或相对路径的数组")
	}
	root := m.workspaceDir(current.workspaceID)
	out := make([]FormMaterial, 0, len(list))
	seen := map[string]string{}
	for _, item := range list {
		text, _ := item.(string)
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, toolFailure("invalid_materials", "materials 里有一项不是文件名或路径")
		}
		var material FormMaterial
		if attachment, found := attachmentNamed(current, text); found {
			material = FormMaterial{Name: attachment.Name, Size: attachment.Size, Source: MaterialAttachment}
		} else if root == "" {
			return nil, toolFailure("material_not_found", "找不到材料「%s」：会话里没有这份材料，也没有可查的工作区目录。", text)
		} else if target, rel, err := folderPathProblem(root, text); err != nil {
			return nil, toolFailure("material_not_found", "找不到材料「%s」：既不是会话里已有的材料，也不是工作区目录里可用的文件（%s）。", text, err.Error())
		} else {
			size := int64(0)
			if info, statErr := os.Stat(target); statErr == nil {
				size = info.Size()
			}
			material = FormMaterial{Name: filepath.Base(target), Size: size, Source: MaterialFolder, Path: rel}
		}
		if previous, clash := seen[material.Name]; clash {
			if previous == text {
				continue
			}
			return nil, toolFailure("material_clash", "材料「%s」和「%s」同名，会互相覆盖。请只选其中一份。", previous, text)
		}
		seen[material.Name] = text
		out = append(out, material)
	}
	return out, nil
}

func attachmentNamed(current *record, name string) (Attachment, bool) {
	for _, attachment := range current.attachments {
		if attachment.Name == name {
			return attachment, true
		}
	}
	return Attachment{}, false
}

// runEmployeeTool 前台 Agent 的 run_employee。动作类别是“运行员工”：仅可查看模式拒绝；
// “问”和“放行”由工具自己处理（见包说明），关口不叠权限确认卡。
func (loop *turnLoop) runEmployeeTool(employees []Employee, configs map[string]*contract.EmployeeConfig) tool {
	ids := make([]any, 0, len(employees))
	for _, employee := range employees {
		ids = append(ids, employee.ID)
	}
	return tool{
		spec: agent.ToolSpec{
			Name: ToolRunEmployee,
			Description: "让一个员工做这项工作。把员工需要的表单字段（fields）和材料（materials）整理好交给程序，程序会弹出员工表单卡让发起人核对、补充并确认，确认后员工才开工；" +
				"结果是任务 ID 和当前状态，不等任务跑完。字段值不合法、材料找不到会直接报错，改正后再调。会话里已有未结束的任务时会被拒绝。",
			Parameters: map[string]any{
				"type":     "object",
				"required": []any{"employee"},
				"properties": map[string]any{
					"employee":  map[string]any{"type": "string", "enum": ids, "description": "员工 id"},
					"fields":    map[string]any{"type": "object", "description": "这个员工的表单字段（键是字段名，见上面各员工的字段说明）；只填你有把握的，缺的由发起人在表单上补"},
					"materials": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "材料：会话里已有的材料写文件名，工作区目录里的文件写相对路径"},
					"reason":    map[string]any{"type": "string", "description": "一句话说明为什么由这个员工做、字段和材料依据什么"},
				},
			},
		},
		action:     ActionRunEmployee,
		selfDecide: true,
		describe: func(arguments map[string]any) (string, string) {
			id, _ := arguments["employee"].(string)
			if name := employeeNameOf(employees, id); name != "" {
				return name + "（" + id + "）", "运行员工"
			}
			return id, "运行员工"
		},
		runCtx: func(ctx context.Context, arguments map[string]any) (any, error) {
			return loop.runEmployee(ctx, employees, configs, arguments)
		},
	}
}

func (loop *turnLoop) runEmployee(ctx context.Context, employees []Employee, configs map[string]*contract.EmployeeConfig, arguments map[string]any) (any, error) {
	m := loop.job.manager
	sessionID := loop.job.session
	id, _ := arguments["employee"].(string)
	config := configs[id]
	name := employeeNameOf(employees, id)
	if config == nil || name == "" {
		ids := make([]string, 0, len(employees))
		for _, employee := range employees {
			ids = append(ids, employee.ID)
		}
		return nil, toolFailure("unknown_employee", "没有这个员工：%s。可用的员工 id：%s。", id, strings.Join(ids, "、"))
	}
	// 每次读最新的记录：人可能在这一轮进行中上传了材料、换了模式、任务刚结束
	current, err := m.load(sessionID)
	if err != nil {
		return nil, err
	}
	// 说话的不是发起人时，权限关口（turnLoop.gate）已经拒绝了，到不了这里
	if err := m.refuseNewTask(current); err != nil {
		return nil, toolFailure(ErrSessionHasTask, "%s", err.Error())
	}
	fields := map[string]any{}
	if raw, present := arguments["fields"]; present && raw != nil {
		typed, ok := raw.(map[string]any)
		if !ok {
			return nil, toolFailure("invalid_form", "fields 必须是对象（键是表单字段名）")
		}
		fields = typed
	}
	if problems := fieldProblems(config, fields); len(problems) > 0 {
		return nil, toolFailure("invalid_form", "表单字段有问题，没有弹出表单：%s。", strings.Join(problems, "；"))
	}
	materials, err := m.resolveMaterials(current, arguments["materials"])
	if err != nil {
		return nil, err
	}
	reason, _ := arguments["reason"].(string)

	// 材料名：只算 Agent 给的（会话里已有的材料也要点名，上一次任务的材料不会混进来）
	names := []string{}
	hasFolder := false
	for _, material := range materials {
		names = append(names, material.Name)
		hasFolder = hasFolder || material.Source == MaterialFolder
	}
	mode := current.mode()
	ready := readyForStart(config, fields, names)
	// 免确认直接开工：运行员工在这个模式下免问（full），表单完整有效且必需清单项备齐；
	// 带目录文件时还要“使用目录文件”在这个模式下也免问（full 才免），否则弹表单让人确认
	if ready && Decide(mode, ActionRunEmployee) == VerdictAllow && (!hasFolder || Decide(mode, ActionUseFolderFile) == VerdictAllow) {
		return loop.startDirect(ctx, id, name, config, fields, materials, mode, arguments)
	}
	return loop.askForm(ctx, id, name, config, fields, materials, reason)
}

// startDirect 免确认直接开工：创建任务，留痕。
func (loop *turnLoop) startDirect(ctx context.Context, id, name string, config *contract.EmployeeConfig, fields map[string]any, materials []FormMaterial, mode string, arguments map[string]any) (any, error) {
	if ctx.Err() != nil {
		return nil, errTurnCancelled
	}
	m := loop.job.manager
	t, done, err := m.beginUnlocked(loop.job.session, contract.RoleInitiator)
	if err != nil {
		return nil, err
	}
	defer done()
	attachments := []string{}
	for _, material := range materials {
		if material.Source == MaterialAttachment {
			attachments = append(attachments, material.Name)
		}
	}
	snapshot, err := m.startEmployeeTask(t, contract.RoleInitiator, startRequest{
		employee: id, config: config, form: fields, attachments: attachments, materials: materials,
	})
	if err != nil {
		return nil, toolErrorOf(err)
	}
	if err := loop.write(Message{Kind: KindPermissionAllowed, Tool: ToolRunEmployee, Args: arguments,
		Reason: fmt.Sprintf("%s模式：%s的表单完整有效，必需的开工清单项已备齐，不用确认直接开工", PermissionModeLabels[mode], name)}); err != nil {
		return nil, err
	}
	// 目录文件也是自动确认的（只有完全权限模式走到这里）：每一份都留痕，事后能核对用了哪些文件
	for _, material := range materials {
		if material.Source != MaterialFolder {
			continue
		}
		if err := loop.write(Message{Kind: KindPermissionAllowed, Tool: "use_folder_file", Args: map[string]any{"path": material.Path},
			Reason: fmt.Sprintf("%s模式：自动确认使用目录文件 %s（随 %s 的表单一起复制进任务）", PermissionModeLabels[mode], material.Path, name)}); err != nil {
			return nil, err
		}
	}
	return startedResult(snapshot.ID, snapshot.State), nil
}

// toolErrorOf 会话业务错误 → 交给 Agent 的工具错误（带错误码和说明）。
func toolErrorOf(err error) error {
	var typed *Error
	if errors.As(err, &typed) {
		return &toolError{code: typed.Code, message: typed.Message}
	}
	return err
}

// startedResult run_employee 开工成功时交给 Agent 的结果：任务 ID 和当前状态，不等任务跑完。
func startedResult(taskID contract.TaskID, state contract.TaskState) map[string]any {
	return map[string]any{
		"started": true, "taskId": taskID, "state": state, "stateLabel": contract.StateLabels[state],
	}
}

// askForm 弹出员工表单卡，这一轮在这里暂停，直到人确认、说“不是这个员工”或点停止。
func (loop *turnLoop) askForm(ctx context.Context, id, name string, config *contract.EmployeeConfig, fields map[string]any, materials []FormMaterial, reason string) (any, error) {
	if ctx.Err() != nil {
		return nil, errTurnCancelled
	}
	m := loop.job.manager
	wait := &formWait{id: newRequestID(), session: loop.job.session, result: make(chan formResult, 1)}
	// 先登记再发卡：人看到卡片时它一定已经能答复
	m.registerForm(wait)
	baseline := []string{}
	if fresh, err := m.load(loop.job.session); err == nil {
		baseline = attachmentNames(fresh.attachments)
	}
	card := Card{
		Type: CardEmployeeForm, RequestID: wait.id, Employee: id, EmployeeName: name, Draft: fields,
		Prefilled: prefilledNames(fields), Materials: materials, Reason: reason, Pending: true, Checklist: config.Checklist, Baseline: baseline,
	}
	if err := loop.write(Message{Kind: KindCard, Card: &card}); err != nil {
		m.dropForm(wait.id)
		return nil, err
	}
	written := loop.turn.out[len(loop.turn.out)-1]

	var res formResult
	select {
	case res = <-wait.result:
	case <-ctx.Done():
		wait.mu.Lock()
		if !wait.settled {
			wait.settled = true
			wait.mu.Unlock()
			m.dropForm(wait.id)
			if err := loop.write(Message{Kind: kindEmployeeFormDecided, RequestID: wait.id, Decision: DecisionCancelled}); err != nil {
				return nil, err
			}
			return nil, errTurnCancelled
		}
		wait.mu.Unlock()
		// 人的答复恰好同时到了：它已经写进日志，结果照实交回（这一轮随后被停止）
		res = <-wait.result
	}
	// 答复是别的协程写进日志的：重读，这一轮后面的判定才看得到最新状态
	if current, err := m.load(loop.job.session); err == nil {
		loop.turn.current = current
	}
	answered := written
	copied := *written.Card
	copied.Pending, copied.Decision = false, DecisionFormConfirmed
	if res.rejected {
		copied.Decision = DecisionFormRejected
	} else {
		copied.TaskID = res.taskID
	}
	answered.Card = &copied
	loop.bus.publish(AgentEvent{Turn: loop.job.id, Type: EventMessage, Msg: &answered})
	if res.rejected {
		return nil, deniedByInitiator("%s", rejectedReason)
	}
	return startedResult(res.taskID, res.state), nil
}

// ---------------------------------------------------------------- 开工

// startRequest 开一个任务需要的东西。
type startRequest struct {
	employee string
	config   *contract.EmployeeConfig
	form     map[string]any
	// attachments 要带进任务的会话材料（文件名）
	attachments []string
	// materials 要先复制进会话的材料：工作区目录里的文件、公开样例文件
	materials []FormMaterial
	// skipped 人确认跳过的可跳过清单项
	skipped []string
}

// startEmployeeTask 员工表单卡确认开工（和 run_employee 免确认直接开工）的共同路径：
// 按开工清单和表单判定 → 提交前检查 → 目录文件、样例文件复制进会话 → 跳过留痕 → 创建任务并提交。
// 这是会话里发任务命令的地方（还有旧会话兼容的 Submit）；Agent 的工具拿不到任务命令。
func (m *Manager) startEmployeeTask(t *turn, role contract.Role, req startRequest) (*contract.TaskSnapshot, error) {
	if err := m.refuseNewTask(t.current); err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, attachment := range t.current.attachments {
		have[attachment.Name] = true
	}
	names := []string{}
	seen := map[string]bool{}
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	for _, name := range req.attachments {
		if !have[name] {
			return nil, failure(contract.ErrInvalidInput, "会话材料里没有这份文件：%s", name)
		}
		add(name)
	}
	var folderPaths []string
	var sampleNames []string
	for _, material := range req.materials {
		switch material.Source {
		case MaterialFolder:
			folderPaths = append(folderPaths, material.Path)
			add(material.Name)
		case MaterialSample:
			sampleNames = append(sampleNames, material.Name)
			add(material.Name)
		case MaterialAttachment:
			if !have[material.Name] {
				return nil, failure(contract.ErrInvalidInput, "会话材料里没有这份文件：%s", material.Name)
			}
			add(material.Name)
		}
	}

	items := req.config.Checklist
	skipped := map[string]bool{}
	for _, itemID := range req.skipped {
		var item *contract.ChecklistItem
		for index := range items {
			if items[index].ID == itemID {
				item = &items[index]
			}
		}
		switch {
		case item == nil:
			return nil, failure(contract.ErrInvalidInput, "开工清单里没有这一项：%s", itemID)
		case item.Required:
			return nil, failure(contract.ErrInvalidInput, "“%s”是必需项，不能跳过", item.Label)
		}
		skipped[itemID] = true
	}
	attachments := make([]Attachment, 0, len(names))
	for _, name := range names {
		attachments = append(attachments, Attachment{Name: name})
	}
	var checklist *Checklist
	if len(items) > 0 {
		checklist = evaluateChecklist(items, req.config.FormSchema, req.form, attachments, skipped)
		if !checklist.Ready {
			return nil, failure(ErrChecklistIncomplete, "开工清单还缺：%s。备齐后才能开工。", strings.Join(checklist.Missing, "、"))
		}
	}
	if problems := completenessProblems(req.config, req.form); len(problems) > 0 {
		return nil, failure(contract.ErrInvalidInput, "表单还没填全或有错：%s", agent.ErrorText(problems))
	}
	// 提交前检查按会话模型：它所在的连接有 Key，并且模型在连接的模型列表里
	model := m.effectiveModel(t.current)
	if m.deps.Preflight != nil {
		if err := m.deps.Preflight(model); err != nil {
			return nil, err
		}
	}
	if model.Connection == "" {
		// 还没选过模型：任务照样能建（剧本模式不看它），记下检查时用的缺省模型
		model = m.effectiveModelSettings().Default
	}

	// 检查都过了才动材料：被清单拦下的确认不留半份副本
	if len(folderPaths) > 0 {
		if _, err := m.copyFolderMaterials(t, folderPaths); err != nil {
			return nil, err
		}
	}
	if len(sampleNames) > 0 {
		if err := m.copySampleMaterials(t, sampleNames); err != nil {
			return nil, err
		}
	}
	// 人确认跳过的可跳过项：留痕，开工时才把 skipField 写进表单
	for _, entry := range checklistSkipped(checklist) {
		if err := t.write(Message{Kind: kindChecklistSkipped, Item: entry.ID}); err != nil {
			return nil, err
		}
		text := "发起人确认跳过开工清单的“" + entry.Label + "”"
		if entry.SkipLabel != "" {
			text += "（" + entry.SkipLabel + "）"
		}
		if err := t.write(Message{Kind: KindNotice, Role: role, Text: text + "。"}); err != nil {
			return nil, err
		}
	}

	inputs := make([]contract.AttachmentInput, 0, len(names))
	for _, name := range names {
		inputs = append(inputs, contract.AttachmentInput{Name: name, SourcePath: m.store.attachmentPath(t.current.id, name)})
	}
	snapshot, err := m.deps.Engine.Create(contract.CreateTaskInput{
		Employee: req.employee, Form: applySkips(req.form, checklist, items), Attachments: inputs, Actor: role,
		WorkspaceID: t.current.workspaceID, Model: model,
	})
	if err != nil {
		return nil, err
	}
	if t.current.employee != req.employee {
		if err := t.write(Message{Kind: kindEmployeeSet, Employee: req.employee}); err != nil {
			return nil, err
		}
	}
	if err := t.write(Message{Kind: kindTaskBound, TaskID: snapshot.ID}); err != nil {
		return nil, err
	}
	if result := m.deps.Engine.Command(snapshot.ID, contract.TaskCommand{Type: contract.CmdSubmit}, role); !result.OK {
		return nil, failure(result.Error, "%s", result.Message)
	}
	if latest, found := m.deps.Engine.Get(snapshot.ID); found {
		return latest, nil
	}
	return snapshot, nil
}

// checklistSkipped 清单里被人确认跳过的项（开工清单没有时为空）。
func checklistSkipped(checklist *Checklist) []ChecklistEntry {
	if checklist == nil {
		return nil
	}
	out := []ChecklistEntry{}
	for _, entry := range checklist.Items {
		if entry.Status == ItemSkipped {
			out = append(out, entry)
		}
	}
	return out
}

// copySampleMaterials 把应用目录里的样例文件复制进会话材料。样例文件只留在应用目录和会话目录，不写进人的工作目录。
func (m *Manager) copySampleMaterials(t *turn, names []string) error {
	sample := m.sample()
	if sample == nil {
		return failure(contract.ErrNotFound, "公开样例已经不在了，没法复制样例材料")
	}
	for _, name := range names {
		var file *SampleFile
		for index := range sample.Files {
			if sample.Files[index].Name == name {
				file = &sample.Files[index]
			}
		}
		if file == nil {
			return failure(contract.ErrNotFound, "公开样例里没有这份文件：%s", name)
		}
		size, err := m.store.saveAttachment(t.current.id, contract.AttachmentInput{Name: file.Name, SourcePath: file.Path})
		if err != nil {
			return err
		}
		if err := t.write(Message{Kind: kindAttachmentAdded, Attachment: &Attachment{Name: file.Name, Size: size}}); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) sample() *Sample {
	if m.deps.Sample == nil {
		return nil
	}
	return m.deps.Sample()
}

// ---------------------------------------------------------------- 人在员工表单卡上答复

// FormAnswer 人在员工表单卡上的答复。
type FormAnswer struct {
	// Decision confirm（确认开工）或 reject（不是这个员工）
	Decision string
	// Form 人在卡片上改过的表单字段；为 nil 时用卡片上的预填
	Form map[string]any
	// Attachments 要带进任务的会话材料（人补传的，加上卡片上 Agent 给的会话材料）
	Attachments []string
	// Skipped 人确认跳过的可跳过清单项
	Skipped []string
}

// formCardOf 会话日志里某张员工表单卡。
func formCardOf(current *record, requestID string) (Card, bool) {
	for _, message := range current.messages {
		if message.Card != nil && message.Card.Type == CardEmployeeForm && message.Card.RequestID == requestID {
			return *message.Card, true
		}
	}
	return Card{}, false
}

func formDecided(current *record, requestID string) bool {
	for _, message := range current.messages {
		if message.Kind == kindEmployeeFormDecided && message.RequestID == requestID {
			return true
		}
	}
	return false
}

// formWaitFor 一张表单卡的等待记录。载入公开样例弹出的卡没有一轮在等它：进程重启后记录没了，
// 只要日志里还没有答复，就现建一个，卡片照常能确认。
func (m *Manager) formWaitFor(sessionID string, card Card) *formWait {
	m.formMu.Lock()
	defer m.formMu.Unlock()
	wait := m.forms[card.RequestID]
	if wait == nil && card.Standalone {
		wait = &formWait{id: card.RequestID, session: sessionID}
		m.forms[card.RequestID] = wait
	}
	if wait != nil && wait.session != sessionID {
		return nil
	}
	return wait
}

// AnswerEmployeeForm 发起人在员工表单卡上答复：确认开工，或者说“不是这个员工”。
// 这是人的动作，Agent 没有对应的工具。不占这一轮的写锁：这一轮正停在这张卡上等着。
func (m *Manager) AnswerEmployeeForm(id string, role contract.Role, requestID string, answer FormAnswer) (*Reply, error) {
	if role != contract.RoleInitiator {
		return nil, failure(contract.ErrForbiddenRole, "只有发起人可以答复员工表单")
	}
	if answer.Decision != AnswerConfirm && answer.Decision != AnswerReject {
		return nil, failure(contract.ErrInvalidInput, "答复必须是 confirm 或 reject，收到 %q", answer.Decision)
	}
	current, err := m.load(id)
	if err != nil {
		return nil, err
	}
	card, found := formCardOf(current, requestID)
	if !found {
		return nil, failure(contract.ErrNotFound, "没有这张员工表单：%s", requestID)
	}
	wait := m.formWaitFor(id, card)
	if wait == nil || formDecided(current, requestID) {
		return nil, failure(ErrFormSettled, "这张表单已经答复或取消了")
	}
	wait.mu.Lock()
	defer wait.mu.Unlock()
	if wait.settled {
		return nil, failure(ErrFormSettled, "这张表单已经答复或取消了")
	}
	t, done, err := m.beginUnlocked(id, role)
	if err != nil {
		return nil, err
	}
	defer done()
	if formDecided(t.current, requestID) {
		return nil, failure(ErrFormSettled, "这张表单已经答复或取消了")
	}

	var res formResult
	if answer.Decision == AnswerReject {
		if err := t.write(Message{Kind: kindEmployeeFormDecided, RequestID: requestID, Decision: DecisionFormRejected}); err != nil {
			return nil, err
		}
		res = formResult{rejected: true}
	} else {
		snapshot, err := m.confirmCard(t, role, card, answer)
		if err != nil {
			// 没开成：卡片还在等，人改好再点
			return nil, err
		}
		if err := t.write(Message{Kind: kindEmployeeFormDecided, RequestID: requestID, Decision: DecisionFormConfirmed, TaskID: snapshot.ID}); err != nil {
			return nil, err
		}
		res = formResult{taskID: snapshot.ID, state: snapshot.State}
	}
	wait.settled = true
	m.dropForm(requestID)
	if wait.result != nil {
		wait.result <- res
	}
	return m.reply(t)
}

// confirmCard 确认开工：以人在卡片上改过的表单为准，材料是卡片上 Agent 给的加上人补传的。
func (m *Manager) confirmCard(t *turn, role contract.Role, card Card, answer FormAnswer) (*contract.TaskSnapshot, error) {
	if m.deps.LoadEmployee == nil {
		return nil, failure(contract.ErrNotFound, "读不到员工配置")
	}
	config, err := m.deps.LoadEmployee(card.Employee)
	if err != nil {
		return nil, failure(contract.ErrNotFound, "读不到员工配置：%s", err.Error())
	}
	form := answer.Form
	if form == nil {
		form = card.Draft
	}
	attachments := append([]string{}, answer.Attachments...)
	for _, material := range card.Materials {
		if material.Source == MaterialAttachment {
			attachments = append(attachments, material.Name)
		}
	}
	return m.startEmployeeTask(t, role, startRequest{
		employee: card.Employee, config: config, form: form, attachments: attachments, materials: card.Materials, skipped: answer.Skipped,
	})
}

// LoadSample 欢迎页“载入公开样例”：把样例用例的输入（统计周期、样例材料）预填成一张员工表单，人确认即开工。
// 样例材料从应用目录取，确认时才复制进会话，样例文件不写进人的工作目录。
func (m *Manager) LoadSample(id string, role contract.Role) (*Reply, error) {
	if role != contract.RoleInitiator {
		return nil, failure(contract.ErrForbiddenRole, "只有发起人可以载入公开样例")
	}
	sample := m.sample()
	if sample == nil {
		return nil, failure(contract.ErrNotFound, "没有公开样例（首次启动会自动释放到应用目录）")
	}
	employees, err := m.deps.Employees()
	if err != nil {
		return nil, err
	}
	name := employeeNameOf(employees, sample.Employee)
	if name == "" || m.deps.LoadEmployee == nil {
		return nil, failure(contract.ErrNotFound, "公开样例的员工不存在：%s", sample.Employee)
	}
	config, err := m.deps.LoadEmployee(sample.Employee)
	if err != nil {
		return nil, failure(contract.ErrNotFound, "读不到公开样例的员工配置：%s", err.Error())
	}
	t, done, err := m.begin(id, role)
	if err != nil {
		return nil, err
	}
	defer done()
	if err := m.refuseNewTask(t.current); err != nil {
		return nil, err
	}
	materials := make([]FormMaterial, 0, len(sample.Files))
	for _, file := range sample.Files {
		materials = append(materials, FormMaterial{Name: file.Name, Size: file.Size, Source: MaterialSample})
	}
	fields := map[string]any{}
	for key, value := range sample.Form {
		fields[key] = value
	}
	reason := "公开样例"
	if sample.Title != "" {
		reason += "：" + sample.Title
	}
	card := Card{
		Type: CardEmployeeForm, RequestID: newRequestID(), Employee: sample.Employee, EmployeeName: name, Draft: fields,
		Prefilled: prefilledNames(fields), Materials: materials, Reason: reason, Pending: true, Standalone: true, Checklist: config.Checklist, Baseline: attachmentNames(t.current.attachments),
	}
	m.registerForm(&formWait{id: card.RequestID, session: id})
	if err := t.write(Message{Kind: KindCard, Card: &card}); err != nil {
		m.dropForm(card.RequestID)
		return nil, err
	}
	return m.reply(t)
}

// ---------------------------------------------------------------- 读取时给卡片配上状态

// projectEmployeeForms 给员工表单卡配上现在的状态：有没有答复、是否还在等。
// 卡片写进日志时只有请求本身；答复是后来的一行，所以读取时才算。进程重启后没有人在等的 Agent 卡算已取消，
// 载入公开样例弹出的卡不依赖某一轮，重启后仍可确认。
func (m *Manager) projectEmployeeForms(messages []Message, current *record) []Message {
	if current == nil {
		return messages
	}
	decided := map[string]Message{}
	for _, message := range current.messages {
		if message.Kind == kindEmployeeFormDecided {
			decided[message.RequestID] = message
		}
	}
	for index := range messages {
		card := messages[index].Card
		if card == nil || card.Type != CardEmployeeForm {
			continue
		}
		copied := *card
		copied.Pending, copied.Decision = false, ""
		if message, found := decided[copied.RequestID]; found {
			copied.Decision = message.Decision
			if message.TaskID != "" {
				copied.TaskID = message.TaskID
			}
		} else if m.formWaiting(copied.RequestID, current.id) || copied.Standalone {
			copied.Pending = true
		} else {
			copied.Decision = DecisionCancelled
		}
		messages[index].Card = &copied
	}
	return messages
}

// projectCards 读取时给需要现算状态的卡片（权限确认、员工表单）配上状态。
func (m *Manager) projectCards(messages []Message, current *record) []Message {
	return m.projectEmployeeForms(m.projectQuestions(m.projectPermissions(messages, current), current), current)
}

func attachmentNames(attachments []Attachment) []string {
	names := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		names = append(names, attachment.Name)
	}
	return names
}
