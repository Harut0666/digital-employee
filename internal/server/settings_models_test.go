// T37 模型列表拉取：POST /api/settings/connections/{name}/models 接本地假模型服务（httptest）。
// 拉取只读设置，不写回；错误信息里的 Key 必须脱敏。
package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"digital-employee/internal/contract"
	"digital-employee/internal/settings"
)

const fetchTestKey = "sk-fetch-models-key-5678"

// fakeListService 假的 /models 接口：记录请求头与路径，按 body / status 作答。
type fakeListService struct {
	server *httptest.Server
	status int
	body   string
	// onRequest 在每次请求时调用（用于断言请求内容）
	onRequest func(*http.Request)

	mu     sync.Mutex
	auth   string
	paths  []string
	called int
}

func newFakeListService(t *testing.T, status int, body string) *fakeListService {
	t.Helper()
	service := &fakeListService{status: status, body: body}
	service.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		service.mu.Lock()
		service.called++
		service.auth = r.Header.Get("Authorization")
		service.paths = append(service.paths, r.URL.Path)
		hook := service.onRequest
		service.mu.Unlock()
		if hook != nil {
			hook(r)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(service.status)
		_, _ = io.WriteString(w, service.body)
	}))
	t.Cleanup(service.server.Close)
	return service
}

func (s *fakeListService) snapshot() (calls int, auth string, paths []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.called, s.auth, append([]string{}, s.paths...)
}

// saveConnection 先把连接写进设置（与设置页保存走同一个接口）。
func saveConnectionForFetch(t *testing.T, base, name, payload string) {
	t.Helper()
	status, body, _ := jsonRequest(t, http.MethodPut, base+"/api/settings/connections/"+name, payload, nil)
	if status != 200 {
		t.Fatalf("保存连接失败：%d %s", status, body)
	}
}

func fetchModels(t *testing.T, base, name, payload string) (int, map[string]any, []byte) {
	t.Helper()
	status, body, _ := jsonRequest(t, http.MethodPost, base+"/api/settings/connections/"+name+"/models", payload, nil)
	var reply map[string]any
	decodeJSON(t, body, &reply)
	return status, reply, body
}

func modelListOf(t *testing.T, reply map[string]any) []string {
	t.Helper()
	raw, ok := reply["models"].([]any)
	if !ok {
		t.Fatalf("响应缺少 models 数组：%v", reply)
	}
	models := make([]string, 0, len(raw))
	for _, item := range raw {
		models = append(models, fmt.Sprint(item))
	}
	return models
}

func settingsDocument(t *testing.T, workspace string) contract.SettingsDocument {
	t.Helper()
	return settings.Read(workspace)
}

func TestFetchModelsReturnsListFromSavedConnection(t *testing.T) {
	service := newFakeListService(t, http.StatusOK, `{"data":[{"id":"deepseek-chat","object":"model"},{"id":"deepseek-reasoner"}]}`)
	workspace := t.TempDir()
	ts, _ := newServerWith(t, Options{WorkspaceDir: workspace})
	base := ts.URL
	saveConnectionForFetch(t, base, "probe",
		`{"baseUrl":"`+service.server.URL+`/v1/","apiKey":"`+fetchTestKey+`","models":["old-model"]}`)

	status, reply, body := fetchModels(t, base, "probe", "")
	if status != 200 {
		t.Fatalf("拉取模型应返回 200：%d %s", status, body)
	}
	if reply["ok"] != true {
		t.Fatalf("ok 应为 true：%s", body)
	}
	if got := modelListOf(t, reply); strings.Join(got, ",") != "deepseek-chat,deepseek-reasoner" {
		t.Fatalf("模型列表不对：%s", body)
	}
	calls, auth, paths := service.snapshot()
	if calls != 1 {
		t.Fatalf("应只请求一次 /models：%d", calls)
	}
	if len(paths) != 1 || paths[0] != "/v1/models" {
		t.Fatalf("应请求 {baseUrl}/models（去掉尾斜杠）：%v", paths)
	}
	if auth != "Bearer "+fetchTestKey {
		t.Fatalf("应带 Bearer Key：%q", auth)
	}
	// 拉取结果不写设置
	if strings.Contains(string(body), fetchTestKey) {
		t.Fatalf("响应里不应出现 Key：%s", body)
	}
	conn := settingsDocument(t, workspace)
	if len(conn.Connections) != 1 {
		t.Fatalf("设置里的连接数不应变化：%+v", conn.Connections)
	}
	for _, c := range conn.Connections {
		if c.Name == "probe" && (len(c.Models) != 1 || c.Models[0] != "old-model") {
			t.Fatalf("拉取结果不应写回设置：%+v", c.Models)
		}
	}
}

func TestFetchModelsParsesBothShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"data 形状", `{"object":"list","data":[{"id":"a"},{"id":"b"}]}`, []string{"a", "b"}},
		{"models 字符串数组", `{"models":["x","y","z"]}`, []string{"x", "y", "z"}},
		{"models 对象数组", `{"models":[{"id":"m1"},{"id":"m2"}]}`, []string{"m1", "m2"}},
		{"两种都给且重复", `{"data":[{"id":"a"}],"models":["a","b","", "b"]}`, []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := newFakeListService(t, http.StatusOK, tc.body)
			ts, _ := newServerWith(t, Options{})
			base := ts.URL
			saveConnectionForFetch(t, base, "shape",
				`{"baseUrl":"`+service.server.URL+`","apiKey":"`+fetchTestKey+`","models":["m"]}`)
			status, reply, body := fetchModels(t, base, "shape", "")
			if status != 200 {
				t.Fatalf("应返回 200：%d %s", status, body)
			}
			if got := strings.Join(modelListOf(t, reply), ","); got != strings.Join(tc.want, ",") {
				t.Fatalf("解析结果不对：want %v got %s", tc.want, body)
			}
		})
	}
}

func TestFetchModelsErrorIsRedacted(t *testing.T) {
	// 401：上游报错体里故意回显 Key，响应里必须换成 ***
	service := newFakeListService(t, http.StatusUnauthorized,
		`{"error":{"message":"Incorrect API key provided: `+fetchTestKey+`"}}`)
	ts, _ := newServerWith(t, Options{})
	base := ts.URL
	saveConnectionForFetch(t, base, "badkey",
		`{"baseUrl":"`+service.server.URL+`","apiKey":"`+fetchTestKey+`","models":["m"]}`)

	status, reply, body := fetchModels(t, base, "badkey", "")
	if status != http.StatusOK {
		t.Fatalf("拉取失败应返回 200（ok:false）：%d %s", status, body)
	}
	if reply["ok"] != false {
		t.Fatalf("ok 应为 false：%s", body)
	}
	message, _ := reply["error"].(string)
	if message == "" {
		t.Fatalf("应返回失败原因：%s", body)
	}
	if strings.Contains(string(body), fetchTestKey) {
		t.Fatalf("错误信息里不能含 Key：%s", body)
	}
	if !strings.Contains(message, "401") || !strings.Contains(message, "Incorrect API key") {
		t.Fatalf("错误信息应带状态码和上游说明：%q", message)
	}
	if _, present := reply["models"]; present {
		t.Fatalf("失败时不应返回 models：%s", body)
	}
}

func TestFetchModelsRejectsOversizedResponse(t *testing.T) {
	// 超过 4 MB 的响应：截断后按“响应过大”报错
	big := `{"data":[{"id":"` + strings.Repeat("m", (4<<20)+1024) + `"}]}`
	service := newFakeListService(t, http.StatusOK, big)
	ts, _ := newServerWith(t, Options{})
	base := ts.URL
	saveConnectionForFetch(t, base, "big",
		`{"baseUrl":"`+service.server.URL+`","apiKey":"`+fetchTestKey+`","models":["m"]}`)

	status, reply, body := fetchModels(t, base, "big", "")
	if status != http.StatusOK || reply["ok"] != false {
		t.Fatalf("超大响应应返回 ok:false：%d %.300s", status, body)
	}
	message, _ := reply["error"].(string)
	if !strings.Contains(message, "4 MB") {
		t.Fatalf("应说明响应超过 4 MB：%q", message)
	}
}

