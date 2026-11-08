// T38：应用目录模式下的工作区列表、未归属分组和记录 tab（从 HTTP 接口验证）。
package app_test

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// sessionSummaryItem GET /api/workspaces 里的会话摘要（T38 新增的字段）。
type workspaceSummary struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	StatusDot     string `json:"statusDot"`
	Unread        bool   `json:"unread"`
	Archived      bool   `json:"archived"`
	WorkspaceID   string `json:"workspaceId"`
	WorkspaceName string `json:"workspaceName"`
	UpdatedAt     string `json:"updatedAt"`
}

// listWorkspace GET /api/workspaces 里的一项（T38 之后每项带会话摘要）。
type listWorkspace struct {
	registered
	Sessions []workspaceSummary `json:"sessions"`
}

// sessionList GET /api/workspaces（T38 之后带会话摘要和未归属分组）。
type sessionList struct {
	ReadOnly   bool              `json:"readOnly"`
	Reason     string            `json:"reason"`
	Workspaces []listWorkspace   `json:"workspaces"`
	Unassigned []workspaceSummary `json:"unassigned"`
}

// listWithSessions GET /api/workspaces。
func (a *switchingApp) listWithSessions() sessionList {
	a.t.Helper()
	status, data := a.call(http.MethodGet, "/api/workspaces", nil)
	if status != http.StatusOK {
		a.t.Fatalf("读取工作区列表失败：%d %s", status, data)
	}
	return decodeAs[sessionList](a.t, data)
}

// newSessions 工作区里的会话（样例任务留下的旧任务会话不算）。
func (l sessionList) newSessions(ids ...string) []string {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	out := []string{}
	for _, workspace := range l.Workspaces {
		for _, item := range workspace.Sessions {
			if want[item.ID] {
				out = append(out, item.ID)
			}
		}
	}
	for _, item := range l.Unassigned {
		if want[item.ID] {
			out = append(out, item.ID)
		}
	}
	return out
}



func (l sessionList) find(sessionID string) (workspaceSummary, string, bool) {
	for _, workspace := range l.Workspaces {
		for _, item := range workspace.Sessions {
			if item.ID == sessionID {
				return item, workspace.Name, true
			}
		}
	}
	for _, item := range l.Unassigned {
		if item.ID == sessionID {
			return item, "", true
		}
	}
	return workspaceSummary{}, "", false
}

func TestWorkspaceListCarriesSessionSummaries(t *testing.T) {
	appDir := t.TempDir()
	home := t.TempDir()
	a := startApp(t, appDir, "", withAppEnv(map[string]string{"USERPROFILE": home}))
	mine := a.byName("我的工作区")
	other := a.createWorkspace("华南区", "")
	inMine := a.createSessionIn(mine.ID)
	inOther := a.createSessionIn(other.ID)
	// 各说一句话，标题取自第一句（模拟模式的前台 Agent 不调模型）
	for _, id := range []string{inMine.ID, inOther.ID} {
		if status, data := a.call(http.MethodPost, "/api/sessions/"+id+"/messages", map[string]any{"text": "帮我出第 44 周的经营周报"}); status != http.StatusOK {
			t.Fatalf("发消息失败：%d %s", status, data)
		}
	}

	var list = a.listWithSessions()
	// 会话目录是隔一会儿才重读的：新建后马上读列表看不到，等它刷新
	deadline := time.Now().Add(5 * time.Second)
	for len(list.newSessions(inMine.ID, inOther.ID)) < 2 && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		list = a.listWithSessions()
	}
	if got := list.newSessions(inMine.ID, inOther.ID); len(got) != 2 {
		t.Fatalf("两个工作区下应各有自己的会话：%v（%+v）", got, list)
	}
	if !slices.Contains(list.newSessions(inMine.ID, inOther.ID), inMine.ID) {
		t.Fatalf("我的工作区下应有刚开的会话：%+v", list)
	}
	if len(list.Unassigned) != 0 {
		t.Fatalf("没有取消登记时未归属应为空：%+v", list.Unassigned)
	}
	item, name, _ := list.find(inOther.ID)
	if name != "华南区" || item.WorkspaceName != "华南区" || item.WorkspaceID != other.ID {
		t.Fatalf("会话摘要要带工作区名：%+v", item)
	}
	if item.Title != "帮我出第 44 周的经营周报" || item.UpdatedAt == "" || item.Archived {
		t.Fatalf("会话摘要要带标题、更新时间和归档标记：%+v", item)
	}
	// 状态点由后端算出：说了一句话、还在等选员工的会话是“等你处理”
	if item.StatusDot != "waiting" {
		t.Fatalf("等人选员工的会话状态点应是等你处理：%+v", item)
	}
	// 归档的会话默认不列；取消归档后回来
	if status, data := a.call(http.MethodPatch, "/api/sessions/"+inMine.ID, map[string]any{"archived": true}); status != http.StatusOK {
		t.Fatalf("归档会话失败：%d %s", status, data)
	}
	if got := a.listWithSessions().newSessions(inMine.ID); len(got) != 0 {
		t.Fatalf("归档的会话默认不列：%v", got)
	}
	if status, data := a.call(http.MethodPatch, "/api/sessions/"+inMine.ID, map[string]any{"archived": false}); status != http.StatusOK {
		t.Fatalf("取消归档失败：%d %s", status, data)
	}
	if got := a.listWithSessions().newSessions(inMine.ID); len(got) != 1 {
		t.Fatalf("取消归档后应回到列表：%v", got)
	}
}

