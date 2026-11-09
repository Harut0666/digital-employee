// 旧任务推导出的只读会话（没有会话日志）也能归档和取消归档：
// 归档状态单独存在应用目录的 legacy-archived.json 里，其余写操作仍然只读。
package server_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

func TestOldTaskSessionCanBeArchivedAndRestored(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())
	task := h.createTask(nil)
	h.command(task.ID, initiator, cmd(contract.CmdSubmit))
	listed := h.listSessions()
	if len(listed) != 1 || !listed[0].ReadOnly {
		t.Fatalf("应有一个旧任务推导出的只读会话：%+v", listed)
	}
	id := listed[0].ID
	if !strings.HasPrefix(id, "task-") {
		t.Fatalf("旧任务会话的 ID 应以 task- 开头：%s", id)
	}

	// 归档：接口成功，回的摘要是归档的
	reply := h.sessionPatch(id, map[string]any{"archived": true})
	if summary, _ := reply["session"].(map[string]any); summary["archived"] != true || summary["readOnly"] != true {
		t.Fatalf("归档后应返回已归档的只读会话摘要：%+v", reply)
	}

	// 默认列表（会话列表、工作区列表）不含它；带 archived=1 时含且 archived=true
	if got := sessionListOf(h, ""); len(got) != 0 {
		t.Fatalf("归档后默认会话列表不应含它：%+v", got)
	}
	if _, found := h.workspacesAs(contract.RoleInitiator).findSession(id); found {
		t.Fatal("归档后默认工作区列表不应含它")
	}
	if got := sessionListOf(h, "?archived=1"); len(got) != 1 || got[0].ID != id || !got[0].Archived {
		t.Fatalf("archived=1 时应列出归档的旧会话且 archived=true：%+v", got)
	}
	var all workspaceList
	getInto(t, h, "/api/workspaces?archived=1", contract.RoleInitiator, &all)
	if item, found := all.findSession(id); !found || !item.Archived || item.StatusDot != "idle" {
		t.Fatalf("工作区列表带 archived=1 时应含归档的旧会话：%+v %v", item, found)
	}
	// 会话视图的 archived 字段；记录里任务仍然在（归档只影响会话列表）
	if detail := h.readSession(id); !detail.Archived || !detail.ReadOnly {
		t.Fatalf("会话视图应是已归档的只读会话：%+v", detail.View)
	}
	recorded := 0
	for _, group := range h.records().Groups {
		for _, item := range group.Items {
			if item.TaskID == task.ID {
				recorded++
			}
		}
	}
	if recorded != 1 {
		t.Fatalf("归档不影响记录，任务应仍在记录里恰好一次，实际 %d 次", recorded)
	}

	// 归档状态单独存在应用目录里，重启后仍然有效
	if _, err := os.Stat(filepath.Join(h.workspace, "legacy-archived.json")); err != nil {
		t.Fatalf("应有 legacy-archived.json：%v", err)
	}
	h.baseURL = h.serve()
	if got := sessionListOf(h, ""); len(got) != 0 {
		t.Fatalf("重启后仍应是归档的：%+v", got)
	}

	// 仍然只读：发消息、重命名、选模型都返回只读错误
	path := "/api/sessions/" + id
	readOnly := map[string]func() (int, []byte){
		"发消息": func() (int, []byte) { return h.postJSON(path+"/messages", initiator, map[string]any{"text": "你好"}) },
		"重命名": func() (int, []byte) { return h.patch(path, map[string]any{"title": "新标题"}) },
		"选模型": func() (int, []byte) {
			return h.trySetModel(id, initiator, map[string]any{"connection": scriptedConnection.Name, "model": scriptedConnection.Models[0]})
		},
	}
	for name, write := range readOnly {
		if status, data := write(); status != http.StatusConflict || !strings.Contains(string(data), session.ErrSessionReadOnly) {
			t.Fatalf("%s：%d %s", name, status, data)
		}
	}

	// 取消归档：回到默认列表
	reply = h.sessionPatch(id, map[string]any{"archived": false})
	if summary, _ := reply["session"].(map[string]any); summary["archived"] != false {
		t.Fatalf("取消归档后应返回未归档的摘要：%+v", reply)
	}
	if got := sessionListOf(h, ""); len(got) != 1 || got[0].ID != id || got[0].Archived {
		t.Fatalf("取消归档后应回到默认列表：%+v", got)
	}
	if h.readSession(id).Archived {
		t.Fatal("取消归档后会话视图不应是归档的")
	}
}

func TestArchivingAnUnknownOldTaskSessionIsNotFound(t *testing.T) {
	h := newHarness(t, nil, withScriptedModel())
	status, data := h.patch("/api/sessions/task-no-such-task", map[string]any{"archived": true})
	if status != http.StatusNotFound || !strings.Contains(string(data), contract.ErrNotFound) {
		t.Fatalf("不存在的旧任务会话应返回 404：%d %s", status, data)
	}
}
