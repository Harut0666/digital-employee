package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"digital-employee/internal/contract"
)

func TestNormalizeDefaultsAndMigration(t *testing.T) {
	doc := Normalize(map[string]any{})
	if doc.Version != contract.SettingsVersion || doc.Revision != 0 {
		t.Fatalf("默认文档 version/revision 不对：%d/%d", doc.Version, doc.Revision)
	}
	if len(doc.Connections) != 1 || doc.Connections[0].Name != DefaultConnectionName {
		t.Fatalf("默认连接不对：%+v", doc.Connections)
	}
	if doc.DefaultConnection != DefaultConnectionName || doc.DefaultModel != DefaultDefaultModel {
		t.Fatalf("默认连接/模型不对：%q %q", doc.DefaultConnection, doc.DefaultModel)
	}

	// v1 文档（无 version）+ 未识别顶层键 + 坏值：合法值保留，坏值退回默认，未知键进 Extra
	doc = Normalize(map[string]any{
		"version":     1,
		"revision":    7,
		"futureKey":   map[string]any{"a": 1},
		"ui":          map[string]any{"theme": "dark", "fontSize": 99},
		"execution":   map[string]any{"modelMaxRetries": 3, "retryBackoffMs": []any{100, 200}},
		"permissions": map[string]any{"sandboxMode": "read-only"},
	})
	if doc.UI.Theme != "dark" {
		t.Fatalf("合法字段应保留：%+v", doc.UI)
	}
	if doc.UI.FontSize != contract.DefaultUISettings.FontSize {
		t.Fatalf("越界字段应退回默认值：%d", doc.UI.FontSize)
	}
	if doc.Revision != 7 {
		t.Fatalf("revision 应保留：%d", doc.Revision)
	}
	if doc.Extra["futureKey"] == nil {
		t.Fatalf("未知顶层键应进 Extra：%+v", doc.Extra)
	}
	if doc.Execution.ModelMaxRetries != 3 || len(doc.Execution.RetryBackoffMS) != 2 {
		t.Fatalf("执行设置未保留：%+v", doc.Execution)
	}
	if doc.Permissions.SandboxMode != "read-only" || doc.Permissions.ApprovalPolicy != "ask" {
		t.Fatalf("权限设置合并不对：%+v", doc.Permissions)
	}
}

func TestWriteIncrementsRevisionAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	written, err := Write(dir, Normalize(map[string]any{"futureKey": "keep"}))
	if err != nil {
		t.Fatal(err)
	}
	if written.Revision != 1 {
		t.Fatalf("写入后 revision 应为 1：%d", written.Revision)
	}
	written, err = Update(dir, func(doc *contract.SettingsDocument) {
		doc.UI.Theme = "light"
	})
	if err != nil {
		t.Fatal(err)
	}
	if written.Revision != 2 {
		t.Fatalf("第二次写入后 revision 应为 2：%d", written.Revision)
	}
	read := Read(dir)
	if read.Revision != 2 || read.UI.Theme != "light" {
		t.Fatalf("回读不对：%+v", read)
	}
	if read.Extra["futureKey"] != "keep" {
		t.Fatalf("未知顶层键应原样保留：%+v", read.Extra)
	}
	// 磁盘上是 v2 文档
	data, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["version"].(float64) != contract.SettingsVersion {
		t.Fatalf("落盘 version 不对：%v", raw["version"])
	}
}

func TestViewRedactsKeyAndEnvOverride(t *testing.T) {
	doc := Normalize(map[string]any{
		"connections": []any{
			map[string]any{"name": "deepseek", "baseUrl": "https://api.deepseek.com", "apiKey": "sk-secret-1234", "models": []any{"deepseek-chat"}},
		},
	})
	view := View(doc, Env{})
	if len(view.Connections) != 1 {
		t.Fatalf("连接数不对：%d", len(view.Connections))
	}
	connection := view.Connections[0]
	if !connection.HasKey || connection.Source != "file" || connection.APIKeyLast4 == nil || *connection.APIKeyLast4 != "1234" {
		t.Fatalf("脱敏视图不对：%+v", connection)
	}
	encoded, _ := json.Marshal(view)
	if containsText(string(encoded), "sk-secret-1234") {
		t.Fatalf("视图里不能出现明文 Key：%s", encoded)
	}

	envView := View(doc, Env{"DEEPSEEK_API_KEY": "sk-env-9999"})
	if envView.Connections[0].Source != "env" || !envView.EnvOverride {
		t.Fatalf("环境变量优先不对：%+v", envView.Connections[0])
	}
	if *envView.Connections[0].APIKeyLast4 != "9999" {
		t.Fatalf("末 4 位应来自环境变量：%v", *envView.Connections[0].APIKeyLast4)
	}
}

