// 目录材料（T41）：前台 Agent 的只读工具、使用目录文件卡片、@ 选择、上传写进目录、清单判定。
// 全部从主接缝验证：HTTP 接口 + 真实引擎 + 剧本模型，工作区目录是临时目录。
package server_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

// writeWorkspaceFile 在工作区目录里放一个文件（相对路径，/ 分隔），返回完整路径。
func (h *harness) writeWorkspaceFile(t *testing.T, rel string, content []byte) string {
	t.Helper()
	// 不用 filepath.Join 拼：在 Linux 上 tmp 常常是符号链接，解开后和工作区目录对不上
	path := h.workspace + string(os.PathSeparator) + strings.ReplaceAll(rel, "/", string(os.PathSeparator))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// folderSession 员工接手、还没提交的会话。
func (h *harness) folderSession() session.View {
	h.t.Helper()
	created := h.createSession()
	h.confirm(created.ID, contract.RoleInitiator, harnessEmployee)
	return created
}

// useFolderFile 人在“使用目录文件”卡片上确认（@ 选择走同一个接口）。
func (h *harness) useFolderFile(sessionID string, role contract.Role, paths ...string) (int, []byte) {
	h.t.Helper()
	return h.postJSON("/api/sessions/"+sessionID+"/folder-files", role, map[string]any{"paths": paths})
}

// toolDenial 会话日志里对某个工具的拒绝：参数和原因。
func toolDenial(t *testing.T, detail session.Detail, tool string) (map[string]any, string) {
	t.Helper()
	for index := len(detail.Messages) - 1; index >= 0; index-- {
		message := detail.Messages[index]
		if message.Kind == session.KindToolDenied && message.Tool == tool {
			args, _ := message.Args.(map[string]any)
			return args, message.Reason
		}
	}
	t.Fatalf("会话日志里没有 %s 的拒绝记录", tool)
	return nil, ""
}

// toolResults 会话日志里某个工具的全部调用结果。
func toolResults(t *testing.T, detail session.Detail, tool string) []any {
	t.Helper()
	out := []any{}
	for _, message := range detail.Messages {
		if message.Kind == session.KindToolCalled && message.Tool == tool {
			out = append(out, message.Result)
		}
	}
	if len(out) == 0 {
		t.Fatalf("会话日志里没有 %s 的调用记录", tool)
	}
	return out
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// listingPaths 一次 list_files 结果里的路径（文件夹带尾斜杠）。
func listingPaths(t *testing.T, result any) []string {
	t.Helper()
	fields, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("list_files 的结果不是对象：%+v", result)
	}
	entries, _ := fields["entries"].([]any)
	paths := []string{}
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		if entry["dir"] == true {
			paths = append(paths, fmt.Sprint(entry["path"], "/"))
			continue
		}
		paths = append(paths, fmt.Sprint(entry["path"]))
	}
	return paths
}

// 员工接手后开放目录工具：list_files、read_file、use_folder_file。
func TestFolderToolsAreOpenToTheFrontdesk(t *testing.T) {
	h := newHarness(t, frontdeskScript(modelSays("目录里有两份文件。")))
	created := h.folderSession()

	h.say(created.ID, contract.RoleInitiator, "目录里有什么")

	requests := h.provider.Requests()
	if len(requests) == 0 {
		t.Fatal("模型没有被调用")
	}
	names := toolNames(requests[len(requests)-1].Request.Tools)
	for _, want := range []string{"list_files", "read_file", "use_folder_file"} {
		if !containsString(names, want) {
			t.Fatalf("员工接手后应开放 %s，实际开放：%v", want, names)
		}
	}
}

// 列目录：文件、子目录、跳过隐藏文件。工作区目录里只有人的文件、“交付”和上传的“材料”。
func TestListFilesSkipsHiddenFilesAndSubdirectory(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("list_files", map[string]any{}),
		modelCalls("list_files", map[string]any{"dir": "资料"}),
		modelSays("看完了。"),
	), withAppMode())
	h.writeWorkspaceFile(t, "a.txt", []byte("甲"))
	h.writeWorkspaceFile(t, ".secret", []byte("隐藏"))
	h.writeWorkspaceFile(t, "资料/汇总.xlsx", []byte("x"))
	h.writeWorkspaceFile(t, "资料/.cache", []byte("隐藏"))
	created := h.folderSession()

	h.say(created.ID, contract.RoleInitiator, "目录里有什么")

	results := toolResults(t, h.readSession(created.ID), "list_files")
	if len(results) != 2 {
		t.Fatalf("list_files 调用了 %d 次", len(results))
	}
	root := listingPaths(t, results[0])
	want := []string{"交付/", "资料/", "a.txt"}
	if fmt.Sprint(root) != fmt.Sprint(want) {
		t.Fatalf("工作区目录的列表 = %v，want %v（隐藏文件和“材料”不列）", root, want)
	}
	if sub := listingPaths(t, results[1]); fmt.Sprint(sub) != fmt.Sprint([]string{"资料/汇总.xlsx"}) {
		t.Fatalf("资料目录的列表 = %v", sub)
	}
	if fields, _ := results[0].(map[string]any); fields["truncated"] != false {
		t.Fatalf("没有截断时 truncated 应为 false：%+v", fields)
	}
}

