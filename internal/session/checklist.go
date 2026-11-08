// 开工清单：员工开工前必须备齐的事项，由人写在员工配置的 checklist.yaml 里，只有程序能勾选。
//
// 清单只判定“材料是否已提供、必填项是否已填写”；内容问题（周次不符、工作表缺失）留给引擎的准入校验，
// 它仍是最后一道关。必需项不齐时会话层拒绝开工；可跳过项由发起人确认跳过，开工时写入表单字段并留痕。
package session

import (
	"path/filepath"
	"strings"

	"digital-employee/internal/contract"
)

// 清单项的状态。
const (
	ItemDone    = "done"    // 已备齐
	ItemMissing = "missing" // 还缺
	ItemSkipped = "skipped" // 发起人确认跳过
)

// ErrChecklistIncomplete 必需项不齐，不能开工。
const ErrChecklistIncomplete = "checklist_incomplete"

// ChecklistEntry 清单的一项在会话里的样子。
type ChecklistEntry struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
	Status   string `json:"status"`
	// SkipLabel 可跳过项的跳过按钮说法
	SkipLabel string `json:"skipLabel,omitempty"`
}

// Checklist 会话的开工清单。
type Checklist struct {
	Items []ChecklistEntry `json:"items"`
	// Ready 必需项都已备齐，可以开工
	Ready bool `json:"ready"`
	// Missing 还缺的必需项名称
	Missing []string `json:"missing"`
}

// evaluateChecklist 按会话里的材料和表单判定清单。
func evaluateChecklist(items []contract.ChecklistItem, formSchema map[string]any, form map[string]any, attachments []Attachment, skipped map[string]bool) *Checklist {
	out := &Checklist{Items: []ChecklistEntry{}, Ready: true, Missing: []string{}}
	for _, item := range items {
		entry := ChecklistEntry{ID: item.ID, Label: item.Label, Required: item.Required, Status: ItemMissing}
		if !item.Required {
			entry.SkipLabel = item.SkipLabel
			if entry.SkipLabel == "" {
				entry.SkipLabel = "跳过"
			}
		}
		switch {
		case itemSatisfied(item, formSchema, form, attachments):
			entry.Status = ItemDone
		case !item.Required && skipped[item.ID]:
			entry.Status = ItemSkipped
		case item.Required:
			out.Ready = false
			out.Missing = append(out.Missing, item.Label)
		}
		out.Items = append(out.Items, entry)
	}
	return out
}

func itemSatisfied(item contract.ChecklistItem, formSchema map[string]any, form map[string]any, attachments []Attachment) bool {
	switch item.Source {
	case contract.ChecklistSourceMaterial, contract.ChecklistSourceUpload:
		// “已提供材料”：上传的和从工作区目录确认的都算，只看扩展名
		for _, attachment := range attachments {
			extension := strings.ToLower(filepath.Ext(attachment.Name))
			for _, accepted := range item.Accept {
				if extension == strings.ToLower(accepted) {
					return true
				}
			}
		}
	case contract.ChecklistSourceField:
		properties, _ := formSchema["properties"].(map[string]any)
		schema, _ := properties[item.Field].(map[string]any)
		return filled(form[item.Field], schema)
	}
	return false
}

// filled 字段是否已填写：空串、空数组、空对象都算没填；对象还要求 Schema 里的必填子字段都填了。
func filled(value any, schema map[string]any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		return len(typed) > 0
	case map[string]any:
		if len(typed) == 0 {
			return false
		}
		required, _ := schema["required"].([]any)
		properties, _ := schema["properties"].(map[string]any)
		for _, raw := range required {
			name, _ := raw.(string)
			child, _ := properties[name].(map[string]any)
			if !filled(typed[name], child) {
				return false
			}
		}
		return true
	}
	return true
}

// SkipItem 发起人在清单上确认跳过一项可跳过项。这是人的动作，Agent 没有对应的工具。
// 跳过写进会话日志留痕，开工时才把 skipField 写进表单。
func (m *Manager) SkipItem(id string, role contract.Role, itemID string) (*Reply, error) {
	if role != contract.RoleInitiator {
		return nil, failure(contract.ErrForbiddenRole, "只有发起人可以确认跳过清单项")
	}
	t, done, err := m.begin(id, role)
	if err != nil {
		return nil, err
	}
	defer done()
	if t.current.taskID != "" {
		return nil, failure(ErrSessionHasTask, "任务已经开始，清单不能再改")
	}
	if t.current.employee == "" {
		return nil, failure(ErrEmployeeMissing, "请先确认由哪个员工接手")
	}
	_, items, err := m.checklistOf(t.current, nil)
	if err != nil {
		return nil, err
	}
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
	if err := t.write(Message{Kind: kindChecklistSkipped, Item: item.ID}); err != nil {
		return nil, err
	}
	text := "发起人确认跳过开工清单的“" + item.Label + "”"
	if item.SkipLabel != "" {
		text += "（" + item.SkipLabel + "）"
	}
	if err := t.write(Message{Kind: KindNotice, Role: role, Text: text + "。"}); err != nil {
		return nil, err
	}
	return m.reply(t)
}

// applySkips 开工时把跳过的项写进表单字段。文件后来又传上了的项不算跳过。
func applySkips(form map[string]any, checklist *Checklist, items []contract.ChecklistItem) map[string]any {
	out := map[string]any{}
	for name, value := range form {
		out[name] = value
	}
	if checklist == nil {
		return out
	}
	for index, entry := range checklist.Items {
		if entry.Status == ItemSkipped && items[index].SkipField != "" {
			out[items[index].SkipField] = true
		}
	}
	return out
}

var entryText = map[string]string{ItemDone: "已备齐", ItemMissing: "还缺", ItemSkipped: "发起人已确认跳过"}

// checklistLines 给前台 Agent 看的清单现状，用来引导人补齐。勾选只由程序完成，Agent 没有勾选的工具。
func checklistLines(config *contract.EmployeeConfig, current *record) []string {
	if len(config.Checklist) == 0 {
		return nil
	}
	checklist := evaluateChecklist(config.Checklist, config.FormSchema, current.draft, current.attachments, current.skipped)
	lines := []string{"- 开工清单（程序按已提供的材料和表单判定，你不能勾选；必需项不齐不能开工，可跳过项要发起人在清单上确认跳过）："}
	for _, entry := range checklist.Items {
		text := "  - " + entry.Label + "：" + entryText[entry.Status]
		if entry.Status == ItemMissing {
			if entry.Required {
				text += "（必需）"
			} else {
				text += "（可跳过：" + entry.SkipLabel + "）"
			}
		}
		lines = append(lines, text)
	}
	return lines
}

// checklistOf 一个会话现在的清单；没有员工或员工没有清单时为 nil。form 为 nil 时用草稿。
func (m *Manager) checklistOf(current *record, form map[string]any) (*Checklist, []contract.ChecklistItem, error) {
	if current.employee == "" || m.deps.LoadEmployee == nil {
		return nil, nil, nil
	}
	config, err := m.deps.LoadEmployee(current.employee)
	if err != nil {
		return nil, nil, err
	}
	if len(config.Checklist) == 0 {
		return nil, nil, nil
	}
	if form == nil {
		form = current.draft
	}
	return evaluateChecklist(config.Checklist, config.FormSchema, form, current.attachments, current.skipped), config.Checklist, nil
}
