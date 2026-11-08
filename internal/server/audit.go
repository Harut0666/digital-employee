// 危险操作的审计留痕（Spec 0004、T23）：追加写 <应用目录>/audit.jsonl，一行一条 JSON，所有工作区共用。
//
// 约定：字段顺序固定（time / role / action / target / result / detail）；绝不写请求里的值，
// detail 只放服务端生成的说明且统一脱敏；审计写失败只打 warn，不让业务操作失败。
package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// AuditFileName 审计文件名（应用目录根目录）。
const AuditFileName = "audit.jsonl"

var auditSecretPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)"apiKey"\s*:\s*"[^"]*"`), `"apiKey":"***"`},
	{regexp.MustCompile(`(?i)(apiKey=)[A-Za-z0-9._-]+`), "${1}***"},
	{regexp.MustCompile(`sk-[A-Za-z0-9._-]{4,}`), "sk-***"},
	{regexp.MustCompile(`(?i)(Bearer)\s+[A-Za-z0-9._~+/=-]+`), "${1} ***"},
}

// RedactAuditText 抹掉文本里的密钥形态（审计行与错误说明共用）。
func RedactAuditText(text string) string {
	out := text
	for _, rule := range auditSecretPatterns {
		out = rule.pattern.ReplaceAllString(out, rule.replacement)
	}
	return out
}

// AuditEntry 磁盘上的一行（已归一化、已脱敏）。
type AuditEntry struct {
	Time   string `json:"time"`
	Role   string `json:"role"`
	Action string `json:"action"`
	Target string `json:"target"`
	Result string `json:"result"`
	Detail string `json:"detail,omitempty"`
}

// appendAudit 追加一条审计记录；失败只打 warn。
func appendAudit(appDir string, logger *Logger, entry AuditEntry) {
	entry.Time = time.Now().UTC().Format(time.RFC3339Nano)
	if entry.Detail != "" {
		entry.Detail = RedactAuditText(entry.Detail)
	}
	data, err := json.Marshal(entry)
	if err != nil {
		logger.Warnf("审计写入失败（%s）：%v", entry.Action, err)
		return
	}
	file := filepath.Join(appDir, AuditFileName)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		logger.Warnf("审计写入失败（%s）：%v", entry.Action, err)
		return
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		logger.Warnf("审计写入失败（%s）：%v", entry.Action, err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		logger.Warnf("审计写入失败（%s）：%v", entry.Action, err)
	}
}
