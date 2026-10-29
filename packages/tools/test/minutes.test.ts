import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { connect, sample, type Harness } from "./helpers";

let h: Harness;
beforeAll(async () => {
  h = await connect();
});
afterAll(async () => {
  await h.close();
});

describe("parse_minutes（接缝 2）", () => {
  test("识别周次与标题，切分带稳定 ID 和章节的段落", async () => {
    const r = await h.call("parse_minutes", { paths: [sample("周会纪要_2026W44.docx")] });
    expect(r.minutes).toHaveLength(1);
    const m = r.minutes[0];
    expect(m.week).toEqual({ year: 2026, week: 44 });
    expect(m.title).toContain("经营周会纪要");
    expect(m.paragraphs.length).toBeGreaterThan(8);
    for (const p of m.paragraphs) expect(p.id).toMatch(/^W44-p\d{2}$/);
    expect(new Set(m.paragraphs.map((p: any) => p.id)).size).toBe(m.paragraphs.length);
    expect(m.paragraphs[0].id).toBe("W44-p01");

    const hz = m.paragraphs.find((p: any) => p.text.includes("杭州湖滨店") && p.text.includes("停业"));
    expect(hz.section).toBe("各区域情况");
    expect(hz.text).toContain("10 月 27 日至 29 日");

    // 华南“基本持平”的说法需要能被定位到
    expect(m.paragraphs.some((p: any) => p.text.includes("基本持平"))).toBe(true);
    // 章节标题和文档标题本身不作为段落
    expect(m.paragraphs.some((p: any) => p.text === "二、各区域情况")).toBe(false);
  });

  test("行动项表格的每一行是一段，带列名", async () => {
    const r = await h.call("parse_minutes", { paths: [sample("周会纪要_2026W43.docx")] });
    const rows = r.minutes[0].paragraphs.filter((p: any) => p.section === "行动项");
    expect(rows).toHaveLength(4);
    expect(rows[0].text).toBe("事项：核实并处理南山店 10/21 重复数据；负责人：IT、孙某；截止日期：10/30");
  });

  test("可一次解析多份纪要", async () => {
    const r = await h.call("parse_minutes", {
      paths: [sample("周会纪要_2026W42.docx"), sample("周会纪要_2026W43.docx")],
    });
    expect(r.minutes.map((m: any) => m.week.week)).toEqual([42, 43]);
    expect(r.minutes[0].paragraphs[0].id).toBe("W42-p01");
  });
});