func TestFetchModelsWithUnsavedFieldsForNewConnection(t *testing.T) {
	service := newFakeListService(t, http.StatusOK, `{"data":[{"id":"fresh-1"}]}`)
	workspace := t.TempDir()
	ts, _ := newServerWith(t, Options{WorkspaceDir: workspace})
	base := ts.URL

	// 连接还不存在：请求体带 baseUrl 与 apiKey 也能拉取
	payload := `{"baseUrl":"` + service.server.URL + `","apiKey":"` + fetchTestKey + `"}`
	status, reply, body := fetchModels(t, base, "brand-new", payload)
	if status != 200 || reply["ok"] != true {
		t.Fatalf("新建连接应能用请求体里的 baseUrl 拉取：%d %s", status, body)
	}
	if got := strings.Join(modelListOf(t, reply), ","); got != "fresh-1" {
		t.Fatalf("模型列表不对：%s", body)
	}
	// 连接不存在时不能因为拉取而凭空写进设置（工作区里还没有 settings.json）
	if _, err := os.Stat(settings.Path(workspace)); !os.IsNotExist(err) {
		t.Fatalf("拉取不应写设置：%v", err)
	}

	// 只给 apiKey、没有 baseUrl：连接不存在 → 404，且不发请求
	before, _, _ := service.snapshot()
	status, reply, body = fetchModels(t, base, "no-base", `{"apiKey":"`+fetchTestKey+`"}`)
	if status != http.StatusNotFound {
		t.Fatalf("连接不存在且没给 baseUrl 应为 404：%d %s", status, body)
	}
	if after, _, _ := service.snapshot(); after != before {
		t.Fatalf("404 时不应发请求")
	}
	// 非法 baseUrl → 400
	status, _, _ = fetchModels(t, base, "bad-url", `{"baseUrl":"ftp://x"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("非法 baseUrl 应为 400：%d", status)
	}
	// 没 Key → ok:false，不发请求
	before, _, _ = service.snapshot()
	status, reply, body = fetchModels(t, base, "nokey", `{"baseUrl":"`+service.server.URL+`"}`)
	if status != http.StatusOK || reply["ok"] != false || !strings.Contains(fmt.Sprint(reply["error"]), "API Key") {
		t.Fatalf("没 Key 应返回原因：%d %s", status, body)
	}
	if after, _, _ := service.snapshot(); after != before {
		t.Fatalf("没 Key 时不应发请求")
	}
}

func TestFetchModelsTimeout(t *testing.T) {
	release := make(chan struct{})
	service := newFakeListService(t, http.StatusOK, `{"data":[{"id":"slow"}]}`)
	service.onRequest = func(r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}
	t.Cleanup(func() { close(release) })
	ts, _ := newServerWith(t, Options{
		ModelListTimeout: 200 * time.Millisecond,
	})
	base := ts.URL
	saveConnectionForFetch(t, base, "slow",
		`{"baseUrl":"`+service.server.URL+`","apiKey":"`+fetchTestKey+`","models":["m"]}`)

	started := time.Now()
	status, reply, body := fetchModels(t, base, "slow", "")
	if status != http.StatusOK || reply["ok"] != false {
		t.Fatalf("超时应返回 ok:false：%d %s", status, body)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("超时应很快返回：%v", elapsed)
	}
	if message := fmt.Sprint(reply["error"]); message == "" || strings.Contains(message, fetchTestKey) {
		t.Fatalf("超时原因不对：%q", message)
	}
}
