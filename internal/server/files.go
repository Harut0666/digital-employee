// 文件响应：交付物下载与前端静态资源托管（对应 packages/server/src/files.ts）。
package server

import (
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

// placeholderPattern 下载名模板里的 {字段}。
var placeholderPattern = regexp.MustCompile(`\{([^{}]+)\}`)

// unsafeNameChars 文件名里不能出现的字符：表单是人填的，填出来的名字不能变成路径。
var unsafeNameChars = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)

// scalarText 表单里的字符串或数字写进文件名时的样子。
func scalarText(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, strings.TrimSpace(typed) != ""
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	}
	return "", false
}

// formField 取表单字段：先看顶层，再看对象字段里的一层（{year} 能取到 period.year）。
func formField(form map[string]any, name string) (string, bool) {
	if text, ok := scalarText(form[name]); ok {
		return text, true
	}
	keys := make([]string, 0, len(form))
	for key := range form {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if nested, ok := form[key].(map[string]any); ok {
			if text, ok := scalarText(nested[name]); ok {
				return text, true
			}
		}
	}
	return "", false
}

// fillNameTemplate 用表单字段填模板；有字段填不上时返回 false，不交付残缺的名字。
func fillNameTemplate(template string, form map[string]any) (string, bool) {
	complete := true
	filled := placeholderPattern.ReplaceAllStringFunc(template, func(placeholder string) string {
		text, ok := formField(form, placeholder[1:len(placeholder)-1])
		complete = complete && ok
		return text
	})
	filled = strings.TrimRight(unsafeNameChars.ReplaceAllString(filled, "_"), ". ")
	return filled, complete && filled != ""
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
	name, ok := fillNameTemplate(employee.Workflow.DeliverableName, snapshot.Form)
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
