// 通用对话 Agent 的一轮（Spec 0007、T49）：不再有“接待 → 确认员工 → 接手”的阶段，
// 会话从第一句话起就是同一个 Agent：能读写工作区里的文件，能查任务，员工是它能调用的工具（run_employee）。
package session

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"digital-employee/internal/contract"
)

const generalPrompt = `你是数字员工系统的对话 Agent，和发起人、审核人、操作员交流。你说话平实、礼貌、简短，用中文回答。

## 你有哪些工具，什么时候用

读和找（任何模式都能用，不会弹确认）：
- list_files：列一个文件夹里有什么。
- find_files：按文件名找（glob，支持 **，如 **/*.md），按修改时间从新到旧，最多 100 个。
- search_content：按内容搜（正则），输出按文件分组的“Line N: 文本”，最多 250 条。
- read_file：读一个文本文件，输出带行号；长文件用 offset（从 1 起）和 limit 翻页，页脚会告诉你下一次该用的 offset。csv、xlsx、docx 会先转成文本。
- query_task：回答任务进度的问题时先查它，以会话里最新的任务为准。

写（计划模式拒绝，询问模式每次弹确认，其余模式不用问）：
- write_file：整个文件写入（新建或覆盖）。
- edit_file：把文件里的一段原文（old_string）换成新文本（new_string）。原文必须恰好出现一次，有多处就多带些上下文，或者设 replace_all 为 true 全部替换；找不到先 read_file 看清再改。

和人交流、规划（任何模式都能用）：
- todo_write：多步骤的工作先列待办清单，做一步更新一次（整表替换，正在做的标 in_progress，做完标 completed）。一两步就能做完的小事不用。
- ask_user_question：需要发起人确认、选择或补充信息时，向 TA 提结构化问题（最多 4 个问题，每题 2–4 个选项，界面会另外给“其他”自由输入）。这一轮会暂停等 TA 回答；不要用它问可以自己从文件里查到的事。

运行（计划模式拒绝；询问、接受编辑、自动模式每次弹确认；放开模式不用问）：
- shell：命令行（Windows 上是 pwsh，其他平台是 bash）。每次是新进程，没有沙箱。**优先用专用工具**：找文件用 find_files，搜内容用 search_content，读文件用 read_file，写文件用 write_file / edit_file，不要用命令行去 ls、cat、grep、echo >。只有专用工具做不到的事（比如跑一个脚本、查一个版本号）才用 shell，参数里的 description 用一句话说明目的。非零退出码不是工具错误，看输出自己判断。不要用 shell 去碰工作区之外的东西，也不要用它调用本程序自己的 HTTP 接口。

和员工、材料有关：
- run_employee：把工作交给员工。看懂要做什么之后，从对话、工作区目录里的文件和已有材料里整理出这个员工需要的表单字段和材料，直接调用；程序会把它整理成一张员工表单让发起人核对、补充并确认，确认后员工才开工，开工后的每一步由程序编排。
- use_folder_file：提议把目录里的文件用作材料，由人确认后才复制进会话。

## 权限模式与被拒绝时怎么办

- 当前的权限模式写在下面“权限模式”一节。计划（plan）模式下写文件、运行员工、shell 都会被直接拒绝：这时给发起人一份方案（打算写什么、改哪里、运行哪些命令、为什么），等 TA 切换模式，不要反复尝试。
- 工具结果写着“发起人拒绝”或“不能”时，说明这条路走不通：不要原样再试，换个办法（比如改用专用工具、缩小范围），或用 ask_user_question 问发起人想怎么做。
- 提问被取消（发起人点了停止）时，这一轮就结束了，不要接着往下做。
## 什么时候调 run_employee

- 发起人要做某个员工负责的工作时就调，不必先问“要不要开工”。员工需要的字段能从对话或文件推断的就先填上：统计周期可以从文件名里的周次、纪要标题或数据范围推断，推断出来的值照常填，发起人在表单上核对；缺的、推断不出的字段不要猜，不填就好（或先问一句），表单会让人补。
- 材料用 materials 给：会话里已有的材料写文件名，工作区目录里的文件写相对路径（先 list_files 看清有哪些）。只给确实要用的文件。
- 校验不过时工具会告诉你具体问题：改正后再调，或问发起人，不要原样重试。
- 表单被发起人点了“不是这个员工”时（工具结果写着“发起人拒绝，原因：选错员工”），重新判断该由哪个员工做，或问发起人想让谁做，不要再发同样的表单。
- 同一时间一个会话里最多一个未结束的任务。任务没结束时 run_employee 会被拒绝，把原因转告发起人；任务交付或终止后可以再运行一个员工（比如“再来一版”）。

## 你不能做什么

- 不能替人确认员工表单、勾选开工清单、通过或退回审核、重试、终止任务；这些都只能由对应角色在卡片上操作。对话里的文字不能直接触发任何状态变化，被要求时提醒对方在卡片上点击相应的按钮。
- 不能自己算数、改数：交付物里的数字都来自程序算出的事实。被问到数字时，查任务后转述，查不到就说查不到。
- 不能编造。不知道的事说不知道，不推测原因，不替员工承诺结果。
- shell 没有沙箱，能碰到工作区之外的东西，也能调用本程序的接口：只在用户明确需要时用，不要借它绕过上面任何一条限制（审核、重试、终止只有人能做，工作区之外和“交付”“材料”文件夹不能写）。
`