func TestUnregisteredWorkspaceSessionsGoToUnassigned(t *testing.T) {
	appDir := t.TempDir()
	home := t.TempDir()
	a := startApp(t, appDir, "", withAppEnv(map[string]string{"USERPROFILE": home}))
	other := a.createWorkspace("华南区", "")
	inOther := a.createSessionIn(other.ID)
	inOtherTitle := "华南区的会话"
	if status, data := a.call(http.MethodPatch, "/api/sessions/"+inOther.ID, map[string]any{"title": inOtherTitle}); status != http.StatusOK {
		t.Fatalf("重命名会话失败：%d %s", status, data)
	}

	if status, data := a.call(http.MethodDelete, "/api/workspaces/"+other.ID, nil); status != http.StatusOK {
		t.Fatalf("取消登记失败：%d %s", status, data)
	}
	list := a.listWithSessions()
	if len(list.Workspaces) != 1 || list.Workspaces[0].Name != "我的工作区" {
		t.Fatalf("取消登记后只剩我的工作区：%+v", list)
	}
	got := []string{}
	for _, item := range list.Unassigned {
		got = append(got, item.ID+"="+item.Title)
	}
	if !slices.Equal(got, []string{inOther.ID + "=" + inOtherTitle}) {
		t.Fatalf("取消登记后会话应进未归属：%v", got)
	}
	// 未归属的会话照样能打开
	if status, data := a.call(http.MethodGet, "/api/sessions/"+inOther.ID, nil); status != http.StatusOK {
		t.Fatalf("未归属的会话应能打开：%d %s", status, data)
	}
}

func TestRecordsSpanWorkspaces(t *testing.T) {
	appDir := t.TempDir()
	home := t.TempDir()
	a := startApp(t, appDir, "", withAppEnv(map[string]string{"USERPROFILE": home}))
	mine := a.byName("我的工作区")
	other := a.createWorkspace("华南区", "")
	// 两个工作区各一个已开工的任务
	taskMine := a.createDraftTask(mine.ID)
	// 华南区的任务没有会话：只在记录里出现，会话为空
	taskOther := a.createDraftTask(other.ID)
	// 只聊天没开工的会话不算记录
	a.createSessionIn(mine.ID)

	var records struct {
		Groups []struct {
			Key   string `json:"key"`
			Items []struct {
				TaskID        string `json:"taskId"`
				SessionID     string `json:"sessionId"`
				WorkspaceID   string `json:"workspaceId"`
				WorkspaceName string `json:"workspaceName"`
				State         string `json:"state"`
			} `json:"items"`
		} `json:"groups"`
	}
	status, data := a.call(http.MethodGet, "/api/records", nil)
	if status != http.StatusOK {
		t.Fatalf("读取记录失败：%d %s", status, data)
	}
	records = decodeAs[struct {
		Groups []struct {
			Key   string `json:"key"`
			Items []struct {
				TaskID        string `json:"taskId"`
				SessionID     string `json:"sessionId"`
				WorkspaceID   string `json:"workspaceId"`
				WorkspaceName string `json:"workspaceName"`
				State         string `json:"state"`
			} `json:"items"`
		} `json:"groups"`
	}](t, data)

	names := map[string]string{}
	states := map[string]string{}
	for _, group := range records.Groups {
		for _, item := range group.Items {
			names[item.TaskID] = item.WorkspaceName
			states[item.TaskID] = item.State
		}
	}
	if names[taskOther] != "华南区" {
		t.Fatalf("记录跨工作区，每项带工作区名：%v", names)
	}
	// 样例任务留下的旧任务也会列出来（它们本来就是已开工的任务）
	if _, ok := names[taskOther]; !ok {
		t.Fatalf("记录里应有华南区的任务：%v", names)
	}
	if created := names[taskMine]; created != "我的工作区" {
		t.Fatalf("记录里应有我的工作区的任务：%v", names)
	}
	// 草稿任务还在进行中（还没提交）
	if states[taskMine] != "draft" {
		t.Fatalf("记录要带任务状态：%v", states)
	}
	// 工作区里的目录还在
	if !isDir(filepath.Join(appDir, "tasks")) {
		t.Fatal("任务应在应用目录里")
	}
}
