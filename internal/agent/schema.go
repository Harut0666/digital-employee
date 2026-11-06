// 一个够用的 JSON Schema 校验器（对齐 TS 版的 Ajv：draft-07 常用关键字 + anyOf/oneOf/allOf）。
//
// 为什么自己写：go.mod 不允许加依赖，而员工配置里的输出 Schema 只用到少量关键字。
// 错误文案尽量对齐 Ajv 的 errorsText（dataVar = "output"），便于与 TS 版比对失败信息。
package agent

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Validator 已编译的输出 Schema 校验器。
type Validator struct {
	schema map[string]any
	// patterns 预编译的 pattern（键是 schema 里的 pattern 字符串）
	patterns map[string]*regexp.Regexp
	err      error
}

// NewValidator 编译一个 JSON Schema。编译失败时 Validate 返回编译错误。
func NewValidator(schema map[string]any) *Validator {
	validator := &Validator{schema: schema, patterns: map[string]*regexp.Regexp{}}
	collectPatterns(schema, validator)
	return validator
}

// CompileError 返回 Schema 编译错误（nil 表示可以校验）。
func (v *Validator) CompileError() error { return v.err }

func collectPatterns(node any, validator *Validator) {
	switch typed := node.(type) {
	case map[string]any:
		if pattern, ok := typed["pattern"].(string); ok {
			compiled, err := regexp.Compile(pattern)
			if err != nil {
				validator.err = fmt.Errorf("pattern %q 无法编译：%v", pattern, err)
				return
			}
			validator.patterns[pattern] = compiled
		}
		for _, key := range sortMapKeys(typed) {
			collectPatterns(typed[key], validator)
		}
	case []any:
		for _, item := range typed {
			collectPatterns(item, validator)
		}
	}
}

// Validate 校验一个值；返回 Ajv 风格的错误列表（空表示通过）。
func (v *Validator) Validate(value any) []string {
	if v.err != nil {
		return []string{v.err.Error()}
	}
	errors := []string{}
	validateNode(v.schema, value, "output", &errors, v)
	return errors
}

// ErrorText 把错误列表拼成一行（对齐 Ajv 的 errorsText）。
func ErrorText(errors []string) string {
	return strings.Join(errors, ", ")
}

func validateNode(schema map[string]any, value any, path string, errors *[]string, validator *Validator) {
	if schema == nil {
		return
	}

	// 组合关键字
	if anyOf, ok := schema["anyOf"].([]any); ok {
		matched := false
		for _, candidate := range anyOf {
			sub, ok := candidate.(map[string]any)
			if !ok {
				continue
			}
			subErrors := []string{}
			validateNode(sub, value, path, &subErrors, validator)
			if len(subErrors) == 0 {
				matched = true
				break
			}
		}
		if !matched {
			*errors = append(*errors, path+" must match a schema in anyOf")
		}
	}
	if oneOf, ok := schema["oneOf"].([]any); ok {
		matched := 0
		for _, candidate := range oneOf {
			sub, ok := candidate.(map[string]any)
			if !ok {
				continue
			}
			subErrors := []string{}
			validateNode(sub, value, path, &subErrors, validator)
			if len(subErrors) == 0 {
				matched++
			}
		}
		if matched != 1 {
			*errors = append(*errors, path+" must match exactly one schema in oneOf")
		}
	}
	if allOf, ok := schema["allOf"].([]any); ok {
		for _, candidate := range allOf {
			if sub, ok := candidate.(map[string]any); ok {
				validateNode(sub, value, path, errors, validator)
			}
		}
	}
	if not, ok := schema["not"].(map[string]any); ok {
		subErrors := []string{}
		validateNode(not, value, path, &subErrors, validator)
		if len(subErrors) == 0 {
			*errors = append(*errors, path+" must NOT be valid")
		}
	}

	// type
	if rawType, present := schema["type"]; present {
		types := []string{}
		switch typed := rawType.(type) {
		case string:
			types = append(types, typed)
		case []any:
			for _, item := range typed {
				if text, ok := item.(string); ok {
					types = append(types, text)
				}
			}
		}
		if len(types) > 0 && !matchesType(types, value) {
			expected := types[0]
			if len(types) > 1 {
				expected = strings.Join(types, ",")
			}
			*errors = append(*errors, path+" must be "+expected)
			return
		}
	}

	// enum / const
	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, candidate := range enum {
			if jsonEqual(candidate, value) {
				found = true
				break
			}
		}
		if !found {
			*errors = append(*errors, path+" must be equal to one of the allowed values")
		}
	}
	if constant, present := schema["const"]; present {
		if !jsonEqual(constant, value) {
			*errors = append(*errors, path+" must be equal to constant")
		}
	}

	switch typed := value.(type) {
	case map[string]any:
		validateObject(schema, typed, path, errors, validator)
	case []any:
		validateArray(schema, typed, path, errors, validator)
	case string:
		validateString(schema, typed, path, errors, validator)
	default:
		validateNumber(schema, value, path, errors)
	}
	if value == nil {
		return
	}
}

