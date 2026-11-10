// 场景 B（技术文献研判员工）的引擎侧回归：真实工具 + 真实渲染 + 剧本式模型，覆盖正常交付、
// 清单与方向不符、资料不足、资料偏少后按现有资料继续。剧本由 internal/literature 的生成器产出。
package engine_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/engine"
	"digital-employee/internal/tools"
)

func newLiteratureEngine(t *testing.T, scriptName string) *engine.Engine {
	t.Helper()
	root := repoRoot(t)
	script := agent.ModelScript{}
	if err := json.Unmarshal(readJSON(t, filepath.Join(root, "samples", "cases", "scripts", scriptName+".json")), &script); err != nil {
		t.Fatalf("解析剧本失败：%v", err)
	}
	registry := tools.NewRegistry()
	t.Cleanup(func() { _ = registry.Close() })
	eng, err := engine.New(engine.Deps{
		Dir:          t.TempDir(),
		EmployeesDir: filepath.Join(root, "employees"),
		ToolClient:   registry,
		Model:        agent.NewScriptedModelProvider(script, agent.ScriptedProviderOptions{}),
	})
	if err != nil {
		t.Fatalf("创建引擎失败：%v", err)
	}
	return eng
}

func createLiteratureTask(t *testing.T, eng *engine.Engine, direction, listFile string) *contract.TaskSnapshot {
	t.Helper()
	attachments := []contract.AttachmentInput{}
	if listFile != "" {
		attachments = append(attachments, contract.AttachmentInput{
			Name: listFile, SourcePath: filepath.Join(repoRoot(t), "samples", "scenario-b", listFile),
		})
	}
	created, err := eng.Create(contract.CreateTaskInput{
		Employee: "literature-brief", Form: map[string]any{"direction": direction},
		Attachments: attachments, Actor: contract.RoleInitiator,
	})
	if err != nil {
		t.Fatalf("创建任务失败：%v", err)
	}
	return created
}

func command(t *testing.T, eng *engine.Engine, id string, cmd contract.TaskCommand, actor contract.Role) *contract.TaskSnapshot {
	t.Helper()
	if result := eng.Command(id, cmd, actor); !result.OK {
		t.Fatalf("%s 被拒绝：%s（%s）", cmd.Type, result.Error, result.Message)
	}
	snapshot, err := eng.WaitForSettled(id)
	if err != nil {
		t.Fatalf("等待任务停稳失败：%v", err)
	}
	return snapshot
}

func TestLiteratureB01DeliversMarkdownWithAssets(t *testing.T) {
	eng := newLiteratureEngine(t, "B-01")
	task := createLiteratureTask(t, eng, "固态电池电解质", "专利清单_固态电池电解质.csv")

	snapshot := command(t, eng, task.ID, contract.TaskCommand{Type: contract.CmdSubmit}, contract.RoleInitiator)
	if snapshot.State != contract.StateAwaitingReview {
		events, _ := eng.Events(task.ID, 0)
		t.Fatalf("状态 = %s，want awaiting_review；事件尾部：%s", snapshot.State, tailEventTypes(events, 6))
	}
	if len(snapshot.QC) != 6 {
		t.Fatalf("首轮质检应有 6 项（意见响应只在重做时检查）：%+v", snapshot.QC)
	}
	for _, check := range snapshot.QC {
		if !check.Passed {
			t.Fatalf("质检项 %s 未通过：%v", check.ID, check.Issues)
		}
	}

	snapshot = command(t, eng, task.ID, contract.TaskCommand{Type: contract.CmdApprove}, contract.RoleReviewer)
	if snapshot.State != contract.StateDelivered {
		t.Fatalf("状态 = %s，want delivered", snapshot.State)
	}
	var mdPath string
	kinds := map[contract.DeliverableKind]bool{}
	for _, d := range snapshot.Deliverables {
		kinds[d.Kind] = true
		if d.Kind == contract.DeliverableMD {
			mdPath = d.Path
		}
	}
	if !kinds[contract.DeliverableMD] || !kinds[contract.DeliverableQCRecord] || kinds[contract.DeliverableDocx] {
		t.Fatalf("交付物应是 Markdown 和检验记录：%+v", snapshot.Deliverables)
	}
	raw, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}
	md := string(raw)
	for _, want := range []string{"最终纳入分析：40 项发明", "## 来源清单", "（依据标题和主权项）"} {
		if !strings.Contains(md, want) {
			t.Errorf("简报里应有 %q", want)
		}
	}
	// 图表随交付物一起存进交付物目录，简报里的相对路径能对上
	assets, err := filepath.Glob(filepath.Join(filepath.Dir(mdPath), "assets", "*.png"))
	if err != nil || len(assets) != 2 {
		t.Fatalf("交付物旁边的 assets 里应有 2 张图表：%v %v", assets, err)
	}
	for _, asset := range assets {
		if !strings.Contains(md, "assets/"+filepath.Base(asset)) {
			t.Errorf("简报没有引用图表 %s", filepath.Base(asset))
		}
	}
}

