// T39 会话模型：设置文档去掉 defaultModel，改为 lastModel（最近一次在会话里选的模型）。
package settings

import (
	"encoding/json"
	"os"
	"testing"

	"digital-employee/internal/contract"
)

func TestLastModelMigratesFromDefaultModel(t *testing.T) {
	raw := map[string]any{
		"version":           2,
		"connections":       []any{map[string]any{"name": "deepseek", "baseUrl": "https://api.deepseek.com", "apiKey": "k", "models": []any{"deepseek-chat", "deepseek-reasoner"}}},
		"defaultConnection": "deepseek",
		"defaultModel":      "deepseek-reasoner",
	}
	doc := Normalize(raw)
	if doc.LastModel == nil || doc.LastModel.Connection != "deepseek" || doc.LastModel.Model != "deepseek-reasoner" {
		t.Fatalf("旧文档的 defaultConnection/defaultModel 应换算成 lastModel：%+v", doc.LastModel)
	}
	// 再读一遍（已经是新形状）结果不变：迁移是幂等的
	again := Normalize(Serialize(doc))
	if again.LastModel == nil || *again.LastModel != *doc.LastModel {
		t.Fatalf("换算应幂等：%+v → %+v", doc.LastModel, again.LastModel)
	}
}

func TestLastModelExplicitWinsOverDefaultModel(t *testing.T) {
	doc := Normalize(map[string]any{
		"connections":       []any{map[string]any{"name": "a", "baseUrl": "https://a.example.com", "models": []any{"m1", "m2"}}},
		"defaultConnection": "a",
		"defaultModel":      "m1",
		"lastModel":         map[string]any{"connection": "a", "model": "m2"},
	})
	if doc.LastModel == nil || *doc.LastModel != (contract.ModelRef{Connection: "a", Model: "m2"}) {
		t.Fatalf("显式 lastModel 应优先：%+v", doc.LastModel)
	}
}

func TestLastModelAbsentWhenOnlyDefaultConnection(t *testing.T) {
	// 没有 defaultModel、也没有 lastModel：不知道人上次选的是哪个模型
	doc := Normalize(map[string]any{
		"connections":       []any{map[string]any{"name": "a", "baseUrl": "https://a.example.com", "models": []any{"m1"}}},
		"defaultConnection": "a",
	})
	if doc.LastModel != nil {
		t.Fatalf("没有选择记录时 lastModel 应为空：%+v", doc.LastModel)
	}
	// 坏形状不认
	doc = Normalize(map[string]any{"lastModel": map[string]any{"connection": "a"}})
	if doc.LastModel != nil {
		t.Fatalf("缺 model 的 lastModel 应忽略：%+v", doc.LastModel)
	}
}

func TestSerializedDocumentDropsDefaultModel(t *testing.T) {
	dir := t.TempDir()
	written, err := Write(dir, Normalize(map[string]any{
		"connections":  []any{map[string]any{"name": "deepseek", "baseUrl": "https://api.deepseek.com", "models": []any{"deepseek-chat"}}},
		"lastModel":    map[string]any{"connection": "deepseek", "model": "deepseek-chat"},
		"defaultModel": "deepseek-chat",
	}))
	if err != nil {
		t.Fatal(err)
	}
	_ = written
	data, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, present := raw["defaultModel"]; present {
		t.Fatalf("落盘不应再有 defaultModel：%s", data)
	}
	lastModel, ok := raw["lastModel"].(map[string]any)
	if !ok || lastModel["connection"] != "deepseek" || lastModel["model"] != "deepseek-chat" {
		t.Fatalf("落盘应有 lastModel：%s", data)
	}
	if _, present := raw["defaultConnection"]; present {
		t.Fatalf("落盘不应再有 defaultConnection：%s", data)
	}
}

func TestLastModelUpdateKeepsProgress(t *testing.T) {
	dir := t.TempDir()
	if _, err := Write(dir, Normalize(map[string]any{
		"connections": []any{map[string]any{"name": "a", "baseUrl": "https://a.example.com", "models": []any{"m1", "m2"}}},
	})); err != nil {
		t.Fatal(err)
	}
	updated, err := Update(dir, func(doc *contract.SettingsDocument) {
		doc.LastModel = &contract.ModelRef{Connection: "a", Model: "m2"}
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.LastModel == nil || *updated.LastModel != (contract.ModelRef{Connection: "a", Model: "m2"}) {
		t.Fatalf("写入后 lastModel 不对：revision %d %+v", updated.Revision, updated.LastModel)
	}
	if again := Read(dir); again.LastModel == nil || *again.LastModel != (contract.ModelRef{Connection: "a", Model: "m2"}) {
		t.Fatalf("回读 lastModel 不对：%+v", again.LastModel)
	}
}
