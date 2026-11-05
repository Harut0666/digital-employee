// 设置文档 v2 的纯函数接缝（T19）：v1 → v2 迁移、revision、未知键保留、脱敏导出、导入校验。
// 这里只断言可观察结果（读出的文档、写盘的 JSON、导出对象），不碰内部实现。
import { describe, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  DEFAULT_DIAGNOSTICS_SETTINGS,
  DEFAULT_EXECUTION_SETTINGS,
  DEFAULT_PERMISSION_SETTINGS,
  DEFAULT_UI_SETTINGS,
  SETTINGS_VERSION,
} from "../../src/contract";
import {
  normalizeSettingsDocument,
  readSettingsDocument,
  settingsDocumentView,
  settingsExport,
  updateSettingsDocument,
  validateSettingsDocumentInput,
  writeSettingsDocument,
} from "../../src/config";

const tempDir = () => mkdtempSync(join(tmpdir(), "de-settings-doc-"));
const KEY = "sk-secret-1234";

function v1Workspace(): string {
  const ws = tempDir();
  writeFileSync(
    join(ws, "settings.json"),
    JSON.stringify(
      {
        connections: [
          { name: "deepseek", baseUrl: "https://api.deepseek.com", apiKey: KEY, models: ["deepseek-chat"] },
        ],
        defaultConnection: "deepseek",
        defaultModel: "deepseek-chat",
        // 其它模块 / 未来版本写入的键
        customModule: { keep: true },
        note: "现场演示",
      },
      null,
      2,
    ),
  );
  return ws;
}

describe("v1 → v2 迁移（读路径不写盘）", () => {
  test("没有 version 的旧文件读出 v2 默认值、revision 0，未知顶层键保留在 extra", () => {
    const ws = v1Workspace();
    const before = readFileSync(join(ws, "settings.json"), "utf8");

    const doc = readSettingsDocument(ws);
    expect(doc.version).toBe(SETTINGS_VERSION);
    expect(doc.revision).toBe(0);
    expect(doc.ui).toEqual(DEFAULT_UI_SETTINGS);
    expect(doc.execution).toEqual(DEFAULT_EXECUTION_SETTINGS);
    expect(doc.diagnostics).toEqual(DEFAULT_DIAGNOSTICS_SETTINGS);
    expect(doc.permissions).toEqual(DEFAULT_PERMISSION_SETTINGS);
    expect(doc.connections).toHaveLength(1);
    expect(doc.extra).toEqual({ customModule: { keep: true }, note: "现场演示" });

    // 读路径不写盘：首次写入前文件原封不动
    expect(readFileSync(join(ws, "settings.json"), "utf8")).toBe(before);
  });

  test("首次写入后变成 v2，revision 变 1，自定义键仍保留", () => {
    const ws = v1Workspace();
    const written = updateSettingsDocument(ws, (doc) => {
      doc.ui.theme = "dark";
    });
    expect(written.revision).toBe(1);

    const stored = JSON.parse(readFileSync(join(ws, "settings.json"), "utf8")) as Record<string, any>;
    expect(stored.version).toBe(2);
    expect(stored.revision).toBe(1);
    expect(stored.ui.theme).toBe("dark");
    expect(stored.ui.fontSize).toBe(14);
    expect(stored.execution).toEqual(DEFAULT_EXECUTION_SETTINGS);
    expect(stored.customModule).toEqual({ keep: true });
    expect(stored.note).toBe("现场演示");
    expect(stored.connections[0].apiKey).toBe(KEY);
  });

  test("文件里的坏值读时回落到默认值，不抛错", () => {
    const ws = tempDir();
    writeFileSync(
      join(ws, "settings.json"),
      JSON.stringify({
        version: 2,
        revision: 3,
        ui: { theme: "neon", fontSize: 99, enterBehavior: "newline" },
        execution: { modelMaxRetries: -1, stepTimeoutMs: "soon" },
        permissions: { sandboxMode: "full" },
      }),
    );
    const doc = readSettingsDocument(ws);
    expect(doc.revision).toBe(3);
    expect(doc.ui.theme).toBe("system");
    expect(doc.ui.fontSize).toBe(14);
    expect(doc.ui.enterBehavior).toBe("newline");
    expect(doc.execution.modelMaxRetries).toBe(DEFAULT_EXECUTION_SETTINGS.modelMaxRetries);
    expect(doc.execution.stepTimeoutMs).toBe(DEFAULT_EXECUTION_SETTINGS.stepTimeoutMs);
    expect(doc.permissions.sandboxMode).toBe(DEFAULT_PERMISSION_SETTINGS.sandboxMode);
  });
});

