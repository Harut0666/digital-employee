import { describe, expect, test } from "bun:test";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { loadSettings, resolveWorkspace } from "../../src/config";

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
