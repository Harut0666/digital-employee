// T46 设置不再预置厂商：全部从 HTTP 主接缝验证。
//
// 全新应用目录里设置没有任何连接、lastModel 为空；DEEPSEEK_API_KEY 不影响任何连接的 Key；
// 老设置文档里的 deepseek 连接原样保留；没有任何连接时提交被拦下并提示“还没有配置模型连接”；
// 添加连接 → 拉取模型 → 勾选保存 → 能提交，整条路走通。模型服务用本地假服务（httptest），不连真实接口。
package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"digital-employee/internal/agent"
	"digital-employee/internal/contract"
	"digital-employee/internal/settings"
)

const newVendorKey = "sk-new-vendor-key-2468"

// decodeJSONBody 响应体解析成任意 JSON。
func decodeJSONBody(t *testing.T, data []byte) map[string]any {
	t.Helper()
	out := map[string]any{}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("响应不是 JSON 对象：%v\n%s", err, data)
	}
	return out
}

// settingsView GET /api/settings。
func (h *harness) settingsView() map[string]any {
	h.t.Helper()
	status, data := h.send(http.MethodGet, "/api/settings", "", "", nil)
	if status != http.StatusOK {
		h.t.Fatalf("读取设置失败：%d %s", status, data)
	}
	return decodeJSONBody(h.t, data)
}

// vendoredModel 按顺序作答的假模型服务：/models 给出模型列表，/chat/completions 依次交出排好的回答。
// 连接的 baseUrl 是 {服务}/{连接名}，记录每次请求走的连接名与模型名。
type vendoredModel struct {
	server *httptest.Server

	mu      sync.Mutex
	turns   []scriptedCall
	served  int
	models  []string
	fetches int
	calls   []modelCall
}

// scriptedCall 一次请求的回答：期望的模型名和要调用的工具。
type scriptedCall struct {
	model string
	calls []toolCall
}

type toolCall struct {
	name string
	args map[string]any
}

func newVendoredModel(t *testing.T, models ...string) *vendoredModel {
	t.Helper()
	service := &vendoredModel{models: models}
	service.server = httptest.NewServer(http.HandlerFunc(service.serve))
	t.Cleanup(service.server.Close)
	return service
}

// script 按工作流里判断步骤的顺序排好回答（剧本里的工具调用原样作答）。
func (s *vendoredModel) script(model string, script agent.ModelScript, steps ...string) {
	calls := []scriptedCall{}
	for _, step := range steps {
		for _, turn := range script[step] {
			item := scriptedCall{model: model}
			for _, call := range turn.ToolCalls {
				item.calls = append(item.calls, toolCall{name: call.Name, args: call.Arguments})
			}
			calls = append(calls, item)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turns = calls
	s.served = 0
	s.calls = nil
}

// urlOf 新建连接时的 Base URL：带连接名路径段。
func (s *vendoredModel) urlOf(connection string) string { return s.server.URL + "/" + connection }

func (s *vendoredModel) fetchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fetches
}

func (s *vendoredModel) called() []modelCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]modelCall{}, s.calls...)
}

func (s *vendoredModel) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if strings.HasSuffix(r.URL.Path, "/models") {
		s.mu.Lock()
		s.fetches++
		list := make([]map[string]any, 0, len(s.models))
		for _, model := range s.models {
			list = append(list, map[string]any{"id": model, "object": "model"})
		}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": list})
		return
	}
	var parsed struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &parsed)
	connection := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0]
	s.mu.Lock()
	s.calls = append(s.calls, modelCall{connection: connection, model: parsed.Model, body: body})
	index := s.served
	s.served++
	turns := append([]scriptedCall{}, s.turns...)
	s.mu.Unlock()
	if parsed.Model != "vendor-b" {
		http.Error(w, "请求的模型不是新连接里勾选的 vendor-b："+parsed.Model, http.StatusBadRequest)
		return
	}
	if index >= len(turns) {
		http.Error(w, "剧本已用完："+parsed.Model, http.StatusInternalServerError)
		return
	}
	step := turns[index]
	if step.model != parsed.Model {
		http.Error(w, fmt.Sprintf("期望模型 %s，实际 %s", step.model, parsed.Model), http.StatusBadRequest)
		return
	}
	calls := []map[string]any{}
	for at, call := range step.calls {
		arguments, _ := json.Marshal(call.args)
		calls = append(calls, map[string]any{
			"id": fmt.Sprintf("call_%d_%d", index, at), "type": "function",
			"function": map[string]any{"name": call.name, "arguments": string(arguments)},
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{
		"message":       map[string]any{"content": "", "tool_calls": calls},
		"finish_reason": "tool_calls",
	}}})
}

// postJSONBody 发一个 JSON 请求，返回状态码和响应体。
func (h *harness) postJSONBody(method, path string, role contract.Role, body any) (int, []byte) {
	h.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.send(method, path, role, "application/json", bytes.NewReader(raw))
}

// ---------------------------------------------------------------- 空状态

