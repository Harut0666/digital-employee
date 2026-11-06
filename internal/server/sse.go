// SSE：先补发 after 之后的历史事件，再推送实时事件（对应 packages/server/src/sse.ts）。
//
// 先订阅、后读历史：订阅期间到达的事件先缓冲，补发完再按 seq 去重冲刷，保证不丢不重。
// 每条事件写 `id: <seq>` / `event: <type>` / `data: <json>`，每 15 秒发一次心跳注释。
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"digital-employee/internal/contract"
)

const heartbeatInterval = 15 * time.Second

// encodeEvent 一条事件 → SSE 帧。
func encodeEvent(event contract.TaskEvent) (string, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("id: %d\nevent: %s\ndata: %s\n\n", event.Seq, event.Type, data), nil
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	if s.engine() == nil {
		writeJSON(w, http.StatusInternalServerError, internalError("任务引擎未就绪"))
		return
	}
	id := r.PathValue("id")
	// 重连时浏览器会带 Last-Event-ID，优先于 ?after=
	afterValue := r.Header.Get("Last-Event-ID")
	if afterValue == "" {
		afterValue = r.URL.Query().Get("after")
	}
	after, ok := parseAfter(afterValue)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "after 必须是非负整数"))
		return
	}
	if _, found := s.engine().Get(id); !found {
		writeAPIError(w, http.StatusNotFound, apiError(contract.ErrNotFound, fmt.Sprintf("任务不存在：%s", id)))
		return
	}
	s.eventStream(w, r, id, after)
}

// eventStream 建立 SSE 长连接；返回时连接已结束（客户端断开或任务删除）。
func (s *Server) eventStream(w http.ResponseWriter, r *http.Request, taskID string, after int64) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, internalError("当前连接不支持流式响应"))
		return
	}

	var mu sync.Mutex
	queue := make([]contract.TaskEvent, 0, 16)
	notify := make(chan struct{}, 1)
	closed := false

	cancel := s.engine().Subscribe(taskID, func(event contract.TaskEvent) {
		mu.Lock()
		if !closed {
			queue = append(queue, event)
		}
		mu.Unlock()
		select {
		case notify <- struct{}{}:
		default:
		}
	})
	defer func() {
		mu.Lock()
		closed = true
		mu.Unlock()
		cancel()
	}()

	// 先读历史（订阅已生效，期间到达的事件在 queue 里缓冲）；失败还能返回 JSON 错误
	history, err := s.engine().Events(taskID, after)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}

	header := w.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-cache, no-transform")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	lastSeq := after
	var writeErr error
	push := func(event contract.TaskEvent) bool {
		if event.Seq <= lastSeq {
			return true
		}
		lastSeq = event.Seq
		frame, err := encodeEvent(event)
		if err != nil {
			return true
		}
		if _, err := w.Write([]byte(frame)); err != nil {
			writeErr = err
			return false
		}
		return true
	}

	for _, event := range history {
		if !push(event) {
			return
		}
	}
	flusher.Flush()

	// 冲刷订阅缓冲（排序去重）
	drain := func() bool {
		mu.Lock()
		batch := queue
		queue = nil
		mu.Unlock()
		if len(batch) == 0 {
			return true
		}
		sort.SliceStable(batch, func(i, j int) bool { return batch[i].Seq < batch[j].Seq })
		for _, event := range batch {
			if !push(event) {
				return false
			}
		}
		flusher.Flush()
		return true
	}
	if !drain() {
		return
	}

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-notify:
			if !drain() {
				return
			}
		case <-ticker.C:
			if _, err := w.Write([]byte(": keep-alive\n\n")); err != nil {
				writeErr = err
				return
			}
			flusher.Flush()
		}
		if writeErr != nil {
			return
		}
	}
}
