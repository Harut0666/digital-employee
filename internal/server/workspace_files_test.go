// 右栏“工作区文件”：浏览文件夹、预览文件、用本机应用打开。边界和前台 Agent 读目录是同一道：出不了工作区目录，也不看隐藏项。
package server_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

func (h *harness) browse(sessionID, sub string, role contract.Role) (int, []byte) {
	return h.send(http.MethodGet, "/api/sessions/"+sessionID+"/workspace?path="+url.QueryEscape(sub), role, "", nil)
}

func TestBrowsingTheWorkspaceListsOneLevelIncludingProgramFolders(t *testing.T) {
	h := newHarness(t, nil, withAppMode())
	h.writeWorkspaceFile(t, "a.txt", []byte("甲"))
	h.writeWorkspaceFile(t, "资料/汇总.xlsx", []byte("x"))
	h.writeWorkspaceFile(t, "资料/子目录/明细.csv", []byte("y"))
	h.writeWorkspaceFile(t, ".secret", []byte("隐藏"))
	h.writeWorkspaceFile(t, ".hidden/内部.txt", []byte("隐藏文件夹"))
	h.writeWorkspaceFile(t, "交付/周报_v1.docx", []byte("x"))
	h.writeWorkspaceFile(t, "材料/销售明细.xlsx", []byte("x"))
	created := h.folderSession()

	names := func(sub string) string {
		status, data := h.browse(created.ID, sub, contract.RoleInitiator)
		if status != http.StatusOK {
			t.Fatalf("浏览 %q：%d %s", sub, status, data)
		}
		listed := decode[session.FolderListing](t, data)
		out := []string{}
		for _, entry := range listed.Entries {
			mark := ""
			if entry.Dir {
				mark = "/"
			}
			out = append(out, entry.Name+mark)
		}
		return fmt.Sprint(out)
	}
	// 这一层：文件夹在前；“交付”“材料”是给人看的，所以列出来；隐藏项不列
	if got, want := names(""), "[交付/ 材料/ 资料/ a.txt]"; got != want {
		t.Fatalf("根目录 = %s，want %s", got, want)
	}
	if got, want := names("资料"), "[子目录/ 汇总.xlsx]"; got != want {
		t.Fatalf("资料 = %s，want %s", got, want)
	}
	if got, want := names("交付"), "[周报_v1.docx]"; got != want {
		t.Fatalf("交付 = %s，want %s", got, want)
	}
}

func TestBrowsingTheWorkspaceRefusesAnythingOutsideOrHidden(t *testing.T) {
	h := newHarness(t, nil, withAppMode())
	h.writeWorkspaceFile(t, ".hidden/内部.txt", []byte("隐藏文件夹"))
	h.writeWorkspaceFile(t, "a.txt", []byte("甲"))
	created := h.folderSession()

	for _, sub := range []string{"..", "../..", "资料/../..", ".hidden", `C:\Windows`, "/etc"} {
		if status, data := h.browse(created.ID, sub, contract.RoleInitiator); status < 400 {
			t.Errorf("浏览 %q 应被拒绝：%d %s", sub, status, data)
		}
	}
	// 读文件同样：越界、隐藏、目录本身、不存在都拒绝；没带角色不能读
	for _, rel := range []string{"../x.txt", ".hidden/内部.txt", "", "没有.txt"} {
		status, _ := h.send(http.MethodGet, "/api/sessions/"+created.ID+"/workspace/file?path="+url.QueryEscape(rel), contract.RoleInitiator, "", nil)
		if status < 400 {
			t.Errorf("读 %q 应被拒绝：%d", rel, status)
		}
	}
	if status, _ := h.send(http.MethodGet, "/api/sessions/"+created.ID+"/workspace/file?path=a.txt", "", "", nil); status != http.StatusForbidden {
		t.Fatalf("没带角色不能读：%d", status)
	}
	if status, _ := h.browse(created.ID, "", ""); status != http.StatusForbidden {
		t.Fatalf("没带角色不能浏览：%d", status)
	}
}

func TestWorkspaceFileContentIsServedSafely(t *testing.T) {
	h := newHarness(t, nil, withAppMode())
	h.writeWorkspaceFile(t, "说明.md", []byte("# 标题"))
	h.writeWorkspaceFile(t, "网页.html", []byte("<script>alert(1)</script>"))
	h.writeWorkspaceFile(t, "图.png", []byte("not really a png"))
	created := h.folderSession()
	get := func(rel string) *http.Response {
		request, _ := http.NewRequest(http.MethodGet, h.baseURL+"/api/sessions/"+created.ID+"/workspace/file?path="+url.QueryEscape(rel), nil)
		request.Header.Set("x-role", string(contract.RoleInitiator))
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = response.Body.Close() })
		return response
	}

	md := get("说明.md")
	if md.StatusCode != http.StatusOK || md.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("Markdown 按二进制发出（由界面决定怎么显示）：%d %q", md.StatusCode, md.Header.Get("Content-Type"))
	}
	html := get("网页.html")
	// 网页绝不能以 text/html 发出：否则点开会在应用的来源里执行脚本
	if got := html.Header.Get("Content-Type"); got != "application/octet-stream" || html.Header.Get("Content-Security-Policy") == "" || html.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("网页文件必须是二进制 + sandbox + nosniff：%q %q", got, html.Header.Get("Content-Security-Policy"))
	}
	if got := get("图.png").Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("图片类型 = %q", got)
	}
}

func TestOpeningWorkspaceFilesWithLocalApps(t *testing.T) {
	h := newHarness(t, nil, withAppMode())
	h.writeWorkspaceFile(t, "资料/汇总.xlsx", []byte("x"))
	created := h.folderSession()
	open := func(rel, mode string) (int, []byte) {
		return h.postJSON("/api/sessions/"+created.ID+"/workspace/open", contract.RoleInitiator, map[string]any{"path": rel, "mode": mode})
	}

	if status, data := open("资料/汇总.xlsx", "default"); status != http.StatusOK {
		t.Fatalf("默认应用打开：%d %s", status, data)
	}
	if status, data := open("资料/汇总.xlsx", "reveal"); status != http.StatusOK {
		t.Fatalf("在文件夹中显示：%d %s", status, data)
	}
	// 文件夹交给文件管理器（OpenFolder 是假的记录器之外的缺省实现，这里只验证被拒绝的情况）
	before := len(h.openedList())
	for name, call := range map[string]func() (int, []byte){
		"越界":     func() (int, []byte) { return open("../x.xlsx", "default") },
		"没有这个文件": func() (int, []byte) { return open("没有.xlsx", "default") },
		"未知方式":   func() (int, []byte) { return open("资料/汇总.xlsx", "run") },
	} {
		if status, data := call(); status < 400 {
			t.Errorf("%s 应被拒绝：%d %s", name, status, data)
		}
	}
	if got := h.openedList(); len(got) != before || got[0] != "default:汇总.xlsx" || got[1] != "reveal:汇总.xlsx" {
		t.Fatalf("打开记录 = %v", got)
	}
}
