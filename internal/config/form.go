// 表单字段用途（x-purpose）的读取与校验（docs/design.md §4.1）。
//
// 移植自 packages/core/src/config/form.ts 与 contract/form.ts：
//
//	writing_requirement  取值的标题 + 内容作为“发起人的写作要求”注入每个判断步骤
//	param:<参数名>       绑定到程序已支持的参数（见 contract.FormParams）
package config

import (
	"fmt"
	"strconv"
	"strings"

	"digital-employee/internal/contract"
)

// WritingRequirement 表单里的一条写作要求。
type WritingRequirement struct {
	// Field 字段路径，嵌套用点号，数组元素用 [i]
	Field string `json:"field"`
	Title string `json:"title"`
	Value any    `json:"value"`
	// Text 给模型看的单行文本，如“重点关注区域：华东”
	Text string `json:"text"`
}

// formParamNames 程序已支持的参数名（保持与 contract.FormParams 一致的顺序）。
var formParamNames = []string{"period", "allowMissingMinutes", "allowPartialPeriod"}

func isFormParamName(name string) bool {
	_, ok := contract.FormParams[name]
	return ok
}

const supportedPurposes = "写作要求用 " + contract.FormPurposeWritingRequirement +
	"，绑定参数用 " + contract.FormPurposeParamPrefix + "<参数名>；已支持的参数：" +
	"period、allowMissingMinutes、allowPartialPeriod"

// ParseFormPurpose 解析一个 x-purpose 取值。
// ok=false 时 message 是给人看的错误说明；purpose 为 "" 表示没有用途。
func ParseFormPurpose(value any) (purpose string, ok bool, message string) {
	if value == nil || value == "" {
		return "", true, ""
	}
	text, isString := value.(string)
	if !isString {
		return "", false, "x-purpose 必须是字符串（" + supportedPurposes + "）"
	}
	if text == contract.FormPurposeWritingRequirement {
		return contract.FormPurposeWritingRequirement, true, ""
	}
	if strings.HasPrefix(text, contract.FormPurposeParamPrefix) {
		param := strings.TrimPrefix(text, contract.FormPurposeParamPrefix)
		if !isFormParamName(param) {
			return "", false, fmt.Sprintf("x-purpose 绑定了程序不支持的参数 %q（%s）", param, supportedPurposes)
		}
		return text, true, ""
	}
	return "", false, fmt.Sprintf("无法识别的 x-purpose %q（%s）", text, supportedPurposes)
}

// isRecord 判断一个解析后的 JSON 值是不是对象。
func isRecord(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	return object, ok
}

// hasValue 空字符串、null、空数组、空对象都不算一条写作要求。
func hasValue(value any) bool {
	if value == nil {
		return false
	}
	if text, ok := value.(string); ok {
		return text != ""
	}
	switch typed := value.(type) {
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	}
	return true
}

// formatValue 把取值写成单行文本（与 TS 的 String(value) / JSON.stringify 对齐）。
func formatValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return formatJSONNumber(typed)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	}
	return stringify(value)
}

func formatJSONNumber(value float64) string {
	// 与 JS 的 String(number) 对齐：整数不带小数点。
	if value == float64(int64(value)) && value < 1e15 && value > -1e15 {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func titleOf(schema map[string]any, fallback string) string {
	if title, ok := schema["title"].(string); ok && strings.TrimSpace(title) != "" {
		return title
	}
	return fallback
}

func lastSegment(path string) string {
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '.' || r == '[' })
	parts = filterNonEmpty(parts)
	if len(parts) == 0 {
		return path
	}
	return parts[len(parts)-1]
}

func filterNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func writingPurpose(schema map[string]any) bool {
	purpose, ok, _ := ParseFormPurpose(schema["x-purpose"])
	return ok && purpose == contract.FormPurposeWritingRequirement
}

