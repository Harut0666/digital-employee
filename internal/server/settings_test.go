package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"digital-employee/internal/contract"
)

func newServerWith(t *testing.T, options Options) (*httptest.Server, *fakeEngine) {
	t.Helper()
	engine := newFakeEngine()
	options.Engine = engine
	if options.WorkspaceDir == "" {
		options.WorkspaceDir = t.TempDir()
	}
	if options.EmployeesDir == "" {
		options.EmployeesDir = filepath.Join("..", "..", "employees")
	}
	if options.Env == nil {
		options.Env = map[string]string{}
	}
	if options.Logger == nil {
		options.Logger = NewLogger(LevelError, func(string, string) {})
	}
	srv := New(options)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, engine
}

func jsonRequest(t *testing.T, method, url, body string, headers map[string]string) (int, []byte, http.Header) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data, response.Header
}

func TestSettingsConnectionsCRUD(t *testing.T) {
	ts, _ := newServerWith(t, Options{})
	base := ts.URL

	status, body, _ := jsonRequest(t, http.MethodGet, base+"/api/settings", "", nil)
	if status != 200 {
		t.Fatalf("GET /api/settings：%d %s", status, body)
	}
	var view map[string]any
	decodeJSON(t, body, &view)
	connections := view["connections"].([]any)
	if len(connections) != 1 {
		t.Fatalf("默认连接数应为 1：%s", body)
	}
	if connections[0].(map[string]any)["isDefault"] != true {
		t.Fatalf("默认连接标记不对：%s", body)
	}

	// 非法 Base URL
	status, body, _ = jsonRequest(t, http.MethodPut, base+"/api/settings/connections/bad", `{"baseUrl":"ftp://x","models":["m"]}`, nil)
	if status != 400 {
		t.Fatalf("非法 Base URL 应为 400：%d %s", status, body)
	}
	// 新增连接缺 models
	status, body, _ = jsonRequest(t, http.MethodPut, base+"/api/settings/connections/new", `{"baseUrl":"https://api.example.com"}`, nil)
	if status != 400 {
		t.Fatalf("缺 models 应为 400：%d %s", status, body)
	}
	// 新增连接（带 Key 与高级参数）
	payload := `{"baseUrl":"https://api.example.com","apiKey":"sk-abcdef-9876","models":["m-1","m-2"],"timeoutMs":30000,"maxRetries":2,"modelOptions":{"m-1":{"contextWindow":32000}}}`
	status, body, _ = jsonRequest(t, http.MethodPut, base+"/api/settings/connections/new", payload, nil)
	if status != 200 {
		t.Fatalf("保存连接失败：%d %s", status, body)
	}
	var saved map[string]any
	decodeJSON(t, body, &saved)
	connection := saved["connection"].(map[string]any)
	if connection["hasKey"] != true || connection["apiKeyLast4"] != "9876" || connection["source"] != "file" {
		t.Fatalf("连接脱敏视图不对：%s", body)
	}
	if connection["timeoutMs"].(float64) != 30000 || connection["maxRetries"].(float64) != 2 {
		t.Fatalf("高级参数未保存：%s", body)
	}

	// 默认连接应保持原值（deepseek 仍存在）
	status, body, _ = jsonRequest(t, http.MethodGet, base+"/api/settings/app", "", nil)
	if status != 200 {
		t.Fatalf("GET app：%d %s", status, body)
	}
	var appView map[string]any
	decodeJSON(t, body, &appView)
	if len(appView["connections"].([]any)) != 2 {
		t.Fatalf("连接数应为 2：%s", body)
	}

	// 删除
	status, body, _ = jsonRequest(t, http.MethodDelete, base+"/api/settings/connections/new", "", nil)
	if status != 200 {
		t.Fatalf("删除连接失败：%d %s", status, body)
	}
	status, body, _ = jsonRequest(t, http.MethodDelete, base+"/api/settings/connections/new", "", nil)
	if status != 404 {
		t.Fatalf("删除不存在的连接应为 404：%d %s", status, body)
	}
}

