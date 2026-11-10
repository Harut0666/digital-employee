// 权限模式与权限确认（Spec 0007、T48）。
//
// 权限模式决定前台 Agent 的动作要不要先问人：读始终允许；写文件、运行员工、把目录文件用作材料
// 按模式放行、询问或拒绝。判定放在工具执行之前的同一个关口（turnLoop.gate），Agent 的工具里没有绕过它的路。
// 模式只管 Agent 的动作，不管流程里人的决定：审核、重试、终止、开工清单确认本来就没有对应的 Agent 工具。
//
// “问”的方式：这一轮在工具调用处暂停，会话里出现“权限确认”卡片；人答复（POST /permissions/{requestId}）
// 或点停止之后继续。每一次允许、拒绝、自动放行都写进会话日志。
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

// 权限模式（ADR-0007 增补二）：三档，越往右免问的越多。
const (
	PermissionReadOnly  = "read_only" // 仅可查看：只读文件、做计划、回答问题，不写文件、不运行员工和命令
	PermissionWorkspace = "workspace" // 仅限工作区：工作区内读写文件不问；运行员工、用目录文件、运行命令前先问；新会话默认
	PermissionFull      = "full"      // 完全权限：不再逐项询问
)

// DefaultPermissionMode 新会话的权限模式。
const DefaultPermissionMode = PermissionWorkspace

// PermissionModes 全部权限模式，从严到松（也是界面菜单的顺序）。
var PermissionModes = []string{PermissionReadOnly, PermissionWorkspace, PermissionFull}

// PermissionModeLabels 权限模式的显示名。
var PermissionModeLabels = map[string]string{
	PermissionReadOnly: "仅可查看", PermissionWorkspace: "仅限工作区", PermissionFull: "完全权限",
}

// legacyPermissionModes 旧版本五档里的值 → 新值（会话日志里已有的“选择权限模式”，以及接口入参里的旧值）。
// 老会话因此会略宽松：ask、accept_edits、auto 都读作 workspace，其中 ask 原先连写文件也要问。
var legacyPermissionModes = map[string]string{
	"plan": PermissionReadOnly, "ask": PermissionWorkspace, "accept_edits": PermissionWorkspace, "auto": PermissionWorkspace, "yolo": PermissionFull,
}

// ValidPermissionMode 是不是三个权限模式之一（只认新值；旧值见 NormalizePermissionMode）。
func ValidPermissionMode(mode string) bool {
	_, ok := PermissionModeLabels[mode]
	return ok
}

// NormalizePermissionMode 把新值或旧五档的值读作新值；不认得的返回 false。
func NormalizePermissionMode(mode string) (string, bool) {
	if ValidPermissionMode(mode) {
		return mode, true
	}
	mapped, ok := legacyPermissionModes[mode]
	return mapped, ok
}

// 动作类别：权限判定和“本会话总是允许这一类”都以它为单位。读不是动作类别（始终允许）。
const (
	// ActionWriteFile 新建或修改工作区内的文件（write_file、edit_file 同属这一类）
	ActionWriteFile = "write_file"
	// ActionRunEmployee 运行员工（run_employee）
	ActionRunEmployee = "run_employee"
	// ActionUseFolderFile 把目录里的文件用作材料
	ActionUseFolderFile = "use_folder_file"
)

// ActionLabels 动作类别的说法。
var ActionLabels = map[string]string{
	ActionWriteFile: "写入文件", ActionRunEmployee: "运行员工", ActionUseFolderFile: "使用目录文件", ActionShell: "运行命令",
}

// 人对权限确认卡的答复。
const (
	DecisionAllowOnce   = "allow_once"   // 允许一次
	DecisionAlwaysAllow = "always_allow" // 本会话总是允许这一类
	DecisionDeny        = "deny"         // 拒绝
	DecisionCancelled   = "cancelled"    // 停止时未决的确认
)

// ErrPermissionSettled 这张权限确认已经答复过（或已取消）。
const ErrPermissionSettled = "permission_settled"

// errTurnCancelled 等人的时候这一轮被停止：工具没有执行。
var errTurnCancelled = errors.New("这一轮被停止")

// Verdict 一个动作在某个模式下的处理。
type Verdict string

// 三种处理。
const (
	VerdictAllow Verdict = "allow"
	VerdictAsk   Verdict = "ask"
	VerdictDeny  Verdict = "deny"
)