func TestLiteratureDirectionMismatchPausesWithoutContinue(t *testing.T) {
	eng := newLiteratureEngine(t, "B-04")
	task := createLiteratureTask(t, eng, "硅基负极材料", "专利清单_固态电池电解质.csv")
	snapshot := command(t, eng, task.ID, contract.TaskCommand{Type: contract.CmdSubmit}, contract.RoleInitiator)
	if snapshot.State != contract.StateAwaitingInitiator || snapshot.Pause == nil || snapshot.Pause.Code != contract.PauseListDirection {
		t.Fatalf("应暂停为 list_direction_mismatch：%s %+v", snapshot.State, snapshot.Pause)
	}
	if !strings.Contains(snapshot.Pause.Message, "固态电池电解质") {
		t.Errorf("暂停说明应点出清单属于固态电池电解质：%s", snapshot.Pause.Message)
	}
	for _, action := range snapshot.Actions {
		if action.Command == contract.CmdContinueWithPartial {
			t.Errorf("清单与方向不符时不能选继续：%+v", snapshot.Actions)
		}
	}
	if result := eng.Command(task.ID, contract.TaskCommand{Type: contract.CmdContinueWithPartial}, contract.RoleInitiator); result.OK {
		t.Error("继续命令应被拒绝")
	}
}

func TestLiteratureInsufficientMaterialIsHonest(t *testing.T) {
	eng := newLiteratureEngine(t, "B-05")
	task := createLiteratureTask(t, eng, "钙钛矿太阳能电池", "专利清单_钙钛矿太阳能电池.csv")
	snapshot := command(t, eng, task.ID, contract.TaskCommand{Type: contract.CmdSubmit}, contract.RoleInitiator)
	if snapshot.State != contract.StateAwaitingInitiator || snapshot.Pause == nil || snapshot.Pause.Code != contract.PauseInsufficientMaterial {
		t.Fatalf("应暂停为 insufficient_material：%s %+v", snapshot.State, snapshot.Pause)
	}
	for _, want := range []string{"专利库", "论文库", "查不到"} {
		if !strings.Contains(snapshot.Pause.Message, want) {
			t.Errorf("说明里应写明查了哪些库、结果如何，缺“%s”：%s", want, snapshot.Pause.Message)
		}
	}
	if len(snapshot.Deliverables) != 0 {
		t.Errorf("资料不足时不得交付任何东西：%+v", snapshot.Deliverables)
	}
	for _, action := range snapshot.Actions {
		if action.Command == contract.CmdContinueWithPartial {
			t.Errorf("0 件时不能选“按现有资料继续”：%+v", snapshot.Actions)
		}
	}
}

func TestLiteratureSmallSampleContinues(t *testing.T) {
	eng := newLiteratureEngine(t, "B-06")
	task := createLiteratureTask(t, eng, "钠离子电池正极材料", "专利清单_钠离子电池正极材料_少量.csv")
	snapshot := command(t, eng, task.ID, contract.TaskCommand{Type: contract.CmdSubmit}, contract.RoleInitiator)
	if snapshot.State != contract.StateAwaitingInitiator || snapshot.Pause == nil || snapshot.Pause.Code != contract.PauseInsufficientMaterial {
		t.Fatalf("纳入 3 件应暂停：%s %+v", snapshot.State, snapshot.Pause)
	}
	var carry *contract.TaskAction
	for i := range snapshot.Actions {
		if snapshot.Actions[i].Command == contract.CmdContinueWithPartial {
			carry = &snapshot.Actions[i]
		}
	}
	if carry == nil || carry.Label != "按现有资料继续" {
		t.Fatalf("应有“按现有资料继续”：%+v", snapshot.Actions)
	}
	snapshot = command(t, eng, task.ID, contract.TaskCommand{Type: contract.CmdContinueWithPartial}, contract.RoleInitiator)
	if snapshot.State != contract.StateAwaitingReview {
		t.Fatalf("继续后应到待审核：%s %+v", snapshot.State, snapshot.Pause)
	}
	if snapshot.Form["allowSmallSample"] != true {
		t.Errorf("选择继续后表单应带 allowSmallSample：%+v", snapshot.Form)
	}
}
