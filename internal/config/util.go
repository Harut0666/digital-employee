package config

import (
	"bytes"
	"encoding/json"
	"sort"
)

// sortedKeys 返回对象键的稳定顺序（Go 的 map 无序，遍历时必须显式排序）。
// TS 版按 form.schema.json 里的书写顺序注入写作要求；JSON 解析到 map 后顺序丢失，
// 这里按名字排序，保证结果稳定可重放。
func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// stringify 把任意值写成紧凑 JSON（对齐 TS 的 JSON.stringify）。
func stringify(value any) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return ""
	}
	out := buffer.Bytes()
	// Encoder.Encode 会追加换行
	for len(out) > 0 && (out[len(out)-1] == '\n' || out[len(out)-1] == '\r') {
		out = out[:len(out)-1]
	}
	return string(out)
}