// Decide ADR-0007 增补二的矩阵。档位是单调的：越往右，免问的越多。
//
// 模式不是三档之一（旧五档的值先读作新值；都不认得的，比如以后的版本写进来的）按最严格的 read_only 处理，宁可只读也不放宽。
//
// 运行员工（run_employee）在 full 下放行：表单完整有效、必需开工清单项备齐就直接开工，否则由 run_employee 自己弹员工表单；
// workspace 下的“问”也是员工表单卡（selfDecide），关口不替它判断表单。使用目录文件同理：full 自动确认，
// workspace 的“问”是“使用目录文件”卡。
func Decide(mode, action string) Verdict {
	normalized, known := NormalizePermissionMode(mode)
	if !known {
		normalized = PermissionReadOnly
	}
	switch action {
	case ActionWriteFile:
		// 工作区内读写文件不问（边界检查在 guard 里，始终适用）
		if normalized == PermissionReadOnly {
			return VerdictDeny
		}
		return VerdictAllow
	case ActionRunEmployee, ActionUseFolderFile, ActionShell:
		// 运行员工、目录文件用作材料、命令行（没有沙箱，见 shell.go）：read_only 拒绝，workspace 问，full 放行
		switch normalized {
		case PermissionReadOnly:
			return VerdictDeny
		case PermissionWorkspace:
			return VerdictAsk
		}
		return VerdictAllow
	}
	// 读、搜索、待办、提问和其它没有归类的动作：始终允许
	return VerdictAllow
}

// mode 会话当前的权限模式；没选过就是 workspace，不认得的值按最严格的 read_only。
func (r *record) mode() string {
	if r.permissionMode == "" {
		return DefaultPermissionMode
	}
	if normalized, ok := NormalizePermissionMode(r.permissionMode); ok {
		return normalized
	}
	return PermissionReadOnly
}

// ---------------------------------------------------------------- 选模式

// SetPermissionMode 发起人在输入框左下角选权限模式。写进会话日志，同时清空“本会话总是允许”。
// 不占这一轮的写锁：Agent 正在等权限确认时人也能换模式。
func (m *Manager) SetPermissionMode(id string, role contract.Role, mode string) (*Reply, error) {
	if role != contract.RoleInitiator {
		return nil, failure(contract.ErrForbiddenRole, "只有发起人可以选权限模式")
	}
	// 新值和旧五档的值都收；落盘的总是新值
	normalized, ok := NormalizePermissionMode(strings.TrimSpace(mode))
	if !ok {
		return nil, failure(contract.ErrInvalidInput, "没有这个权限模式：%q。可选 read_only（仅可查看）、workspace（仅限工作区）、full（完全权限）", mode)
	}
	mode = normalized
	t, done, err := m.beginUnlocked(id, role)
	if err != nil {
		return nil, err
	}
	defer done()
	if err := t.write(Message{Kind: KindPermissionModeSet, Mode: mode}); err != nil {
		return nil, err
	}
	return m.reply(t)
}

// permissionPrompt 前台 Agent 的系统提示词里写明的当前权限模式。
func permissionPrompt(mode string) string {
	lines := []string{"## 权限模式", "", "当前模式：" + PermissionModeLabels[mode] + "（" + mode + "）。"}
	switch mode {
	case PermissionReadOnly:
		lines = append(lines, "仅可查看模式是只读的：你只能读、列、搜工作区目录里的文件，可以做计划、问问题，不能写文件，不能运行员工，不能运行命令，也不能把目录文件用作材料；这类动作会被直接拒绝。请把打算怎么做的方案告诉发起人，等 TA 切换到“仅限工作区”或“完全权限”后再动手。")
	case PermissionWorkspace:
		lines = append(lines, "仅限工作区模式：在工作区目录里读、写、改文件不用问发起人；run_employee 一定会弹员工表单卡，由发起人核对并确认后才开工；把目录文件用作材料要先确认；运行命令前要先问发起人。")
	case PermissionFull:
		lines = append(lines, "完全权限模式：不再逐项询问。写、改文件不用问，目录文件自动确认；run_employee 的表单完整有效、必需的开工清单项备齐时直接开工，否则弹员工表单让发起人补；命令直接运行（命令仍可能触达工作区之外，务必只做发起人要求的事）。")
	}
	lines = append(lines, shellModeLine(mode))
	lines = append(lines,
		"动作被拒绝（工具结果写着“发起人拒绝”或“不能”）时，不要原样重试，换个办法或问发起人想怎么做。",
		"不管哪个模式：写文件只限工作区目录内；不能写隐藏文件，也不能写“交付”和“材料”文件夹；单个文件最多 1 MB，只写文本。你没有审核、重试、终止任务的权力。",
	)
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------- 关口

// permissionWait 一张还在等人答复的权限确认。
type permissionWait struct {
	id      string
	session string
	action  string
	// decidedKind 答复写进会话日志时用的消息类型（权限确认、提问各一种）
	decidedKind string
	// decision 人的答复（容量 1：答复的一方不用等这一轮醒来）
	decision chan string
	// settled 已经有了结果（人答复了，或停止取消了）：只有第一个到的能写结果
	settled bool
}

func newRequestID() string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%016x", len(buffer))
	}
	return hex.EncodeToString(buffer)
}

