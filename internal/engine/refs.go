// 步骤入参里的 $ 引用解析（语法见 contract.StepConfig 与 docs/design.md）：
//
//	$form.<path>  $steps.<stepId>.<path>  $rendered.<stepId>
//	$task.dir  $task.attachments  $task.feedback  $task.idempotencyKey  $config.<path>
//
// 路径段 "[*]" 对数组逐项取值；引用不存在（含步骤尚未执行）解析为 nil；其余值原样传入。
// 移植自 packages/core/src/engine/refs.ts。
package engine

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// RefContext 引用解析上下文。
type RefContext struct {
	Form        map[string]any
	TaskDir     string
	Attachments any
	Feedback    any
	QCConfig    any
	// StepOutput 某步骤最近一次完成的输出；没有时返回 nil
	StepOutput func(stepID string) any
	// RenderedOutput 某步骤输出的报告草稿，已完成槽位渲染；没有时返回 nil
	RenderedOutput func(stepID string) any
	// IdempotencyKey 幂等键占位符；引擎在得到入参哈希后再替换成真正的键
	IdempotencyKey string
}

var refPattern = regexp.MustCompile(`^\$(form|steps|rendered|task|config)(\..+)?$`)
var pathPartPattern = regexp.MustCompile(`^([^\[\]]*)((\[[*0-9]+\])*)$`)
var bracketPattern = regexp.MustCompile(`\[(\*|[0-9]+)\]`)

// refToken 路径的一段：字段名或数组下标（"*" 表示逐项）。
type refToken struct {
	name    string
	index   int
	isIndex bool
	all     bool
}

func tokenize(path string) []refToken {
	tokens := []refToken{}
	for _, part := range strings.Split(path, ".") {
		match := pathPartPattern.FindStringSubmatch(part)
		if match == nil {
			tokens = append(tokens, refToken{name: part})
			continue
		}
		if match[1] != "" {
			tokens = append(tokens, refToken{name: match[1]})
		}
		for _, bracket := range bracketPattern.FindAllStringSubmatch(match[2], -1) {
			if bracket[1] == "*" {
				tokens = append(tokens, refToken{isIndex: true, all: true})
				continue
			}
			index, err := strconv.Atoi(bracket[1])
			if err != nil {
				index = 0
			}
			tokens = append(tokens, refToken{isIndex: true, index: index})
		}
	}
	return tokens
}

func walk(value any, tokens []refToken) any {
	if len(tokens) == 0 {
		if value == nil {
			return nil
		}
		return value
	}
	if value == nil {
		return nil
	}
	head := tokens[0]
	rest := tokens[1:]
	if !head.isIndex {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		return walk(object[head.name], rest)
	}
	array, ok := value.([]any)
	if !ok {
		return nil
	}
	if head.all {
		out := make([]any, 0, len(array))
		for _, item := range array {
			out = append(out, walk(item, rest))
		}
		return out
	}
	if head.index < 0 || head.index >= len(array) {
		return nil
	}
	return walk(array[head.index], rest)
}

func resolveRef(ref string, ctx RefContext) any {
	match := refPattern.FindStringSubmatch(ref)
	if match == nil {
		return ref
	}
	root := match[1]
	path := ""
	if match[2] != "" {
		path = match[2][1:]
	}
	tokens := []refToken{}
	if path != "" {
		tokens = tokenize(path)
	}
	switch root {
	case "form":
		return walk(ctx.Form, tokens)
	case "config":
		return walk(ctx.QCConfig, tokens)
	case "task":
		if len(tokens) == 0 {
			return nil
		}
		first := tokens[0]
		rest := tokens[1:]
		if first.isIndex {
			return nil
		}
		switch first.name {
		case "dir":
			return ctx.TaskDir
		case "attachments":
			return walk(ctx.Attachments, rest)
		case "feedback":
			return walk(ctx.Feedback, rest)
		case "idempotencyKey":
			return ctx.IdempotencyKey
		}
		return nil
	case "steps", "rendered":
		if len(tokens) == 0 || tokens[0].isIndex {
			return nil
		}
		stepID := tokens[0].name
		var output any
		if root == "steps" {
			output = ctx.StepOutput(stepID)
		} else {
			output = ctx.RenderedOutput(stepID)
		}
		return walk(output, tokens[1:])
	}
	return ref
}

// ResolveInput 解析步骤入参里的全部 $ 引用。
func ResolveInput(value any, ctx RefContext) any {
	switch typed := value.(type) {
	case string:
		return resolveRef(typed, ctx)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, ResolveInput(item, ctx))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = ResolveInput(item, ctx)
		}
		return out
	default:
		return value
	}
}

// ReplaceString 把入参里的字符串占位符整体替换为真值（只替换完全相等的字符串）。
func ReplaceString(value any, from, to string) any {
	switch typed := value.(type) {
	case string:
		if typed == from {
			return to
		}
		return typed
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, ReplaceString(item, from, to))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = ReplaceString(item, from, to)
		}
		return out
	default:
		return value
	}
}

// argsKey 入参的稳定序列化（键排序），用于比较两次工具调用的入参是否相同。
// Go 的 encoding/json 本来就按 key 排序，因此直接序列化即是稳定形式。
func argsKey(args any) string {
	if args == nil {
		return "null"
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(args); err != nil {
		return "null"
	}
	out := buffer.Bytes()
	for len(out) > 0 && (out[len(out)-1] == '\n' || out[len(out)-1] == '\r') {
		out = out[:len(out)-1]
	}
	return string(out)
}
