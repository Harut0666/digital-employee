package app

import "encoding/json"

// marshalIndentJSON 两空格缩进 + 末尾换行（与 TS 的 JSON.stringify(value, null, 2) + "\n" 一致）。
func marshalIndentJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
