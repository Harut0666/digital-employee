// 对会改变审核、退回、终止、权限确认状态的接口，额外要求请求来自浏览器页面。
//
// 为什么：前台 Agent 的 shell 工具能在本机运行命令，命令里可以 curl 本服务的 HTTP 接口并自带 x-role: reviewer 去“审核通过”
// （见 ADR-0007 增补）。浏览器页面发出的请求会自动带 Sec-Fetch-Site: same-origin（以及与 Host 相同的 Origin），
// 命令行 curl 默认两个都不带，所以要求其一，能挡住“随手一条 curl”。
//
// 这只是提高门槛，不是防护：Sec-Fetch-Site 和 Origin 都是请求头，命令行里想伪造就能伪造。
// 真正的边界是 shell 的权限模式（plan 拒绝、其余每次问）和说话人角色关口。
package server

import (
	"net/http"
	"net/url"
	"strings"
)

// errBrowserOnly 缺少浏览器同源标记时的错误。
var errBrowserOnly = apiError("browser_origin_required", "这个操作只接受来自界面（浏览器页面）的请求：请求需带 Sec-Fetch-Site: same-origin，或与服务地址相同的 Origin。请在界面上点按钮操作")

// fromBrowserPage 请求像是本服务自己的页面发出的。
func fromBrowserPage(r *http.Request) bool {
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "same-origin") {
		return true
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" || origin == "null" {
		return false
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host != "" && strings.EqualFold(parsed.Host, r.Host)
}

// requireBrowserPage 不是来自页面的请求直接回 403 并返回 false。
func requireBrowserPage(w http.ResponseWriter, r *http.Request) bool {
	if fromBrowserPage(r) {
		return true
	}
	writeAPIError(w, http.StatusForbidden, errBrowserOnly)
	return false
}