func (m *Manager) registerWait(wait *permissionWait) {
	m.permMu.Lock()
	defer m.permMu.Unlock()
	m.waits[wait.id] = wait
}

func (m *Manager) dropWait(id string) {
	m.permMu.Lock()
	defer m.permMu.Unlock()
	delete(m.waits, id)
}

// settleWait 抢到结果的权利：第一个调用的得到 true。
func (m *Manager) settleWait(wait *permissionWait) bool {
	m.permMu.Lock()
	defer m.permMu.Unlock()
	if wait.settled {
		return false
	}
	wait.settled = true
	return true
}

// pendingPermission 会话里有没有权限确认或员工表单在等人答复（状态点显示“等你处理”）。
func (m *Manager) pendingPermission(id string) bool {
	m.permMu.Lock()
	for _, wait := range m.waits {
		if wait.session == id && !wait.settled {
			m.permMu.Unlock()
			return true
		}
	}
	m.permMu.Unlock()
	return m.pendingForm(id)
}

// permissionButtons 权限确认卡上的三个按钮（decision 就是答复）。
func permissionButtons() []PermissionChoice {
	return []PermissionChoice{
		{Decision: DecisionAllowOnce, Label: "允许一次", Style: "primary"},
		{Decision: DecisionAlwaysAllow, Label: "本会话总是允许这一类", Style: "default"},
		{Decision: DecisionDeny, Label: "拒绝", Style: "danger"},
	}
}

// buttonsFor 这张卡上能点的按钮：shell 只有“允许一次”和“拒绝”（每条命令都可能不同，不提供“总是允许”）。
func buttonsFor(action string) []PermissionChoice {
	if action != ActionShell {
		return permissionButtons()
	}
	return []PermissionChoice{
		{Decision: DecisionAllowOnce, Label: "允许一次", Style: "primary"},
		{Decision: DecisionDeny, Label: "拒绝", Style: "danger"},
	}
}

// gate 工具执行前的同一个关口：先查始终适用的边界，再按权限模式放行、询问或拒绝。
//
// 返回 refusal 非空表示拒绝（原因同时是给模型的工具结果；发起人自己的拒绝带 byInitiator）；cancelled 表示等确认时被停止；err 是日志写不进去。
func (loop *turnLoop) gate(ctx context.Context, call agent.ContentBlock, item tool) (refusal *denial, cancelled bool, err error) {
	if item.action == "" {
		// 读：没有动作类别，只有边界检查
		if item.guard != nil {
			if guardErr := item.guard(call.Arguments); guardErr != nil {
				return &denial{reason: guardErr.Error()}, false, nil
			}
		}
		return nil, false, nil
	}
	manager := loop.job.manager
	// 每次都读最新的记录：人可能在这一轮进行中换了模式或选了“总是允许”
	current, loadErr := manager.load(loop.job.session)
	if loadErr != nil {
		return nil, false, loadErr
	}
	// 角色权限保持原样：写文件、运行员工、用目录文件都是发起人的事。现在说话的不是发起人
	// （审核人、操作员的话触发的这一轮）时一律拒绝，不论模式，也不弹只有发起人能答的确认
	if speaker := speakerRole(current, loop.job.role); speaker != contract.RoleInitiator {
		return &denial{reason: fmt.Sprintf("现在和你说话的是%s，只有发起人可以让你%s。请发起人来说，或让 TA 在对话里提出。",
			contract.RoleLabels[speaker], ActionLabels[item.action])}, false, nil
	}
	if item.guard != nil {
		if guardErr := item.guard(call.Arguments); guardErr != nil {
			return &denial{reason: guardErr.Error()}, false, nil
		}
	}
	mode := current.mode()
	label := ActionLabels[item.action]
	object, detail := "", ""
	if item.describe != nil {
		object, detail = item.describe(call.Arguments)
	}
	switch Decide(mode, item.action) {
	case VerdictDeny:
		return &denial{reason: fmt.Sprintf("当前是%s模式，不能%s%s。请改为给出方案（包括打算运行的命令）告诉发起人，等 TA 切换权限模式后再动手。",
			PermissionModeLabels[mode], label, suffixObject(object))}, false, nil
	case VerdictAllow:
		if item.selfDecide {
			return nil, false, nil
		}
		if item.forceAsk != nil {
			if force, _ := item.forceAsk(call.Arguments); force {
				// 模式放行，但这次的内容太危险：改为询问
				return loop.ask(ctx, call, item, object, detail)
			}
		}
		return nil, false, loop.traceAllowed(call, item, fmt.Sprintf("%s模式：%s不用确认", PermissionModeLabels[mode], label))
	}
	if item.selfDecide {
		// 工具自己问：运行员工弹员工表单卡（一定要人确认，“总是允许”不适用），使用目录文件发“使用目录文件”卡
		return nil, false, nil
	}
	if current.alwaysAllowed[item.action] && !item.noAlways {
		return nil, false, loop.traceAllowed(call, item, fmt.Sprintf("本会话已选择总是允许「%s」", label))
	}
	return loop.ask(ctx, call, item, object, detail)
}

