// 拉取模型的 HTTP 客户端：上限、超时与脱敏（HTTP 主接缝见 internal/server/settings_models_test.go）。
package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchModelsDefaults(t *testing.T) {
	if ModelListTimeout != 15*time.Second {
		t.Fatalf("拉取超时应为 15 秒：%v", ModelListTimeout)
	}
	if ModelListMaxBytes != 4<<20 {
		t.Fatalf("响应上限应为 4 MB：%d", ModelListMaxBytes)
	}
}

func TestFetchModelsRequestShape(t *testing.T) {
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	defer server.Close()

	models, err := FetchModels(context.Background(), server.URL+"/v1///", "sk-unit-1234", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0] != "m1" {
		t.Fatalf("模型列表不对：%v", models)
	}
	if gotPath != "/v1/models" || gotAuth != "Bearer sk-unit-1234" {
		t.Fatalf("请求不对：path=%q auth=%q", gotPath, gotAuth)
	}
}

func TestFetchModelsExactLimitIsAccepted(t *testing.T) {
	// 恰好 4 MB 的合法响应不报错：在 JSON 后面补空格到上限
	base := `{"models":["m"]}`
	body := base + strings.Repeat(" ", ModelListMaxBytes-len(base))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	models, err := FetchModels(context.Background(), server.URL, "sk-unit-1234", 5*time.Second)
	if err != nil {
		t.Fatalf("恰好等于上限的响应不应报错：%v", err)
	}
	if len(models) != 1 || models[0] != "m" {
		t.Fatalf("模型列表不对：%v", models)
	}

	// 多一个字节就报“超过 4 MB”（错误链里能认出 ErrModelListTooLarge，文字已脱敏）
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body + " "))
	}))
	defer server2.Close()
	_, err = FetchModels(context.Background(), server2.URL, "sk-unit-1234", 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "超过 4 MB") {
		t.Fatalf("超过上限应报错：%v", err)
	}
}

func TestFetchModelsErrorsAreRedactedAndSingleLine(t *testing.T) {
	// 上游 500 里回显 Key，且带换行：错误信息要脱敏、压成一行
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("key=sk-unit-1234\n内部错误"))
	}))
	defer server.Close()
	_, err := FetchModels(context.Background(), server.URL, "sk-unit-1234", 5*time.Second)
	if err == nil {
		t.Fatal("应返回错误")
	}
	if strings.Contains(err.Error(), "sk-unit-1234") || strings.Contains(err.Error(), "\n") {
		t.Fatalf("错误信息要脱敏且单行：%q", err.Error())
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("应带状态码：%q", err.Error())
	}
}

func TestFetchModelsTimeoutAndContextCancel(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)

	_, err := FetchModels(context.Background(), server.URL, "sk-unit-1234", 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("应按超时报错：%v", err)
	}

	// 请求方已取消（比如页面关掉了）：不发请求，立即返回
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() {
		_, err := FetchModels(ctx, server.URL, "sk-unit-1234", 5*time.Second)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("已取消的请求应报错")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("已取消的请求应立即返回")
	}
}

func TestFetchModelsWithoutKeyDoesNotCallServer(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer server.Close()
	_, err := FetchModels(context.Background(), server.URL, "", time.Second)
	if err == nil || !strings.Contains(err.Error(), "API Key") {
		t.Fatalf("没有 Key 应直接报错：%v", err)
	}
	if called {
		t.Fatal("没有 Key 时不应发请求")
	}
}