func TestTestConnectionEndpoint(t *testing.T) {
	probed := ""
	ts, _ := newServerWith(t, Options{
		ProbeConnection: func(connection contract.ModelConnection, model string) contract.ModelConnectionTest {
			probed = connection.Name + "/" + model
			return contract.ModelConnectionTest{OK: true, LatencyMS: 42, Model: model}
		},
	})
	base := ts.URL
	payload := `{"baseUrl":"https://api.example.com","apiKey":"sk-test-1234","models":["m-1"]}`
	if status, body, _ := jsonRequest(t, http.MethodPut, base+"/api/settings/connections/probe", payload, nil); status != 200 {
		t.Fatalf("保存连接失败：%d %s", status, body)
	}
	status, body, _ := jsonRequest(t, http.MethodPost, base+"/api/settings/connections/probe/test", `{"model":"m-1"}`, nil)
	if status != 200 {
		t.Fatalf("测试连接失败：%d %s", status, body)
	}
	var result map[string]any
	decodeJSON(t, body, &result)
	if result["ok"] != true || result["latencyMs"].(float64) != 42 || result["model"] != "m-1" {
		t.Fatalf("测试连接结果不对：%s", body)
	}
	if probed != "probe/m-1" {
		t.Fatalf("探针入参不对：%q", probed)
	}

	// 没有 Key 时直接返回可读错误，不发请求
	payload = `{"baseUrl":"https://api.example.com","models":["m-1"]}`
	jsonRequest(t, http.MethodPut, base+"/api/settings/connections/nokey", payload, nil)
	status, body, _ = jsonRequest(t, http.MethodPost, base+"/api/settings/connections/nokey/test", `{}`, nil)
	if status != 200 {
		t.Fatalf("无 Key 测试连接应返回 200：%d %s", status, body)
	}
	decodeJSON(t, body, &result)
	if result["ok"] != false || !strings.Contains(result["error"].(string), "未配置 API Key") {
		t.Fatalf("无 Key 提示不对：%s", body)
	}

	// 连接不存在 → 404
	status, _, _ = jsonRequest(t, http.MethodPost, base+"/api/settings/connections/nope/test", `{}`, nil)
	if status != 404 {
		t.Fatalf("不存在的连接应为 404：%d", status)
	}
}

func TestRoleAndDemoEndpoints(t *testing.T) {
	workspace := t.TempDir()
	ts, _ := newServerWith(t, Options{WorkspaceDir: workspace})
	base := ts.URL

	status, body, _ := jsonRequest(t, http.MethodPut, base+"/api/settings/role", `{"role":"hacker"}`, nil)
	if status != 400 {
		t.Fatalf("非法角色应为 400：%d %s", status, body)
	}
	status, body, _ = jsonRequest(t, http.MethodPut, base+"/api/settings/role", `{"role":"reviewer","revision":0}`, nil)
	if status != 200 {
		t.Fatalf("设置默认角色失败：%d %s", status, body)
	}
	var roleResult map[string]any
	decodeJSON(t, body, &roleResult)
	if roleResult["revision"].(float64) != 1 {
		t.Fatalf("revision 应 +1：%s", body)
	}
	permissions := roleResult["permissions"].(map[string]any)
	if permissions["defaultRole"] != "reviewer" {
		t.Fatalf("默认角色未写入：%s", body)
	}

	// demo：合法保存 + 非法工具名
	status, body, _ = jsonRequest(t, http.MethodPut, base+"/api/settings/demo", `{"toolFaults":{"load_sales":"timeout"},"corruptTraceability":true}`, nil)
	if status != 200 {
		t.Fatalf("保存演示开关失败：%d %s", status, body)
	}
	var demoResult map[string]any
	decodeJSON(t, body, &demoResult)
	demo := demoResult["demo"].(map[string]any)
	if demo["corruptTraceability"] != true {
		t.Fatalf("演示开关未写入：%s", body)
	}
	toolFaults := demo["toolFaults"].(map[string]any)
	if toolFaults["load_sales"] != "timeout" {
		t.Fatalf("工具故障未写入：%s", body)
	}
	status, _, _ = jsonRequest(t, http.MethodPut, base+"/api/settings/demo", `{"toolFaults":{"bogus":"timeout"}}`, nil)
	if status != 400 {
		t.Fatalf("非法工具名应为 400：%d", status)
	}
	// 布防模拟崩溃会写审计
	status, _, _ = jsonRequest(t, http.MethodPut, base+"/api/settings/demo", `{"crashOnNextStep":true}`, nil)
	if status != 200 {
		t.Fatalf("布防模拟崩溃失败：%d", status)
	}
	auditData, err := os.ReadFile(filepath.Join(workspace, AuditFileName))
	if err != nil || !strings.Contains(string(auditData), "simulate-crash") {
		t.Fatalf("审计里应有 simulate-crash：%v %s", err, auditData)
	}
}

