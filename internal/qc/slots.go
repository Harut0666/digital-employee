// 槽位渲染（ADR-0002、contract/facts.ts）：把 {{path}} 替换为格式化后的事实值。
//
// 移植自 packages/core/src/qc/slots.ts。事实以 JSON 形状遍历（与 TS 一致）：
// WeeklyFacts 先转成 map，再按路径逐段取值——数组元素按业务键（code/category/id/key/name/store/product）匹配。
package qc

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"digital-employee/internal/contract"
)

// SlotPattern 匹配 {{path}}，path 内不含花括号，允许首尾空格。
var SlotPattern = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

// fieldAliases 字段别名：叙述里写 revenue，事实里叫 revenueCents。
var fieldAliases = map[string]string{"revenue": "revenueCents"}

// arrayKeys 数组元素的业务键，按顺序尝试。
var arrayKeys = []string{"code", "category", "id", "key", "name", "store", "product"}

// ResolvedSlot 一条解析成功的槽位。
type ResolvedSlot struct {
	Format contract.SlotFormat
	// Value 事实上的原始值（金额为分，比例为小数）；nil 表示事实存在但不可得
	Value *float64
	// Field 路径最后一段，用于区分环比与其他占比
	Field string
}

// Slots 绑定一份事实的槽位渲染器：同一份事实渲染多段文本时只转换一次。
type Slots struct {
	facts map[string]any
}

// NewSlots 以一份事实构造渲染器。
func NewSlots(facts contract.WeeklyFacts) *Slots {
	return &Slots{facts: FactsMap(facts)}
}

// NewSlotsFromMap 以已经是 JSON 形状的事实构造渲染器。
func NewSlotsFromMap(facts map[string]any) *Slots {
	if facts == nil {
		facts = map[string]any{}
	}
	return &Slots{facts: facts}
}

// FactsMap 把 WeeklyFacts 转成 JSON 形状的 map（键名即 json tag）。
func FactsMap(facts contract.WeeklyFacts) map[string]any {
	raw, err := json.Marshal(facts)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

// RenderSlots 把文本里的 {{path}} 替换为格式化后的事实值；Unresolved 为无法解析的槽位。
func RenderSlots(text string, facts contract.WeeklyFacts) contract.SlotRenderResult {
	return NewSlots(facts).Render(text)
}

// Render 渲染一段文本。
func (s *Slots) Render(text string) contract.SlotRenderResult {
	unresolved := []string{}
	seen := map[string]bool{}
	rendered := SlotPattern.ReplaceAllStringFunc(text, func(whole string) string {
		match := SlotPattern.FindStringSubmatch(whole)
		if match == nil {
			return whole
		}
		path := strings.TrimSpace(match[1])
		slot, ok := resolveSlotIn(s.facts, path)
		if !ok {
			if !seen[path] {
				seen[path] = true
				unresolved = append(unresolved, path)
			}
			return whole
		}
		return formatSlot(slot)
	})
	return contract.SlotRenderResult{Text: rendered, Unresolved: unresolved}
}

// ExtractSlotPaths 找出文本中的所有槽位路径（按出现顺序，含重复）。
func ExtractSlotPaths(text string) []string {
	matches := SlotPattern.FindAllStringSubmatch(text, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, strings.TrimSpace(match[1]))
	}
	return out
}

// ResolveSlot 按路径在事实上取值；无法解析返回 false。
func ResolveSlot(path string, facts contract.WeeklyFacts) (ResolvedSlot, bool) {
	return resolveSlotIn(FactsMap(facts), path)
}

