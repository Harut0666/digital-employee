// 事件日志：每个任务一份 events.jsonl，追加写，每条落盘（fsync）。
// 移植自 packages/core/src/engine/event-log.ts。
package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"digital-employee/internal/contract"
)

// EventLogScan 一次扫描的结果。
type EventLogScan struct {
	Events []contract.TaskEvent
	// Corruption 中间行无法解析：日志损坏，错误说明；未损坏时为空
	Corruption string
	// TornFinalLine 末尾是无法解析的半行（进程在写入中途被杀）
	TornFinalLine bool
}

func scanLog(text, path string) EventLogScan {
	events := []contract.TaskEvent{}
	lines := strings.Split(text, "\n")
	last := len(lines) - 1
	for last >= 0 && lines[last] == "" {
		last--
	}
	for index := 0; index <= last; index++ {
		line := lines[index]
		if line == "" {
			continue
		}
		var event contract.TaskEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			if index != last {
				return EventLogScan{
					Events: events,
					Corruption: fmt.Sprintf("事件日志损坏：%s 第 %d 行无法解析，其后仍有内容。已保持文件不变，请人工检查。",
						path, index+1),
				}
			}
			return EventLogScan{Events: events, TornFinalLine: true}
		}
		events = append(events, event)
	}
	return EventLogScan{Events: events}
}

// ReadEventLog 读取日志。
//
// 进程在写入中途被杀时，最后一行可能只写了一半：只丢弃无法解析的“最后一行”，
// 并把文件整理成干净状态，保证后续追加不会接在半行后面。
// 中间的行无法解析不是写入中断，而是日志损坏：返回错误且不改动文件，避免丢掉其后的有效事件。
func ReadEventLog(path string) ([]contract.TaskEvent, error) {
	text, err := readFileMaybe(path)
	if err != nil {
		return nil, err
	}
	if text == nil {
		return nil, nil
	}
	scan := scanLog(string(text), path)
	if scan.Corruption != "" {
		return nil, fmt.Errorf("%s", scan.Corruption)
	}
	if scan.TornFinalLine || (len(text) > 0 && !bytes.HasSuffix(text, []byte("\n"))) {
		var builder bytes.Buffer
		for _, event := range scan.Events {
			line, err := MarshalEvent(event)
			if err != nil {
				return nil, err
			}
			builder.Write(line)
			builder.WriteByte('\n')
		}
		if err := os.WriteFile(path, builder.Bytes(), 0o644); err != nil {
			return nil, err
		}
		if err := syncFile(path); err != nil {
			return nil, err
		}
	}
	return scan.Events, nil
}

// ReadEventLogSalvage 尽力读取（只读，不整理文件）：遇到中间坏行就停在它之前，
// 返回已解析的事件与错误说明。供 List 在日志损坏时仍能给出任务的最后已知状态。
func ReadEventLogSalvage(path string) ([]contract.TaskEvent, string) {
	text, err := readFileMaybe(path)
	if err != nil || text == nil {
		return nil, ""
	}
	scan := scanLog(string(text), path)
	return scan.Events, scan.Corruption
}

// AppendEventLine 追加一条事件并落盘。
func AppendEventLine(path string, event contract.TaskEvent) error {
	line, err := MarshalEvent(event)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	buffer := append(line, '\n')
	if _, err := file.Write(buffer); err != nil {
		return err
	}
	return file.Sync()
}

// MarshalEvent 序列化一条事件（紧凑 JSON、不转义 HTML，与 JSON.stringify 对齐）。
func MarshalEvent(event contract.TaskEvent) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(event); err != nil {
		return nil, err
	}
	out := buffer.Bytes()
	for len(out) > 0 && (out[len(out)-1] == '\n' || out[len(out)-1] == '\r') {
		out = out[:len(out)-1]
	}
	return out, nil
}

func readFileMaybe(path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return content, nil
}

func syncFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
