package agent

import (
	"bytes"
	"encoding/json"
	"math"
	"sort"
	"strings"
)

// marshalJSON 序列化（不转义 HTML，与 JSON.stringify 对齐）。
func marshalJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	out := buffer.Bytes()
	for len(out) > 0 && (out[len(out)-1] == '\n' || out[len(out)-1] == '\r') {
		out = out[:len(out)-1]
	}
	return out, nil
}

func unmarshalJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(target)
}

// MarshalJSONValue 把值序列化成紧凑 JSON 字符串。
func MarshalJSONValue(value any) string {
	raw, err := marshalJSON(value)
	if err != nil {
		return "null"
	}
	return string(raw)
}

// MarshalJSONIndent 把值序列化成 2 空格缩进的 JSON（对齐 JSON.stringify(value, null, 2)）。
func MarshalJSONIndent(value any) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return ""
	}
	return strings.TrimRight(buffer.String(), "\r\n")
}

// sortMapKeys 稳定遍历对象键。
func sortMapKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// jsonTypeName 返回一个值对应的 JSON Schema 类型名。
func jsonTypeName(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case json.Number:
		if _, err := typed.Int64(); err == nil {
			return "integer"
		}
		return "number"
	case int, int64, int32:
		return "integer"
	case float64:
		if typed == math.Trunc(typed) && !math.IsInf(typed, 0) {
			// JSON Schema 的 number 同时接受整数；integer 单独判断
			return "number"
		}
		return "number"
	default:
		return "unknown"
	}
}

// asNumber 把值转成 float64。
func asNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
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
	}
	return 0, false
}
