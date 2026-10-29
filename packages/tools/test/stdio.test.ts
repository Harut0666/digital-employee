import { describe, expect, test } from "bun:test";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import { join } from "node:path";

describe("stdio 入口", () => {
  test("bun src/stdio.ts 可作为子进程提供工具", async () => {
    const transport = new StdioClientTransport({
      command: process.execPath,
      args: [join(import.meta.dir, "../src/stdio.ts")],
    });
    const client = new Client({ name: "test", version: "0.0.0" });
    await client.connect(transport);
    try {
      const { tools } = await client.listTools();
      expect(tools.map((t) => t.name)).toContain("compute_metrics");
      const r: any = await client.callTool({ name: "web_search", arguments: { query: "x" } });
      expect(r.isError).toBe(true);
    } finally {
      await client.close();
    }
  });
});