func suffixObject(object string) string {
	if object == "" {
		return ""
	}
	return "：" + object
}

// traceAllowed 免问放行也留痕：轨迹里能看到哪些动作是自动放行的。
func (loop *turnLoop) traceAllowed(call agent.ContentBlock, item tool, reason string) error {
	return loop.write(Message{Kind: KindPermissionAllowed, Tool: call.Name, Args: item.loggedArgs(call.Arguments), Reason: reason})
}

// ask 弹出权限确认卡，这一轮在这里暂停，直到人答复或点停止。
func (loop *turnLoop) ask(ctx context.Context, call agent.ContentBlock, item tool, object, detail string) (*denial, bool, error) {
	label := ActionLabels[item.action]
	card := Card{
		Type: CardPermission, Action: item.action, ActionLabel: label, Tool: call.Name,
		Object: object, Detail: detail, Choices: buttonsFor(item.action),
	}
	if item.action == ActionWriteFile {
		card.Path = object
	}
	if item.decorate != nil {
		item.decorate(&card, call.Arguments)
	}
	decision, cancelled, err := loop.awaitHuman(ctx, kindPermissionDecided, item.action, card)
	if err != nil || cancelled {
		return nil, cancelled, err
	}
	if decision == DecisionDeny {
		return &denial{reason: fmt.Sprintf("发起人拒绝：没有允许%s%s。请换个办法，或问问发起人想怎么做。", label, suffixObject(object)), byInitiator: true}, false, nil
	}
	return nil, false, nil
}

// awaitHuman 等人答复的通用一步：登记等待、发卡、暂停这一轮，直到人答复或点停止。
// 权限确认和 ask_user_question 共用它（等待表、停止时记“已取消”、答复和停止同时到的竞争都只有这一份）。
// 返回人的答复（提问时是答案的 JSON）；cancelled 表示被停止，动作没有执行。
func (loop *turnLoop) awaitHuman(ctx context.Context, decidedKind, action string, card Card) (decision string, cancelled bool, err error) {
	if ctx.Err() != nil {
		return "", true, nil
	}
	manager := loop.job.manager
	wait := &permissionWait{id: newRequestID(), session: loop.job.session, action: action, decidedKind: decidedKind, decision: make(chan string, 1)}
	// 先登记再发卡：人看到卡片时它一定已经能答复
	manager.registerWait(wait)
	defer manager.dropWait(wait.id)

	card.RequestID, card.Pending = wait.id, true
	if err := loop.write(Message{Kind: KindCard, Card: &card}); err != nil {
		return "", false, err
	}
	written := loop.turn.out[len(loop.turn.out)-1]

	select {
	case decision = <-wait.decision:
	case <-ctx.Done():
		if manager.settleWait(wait) {
			return "", true, loop.write(Message{Kind: decidedKind, RequestID: wait.id, Decision: DecisionCancelled, Action: wait.action})
		}
		// 人的答复恰好同时到了：它已经写进日志，但这一轮被停止，动作不执行
		<-wait.decision
		return "", true, nil
	}
	// 答复是别的协程写进日志的：重读，这一轮后面的判定才看得到“总是允许”
	if current, err := manager.load(loop.job.session); err == nil {
		loop.turn.current = current
	}
	answered := written
	copied := *written.Card
	copied.Pending, copied.Choices = false, nil
	if decidedKind == kindPermissionDecided {
		copied.Decision = decision
	} else {
		copied.Decision = DecisionAnswered
		copied.Answers = answersRecorded(loop.turn.current, wait.id)
	}
	answered.Card = &copied
	loop.bus.publish(AgentEvent{Turn: loop.job.id, Type: EventMessage, Msg: &answered})
	return decision, false, nil
}

