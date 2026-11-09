// 会话日志的并发写入：Agent 的一轮在流式追加文字增量（update 整份重写），
// 同一时间人可能在发消息（append 追加一行）。两者必须按顺序落盘，不能互相覆盖。
package session

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestStoreSerializesConcurrentAppendAndUpdate(t *testing.T) {
	dir := t.TempDir()
	s := newStore(dir, func() time.Time { return time.Unix(0, 0) })
	const sessionID = "s1"
	// 先放一条“这一轮的文字”，后面反复整份重写它
	text, err := s.append(sessionID, Message{Kind: KindTurnText, Text: "开始"})
	if err != nil {
		t.Fatalf("写入第一行失败：%v", err)
	}

	const appends = 40
	const updates = 40
	var wg sync.WaitGroup
	errs := make(chan error, appends+updates)
	for index := 0; index < appends; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			if _, err := s.append(sessionID, Message{Kind: KindUser, Text: fmt.Sprintf("第 %d 句", index)}); err != nil {
				errs <- err
			}
		}(index)
	}
	for index := 0; index < updates; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.update(sessionID, text.Seq, func(message *Message) { message.Text += "。" }); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("并发写入失败：%v", err)
	}

	current, err := s.read(sessionID)
	if err != nil {
		t.Fatalf("回放失败：%v", err)
	}
	users := 0
	for _, message := range current.messages {
		if message.Kind == KindUser {
			users++
		}
	}
	if users != appends {
		t.Fatalf("并发追加后只剩 %d 条人说的话，want %d（有写入被整份重写覆盖了）", users, appends)
	}
	// 序号必须唯一且从 1 连续排到最后（并发追加不能拿到同一个序号）
	for index, message := range current.messages {
		if message.Seq != int64(index+1) {
			t.Fatalf("第 %d 条消息的序号是 %d，want %d：%+v", index+1, message.Seq, index+1, message)
		}
	}
}
