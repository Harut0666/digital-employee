// 添加工作区的“浏览…”：在本机弹出系统的选择文件夹对话框（服务端就跑在用户本机）。
package server

import (
	"net/http"
	"strings"

	"digital-employee/internal/contract"
)

// errWorkspaceReadOnly 工作区登记只读（单目录模式）；和应用宿主里新建、重命名、取消登记用的是同一个错误码。
const errWorkspaceReadOnly = "workspace_read_only"

// pickFolderResult POST /api/workspaces/pick-folder 的响应：
// 选中 {ok:true,path}，取消 {ok:true,cancelled:true}，失败 {ok:false,message}（HTTP 200，业务失败的约定）。
type pickFolderResult struct {
	OK        bool   `json:"ok"`
	Path      string `json:"path,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
	Message   string `json:"message,omitempty"`
}

// handlePickFolder POST /api/workspaces/pick-folder：请求体可空。对话框是阻塞的，
// 请求会一直挂到用户选完或取消；请求断开时对话框随之关掉。
func (s *Server) handlePickFolder(w http.ResponseWriter, r *http.Request) {
	role, ok := roleOf(r)
	if !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	// 与“添加工作区”一致：发起人的操作
	if role != contract.RoleInitiator {
		writeAPIError(w, http.StatusForbidden, apiError(contract.ErrForbiddenRole, "只有发起人可以添加工作区"))
		return
	}
	// 单目录模式（DE_WORKSPACE 指定）：登记只读，沿用“不能添加”的错误
	if !s.opts.WorkspaceWritable {
		reason := s.opts.WorkspaceReadOnlyReason
		if reason == "" {
			reason = "工作区由环境变量指定，不能在界面上添加工作区"
		}
		writeAPIError(w, http.StatusConflict, apiError(errWorkspaceReadOnly, reason))
		return
	}
	// 同一时刻只开一个对话框：再点一次不会叠出第二个
	if !s.pickMu.TryLock() {
		writeJSON(w, http.StatusOK, pickFolderResult{Message: "已经有一个选择文件夹的对话框打开着，请先在它上面选择或取消"})
		return
	}
	defer s.pickMu.Unlock()

	path, picked, err := s.opts.PickFolder(r.Context())
	switch {
	case err != nil:
		s.log.Warnf("选择文件夹失败：%v", err)
		writeJSON(w, http.StatusOK, pickFolderResult{Message: "无法选择文件夹：" + err.Error()})
	case !picked:
		writeJSON(w, http.StatusOK, pickFolderResult{OK: true, Cancelled: true})
	case strings.TrimSpace(path) == "":
		writeJSON(w, http.StatusOK, pickFolderResult{Message: "没有拿到所选文件夹的路径，请重试或手动填写路径"})
	default:
		writeJSON(w, http.StatusOK, pickFolderResult{OK: true, Path: strings.TrimSpace(path)})
	}
}
