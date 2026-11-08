package contract

// 交付名模板（员工配置的 deliverableName，如 经营周报_{year}年第{week}周）。
// 下载接口按它给下载的文件起名；T42 写进工作区“交付”文件夹时也按它起名，两处共用这里的规则。

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// placeholderPattern 下载名模板里的 {字段}。
var placeholderPattern = regexp.MustCompile(`\{([^{}]+)\}`)

// unsafeNameChars 文件名里不能出现的字符：表单里的人填的字段不能变成路径。
var unsafeNameChars = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)

// scalarText 表单里的字符串或数字写进文件名时的样子。
func scalarText(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, strings.TrimSpace(typed) != ""
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	}
	return "", false
}

// formField 取表单字段：先看顶层，再看对象字段里的一层（{year} 能取到 period.year）。
func formField(form map[string]any, name string) (string, bool) {
	if text, ok := scalarText(form[name]); ok {
		return text, true
	}
	keys := make([]string, 0, len(form))
	for key := range form {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if nested, ok := form[key].(map[string]any); ok {
			if text, ok := scalarText(nested[name]); ok {
				return text, true
			}
		}
	}
	return "", false
}

// FillNameTemplate 用表单字段填模板；有字段填不上时返回 false，不交付残缺的名字。
func FillNameTemplate(template string, form map[string]any) (string, bool) {
	complete := true
	filled := placeholderPattern.ReplaceAllStringFunc(template, func(placeholder string) string {
		text, ok := formField(form, placeholder[1:len(placeholder)-1])
		complete = complete && ok
		return text
	})
	filled = strings.TrimRight(unsafeNameChars.ReplaceAllString(filled, "_"), ". ")
	return filled, complete && filled != ""
}