func TestListFilesLimit(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("list_files", map[string]any{"dir": "资料"}),
		modelSays("看完了。"),
	), withAppMode())
	for index := 0; index < 205; index++ {
		h.writeWorkspaceFile(t, fmt.Sprintf("资料/%03d.txt", index), []byte("x"))
	}
	created := h.folderSession()

	h.say(created.ID, contract.RoleInitiator, "看资料目录")

	result := toolResults(t, h.readSession(created.ID), "list_files")[0]
	if paths := listingPaths(t, result); len(paths) != 200 {
		t.Fatalf("最多列 200 项，实际 %d", len(paths))
	}
	if fields, _ := result.(map[string]any); fields["truncated"] != true {
		t.Fatalf("截断时应标明 truncated：%+v", result)
	}
}

// 越界：.. 和绝对路径都拒绝，并写进会话日志。
func TestFolderToolsDenyPathsOutsideTheWorkspace(t *testing.T) {
	cases := []struct {
		name  string
		tool  string
		key   string
		build func(h *harness) string
	}{
		{"read_file 用 .. 跳出工作区", "read_file", "path", func(h *harness) string {
			return "../" + filepath.Base(h.workspace) + "之外/机密.txt"
		}},
		{"read_file 用绝对路径", "read_file", "path", func(h *harness) string {
			return filepath.Join(filepath.Dir(h.workspace), "机密.txt")
		}},
		{"read_file 从子目录往上跳", "read_file", "path", func(*harness) string { return "资料/../../x.txt" }},
		{"list_files 用 ..", "list_files", "dir", func(h *harness) string {
			return "../" + filepath.Base(h.workspace) + "之外"
		}},
		{"list_files 用绝对路径", "list_files", "dir", func(h *harness) string {
			return filepath.Dir(h.workspace)
		}},
		{"use_folder_file 用 ..", "use_folder_file", "path", func(h *harness) string {
			return "../" + filepath.Base(h.workspace) + "之外/机密.txt"
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newHarness(t, nil)
			asked := testCase.build(h)
			h.setFrontdeskScript(frontdeskScript(
				modelCalls(testCase.tool, map[string]any{testCase.key: asked}),
				modelSays("不行。"),
			))
			created := h.folderSession()

			h.say(created.ID, contract.RoleInitiator, "试试越界")

			args, reason := toolDenial(t, h.readSession(created.ID), testCase.tool)
			if args[testCase.key] != asked {
				t.Fatalf("拒绝记录里应带上越界的路径 %q：%+v", asked, args)
			}
			if !strings.Contains(reason, "工作区目录") {
				t.Fatalf("拒绝原因 = %q，应说明路径不在工作区目录里", reason)
			}
		})
	}
}

func TestReadFileUsesExistingParsers(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("read_file", map[string]any{"path": "销售明细.xlsx"}),
		modelCalls("read_file", map[string]any{"path": "周会纪要_2026W44.docx"}),
		modelCalls("read_file", map[string]any{"path": "说明.md"}),
		modelSays("读完了。"),
	))
	h.writeWorkspaceFile(t, "销售明细.xlsx", sampleFile(t, "销售明细.xlsx").content)
	h.writeWorkspaceFile(t, "周会纪要_2026W44.docx", sampleFile(t, "周会纪要_2026W44.docx").content)
	h.writeWorkspaceFile(t, "说明.md", []byte("# 说明\n\n这是目录里的说明。\n"))
	created := h.folderSession()

	h.say(created.ID, contract.RoleInitiator, "读文件")

	results := toolResults(t, h.readSession(created.ID), "read_file")
	if len(results) != 3 {
		t.Fatalf("read_file 调用了 %d 次", len(results))
	}
	texts := make([]string, 0, len(results))
	for _, result := range results {
		fields, _ := result.(map[string]any)
		texts = append(texts, fmt.Sprint(fields["text"]))
	}
	for index, want := range []string{"记录ID", "第 44 周", "# 说明\n\n这是目录里的说明。"} {
		if !strings.Contains(texts[index], want) {
			t.Fatalf("第 %d 次读到的正文里没有 %q：%.200s", index+1, want, texts[index])
		}
	}
}

