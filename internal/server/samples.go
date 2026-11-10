// 欢迎页“载入公开样例”（对应 packages/server/src/routes/samples.ts）：
// 把首次启动释放到工作区里的 samples 一键变成草稿任务，样例定义复用 samples/cases/A-01.json。
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"digital-employee/internal/contract"
	"digital-employee/internal/session"
)

// PublicSampleID 公开样例 id。
const PublicSampleID = "scenario-a"

// sampleCaseFile 公开样例对应的用例文件（相对 <工作区目录>/samples）。
var sampleCaseFile = filepath.Join("cases", "A-01.json")

type publicSample struct {
	ID          string             `json:"id"`
	Employee    string             `json:"employee"`
	Title       string             `json:"title"`
	Form        map[string]any     `json:"form"`
	Files       []publicSampleFile `json:"files"`
	attachments []contract.AttachmentInput
}

type publicSampleFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type sampleCase struct {
	ID          string `json:"id"`
	Employee    string `json:"employee"`
	Title       string `json:"title"`
	Form        map[string]any
	Attachments []struct {
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"attachments"`
}

// readPublicSample 读工作区里的公开样例；样例没释放或文件损坏时返回 nil（界面上不显示入口）。
func readPublicSample(workspaceDir string) *publicSample {
	caseFile := filepath.Join(workspaceDir, "samples", sampleCaseFile)
	data, err := os.ReadFile(caseFile)
	if err != nil {
		return nil
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil
	}
	employee, _ := parsed["employee"].(string)
	form, formOK := parsed["form"].(map[string]any)
	rawAttachments, attachmentsOK := parsed["attachments"].([]any)
	if employee == "" || !formOK || form == nil || !attachmentsOK {
		return nil
	}
	baseDir := filepath.Dir(caseFile)
	sample := &publicSample{ID: PublicSampleID, Employee: employee, Form: form, Title: "公开样例"}
	if title, ok := parsed["title"].(string); ok {
		sample.Title = title
	}
	for _, item := range rawAttachments {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, nameOK := entry["name"].(string)
		rel, pathOK := entry["path"].(string)
		if !nameOK || !pathOK || name == "" || rel == "" {
			continue
		}
		sourcePath := rel
		if !filepath.IsAbs(sourcePath) {
			sourcePath = filepath.Join(baseDir, rel)
		}
		info, err := os.Stat(sourcePath)
		if err != nil {
			continue
		}
		sample.attachments = append(sample.attachments, contract.AttachmentInput{Name: name, SourcePath: sourcePath})
		sample.Files = append(sample.Files, publicSampleFile{Name: name, Size: info.Size()})
	}
	if len(sample.attachments) == 0 {
		return nil
	}
	return sample
}

// sessionSample 会话里“载入公开样例”用的样例：员工、预填的表单字段、应用目录里的样例文件。
func (s *Server) sessionSample() *session.Sample {
	// 样例材料从应用目录取（单目录模式下应用目录就是工作区目录）；不看人的工作目录
	sample := readPublicSample(s.opts.AppDir)
	if sample == nil {
		return nil
	}
	out := &session.Sample{ID: sample.ID, Title: sample.Title, Employee: sample.Employee, Form: sample.Form}
	for index, attachment := range sample.attachments {
		out.Files = append(out.Files, session.SampleFile{Name: attachment.Name, Path: attachment.SourcePath, Size: sample.Files[index].Size})
	}
	return out
}

func (s *Server) handleListSamples(w http.ResponseWriter, r *http.Request) {
	list := []publicSample{}
	if sample := readPublicSample(s.workspaceSampleDir()); sample != nil {
		list = append(list, *sample)
	}
	// 列表不带 sourcePath：浏览器只需要知道有哪些样例文件
	type sampleInfo struct {
		ID       string             `json:"id"`
		Employee string             `json:"employee"`
		Title    string             `json:"title"`
		Form     map[string]any     `json:"form"`
		Files    []publicSampleFile `json:"files"`
	}
	out := make([]sampleInfo, 0, len(list))
	for _, sample := range list {
		out = append(out, sampleInfo{
			ID: sample.ID, Employee: sample.Employee, Title: sample.Title,
			Form: sample.Form, Files: sample.Files,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"samples": out})
}

func (s *Server) handleCreateSampleTask(w http.ResponseWriter, r *http.Request) {
	if s.engine() == nil {
		writeJSON(w, http.StatusInternalServerError, internalError("任务引擎未就绪"))
		return
	}
	role, ok := roleOf(r)
	if !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	if role != contract.RoleInitiator {
		writeAPIError(w, http.StatusForbidden, apiError(contract.ErrForbiddenRole, "只有发起人可以载入公开样例任务"))
		return
	}
	id := r.PathValue("id")
	var sample *publicSample
	if id == PublicSampleID {
		sample = readPublicSample(s.workspaceSampleDir())
	}
	if sample == nil {
		writeAPIError(w, http.StatusNotFound, apiError(contract.ErrNotFound, fmt.Sprintf("工作区里没有公开样例：%s（首次启动会自动释放）", id)))
		return
	}
	// 样例任务建在发起时所在的工作区：请求体的 workspaceId，缺省取唯一的工作区
	body, _ := readBodyJSON(r)
	workspaceID, _ := body["workspaceId"].(string)
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		workspaceID = s.onlyWorkspace()
	}
	if !s.workspaceRegistered(workspaceID) {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "载入样例要指定工作区：请求体缺少有效的 workspaceId"))
		return
	}
	snapshot, err := s.engine().Create(contract.CreateTaskInput{
		Employee:    sample.Employee,
		Form:        sample.Form,
		Attachments: sample.attachments,
		Actor:       role,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	writeJSON(w, http.StatusCreated, snapshot)
}
