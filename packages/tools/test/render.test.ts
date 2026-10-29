import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { existsSync, mkdtempSync, readdirSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import mammoth from "mammoth";
import type { RenderedReport, WeeklyFacts } from "@de/core/contract";
import { connect, sample, type Harness } from "./helpers";

let h: Harness;
let facts: WeeklyFacts;

beforeAll(async () => {
  h = await connect();
  const dir = mkdtempSync(join(tmpdir(), "de-render-ds-"));
  const ds = await h.call("load_sales", { salesPath: sample("销售明细.xlsx"), outDir: dir });
  facts = await h.call("compute_metrics", { datasetPath: ds.datasetPath, period: { year: 2026, week: 44 }, anomalyThreshold: 0.1 });
});
afterAll(async () => {
  await h.close();
});

const report = (extraParagraphs = 0): RenderedReport => ({
  title: "青禾烘焙经营周报（2026 年第 44 周）",
  sections: [
    {
      id: "summary",
      heading: "本周摘要",
      paragraphs: ["本周全公司实收 573,635 元，环比 -9.3%，毛利率 61.1%。", ...Array.from({ length: extraParagraphs }, (_, i) => `补充说明第 ${i + 1} 条：${"数据口径说明".repeat(12)}。`)],
    },
    { id: "anomalies", heading: "异常与原因", paragraphs: ["华东与华南环比下滑超过 10%。"], bullets: ["杭州湖滨店 10/27–10/29 停业三天（W44-p03）", "原因待核实"] },
    { id: "next", heading: "下周关注事项", paragraphs: [], bullets: ["静安店会员日活动方案"] },
  ],
});

const render = (key: string, r = report(), outDir?: string) =>
  h.call("render_report_docx", { report: r, facts, outDir: outDir ?? mkdtempSync(join(tmpdir(), "de-render-")), idempotencyKey: key });

describe("render_report_docx（接缝 2）", () => {
  test("生成 Word：能被 mammoth 读回，含标题、章节、核心指标表和至少 2 张图", async () => {
    const out = await render("task1/step-render/hash1");
    expect(existsSync(out.docxPath)).toBe(true);
    expect(out.docxPath.endsWith(".docx")).toBe(true);
    expect(out.chartCount).toBeGreaterThanOrEqual(2);
    expect(out.sha256).toMatch(/^[0-9a-f]{64}$/);

    const text = (await mammoth.extractRawText({ path: out.docxPath })).value;
    expect(text).toContain("青禾烘焙经营周报（2026 年第 44 周）");
    expect(text).toContain("本周摘要");
    expect(text).toContain("本周全公司实收 573,635 元");
    expect(text).toContain("下周关注事项");
    expect(text).toContain("静安店会员日活动方案");
    // 核心指标表：全公司与四个区域，数字取自 facts
    expect(text).toContain("全公司");
    for (const region of ["华东", "华南", "华北", "西南"]) expect(text).toContain(region);
    expect(text).toContain("573,635");
    expect(text).toContain("-9.3%");
    expect(text).toContain("84.1%");

    const html = (await mammoth.convertToHtml({ path: out.docxPath })).value;
    const imgs = [...html.matchAll(/<img[^>]+src="data:image\/png;base64,([^"]+)"/g)];
    expect(imgs.length).toBeGreaterThanOrEqual(2);
    for (const m of imgs) {
      const bytes = Buffer.from(m[1]!, "base64");
      expect(bytes.subarray(1, 4).toString()).toBe("PNG");
      expect(bytes.length).toBeGreaterThan(5_000); // 不是空白图
    }
  });

  test("幂等：同一幂等键调用两次，只生成一个文件，返回同一结果", async () => {
    const outDir = mkdtempSync(join(tmpdir(), "de-render-idem-"));
    const a = await render("task2/step-render/hashA", report(), outDir);
    const mtime = statSync(a.docxPath).mtimeMs;
    const b = await render("task2/step-render/hashA", report(), outDir);
    expect(b).toEqual(a);
    expect(statSync(b.docxPath).mtimeMs).toBe(mtime);
    expect(readdirSync(outDir).filter((f) => f.endsWith(".docx"))).toHaveLength(1);
    // 换一个键就是另一个文件
    const c = await render("task2/step-render/hashB", report(), outDir);
    expect(c.docxPath).not.toBe(a.docxPath);
    expect(readdirSync(outDir).filter((f) => f.endsWith(".docx"))).toHaveLength(2);
  });

  test("按版式估算页数：内容越多页数越多，常规周报在 3 页内", async () => {
    const small = await render("task3/step-render/small");
    const big = await render("task3/step-render/big", report(80));
    expect(small.estimatedPages).toBeGreaterThanOrEqual(1);
    expect(small.estimatedPages).toBeLessThanOrEqual(3);
    expect(big.estimatedPages).toBeGreaterThan(small.estimatedPages);
  });

  test("页数估算带一位小数（质检按 3 页 × 90% = 2.7 页判断，取整会让上限变成 2 页）", async () => {
    const small = await render("task3/step-render/fraction");
    expect(Math.round(small.estimatedPages * 10) / 10).toBe(small.estimatedPages);
    expect(Number.isInteger(small.estimatedPages)).toBe(false);
  });

  test("没有上周数据（第 36 周）也能出图和表", async () => {
    const dir = mkdtempSync(join(tmpdir(), "de-render-w36-"));
    const ds = await h.call("load_sales", { salesPath: sample("销售明细.xlsx"), outDir: dir });
    const f36 = await h.call("compute_metrics", { datasetPath: ds.datasetPath, period: { year: 2026, week: 36 }, anomalyThreshold: 0.1 });
    const out = await h.call("render_report_docx", {
      report: report(),
      facts: f36,
      outDir: dir,
      idempotencyKey: "task4/step-render/w36",
    });
    const text = (await mammoth.extractRawText({ path: out.docxPath })).value;
    expect(text).toContain("不可比");
    expect(out.chartCount).toBeGreaterThanOrEqual(2);
  });

  test("章节只有要点（bullets）时也能生成 Word 并估算页数", async () => {
    const r: RenderedReport = {
      title: "青禾烘焙经营周报（2026 年第 44 周）",
      sections: [
        { id: "summary", heading: "本周摘要", paragraphs: ["本周全公司实收 573,635 元。"] },
        { id: "categories", heading: "品类表现", bullets: ["饮品环比 -12.0%。"] },
        { id: "next", heading: "下周关注事项", bullets: ["静安店会员日活动方案"] },
      ],
    };
    const out = await render("task5/step-render/bullets", r);
    const text = (await mammoth.extractRawText({ path: out.docxPath })).value;
    expect(text).toContain("品类表现");
    expect(text).toContain("饮品环比 -12.0%。");
    expect(text).toContain("静安店会员日活动方案");
    expect(out.estimatedPages).toBeGreaterThanOrEqual(1);
  });

  test("核心指标：章节标题本身是核心指标时，渲染器不再插入同名表格标题（只出现一次）", async () => {
    const r: RenderedReport = {
      title: "青禾烘焙经营周报（2026 年第 44 周）",
      sections: [
        { id: "summary", heading: "本周摘要", paragraphs: ["本周全公司实收 573,635 元。"] },
        { id: "metrics", heading: "核心指标", paragraphs: ["西南目标达成率最高。"] },
        { id: "next", heading: "下周关注事项", bullets: ["静安店会员日活动方案"] },
      ],
    };
    const out = await render("task6/step-render/metrics-heading", r);
    const text = (await mammoth.extractRawText({ path: out.docxPath })).value;
    expect(text.split("核心指标").length - 1).toBe(1);
    // 章节顺序不变：摘要 → 核心指标（表格与图表跟在这一章里）→ 下周关注
    expect(text.indexOf("本周摘要")).toBeLessThan(text.indexOf("核心指标"));
    expect(text.indexOf("核心指标")).toBeLessThan(text.indexOf("下周关注事项"));
  });

  test("章节不是核心指标时，渲染器仍插入一次表格标题", async () => {
    const r: RenderedReport = {
      title: "青禾烘焙经营周报（2026 年第 44 周）",
      sections: [{ id: "summary", heading: "本周摘要", paragraphs: ["本周全公司实收 573,635 元。"] }],
    };
    const out = await render("task6/step-render/metrics-inserted", r);
    const text = (await mammoth.extractRawText({ path: out.docxPath })).value;
    expect(text.split("核心指标").length - 1).toBe(1);
  });

  test("数据不完整：正文开头醒目标注只覆盖到哪一天，且排在本周摘要之前", async () => {
    const partial: WeeklyFacts = {
      ...facts,
      period: { ...facts.period, to: "2026-11-01" },
      dataCoverage: { from: "2026-08-31", to: "2026-10-29" },
    };
    const out = await h.call("render_report_docx", {
      report: report(),
      facts: partial,
      outDir: mkdtempSync(join(tmpdir(), "de-render-partial-")),
      idempotencyKey: "task7/step-render/partial",
    });
    const text = (await mammoth.extractRawText({ path: out.docxPath })).value;
    expect(text).toContain("数据不完整");
    expect(text).toContain("10 月 29 日");
    // 标注在标题之后、第一段正文（本周摘要）之前：打开第一页就能看到
    expect(text.indexOf("数据不完整")).toBeGreaterThan(text.indexOf("青禾烘焙经营周报"));
    expect(text.indexOf("数据不完整")).toBeLessThan(text.indexOf("本周摘要"));
    // 标注本身也计入版式估算，仍不超过 3 页
    expect(out.estimatedPages).toBeGreaterThanOrEqual(1);
    expect(out.estimatedPages).toBeLessThanOrEqual(3);
  });

  test("数据覆盖完整时首页不出现“数据不完整”标注", async () => {
    const out = await render("task7/step-render/complete");
    const text = (await mammoth.extractRawText({ path: out.docxPath })).value;
    expect(text).not.toContain("数据不完整");
  });
});
