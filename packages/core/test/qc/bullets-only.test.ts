// 真实模型的输出形态：品类表现、下周关注按提示词用要点写，章节里没有 paragraphs。
// 契约与质检、渲染都必须接受这种形态，而不是直接抛错。
import { describe, expect, test } from "bun:test";
import type { DraftReport } from "../../src/contract";
import { createQcEngine } from "../../src/qc";
import { narrativePieces } from "../../src/qc/text";
import { makeDraft, makeInput } from "./fixtures";

const engine = createQcEngine();

function replaceSections(draft: DraftReport, sections: DraftReport["sections"]): DraftReport {
  return { ...draft, sections };
}

describe("只有要点的章节（bullets-only）", () => {
  test("叙述文本收集：要点被收集，不因缺少 paragraphs 抛错", () => {
    const draft = replaceSections(makeDraft(), [
      { id: "categories", heading: "品类表现", bullets: ["饮品环比 {{categories.饮品.wow}}。"] },
      { id: "next_week", heading: "下周关注事项", bullets: ["跟进华东恢复情况"] },
    ]);
    expect(() => narrativePieces(draft)).not.toThrow();
    const pieces = narrativePieces(draft);
    expect(pieces.some((p) => p.where === "品类表现" && p.text.includes("饮品环比"))).toBe(true);
    expect(pieces.some((p) => p.where === "下周关注事项" && p.text.includes("跟进华东恢复情况"))).toBe(true);
  });

  test("结构质检：下周关注只有要点时按条数判断，不抛错", () => {
    const draft = replaceSections(
      makeDraft(),
      makeDraft().sections.map((s) =>
        s.id === "next_week" ? { id: "next_week", heading: "下周关注事项", bullets: ["a", "b"] } : s,
      ),
    );
    const structure = engine.run(makeInput({ draft })).find((r) => r.id === "structure");
    expect(structure?.passed).toBe(true);
  });

  test("数据溯源：只有要点的章节也参与裸数字检查", () => {
    const draft = replaceSections(makeDraft(), [
      { id: "categories", heading: "品类表现", bullets: ["饮品实收 123,456 元。"] },
    ]);
    const traceability = engine.run(makeInput({ draft })).find((r) => r.id === "traceability");
    expect(traceability?.passed).toBe(false);
    expect(traceability?.issues.join("\n")).toContain("123,456");
  });
});
