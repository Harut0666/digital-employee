// 设置页请求级测试（接缝：HTTP 接口）。重点：API Key 只回末 4 位、空 Key 保留原值、测试连接走注入的假提供者、
// 演示开关经 admin_set_faults 下发到工具子进程、工作区文件操作。
import { describe, expect, test } from "bun:test";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import type { FaultHooks, ModelSettings, ToolClient, ToolDescriptor } from "@de/core/contract";
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