func matchesType(types []string, value any) bool {
	actual := jsonTypeName(value)
	if actual == "integer" && contains(types, "number") {
		return true
	}
	if actual == "number" && contains(types, "integer") {
		if number, ok := asNumber(value); ok && number == math.Trunc(number) {
			return true
		}
	}
	for _, want := range types {
		if want == actual {
			return true
		}
	}
	return false
}

func validateObject(schema map[string]any, value map[string]any, path string, errors *[]string, validator *Validator) {
	if required, ok := schema["required"].([]any); ok {
		for _, item := range required {
			key, ok := item.(string)
			if !ok {
				continue
			}
			if _, present := value[key]; !present {
				*errors = append(*errors, path+" must have required property '"+key+"'")
			}
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	if properties != nil {
		for _, key := range sortMapKeys(properties) {
			sub, ok := properties[key].(map[string]any)
			if !ok {
				continue
			}
			child, present := value[key]
			if !present {
				continue
			}
			validateNode(sub, child, childPath(path, key), errors, validator)
		}
	}
	if additional, present := schema["additionalProperties"]; present {
		if allow, ok := additional.(bool); ok && !allow {
			for _, key := range sortMapKeys(value) {
				if properties != nil {
					if _, known := properties[key]; known {
						continue
					}
				}
				*errors = append(*errors, path+" must NOT have additional properties")
				break
			}
		} else if sub, ok := additional.(map[string]any); ok {
			for _, key := range sortMapKeys(value) {
				if properties != nil {
					if _, known := properties[key]; known {
						continue
					}
				}
				validateNode(sub, value[key], childPath(path, key), errors, validator)
			}
		}
	}
	// 对象属性个数（draft-07 的 minProperties/maxProperties）
	if raw, present := schema["minProperties"]; present {
		if limit, ok := asNumber(raw); ok && float64(len(value)) < limit {
			*errors = append(*errors, fmt.Sprintf("%s must NOT have fewer than %d properties", path, int(limit)))
		}
	}
	if raw, present := schema["maxProperties"]; present {
		if limit, ok := asNumber(raw); ok && float64(len(value)) > limit {
			*errors = append(*errors, fmt.Sprintf("%s must NOT have more than %d properties", path, int(limit)))
		}
	}
}

func validateArray(schema map[string]any, value []any, path string, errors *[]string, validator *Validator) {
	if raw, present := schema["minItems"]; present {
		if limit, ok := asNumber(raw); ok && float64(len(value)) < limit {
			*errors = append(*errors, fmt.Sprintf("%s must NOT have fewer than %d items", path, int(limit)))
		}
	}
	if raw, present := schema["maxItems"]; present {
		if limit, ok := asNumber(raw); ok && float64(len(value)) > limit {
			*errors = append(*errors, fmt.Sprintf("%s must NOT have more than %d items", path, int(limit)))
		}
	}
	if unique, ok := schema["uniqueItems"].(bool); ok && unique {
		for i := 0; i < len(value); i++ {
			for j := i + 1; j < len(value); j++ {
				if jsonEqual(value[i], value[j]) {
					*errors = append(*errors, path+" must NOT have duplicate items")
					i = len(value)
					break
				}
			}
		}
	}
	switch items := schema["items"].(type) {
	case map[string]any:
		for index, item := range value {
			validateNode(items, item, fmt.Sprintf("%s/%d", path, index), errors, validator)
		}
	case []any:
		for index, raw := range items {
			if index >= len(value) {
				break
			}
			if sub, ok := raw.(map[string]any); ok {
				validateNode(sub, value[index], fmt.Sprintf("%s/%d", path, index), errors, validator)
			}
		}
	}
}

func validateString(schema map[string]any, value string, path string, errors *[]string, validator *Validator) {
	length := float64(len([]rune(value)))
	if raw, present := schema["minLength"]; present {
		if limit, ok := asNumber(raw); ok && length < limit {
			*errors = append(*errors, fmt.Sprintf("%s must NOT have fewer than %d characters", path, int(limit)))
		}
	}
	if raw, present := schema["maxLength"]; present {
		if limit, ok := asNumber(raw); ok && length > limit {
			*errors = append(*errors, fmt.Sprintf("%s must NOT have more than %d characters", path, int(limit)))
		}
	}
	if pattern, ok := schema["pattern"].(string); ok {
		compiled := validator.patterns[pattern]
		if compiled == nil {
			if c, err := regexp.Compile(pattern); err == nil {
				compiled = c
			}
		}
		if compiled != nil && !compiled.MatchString(value) {
			*errors = append(*errors, path+" must match pattern "+fmt.Sprintf("%q", pattern))
		}
	}
}

func validateNumber(schema map[string]any, value any, path string, errors *[]string) {
	number, ok := asNumber(value)
	if !ok {
		return
	}
	if raw, present := schema["minimum"]; present {
		if limit, ok := asNumber(raw); ok && number < limit {
			*errors = append(*errors, fmt.Sprintf("%s must be >= %s", path, formatLimit(limit)))
		}
	}
	if raw, present := schema["maximum"]; present {
		if limit, ok := asNumber(raw); ok && number > limit {
			*errors = append(*errors, fmt.Sprintf("%s must be <= %s", path, formatLimit(limit)))
		}
	}
	if raw, present := schema["exclusiveMinimum"]; present {
		if limit, ok := asNumber(raw); ok && number <= limit {
			*errors = append(*errors, fmt.Sprintf("%s must be > %s", path, formatLimit(limit)))
		}
	}
	if raw, present := schema["exclusiveMaximum"]; present {
		if limit, ok := asNumber(raw); ok && number >= limit {
			*errors = append(*errors, fmt.Sprintf("%s must be < %s", path, formatLimit(limit)))
		}
	}
	if raw, present := schema["multipleOf"]; present {
		if limit, ok := asNumber(raw); ok && limit != 0 {
			quotient := number / limit
			if math.Abs(quotient-math.Round(quotient)) > 1e-9 {
				*errors = append(*errors, path+" must be multiple of "+formatLimit(limit))
			}
		}
	}
}

func formatLimit(value float64) string {
	if value == math.Trunc(value) && math.Abs(value) < 1e15 {
		return fmt.Sprintf("%d", int64(value))
	}
	return fmt.Sprintf("%v", value)
}

func childPath(parent, key string) string {
	return parent + "/" + key
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// jsonEqual 比较两个 JSON 值是否相等（数字按数值比较）。
func jsonEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if left, ok := asNumber(a); ok {
		if right, ok := asNumber(b); ok {
			return left == right
		}
		return false
	}
	leftJSON, errLeft := marshalJSON(a)
	rightJSON, errRight := marshalJSON(b)
	if errLeft != nil || errRight != nil {
		return false
	}
	return string(leftJSON) == string(rightJSON)
}
