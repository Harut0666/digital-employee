// 文件响应：交付物下载与前端静态资源托管（对应 packages/server/src/files.ts）。
package server

import (
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"digital-employee/internal/contract"
)

var contentTypes = map[string]string{
	".docx":        "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".md":          "text/markdown; charset=utf-8",
	".json":        "application/json; charset=utf-8",
	".jsonl":       "application/x-ndjson; charset=utf-8",
	".html":        "text/html; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".webp":        "image/webp",
	".ico":         "image/x-icon",
	".gif":         "image/gif",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".ttf":         "font/ttf",
	".map":         "application/json; charset=utf-8",
	".txt":         "text/plain; charset=utf-8",
	".wasm":        "application/wasm",
	".webmanifest": "application/manifest+json",
}

func contentTypeOf(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if value, ok := contentTypes[ext]; ok {
		return value
	}
	if value := mime.TypeByExtension(ext); value != "" {
		return value
	}
	return "application/octet-stream"
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// serveStatic 托管静态资源；找不到具体文件时回退到 index.html（前端路由）。
// 目录穿越、带扩展名的缺失资源都返回 false，交给调用方 404。
func serveStatic(w http.ResponseWriter, r *http.Request, distDir string) bool {
	root, err := filepath.Abs(distDir)
	if err != nil {
		return false
	}
	rawPath := r.URL.Path
	decoded, err := url.PathUnescape(rawPath)
	if err != nil {
		return false
	}
	rel := strings.TrimPrefix(decoded, "/")
	target := filepath.Join(root, filepath.FromSlash(rel))
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	// 只允许 root 内部：target 必须是 root 或 root 下的路径
	if targetAbs != root && !strings.HasPrefix(targetAbs, root+string(filepath.Separator)) {
		return false
	}
	if isFile(targetAbs) {
		serveFile(w, r, targetAbs)
		return true
	}
	// 带扩展名的请求（缺失的资源文件）不回退，避免把 index.html 当成 js 返回
	if filepath.Ext(rel) != "" {
		return false
	}
	index := filepath.Join(root, "index.html")
	if !isFile(index) {
		return false
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeFile(w, r, index)
	return true
}

func serveFile(w http.ResponseWriter, r *http.Request, path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	w.Header().Set("Content-Type", contentTypeOf(path))
	http.ServeFile(w, r, path)
	_ = info
}

// deliverableDownloadName 交付物的下载名：员工配置有模板时按模板，否则用磁盘上的文件名。
// 检验记录不是交付给业务的文件，保持原名。模板取当前员工配置，改模板后已交付的任务也用新名字。
func (s *Server) deliverableDownloadName(snapshot *contract.TaskSnapshot, ref *contract.DeliverableRef) string {
	fallback := filepath.Base(ref.Path)
	if ref.Kind == contract.DeliverableQCRecord {
		return fallback
	}
	employee, err := s.opts.LoadEmployee(filepath.Join(s.opts.EmployeesDir, snapshot.Employee))
	if err != nil || employee.Workflow.DeliverableName == "" {
		return fallback
	}
	name, ok := contract.FillNameTemplate(employee.Workflow.DeliverableName, snapshot.Form)
	if !ok {
		return fallback
	}
	return name + filepath.Ext(ref.Path)
}

// downloadResponse 下载交付物，name 是响应里的文件名；文件不存在返回 false。
func downloadResponse(w http.ResponseWriter, r *http.Request, path, name string) bool {
	if !isFile(path) {
		return false
	}
	encoded := strings.ReplaceAll(url.QueryEscape(name), "+", "%20")
	w.Header().Set("Content-Type", contentTypeOf(path))
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+encoded)
	http.ServeFile(w, r, path)
	return true
}
