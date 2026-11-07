package contract

import (
	"regexp"
	"strings"
)

// frontdesk.md 末尾用一个人可读的段落列出开放给前台 Agent 的只读工具（配置页勾选后写回）：
//
//	## 开放给你的只读工具
//
//	- query_task：只读查询任务

// FrontdeskToolsHeading 只读工具段落的标题。
const FrontdeskToolsHeading = "开放给你的只读工具"

// FrontdeskReadonlyTools 前台 Agent 可以勾选的只读工具。
var FrontdeskReadonlyTools = []string{ToolQueryTask, ToolKBSearch, ToolKBRead}

// FrontdeskDefaultTools 段落缺失或没有勾选任何工具时开放的只读工具。知识库要人明确勾选才开放。
var FrontdeskDefaultTools = []string{ToolQueryTask}

// IsFrontdeskReadonlyTool 是不是前台 Agent 可以勾选的只读工具。
func IsFrontdeskReadonlyTool(name string) bool {
	for _, tool := range FrontdeskReadonlyTools {
		if tool == name {
			return true
		}
	}
	return false
}

func markdownHeading(line string) (string, bool) {
	trimmed := strings.TrimRight(strings.TrimSuffix(line, "\r"), " \t")
	if !strings.HasPrefix(trimmed, "## ") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")), true
}

// FrontdeskToolsSection 只读工具段落在 frontdesk.md 里的行范围 [start, end)，start 是标题行；没有段落时 ok 为 false。
func FrontdeskToolsSection(lines []string) (start, end int, ok bool) {
	start = -1
	for index, line := range lines {
		if heading, isHeading := markdownHeading(line); isHeading && heading == FrontdeskToolsHeading {
			start = index
			break
		}
	}
	if start < 0 {
		return 0, 0, false
	}
	end = len(lines)
	for index := start + 1; index < len(lines); index++ {
		if _, isHeading := markdownHeading(lines[index]); isHeading {
			end = index
			break
		}
	}
	return start, end, true
}

var frontdeskToolItem = regexp.MustCompile(`^\s*[-*]\s+([a-z_][a-z0-9_]*)`)

// FrontdeskTools frontdesk.md 里勾选的只读工具；没有勾选时用默认值。
func FrontdeskTools(markdown string) []string {
	lines := strings.Split(markdown, "\n")
	tools := []string{}
	if start, end, ok := FrontdeskToolsSection(lines); ok {
		for _, line := range lines[start+1 : end] {
			match := frontdeskToolItem.FindStringSubmatch(strings.TrimSuffix(line, "\r"))
			if match != nil && IsFrontdeskReadonlyTool(match[1]) {
				tools = append(tools, match[1])
			}
		}
	}
	if len(tools) == 0 {
		return append([]string{}, FrontdeskDefaultTools...)
	}
	return tools
}