func TestReadFileTruncatesLongText(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("read_file", map[string]any{"path": "长文.txt"}),
		modelSays("太长了。"),
	))
	h.writeWorkspaceFile(t, "长文.txt", []byte(strings.Repeat("字", 25000)))
	created := h.folderSession()

	h.say(created.ID, contract.RoleInitiator, "读长文")

	result, _ := toolResults(t, h.readSession(created.ID), "read_file")[0].(map[string]any)
	if runes := []rune(fmt.Sprint(result["text"])); len(runes) != 20000 {
		t.Fatalf("超过 2 万字应截断到 20000 字，实际 %d", len(runes))
	}
	if result["truncated"] != true {
		t.Fatalf("截断时应标明 truncated：%+v", result)
	}
}

// 提议后发一张“使用目录文件”卡片，人确认前会话里没有材料。
func TestUsingAFolderFileNeedsConfirmation(t *testing.T) {
	h := newHarness(t, frontdeskScript(
		modelCalls("use_folder_file", map[string]any{"path": "资料/销售明细.xlsx", "reason": "本周的销售明细"}),
		modelSays("我找到了目录里的销售明细，确认后作为材料。"),
	))
	h.writeWorkspaceFile(t, "资料/销售明细.xlsx", sampleFile(t, "销售明细.xlsx").content)
	created := h.folderSession()

	reply := h.say(created.ID, contract.RoleInitiator, "用目录里的销售明细")

	card := cardOf(t, reply.Messages, session.CardUseFile)
	if card.Path != "资料/销售明细.xlsx" || card.Name != "销售明细.xlsx" || card.Reason != "本周的销售明细" {
		t.Fatalf("使用目录文件卡片 = %+v", card)
	}
	if card.Size != int64(len(sampleFile(t, "销售明细.xlsx").content)) {
		t.Fatalf("卡片应带上文件大小：%+v", card)
	}
	if card.Confirmed {
		t.Fatal("人还没确认")
	}
	if got := h.readSession(created.ID); len(got.Attachments) != 0 {
		t.Fatalf("人确认之前不应有材料：%+v", got.Attachments)
	}
}

// 确认后材料是副本：原文件改了不影响已开工的任务。
func TestConfirmedFolderFileIsACopy(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	original := h.writeWorkspaceFile(t, "资料/销售明细.xlsx", sampleFile(t, "销售明细.xlsx").content)
	h.writeWorkspaceFile(t, "资料/周会纪要_2026W44.docx", sampleFile(t, "周会纪要_2026W44.docx").content)
	created := h.folderSession()

	status, data := h.useFolderFile(created.ID, contract.RoleInitiator, "资料/销售明细.xlsx")
	if status != http.StatusOK {
		t.Fatalf("确认目录文件失败：%d %s", status, data)
	}
	got := h.readSession(created.ID)
	if len(got.Attachments) != 1 || got.Attachments[0].Name != "销售明细.xlsx" || got.Attachments[0].Size != int64(len(sampleFile(t, "销售明细.xlsx").content)) {
		t.Fatalf("确认后的材料 = %+v", got.Attachments)
	}
	if err := os.WriteFile(original, []byte("原文件被改了"), 0o644); err != nil {
		t.Fatal(err)
	}

	uploadOK(h, created.ID, sampleFile(t, "周会纪要_2026W44.docx"))
	reply := h.submit(created.ID, week44Form)
	snapshot := h.settled(reply.Session.TaskID)
	expectState(t, snapshot, contract.StateAwaitingReview)
	if len(snapshot.Attachments) != 2 {
		t.Fatalf("任务附件 = %+v", snapshot.Attachments)
	}
}

