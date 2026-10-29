import { afterEach, describe, expect, test } from "bun:test";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { connect, sample, type Harness } from "./helpers";

let h: Harness | undefined;
afterEach(async () => {
  await h?.close();
  h = undefined;
});

const args = () => ({ salesPath: sample("销售明细.csv"), outDir: mkdtempSync(join(tmpdir(), "de-fault-")) });

describe("故障注入", () => {
  test("timeout：该工具按超时失败，返回 isError", async () => {
    h = await connect({ faultTimeoutMs: 20 });
    h.tools.setFaults({ tools: { load_sales: "timeout" } });
    const r = await h.raw("load_sales", args());
    expect(r.isError).toBe(true);
    expect(r.content[0].text).toContain("超时");
  });

  test("error：返回 isError 与中文报错", async () => {
    h = await connect();
    h.tools.setFaults({ tools: { load_sales: "error" } });
    const r = await h.raw("load_sales", args());
    expect(r.isError).toBe(true);
    expect(r.content[0].text).toContain("load_sales");
  });

  test("invalid_output：不会把非法数据当作正常结果交出去", async () => {
    h = await connect();
    h.tools.setFaults({ tools: { load_sales: "invalid_output" } });
    const r = await h.raw("load_sales", args());
    expect(r.isError).toBe(true);
    expect(r.structuredContent?.datasetPath).toBeUndefined();
  });

  test("只影响列出的工具；清空后恢复正常", async () => {
    h = await connect();
    h.tools.setFaults({ tools: { load_sales: "error" } });
    const other = await h.raw("parse_minutes", { paths: [sample("周会纪要_2026W44.docx")] });
    expect(other.isError).toBeFalsy();
    h.tools.setFaults({ tools: {} });
    const ok = await h.raw("load_sales", args());
    expect(ok.isError).toBeFalsy();
  });

  test("创建时即可带故障配置", async () => {
    h = await connect({ faults: { tools: { compute_metrics: "error" } } });
    const r = await h.raw("compute_metrics", { datasetPath: "x", period: { year: 2026, week: 44 }, anomalyThreshold: 0.1 });
    expect(r.isError).toBe(true);
    expect(r.content[0].text).toContain("故障注入");
  });
});

describe("web_search", () => {
  test("只注册，调用即返回“未开放”错误", async () => {
    h = await connect();
    const tools = await h.client.listTools();
    expect(tools.tools.map((t) => t.name)).toContain("web_search");
    const r = await h.raw("web_search", { query: "青禾烘焙" });
    expect(r.isError).toBe(true);
    expect(r.content[0].text).toContain("未开放");
  });
});

describe("工具清单", () => {
  test("场景 A 的工具都带有出参 Schema（web_search 除外）", async () => {
    h = await connect();
    const { tools } = await h.client.listTools();
    const names = tools.map((t) => t.name).sort();
    expect(names).toEqual(
      ["compute_metrics", "load_sales", "parse_minutes", "render_report_docx", "validate_submission", "web_search"].sort(),
    );
    for (const t of tools) if (t.name !== "web_search") expect(t.outputSchema).toBeDefined();
  });
});
