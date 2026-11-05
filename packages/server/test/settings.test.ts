// 设置页请求级测试（接缝：HTTP 接口）。重点：API Key 只回末 4 位、空 Key 保留原值、测试连接走注入的假提供者、
// 演示开关经 admin_set_faults 下发到工具子进程、工作区文件操作。
import { describe, expect, test } from "bun:test";
import { existsSync, mkdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import type { FaultHooks, ModelSettings, ToolClient, ToolDescriptor } from "@de/core/contract";
import { AUDIT_FILE_NAME, formatAuditLine, readAuditEntries } from "../src/audit";
import type { ConnectionProbe, FolderOpener } from "../src/routes/deps";
import { jsonRequest, setup, tempDir } from "./helpers";

const KEY = "sk-secret-1234";
const LOCAL_KEY = "sk-local-abcd";

function settingsWorkspace(extra: Record<string, unknown> = {}): string {
  const ws = tempDir("de-settings-ws-");
  writeFileSync(
    join(ws, "settings.json"),
    JSON.stringify(
      {
        connections: [
          { name: "deepseek", baseUrl: "https://api.deepseek.com", apiKey: KEY, models: ["deepseek-chat"] },
          { name: "qwen", baseUrl: "http://localhost:8000/v1", apiKey: LOCAL_KEY, models: ["qwen-max"] },
        ],
        defaultConnection: "deepseek",
        defaultModel: "deepseek-chat",
        ...extra,
      },
      null,
      2,
    ),
  );
  return ws;
}

interface FakeTools extends ToolClient {
  calls: { name: string; args: unknown }[];
}

function fakeToolClient(result: { ok: true; data: unknown } | { ok: false; error: string } = { ok: true, data: {} }): FakeTools {
  const calls: FakeTools["calls"] = [];
  return {
    calls,
    async listTools(): Promise<ToolDescriptor[]> {
      return [];
    },
    async call(name, args) {
      calls.push({ name, args });
      return result.ok ? result : { ok: false, error: result.error, retryable: false };
    },
    async close() {},
  };
}

function readJson(path: string): Record<string, any> {
  return JSON.parse(readFileSync(path, "utf8")) as Record<string, any>;
}

async function bodyOf(res: Response): Promise<Record<string, any>> {
  return (await res.json()) as Record<string, any>;
}

describe("GET /api/settings · 模型连接", () => {
  test("apiKey 只返回末 4 位 + hasKey + 来源，任何响应里都不出现完整 Key", async () => {
    const { call } = setup({ workspaceDir: settingsWorkspace(), env: {} });
    const res = await call("/api/settings");
    expect(res.status).toBe(200);
    const text = await res.text();
    expect(text).not.toContain(KEY);
    expect(text).not.toContain(LOCAL_KEY);

    const body = JSON.parse(text) as Record<string, any>;
    expect(body).toMatchObject({ ok: true, defaultConnection: "deepseek", defaultModel: "deepseek-chat", envOverride: false });
    const deepseek = body.connections.find((c: any) => c.name === "deepseek");
    expect(deepseek).toMatchObject({
      name: "deepseek",
      baseUrl: "https://api.deepseek.com",
      models: ["deepseek-chat"],
      apiKeyLast4: "1234",
      hasKey: true,
      source: "file",
      isDefault: true,
    });
    const qwen = body.connections.find((c: any) => c.name === "qwen");
    expect(qwen).toMatchObject({ apiKeyLast4: "abcd", hasKey: true, source: "file", isDefault: false });
  });

  test("DEEPSEEK_API_KEY 优先，并在返回里标明来自环境变量", async () => {
    const { call } = setup({ workspaceDir: settingsWorkspace(), env: { DEEPSEEK_API_KEY: "sk-env-9999" } });
    const res = await call("/api/settings");
    const text = await res.text();
    expect(text).not.toContain("sk-env-9999");
    const body = JSON.parse(text) as Record<string, any>;
    expect(body.envOverride).toBe(true);
    expect(body.connections.find((c: any) => c.name === "deepseek")).toMatchObject({
      apiKeyLast4: "9999",
      hasKey: true,
      source: "env",
    });
    expect(body.connections.find((c: any) => c.name === "qwen")).toMatchObject({ source: "file" });
  });

  test("没有 Key 的连接：hasKey=false、apiKeyLast4=null", async () => {
    const ws = tempDir();
    writeFileSync(
      join(ws, "settings.json"),
      JSON.stringify({
        connections: [{ name: "local", baseUrl: "http://localhost:8000/v1", apiKey: "", models: ["qwen"] }],
        defaultConnection: "local",
        defaultModel: "qwen",
      }),
    );
    const { call } = setup({ workspaceDir: ws, env: {} });
    const body = await bodyOf(await call("/api/settings"));
    expect(body.connections[0]).toMatchObject({ name: "local", hasKey: false, apiKeyLast4: null, source: "none" });
  });
});

describe("PUT / DELETE /api/settings/connections", () => {
  test("空 apiKey 或省略 apiKey 都保留原值；保存后重建模型提供者", async () => {
    const ws = settingsWorkspace();
    let saved: ModelSettings | undefined;
    const { call } = setup({
      workspaceDir: ws,
      env: {},
      onSettingsSaved: (s) => {
        saved = s;
      },
    });

    const res = await call(
      "/api/settings/connections/deepseek",
      jsonRequest("PUT", { baseUrl: "https://api.deepseek.com/v1", models: ["deepseek-reasoner"], apiKey: "" }),
    );
    expect(res.status).toBe(200);
    const text = await res.text();
    expect(text).not.toContain(KEY);

    const stored = readJson(join(ws, "settings.json")).connections.find((c: any) => c.name === "deepseek");
    expect(stored.apiKey).toBe(KEY);
    expect(stored.baseUrl).toBe("https://api.deepseek.com/v1");
    expect(stored.models).toEqual(["deepseek-reasoner"]);
    expect(saved?.connections.find((c) => c.name === "deepseek")?.baseUrl).toBe("https://api.deepseek.com/v1");

    // 省略 apiKey 同样保留
    await call("/api/settings/connections/deepseek", jsonRequest("PUT", { models: ["deepseek-chat"] }));
    expect(readJson(join(ws, "settings.json")).connections[0].apiKey).toBe(KEY);
  });

  test("新增连接：写入 settings.json，响应仍是打码后的视图", async () => {
    const ws = settingsWorkspace();
    const { call } = setup({ workspaceDir: ws, env: {} });
    const res = await call(
      "/api/settings/connections/glm",
      jsonRequest("PUT", { baseUrl: "https://open.bigmodel.cn/api/paas/v4", apiKey: "sk-glm-7777", models: ["glm-4"] }),
    );
    expect(res.status).toBe(200);
    const text = await res.text();
    expect(text).not.toContain("sk-glm-7777");
    expect(JSON.parse(text).connection).toMatchObject({ name: "glm", apiKeyLast4: "7777", hasKey: true, source: "file" });
    expect(readJson(join(ws, "settings.json")).connections.map((c: any) => c.name)).toEqual(["deepseek", "qwen", "glm"]);
  });

  test("非法请求返回 400，文件不动", async () => {
    const ws = settingsWorkspace();
    const before = readFileSync(join(ws, "settings.json"), "utf8");
    const { call } = setup({ workspaceDir: ws, env: {} });
    const noUrl = await call("/api/settings/connections/newone", jsonRequest("PUT", { models: ["m"] }));
    expect(noUrl.status).toBe(400);
    const badModels = await call("/api/settings/connections/newone", jsonRequest("PUT", { baseUrl: "https://x.test", models: "m" }));
    expect(badModels.status).toBe(400);
    expect(readFileSync(join(ws, "settings.json"), "utf8")).toBe(before);
  });

  test("DELETE 删除连接；删掉默认连接后默认连接顺延", async () => {
    const ws = settingsWorkspace();
    const { call } = setup({ workspaceDir: ws, env: {} });
    const res = await call("/api/settings/connections/deepseek", { method: "DELETE" });
    expect(res.status).toBe(200);
    const body = await bodyOf(res);
    expect(body.connections.map((c: any) => c.name)).toEqual(["qwen"]);
    const stored = readJson(join(ws, "settings.json"));
    expect(stored.connections.map((c: any) => c.name)).toEqual(["qwen"]);
    expect(stored.defaultConnection).toBe("qwen");

    expect((await call("/api/settings/connections/nope", { method: "DELETE" })).status).toBe(404);
  });
});

describe("POST /api/settings/connections/:name/test", () => {
  test("走注入的假提供者：返回可用/延迟/模型，不泄露 Key", async () => {
    const ws = settingsWorkspace();
    const seen: { connection: string; apiKey: string; model: string }[] = [];
    const probe: ConnectionProbe = async (connection, model) => {
      seen.push({ connection: connection.name, apiKey: connection.apiKey, model });
      return { ok: true, latencyMs: 12, model };
    };
    const { call } = setup({ workspaceDir: ws, env: {}, probeConnection: probe });
    const res = await call("/api/settings/connections/deepseek/test", jsonRequest("POST", { model: "deepseek-chat" }));
    expect(res.status).toBe(200);
    const text = await res.text();
    expect(text).not.toContain(KEY);
    expect(JSON.parse(text)).toEqual({ ok: true, latencyMs: 12, model: "deepseek-chat" });
    expect(seen).toEqual([{ connection: "deepseek", apiKey: KEY, model: "deepseek-chat" }]);
  });

  test("环境变量优先：测试连接也用环境变量的 Key", async () => {
    const ws = settingsWorkspace();
    let usedKey = "";
    const { call } = setup({
      workspaceDir: ws,
      env: { DEEPSEEK_API_KEY: "sk-env-9999" },
      probeConnection: async (connection, model) => {
        usedKey = connection.apiKey;
        return { ok: true, latencyMs: 1, model };
      },
    });
    await call("/api/settings/connections/deepseek/test", jsonRequest("POST", {}));
    expect(usedKey).toBe("sk-env-9999");
  });

  test("提供者失败时返回 ok=false 与错误说明；错误里的 Key 被打码", async () => {
    const ws = settingsWorkspace();
    const probe: ConnectionProbe = async () => {
      throw new Error(`401 Unauthorized（key=${KEY}）`);
    };
    const { call } = setup({ workspaceDir: ws, env: {}, probeConnection: probe });
    const res = await call("/api/settings/connections/deepseek/test", jsonRequest("POST", {}));
    expect(res.status).toBe(200);
    const text = await res.text();
    expect(text).not.toContain(KEY);
    const body = JSON.parse(text);
    expect(body.ok).toBe(false);
    expect(body.error).toContain("401");
  });

  test("未知连接 404；未配置 Key 时直接给出说明，不调用提供者", async () => {
    const ws = tempDir();
    writeFileSync(
      join(ws, "settings.json"),
      JSON.stringify({
        connections: [{ name: "local", baseUrl: "http://localhost:8000/v1", apiKey: "", models: ["qwen"] }],
        defaultConnection: "local",
        defaultModel: "qwen",
      }),
    );
    let called = false;
    const { call } = setup({
      workspaceDir: ws,
      env: {},
      probeConnection: async (_connection, model) => {
        called = true;
        return { ok: true, latencyMs: 1, model };
      },
    });
    expect((await call("/api/settings/connections/nope/test", jsonRequest("POST", {}))).status).toBe(404);
    const res = await bodyOf(await call("/api/settings/connections/local/test", jsonRequest("POST", {})));
    expect(res.ok).toBe(false);
    expect(res.error).toContain("API Key");
    expect(called).toBe(false);
  });
});

describe("GET / PUT /api/settings/demo", () => {
  test("默认关闭；工具清单不含管理工具", async () => {
    const { call } = setup({ workspaceDir: tempDir(), env: {} });
    const body = await bodyOf(await call("/api/settings/demo"));
    expect(body).toMatchObject({ ok: true, toolFaults: {}, corruptTraceability: false, crashOnNextStep: false });
    const names = body.tools.map((t: any) => t.name);
    expect(names).toContain("load_sales");
    expect(names).not.toContain("admin_set_faults");
  });

  test("PUT 持久化到 settings.json、更新共享 FaultHooks，并经 admin_set_faults 下发", async () => {
    const ws = settingsWorkspace();
    const faults: FaultHooks = {};
    const tools = fakeToolClient();
    const { call } = setup({ workspaceDir: ws, env: {}, faults, toolClient: tools });
    const res = await call(
      "/api/settings/demo",
      jsonRequest("PUT", { toolFaults: { load_sales: "timeout" }, corruptTraceability: true, crashOnNextStep: true }),
    );
    expect(res.status).toBe(200);
    const text = await res.text();
    expect(text).not.toContain(KEY);

    const stored = readJson(join(ws, "settings.json"));
    expect(stored.demo).toMatchObject({
      toolFaults: { load_sales: "timeout" },
      corruptTraceability: true,
      crashOnNextStep: true,
    });
    expect(stored.connections).toHaveLength(2);
    expect(faults).toMatchObject({ tools: { load_sales: "timeout" }, corruptTraceability: true, crashOnNextStep: true });
    expect(tools.calls).toEqual([{ name: "admin_set_faults", args: { tools: { load_sales: "timeout" } } }]);

    const got = await bodyOf(await call("/api/settings/demo"));
    expect(got).toMatchObject({ toolFaults: { load_sales: "timeout" }, corruptTraceability: true, crashOnNextStep: true });
  });

  test("部分更新保留其它开关；非法故障模式返回 400 且不写入", async () => {
    const ws = settingsWorkspace();
    const faults: FaultHooks = {};
    const tools = fakeToolClient();
    const { call } = setup({ workspaceDir: ws, env: {}, faults, toolClient: tools });
    await call("/api/settings/demo", jsonRequest("PUT", { toolFaults: { load_sales: "timeout" }, corruptTraceability: true }));
    const before = readFileSync(join(ws, "settings.json"), "utf8");

    const bad = await call("/api/settings/demo", jsonRequest("PUT", { toolFaults: { load_sales: "explode" } }));
    expect(bad.status).toBe(400);
    expect(readFileSync(join(ws, "settings.json"), "utf8")).toBe(before);

    const partial = await call("/api/settings/demo", jsonRequest("PUT", { crashOnNextStep: true }));
    expect(partial.status).toBe(200);
    const stored = readJson(join(ws, "settings.json")).demo;
    expect(stored).toMatchObject({ toolFaults: { load_sales: "timeout" }, corruptTraceability: true, crashOnNextStep: true });
    // 工具故障没变，但仍同步一次；最后一次调用应带完整工具故障表
    expect(tools.calls.at(-1)).toEqual({ name: "admin_set_faults", args: { tools: { load_sales: "timeout" } } });
  });

  test("下发失败时仍保存设置，但返回 toolError 让界面提示", async () => {
    const ws = settingsWorkspace();
    const tools = fakeToolClient({ ok: false, error: "工具服务器未启动" });
    const { call } = setup({ workspaceDir: ws, env: {}, toolClient: tools });
    const body = await bodyOf(await call("/api/settings/demo", jsonRequest("PUT", { corruptTraceability: true })));
    expect(body.ok).toBe(true);
    expect(body.toolError).toContain("工具服务器未启动");
    expect(readJson(join(ws, "settings.json")).demo.corruptTraceability).toBe(true);
  });
});

describe("工作区", () => {
  test("GET 返回工作区与员工目录；打开文件夹走注入的实现", async () => {
    const ws = tempDir();
    const employeesDir = tempDir();
    const opened: string[] = [];
    const openFolder: FolderOpener = (dir) => {
      opened.push(dir);
    };
    const { call } = setup({ workspaceDir: ws, employeesDir, defaultEmployeesDir: tempDir(), env: {}, openFolder });
    const info = await bodyOf(await call("/api/settings/workspace"));
    expect(info).toMatchObject({ ok: true, workspaceDir: ws, employeesDir });
    expect(info.configVersionsDir).toBe(join(ws, "config-versions"));

    const res = await call("/api/settings/workspace/open", { method: "POST" });
    expect(res.status).toBe(200);
    expect(opened).toEqual([ws]);
  });

  test("恢复默认员工配置：先备份当前版本到 config-versions 再覆盖，新任务立即读到默认配置", async () => {
    const ws = tempDir();
    const employeesDir = join(ws, "employees");
    mkdirSync(join(employeesDir, "weekly-report"), { recursive: true });
    writeFileSync(join(employeesDir, "weekly-report", "workflow.yaml"), "id: modified\n");
    writeFileSync(join(employeesDir, "weekly-report", "extra.txt"), "本地改动\n");
    const defaultsDir = tempDir();
    mkdirSync(join(defaultsDir, "weekly-report"));
    writeFileSync(join(defaultsDir, "weekly-report", "workflow.yaml"), "id: default\n");

    const { call } = setup({ workspaceDir: ws, employeesDir, defaultEmployeesDir: defaultsDir, env: {} });
    const body = await bodyOf(await call("/api/settings/workspace/reset-employees", { method: "POST" }));
    expect(body.ok).toBe(true);
    expect(body.employees).toEqual(["weekly-report"]);
    expect(typeof body.backupPath).toBe("string");
    expect(existsSync(join(body.backupPath, "weekly-report", "extra.txt"))).toBe(true);
    expect(readFileSync(join(employeesDir, "weekly-report", "workflow.yaml"), "utf8")).toBe("id: default\n");
    expect(existsSync(join(employeesDir, "weekly-report", "extra.txt"))).toBe(false);
  });

  test("员工目录不存在时直接创建，不报错", async () => {
    const ws = tempDir();
    const employeesDir = join(ws, "employees");
    const defaultsDir = tempDir();
    mkdirSync(join(defaultsDir, "weekly-report"));
    writeFileSync(join(defaultsDir, "weekly-report", "workflow.yaml"), "id: default\n");
    const { call } = setup({ workspaceDir: ws, employeesDir, defaultEmployeesDir: defaultsDir, env: {} });
    const body = await bodyOf(await call("/api/settings/workspace/reset-employees", { method: "POST" }));
    expect(body).toMatchObject({ ok: true, employees: ["weekly-report"] });
    expect(existsSync(join(employeesDir, "weekly-report", "workflow.yaml"))).toBe(true);
  });
});

// ---------------------------------------------------------------- T19：设置文档 v2

const UI_DEFAULTS = {
  theme: "system",
  fontSize: 14,
  trajectoryDetail: "standard",
  collapseTiming: "completion",
  enterBehavior: "send",
  linkOpening: "in-app",
};

function storedSettings(ws: string): Record<string, any> {
  return readJson(join(ws, "settings.json"));
}

/** v1 文件（无 version）+ 其它模块写入的自定义键。 */
function v1WorkspaceWithExtras(): string {
  const ws = settingsWorkspace({ customModule: { keep: true }, note: "现场演示" });
  return ws;
}

describe("GET /api/settings/app · 设置文档 v2", () => {
  test("v1 文件读成 v2：默认分区 + revision 0 + 密钥脱敏，且读不写盘", async () => {
    const ws = v1WorkspaceWithExtras();
    const before = readFileSync(join(ws, "settings.json"), "utf8");
    const { call } = setup({ workspaceDir: ws, env: {} });

    const res = await call("/api/settings/app");
    expect(res.status).toBe(200);
    const text = await res.text();
    expect(text).not.toContain(KEY);
    expect(text).not.toContain(LOCAL_KEY);
    const body = JSON.parse(text) as Record<string, any>;

    expect(body).toMatchObject({ ok: true, version: 2, revision: 0, envOverride: false, defaultConnection: "deepseek" });
    expect(body.ui).toEqual(UI_DEFAULTS);
    expect(body.execution).toMatchObject({ stepTimeoutMs: 120000, modelMaxRetries: 2, qcAutofix: 3 });
    expect(body.diagnostics).toEqual({ logLevel: "info" });
    expect(body.permissions).toEqual({ sandboxMode: "workspace-write", approvalPolicy: "ask", defaultRole: "initiator" });
    expect(body.connections.find((c: any) => c.name === "deepseek")).toMatchObject({
      apiKeyLast4: "1234",
      hasKey: true,
      source: "file",
      isDefault: true,
    });
    expect(body.extra).toMatchObject({ customModule: { keep: true }, note: "现场演示" });

    // 读路径不写盘
    expect(readFileSync(join(ws, "settings.json"), "utf8")).toBe(before);
  });

  test("PATCH 带正确 revision：合并分区、revision +1，首写迁移成 v2 且未知键保留", async () => {
    const ws = v1WorkspaceWithExtras();
    const { call } = setup({ workspaceDir: ws, env: {} });

    const res = await call(
      "/api/settings/app",
      jsonRequest("PATCH", { revision: 0, ui: { theme: "dark" }, execution: { modelMaxRetries: 4 } }),
    );
    expect(res.status).toBe(200);
    const body = await bodyOf(res);
    expect(body).toMatchObject({ ok: true, revision: 1 });
    expect(body.ui).toMatchObject({ theme: "dark", fontSize: 14 });
    expect(body.execution).toMatchObject({ modelMaxRetries: 4, stepTimeoutMs: 120000 });

    const stored = storedSettings(ws);
    expect(stored.version).toBe(2);
    expect(stored.revision).toBe(1);
    expect(stored.ui.theme).toBe("dark");
    expect(stored.customModule).toEqual({ keep: true });
    expect(stored.note).toBe("现场演示");
    expect(stored.connections[0].apiKey).toBe(KEY);

    // 再写一次：revision 继续 +1
    const again = await bodyOf(await call("/api/settings/app", jsonRequest("PATCH", { revision: 1, ui: { fontSize: 16 } })));
    expect(again.revision).toBe(2);
    expect(storedSettings(ws).ui).toMatchObject({ theme: "dark", fontSize: 16 });
  });

  test("PATCH 的 revision 过期：409 + 中文提示，文件不变", async () => {
    const ws = settingsWorkspace();
    const { call } = setup({ workspaceDir: ws, env: {} });
    await call("/api/settings/app", jsonRequest("PATCH", { revision: 0, ui: { theme: "dark" } }));
    const before = readFileSync(join(ws, "settings.json"), "utf8");

    const res = await call("/api/settings/app", jsonRequest("PATCH", { revision: 0, ui: { theme: "light" } }));
    expect(res.status).toBe(409);
    const body = await bodyOf(res);
    expect(body).toMatchObject({ ok: false, error: "revision_conflict", revision: 1 });
    expect(body.message).toContain("刷新");
    expect(readFileSync(join(ws, "settings.json"), "utf8")).toBe(before);
  });

  test("PATCH 非法枚举 / 越界：400 且文件不变", async () => {
    const ws = settingsWorkspace();
    const { call } = setup({ workspaceDir: ws, env: {} });
    const before = readFileSync(join(ws, "settings.json"), "utf8");

    const badTheme = await call("/api/settings/app", jsonRequest("PATCH", { revision: 0, ui: { theme: "neon" } }));
    expect(badTheme.status).toBe(400);
    const badFont = await call("/api/settings/app", jsonRequest("PATCH", { revision: 0, ui: { fontSize: 99 } }));
    expect(badFont.status).toBe(400);
    const badTimeout = await call("/api/settings/app", jsonRequest("PATCH", { revision: 0, execution: { stepTimeoutMs: 0 } }));
    expect(badTimeout.status).toBe(400);
    const badRole = await call("/api/settings/app", jsonRequest("PATCH", { revision: 0, permissions: { defaultRole: "admin" } }));
    expect(badRole.status).toBe(400);
    const noRevision = await call("/api/settings/app", jsonRequest("PATCH", { ui: { theme: "dark" } }));
    expect(noRevision.status).toBe(400);

    expect(readFileSync(join(ws, "settings.json"), "utf8")).toBe(before);
  });

  test("现有连接写入也走统一入口：经过一次保存 revision +1", async () => {
    const ws = settingsWorkspace();
    const { call } = setup({ workspaceDir: ws, env: {} });
    expect(storedSettings(ws).revision).toBeUndefined();

    await call("/api/settings/connections/deepseek", jsonRequest("PUT", { models: ["deepseek-chat"] }));
    expect(storedSettings(ws)).toMatchObject({ version: 2, revision: 1 });
    const app = await bodyOf(await call("/api/settings/app"));
    expect(app.revision).toBe(1);
  });
});

describe("PUT /api/settings/role", () => {
  test("只写 permissions.defaultRole，其余设置与密钥不动", async () => {
    const ws = v1WorkspaceWithExtras();
    const { call } = setup({ workspaceDir: ws, env: {} });

    const res = await call("/api/settings/role", jsonRequest("PUT", { role: "reviewer" }));
    expect(res.status).toBe(200);
    const body = await bodyOf(res);
    expect(body).toMatchObject({ ok: true, revision: 1, permissions: { defaultRole: "reviewer" } });
    expect(body.permissions).toMatchObject({ sandboxMode: "workspace-write", approvalPolicy: "ask" });

    const stored = storedSettings(ws);
    expect(stored.permissions.defaultRole).toBe("reviewer");
    expect(stored.version).toBe(2);
    expect(stored.connections[0].apiKey).toBe(KEY);
    expect(stored.customModule).toEqual({ keep: true });
  });

  test("非法角色 400 且不写盘", async () => {
    const ws = settingsWorkspace();
    const before = readFileSync(join(ws, "settings.json"), "utf8");
    const { call } = setup({ workspaceDir: ws, env: {} });
    expect((await call("/api/settings/role", jsonRequest("PUT", { role: "admin" }))).status).toBe(400);
    expect((await call("/api/settings/role", jsonRequest("PUT", {}))).status).toBe(400);
    expect(readFileSync(join(ws, "settings.json"), "utf8")).toBe(before);
  });
});

describe("GET /api/settings/export", () => {
  test("默认脱敏：正文没有明文 Key，文件名标注为普通导出", async () => {
    const ws = settingsWorkspace();
    const { call } = setup({ workspaceDir: ws, env: {} });
    const res = await call("/api/settings/export");
    expect(res.status).toBe(200);
    const text = await res.text();
    expect(text).not.toContain(KEY);
    expect(text).not.toContain(LOCAL_KEY);

    const body = JSON.parse(text) as Record<string, any>;
    expect(body).toMatchObject({ ok: true, redacted: true });
    expect(body.filename).toMatch(/^settings-.+\.json$/);
    expect(body.filename).not.toContain("with-keys");
    expect(body.document.version).toBe(2);
    expect(body.document.connections.every((c: any) => c.apiKey === "")).toBe(true);
    expect(res.headers.get("content-disposition")).toContain(body.filename);
  });

  test("includeKeys=true 才含明文 Key，文件名带 -with-keys", async () => {
    const ws = settingsWorkspace();
    const { call } = setup({ workspaceDir: ws, env: {} });
    const res = await call("/api/settings/export?includeKeys=true");
    expect(res.status).toBe(200);
    const body = (await res.json()) as Record<string, any>;
    expect(body).toMatchObject({ ok: true, redacted: false });
    expect(body.filename).toContain("with-keys");
    expect(JSON.stringify(body)).toContain(KEY);
    expect(res.headers.get("content-disposition")).toContain("with-keys");
  });
});

describe("POST /api/settings/import", () => {
  test("导入合法文件：写入 v2、revision +1，旧文件备份到 config-versions/settings", async () => {
    const ws = settingsWorkspace();
    const { call } = setup({ workspaceDir: ws, env: {} });
    const exported = await bodyOf(await call("/api/settings/export?includeKeys=true"));
    exported.document.ui = { ...exported.document.ui, theme: "dark" };

    const res = await call("/api/settings/import", jsonRequest("POST", { document: exported.document }));
    expect(res.status).toBe(200);
    const body = await bodyOf(res);
    expect(body).toMatchObject({ ok: true, revision: 1, migrated: false });
    expect(typeof body.backupPath).toBe("string");
    expect(existsSync(body.backupPath)).toBe(true);
    expect(readJson(body.backupPath).connections[0].apiKey).toBe(KEY);
    expect(dirname(body.backupPath)).toBe(join(ws, "config-versions", "settings"));

    const stored = storedSettings(ws);
    expect(stored.version).toBe(2);
    expect(stored.ui.theme).toBe("dark");
    expect(stored.connections[0].apiKey).toBe(KEY);
  });

  test("导入 v1 文件（裸文档、无 version）：迁移成 v2 并保留自定义键", async () => {
    const ws = settingsWorkspace();
    const { call } = setup({ workspaceDir: ws, env: {} });
    const v1 = {
      connections: [{ name: "local", baseUrl: "http://localhost:8000/v1", apiKey: "sk-local-xy12", models: ["qwen"] }],
      defaultConnection: "local",
      defaultModel: "qwen",
      keepMe: { yes: true },
    };
    const res = await call("/api/settings/import", jsonRequest("POST", v1));
    expect(res.status).toBe(200);
    const body = await bodyOf(res);
    expect(body).toMatchObject({ ok: true, migrated: true, revision: 1 });

    const stored = storedSettings(ws);
    expect(stored.version).toBe(2);
    expect(stored.ui).toEqual(UI_DEFAULTS);
    expect(stored.keepMe).toEqual({ yes: true });
    expect(stored.connections[0].apiKey).toBe("sk-local-xy12");
  });

  test("导入非法文件：400 且文件不变", async () => {
    const ws = settingsWorkspace();
    const before = readFileSync(join(ws, "settings.json"), "utf8");
    const { call } = setup({ workspaceDir: ws, env: {} });

    for (const bad of [
      { ui: { theme: "neon" } },
      { execution: { stepTimeoutMs: -1 } },
      { connections: "nope" },
      { version: 9 },
      "not-an-object",
    ]) {
      const res = await call("/api/settings/import", jsonRequest("POST", bad));
      expect(res.status).toBe(400);
    }
    expect(readFileSync(join(ws, "settings.json"), "utf8")).toBe(before);
    expect(existsSync(join(ws, "config-versions", "settings"))).toBe(false);
  });
});

describe("诊断", () => {
  test("GET /api/settings/diagnostics：版本、运行时、工作区、脱敏快照、最近事件，且无明文 Key", async () => {
    const ws = settingsWorkspace();
    const { call, engine } = setup({ workspaceDir: ws, env: {} });
    await engine.create({ employee: "weekly-report", form: {}, attachments: [], actor: "initiator" });

    const res = await call("/api/settings/diagnostics");
    expect(res.status).toBe(200);
    const text = await res.text();
    expect(text).not.toContain(KEY);
    expect(text).not.toContain(LOCAL_KEY);

    const body = JSON.parse(text) as Record<string, any>;
    expect(body).toMatchObject({ ok: true, app: { settingsVersion: 2 } });
    expect(typeof body.app.version).toBe("string");
    expect(body.runtime.platform).toBe(process.platform);
    expect(body.runtime.arch).toBe(process.arch);
    expect(typeof body.runtime.bun).toBe("string");
    expect(body.workspace.workspaceDir).toBe(ws);
    expect(body.workspace.settingsFile).toBe(join(ws, "settings.json"));
    expect(body.settings.connections.find((c: any) => c.name === "deepseek")).toMatchObject({ apiKeyLast4: "1234", hasKey: true });
    expect(body.tasks[0].events.map((e: any) => e.type)).toContain("task_created");
  });

  test("POST /api/settings/diagnostics/export：落盘并返回路径，文件里没有明文 Key", async () => {
    const ws = settingsWorkspace();
    const { call } = setup({ workspaceDir: ws, env: {} });
    const res = await call("/api/settings/diagnostics/export", { method: "POST" });
    expect(res.status).toBe(200);
    const body = await bodyOf(res);
    expect(body.ok).toBe(true);
    expect(existsSync(body.path)).toBe(true);
    const written = readFileSync(body.path, "utf8");
    expect(written).not.toContain(KEY);
    expect(JSON.parse(written).runtime.platform).toBe(process.platform);
  });

  test("PATCH execution / diagnostics 后：GET 与诊断包都反映新值，密钥仍在文件里", async () => {
    const ws = settingsWorkspace();
    const { call } = setup({ workspaceDir: ws, env: {} });

    const res = await call(
      "/api/settings/app",
      jsonRequest("PATCH", {
        revision: 0,
        execution: { stepTimeoutMs: 45000, modelMaxRetries: 3, retryBackoffMs: [200, 800], qcAutofix: 1 },
        diagnostics: { logLevel: "debug" },
      }),
    );
    expect(res.status).toBe(200);
    const patched = await bodyOf(res);
    expect(patched).toMatchObject({
      ok: true,
      revision: 1,
      execution: { stepTimeoutMs: 45000, modelMaxRetries: 3, retryBackoffMs: [200, 800], qcAutofix: 1 },
      diagnostics: { logLevel: "debug" },
    });

    const stored = storedSettings(ws);
    expect(stored.execution.retryBackoffMs).toEqual([200, 800]);
    expect(stored.diagnostics.logLevel).toBe("debug");
    expect(stored.connections[0].apiKey).toBe(KEY);

    const diagnostics = await bodyOf(await call("/api/settings/diagnostics"));
    expect(diagnostics.settings.execution).toMatchObject({ stepTimeoutMs: 45000, retryBackoffMs: [200, 800] });
    expect(diagnostics.settings.diagnostics).toEqual({ logLevel: "debug" });

    const exported = await bodyOf(await call("/api/settings/diagnostics/export", { method: "POST" }));
    const written = readFileSync(exported.path, "utf8");
    expect(written).not.toContain(KEY);
    expect(JSON.parse(written).settings.diagnostics.logLevel).toBe("debug");
  });

  test("非法退避数组 / 日志级别：400 且不写盘", async () => {
    const ws = settingsWorkspace();
    const before = readFileSync(join(ws, "settings.json"), "utf8");
    const { call } = setup({ workspaceDir: ws, env: {} });

    for (const bad of [
      { execution: { retryBackoffMs: [-1] } },
      { execution: { retryBackoffMs: Array.from({ length: 11 }, () => 100) } },
      { execution: { retryBackoffMs: "500,1500" } },
      { execution: { retryBackoffMs: [600001] } },
      { diagnostics: { logLevel: "verbose" } },
    ]) {
      const res = await call("/api/settings/app", jsonRequest("PATCH", { revision: 0, ...bad }));
      expect(res.status).toBe(400);
    }
    expect(readFileSync(join(ws, "settings.json"), "utf8")).toBe(before);
  });

  test("POST /api/settings/diagnostics/open：打开诊断目录（不存在时先建出来）", async () => {
    const ws = settingsWorkspace();
    const opened: string[] = [];
    const { call } = setup({ workspaceDir: ws, env: {}, openFolder: (dir: string) => void opened.push(dir) });
    const res = await call("/api/settings/diagnostics/open", { method: "POST" });
    expect(res.status).toBe(200);
    const body = await bodyOf(res);
    expect(body).toMatchObject({ ok: true, path: join(ws, "diagnostics") });
    expect(opened).toEqual([join(ws, "diagnostics")]);
    expect(existsSync(join(ws, "diagnostics"))).toBe(true);
  });
});

describe("POST /api/settings/open-file", () => {
  test("用注入的打开器打开 settings.json", async () => {
    const ws = settingsWorkspace();
    const opened: string[] = [];
    const { call } = setup({ workspaceDir: ws, env: {}, openFile: (path: string) => void opened.push(path) });
    const res = await call("/api/settings/open-file", { method: "POST" });
    expect(res.status).toBe(200);
    const body = await bodyOf(res);
    expect(body).toMatchObject({ ok: true, path: join(ws, "settings.json") });
    expect(opened).toEqual([join(ws, "settings.json")]);
  });

  test("文件不存在时先写一份默认文档再打开", async () => {
    const ws = tempDir();
    const opened: string[] = [];
    const { call } = setup({ workspaceDir: ws, env: {}, openFile: (path: string) => void opened.push(path) });
    const res = await call("/api/settings/open-file", { method: "POST" });
    expect(res.status).toBe(200);
    expect(opened).toEqual([join(ws, "settings.json")]);
    expect(storedSettings(ws)).toMatchObject({ version: 2, revision: 1 });
  });
});

// T20：连接卡片“高级”区（每模型上下文窗口/输出上限/档位 + 连接级超时/重试）。
describe("PUT /api/settings/connections · 高级参数", () => {
  const advanced = {
    baseUrl: "https://api.deepseek.com",
    models: ["deepseek-chat", "deepseek-reasoner"],
    modelOptions: {
      "deepseek-reasoner": { contextWindow: 65_536, maxTokens: 4_096, defaultReasoningEffort: "high", reasoningEfforts: ["off", "low", "high"] },
      "deepseek-chat": { maxTokens: 2_048 },
    },
    timeoutMs: 45_000,
    maxRetries: 4,
  };

  test("保存每模型参数与连接级超时/重试，可经 /app 视图读回", async () => {
    const ws = settingsWorkspace();
    const { call } = setup({ workspaceDir: ws, env: {} });

    const res = await call("/api/settings/connections/deepseek", jsonRequest("PUT", advanced));
    expect(res.status).toBe(200);
    const body = await bodyOf(res);
    expect(body.connection).toMatchObject({ name: "deepseek", timeoutMs: 45_000, maxRetries: 4 });
    expect(body.connection.modelOptions["deepseek-reasoner"]).toMatchObject({ contextWindow: 65_536, maxTokens: 4_096 });

    const stored = readJson(join(ws, "settings.json")).connections.find((c: any) => c.name === "deepseek");
    expect(stored.timeoutMs).toBe(45_000);
    expect(stored.maxRetries).toBe(4);
    expect(stored.modelOptions["deepseek-reasoner"].defaultReasoningEffort).toBe("high");

    const readBack = (await bodyOf(await call("/api/settings/app"))).connections.find((c: any) => c.name === "deepseek");
    expect(readBack).toMatchObject({ timeoutMs: 45_000, maxRetries: 4 });
    expect(readBack.modelOptions["deepseek-chat"]).toMatchObject({ maxTokens: 2_048 });
  });

  test("再保存时没传高级字段就保持原值，不因改模型列表而丢掉", async () => {
    const ws = settingsWorkspace();
    const { call } = setup({ workspaceDir: ws, env: {} });
    await call("/api/settings/connections/deepseek", jsonRequest("PUT", advanced));

    const res = await call("/api/settings/connections/deepseek", jsonRequest("PUT", { models: ["deepseek-chat"] }));
    expect(res.status).toBe(200);
    const stored = readJson(join(ws, "settings.json")).connections.find((c: any) => c.name === "deepseek");
    expect(stored.models).toEqual(["deepseek-chat"]);
    expect(stored.timeoutMs).toBe(45_000);
    expect(stored.maxRetries).toBe(4);
    expect(stored.modelOptions["deepseek-reasoner"].contextWindow).toBe(65_536);
  });

  test("传 null 清除连接级参数与每模型参数", async () => {
    const ws = settingsWorkspace();
    const { call } = setup({ workspaceDir: ws, env: {} });
    await call("/api/settings/connections/deepseek", jsonRequest("PUT", advanced));

    const res = await call("/api/settings/connections/deepseek", jsonRequest("PUT", { timeoutMs: null, maxRetries: null, modelOptions: null }));
    expect(res.status).toBe(200);
    const stored = readJson(join(ws, "settings.json")).connections.find((c: any) => c.name === "deepseek");
    expect(stored.timeoutMs).toBeUndefined();
    expect(stored.maxRetries).toBeUndefined();
    expect(stored.modelOptions).toBeUndefined();
  });

  test("非法高级参数返回 400 且文件不动", async () => {
    const cases: Record<string, unknown>[] = [
      { modelOptions: { "deepseek-chat": { contextWindow: 0 } } },
      { modelOptions: { "deepseek-chat": { maxTokens: -1 } } },
      { modelOptions: { "deepseek-chat": { defaultReasoningEffort: "turbo" } } },
      { modelOptions: { "deepseek-chat": { reasoningEfforts: ["hyper"] } } },
      { modelOptions: { "deepseek-chat": { contextWindow: "大" } } },
      { modelOptions: ["不是对象"] },
      { timeoutMs: 0 },
      { timeoutMs: -100 },
      { timeoutMs: 3_600_001 },
      { timeoutMs: 1.5 },
      { maxRetries: 11 },
      { maxRetries: -1 },
      { maxRetries: 1.5 },
      { maxRetries: "两次" },
    ];
    for (const patch of cases) {
      const ws = settingsWorkspace();
      const before = readFileSync(join(ws, "settings.json"), "utf8");
      const { call } = setup({ workspaceDir: ws, env: {} });
      const res = await call("/api/settings/connections/deepseek", jsonRequest("PUT", { models: ["deepseek-chat"], ...patch }));
      expect({ patch, status: res.status }).toEqual({ patch, status: 400 });
      expect(readFileSync(join(ws, "settings.json"), "utf8")).toBe(before);
    }
  });
});

// ---------------------------------------------------------------- T23：权限预设、默认角色与审计

/** 带角色头的请求：审计要记录操作角色。 */
function roleRequest(method: string, body: unknown, role = "operator"): RequestInit {
  return { method, headers: { "content-type": "application/json", "x-role": role }, body: JSON.stringify(body) };
}

function auditLines(ws: string): Record<string, any>[] {
  const file = join(ws, AUDIT_FILE_NAME);
  if (!existsSync(file)) return [];
  return readFileSync(file, "utf8")
    .split(/\r?\n/)
    .filter((line) => line.trim() !== "")
    .map((line) => JSON.parse(line) as Record<string, any>);
}

describe("T23 · 审计留痕（接缝 A）", () => {
  test("恢复默认员工配置成功后写一行：时间、角色、操作、目标、结果", async () => {
    const ws = tempDir();
    const employeesDir = join(ws, "employees");
    mkdirSync(join(employeesDir, "weekly-report"), { recursive: true });
    writeFileSync(join(employeesDir, "weekly-report", "workflow.yaml"), "id: modified\n");
    const defaultsDir = tempDir();
    mkdirSync(join(defaultsDir, "weekly-report"));
    writeFileSync(join(defaultsDir, "weekly-report", "workflow.yaml"), "id: default\n");

    const { call } = setup({ workspaceDir: ws, employeesDir, defaultEmployeesDir: defaultsDir, env: {} });
    const res = await call("/api/settings/workspace/reset-employees", { method: "POST", headers: { "x-role": "operator" } });
    expect(res.status).toBe(200);

    const lines = auditLines(ws);
    expect(lines).toHaveLength(1);
    expect(lines[0]).toMatchObject({
      role: "operator",
      action: "reset-employees",
      target: employeesDir,
      result: "success",
    });
    expect(lines[0]!.time).toMatch(/^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{3}Z$/);
    expect(JSON.stringify(lines)).not.toContain("id: default");
  });

  test("恢复失败也留痕：默认目录不存在 → result=failure（不会伪造成功记录）", async () => {
    const ws = tempDir();
    const employeesDir = join(ws, "employees");
    const missing = join(ws, "defaults-missing");
    const { call } = setup({ workspaceDir: ws, employeesDir, defaultEmployeesDir: missing, env: {} });

    const res = await call("/api/settings/workspace/reset-employees", { method: "POST", headers: { "x-role": "operator" } });
    expect((await bodyOf(res)).ok).toBe(false);

    const lines = auditLines(ws);
    expect(lines).toHaveLength(1);
    expect(lines[0]).toMatchObject({ action: "reset-employees", result: "failure" });
    expect(lines[0]!.detail).toContain("不存在");
  });

  test("导入成功与失败都留痕；审计文件绝不出现明文 Key", async () => {
    const ws = settingsWorkspace();
    const { call } = setup({ workspaceDir: ws, env: {} });
    const exported = await bodyOf(await call("/api/settings/export?includeKeys=true"));

    const ok = await call("/api/settings/import", roleRequest("POST", { document: exported.document }, "operator"));
    expect(ok.status).toBe(200);
    let lines = auditLines(ws);
    // 导出含密钥本身也留痕，这里只看最后一条导入记录
    expect(lines).toHaveLength(2);
    expect(lines[1]).toMatchObject({ action: "import-settings", target: "settings.json", result: "success" });
    expect(readFileSync(join(ws, AUDIT_FILE_NAME), "utf8")).not.toContain(KEY);

    const bad = await call("/api/settings/import", roleRequest("POST", { ui: { theme: "neon" } }, "reviewer"));
    expect(bad.status).toBe(400);
    lines = auditLines(ws);
    expect(lines).toHaveLength(3);
    expect(lines[2]).toMatchObject({ role: "reviewer", action: "import-settings", result: "failure" });
  });

  test("导出含密钥写审计；普通脱敏导出不写", async () => {
    const ws = settingsWorkspace();
    const { call } = setup({ workspaceDir: ws, env: {} });

    const plain = await call("/api/settings/export", { headers: { "x-role": "operator" } });
    expect(plain.status).toBe(200);
    expect(existsSync(join(ws, AUDIT_FILE_NAME))).toBe(false);

    const withKeys = await bodyOf(await call("/api/settings/export?includeKeys=true", { headers: { "x-role": "operator" } }));
    expect(withKeys.redacted).toBe(false);
    const lines = auditLines(ws);
    expect(lines).toHaveLength(1);
    expect(lines[0]).toMatchObject({ action: "export-settings-with-keys", target: withKeys.filename, result: "success" });
    expect(readFileSync(join(ws, AUDIT_FILE_NAME), "utf8")).not.toContain(KEY);
    expect(readFileSync(join(ws, AUDIT_FILE_NAME), "utf8")).not.toContain(LOCAL_KEY);
  });

  test("切到完全访问 / 从不询问分别留痕；revision 冲突的尝试记 failure", async () => {
    const ws = settingsWorkspace();
    const { call } = setup({ workspaceDir: ws, env: {} });

    const danger = await call("/api/settings/app", roleRequest("PATCH", { revision: 0, permissions: { sandboxMode: "danger-full-access" } }));
    expect(danger.status).toBe(200);
    const never = await call("/api/settings/app", roleRequest("PATCH", { revision: 1, permissions: { approvalPolicy: "never" } }, "reviewer"));
    expect(never.status).toBe(200);

    let lines = auditLines(ws);
    expect(lines.map((l) => [l.action, l.result, l.role])).toEqual([
      ["set-sandbox-mode", "success", "operator"],
      ["set-approval-policy", "success", "reviewer"],
    ]);

    const conflict = await call("/api/settings/app", roleRequest("PATCH", { revision: 0, permissions: { sandboxMode: "danger-full-access" } }));
    expect(conflict.status).toBe(409);
    lines = auditLines(ws);
    expect(lines).toHaveLength(3);
    expect(lines[2]).toMatchObject({ action: "set-sandbox-mode", result: "failure", target: "permissions.sandboxMode" });
  });

  test("模拟崩溃布防也留痕（危险演示操作）", async () => {
    const ws = settingsWorkspace();
    const { call } = setup({ workspaceDir: ws, env: {} });
    const res = await call("/api/settings/demo", roleRequest("PUT", { crashOnNextStep: true }, "operator"));
    expect(res.status).toBe(200);
    const lines = auditLines(ws);
    expect(lines).toHaveLength(1);
    expect(lines[0]).toMatchObject({ action: "simulate-crash", target: "demo.crashOnNextStep", result: "success" });
    // 关掉不需要审计
    await call("/api/settings/demo", roleRequest("PUT", { crashOnNextStep: false }, "operator"));
    expect(auditLines(ws)).toHaveLength(1);
  });

  test("非法预设 / 非法角色：400 且文件不变，不写成功审计", async () => {
    const ws = settingsWorkspace();
    const before = readFileSync(join(ws, "settings.json"), "utf8");
    const { call } = setup({ workspaceDir: ws, env: {} });

    for (const body of [
      { revision: 0, permissions: { sandboxMode: "sandbox" } },
      { revision: 0, permissions: { approvalPolicy: "sometimes" } },
    ]) {
      const res = await call("/api/settings/app", roleRequest("PATCH", body));
      expect(res.status).toBe(400);
    }
    expect((await call("/api/settings/role", roleRequest("PUT", { role: "admin" }))).status).toBe(400);
    expect((await call("/api/settings/role", roleRequest("PUT", { role: "root" }))).status).toBe(400);

    expect(readFileSync(join(ws, "settings.json"), "utf8")).toBe(before);
    expect(auditLines(ws)).toHaveLength(0);
  });

  test("完全访问预设只改设置值，不改变任何真实文件权限", async () => {
    const ws = settingsWorkspace();
    const note = join(ws, "note.txt");
    writeFileSync(note, "现场文件\n");
    const modeBefore = statSync(note).mode;
    const { call } = setup({ workspaceDir: ws, env: {} });

    const res = await call("/api/settings/app", roleRequest("PATCH", { revision: 0, permissions: { sandboxMode: "danger-full-access" } }));
    expect(res.status).toBe(200);
    const view = await bodyOf(res);
    expect(view.permissions).toMatchObject({ sandboxMode: "danger-full-access", approvalPolicy: "ask" });

    // 只写设置文档：文件内容与权限位不变（本机模式没有系统级沙箱）
    expect(readFileSync(note, "utf8")).toBe("现场文件\n");
    expect(statSync(note).mode).toBe(modeBefore);
    expect(storedSettings(ws).permissions.sandboxMode).toBe("danger-full-access");
  });
});

describe("T23 · 审计行格式（纯函数）", () => {
  test("字段顺序固定、UTC ISO 时间、单行 JSON", () => {
    const line = formatAuditLine({
      time: new Date("2026-11-05T09:19:29.000Z"),
      role: "operator",
      action: "export-settings-with-keys",
      target: "settings-with-keys-20261105091929.json",
      result: "success",
    });
    expect(line).toBe(
      '{"time":"2026-11-05T09:19:29.000Z","role":"operator","action":"export-settings-with-keys","target":"settings-with-keys-20261105091929.json","result":"success"}\n',
    );
  });

  test("脱敏：明文 Key、Bearer 令牌与 apiKey 字段都打码", () => {
    const line = formatAuditLine({
      time: new Date("2026-11-05T09:19:29.000Z"),
      role: "operator",
      action: "import-settings",
      target: "settings.json",
      result: "failure",
      detail: 'key=sk-secret-1234 Authorization: Bearer abc.def apiKey=sk-local-abcd {"apiKey":"sk-deep-5678"}',
    });
    expect(line).not.toContain("sk-secret-1234");
    expect(line).not.toContain("sk-local-abcd");
    expect(line).not.toContain("sk-deep-5678");
    expect(line).not.toContain("abc.def");
    expect(line).toContain("sk-***");
    expect(line).toContain("Bearer ***");
    expect(line.split("\n")).toHaveLength(2); // 只有末尾一个换行
  });

  test("detail 里的换行不会把一行审计拆成两行", () => {
    const line = formatAuditLine({
      time: new Date("2026-11-05T09:19:29.000Z"),
      role: "operator",
      action: "import-settings",
      target: "settings.json",
      result: "failure",
      detail: "第一行\n第二行",
    });
    expect(line.trimEnd().split(/\r?\n/)).toHaveLength(1);
  });

  test("损坏行跳过：坏 JSON / 缺字段的行不影响其余记录", () => {
    const ws = tempDir();
    writeFileSync(
      join(ws, AUDIT_FILE_NAME),
      [
        JSON.stringify({ time: "2026-11-05T09:19:29.000Z", role: "operator", action: "import-settings", target: "settings.json", result: "success" }),
        "不是 JSON",
        JSON.stringify({ time: 1, role: "operator", action: "reset-employees" }),
        JSON.stringify({ time: "2026-11-05T09:19:30.000Z", role: "operator", action: "export-settings-with-keys", result: "success" }),
        "",
      ].join("\n"),
    );
    expect(readAuditEntries(ws).map((entry) => entry.action)).toEqual(["import-settings"]);
  });
});