func TestExportImportDiagnostics(t *testing.T) {
	workspace := t.TempDir()
	ts, _ := newServerWith(t, Options{WorkspaceDir: workspace})
	base := ts.URL

	status, body, headers := jsonRequest(t, http.MethodGet, base+"/api/settings/export", "", nil)
	if status != 200 {
		t.Fatalf("导出失败：%d %s", status, body)
	}
	if !strings.Contains(headers.Get("Content-Disposition"), "attachment") {
		t.Fatalf("导出应带下载头：%q", headers.Get("Content-Disposition"))
	}
	var exportResult map[string]any
	decodeJSON(t, body, &exportResult)
	if exportResult["redacted"] != true {
		t.Fatalf("默认导出应脱敏：%s", body)
	}
	document := exportResult["document"].(map[string]any)
	if document["version"].(float64) != 2 {
		t.Fatalf("导出文档应为 v2：%s", body)
	}

	// 导入刚导出的文档：应备份并成功
	importBody, _ := json.Marshal(map[string]any{"document": document})
	status, body, _ = jsonRequest(t, http.MethodPost, base+"/api/settings/import", string(importBody), nil)
	if status != 200 {
		t.Fatalf("导入失败：%d %s", status, body)
	}
	var importResult map[string]any
	decodeJSON(t, body, &importResult)
	if importResult["ok"] != true || importResult["backupPath"] == nil {
		t.Fatalf("导入应成功且带备份路径：%s", body)
	}
	if _, err := os.Stat(importResult["backupPath"].(string)); err != nil {
		t.Fatalf("备份文件不存在：%v", err)
	}
	// 非法文档 → 400
	status, _, _ = jsonRequest(t, http.MethodPost, base+"/api/settings/import", `{"version":9}`, nil)
	if status != 400 {
		t.Fatalf("非法导入应为 400：%d", status)
	}

	// 诊断信息
	status, body, _ = jsonRequest(t, http.MethodGet, base+"/api/settings/diagnostics", "", nil)
	if status != 200 {
		t.Fatalf("诊断信息失败：%d %s", status, body)
	}
	var info map[string]any
	decodeJSON(t, body, &info)
	if info["ok"] != true {
		t.Fatalf("诊断信息不对：%s", body)
	}
	app := info["app"].(map[string]any)
	if app["settingsVersion"].(float64) != 2 {
		t.Fatalf("诊断里的设置版本不对：%s", body)
	}
	if _, ok := info["settings"].(map[string]any)["connections"]; !ok {
		t.Fatalf("诊断应包含脱敏设置：%s", body)
	}
	// 导出诊断包
	status, body, _ = jsonRequest(t, http.MethodPost, base+"/api/settings/diagnostics/export", `{}`, nil)
	if status != 200 {
		t.Fatalf("导出诊断包失败：%d %s", status, body)
	}
	var exported map[string]any
	decodeJSON(t, body, &exported)
	if _, err := os.Stat(exported["path"].(string)); err != nil {
		t.Fatalf("诊断包不存在：%v", err)
	}
}

