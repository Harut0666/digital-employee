// 知识库接口：入库、浏览、查看、删除。规则都在 internal/knowledge。
package server

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"digital-employee/internal/contract"
	"digital-employee/internal/knowledge"
	"digital-employee/internal/session"
)

// knowledgeErrorStatus 知识库错误码 → HTTP 状态码（通用错误码走 statusOf）。
var knowledgeErrorStatus = map[string]int{
	knowledge.ErrNotDelivered: http.StatusConflict,
}

func writeKnowledgeError(w http.ResponseWriter, err error) {
	var failure *knowledge.Error
	if !errors.As(err, &failure) {
		writeJSON(w, http.StatusInternalServerError, internalError(err.Error()))
		return
	}
	status, ok := knowledgeErrorStatus[failure.Code]
	if !ok {
		status = statusOf(failure.Code)
	}
	writeAPIError(w, status, apiError(failure.Code, failure.Message))
}

func (s *Server) handleListKnowledge(w http.ResponseWriter, r *http.Request) {
	entries, err := s.knowledge.List()
	if err != nil {
		writeKnowledgeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

// handleFileKnowledge 把已交付任务的交付物放进知识库。只有发起人能入库。
func (s *Server) handleFileKnowledge(w http.ResponseWriter, r *http.Request) {
	role, ok := roleOf(r)
	if !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	if role != contract.RoleInitiator {
		writeAPIError(w, http.StatusForbidden, apiError(contract.ErrForbiddenRole, "只有发起人能把交付物放进知识库"))
		return
	}
	if s.engine() == nil {
		writeJSON(w, http.StatusInternalServerError, internalError("任务引擎未就绪"))
		return
	}
	body, err := readBodyJSON(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, err.Error()))
		return
	}
	taskID, _ := body["taskId"].(string)
	snapshot, found := s.engine().Get(taskID)
	if !found {
		writeAPIError(w, http.StatusNotFound, apiError(contract.ErrNotFound, fmt.Sprintf("任务不存在：%s", taskID)))
		return
	}
	submission := knowledge.Submission{Task: snapshot}
	submission.Description, _ = body["description"].(string)
	submission.Title, _ = body["title"].(string)
	if kind, ok := body["kind"].(string); ok {
		submission.Kind = contract.DeliverableKind(kind)
	}
	if tags, ok := body["tags"].([]any); ok {
		for _, tag := range tags {
			if text, ok := tag.(string); ok {
				submission.Tags = append(submission.Tags, text)
			}
		}
	}
	if submission.Title == "" {
		submission.Title = s.defaultKnowledgeTitle(snapshot, submission.Kind)
	}
	entry, err := s.knowledge.Add(submission)
	if err != nil {
		writeKnowledgeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}

// defaultKnowledgeTitle 没写标题时用交付物的下载名（不含扩展名）；找不到交付物时留空，由入库规则报错。
func (s *Server) defaultKnowledgeTitle(snapshot *contract.TaskSnapshot, kind contract.DeliverableKind) string {
	for index := range snapshot.Deliverables {
		ref := &snapshot.Deliverables[index]
		if ref.Kind == contract.DeliverableQCRecord || (kind != "" && ref.Kind != kind) {
			continue
		}
		name := s.deliverableDownloadName(snapshot, ref)
		return strings.TrimSuffix(name, filepath.Ext(name))
	}
	return ""
}

// filingSource 入库卡片读知识库的那一面：会话模块只依赖 session.FilingSource。
type filingSource struct{ server *Server }

func (f filingSource) delivered(taskID string) (*contract.TaskSnapshot, bool) {
	if f.server.engine() == nil {
		return nil, false
	}
	snapshot, found := f.server.engine().Get(taskID)
	if !found || snapshot.State != contract.StateDelivered || !knowledge.Fileable(snapshot) {
		return nil, false
	}
	return snapshot, true
}

func (f filingSource) Offer(taskID string) (session.FilingOffer, bool) {
	snapshot, ok := f.delivered(taskID)
	if !ok {
		return session.FilingOffer{}, false
	}
	filed, err := f.server.knowledge.FiledFrom(taskID)
	if err != nil {
		filed = ""
	}
	return session.FilingOffer{Title: f.server.defaultKnowledgeTitle(snapshot, ""), Filed: filed}, true
}

func (f filingSource) Text(taskID string) (string, error) {
	snapshot, ok := f.delivered(taskID)
	if !ok {
		return "", fmt.Errorf("任务没有可入库的交付物：%s", taskID)
	}
	return knowledge.DeliverableText(snapshot)
}

// handleReadKnowledge 查看一个条目：索引字段加正文。
func (s *Server) handleReadKnowledge(w http.ResponseWriter, r *http.Request) {
	entry, text, err := s.knowledge.Read(r.PathValue("id"))
	if err != nil {
		writeKnowledgeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		knowledge.Entry
		Text string `json:"text"`
	}{entry, text})
}

// handleDeleteKnowledge 删除条目。和入库一样只有发起人能做。
func (s *Server) handleDeleteKnowledge(w http.ResponseWriter, r *http.Request) {
	role, ok := roleOf(r)
	if !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	if role != contract.RoleInitiator {
		writeAPIError(w, http.StatusForbidden, apiError(contract.ErrForbiddenRole, "只有发起人能删除知识库条目"))
		return
	}
	if err := s.knowledge.Delete(r.PathValue("id")); err != nil {
		writeKnowledgeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