func containsText(haystack, needle string) bool {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestParsePatchApplyAndConflict(t *testing.T) {
	patch, err := ParsePatch(map[string]any{
		"revision":  3,
		"ui":        map[string]any{"theme": "dark"},
		"execution": map[string]any{"modelMaxRetries": 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	if patch.Revision != 3 || patch.UI["theme"] != "dark" || patch.Execution["modelMaxRetries"] != 4 {
		t.Fatalf("patch 解析不对：%+v", patch)
	}
	doc := Normalize(map[string]any{"revision": 3})
	doc.UI.FontSize = 16
	if err := ApplyPatch(&doc, patch); err != nil {
		t.Fatal(err)
	}
	if doc.UI.Theme != "dark" || doc.UI.FontSize != 16 {
		t.Fatalf("合并应只改传入字段：%+v", doc.UI)
	}
	if doc.Execution.ModelMaxRetries != 4 {
		t.Fatalf("执行设置未合并：%+v", doc.Execution)
	}

	if _, err := ParsePatch(map[string]any{"ui": map[string]any{"theme": "dark"}}); err == nil {
		t.Fatal("缺 revision 应报错")
	}
	if _, err := ParsePatch(map[string]any{"revision": 0, "ui": map[string]any{"theme": "rainbow"}}); err == nil {
		t.Fatal("非法枚举应报错")
	}
	if _, err := ParsePatch(map[string]any{"revision": 0, "nope": 1}); err == nil {
		t.Fatal("未知字段应报错")
	}
}

func TestValidateImportAndExport(t *testing.T) {
	migrated, err := ValidateImport(map[string]any{
		"version": 1,
		"connections": []any{
			map[string]any{"name": "deepseek", "baseUrl": "https://api.deepseek.com", "models": []any{"deepseek-chat"}},
		},
		"ui": map[string]any{"theme": "dark"},
	})
	if err != nil || !migrated {
		t.Fatalf("v1 导入应允许并标记迁移：%v %v", migrated, err)
	}
	if _, err := ValidateImport(map[string]any{"version": 3}); err == nil {
		t.Fatal("不支持的版本应报错")
	}
	if _, err := ValidateImport(map[string]any{"connections": "nope"}); err == nil {
		t.Fatal("connections 非数组应报错")
	}
	if _, err := ValidateImport(map[string]any{"demo": map[string]any{"toolFaults": map[string]any{"x": "boom"}}}); err == nil {
		t.Fatal("非法故障模式应报错")
	}

	doc := Normalize(map[string]any{
		"connections": []any{map[string]any{"name": "deepseek", "baseUrl": "https://api.deepseek.com", "apiKey": "sk-plain-key", "models": []any{"deepseek-chat"}}},
	})
	redacted := Export(doc, false)
	encoded, _ := json.Marshal(redacted)
	if containsText(string(encoded), "sk-plain-key") {
		t.Fatalf("脱敏导出不应含明文 Key：%s", encoded)
	}
	full := Export(doc, true)
	encoded, _ = json.Marshal(full)
	if !containsText(string(encoded), "sk-plain-key") {
		t.Fatalf("includeKeys 导出应含明文 Key：%s", encoded)
	}
	if _, err := json.Marshal(doc); err != nil {
		t.Fatal(err)
	}
}

func TestModelSettingsEnvOverride(t *testing.T) {
	dir := t.TempDir()
	if _, err := Write(dir, Normalize(map[string]any{
		"connections": []any{map[string]any{"name": "deepseek", "baseUrl": "https://api.deepseek.com", "apiKey": "file-key", "models": []any{"deepseek-chat"}}},
	})); err != nil {
		t.Fatal(err)
	}
	plain := LoadModelSettings(dir, Env{})
	if plain.Connections[0].APIKey != "file-key" {
		t.Fatalf("无环境变量时应读文件 Key：%q", plain.Connections[0].APIKey)
	}
	overridden := LoadModelSettings(dir, Env{"DEEPSEEK_API_KEY": "env-key"})
	if overridden.Connections[0].APIKey != "env-key" {
		t.Fatalf("环境变量应优先：%q", overridden.Connections[0].APIKey)
	}
}

func TestSettingsPath(t *testing.T) {
	dir := t.TempDir()
	if Path(dir) != filepath.Join(dir, FileName) {
		t.Fatalf("settings 路径不对：%s", Path(dir))
	}
}
