import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import resvgWasm from "@resvg/resvg-wasm/index_bg.wasm" with { type: "file" };
import { join, resolve } from "node:path";
import { createToolsServer, type ToolsServerOptions } from "../src/index";

export const SAMPLES = resolve(import.meta.dir, "../../../samples/scenario-a");
export const sample = (name: string) => join(SAMPLES, name);
export const att = (name: string) => ({ name, path: sample(name) });

export interface Harness {
  client: Client;
  tools: ReturnType<typeof createToolsServer>;
  close(): Promise<void>;
  /** 调用工具，成功时返回 structuredContent；失败时抛出带 isError 文本的错误 */
  call<T = any>(name: string, args: Record<string, unknown>): Promise<T>;
  /** 原始调用结果（含 isError） */
  raw(name: string, args: Record<string, unknown>): Promise<any>;
}

export async function connect(options?: ToolsServerOptions): Promise<Harness> {
  // 测试同样走注入：Bun 文件导入拿到 wasm 真实路径
  const tools = createToolsServer({ resvgWasm, ...options });
  const [a, b] = InMemoryTransport.createLinkedPair();
  const client = new Client({ name: "test", version: "0.0.0" });
  await Promise.all([tools.server.connect(a), client.connect(b)]);
  const raw = (name: string, args: Record<string, unknown>) =>
    client.callTool({ name, arguments: args }, undefined, { timeout: 120_000 });
  return {
    client,
    tools,
    close: () => client.close(),
    raw,
    async call(name, args) {
      const r: any = await raw(name, args);
      if (r.isError) throw new Error(`${name} 失败：${r.content?.[0]?.text}`);
      return r.structuredContent;
    },
  };
}

/** 比例按 1 位小数比较（输入为小数，如 0.123 → 12.3） */
export const pct1 = (x: number | null) => (x === null ? null : Math.round(x * 1000) / 10);
/** 金额（分）→ 元，取整 */
export const yuan = (cents: number) => Math.round(cents / 100);
