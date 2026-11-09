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

// 权限模式。
const (
	PermissionPlan        = "plan"         // 计划：只读
	PermissionAsk         = "ask"          // 询问：默认，写之前问
	PermissionAcceptEdits = "accept_edits" // 接受编辑：工作区里写文件不问
	PermissionAuto        = "auto"         // 自动：员工表单完整有效时直接开工
	PermissionYolo        = "yolo"         // 放开：连目录文件的确认也免掉
)

// PermissionModes 全部权限模式，从严到松。
var PermissionModes = []string{PermissionPlan, PermissionAsk, PermissionAcceptEdits, PermissionAuto, PermissionYolo}

// PermissionModeLabels 权限模式的显示名。
var PermissionModeLabels = map[string]string{
	PermissionPlan: "计划", PermissionAsk: "询问", PermissionAcceptEdits: "接受编辑", PermissionAuto: "自动", PermissionYolo: "放开",
}

// ValidPermissionMode 是不是五个权限模式之一。
func ValidPermissionMode(mode string) bool {
	_, ok := PermissionModeLabels[mode]
	return ok
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
	ActionWriteFile: "写入文件", ActionRunEmployee: "运行员工", ActionUseFolderFile: "使用目录文件",
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

// Decide Spec 0007 的矩阵。档位是单调的：越往松，免问的越多。
//
// 运行员工（run_employee，T49）在 auto、yolo 下放行：表单完整有效就直接开工，否则由 run_employee 自己弹员工表单；
// ask、accept_edits 下的“问”也是员工表单卡（selfDecide），关口不替它判断表单。使用目录文件同理：yolo 自动确认，
// 其余模式的“问”是“使用目录文件”卡。
func Decide(mode, action string) Verdict {
	switch action {
	case ActionWriteFile:
		switch mode {
		case PermissionPlan:
			return VerdictDeny
		case PermissionAsk:
			return VerdictAsk
		}
		return VerdictAllow
	case ActionRunEmployee:
		switch mode {
		case PermissionPlan:
			return VerdictDeny
		case PermissionAsk, PermissionAcceptEdits:
			return VerdictAsk
		}
		return VerdictAllow
	case ActionUseFolderFile:
		switch mode {
		case PermissionPlan:
			return VerdictDeny
		case PermissionYolo:
			return VerdictAllow
		}
		return VerdictAsk
	}
	// 读和其它没有归类的动作：始终允许
	return VerdictAllow
}

// mode 会话当前的权限模式；没选过就是 ask。
func (r *record) mode() string {
	if ValidPermissionMode(r.permissionMode) {
		return r.permissionMode
	}
	return PermissionAsk
}

// ---------------------------------------------------------------- 选模式

// SetPermissionMode 发起人在输入框左下角选权限模式。写进会话日志，同时清空“本会话总是允许”。
// 不占这一轮的写锁：Agent 正在等权限确认时人也能换模式。
func (m *Manager) SetPermissionMode(id string, role contract.Role, mode string) (*Reply, error) {
	if role != contract.RoleInitiator {
		return nil, failure(contract.ErrForbiddenRole, "只有发起人可以选权限模式")
	}
	mode = strings.TrimSpace(mode)
	if !ValidPermissionMode(mode) {
		return nil, failure(contract.ErrInvalidInput, "没有这个权限模式：%q。可选 plan、ask、accept_edits、auto、yolo", mode)
	}
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
	case PermissionPlan:
		lines = append(lines, "计划模式是只读的：你只能读、列工作区目录里的文件，不能写文件，不能运行员工，也不能把目录文件用作材料；这类动作会被直接拒绝。请把打算怎么做的方案告诉发起人，等 TA 切换模式后再动手。")
	case PermissionAsk:
		lines = append(lines, "询问模式：读文件不受限；写文件前程序会弹出权限确认，由发起人允许或拒绝；run_employee 一定会弹员工表单卡，由发起人核对并确认后才开工。")
	case PermissionAcceptEdits:
		lines = append(lines, "接受编辑模式：在工作区目录里写、改文件不用问发起人；run_employee 仍一定会弹员工表单卡，把目录文件用作材料仍要先确认。")
	case PermissionAuto:
		lines = append(lines, "自动模式：写、改文件不用问；run_employee 的表单填得完整有效、必需的开工清单项都备齐时直接开工，缺什么才弹员工表单让发起人补；把目录文件用作材料仍要先确认（带目录文件的表单照样弹）。")
	case PermissionYolo:
		lines = append(lines, "放开模式：写、改文件不用问，目录文件自动确认；run_employee 的表单完整有效、必需的开工清单项备齐时直接开工，否则弹员工表单让发起人补。")
	}
	lines = append(lines,
		"动作被拒绝（工具结果写着“用户拒绝”或“不能”）时，不要原样重试，换个办法或问发起人想怎么做。",
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

// gate 工具执行前的同一个关口：先查始终适用的边界，再按权限模式放行、询问或拒绝。
//
// 返回 refusal 非空表示拒绝（原因同时是给模型的工具结果）；cancelled 表示等确认时被停止；err 是日志写不进去。
func (loop *turnLoop) gate(ctx context.Context, call agent.ContentBlock, item tool) (refusal string, cancelled bool, err error) {
	if item.guard != nil {
		if guardErr := item.guard(call.Arguments); guardErr != nil {
			return guardErr.Error(), false, nil
		}
	}
	if item.action == "" {
		return "", false, nil
	}
	manager := loop.job.manager
	// 每次都读最新的记录：人可能在这一轮进行中换了模式或选了“总是允许”
	current, loadErr := manager.load(loop.job.session)
	if loadErr != nil {
		return "", false, loadErr
	}
	mode := current.mode()
	label := ActionLabels[item.action]
	object, detail := "", ""
	if item.describe != nil {
		object, detail = item.describe(call.Arguments)
	}
	switch Decide(mode, item.action) {
	case VerdictDeny:
		return fmt.Sprintf("%s模式下只能读，不能%s%s。请先把打算怎么做告诉发起人，等 TA 切换权限模式后再动手。",
			PermissionModeLabels[mode], label, suffixObject(object)), false, nil
	case VerdictAllow:
		if item.selfDecide {
			return "", false, nil
		}
		return "", false, loop.traceAllowed(call, fmt.Sprintf("%s模式：%s不用确认", PermissionModeLabels[mode], label))
	}
	if item.selfDecide {
		// 工具自己问：运行员工弹员工表单卡（一定要人确认，“总是允许”不适用），使用目录文件发“使用目录文件”卡
		return "", false, nil
	}
	if current.alwaysAllowed[item.action] {
		return "", false, loop.traceAllowed(call, fmt.Sprintf("本会话已选择总是允许「%s」", label))
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
func (loop *turnLoop) traceAllowed(call agent.ContentBlock, reason string) error {
	return loop.write(Message{Kind: KindPermissionAllowed, Tool: call.Name, Args: call.Arguments, Reason: reason})
}

// ask 弹出权限确认卡，这一轮在这里暂停，直到人答复或点停止。
func (loop *turnLoop) ask(ctx context.Context, call agent.ContentBlock, item tool, object, detail string) (string, bool, error) {
	if ctx.Err() != nil {
		return "", true, nil
	}
	manager := loop.job.manager
	label := ActionLabels[item.action]
	wait := &permissionWait{id: newRequestID(), session: loop.job.session, action: item.action, decision: make(chan string, 1)}
	// 先登记再发卡：人看到卡片时它一定已经能答复
	manager.registerWait(wait)
	defer manager.dropWait(wait.id)

	card := Card{
		Type: CardPermission, RequestID: wait.id, Action: item.action, ActionLabel: label, Tool: call.Name,
		Object: object, Detail: detail, Pending: true, Choices: permissionButtons(),
	}
	if item.action == ActionWriteFile {
		card.Path = object
	}
	if err := loop.write(Message{Kind: KindCard, Card: &card}); err != nil {
		return "", false, err
	}
	written := loop.turn.out[len(loop.turn.out)-1]

	var decision string
	select {
	case decision = <-wait.decision:
	case <-ctx.Done():
		if manager.settleWait(wait) {
			return "", true, loop.write(Message{Kind: kindPermissionDecided, RequestID: wait.id, Decision: DecisionCancelled, Action: wait.action})
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
	copied.Pending, copied.Decision, copied.Choices = false, decision, nil
	answered.Card = &copied
	loop.bus.publish(AgentEvent{Turn: loop.job.id, Type: EventMessage, Msg: &answered})
	if decision == DecisionDeny {
		return fmt.Sprintf("用户拒绝：发起人没有允许%s%s。请换个办法，或问问发起人想怎么做。", label, suffixObject(object)), false, nil
	}
	return "", false, nil
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
	current, err := m.load(id)
	if err != nil {
		return nil, err
	}
	if !hasPermissionCard(current, requestID) {
		return nil, failure(contract.ErrNotFound, "没有这张权限确认：%s", requestID)
	}
	m.permMu.Lock()
	wait := m.waits[requestID]
	if wait == nil || wait.session != id || wait.settled {
		m.permMu.Unlock()
		return nil, failure(ErrPermissionSettled, "这张权限确认已经答复或取消了")
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
	writeErr := t.write(Message{Kind: kindPermissionDecided, RequestID: requestID, Decision: decision, Action: wait.action})
	wait.decision <- decision
	if writeErr != nil {
		return nil, writeErr
	}
	return m.reply(t)
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
			copied.Pending, copied.Decision, copied.Choices = true, "", permissionButtons()
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