func TestFreshAppDirHasNoConnections(t *testing.T) {
	h := newHarness(t, nil, withFreshAppDir())

	view := h.settingsView()
	if connections, ok := view["connections"].([]any); !ok || len(connections) != 0 {
		t.Fatalf("全新应用目录不应有任何连接：%v", view["connections"])
	}
	if value, present := view["lastModel"]; !present || value != nil {
		t.Fatalf("全新应用目录的 lastModel 应为 null：%v（present=%v）", value, present)
	}
	if _, present := view["envOverride"]; present {
		t.Fatalf("设置视图里不应再有环境变量覆盖：%v", view)
	}
	// 设置文档视图同样为空
	status, data := h.send(http.MethodGet, "/api/settings/app", "", "", nil)
	if status != http.StatusOK {
		t.Fatalf("读取设置文档失败：%d %s", status, data)
	}
	doc := decodeJSONBody(t, data)
	if connections, _ := doc["connections"].([]any); len(connections) != 0 {
		t.Fatalf("设置文档视图不应有连接：%v", doc["connections"])
	}
	if value, present := doc["lastModel"]; !present || value != nil {
		t.Fatalf("设置文档视图的 lastModel 应为 null：%v", value)
	}
	// 会话里也没有可选模型
	if options := h.modelOptions(); len(options) != 0 {
		t.Fatalf("没有连接时应没有可选模型：%+v", options)
	}
}

// 环境变量 DEEPSEEK_API_KEY 不改变任何连接的 Key，也不改变会话可选的模型。
func TestEnvironmentKeyDoesNotChangeAnyConnectionInTheView(t *testing.T) {
	h := newHarness(t, nil, withFreshAppDir(), withEnv(map[string]string{"DEEPSEEK_API_KEY": "sk-from-env-9999"}))
	legacy := `{"version":2,"revision":0,"connections":[{"name":"deepseek","baseUrl":"https://api.deepseek.com","apiKey":"sk-legacy-key-4321","models":["deepseek-chat"]},{"name":"qwen","baseUrl":"http://127.0.0.1:1/v1","models":["qwen-plus"]}]}`
	if err := writeAppSettings(h, legacy); err != nil {
		t.Fatal(err)
	}
	status, data := h.send(http.MethodGet, "/api/settings", "", "", nil)
	if status != http.StatusOK {
		t.Fatalf("读设置失败：%d %s", status, data)
	}
	if strings.Contains(string(data), "sk-from-env-9999") {
		t.Fatalf("环境变量里的 Key 不应出现在设置里：%s", data)
	}
	view := decodeJSONBody(t, data)
	if _, present := view["envOverride"]; present {
		t.Fatalf("设置视图里不应再有“环境变量覆盖”：%s", data)
	}
	connections := view["connections"].([]any)
	if len(connections) != 2 {
		t.Fatalf("连接数 = %d", len(connections))
	}
	deepseek := connections[0].(map[string]any)
	if deepseek["source"] != "file" || deepseek["apiKeyLast4"] != "4321" {
		t.Fatalf("deepseek 连接的 Key 不应被环境变量覆盖：%v", deepseek)
	}
	qwen := connections[1].(map[string]any)
	if qwen["hasKey"] != false || qwen["source"] != "none" {
		t.Fatalf("没有 Key 的连接不应因为环境变量而有 Key：%v", qwen)
	}
	if options := h.modelOptions(); len(options) != 1 || options[0].Name != "deepseek" {
		t.Fatalf("可选的模型只来自配了 Key 的连接：%+v", options)
	}
}

// ---------------------------------------------------------------- 完整流程