func collectFromSchema(schema map[string]any, value any, path string, out *[]WritingRequirement) {
	if !hasValue(value) {
		return
	}

	// 数组字段：x-purpose 既可以标在数组字段上（整组算一条），也可以标在 items 上（逐项算一条）
	if array, ok := value.([]any); ok {
		items, itemsIsRecord := isRecord(schema["items"])
		if writingPurpose(schema) {
			title := titleOf(schema, lastSegment(path))
			*out = append(*out, WritingRequirement{
				Field: path, Title: title, Value: value,
				Text: title + "：" + formatValue(value),
			})
			return
		}
		if itemsIsRecord {
			for index, item := range array {
				itemPath := fmt.Sprintf("%s[%d]", path, index)
				if writingPurpose(items) {
					if !hasValue(item) {
						continue
					}
					title := titleOf(items, lastSegment(path))
					*out = append(*out, WritingRequirement{
						Field: itemPath, Title: title, Value: item,
						Text: title + "：" + formatValue(item),
					})
					continue
				}
				collectFromSchema(items, item, itemPath, out)
			}
		}
		return
	}

	if writingPurpose(schema) {
		title := titleOf(schema, lastSegment(path))
		*out = append(*out, WritingRequirement{
			Field: path, Title: title, Value: value,
			Text: title + "：" + formatValue(value),
		})
	}

	properties, ok := isRecord(schema["properties"])
	if !ok {
		return
	}
	object, isObject := isRecord(value)
	if !isObject {
		return
	}
	// 按 schema 中 properties 的顺序遍历（Go 的 map 无序，这里按 key 排序会改变注入顺序，
	// 因此从 schema 的原始 JSON 顺序取不到顺序时退化为按名字排序，行为仍然稳定）。
	for _, key := range sortedKeys(properties) {
		child, childIsRecord := isRecord(properties[key])
		if !childIsRecord {
			continue
		}
		childPath := key
		if path != "" {
			childPath = path + "." + key
		}
		collectFromSchema(child, object[key], childPath, out)
	}
}

// CollectWritingRequirements 收集所有 x-purpose: writing_requirement 字段的标题和取值。
func CollectWritingRequirements(formSchema any, form map[string]any) []WritingRequirement {
	out := []WritingRequirement{}
	schema, ok := isRecord(formSchema)
	if !ok {
		return out
	}
	if form == nil {
		form = map[string]any{}
	}
	collectFromSchema(schema, form, "", &out)
	return out
}

func checkPurpose(schema any, path string, errors *[]string) {
	object, ok := isRecord(schema)
	if !ok {
		return
	}
	if _, present := object["x-purpose"]; !present {
		return
	}
	if _, ok, message := ParseFormPurpose(object["x-purpose"]); !ok {
		*errors = append(*errors, path+".x-purpose："+message)
	}
}

func validateProperties(schema any, path string, errors *[]string) {
	object, ok := isRecord(schema)
	if !ok {
		return
	}
	properties, ok := isRecord(object["properties"])
	if !ok {
		return
	}
	for _, key := range sortedKeys(properties) {
		child := properties[key]
		childPath := path + "." + key
		checkPurpose(child, childPath, errors)
		childObject, isObject := isRecord(child)
		if !isObject {
			continue
		}
		validateProperties(child, childPath+".properties", errors)
		if items, ok := isRecord(childObject["items"]); ok {
			checkPurpose(items, childPath+".items", errors)
			validateProperties(items, childPath+".items.properties", errors)
		}
	}
}

// ValidateFormSchemaPurposes 保存前的用途校验：返回逐条错误（空数组表示全部合法）。
func ValidateFormSchemaPurposes(formSchema any) []string {
	errors := []string{}
	if _, ok := isRecord(formSchema); !ok {
		return []string{"表单 Schema 必须是 JSON 对象"}
	}
	checkPurpose(formSchema, "", &errors)
	validateProperties(formSchema, "properties", &errors)
	return errors
}