func TestConfirmingFolderFilesIsValidated(t *testing.T) {
	h := newHarness(t, nil)
	h.writeWorkspaceFile(t, "a.xlsx", []byte("x"))
	h.writeWorkspaceFile(t, ".git/config", []byte("x"))
	h.writeWorkspaceFile(t, "交付/周报_v1.docx", []byte("x"))
	created := h.folderSession()

	if status, _ := h.useFolderFile(created.ID, contract.RoleReviewer, "a.xlsx"); status != http.StatusForbidden {
		t.Fatalf("只有发起人能确认目录文件：%d", status)
	}
	refused := [][]string{
		{"../外面的.xlsx"},
		{filepath.Join(h.workspace, "a.xlsx")},
		{"不存在.xlsx"},
		{"a.xlsx", "不存在.xlsx"},
		{".git/config"},
		{"交付/周报_v1.docx"},
	}
	for _, paths := range refused {
		if status, data := h.useFolderFile(created.ID, contract.RoleInitiator, paths...); status != http.StatusBadRequest {
			t.Fatalf("%v 应被拒绝：%d %s", paths, status, data)
		}
	}
	if status, data := h.useFolderFile(created.ID, contract.RoleInitiator); status != http.StatusBadRequest {
		t.Fatalf("没给文件应被拒绝：%d %s", status, data)
	}
	if got := h.readSession(created.ID); len(got.Attachments) != 0 {
		t.Fatalf("被拒绝的确认不应留下材料：%+v", got.Attachments)
	}
	// 一批里有一个不能用就整批拒绝
	h.writeWorkspaceFile(t, "b.xlsx", []byte("x"))
	if status, _ := h.useFolderFile(created.ID, contract.RoleInitiator, "b.xlsx", "不存在.xlsx"); status != http.StatusBadRequest {
		t.Fatalf("有不存在的文件时应整批拒绝：%d", status)
	}
	if got := h.readSession(created.ID); len(got.Attachments) != 0 {
		t.Fatalf("整批拒绝时不应留下材料：%+v", got.Attachments)
	}
}

// 清单的“已提供材料”：从目录确认的材料和上传的一样算。
func TestFolderFileCountsAsProvidedMaterial(t *testing.T) {
	h := newHarness(t, nil)
	h.writeWorkspaceFile(t, "资料/销售明细.xlsx", sampleFile(t, "销售明细.xlsx").content)
	h.writeWorkspaceFile(t, "资料/周会纪要_2026W44.docx", sampleFile(t, "周会纪要_2026W44.docx").content)
	created := h.folderSession()

	if before := h.readSession(created.ID).Checklist; before == nil || before.Ready {
		t.Fatalf("还没给材料时清单不应备齐：%+v", before)
	}
	if status, data := h.useFolderFile(created.ID, contract.RoleInitiator, "资料/销售明细.xlsx", "资料/周会纪要_2026W44.docx"); status != http.StatusOK {
		t.Fatalf("确认目录文件失败：%d %s", status, data)
	}
	after := h.readSession(created.ID)
	if after.Checklist == nil {
		t.Fatal("会话应带开工清单")
	}
	for _, item := range after.Checklist.Items {
		if item.ID != "period" && item.Status != session.ItemDone {
			t.Fatalf("目录确认的材料应算已提供，清单项 %s 的状态 = %s", item.ID, item.Status)
		}
	}
	// 统计周期不是材料，还缺；带上表单里的周期就能提交
	if after.Checklist.Ready {
		t.Fatalf("统计周期还没填，清单不应备齐：%+v", after.Checklist)
	}
	if status, data := h.trySubmit(created.ID, contract.RoleInitiator, week44Form); status != http.StatusOK {
		t.Fatalf("材料备齐、周期填好后应能提交：%d %s", status, data)
	}
}

// 清单判定改名“已提供材料”：旧的 source: upload 和新写法 material 都能用，上传和目录确认都算。
func TestMaterialSourceCountsBoth(t *testing.T) {
	h := newHarness(t, nil)
	editWorkflow(t, filepath.Join(h.employees, harnessEmployee, "checklist.yaml"), [][2]string{
		{"source: upload", "source: material"},
	})
	h.writeWorkspaceFile(t, "资料/销售明细.xlsx", sampleFile(t, "销售明细.xlsx").content)
	h.writeWorkspaceFile(t, "周会纪要_2026W44.docx", sampleFile(t, "周会纪要_2026W44.docx").content)
	created := h.folderSession()

	if status, data := h.useFolderFile(created.ID, contract.RoleInitiator, "资料/销售明细.xlsx"); status != http.StatusOK {
		t.Fatalf("确认目录文件失败：%d %s", status, data)
	}
	uploadOK(h, created.ID, sampleFile(t, "周会纪要_2026W44.docx"))

	checklist := h.readSession(created.ID).Checklist
	if checklist == nil {
		t.Fatal("会话应带开工清单")
	}
	for _, item := range checklist.Items {
		if item.ID != "period" && item.Status != session.ItemDone {
			t.Fatalf("source=material 的清单应把目录确认和上传都算上，%s 的状态 = %s", item.ID, item.Status)
		}
	}
	if status, data := h.trySubmit(created.ID, contract.RoleInitiator, week44Form); status != http.StatusOK {
		t.Fatalf("source=material 的清单备齐后应能提交：%d %s", status, data)
	}
}

