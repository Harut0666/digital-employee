// 判断步骤的模型输出增量：只走任务事件流，不写进事件日志（Spec 0006“员工步骤在对话中的形式”、T43）。
//
// 增量是给人“边输出边看”的，不是任务状态的一部分：它没有 seq、不落盘、重连后也不会补发。
package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
)

// addDeltas 给某个判断步骤的第一轮加上分片文字：流式调用时按片推送增量。
func addDeltas(t *testing.T, script agent.ModelScript, stepID string, pieces ...string) {
	t.Helper()
	turns := script[stepID]
	if len(turns) == 0 {
		t.Fatalf("剧本里没有步骤 %s", stepID)
	}
	text := strings.Join(pieces, "")
	turns[0].Text = &text
	turns[0].Chunks = append([]string{}, pieces...)
	script[stepID] = turns
}

// taskStream 后台读一段任务事件流；帧以空行分隔。
type taskStream struct {
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	frames []string
}

// openTaskStream 订阅任务事件流（先建好连接，再让任务跑起来）。
func (h *harness) openTaskStream(taskID string) *taskStream {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.baseURL+"/api/tasks/"+taskID+"/stream?after=0", nil)
	if err != nil {
		cancel()
		h.t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		cancel()
		h.t.Fatalf("订阅任务事件流失败：%v", err)
	}
	stream := &taskStream{cancel: func() { cancel(); response.Body.Close() }, done: make(chan struct{})}
	go func() {
		defer close(stream.done)
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		current := []string{}
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if len(current) > 0 {
					stream.mu.Lock()
					stream.frames = append(stream.frames, strings.Join(current, "\n"))
					stream.mu.Unlock()
					current = nil
				}
				continue
			}
			current = append(current, line)
		}
	}()
	return stream
}

// close 结束读取。
func (s *taskStream) close() {
	s.cancel()
	<-s.done
}

// framesContaining 已经收到的、包含某段文字的帧。
func (s *taskStream) framesContaining(text string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for _, frame := range s.frames {
		if strings.Contains(frame, text) {
			out = append(out, frame)
		}
	}
	return out
}

// waitUntil 反复检查直到条件成立（超时视为失败）。
func (s *taskStream) waitUntil(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("15 秒内没等到：%s；已收到 %d 帧", what, len(s.framesContaining("")))
}

// deltaFrame 一条 model_delta 帧里的数据。
type deltaFrame struct {
	Type   string `json:"type"`
	StepID string `json:"stepId"`
	Turn   *int   `json:"turn"`
	Text   string `json:"text"`
	Seq    int64  `json:"seq"`
}

// parseDeltaFrame 校验 SSE 帧形状并取出增量数据。
func parseDeltaFrame(t *testing.T, frame string) deltaFrame {
	t.Helper()
	lines := strings.Split(frame, "\n")
	if lines[0] != "event: model_delta" {
		t.Fatalf("增量帧应以具名事件开头，实际 %q", frame)
	}
	// 不落盘的增量没有 id：重连时不能被当成“已经看过”
	for _, line := range lines {
		if strings.HasPrefix(line, "id: ") {
			t.Fatalf("增量帧不该带 id：%q", frame)
		}
	}
	if len(lines) < 2 || !strings.HasPrefix(lines[1], "data: ") {
		t.Fatalf("增量帧缺少 data：%q", frame)
	}
	var data deltaFrame
	if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &data); err != nil {
		t.Fatalf("增量帧不是合法 JSON：%v\n%s", err, frame)
	}
	return data
}

func TestJudgmentModelDeltasReachTheTaskStreamWithoutBeingPersisted(t *testing.T) {
	script := caseScript(t, "A-01")
	addDeltas(t, script, "match_causes", "先看", "原因")
	addDeltas(t, script, "write_report", "正在", "撰写")
	h := newHarness(t, script, withScriptedModel())
	task := h.createTask(a01Files(t))

	stream := h.openTaskStream(task.ID)
	defer stream.close()

	expectState(t, h.command(task.ID, initiator, cmd(contract.CmdSubmit)), contract.StateAwaitingReview)

	// 增量按片推送：等到两个判断步骤的文字都收齐（拼起来等于剧本里写的输出）
	streamed := func() map[string]string {
		texts := map[string]string{}
		for _, frame := range stream.framesContaining("event: model_delta") {
			data := parseDeltaFrame(t, frame)
			texts[data.StepID] += data.Text
		}
		return texts
	}
	stream.waitUntil(t, "两个判断步骤的增量推完", func() bool {
		texts := streamed()
		return texts["match_causes"] == "先看原因" && texts["write_report"] == "正在撰写"
	})

	frames := stream.framesContaining("event: model_delta")
	if len(frames) == 0 {
		t.Fatal("任务事件流里没有判断步骤的模型输出增量")
	}
	texts := map[string]string{}
	steps := map[string]bool{}
	for _, frame := range frames {
		data := parseDeltaFrame(t, frame)
		if data.Type != contract.EventModelDelta {
			t.Fatalf("事件类型 = %q，want %q", data.Type, contract.EventModelDelta)
		}
		if data.Seq != 0 {
			t.Fatalf("增量不该有 seq，实际 %d", data.Seq)
		}
		steps[data.StepID] = true
		texts[data.StepID] += data.Text
	}
	// 两个判断步骤都逐片推过，拼起来是模型输出的完整文字
	if !steps["match_causes"] || !steps["write_report"] {
		t.Fatalf("推送增量的步骤 = %v，want match_causes 和 write_report", steps)
	}
	if len(frames) < 4 {
		t.Fatalf("增量应按片推送（至少 4 帧），实际 %d 帧", len(frames))
	}

	// 事件日志：增量不落盘
	logPath := filepath.Join(h.appDirs(), "tasks", task.ID, "events.jsonl")
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("读取事件日志失败：%v", err)
	}
	if strings.Contains(string(raw), contract.EventModelDelta) {
		t.Fatalf("事件日志里不该有增量：%s", raw)
	}
	if strings.Contains(h.eventsJSON(task.ID), contract.EventModelDelta) {
		t.Fatal("事件接口不该返回增量")
	}
	events := decode[[]contract.TaskEvent](t, []byte(h.eventsJSON(task.ID)))
	for index, event := range events {
		if event.Seq != int64(index+1) {
			t.Fatalf("事件 seq 应连续：第 %d 条 seq=%d", index+1, event.Seq)
		}
		if event.Type == contract.EventModelDelta {
			t.Fatal("事件日志里出现了增量事件")
		}
	}

	// 模型输出的全文仍按现有事件落盘（model_called），断点恢复不受影响
	persisted := false
	for _, event := range events {
		if event.Type == contract.EventModelCalled && strings.Contains(brief(event.Response), "正在撰写") {
			persisted = true
		}
	}
	if !persisted {
		t.Fatal("model_called 里应保留判断步骤的完整输出")
	}
}

// brief 把事件里的响应压成一段文字，方便查找。
func brief(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