// 添加连接 → 拉取模型 → 勾选保存 → 会话能提交。
// 判断步骤的模型由剧本模型作答（演示模式不连真实接口）；任务记下的是新连接上的那个模型。
func TestAddConnectionFetchModelsSaveThenSubmit(t *testing.T) {
	service := newVendoredModel(t, "vendor-a", "vendor-b")
	h := newHarness(t, caseScript(t, "A-01"), withFreshAppDir())

	// 1. 没有连接时提交被拦下
	created := h.createSession()
	h.confirmed(created.ID, harnessEmployee)
	if status, data := h.uploadTo(created.ID, a01Files(t)); status != http.StatusOK {
		t.Fatalf("上传失败：%d %s", status, data)
	}
	status, data := h.trySubmit(created.ID, contract.RoleInitiator, week44Form)
	if status != http.StatusPreconditionFailed {
		t.Fatalf("没有连接时提交应被拦下：%d %s", status, data)
	}
	blocked := decode[preflightReply](t, data)
	if blocked.Error != contract.ErrModelNotConfigured || !strings.Contains(blocked.Message, "还没有配置模型连接") {
		t.Fatalf("提示应为“还没有配置模型连接”：%+v", blocked)
	}
	if blocked.Settings.Section != "models" || blocked.Settings.Label == "" {
		t.Fatalf("应带设置入口：%+v", blocked.Settings)
	}

	// 2. 新建连接先拉取模型（还没保存）
	status, data = h.postJSONBody(http.MethodPost, "/api/settings/connections/我的厂商/models", contract.RoleInitiator,
		map[string]any{"baseUrl": service.urlOf("我的厂商"), "apiKey": newVendorKey})
	if status != http.StatusOK {
		t.Fatalf("拉取模型失败：%d %s", status, data)
	}
	fetched := decodeJSONBody(t, data)
	if fetched["ok"] != true {
		t.Fatalf("拉取模型应成功：%s", data)
	}
	if got := strings.Join(joinStrings(fetched["models"]), ","); got != "vendor-a,vendor-b" {
		t.Fatalf("拉取的模型 = %q", got)
	}
	if service.fetchCount() != 1 {
		t.Fatalf("应请求一次 /models：%d", service.fetchCount())
	}
	if options := h.modelOptions(); len(options) != 0 {
		t.Fatalf("还没保存时不应有可选模型：%+v", options)
	}

	// 3. 勾选后保存连接
	status, data = h.postJSONBody(http.MethodPut, "/api/settings/connections/我的厂商", contract.RoleInitiator,
		map[string]any{"baseUrl": service.urlOf("我的厂商"), "apiKey": newVendorKey, "models": []string{"vendor-b"}})
	if status != http.StatusOK {
		t.Fatalf("保存连接失败：%d %s", status, data)
	}
	if strings.Contains(string(data), newVendorKey) {
		t.Fatalf("响应里不应出现明文 Key：%s", data)
	}
	saved := decodeJSONBody(t, data)["connection"].(map[string]any)
	if saved["name"] != "我的厂商" || saved["apiKeyLast4"] != "2468" || saved["source"] != "file" {
		t.Fatalf("保存后的连接视图 = %v", saved)
	}
	if options := h.modelOptions(); len(options) != 1 || options[0].Name != "我的厂商" || strings.Join(options[0].Models, ",") != "vendor-b" {
		t.Fatalf("保存后会话可选模型 = %+v", options)
	}
	if view := h.settingsView(); len(view["connections"].([]any)) != 1 {
		t.Fatalf("设置里应有这一个连接：%v", view["connections"])
	}

	// 4. 能提交：任务用新连接上的 vendor-b
	submitted := h.submit(created.ID, week44Form)
	taskID := submitted.Session.TaskID
	expectState(t, h.settled(taskID), contract.StateAwaitingReview)
	created0 := h.taskEvents(taskID)[0]
	if created0.Type != contract.EventTaskCreated || created0.Model == nil ||
		*created0.Model != (contract.ModelRef{Connection: "我的厂商", Model: "vendor-b"}) {
		t.Fatalf("任务应记下新连接的模型：%+v", created0.Model)
	}
	// 拉取过模型、但保存的是勾选结果；连接地址带的那个假服务没被用于任务调用
	if service.fetchCount() != 1 {
		t.Fatalf("应只请求一次 /models：%d", service.fetchCount())
	}
}

// joinStrings JSON 读出来的字符串数组。
func joinStrings(value any) []string {
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, _ := item.(string)
		out = append(out, text)
	}
	return out
}

// writeAppSettings 把一份设置文档原样写进装置的应用目录（模拟老用户已有的 settings.json）。
func writeAppSettings(h *harness, document string) error {
	return os.WriteFile(settings.Path(h.appDirs()), []byte(document), 0o644)
}

// 老设置文档里的 deepseek 连接原样保留：读设置、再加一个别的连接，deepseek 的名称、地址、Key、模型列表都不变，
// 设置文档里没有 connections 键的老文档读出来也不补回任何预置内容。
func TestLegacyDeepSeekConnectionIsKeptAsIs(t *testing.T) {
	h := newHarness(t, nil, withFreshAppDir())
	legacy := `{"version":2,"revision":0,"connections":[{"name":"deepseek","baseUrl":"https://api.deepseek.com","apiKey":"sk-legacy-key-4321","models":["deepseek-chat","deepseek-reasoner"]}]}`
	if err := writeAppSettings(h, legacy); err != nil {
		t.Fatal(err)
	}
	want := contract.ModelConnection{Name: "deepseek", BaseURL: "https://api.deepseek.com", APIKey: "sk-legacy-key-4321", Models: []string{"deepseek-chat", "deepseek-reasoner"}}
	check := func(stage string) {
		t.Helper()
		conns := settings.Read(h.appDirs()).Connections
		if len(conns) == 0 || fmt.Sprint(conns[0]) != fmt.Sprint(want) {
			t.Fatalf("%s：deepseek 连接被改动了：%+v", stage, conns)
		}
	}
	check("读之前")
	if view := h.settingsView(); len(view["connections"].([]any)) != 1 {
		t.Fatalf("应只有老用户自己的连接：%v", view["connections"])
	}
	check("读之后")
	status, data := h.postJSONBody(http.MethodPut, "/api/settings/connections/other", contract.RoleInitiator,
		map[string]any{"baseUrl": "http://127.0.0.1:1/v1", "apiKey": "sk-other-1111", "models": []string{"m1"}})
	if status != http.StatusOK {
		t.Fatalf("加连接失败：%d %s", status, data)
	}
	check("写之后")
	if conns := settings.Read(h.appDirs()).Connections; len(conns) != 2 {
		t.Fatalf("连接数 = %d", len(conns))
	}
}
