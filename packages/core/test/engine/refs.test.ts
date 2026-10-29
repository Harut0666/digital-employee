import { afterEach, describe, expect, test } from "bun:test";
import { resolveInput, type RefContext } from "../../src/engine/refs";
import { cleanupAll, createHarness, createStubJudgment } from "./helpers/harness";

afterEach(cleanupAll);

const ctx: RefContext = {
  form: { period: { year: 2026, week: 44 }, flag: false },
  taskDir: "/t/1",
  attachments: [{ name: "a.xlsx", path: "/t/1/attachments/a.xlsx" }],
  feedback: { comment: "改", sections: ["核心指标"] },
  qcConfig: { builtin: { anomaly_coverage: { threshold: 0.1 } } },
  stepOutput: (id) =>
    id === "admission" ? { minutes: [{ path: "m1.docx", week: 44 }, { path: "m2.docx", week: 43 }], sales: { path: "s.xlsx" } } : undefined,
  renderedOutput: () => undefined,
  idempotencyKey: "KEY",
};

describe("$ 引用解析", () => {
  test("form / config / task / steps 与嵌套结构", () => {
    expect(resolveInput({ p: "$form.period", t: "$config.builtin.anomaly_coverage.threshold", d: "$task.dir", n: [1, "$form.period.week"] }, ctx)).toEqual({
      p: { year: 2026, week: 44 },
      t: 0.1,
      d: "/t/1",
      n: [1, 44],
    });
    expect(resolveInput("$steps.admission.sales.path", ctx)).toBe("s.xlsx");
    expect(resolveInput("$form.flag", ctx)).toBe(false);
  });

  test("$task.attachments / $task.feedback / $task.idempotencyKey", () => {
    expect(resolveInput("$task.attachments", ctx)).toEqual(ctx.attachments);
    expect(resolveInput("$task.feedback.sections", ctx)).toEqual(["核心指标"]);
    expect(resolveInput("$task.idempotencyKey", ctx)).toBe("KEY");
  });

  test("[*] 对数组逐项取值", () => {
    expect(resolveInput("$steps.admission.minutes[*].path", ctx)).toEqual(["m1.docx", "m2.docx"]);
    expect(resolveInput("$task.attachments[*].name", ctx)).toEqual(["a.xlsx"]);
  });

  test("引用不存在时解析为 null", () => {
    for (const ref of ["$form.nope", "$form.period.nope.deeper", "$steps.ghost", "$steps.ghost.a.b", "$config.x", "$task.zzz", "$rendered.ghost"]) {
      expect(resolveInput(ref, ctx)).toBeNull();
    }
    expect(resolveInput("$steps.admission.sales[*]", ctx)).toBeNull(); // 不是数组
    expect(resolveInput("$steps.admission.minutes[*].nope", ctx)).toEqual([null, null]); // 逐项取值，缺的项为 null
  });

  test("不是引用的字符串原样传入", () => {
    expect(resolveInput({ a: "$100", b: "普通文本", c: 3 }, ctx)).toEqual({ a: "$100", b: "普通文本", c: 3 });
  });
});

describe("$rendered.<stepId>", () => {
  test("草稿里的槽位用 factsStep 的事实渲染后再交给工具", async () => {
    const judgment = createStubJudgment({
      outputs: {
        write_report: () => ({
          title: "周报",
          sections: [{ id: "summary", heading: "本周摘要", paragraphs: ["全公司实收 {{company.revenue}}。"], bullets: ["华东环比 {{regions.华东.wow}}"] }],
          causes: [],
        }),
      },
    });
    const h = await createHarness({
      judgment: judgment.runner,
      patchWorkflow: (w) => (w.steps.find((s: any) => s.id === "render_document").input.report = "$rendered.write_report"),
    });
    h.tools.setHandler("compute_metrics", () => ({
      company: { revenueCents: 57_363_500 },
      regions: { 华东: { wow: -0.12 } },
    }));
    const settled = await h.run();
    expect(settled.state).toBe("awaiting_review");
    const render = h.tools.calls.find((c) => c.tool === "render_report_docx")!;
    const report = render.args.report as any;
    expect(report.sections[0].paragraphs[0]).toBe("全公司实收 573,635 元。");
    expect(report.sections[0].bullets[0]).toBe("华东环比 -12.0%");
  });
});
