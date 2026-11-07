// 审核版本：每次送审是一版交付物，退回重做后两版都能下载，新版带着退回意见和修改说明。
package server_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"digital-employee/internal/contract"
)

const reworkComment = "摘要请再精简，突出本周最重要的两个异常。"

// returnedOnce 正常退回一次并重做完成，返回重做后的快照。
func returnedOnce(t *testing.T) (*harness, contract.TaskSnapshot) {
	t.Helper()
	h := newHarness(t, caseScript(t, "A-01-rework"))
	task := h.createTask(a01Files(t))
	expectState(t, h.command(task.ID, initiator, cmd(contract.CmdSubmit)), contract.StateAwaitingReview)
	reworked := h.command(task.ID, reviewer, map[string]any{
		"type": contract.CmdReturn, "comment": reworkComment, "sections": []string{"summary"},
	})
	expectState(t, reworked, contract.StateAwaitingReview)
	return h, reworked
}

func fileOf(t *testing.T, version contract.DeliverableVersion, kind contract.DeliverableKind) contract.DeliverableRef {
	t.Helper()
	for _, file := range version.Files {
		if file.Kind == kind {
			return file
		}
	}
	t.Fatalf("第 %d 版没有 %s：%+v", version.Version, kind, version.Files)
	return contract.DeliverableRef{}
}

func sha256Of(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestReturnedTaskHasTwoVersions(t *testing.T) {
	_, reworked := returnedOnce(t)

	if len(reworked.Versions) != 2 {
		t.Fatalf("版本数 = %d，want 2：%+v", len(reworked.Versions), reworked.Versions)
	}
	first, second := reworked.Versions[0], reworked.Versions[1]
	if first.Version != 1 || second.Version != 2 {
		t.Fatalf("版本号 = %d、%d", first.Version, second.Version)
	}
	if first.Feedback != nil || len(first.RevisionNotes) != 0 {
		t.Fatalf("第 1 版不该有退回意见和修改说明：%+v", first)
	}
	if fileOf(t, first, contract.DeliverableDocx).SHA256 == fileOf(t, second, contract.DeliverableDocx).SHA256 {
		t.Fatal("两版周报内容相同")
	}
	// 最新一版就是当前交付物
	if fileOf(t, second, contract.DeliverableDocx) != docxOf(t, reworked) {
		t.Fatalf("第 2 版 %+v 不是当前交付物 %+v", fileOf(t, second, contract.DeliverableDocx), docxOf(t, reworked))
	}
	// 每版有自己的检验记录
	if fileOf(t, first, contract.DeliverableQCRecord).Path == fileOf(t, second, contract.DeliverableQCRecord).Path {
		t.Fatal("两版共用一份检验记录")
	}
}

func TestNewVersionCarriesFeedbackAndRevisionNotes(t *testing.T) {
	_, reworked := returnedOnce(t)

	second := reworked.Versions[len(reworked.Versions)-1]
	if second.Feedback == nil || second.Feedback.Comment != reworkComment {
		t.Fatalf("第 2 版的退回意见 = %+v", second.Feedback)
	}
	if len(second.RevisionNotes) == 0 {
		t.Fatal("第 2 版没有修改说明")
	}
	for _, note := range second.RevisionNotes {
		if strings.TrimSpace(note.Change) == "" {
			t.Fatalf("修改说明没有写改了什么：%+v", note)
		}
	}
}

func TestEachVersionCanBeDownloaded(t *testing.T) {
	h, reworked := returnedOnce(t)

	for _, version := range reworked.Versions {
		want := fileOf(t, version, contract.DeliverableDocx)
		path := "/api/tasks/" + reworked.ID + "/deliverables/docx?version=" + strconv.Itoa(version.Version)
		status, data := h.send(http.MethodGet, path, "", "", nil)
		if status != http.StatusOK {
			t.Fatalf("下载第 %d 版 = %d %s", version.Version, status, data)
		}
		if sha256Of(data) != want.SHA256 {
			t.Fatalf("第 %d 版下载到的不是这一版的文件", version.Version)
		}
	}
	// 不带版本号时仍是最新一版
	status, data := h.send(http.MethodGet, "/api/tasks/"+reworked.ID+"/deliverables/docx", "", "", nil)
	if status != http.StatusOK || sha256Of(data) != docxOf(t, reworked).SHA256 {
		t.Fatalf("不带版本号下载 = %d，内容是否最新：%v", status, sha256Of(data) == docxOf(t, reworked).SHA256)
	}
}

func TestOldVersionDownloadNameSaysWhichVersion(t *testing.T) {
	h, reworked := returnedOnce(t)

	request, err := http.NewRequest(http.MethodGet, h.baseURL+"/api/tasks/"+reworked.ID+"/deliverables/docx?version=1", bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	disposition := response.Header.Get("Content-Disposition")
	// 经营周报_2026年第44周_第1版.docx
	if !strings.Contains(disposition, "%E7%AC%AC1%E7%89%88.docx") {
		t.Fatalf("旧版本的下载名没有标出版本：%s", disposition)
	}
}

func TestSnapshotTellsDownloadName(t *testing.T) {
	_, reworked := returnedOnce(t)

	// 右栏标题显示的文件名要和下载到的一致
	if got := reworked.DownloadNames[string(contract.DeliverableDocx)]; got != "经营周报_2026年第44周.docx" {
		t.Fatalf("docx 的下载名 = %q，全部：%v", got, reworked.DownloadNames)
	}
}

func TestUnknownVersionIsNotFound(t *testing.T) {
	h, reworked := returnedOnce(t)

	for _, version := range []string{"3", "0", "abc"} {
		status, _ := h.send(http.MethodGet, "/api/tasks/"+reworked.ID+"/deliverables/docx?version="+version, "", "", nil)
		if status != http.StatusNotFound && status != http.StatusBadRequest {
			t.Fatalf("version=%s 返回 %d", version, status)
		}
	}
}

func TestOutOfScopeFeedbackKeepsOneVersion(t *testing.T) {
	_, original, returned := returnedOutOfScope(t)

	if len(original.Versions) != 1 || len(returned.Versions) != 1 {
		t.Fatalf("超范围意见不该产出新版本：送审 %d 版，回到待审核 %d 版", len(original.Versions), len(returned.Versions))
	}
}