describe("revision（每次写入 +1）", () => {
  test("连续写入 revision 单调递增，读回一致", () => {
    const ws = tempDir();
    expect(readSettingsDocument(ws).revision).toBe(0);
    expect(updateSettingsDocument(ws, () => {}).revision).toBe(1);
    expect(updateSettingsDocument(ws, () => {}).revision).toBe(2);
    expect(readSettingsDocument(ws).revision).toBe(2);

    const written = writeSettingsDocument(ws, readSettingsDocument(ws));
    expect(written.revision).toBe(3);
  });
});

describe("连接的高级参数", () => {
  test("modelOptions / timeoutMs / maxRetries 读得回来、写得回去", () => {
    const ws = tempDir();
    const doc = normalizeSettingsDocument({
      connections: [
        {
          name: "local",
          baseUrl: "http://localhost:8000/v1",
          apiKey: "sk-local",
          models: ["qwen"],
          modelOptions: { qwen: { contextWindow: 8192, maxTokens: 1024, defaultReasoningEffort: "low" } },
          timeoutMs: 60_000,
          maxRetries: 3,
        },
      ],
    });
    expect(doc.connections[0]?.modelOptions?.qwen).toEqual({
      contextWindow: 8192,
      maxTokens: 1024,
      defaultReasoningEffort: "low",
    });
    expect(doc.connections[0]?.timeoutMs).toBe(60_000);
    expect(doc.connections[0]?.maxRetries).toBe(3);

    writeSettingsDocument(ws, doc);
    const stored = JSON.parse(readFileSync(join(ws, "settings.json"), "utf8")) as Record<string, any>;
    expect(stored.connections[0].modelOptions.qwen.contextWindow).toBe(8192);
    expect(stored.connections[0].timeoutMs).toBe(60_000);
    expect(stored.connections[0].maxRetries).toBe(3);
  });
});

describe("脱敏", () => {
  test("文档视图只出末 4 位 + hasKey + source，序列化后不含明文 Key", () => {
    const ws = v1Workspace();
    const view = settingsDocumentView(readSettingsDocument(ws), {});
    expect(view.connections[0]).toMatchObject({ apiKeyLast4: "1234", hasKey: true, source: "file", isDefault: true });
    expect(JSON.stringify(view)).not.toContain(KEY);
  });

  test("DEEPSEEK_API_KEY 覆盖来源；非 DeepSeek 连接不受影响", () => {
    const doc = normalizeSettingsDocument({
      connections: [
        { name: "deepseek", baseUrl: "https://api.deepseek.com", apiKey: KEY, models: ["deepseek-chat"] },
        { name: "qwen", baseUrl: "http://localhost:8000/v1", apiKey: "sk-local-abcd", models: ["qwen"] },
      ],
    });
    const view = settingsDocumentView(doc, { DEEPSEEK_API_KEY: "sk-env-9999" });
    expect(view.envOverride).toBe(true);
    expect(view.connections[0]).toMatchObject({ apiKeyLast4: "9999", source: "env" });
    expect(view.connections[1]).toMatchObject({ apiKeyLast4: "abcd", source: "file" });
    expect(JSON.stringify(view)).not.toContain(KEY);
    expect(JSON.stringify(view)).not.toContain("sk-env-9999");
  });

  test("导出默认把 Key 抹掉（形状仍可导入），includeKeys 才含明文", () => {
    const doc = readSettingsDocument(v1Workspace());
    const redacted = settingsExport(doc);
    expect(JSON.stringify(redacted)).not.toContain(KEY);
    expect((redacted.connections as any[])[0].apiKey).toBe("");
    expect(redacted.version).toBe(2);

    const withKeys = settingsExport(doc, { includeKeys: true });
    expect((withKeys.connections as any[])[0].apiKey).toBe(KEY);
  });
});

describe("导入校验", () => {
  test("v1 文件允许导入并标记 migrated", () => {
    expect(validateSettingsDocumentInput({ connections: [], defaultConnection: "", defaultModel: "" })).toEqual({
      ok: true,
      migrated: true,
    });
  });

  test("v2 文件不需要迁移", () => {
    expect(validateSettingsDocumentInput({ version: 2, revision: 1 })).toEqual({ ok: true, migrated: false });
  });

  test("非法枚举 / 越界 / 坏连接 / 不支持的版本都拒绝", () => {
    expect(validateSettingsDocumentInput({ ui: { theme: "neon" } }).ok).toBe(false);
    expect(validateSettingsDocumentInput({ execution: { stepTimeoutMs: 0 } }).ok).toBe(false);
    expect(validateSettingsDocumentInput({ permissions: { defaultRole: "admin" } }).ok).toBe(false);
    expect(validateSettingsDocumentInput({ connections: [{ name: "", baseUrl: "https://x.test" }] }).ok).toBe(false);
    expect(validateSettingsDocumentInput({ connections: "nope" }).ok).toBe(false);
    expect(validateSettingsDocumentInput({ version: 9 }).ok).toBe(false);
    expect(validateSettingsDocumentInput("nope").ok).toBe(false);
  });
});