func resolveSlotIn(facts map[string]any, path string) (ResolvedSlot, bool) {
	segments := strings.Split(path, ".")
	if len(segments) < 2 {
		return ResolvedSlot{}, false
	}
	last := segments[len(segments)-1]
	field := last
	if alias, ok := fieldAliases[last]; ok {
		field = alias
	}
	format, ok := contract.SlotFormats[last]
	if !ok {
		format, ok = contract.SlotFormats[field]
	}
	if !ok {
		return ResolvedSlot{}, false
	}

	var node any = facts
	for _, segment := range segments[:len(segments)-1] {
		next, ok := stepInto(node, segment)
		if !ok {
			return ResolvedSlot{}, false
		}
		node = next
	}
	object, ok := node.(map[string]any)
	if !ok {
		return ResolvedSlot{}, false
	}
	value, present := object[field]
	if !present {
		return ResolvedSlot{}, false
	}
	if value == nil {
		return ResolvedSlot{Format: format, Value: nil, Field: field}, true
	}
	number, ok := toNumber(value)
	if !ok {
		return ResolvedSlot{}, false
	}
	return ResolvedSlot{Format: format, Value: &number, Field: field}, true
}

// stepInto 走一层路径：数组按业务键找元素，对象按键取值。
func stepInto(node any, segment string) (any, bool) {
	switch typed := node.(type) {
	case []any:
		for _, item := range typed {
			object, ok := item.(map[string]any)
			if !ok {
				continue
			}
			for _, key := range arrayKeys {
				if value, present := object[key]; present {
					if text, isString := value.(string); isString && text == segment {
						return item, true
					}
				}
			}
		}
		return nil, false
	case map[string]any:
		value, present := typed[segment]
		if !present {
			return nil, false
		}
		return value, true
	default:
		return nil, false
	}
}

func toNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return 0, false
		}
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		return number, true
	default:
		return 0, false
	}
}

// roundHalfAway 四舍五入（远离零），先收敛浮点误差（对齐 TS 的 toPrecision(12)），
// 避免 0.1235*1000 这类边界值被舍错。
func roundHalfAway(x float64) float64 {
	if x == 0 {
		return 0
	}
	abs := math.Abs(x)
	// JS: Number(Math.abs(x).toPrecision(12))
	precision := 12
	text := strconv.FormatFloat(abs, 'g', precision, 64)
	if collapsed, err := strconv.ParseFloat(text, 64); err == nil {
		abs = collapsed
	}
	return math.Copysign(math.Floor(abs+0.5), x)
}

func groupThousands(n int64) string {
	digits := strconv.FormatInt(n, 10)
	negative := strings.HasPrefix(digits, "-")
	if negative {
		digits = digits[1:]
	}
	var builder strings.Builder
	if negative {
		builder.WriteByte('-')
	}
	for index, char := range digits {
		if index > 0 && (len(digits)-index)%3 == 0 {
			builder.WriteByte(',')
		}
		builder.WriteRune(char)
	}
	return builder.String()
}

// DisplayValue 展示用数值：金额为元（取整），比例为百分数（一位小数），计数为整数。
// 供 number_range 规则按展示口径比较。
func DisplayValue(slot ResolvedSlot) *float64 {
	if slot.Value == nil {
		return nil
	}
	var shown float64
	switch slot.Format {
	case contract.SlotYuan:
		shown = roundHalfAway(*slot.Value / 100)
	case contract.SlotPercent1:
		shown = roundHalfAway(*slot.Value*1000) / 10
	case contract.SlotCount:
		shown = math.Floor(*slot.Value + 0.5)
	}
	return &shown
}

func formatSlot(slot ResolvedSlot) string {
	if slot.Value == nil {
		if slot.Field == "wow" {
			return "不可比（无上周数据）"
		}
		return "暂无"
	}
	shown := 0.0
	if displayed := DisplayValue(slot); displayed != nil {
		shown = *displayed
	}
	sign := ""
	if shown < 0 {
		sign = "-"
	}
	switch slot.Format {
	case contract.SlotYuan:
		return fmt.Sprintf("%s%s 元", sign, groupThousands(int64(math.Abs(shown))))
	case contract.SlotPercent1:
		// 只有环比是“变化量”，正数带加号；占比类（毛利率、达成率…）不带。
		plus := ""
		if slot.Field == "wow" && shown > 0 {
			plus = "+"
		}
		return fmt.Sprintf("%s%s%.1f%%", plus, sign, math.Abs(shown))
	case contract.SlotCount:
		return fmt.Sprintf("%s%s", sign, groupThousands(int64(math.Abs(shown))))
	}
	return ""
}
