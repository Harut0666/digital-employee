// T46 设置不再预置厂商：没有任何连接时设置页显示空状态和“添加连接”；添加表单为名称、地址、Key。
import { describe, expect, test } from "bun:test";
import { ADD_CONNECTION_LABEL, NEW_CONNECTION_FIELDS, connectionsEmptyState } from "../src/pages/settings/connections-empty";
import { createMockSettingsApi } from "../src/api/settings";

describe("设置页：没有连接时的空状态", () => {
  test("连接数为 0 时给出空状态，带“添加连接”", () => {
    const state = connectionsEmptyState(0);
    expect(state).not.toBeNull();
    expect(state!.title).toBe("还没有模型连接");
    expect(state!.actionLabel).toBe("添加连接");
    expect(ADD_CONNECTION_LABEL).toBe("添加连接");
    // 说明里不替人选厂商，只说 OpenAI 兼容
    expect(state!.hint).toContain("OpenAI 兼容");
    expect(state!.hint.toLowerCase()).not.toContain("deepseek");
  });

  test("已经有连接时不显示空状态", () => {
    expect(connectionsEmptyState(1)).toBeNull();
    expect(connectionsEmptyState(3)).toBeNull();
  });

  test("添加连接的表单是名称、地址、Key", () => {
    expect(NEW_CONNECTION_FIELDS.map((field) => field.label)).toEqual(["名称", "地址", "Key"]);
    for (const field of NEW_CONNECTION_FIELDS) {
      expect(field.placeholder.toLowerCase()).not.toContain("deepseek");
    }
  });
});

describe("模拟数据不预置厂商", () => {
  test("模拟设置里的连接不含 DeepSeek 写死的名称和地址，视图里也没有环境变量覆盖", async () => {
    const api = createMockSettingsApi();
    const view = await api.getSettings();
    expect(JSON.stringify(view).toLowerCase()).not.toContain("deepseek");
    expect("envOverride" in view).toBe(false);
  });
});