func TestUploadAlsoWritesIntoTheWorkspaceFolder(t *testing.T) {
	h := newHarness(t, nil, withAppMode())
	created := h.folderSession()

	uploadOK(h, created.ID, upload{name: "销售明细.xlsx", content: []byte("第一次")})
	uploadOK(h, created.ID, upload{name: "销售明细.xlsx", content: []byte("第二次，内容不同")})

	dir := filepath.Join(h.workspace, "材料")
	for name, want := range map[string]string{
		"销售明细.xlsx":    "第一次",
		"销售明细(2).xlsx": "第二次，内容不同",
	} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("上传的文件应出现在目录的“材料”里：%v", err)
		}
		if string(data) != want {
			t.Fatalf("%s 的内容 = %q，want %q", name, data, want)
		}
	}
	// 会话里的材料仍是最新的一份，名字不变
	got := h.readSession(created.ID)
	if len(got.Attachments) != 1 || got.Attachments[0].Name != "销售明细.xlsx" {
		t.Fatalf("会话材料 = %+v", got.Attachments)
	}
}

func TestFolderFileCardAction(t *testing.T) {
	h := newHarness(t, nil, withAppMode())
	h.writeWorkspaceFile(t, "a.txt", []byte("内容"))
	created := h.folderSession()

	// 卡片来自模型提议（剧本在员工接手后才用得上）
	h.setFrontdeskScript(frontdeskScript(
		modelCalls("use_folder_file", map[string]any{"path": "a.txt"}),
		modelSays("确认一下。"),
	))
	h.say(created.ID, contract.RoleInitiator, "用 a.txt")

	// 同一个会话连续读两次：卡片要不要按钮由会话日志和磁盘决定，两次必须一致
	first := cardOf(t, h.readSession(created.ID).Messages, session.CardUseFile)
	second := cardOf(t, h.readSession(created.ID).Messages, session.CardUseFile)
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("两次读到的不一样：%+v / %+v", first, second)
	}
	if len(first.Actions) != 1 || first.Actions[0].Roles[0] != contract.RoleInitiator || first.Actions[0].Command != "use_folder_file" {
		t.Fatalf("卡片上的按钮 = %+v", first.Actions)
	}
	if status, data := h.useFolderFile(created.ID, contract.RoleInitiator, first.Path); status != http.StatusOK {
		t.Fatalf("确认失败：%d %s", status, data)
	}
	// 确认后卡片标出已确认，不再给按钮（材料已经是副本，原文件删了也不影响）
	if err := os.Remove(filepath.Join(h.workspace, "a.txt")); err != nil {
		t.Fatal(err)
	}
	confirmed := cardOf(t, h.readSession(created.ID).Messages, session.CardUseFile)
	if !confirmed.Confirmed || len(confirmed.Actions) != 0 {
		t.Fatalf("确认后卡片 = %+v", confirmed)
	}
}

func TestFolderFileRefusedAfterSubmit(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	h.writeWorkspaceFile(t, "资料/销售明细.xlsx", sampleFile(t, "销售明细.xlsx").content)
	h.writeWorkspaceFile(t, "资料/周会纪要_2026W44.docx", sampleFile(t, "周会纪要_2026W44.docx").content)
	created := h.folderSession()
	if status, data := h.useFolderFile(created.ID, contract.RoleInitiator, "资料/销售明细.xlsx", "资料/周会纪要_2026W44.docx"); status != http.StatusOK {
		t.Fatalf("确认目录文件失败：%d %s", status, data)
	}
	reply := h.submit(created.ID, week44Form)
	h.settled(reply.Session.TaskID)

	if status, data := h.useFolderFile(created.ID, contract.RoleInitiator, "资料/销售明细.xlsx"); status != http.StatusConflict {
		t.Fatalf("开工后再确认目录文件应被拒绝：%d %s", status, data)
	}
}