func TestSamplesEndpoints(t *testing.T) {
	workspace := t.TempDir()
	// 工作区里放一份最小公开样例（A-01.json + 附件）
	sampleDir := filepath.Join(workspace, "samples", "cases")
	attachment := filepath.Join(workspace, "samples", "scenario-a", "销售明细.xlsx")
	if err := os.MkdirAll(filepath.Dir(attachment), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(attachment, []byte("xlsx-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	caseJSON := `{"id":"A-01","title":"公开样例","employee":"weekly-report","form":{"period":{"year":2026,"week":44}},"attachments":[{"name":"销售明细.xlsx","path":"../scenario-a/销售明细.xlsx"}]}`
	if err := os.MkdirAll(sampleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sampleDir, "A-01.json"), []byte(caseJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	ts, engine := newServerWith(t, Options{WorkspaceDir: workspace})
	base := ts.URL

	status, body, _ := jsonRequest(t, http.MethodGet, base+"/api/samples", "", nil)
	if status != 200 {
		t.Fatalf("样例列表失败：%d %s", status, body)
	}
	var list map[string]any
	decodeJSON(t, body, &list)
	samples := list["samples"].([]any)
	if len(samples) != 1 {
		t.Fatalf("应有 1 个样例：%s", body)
	}
	sample := samples[0].(map[string]any)
	if sample["id"] != "scenario-a" || sample["employee"] != "weekly-report" {
		t.Fatalf("样例信息不对：%s", body)
	}
	if len(sample["files"].([]any)) != 1 {
		t.Fatalf("样例附件列表不对：%s", body)
	}
	if _, leaked := sample["attachments"]; leaked {
		t.Fatalf("列表不应带 sourcePath：%s", body)
	}

	// 非发起人 → 403
	status, body, _ = jsonRequest(t, http.MethodPost, base+"/api/samples/scenario-a/tasks", "", map[string]string{"x-role": "reviewer"})
	if status != 403 {
		t.Fatalf("非发起人载入样例应为 403：%d %s", status, body)
	}
	// 发起人 → 201，且附件被引擎接收
	status, body, _ = jsonRequest(t, http.MethodPost, base+"/api/samples/scenario-a/tasks", "", map[string]string{"x-role": "initiator"})
	if status != 201 {
		t.Fatalf("载入样例失败：%d %s", status, body)
	}
	var snapshot contract.TaskSnapshot
	decodeJSON(t, body, &snapshot)
	if len(snapshot.Attachments) != 1 || snapshot.Attachments[0].Name != "销售明细.xlsx" {
		t.Fatalf("样例附件未传给引擎：%+v", snapshot.Attachments)
	}
	if len(engine.List()) != 1 {
		t.Fatalf("引擎应收到 1 个任务")
	}
	// 不存在的样例 → 404
	status, _, _ = jsonRequest(t, http.MethodPost, base+"/api/samples/nope/tasks", "", map[string]string{"x-role": "initiator"})
	if status != 404 {
		t.Fatalf("不存在的样例应为 404：%d", status)
	}
}

func TestResetEmployees(t *testing.T) {
	workspace := t.TempDir()
	employeesDir := filepath.Join(workspace, "employees")
	if err := os.MkdirAll(employeesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(employeesDir, "user-edited.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	defaultsDir := t.TempDir()
	defaultEmployee := filepath.Join(defaultsDir, "mini")
	if err := os.MkdirAll(defaultEmployee, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(defaultEmployee, "workflow.yaml"), []byte("id: mini\nname: 小员工\nadmissionStep: admission\nlimits:\n  task:\n    modelCalls: 1\n    durationSec: 10\n  toolRetry:\n    attempts: 1\n    backoffMs: []\nsteps:\n  - id: admission\n    label: 准入\n    kind: compute\n    tool: validate_submission\n"), 0o644)
	ts, _ := newServerWith(t, Options{WorkspaceDir: workspace, EmployeesDir: employeesDir, DefaultEmployeesDir: defaultsDir})

	status, body, _ := jsonRequest(t, http.MethodPost, ts.URL+"/api/settings/workspace/reset-employees", "", nil)
	if status != 200 {
		t.Fatalf("恢复默认员工失败：%d %s", status, body)
	}
	var result map[string]any
	decodeJSON(t, body, &result)
	if result["backupPath"] == nil {
		t.Fatalf("应返回备份路径：%s", body)
	}
	if _, err := os.Stat(result["backupPath"].(string)); err != nil {
		t.Fatalf("备份目录不存在：%v", err)
	}
	employees := result["employees"].([]any)
	if len(employees) != 1 || employees[0] != "mini" {
		t.Fatalf("恢复后的员工列表不对：%s", body)
	}
	if _, err := os.Stat(filepath.Join(employeesDir, "user-edited.txt")); !os.IsNotExist(err) {
		t.Fatalf("恢复默认员工应替换旧目录：%v", err)
	}
	if _, err := os.Stat(filepath.Join(employeesDir, "mini", "workflow.yaml")); err != nil {
		t.Fatalf("默认员工未复制：%v", err)
	}
}
