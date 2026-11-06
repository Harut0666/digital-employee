// 前台 Agent 的对话接口（T12 的可砍范围项）：先返回最小实现，契约形状保持不变。
package server

import "net/http"

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionId")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "stub": "chat", "sessionId": sessionID})
}
