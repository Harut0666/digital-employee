// 交付物写入目录（T42、Spec 0006“交付物写入目录”）。
//
// 每个版本进入待审核时，在所属工作区目录的“交付”文件夹里留一份：
// <员工名>_<统计周期或会话标题>_v<版本号>.<扩展名>。同名时加序号，不覆盖；
// 写不进去不影响任务，只在轨迹里记一条说明事件。任务目录里的版本仍是正本。
package server_test

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/engine"
	"digital-employee/internal/settings"
)

// deliverDir 工作区目录里的“交付”文件夹。
func deliverDir(workspace string) string {
	return filepath.Join(workspace, "交付")
}

// deliverEntries “交付”文件夹里的文件名，已排序。
func deliverEntries(t *testing.T, workspace string) []string {
	t.Helper()
	entries, err := os.ReadDir(deliverDir(workspace))
	if err != nil {
		t.Fatalf("读取交付文件夹失败：%v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

// readDelivered 读“交付”文件夹里的一个文件。
func readDelivered(t *testing.T, workspace, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(deliverDir(workspace), name))
	if err != nil {
		t.Fatalf("读取交付文件 %s 失败：%v", name, err)
	}
	return data
}

// download 下载某一版（version 为 0 时是最新一版）的交付物。
func (h *harness) download(taskID string, kind contract.DeliverableKind, version int) []byte {
	h.t.Helper()
	path := "/api/tasks/" + taskID + "/deliverables/" + string(kind)
	if version > 0 {
		path += "?version=" + strconv.Itoa(version)
	}
	status, data := h.send(http.MethodGet, path, "", "", nil)
	if status != http.StatusOK {
		h.t.Fatalf("下载 %s 第 %d 版失败：%d %s", kind, version, status, data)
	}
	return data
}

// sameContent 两个文件内容相同。
func sameContent(a, b []byte) bool {
	return sha256Of(a) == sha256Of(b)
}

// deliverNotices 轨迹里的“交付文件”说明事件。
func deliverNotices(events []contract.TaskEvent) []contract.TaskEvent {
	out := []contract.TaskEvent{}
	for _, event := range events {
		if event.Type == contract.EventDeliverableFiled {
			out = append(out, event)
		}
	}
	return out
}

// blockDeliverDir 让“交付”文件夹写不进去：在它的位置放一个普通文件，创建目录就失败。
func blockDeliverDir(t *testing.T, workspace string) {
	t.Helper()
	if err := os.WriteFile(deliverDir(workspace), []byte("占住交付文件夹的位置"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// wireApp 把装置接到应用目录模式（工作区 ws-a、ws-b，目录在临时目录里）：
// 引擎换成应用目录的（任务在应用目录）、判断步骤用装置的剧本模型，之后经接口建的任务都走这个引擎。
func (h *harness) wireApp(t *testing.T) {
	t.Helper()
	w := newAppWire(t, h)
	// 应用目录里配好一个带 Key 的连接：提交前检查读的是它
	if _, err := settings.Update(w.appDir, func(doc *contract.SettingsDocument) {
		doc.Connections = []contract.ModelConnection{{
			Name: "测试连接", BaseURL: "http://127.0.0.1:9/v1", APIKey: "sk-t42-test", Models: []string{"m1"},
		}}
		// T39：会话模型用 lastModel（设置里最近选的模型）
		doc.LastModel = &contract.ModelRef{Connection: "测试连接", Model: "m1"}
	}); err != nil {
		t.Fatalf("写应用目录设置失败：%v", err)
	}
	eng, err := engine.New(engine.Deps{
		Dir: w.appDir, EmployeesDir: w.employees, ToolClient: h.registry, Model: h.model,
		// 引擎按登记的工作区找交付文件夹的目录
		Workspaces: func(id string) (string, bool) {
			for _, item := range w.workspaces {
				if item.ID == id {
					return item.Path, true
				}
			}
			return "", false
		},
	})
	if err != nil {
		t.Fatalf("创建引擎失败：%v", err)
	}
	h.engine = eng
	h.app = w
	h.workspace = w.workA
	h.baseURL = startServer(t, w.serve(t, h, eng, w.workA, nil))
}

// submitAppTask 在应用目录模式的某个工作区里经会话提交一个第 44 周的任务。
// prepare 在任务派给引擎之前调用，方便把工作区目录摆成想要的样子（比如把“交付”位置占住）。
func (h *harness) submitAppTask(workspaceID string, prepare func()) string {
	h.t.Helper()
	created := h.createSessionIn(workspaceID)
	h.confirm(created.ID, initiator, harnessEmployee)
	if status, data := h.uploadToWorkspace(created.ID, a01Files(h.t), workspaceID); status != http.StatusOK {
		h.t.Fatalf("上传失败：%d %s", status, data)
	}
	if prepare != nil {
		prepare()
	}
	reply := h.submit(created.ID, week44Form)
	if reply.Session.TaskID == "" {
		h.t.Fatalf("提交后没有任务：%+v", reply.Session)
	}
	h.settled(reply.Session.TaskID)
	return reply.Session.TaskID
}

// appHarness 起一套应用目录模式的装置（工作区 ws-a、ws-b），h.workspace 是工作区 ws-a 的目录。
func appHarness(t *testing.T, script agent.ModelScript, options ...harnessOption) *harness {
	t.Helper()
	h := newHarness(t, script, options...)
	h.wireApp(t)
	return h
}

// appWork 应用目录模式下登记的两个工作区目录（ws-a、ws-b）。
func (h *harness) appWork(t *testing.T) *appWire {
	t.Helper()
	if h.app == nil {
		t.Fatal("装置没有切到应用目录模式")
	}
	return h.app
}

func TestEachVersionIsFiledIntoTheDeliverFolder(t *testing.T) {
	h := appHarness(t, caseScript(t, "A-01-rework"))
	taskID := h.submitAppTask("ws-a", nil)

	// 首轮送审后有 v1，内容正是第 1 版
	versionOne := h.snapshot(taskID)
	expectState(t, versionOne, contract.StateAwaitingReview)
	if got, want := deliverEntries(t, h.workspace), []string{"经营周报员工_2026年第44周_v1.docx"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("首轮送审后的交付文件夹 = %v，want %v", got, want)
	}
	firstVersion := fileOf(t, versionOne.Versions[0], contract.DeliverableDocx)
	if sha256Of(readDelivered(t, h.workspace, "经营周报员工_2026年第44周_v1.docx")) != firstVersion.SHA256 {
		t.Fatal("v1 应正是首轮送审的那一版")
	}

	// 退回重做后再送审：v2 另起一个文件，v1 不动
	reworked := h.command(taskID, reviewer, map[string]any{
		"type": contract.CmdReturn, "comment": reworkComment, "sections": []string{"summary"},
	})
	expectState(t, reworked, contract.StateAwaitingReview)
	if got, want := deliverEntries(t, h.workspace), []string{"经营周报员工_2026年第44周_v1.docx", "经营周报员工_2026年第44周_v2.docx"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("退回重做后的交付文件夹 = %v，want %v", got, want)
	}
	if sha256Of(readDelivered(t, h.workspace, "经营周报员工_2026年第44周_v1.docx")) != firstVersion.SHA256 {
		t.Fatal("退回复写新版本时 v1 被改动了")
	}
	// 每一版：内容与下载接口一致
	for _, version := range reworked.Versions {
		name := "经营周报员工_2026年第44周_v" + strconv.Itoa(version.Version) + ".docx"
		filed := readDelivered(t, h.workspace, name)
		if want := fileOf(t, version, contract.DeliverableDocx); sha256Of(filed) != want.SHA256 {
			t.Fatalf("%s 不是第 %d 版的交付物", name, version.Version)
		}
		if !sameContent(filed, h.download(reworked.ID, contract.DeliverableDocx, version.Version)) {
			t.Fatalf("%s 的内容与下载接口不一致", name)
		}
	}
	// 检验记录不是给业务的交付物，不写进交付文件夹
	for _, name := range deliverEntries(t, h.workspace) {
		if strings.Contains(name, "qc_record") || strings.HasSuffix(name, ".json") {
			t.Fatalf("检验记录不该进交付文件夹：%s", name)
		}
	}
	// 正本仍在任务目录：不带版本号下载的是最新一版
	if !sameContent(h.download(reworked.ID, contract.DeliverableDocx, 0), readDelivered(t, h.workspace, "经营周报员工_2026年第44周_v2.docx")) {
		t.Fatal("不带版本号下载的应是最新一版")
	}
}

func TestFiledVersionsAreExplainedInTheTrajectory(t *testing.T) {
	// 首轮和退回重做后的两个版本都要有说明
	h := appHarness(t, caseScript(t, "A-01-rework"))
	taskID := h.submitAppTask("ws-a", nil)
	h.command(taskID, reviewer, map[string]any{
		"type": contract.CmdReturn, "comment": reworkComment, "sections": []string{"summary"},
	})

	events := h.taskEvents(taskID)
	notices := deliverNotices(events)
	if len(notices) != 2 {
		t.Fatalf("应有两版各一条交付文件事件：%+v", notices)
	}
	if want := filepath.Join(deliverDir(h.workspace), "经营周报员工_2026年第44周_v1.docx"); notices[0].FiledPath != want {
		t.Fatalf("第 1 版交付文件路径 = %q，want %q", notices[0].FiledPath, want)
	}
	if !strings.Contains(notices[0].FiledReason, "经营周报员工_2026年第44周_v1.docx") {
		t.Fatalf("说明里应写出文件名：%q", notices[0].FiledReason)
	}
	// 说明事件紧跟在进入待审核之后，轨迹里看到的就是“已送审，文件在哪”
	for index, event := range events {
		if event.Type != contract.EventDeliverableFiled {
			continue
		}
		before := events[index-1]
		if before.Type != contract.EventStateChanged || before.To != contract.StateAwaitingReview || before.Trigger != contract.TriggerQcPassed {
			t.Fatalf("第 %d 条说明事件前面不是送审：%s", index, before.Type)
		}
	}
}

func TestReadOnlyDeliverFolderDoesNotBlockReview(t *testing.T) {
	h := appHarness(t, caseScript(t, "A-01"))
	// “交付”的位置先被一个普通文件占住：要建文件夹就会失败
	taskID := h.submitAppTask("ws-a", func() { blockDeliverDir(t, h.workspace) })

	review := h.snapshot(taskID)
	expectState(t, review, contract.StateAwaitingReview)
	if len(review.Versions) != 1 {
		t.Fatalf("任务应照常送审：%+v", review.Versions)
	}
	if len(h.download(taskID, contract.DeliverableDocx, 1)) == 0 {
		t.Fatal("任务目录里的正本仍可下载")
	}

	events := h.taskEvents(taskID)
	notices := deliverNotices(events)
	if len(notices) != 1 {
		t.Fatalf("写不进去时应在轨迹里留一条说明：%+v", notices)
	}
	notice := notices[0]
	if !strings.Contains(notice.FiledReason, "交付") {
		t.Fatalf("说明应写明没能写进交付文件夹：%+v", notice)
	}
	if notice.FiledError == "" {
		t.Fatalf("说明应带上失败原因：%+v", notice)
	}
	// 说明事件在进入待审核之后：任务已经照常送审
	reviewed := false
	for _, event := range events {
		if event.Type == contract.EventDeliverableFiled {
			if !reviewed {
				t.Fatal("说明事件应在任务进入待审核之后")
			}
		}
		if event.Type == contract.EventStateChanged && event.To == contract.StateAwaitingReview && event.Trigger == contract.TriggerQcPassed {
			reviewed = true
		}
	}
	// 交付文件夹的位置仍是那个占位的文件，没有被改动
	placeholder, err := os.ReadFile(deliverDir(h.workspace))
	if err != nil || string(placeholder) != "占住交付文件夹的位置" {
		t.Fatalf("写不进去时不该改动交付文件夹的位置：%q %v", placeholder, err)
	}
}

func TestExistingFileWithSameNameIsNotOverwritten(t *testing.T) {
	// 用退回重做的剧本：同一个引擎能连跑两个任务
	h := appHarness(t, caseScript(t, "A-01-rework"))
	first := "经营周报员工_2026年第44周_v1.docx"
	occupied := []byte("别人放的文件")
	if err := os.MkdirAll(deliverDir(h.workspace), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deliverDir(h.workspace), first), occupied, 0o644); err != nil {
		t.Fatal(err)
	}
	task1 := h.submitAppTask("ws-a", nil)
	// 同名不覆盖：另起一个带序号的名字
	if got := readDelivered(t, h.workspace, first); string(got) != string(occupied) {
		t.Fatal("已有的同名文件被覆盖了")
	}
	second := "经营周报员工_2026年第44周_v1-2.docx"
	if !sameContent(readDelivered(t, h.workspace, second), h.download(task1, contract.DeliverableDocx, 1)) {
		t.Fatalf("%s 的内容与下载接口不一致", second)
	}
	// 再送一次同样的版本号（另一个任务）也不会覆盖已有的
	h.submitAppTask("ws-a", nil)
	if got := readDelivered(t, h.workspace, first); string(got) != string(occupied) {
		t.Fatal("第二次送审把已有的同名文件覆盖了")
	}
	want := []string{first, second, "经营周报员工_2026年第44周_v1-3.docx"}
	sort.Strings(want)
	if got := deliverEntries(t, h.workspace); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("交付文件夹 = %v，want %v", got, want)
	}
}

func TestFiledIntoTheWorkspaceThatOwnsTheTask(t *testing.T) {
	// 首轮和退回重做后的两个版本都写进任务所属的工作区 B
	h := appHarness(t, caseScript(t, "A-01-rework"))
	taskID := h.submitAppTask("ws-b", nil)
	h.command(taskID, reviewer, map[string]any{
		"type": contract.CmdReturn, "comment": reworkComment, "sections": []string{"summary"},
	})

	w := h.appWork(t)
	want := []string{"经营周报员工_2026年第44周_v1.docx", "经营周报员工_2026年第44周_v2.docx"}
	if got := deliverEntries(t, w.workB); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("任务属于工作区 B，交付文件应写进 B 的交付文件夹：%v", got)
	}
	if _, err := os.Stat(deliverDir(w.workA)); !os.IsNotExist(err) {
		t.Fatalf("不该写进别的工作区：%v", err)
	}
	if _, err := os.Stat(deliverDir(w.appDir)); !os.IsNotExist(err) {
		t.Fatalf("不该写进应用目录：%v", err)
	}
}

// 单目录模式：目录同时是应用目录，多写一个“交付”文件夹会让应用目录变乱，按约定不写；任务照常。
func TestSingleDirectoryModeDoesNotWriteDeliverFolder(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	task := h.createTask(a01Files(t))
	h.command(task.ID, initiator, cmd(contract.CmdSubmit))

	if _, err := os.Stat(deliverDir(h.workspace)); !os.IsNotExist(err) {
		t.Fatalf("单目录模式不写交付文件夹：%v", err)
	}
	if len(h.download(task.ID, contract.DeliverableDocx, 1)) == 0 {
		t.Fatal("单目录模式下仍可下载")
	}
	if notices := deliverNotices(h.taskEvents(task.ID)); len(notices) != 0 {
		t.Fatalf("单目录模式不写交付文件夹，也不记说明：%+v", notices)
	}
}

// 员工配置里没有交付名模板时用表单里的统计周期，员工名照旧用配置里的名字。
func TestFileNameFallsBackToThePeriod(t *testing.T) {
	h := appHarness(t, caseScript(t, "A-01"),
		withWorkflowEdit("deliverableName: 经营周报_{year}年第{week}周\n", ""))
	h.submitAppTask("ws-a", nil)

	if got, want := deliverEntries(t, h.workspace), []string{"经营周报员工_2026年第44周_v1.docx"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("交付文件夹 = %v，want %v", got, want)
	}
}

// 模板里的字段在表单里填不上时，退回用统计周期，不交付残缺的名字。
func TestFileNameFallsBackWhenTemplateCannotBeFilled(t *testing.T) {
	h := appHarness(t, caseScript(t, "A-01"), withWorkflowEdit("deliverableName: 经营周报_{year}年第{week}周", "deliverableName: 周报_{region}"))
	h.submitAppTask("ws-a", nil)

	if got, want := deliverEntries(t, h.workspace), []string{"经营周报员工_2026年第44周_v1.docx"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("交付文件夹 = %v，want %v", got, want)
	}
}
