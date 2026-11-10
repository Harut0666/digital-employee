// 交付物“用其他应用打开”：只能打开任务自己登记的文件，三种方式各走各的打开器（测试里是假的，不会真的启动程序）。
package server_test

import (
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"digital-employee/internal/contract"
)

// recordOpen 假的打开器：只记下“模式:文件名”。
func (h *harness) recordOpen(mode string) func(path string) error {
	return func(path string) error {
		h.opensMu.Lock()
		defer h.opensMu.Unlock()
		h.opens = append(h.opens, mode+":"+filepath.Base(path))
		return nil
	}
}

func (h *harness) openedList() []string {
	h.opensMu.Lock()
	defer h.opensMu.Unlock()
	return append([]string{}, h.opens...)
}

func (h *harness) openDeliverable(taskID string, kind contract.DeliverableKind, body map[string]any) (int, []byte) {
	h.t.Helper()
	return h.postJSON("/api/tasks/"+taskID+"/deliverables/"+string(kind)+"/open", initiator, body)
}

func TestOpenDeliverableWithLocalApps(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	task := h.reviewedTask(week44FormJSON)
	docx := diskName(t, task, contract.DeliverableDocx)
	record := diskName(t, task, contract.DeliverableQCRecord)

	for _, step := range []struct {
		kind contract.DeliverableKind
		mode string
		want string
	}{
		{contract.DeliverableDocx, "default", "default:" + docx},
		{contract.DeliverableDocx, "reveal", "reveal:" + docx},
		{contract.DeliverableQCRecord, "text", "text:" + record},
	} {
		status, data := h.openDeliverable(task.ID, step.kind, map[string]any{"mode": step.mode})
		if status != http.StatusOK {
			t.Fatalf("%s %s：%d %s", step.kind, step.mode, status, data)
		}
		got := h.openedList()
		if last := got[len(got)-1]; last != step.want {
			t.Fatalf("打开记录 = %q，want %q", last, step.want)
		}
	}
	before := len(h.openedList())

	// 不允许的：Word 文档不能用文本编辑器、未知方式、没有的交付物、没有的版本、没有的任务
	for name, call := range map[string]func() (int, []byte){
		"docx 用文本编辑器": func() (int, []byte) {
			return h.openDeliverable(task.ID, contract.DeliverableDocx, map[string]any{"mode": "text"})
		},
		"未知方式": func() (int, []byte) {
			return h.openDeliverable(task.ID, contract.DeliverableDocx, map[string]any{"mode": "run"})
		},
		"没有这种交付物": func() (int, []byte) {
			return h.openDeliverable(task.ID, contract.DeliverableMD, map[string]any{"mode": "default"})
		},
		"没有这一版": func() (int, []byte) {
			return h.openDeliverable(task.ID, contract.DeliverableDocx, map[string]any{"mode": "default", "version": 9})
		},
		"没有这个任务": func() (int, []byte) {
			return h.openDeliverable("no-such-task", contract.DeliverableDocx, map[string]any{"mode": "default"})
		},
	} {
		if status, data := call(); status < 400 {
			t.Errorf("%s 应被拒绝：%d %s", name, status, data)
		}
	}
	if after := len(h.openedList()); after != before {
		t.Fatal(fmt.Sprintf("被拒绝的请求不该启动任何程序：%d → %d", before, after))
	}
}
