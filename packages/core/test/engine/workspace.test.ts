import { describe, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { loadDemoSettings, loadSettings, resolveWorkspace, saveDemoSettings } from "../../src/config";

const tempDir = () => mkdtempSync(join(tmpdir(), "de-core-ws-"));
describe("resolveWorkspace", () => {
  test("DE_WORKSPACE overrides the default", () => {
    expect(resolveWorkspace({ DE_WORKSPACE: "D:\\ws" })).toBe("D:\\ws");
  });
  test("defaults to %APPDATA%/digital-employee", () => {
    expect(resolveWorkspace({ APPDATA: "C:\\Users\\u\\AppData\\Roaming" }).replace(/\\/g, "/")).toBe(
      "C:/Users/u/AppData/Roaming/digital-employee",
    );
  });
});

describe("loadSettings", () => {
  test("without settings.json it yields a default DeepSeek connection; DEEPSEEK_API_KEY fills the key", () => {
    const ws = tempDir();
    const s = loadSettings(ws, { DEEPSEEK_API_KEY: "sk-env" });
    expect(s.defaultConnection).toBe("deepseek");
    expect(s.connections).toEqual([
      { name: "deepseek", baseUrl: "https://api.deepseek.com", apiKey: "sk-env", models: ["deepseek-chat"] },
    ]);
    expect(s.defaultModel).toBe("deepseek-chat");
  });

  test("the environment variable wins over the key stored in the file, for DeepSeek connections only", () => {
    const ws = tempDir();
    writeFileSync(
      join(ws, "settings.json"),
      JSON.stringify({
        connections: [
          { name: "deepseek", baseUrl: "https://api.deepseek.com", apiKey: "sk-file", models: ["deepseek-chat"] },
          { name: "local", baseUrl: "http://localhost:8000/v1", apiKey: "sk-local", models: ["qwen"] },
        ],
        defaultConnection: "deepseek",
        defaultModel: "deepseek-chat",
      }),
    );
    const withEnv = loadSettings(ws, { DEEPSEEK_API_KEY: "sk-env" });
    expect(withEnv.connections.map((c) => c.apiKey)).toEqual(["sk-env", "sk-local"]);
    const withoutEnv = loadSettings(ws, {});
    expect(withoutEnv.connections.map((c) => c.apiKey)).toEqual(["sk-file", "sk-local"]);
  });

  test("a malformed settings.json throws a readable error", () => {
    const ws = tempDir();
    writeFileSync(join(ws, "settings.json"), "{oops");
    expect(() => loadSettings(ws, {})).toThrow(/settings\.json/);
  });
});

describe("demo settings（settings.json 的 demo 段）", () => {
  test("missing file or missing demo section yields the defaults", () => {
    const empty = tempDir();
    expect(loadDemoSettings(empty)).toEqual({ toolFaults: {}, corruptTraceability: false, crashOnNextStep: false });

    const noDemo = tempDir();
    writeFileSync(
      join(noDemo, "settings.json"),
      JSON.stringify({ connections: [], defaultConnection: "deepseek", defaultModel: "deepseek-chat" }),
    );
    expect(loadDemoSettings(noDemo)).toEqual({ toolFaults: {}, corruptTraceability: false, crashOnNextStep: false });
  });

  test("reads the demo section; saving a patch keeps connections and the other demo fields", () => {
    const ws = tempDir();
    const file = join(ws, "settings.json");
    writeFileSync(
      file,
      JSON.stringify({
        connections: [{ name: "deepseek", baseUrl: "https://api.deepseek.com", apiKey: "sk-secret-1234", models: ["deepseek-chat"] }],
        defaultConnection: "deepseek",
        defaultModel: "deepseek-chat",
        demo: { toolFaults: { load_sales: "timeout" }, corruptTraceability: true, crashOnNextStep: true },
      }),
    );
    expect(loadDemoSettings(ws)).toEqual({
      toolFaults: { load_sales: "timeout" },
      corruptTraceability: true,
      crashOnNextStep: true,
    });

    const next = saveDemoSettings(ws, { crashOnNextStep: false });
    expect(next).toEqual({ toolFaults: { load_sales: "timeout" }, corruptTraceability: true, crashOnNextStep: false });

    const raw = JSON.parse(readFileSync(file, "utf8")) as Record<string, any>;
    expect(raw.connections).toHaveLength(1);
    expect(raw.connections[0].apiKey).toBe("sk-secret-1234");
    expect(raw.demo).toEqual({ toolFaults: { load_sales: "timeout" }, corruptTraceability: true, crashOnNextStep: false });
  });

  test("saving demo settings creates settings.json when it does not exist", () => {
    const ws = tempDir();
    saveDemoSettings(ws, { corruptTraceability: true });
    expect(loadDemoSettings(ws)).toEqual({ toolFaults: {}, corruptTraceability: true, crashOnNextStep: false });
    const raw = JSON.parse(readFileSync(join(ws, "settings.json"), "utf8")) as Record<string, unknown>;
    expect(raw.demo).toMatchObject({ corruptTraceability: true });
  });
});
