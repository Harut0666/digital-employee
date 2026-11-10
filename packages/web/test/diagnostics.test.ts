// 接缝 D（web 纯函数）：诊断信息的文本拼装。
// 断言外部可观察行为：复制出去的文本必须带版本/运行时/工作区路径与脱敏快照，
// 且任何情况下都不出现明文 API Key（Spec 0004「密钥不落响应」）。
import { describe, expect, test } from "bun:test";
import {
  DEFAULT_DIAGNOSTICS_SETTINGS,
  DEFAULT_EXECUTION_SETTINGS,
  DEFAULT_PERMISSION_SETTINGS,
  DEFAULT_UI_SETTINGS,
  SETTINGS_VERSION,
  type AppSettingsView,
  type DiagnosticsInfo,
} from "@de/core/contract";
import { formatDiagnosticsText, redactSecrets } from "../src/view/diagnostics";

const WORKSPACE = "C:\\Users\\demo\\AppData\\Roaming\\digital-employee";

/** 服务端返回的脱敏视图：连接只带末 4 位，不含明文（后端已不在前端包里，这里按契约直接构造）。 */
function settingsView(extra: Record<string, unknown> = {}): AppSettingsView {
  const view: AppSettingsView = {
    ok: true,
    version: SETTINGS_VERSION,
    revision: 1,
    ui: { ...DEFAULT_UI_SETTINGS },
    execution: { ...DEFAULT_EXECUTION_SETTINGS },
    diagnostics: { ...DEFAULT_DIAGNOSTICS_SETTINGS },
    permissions: { ...DEFAULT_PERMISSION_SETTINGS },
    connections: [
      { name: "deepseek", baseUrl: "https://api.deepseek.com", models: ["deepseek-chat"], apiKeyLast4: "1234", hasKey: true, source: "file", isDefault: true },
    ],
    lastModel: null,
    demo: { toolFaults: {}, corruptTraceability: false, crashOnNextStep: false },
    extra: {},
  };
  return { ...view, ...extra };
}

function diagnosticsInfo(overrides: Partial<DiagnosticsInfo> = {}): DiagnosticsInfo {
  return {
    ok: true,
    generatedAt: "2026-11-05T08:00:00.000Z",
    app: { version: "0.2.1", settingsVersion: 2 },
    runtime: { bun: "1.4.2", platform: "win32", arch: "x64" },
    workspace: {
      workspaceDir: WORKSPACE,
      employeesDir: `${WORKSPACE}\\employees`,
      configVersionsDir: `${WORKSPACE}\\config-versions`,
      settingsFile: `${WORKSPACE}\\settings.json`,
      settingsFileExists: true,
    },
    settings: settingsView(),
    tasks: [],
    ...overrides,
  };
}

describe("formatDiagnosticsText", () => {
  test("包含版本、运行时、工作区路径、设置文件与脱敏快照", () => {
    const text = formatDiagnosticsText(diagnosticsInfo());

    expect(text).toContain("应用版本：0.2.1（设置文档 v2）");
    expect(text).toContain("运行环境：Bun 1.4.2 · Windows · x64");
    expect(text).toContain(`应用数据目录：${WORKSPACE}`);
    expect(text).toContain(`${WORKSPACE}\\settings.json（存在）`);
    expect(text).toContain("日志级别：info");
    expect(text).toContain("执行与预算：步骤超时 120000 ms");
    // 脱敏设置快照：连接只有末 4 位，没有明文
    expect(text).toContain("设置快照：");
    expect(text).toContain('"apiKeyLast4": "1234"');
    expect(text).not.toContain("sk-file-secret-1234");
  });

  test("设置文件不存在时写明“未创建”", () => {
    const info = diagnosticsInfo();
    const text = formatDiagnosticsText({
      ...info,
      workspace: { ...info.workspace, settingsFileExists: false },
    });
    expect(text).toContain("settings.json（未创建）");
  });

  test("快照里意外带上明文 apiKey 时也会被脱敏", () => {
    const leaked = settingsView({
      connections: [
        {
          name: "deepseek",
          baseUrl: "https://api.deepseek.com",
          models: ["deepseek-chat"],
          apiKeyLast4: "1234",
          hasKey: true,
          source: "file",
          isDefault: true,
          // 防御性用例：即使上游漏了字段，复制出去的文本也不能带明文
          apiKey: "sk-live-abcdef1234567890",
        },
      ],
    } as unknown as Record<string, unknown>);
    const text = formatDiagnosticsText(diagnosticsInfo({ settings: leaked }));
    expect(text).not.toContain("sk-live-abcdef1234567890");
    expect(text).toContain("已脱敏");
  });

  test("多个连接时列出名字与末 4 位", () => {
    const info = diagnosticsInfo();
    const text = formatDiagnosticsText({
      ...info,
      settings: {
        ...info.settings,
        connections: [
          { name: "deepseek", baseUrl: "https://api.deepseek.com", models: ["deepseek-chat"], apiKeyLast4: "1234", hasKey: true, source: "file", isDefault: true },
          { name: "qwen", baseUrl: "http://localhost:8000/v1", models: ["qwen-max"], apiKeyLast4: null, hasKey: false, source: "none", isDefault: false },
        ],
      },
    });
    expect(text).toContain("模型连接：2 个");
    expect(text).toContain("deepseek（末 4 位 1234）");
    expect(text).toContain("qwen（未配置 Key）");
  });
});

describe("redactSecrets", () => {
  test("把 apiKey 字段的值抹成“已脱敏”", () => {
    const text = redactSecrets('{\n  "apiKey": "sk-file-secret-1234",\n  "name": "deepseek"\n}');
    expect(text).not.toContain("sk-file-secret-1234");
    expect(text).toContain('"apiKey": "已脱敏"');
    expect(text).toContain('"name": "deepseek"');
  });

  test("游离的 sk- 密钥同样打码", () => {
    const text = redactSecrets("请求失败：auth sk-live-abcdef1234567890 无效");
    expect(text).not.toContain("sk-live-abcdef1234567890");
    expect(text).toContain("sk-****");
  });

  test("不含密钥的文本原样返回", () => {
    const text = "digital-employee 诊断信息\n工作区：C:\\demo";
    expect(redactSecrets(text)).toBe(text);
  });
});
