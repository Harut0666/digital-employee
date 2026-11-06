// de replay：读取 <workspace>/tasks/<taskId>/events.jsonl，按 seq 顺序逐行输出。
// 移植自 packages/cli/src/replay.ts。
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"digital-employee/internal/contract"
)

// EventsPath 任务事件日志路径。
func EventsPath(workspace, taskID string) string {
	return filepath.Join(workspace, "tasks", taskID, "events.jsonl")
}

// ReadTaskEvents 读事件日志；崩溃时可能留下写了一半的最后一行，解析失败的行直接跳过。
func ReadTaskEvents(workspace, taskID string) ([]contract.TaskEvent, error) {
	file := EventsPath(workspace, taskID)
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("找不到任务 %s 的事件日志：%s", taskID, file)
	}
	events := []contract.TaskEvent{}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event contract.TaskEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		events = append(events, event)
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Seq < events[j].Seq })
	return events, nil
}

func derefInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func derefBool(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

// Summarize 一条事件的一行摘要。
func Summarize(event contract.TaskEvent) string {
	switch event.Type {
	case contract.EventTaskCreated:
		hash := event.ConfigHash
		if len(hash) > 8 {
			hash = hash[:8]
		}
		return fmt.Sprintf("员工 %s，配置 %s，附件 %d 个", event.Employee, hash, len(event.Attachments))
	case contract.EventFormUpdated:
		return fmt.Sprintf("表单更新（%s），附件 %d 个", event.Actor, len(event.Attachments))
	case contract.EventStateChanged:
		pause := ""
		if event.Pause != nil {
			pause = "，暂停原因 " + string(event.Pause.Code)
		}
		return fmt.Sprintf("%s -> %s（%s，%s%s）", event.From, event.To, event.Trigger, event.Actor, pause)
	case contract.EventTransitionRejected:
		return fmt.Sprintf("拒绝 %s：状态 %s，角色 %s，原因 %s", event.Trigger, event.From, event.Actor, event.Reason)
	case contract.EventStepStarted:
		return fmt.Sprintf("%s（第 %d 次尝试，重做轮次 %d）", event.StepID, derefInt(event.Attempt), derefInt(event.ReworkRound))
	case contract.EventStepCompleted:
		return fmt.Sprintf("%s -> %s", event.StepID, event.OutputPath)
	case contract.EventStepFailed:
		return fmt.Sprintf("%s：%s", event.StepID, event.Error)
	case contract.EventToolCalled:
		return fmt.Sprintf("%s 调用 %s（第 %d 次，callId %s）", event.StepID, event.Tool, derefInt(event.Attempt), event.CallID)
	case contract.EventToolResult:
		if derefBool(event.OK, false) {
			return fmt.Sprintf("callId %s 成功", event.CallID)
		}
		return fmt.Sprintf("callId %s 失败：%s", event.CallID, event.Error)
	case contract.EventToolDenied:
		return fmt.Sprintf("%s 调用 %s 被拒绝：%s", event.StepID, event.Tool, event.Reason)
	case contract.EventModelCalled:
		duration := int64(0)
		if event.DurationMS != nil {
			duration = *event.DurationMS
		}
		return fmt.Sprintf("%s 第 %d 轮，用时 %dms", event.StepID, derefInt(event.Turn), duration)
	case contract.EventQCResult:
		status := "未通过"
		if derefBool(event.Passed, false) {
			status = "通过"
		}
		return fmt.Sprintf("第 %d 次质检%s，共 %d 项", derefInt(event.Attempt), status, len(event.Checks))
	case contract.EventReviewDecision:
		label := "退回"
		if event.Decision == "approve" {
			label = "通过"
		}
		if event.Comment != "" {
			return label + "：" + event.Comment
		}
		return label
	case contract.EventDeliverableCreated:
		return fmt.Sprintf("%s %s", event.Kind, event.Path)
	case contract.EventFaultInjected:
		return fmt.Sprintf("%s %s", event.Target, event.Mode)
	default:
		data, _ := json.Marshal(event)
		return string(data)
	}
}

// FormatEvent 一行回放输出。
func FormatEvent(event contract.TaskEvent) string {
	return fmt.Sprintf("#%03d  %s  %-20s  %s", event.Seq, event.TS, event.Type, Summarize(event))
}

// ReplayLines 回放一个任务的事件行。
func ReplayLines(workspace, taskID string) ([]string, error) {
	events, err := ReadTaskEvents(workspace, taskID)
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0, len(events))
	for _, event := range events {
		lines = append(lines, FormatEvent(event))
	}
	return lines, nil
}
