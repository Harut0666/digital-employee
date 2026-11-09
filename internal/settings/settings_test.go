package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"digital-employee/internal/contract"
)

func TestNormalizeDefaultsAndMigration(t *testing.T) {
	// T46：没有 settings.json 时不预置任何厂商的连接，lastModel 也为空
	doc := Normalize(map[string]any{})
	if doc.Version != contract.SettingsVersion || doc.Revision != 0 {
		t.Fatalf("默认文档 version/revision 不对：%d/%d", doc.Version, doc.Revision)
	}
	if len(doc.Connections) != 0 {
		t.Fatalf("新文档不应有任何连接：%+v", doc.Connections)
	}
	if doc.LastModel != nil {
		t.Fatalf("新文档还没选过模型，lastModel 应为空：%+v", doc.LastModel)
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

// T46：Key 只来自设置里人填写的内容；老文档里的 deepseek 连接按普通连接对待。
func TestViewRedactsKeyIgnoringTheEnvironment(t *testing.T) {
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

	// 设置了 DEEPSEEK_API_KEY 也不影响连接的 Key 和来源
	envView := View(doc, Env{"DEEPSEEK_API_KEY": "sk-env-9999"})
	if envView.Connections[0].Source != "file" || *envView.Connections[0].APIKeyLast4 != "1234" {
		t.Fatalf("环境变量不应影响连接的 Key：%+v", envView.Connections[0])
	}
	encoded, _ = json.Marshal(envView)
	if containsText(string(encoded), "sk-env-9999") || containsText(string(encoded), `"envOverride"`) {
		t.Fatalf("设置视图不应再有环境变量覆盖：%s", encoded)
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

// T46：Key 只来自设置文件；老设置文档里的 deepseek 连接原样保留，读时不补预置内容。
func TestModelSettingsKeepKeysFromTheFileOnly(t *testing.T) {
	dir := t.TempDir()
	if _, err := Write(dir, Normalize(map[string]any{
		"connections": []any{map[string]any{"name": "deepseek", "baseUrl": "https://api.deepseek.com", "apiKey": "file-key", "models": []any{"deepseek-chat"}}},
	})); err != nil {
		t.Fatal(err)
	}
	plain := LoadModelSettings(dir, Env{})
	if plain.Connections[0].APIKey != "file-key" {
		t.Fatalf("没有环境变量时应读文件 Key：%q", plain.Connections[0].APIKey)
	}
	withEnv := LoadModelSettings(dir, Env{"DEEPSEEK_API_KEY": "env-key"})
	if withEnv.Connections[0].APIKey != "file-key" {
		t.Fatalf("环境变量不应覆盖文件里的 Key：%q", withEnv.Connections[0].APIKey)
	}
}

// T46：全新应用目录没有任何连接，lastModel 为空；没有 settings.json 时也不补预置连接。
func TestFreshDirectoryHasNoConnections(t *testing.T) {
	dir := t.TempDir()
	doc := Read(dir)
	if len(doc.Connections) != 0 || doc.LastModel != nil {
		t.Fatalf("全新目录应没有连接、lastModel 为空：%+v %+v", doc.Connections, doc.LastModel)
	}
	models := LoadModelSettings(dir, Env{})
	if len(models.Connections) != 0 || models.Default != (contract.ModelRef{}) {
		t.Fatalf("全新目录的模型设置为空：%+v", models)
	}
}

// T46：老设置文档读进来再写回，连接（名称、地址、Key、模型列表）一个字都不变。
func TestLegacyDeepSeekDocumentRoundTrips(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"version":2,"revision":3,"connections":[{"name":"deepseek","baseUrl":"https://api.deepseek.com","apiKey":"sk-old-key-7777","models":["deepseek-chat","deepseek-reasoner"]}],"lastModel":{"connection":"deepseek","model":"deepseek-reasoner"}}`
	if err := os.WriteFile(Path(dir), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := Read(dir)
	if len(doc.Connections) != 1 {
		t.Fatalf("老文档的连接数不对：%+v", doc.Connections)
	}
	conn := doc.Connections[0]
	if conn.Name != "deepseek" || conn.BaseURL != "https://api.deepseek.com" || conn.APIKey != "sk-old-key-7777" ||
		len(conn.Models) != 2 || conn.Models[0] != "deepseek-chat" || conn.Models[1] != "deepseek-reasoner" {
		t.Fatalf("老文档的连接应原样保留：%+v", conn)
	}
	if doc.LastModel == nil || *doc.LastModel != (contract.ModelRef{Connection: "deepseek", Model: "deepseek-reasoner"}) {
		t.Fatalf("老文档的 lastModel 应原样保留：%+v", doc.LastModel)
	}
	if _, err := Write(dir, doc); err != nil {
		t.Fatal(err)
	}
	raw, err := ReadRawObject(dir)
	if err != nil {
		t.Fatal(err)
	}
	conns, _ := raw["connections"].([]any)
	if len(conns) != 1 {
		t.Fatalf("写回后连接数不对：%v", raw["connections"])
	}
	written := conns[0].(map[string]any)
	if written["name"] != "deepseek" || written["baseUrl"] != "https://api.deepseek.com" || written["apiKey"] != "sk-old-key-7777" {
		t.Fatalf("写回后连接应原样保留：%v", written)
	}
	if models, _ := written["models"].([]any); len(models) != 2 || models[1] != "deepseek-reasoner" {
		t.Fatalf("写回后模型列表应原样保留：%v", written["models"])
	}
}

func TestSettingsPath(t *testing.T) {
	dir := t.TempDir()
	if Path(dir) != filepath.Join(dir, FileName) {
		t.Fatalf("settings 路径不对：%s", Path(dir))
	}
}
