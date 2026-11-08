// 模型列表拉取：GET {baseUrl}/models，带 Bearer Key，把返回的模型 ID 列表交给设置页勾选。
//
// 与 real.go 的 /chat/completions 客户端同一风格：连接层最小实现、错误信息里的 Key 一律脱敏。
// 拉取只读：不写设置，由人勾选后保存成连接的模型列表（Spec 0006「模型列表与会话模型」）。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// ModelListTimeout 拉取模型列表的缺省超时。
	ModelListTimeout = 15 * time.Second
	// ModelListMaxBytes 响应体最多读这么多；超过就当作异常响应报错，不截断后硬解析。
	ModelListMaxBytes = 4 << 20
)

// ErrModelListTooLarge 响应体超过 ModelListMaxBytes。
var ErrModelListTooLarge = errors.New("模型列表响应超过 4 MB")

// FetchModels 请求 {baseUrl}/models，返回去重后的模型 ID。
// 失败时返回的错误里已经抹掉了 apiKey（并去掉换行，直接可放进 JSON）。
func FetchModels(ctx context.Context, baseURL, apiKey string, timeout time.Duration) ([]string, error) {
	models, err := fetchModelIDs(ctx, baseURL, apiKey, timeout)
	if err != nil {
		// %w 保留错误链（errors.Is 能认出 ErrModelListTooLarge 等），文字本身已脱敏且压成一行
		return nil, fmt.Errorf("%w", errors.New(RedactAPIKey(strings.Join(strings.Fields(err.Error()), " "), apiKey)))
	}
	return models, nil
}

func fetchModelIDs(ctx context.Context, baseURL, apiKey string, timeout time.Duration) ([]string, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, errors.New("该连接没有配置 Base URL")
	}
	if apiKey == "" {
		return nil, errors.New("未配置 API Key，无法拉取模型")
	}
	if timeout <= 0 {
		timeout = ModelListTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(callCtx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{Timeout: timeout}
	response, err := client.Do(request)
	if err != nil {
		if callCtx.Err() != nil {
			return nil, fmt.Errorf("拉取模型超时（%s）", timeout)
		}
		return nil, err
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(response.Body, ModelListMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > ModelListMaxBytes {
		return nil, ErrModelListTooLarge
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("模型接口返回 %d：%s", response.StatusCode, truncate(strings.TrimSpace(string(raw)), 300))
	}
	models, err := ParseModelList(raw)
	if err != nil {
		return nil, err
	}
	return models, nil
}

// ParseModelList 解析 /models 的两种常见形状，返回去重后的模型 ID：
//   - OpenAI 风格 {"data":[{"id":"..."}]}；
//   - {"models":["..."]}，列表项也可以写成 {"id":"..."} 对象。
//
// 两种都给时先取 data，再追加 models 里 data 没有的。
func ParseModelList(raw []byte) ([]string, error) {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("模型列表响应不是合法的 JSON：%v", err)
	}
	models := []string{}
	seen := map[string]bool{}
	appendID := func(value string) {
		id := strings.TrimSpace(value)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		models = append(models, id)
	}
	for _, key := range []string{"data", "models"} {
		field, ok := parsed[key]
		if !ok {
			continue
		}
		var items []json.RawMessage
		if err := json.Unmarshal(field, &items); err != nil {
			continue
		}
		for _, item := range items {
			var id string
			if err := json.Unmarshal(item, &id); err == nil {
				appendID(id)
				continue
			}
			var object struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(item, &object); err == nil {
				appendID(object.ID)
			}
		}
	}
	if len(models) == 0 {
		return nil, errors.New("模型接口没有返回任何模型（兼容 data:[{id}] 与 models:[] 两种形状）")
	}
	return models, nil
}
