// 下载名：全部从 HTTP 接口验证（Spec 0005“下载名”）。
package server_test

import (
	"mime"
	"net/http"
	"path/filepath"
	"testing"

	"digital-employee/internal/contract"
)

// downloadName GET 交付物，返回响应头里的文件名。
func (h *harness) downloadName(taskID string, kind contract.DeliverableKind) string {
	h.t.Helper()
	response, err := http.Get(h.baseURL + "/api/tasks/" + taskID + "/deliverables/" + string(kind))
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		h.t.Fatalf("下载 %s 失败：%d", kind, response.StatusCode)
	}
	_, params, err := mime.ParseMediaType(response.Header.Get("Content-Disposition"))
	if err != nil {
		h.t.Fatalf("Content-Disposition 无法解析：%v", err)
	}
	return params["filename"]
}

func (h *harness) reviewedTask(form string) contract.TaskSnapshot {
	h.t.Helper()
	contentType, body := multipartBody(h.t, map[string]string{"employee": harnessEmployee, "form": form}, a01Files(h.t))
	status, data := h.send(http.MethodPost, "/api/tasks", initiator, contentType, body)
	if status != http.StatusCreated {
		h.t.Fatalf("建任务失败：%d %s", status, data)
	}
	task := decode[contract.TaskSnapshot](h.t, data)
	review := h.command(task.ID, initiator, cmd(contract.CmdSubmit))
	expectState(h.t, review, contract.StateAwaitingReview)
	return review
}

const week44FormJSON = `{"period":{"year":2026,"week":44}}`

func diskName(t *testing.T, snapshot contract.TaskSnapshot, kind contract.DeliverableKind) string {
	t.Helper()
	for _, deliverable := range snapshot.Deliverables {
		if deliverable.Kind == kind {
			return filepath.Base(deliverable.Path)
		}
	}
	t.Fatalf("没有 %s 交付物：%+v", kind, snapshot.Deliverables)
	return ""
}

func TestDeliverableDownloadsUnderTemplateName(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	task := h.reviewedTask(week44FormJSON)

	if got, want := h.downloadName(task.ID, contract.DeliverableDocx), "经营周报_2026年第44周.docx"; got != want {
		t.Fatalf("下载名 = %q，want %q", got, want)
	}
	// 只改下载名，磁盘上的文件名不变
	if got := diskName(t, h.snapshot(task.ID), contract.DeliverableDocx); got == "经营周报_2026年第44周.docx" {
		t.Fatalf("磁盘文件名不应改变：%q", got)
	}
}

func TestQcRecordKeepsItsOwnName(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"))
	task := h.reviewedTask(week44FormJSON)

	got := h.downloadName(task.ID, contract.DeliverableQCRecord)
	if want := diskName(t, task, contract.DeliverableQCRecord); got != want {
		t.Fatalf("检验记录的下载名 = %q，want %q", got, want)
	}
}

func TestDownloadFallsBackWithoutTemplate(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"), withWorkflowEdit("deliverableName: 经营周报_{year}年第{week}周\n", ""))
	task := h.reviewedTask(week44FormJSON)

	got := h.downloadName(task.ID, contract.DeliverableDocx)
	if want := diskName(t, task, contract.DeliverableDocx); got != want {
		t.Fatalf("下载名 = %q，want 现有名称 %q", got, want)
	}
}

func TestDownloadFallsBackWhenFormLacksField(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"),
		withWorkflowEdit("deliverableName: 经营周报_{year}年第{week}周", "deliverableName: 周报_{region}"))
	task := h.reviewedTask(week44FormJSON)

	got := h.downloadName(task.ID, contract.DeliverableDocx)
	if want := diskName(t, task, contract.DeliverableDocx); got != want {
		t.Fatalf("下载名 = %q，want 现有名称 %q", got, want)
	}
}

// 表单字段是人填的，填出来的名字不能带路径分隔符。
func TestDownloadNameDropsPathCharacters(t *testing.T) {
	h := newHarness(t, caseScript(t, "A-01"),
		withWorkflowEdit("deliverableName: 经营周报_{year}年第{week}周", "deliverableName: 周报_{remark}"))
	task := h.reviewedTask(`{"period":{"year":2026,"week":44},"remark":"../华东:门店"}`)

	if got, want := h.downloadName(task.ID, contract.DeliverableDocx), "周报_.._华东_门店.docx"; got != want {
		t.Fatalf("下载名 = %q，want %q", got, want)
	}
}
