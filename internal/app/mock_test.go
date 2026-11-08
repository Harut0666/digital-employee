package app

import (
	"encoding/json"
	"strings"
	"testing"

	"digital-employee/internal/contract"
)

func commandsWithRoles(actions []contract.TaskAction) []string {
	rows := make([]string, 0, len(actions))
	for _, action := range actions {
		roles := make([]string, 0, len(action.Roles))
		for _, role := range action.Roles {
			roles = append(roles, string(role))
		}
		rows = append(rows, action.Command+"="+strings.Join(roles, ","))
	}
	return rows
}

// 前端只看快照的 actions 和 waitingFor，占位引擎联调时也要给出来。
func TestMockEngineReportsActionsAndWaitingFor(t *testing.T) {
	engine := NewMockEngine(nil, t.TempDir())
	draft, err := engine.Create(contract.CreateTaskInput{Employee: "weekly-report", WorkspaceID: "ws-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(commandsWithRoles(draft.Actions), " "); got != "submit=initiator terminate=initiator" {
		t.Fatalf("草稿 actions = %q", got)
	}
	if len(draft.WaitingFor) != 1 || draft.WaitingFor[0] != contract.RoleInitiator {
		t.Fatalf("草稿 waitingFor = %v", draft.WaitingFor)
	}

	result := engine.Command(draft.ID, contract.TaskCommand{Type: contract.CmdSubmit}, contract.RoleInitiator)
	if !result.OK {
		t.Fatalf("提交失败：%s", result.Message)
	}
	if got := strings.Join(commandsWithRoles(result.Snapshot.Actions), " "); got != "terminate=initiator" {
		t.Fatalf("暂停 actions = %q", got)
	}
	if result.Snapshot.Pause.Title == "" || result.Snapshot.Pause.Resolution == "" {
		t.Fatalf("暂停说明不全：%+v", result.Snapshot.Pause)
	}

	summaries := engine.List()
	if len(summaries) != 1 || summaries[0].StatusText == "" || len(summaries[0].WaitingFor) != 1 {
		t.Fatalf("摘要 = %+v", summaries)
	}
}

func TestMockEngineTerminatesWhatItOffers(t *testing.T) {
	engine := NewMockEngine(nil, t.TempDir())
	draft, _ := engine.Create(contract.CreateTaskInput{Employee: "weekly-report", WorkspaceID: "ws-1"})
	result := engine.Command(draft.ID, contract.TaskCommand{Type: contract.CmdTerminate}, contract.RoleInitiator)
	if !result.OK || result.Snapshot.State != contract.StateTerminated {
		t.Fatalf("终止结果 = %+v", result)
	}
	// 空值要序列化成 []，不是 null：前端直接在上面调数组方法
	raw, err := json.Marshal(engine.List()[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"waitingFor":[]`) {
		t.Fatalf("摘要 JSON = %s", raw)
	}
	snapshot, _ := engine.Get(draft.ID)
	if raw, _ := json.Marshal(snapshot); !strings.Contains(string(raw), `"actions":[]`) || !strings.Contains(string(raw), `"waitingFor":[]`) {
		t.Fatalf("快照 JSON = %s", raw)
	}
}
