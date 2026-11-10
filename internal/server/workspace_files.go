// 右栏“工作区文件”的接口：浏览工作区目录里的文件夹和文件、读文件内容预览、用本机应用打开。
// 路径边界在 session.BrowseFolder / WorkspaceFilePath（不出工作区目录、不看隐藏项）；这里只管 HTTP。
package server

import (
	"net/http"
	"path/filepath"
	"strings"

	"digital-employee/internal/contract"
)

// safeContentTypes 预览能直接显示的类型；其余一律按二进制发出去，由界面决定怎么显示。
// 注意 html、svg、xml 不在其中：工作区里的网页不能在应用的来源里被当成网页执行。
var safeContentTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".pdf": "application/pdf",
}

func workspaceContentType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if kind, ok := safeContentTypes[ext]; ok {
		return kind
	}
	return "application/octet-stream"
}

// handleBrowseWorkspace GET /api/sessions/{id}/workspace?path=：列这一层的文件和文件夹（path 为空是工作区根）。
func (s *Server) handleBrowseWorkspace(w http.ResponseWriter, r *http.Request) {
	if _, ok := roleOf(r); !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	listing, err := s.sessions.BrowseFolder(r.PathValue("id"), r.URL.Query().Get("path"))
	if err != nil {
		writeSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, listing)
}

// handleWorkspaceFile GET /api/sessions/{id}/workspace/file?path=：文件内容。
func (s *Server) handleWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	if _, ok := roleOf(r); !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	path, err := s.sessions.WorkspaceFilePath(r.PathValue("id"), r.URL.Query().Get("path"))
	if err != nil {
		writeSessionError(w, err)
		return
	}
	w.Header().Set("Content-Type", workspaceContentType(path))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, path)
}

// handleOpenWorkspaceFile POST /api/sessions/{id}/workspace/open：用本机应用打开工作区里的文件或文件夹。
// body {path, mode}：default 用系统默认应用（文件夹就是用文件管理器打开）、reveal 在文件夹里显示。
func (s *Server) handleOpenWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	if !requireBrowserPage(w, r) {
		return
	}
	if _, ok := roleOf(r); !ok {
		writeAPIError(w, http.StatusForbidden, roleRequired)
		return
	}
	body, err := readBodyJSON(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, err.Error()))
		return
	}
	rel, _ := body["path"].(string)
	mode, _ := body["mode"].(string)
	if mode != "default" && mode != "reveal" {
		writeAPIError(w, http.StatusBadRequest, apiError(contract.ErrInvalidInput, "mode 只能是 default 或 reveal"))
		return
	}
	id := r.PathValue("id")
	path, fileErr := s.sessions.WorkspaceFilePath(id, rel)
	isFolder := false
	if fileErr != nil {
		// 不是能预览的文件：可能是文件夹（或者太大的文件，仍然可以交给本机应用）
		folder, folderErr := s.sessions.WorkspaceFolderPath(id, rel)
		if folderErr != nil {
			writeSessionError(w, fileErr)
			return
		}
		path, isFolder = folder, true
	}
	var openErr error
	switch {
	case mode == "reveal" && !isFolder:
		openErr = s.opts.Reveal(path)
	case isFolder:
		openErr = s.opts.OpenFolder(path)
	default:
		openErr = s.opts.OpenDefault(path)
	}
	if openErr != nil {
		writeJSON(w, http.StatusInternalServerError, internalError("打开失败："+openErr.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": path})
}