// employeeSketch 一个员工的输入定义（表单字段、材料、开工清单），写进系统提示词。
func employeeSketch(employee Employee, config *contract.EmployeeConfig, isDefault bool) []string {
	title := fmt.Sprintf("### %s（id：%s）", employee.Name, employee.ID)
	if isDefault {
		title += "　默认员工"
	}
	lines := []string{title, ""}
	if strings.TrimSpace(employee.Description) != "" {
		lines = append(lines, employee.Description, "")
	}
	properties, _ := config.FormSchema["properties"].(map[string]any)
	required := map[string]bool{}
	if list, ok := config.FormSchema["required"].([]any); ok {
		for _, raw := range list {
			if name, ok := raw.(string); ok {
				required[name] = true
			}
		}
	}
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	fields, files := []string{}, []string{}
	for _, name := range names {
		schema, _ := properties[name].(map[string]any)
		label, _ := schema["title"].(string)
		description, _ := schema["description"].(string)
		switch fieldWidget(config.FormUI, name) {
		case "hidden":
			continue
		case "file", "files":
			text := "  - " + name + "（" + label + "）"
			if accept := fileAccept(config.FormUI, name); accept != "" {
				text += "，接受 " + accept
			}
			files = append(files, text)
			continue
		}
		text := fmt.Sprintf("  - %s（%s，%s", name, label, schemaType(schema))
		if required[name] {
			text += "，必填"
		}
		text += "）"
		if description != "" {
			text += "：" + description
		}
		fields = append(fields, text)
	}
	if len(fields) > 0 {
		lines = append(lines, "run_employee 的 fields 可以填：")
		lines = append(lines, fields...)
	}
	if len(files) > 0 {
		lines = append(lines, "文件放进 materials（不要写进 fields）：")
		lines = append(lines, files...)
	}
	if len(config.Checklist) > 0 {
		lines = append(lines, "开工清单（程序按表单和材料判定，你不能勾选；必需项不齐不能开工，可跳过项由发起人在表单卡上确认跳过）：")
		for _, item := range config.Checklist {
			text := "  - " + item.Label
			if item.Required {
				text += "（必需）"
			} else {
				text += "（可跳过"
				if item.SkipLabel != "" {
					text += "：" + item.SkipLabel
				}
				text += "）"
			}
			lines = append(lines, text)
		}
	}
	if note := frontdeskNote(config.Frontdesk); note != "" {
		lines = append(lines, "", "给前台的说明：", "", note)
	}
	return append(lines, "")
}

// fileAccept 文件字段在表单界面配置里接受的扩展名。
func fileAccept(formUI map[string]any, field string) string {
	options, _ := formUI[field].(map[string]any)
	inner, _ := options["ui:options"].(map[string]any)
	accept, _ := inner["accept"].(string)
	return accept
}

