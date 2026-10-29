// contradiction_coverage：程序判出的每条纪要矛盾都必须写进周报（docs/design.md §10.5）。
// 契约来源：DraftReport.contradictionRefs 引用矛盾 id；“异常与原因”章节出现矛盾对象名。
import { describe, expect, test } from "bun:test";
import type { DetectedContradiction, DraftReport, QcCheckResult } from "../../src/contract";
import { createQcEngine } from "../../src/qc";
import { makeDraft, makeInput } from "./fixtures";

const engine = createQcEngine();

function run(overrides: Parameters<typeof makeInput>[0] = {}): QcCheckResult[] {
  return engine.run(makeInput(overrides));
}

function get(results: QcCheckResult[], id: string): QcCheckResult {
  const found = results.find((r) => r.id === id);
  if (!found) throw new Error(`no result for ${id}: ${results.map((r) => r.id).join(",")}`);
  return found;
}

/** 程序从 A-01 纪要判出的矛盾：华南“基本持平”，实际环比 -12.7%。 */
const huananContradiction: DetectedContradiction = {
  id: "contradiction:region:华南",
  subject: { level: "region", key: "华南", name: "华南" },
  claim: { direction: "flat" },
  actualWow: -0.127,
  rule: "flat",
  sourceWeek: { year: 2026, week: 44 },
  quote: "本周华南整体销售与上周基本持平",
};

/** 在草稿里写一段矛盾说明：引用矛盾 id，并在“异常与原因”里写出对象名。 */
function draftWithContradiction(contradiction: DetectedContradiction = huananContradiction): DraftReport {
  const draft = makeDraft();
  const text = `第 44 周纪要称${contradiction.subject.name}与上周基本持平，数据显示${contradiction.subject.name}环比 {{regions.华南.wow}}，以数据为准。`;
  return {
    ...draft,
    sections: draft.sections.map((s) =>
      s.id === "anomalies" ? { ...s, paragraphs: [...(s.paragraphs ?? []), text] } : s,
    ),
    contradictionRefs: [contradiction.id],
  };
}

describe("纪要矛盾覆盖 contradiction_coverage", () => {
  test("矛盾已引用、且“异常与原因”章节出现对象名 → 通过", () => {
    const r = get(run({ draft: draftWithContradiction(), contradictions: [huananContradiction] }), "contradiction_coverage");
    expect(r.passed).toBe(true);
    expect(r.issues).toEqual([]);
    expect(r.fixStep).toBeUndefined();
  });

  test("没有程序判出的矛盾 → 通过（不要求 contradictionRefs）", () => {
    expect(get(run({ contradictions: [] }), "contradiction_coverage").passed).toBe(true);
    expect(get(run(), "contradiction_coverage").passed).toBe(true);
  });

  test("有矛盾但草稿没引用 → 不通过，重跑写作步骤", () => {
    const draft = draftWithContradiction();
    const r = get(run({ draft: { ...draft, contradictionRefs: undefined }, contradictions: [huananContradiction] }), "contradiction_coverage");
    expect(r.passed).toBe(false);
    expect(r.fixStep).toBe("write_report");
    expect(r.issues.join("\n")).toContain("contradiction:region:华南");
  });

  test("引用了矛盾，但“异常与原因”章节没有出现对象名 → 不通过", () => {
    const draft = makeDraft(); // 异常章节只提到华东与 HZ01
    const r = get(run({ draft: { ...draft, contradictionRefs: [huananContradiction.id] }, contradictions: [huananContradiction] }), "contradiction_coverage");
    expect(r.passed).toBe(false);
    expect(r.issues.join("\n")).toContain("华南");
    expect(r.fixStep).toBe("write_report");
  });

  test("两条矛盾只引用了一条 → 只报没引用的那条", () => {
    const second: DetectedContradiction = {
      ...huananContradiction,
      id: "contradiction:region:华东",
      subject: { level: "region", key: "华东", name: "华东" },
      actualWow: -0.12,
    };
    const r = get(run({ draft: draftWithContradiction(), contradictions: [huananContradiction, second] }), "contradiction_coverage");
    expect(r.passed).toBe(false);
    const text = r.issues.join("\n");
    expect(text).toContain("华东");
    expect(text).not.toContain("华南");
  });

  test("fixStep 跟随 qcFixSteps.write；配置里关掉则不出现在结果里", () => {
    const draft = { ...draftWithContradiction(), contradictionRefs: undefined };
    expect(get(run({ draft, contradictions: [huananContradiction], fixSteps: { write: "compose_report", causes: "link_causes" } }), "contradiction_coverage").fixStep).toBe(
      "compose_report",
    );
    const config = makeInput().config;
    config.builtin.contradiction_coverage.enabled = false;
    expect(run({ config }).map((r) => r.id)).not.toContain("contradiction_coverage");
  });
});