// answerWait 人答复一张在等的确认（权限确认、提问共用）：先抢到“结果的权利”，再写日志，最后放行那一轮。
// message 是写进会话日志的答复，deliver 是交给等待方的内容。
func (m *Manager) answerWait(id string, role contract.Role, requestID string, hasCard func(*record) bool, message func(*permissionWait) Message, deliver string) (*Reply, error) {
	current, err := m.load(id)
	if err != nil {
		return nil, err
	}
	if !hasCard(current) {
		return nil, failure(contract.ErrNotFound, "没有这张确认：%s", requestID)
	}
	m.permMu.Lock()
	wait := m.waits[requestID]
	if wait == nil || wait.session != id || wait.settled {
		m.permMu.Unlock()
		return nil, failure(ErrPermissionSettled, "这张确认已经答复或取消了")
	}
	wait.settled = true
	m.permMu.Unlock()

	t, done, err := m.beginUnlocked(id, role)
	if err != nil {
		wait.decision <- DecisionDeny
		return nil, err
	}
	defer done()
	// 先写日志再放行：人读到的答复和这一轮接下来做的事一致
	writeErr := t.write(message(wait))
	wait.decision <- deliver
	if writeErr != nil {
		return nil, writeErr
	}
	return m.reply(t)
}

// DecidePermission 发起人在权限确认卡上答复。不占这一轮的写锁：这一轮正停在这张卡上等着。
func (m *Manager) DecidePermission(id string, role contract.Role, requestID, decision string) (*Reply, error) {
	if role != contract.RoleInitiator {
		return nil, failure(contract.ErrForbiddenRole, "只有发起人可以答复权限确认")
	}
	switch decision {
	case DecisionAllowOnce, DecisionAlwaysAllow, DecisionDeny:
	default:
		return nil, failure(contract.ErrInvalidInput, "答复必须是 allow_once、always_allow 或 deny，收到 %q", decision)
	}
	if decision == DecisionAlwaysAllow {
		if current, err := m.load(id); err == nil {
			for _, message := range current.messages {
				if message.Card != nil && message.Card.Type == CardPermission && message.Card.RequestID == requestID && message.Card.Action == ActionShell {
					return nil, failure(contract.ErrInvalidInput, "运行命令不提供“总是允许”：每条命令都可能不同，只能允许一次或拒绝")
				}
			}
		}
	}
	return m.answerWait(id, role, requestID,
		func(current *record) bool { return hasPermissionCard(current, requestID) },
		func(wait *permissionWait) Message {
			return Message{Kind: kindPermissionDecided, RequestID: requestID, Decision: decision, Action: wait.action}
		}, decision)
}
func hasPermissionCard(current *record, requestID string) bool {
	for _, message := range current.messages {
		if message.Card != nil && message.Card.Type == CardPermission && message.Card.RequestID == requestID {
			return true
		}
	}
	return false
}

// projectPermissions 给权限确认卡配上现在的状态：有没有答复、是否还在等、能点的按钮。
// 卡片写进日志时只有请求本身；答复是后来的一行，所以读取时才算。进程重启后没有人在等的卡片算已取消。
func (m *Manager) projectPermissions(messages []Message, current *record) []Message {
	if current == nil {
		return messages
	}
	decided := map[string]string{}
	for _, message := range current.messages {
		if message.Kind == kindPermissionDecided {
			decided[message.RequestID] = message.Decision
		}
	}
	for index := range messages {
		card := messages[index].Card
		if card == nil || card.Type != CardPermission {
			continue
		}
		copied := *card
		copied.Pending, copied.Choices = false, nil
		if decision, found := decided[copied.RequestID]; found {
			copied.Decision = decision
		} else if m.waiting(copied.RequestID, current.id) {
			copied.Pending, copied.Decision, copied.Choices = true, "", buttonsFor(copied.Action)
		} else {
			copied.Decision = DecisionCancelled
		}
		messages[index].Card = &copied
	}
	return messages
}

func (m *Manager) waiting(requestID, sessionID string) bool {
	m.permMu.Lock()
	defer m.permMu.Unlock()
	wait := m.waits[requestID]
	return wait != nil && wait.session == sessionID
}