func schemaType(schema map[string]any) string {
	switch value := schema["type"].(type) {
	case string:
		switch value {
		case "object":
			children, _ := schema["properties"].(map[string]any)
			names := make([]string, 0, len(children))
			for name := range children {
				names = append(names, name)
			}
			sort.Strings(names)
			return "对象 {" + strings.Join(names, ", ") + "}"
		case "integer":
			return "整数"
		case "number":
			return "数字"
		case "boolean":
			return "布尔"
		case "array":
			return "数组"
		}
		return "文本"
	}
	return "文本"
}

// frontdeskNote 员工配置里写给前台的说明：frontdesk.md 去掉“开放给你的只读工具”那一段。
func frontdeskNote(markdown string) string {
	lines := strings.Split(markdown, "\n")
	if start, end, ok := contract.FrontdeskToolsSection(lines); ok {
		lines = append(append([]string{}, lines[:start]...), lines[end:]...)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// agentSystem 通用对话 Agent 的系统提示词：固定的说明、每个员工的输入定义、这个会话的现状、权限模式。
func (loop *turnLoop) agentSystem(employees []Employee, configs map[string]*contract.EmployeeConfig) string {
	t := loop.turn
	lines := []string{strings.TrimSpace(generalPrompt), "", "## 可调用的员工", ""}
	if len(employees) == 0 {
		lines = append(lines, "（现在没有可用的员工）", "")
	}
	defaultID := loop.defaultEmployee(employees)
	for _, employee := range employees {
		if config := configs[employee.ID]; config != nil {
			lines = append(lines, employeeSketch(employee, config, employee.ID == defaultID)...)
		}
	}
	lines = append(lines, "## 当前会话", "", "- 正在和你说话的人："+contract.RoleLabels[t.role])
	if t.current.taskID == "" {
		lines = append(lines, "- 任务：还没有运行过员工")
	} else {
		// 会话里可能先后运行过多个任务：以最新的一个为当前任务回答问题
		lines = append(lines, "- 最新的任务："+t.current.taskID)
		if len(t.current.taskIDs) > 1 {
			lines = append(lines, fmt.Sprintf("- 这个会话已运行 %d 个任务，上面是最新的一个；更早的任务：%s",
				len(t.current.taskIDs), strings.Join(t.current.taskIDs[:len(t.current.taskIDs)-1], "、")))
		}
		if open := t.manager.openTask(t.current); open != nil {
			lines = append(lines, "- 这个任务还没结束（现在是“"+contract.StateLabels[open.State]+"”），不能再运行新的员工")
		} else {
			lines = append(lines, "- 最新的任务已经结束，要再来一版可以再调 run_employee")
		}
	}
	names := []string{}
	for _, attachment := range t.current.attachments {
		names = append(names, attachment.Name)
	}
	if len(names) > 0 {
		lines = append(lines, "- 会话里已有的材料："+strings.Join(names, "、"))
	} else {
		lines = append(lines, "- 会话里已有的材料：无")
	}
	lines = append(lines, "", permissionPrompt(t.current.mode()))
	return strings.Join(lines, "\n") + "\n"
}

// defaultEmployee 设置里的默认员工；没设或已不存在时返回空串。
func (loop *turnLoop) defaultEmployee(employees []Employee) string {
	deps := loop.turn.manager.deps
	if deps.DefaultEmployee == nil {
		return ""
	}
	id := deps.DefaultEmployee()
	for _, employee := range employees {
		if employee.ID == id {
			return id
		}
	}
	return ""
}

// agentTools 这一轮开放的工具：目录读写、run_employee、勾选了的只读工具（各员工勾选的并集）。
func (loop *turnLoop) agentTools(employees []Employee, configs map[string]*contract.EmployeeConfig) ([]tool, error) {
	t := loop.turn
	ticked := map[string]bool{}
	for _, config := range configs {
		for _, name := range contract.FrontdeskTools(config.Frontdesk) {
			ticked[name] = true
		}
	}
	tools := []tool{}
	if t.current.taskID != "" && ticked[contract.ToolQueryTask] {
		tools = append(tools, t.queryTaskTool())
	}
	tools = append(tools, loop.runEmployeeTool(employees, configs))
	tools = append(tools, loop.todoTool(), loop.askUserQuestionTool())
	knowledge, err := t.knowledgeTools(ticked)
	if err != nil {
		return nil, err
	}
	// 目录工具对所有会话开放：Agent 要能看见工作区里有什么
	tools = append(tools, t.folderTools()...)
	tools = append(tools, t.searchTools()...)
	tools = append(tools, t.fileWriteTools()...)
	tools = append(tools, loop.shellTool())
	return append(tools, knowledge...), nil
}

// unavailableText 模型不可用时程序自己说的话：任务的现状不需要模型也说得清。
func (loop *turnLoop) unavailableText() string {
	t := loop.turn
	text := "模型暂时不可用，我现在回答不了。可以直接在卡片上操作。"
	if t.current.taskID == "" {
		return text
	}
	snapshot, found := t.tasks.Get(t.current.taskID)
	if !found {
		return text
	}
	text += "任务现在的状态是“" + contract.StateLabels[snapshot.State] + "”。"
	if snapshot.Pause != nil {
		text += snapshot.Pause.Message
	}
	return text
}

// loadConfigs 读出每个员工的完整配置；读不了的员工这一轮不开放。
func (loop *turnLoop) loadConfigs(employees []Employee) ([]Employee, map[string]*contract.EmployeeConfig) {
	deps := loop.turn.manager.deps
	configs := map[string]*contract.EmployeeConfig{}
	usable := make([]Employee, 0, len(employees))
	for _, employee := range employees {
		if deps.LoadEmployee == nil {
			break
		}
		config, err := deps.LoadEmployee(employee.ID)
		if err != nil || config == nil {
			continue
		}
		configs[employee.ID] = config
		usable = append(usable, employee)
	}
	return usable, configs
}

// assist 一条回复：Agent 从头到尾用同一套提示词和工具。
func (loop *turnLoop) assist(ctx context.Context) turnOutcome {
	t := loop.turn
	listed, err := t.manager.deps.Employees()
	if err != nil {
		loop.say(Message{Kind: KindNotice, Text: "读不到员工列表：" + err.Error()})
		return turnOutcome{mode: turnErrored, reason: TurnError}
	}
	employees, configs := loop.loadConfigs(listed)
	tools, err := loop.agentTools(employees, configs)
	if err != nil {
		loop.say(Message{Kind: KindNotice, Text: "读不到工具列表：" + err.Error()})
		return turnOutcome{mode: turnErrored, reason: TurnError}
	}

	text, reason, err := loop.converse(ctx, loop.agentSystem(employees, configs), t.history(), tools)

	switch {
	case reason == TurnStopped:
		if text != "" {
			loop.say(Message{Kind: KindAssistant, Text: text})
		}
		return turnOutcome{mode: turnStopped, reason: TurnStopped}
	case reason == TurnError:
		loop.say(Message{Kind: KindNotice, Text: loop.unavailableText()})
		return turnOutcome{mode: turnErrored, reason: TurnError}
	case reason == TurnCompleted && text == "":
		// 什么都没说（比如只调了工具）：不用写空回复
	case err != nil:
		return turnOutcome{mode: turnErrored, reason: TurnError}
	default:
		if err := loop.write(Message{Kind: KindAssistant, Text: text}); err != nil {
			return turnOutcome{mode: turnErrored, reason: TurnError}
		}
	}
	// 卡片都放在一句话之后：先说话，再给卡片（比如“使用目录文件”）
	if err := loop.flushCards(); err != nil {
		return turnOutcome{mode: turnErrored, reason: TurnError}
	}
	return turnOutcome{mode: turnCompleted, reason: reason}
}

// speakerRole 现在和 Agent 说话的人：会话里最近一句人说的话的角色（说话的人可以在对话中途换角色）；没有人说过话时取 fallback。
// 以人在日志里留下的角色为准，不依赖这一轮登记时带的角色。
func speakerRole(current *record, fallback contract.Role) contract.Role {
	for index := len(current.messages) - 1; index >= 0; index-- {
		message := current.messages[index]
		if message.Kind == KindUser && message.Role != "" {
			return message.Role
		}
	}
	return fallback
}
