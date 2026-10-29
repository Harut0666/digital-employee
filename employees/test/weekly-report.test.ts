// 场景 A · 经营周报员工配置的验证：只通过契约（zod Schema、TOOL_NAMES、DraftReport）检查配置文件本身。
import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import Ajv from "ajv";
import {
  EmployeeWorkflowSchema,
  QcConfigSchema,
  TOOL_NAMES,
  type DraftReport,
  type EmployeeWorkflow,
  type JudgeStepConfig,
  type QcConfig,
} from "@de/core/contract";

const DIR = join(import.meta.dir, "..", "weekly-report");
const read = (rel: string) => readFileSync(join(DIR, rel), "utf8");
const readJson = (rel: string) => JSON.parse(read(rel)) as Record<string, any>;
const parseYaml = (rel: string): unknown => Bun.YAML.parse(read(rel));

const workflow: EmployeeWorkflow = EmployeeWorkflowSchema.parse(parseYaml("workflow.yaml"));
const qc: QcConfig = QcConfigSchema.parse(parseYaml("qc.yaml"));
const form = readJson("form.schema.json");
const judgeSteps = workflow.steps.filter((s): s is JudgeStepConfig => s.kind === "judge");

function newAjv() {
  return new Ajv({ allErrors: true, strict: true });
}

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const full = join(dir, name);
    return statSync(full).isDirectory() ? walk(full) : [full];
  });
}

/** 收集一个输入映射里全部以 "$" 开头的引用字符串 */
function collectRefs(value: unknown): string[] {
  if (typeof value === "string") return value.startsWith("$") ? [value] : [];
  if (Array.isArray(value)) return value.flatMap(collectRefs);
  if (value && typeof value === "object") return Object.values(value).flatMap(collectRefs);
  return [];
}

describe("workflow.yaml", () => {
  test("步骤顺序与设计一致，step id 唯一", () => {
    const ids = workflow.steps.map((s) => s.id);
    expect(new Set(ids).size).toBe(ids.length);
    expect(ids).toEqual([
      "admission",
      "load_data",
      "compute_metrics",
      "parse_minutes",
      "interpret_feedback",
      "match_causes",
      "write_report",
      "render_document",
      "qc",
    ]);
  });

  test("准入步骤存在，是第一个计算步骤，调用 validate_submission", () => {
    const first = workflow.steps.find((s) => s.kind === "compute");
    expect(first?.id).toBe(workflow.admissionStep);
    expect(first?.kind === "compute" && first.tool).toBe("validate_submission");
  });

  test("计算步骤绑定的工具与设计一致", () => {
    const tools = Object.fromEntries(
      workflow.steps.flatMap((s) => (s.kind === "compute" ? [[s.id, s.tool]] : [])),
    );
    expect(tools).toEqual({
      admission: "validate_submission",
      load_data: "load_sales",
      compute_metrics: "compute_metrics",
      parse_minutes: "parse_minutes",
      render_document: "render_report_docx",
    });
  });

  test("所有步骤用到的工具都在契约 TOOL_NAMES 中，且不开放 web_search", () => {
    const known = new Set<string>(TOOL_NAMES);
    for (const step of workflow.steps) {
      const used = step.kind === "compute" ? [step.tool] : step.kind === "judge" ? step.tools : [];
      for (const tool of used) expect(known.has(tool)).toBe(true);
    }
    for (const step of judgeSteps) expect(step.tools).not.toContain("web_search");
  });

  test("上限按 design.md §6 设置", () => {
    expect(workflow.limits.task).toEqual({ modelCalls: 30, durationSec: 600 });
    expect(workflow.limits.toolRetry).toEqual({ attempts: 3, backoffMs: [1000, 2000, 4000] });
    expect(workflow.limits.qcAutofix).toBe(2);
    expect(judgeSteps.length).toBe(3);
    for (const step of judgeSteps) expect(step.limits).toEqual({ rounds: 6, durationSec: 90 });
  });

  test("判断步骤：match_causes、write_report、interpret_feedback 退回时重跑", () => {
    expect(judgeSteps.map((s) => [s.id, s.rework])).toEqual([
      ["interpret_feedback", true],
      ["match_causes", true],
      ["write_report", true],
    ]);
  });

  test("判断步骤引用的提示词和输出 Schema 文件都存在，Schema 可编译", () => {
    for (const step of judgeSteps) {
      expect(existsSync(join(DIR, step.prompt))).toBe(true);
      expect(existsSync(join(DIR, step.outputSchema))).toBe(true);
      expect(() => newAjv().compile(readJson(step.outputSchema))).not.toThrow();
    }
  });

  test("$ 引用都能解析：步骤存在、表单字段存在、qc.yaml 路径存在", () => {
    const stepIds = workflow.steps.map((s) => s.id);
    const formProps = Object.keys(form.properties ?? {});
    // 重做时才有值的引用：前一版草稿来自 write_report 自己上一轮的输出
    const reworkOnly = new Set(["interpret_feedback:$steps.write_report", "write_report:$steps.write_report"]);
    for (const [index, step] of workflow.steps.entries()) {
      if (step.kind === "qc") continue;
      for (const ref of collectRefs(step.input)) {
        const parts = ref.slice(1).replace(/\[\*\]/g, "").split(".");
        if (parts[0] === "form") {
          expect(formProps).toContain(parts[1]);
        } else if (parts[0] === "steps") {
          const at = stepIds.indexOf(parts[1] ?? "");
          expect(at).toBeGreaterThanOrEqual(0);
          if (at >= index) expect(reworkOnly.has(`${step.id}:$steps.${parts[1]}`)).toBe(true);
        } else if (parts[0] === "config") {
          let node: any = qc;
          for (const key of parts.slice(1)) {
            expect(node).toBeDefined();
            node = node?.[key];
          }
          expect(node).toBeDefined();
        } else {
          expect(["task.dir", "task.attachments", "task.feedback"]).toContain(parts.join("."));
        }
      }
    }
  });
});

describe("qc.yaml", () => {
  test("七类内置质检齐全且启用", () => {
    const b = qc.builtin;
    for (const check of [b.structure, b.traceability, b.anomaly_coverage, b.quote_integrity, b.forbidden_terms, b.length, b.feedback_response]) {
      expect(check.enabled).toBe(true);
    }
  });

  test("结构参数：六个章节依次为设计要求，条数与图表下限", () => {
    expect(qc.builtin.structure.sections.map((s) => s.heading)).toEqual([
      "本周摘要",
      "核心指标",
      "品类表现",
      "异常与原因",
      "数据质量说明",
      "下周关注事项",
    ]);
    expect(new Set(qc.builtin.structure.sections.map((s) => s.id)).size).toBe(6);
    expect(qc.builtin.structure.summaryMaxSentences).toBe(5);
    expect(qc.builtin.structure.nextWeekMaxItems).toBe(5);
    expect(qc.builtin.structure.minCharts).toBe(2);
  });

  test("异常阈值、篇幅、矛盾判定阈值", () => {
    expect(qc.builtin.anomaly_coverage.threshold).toBe(0.1);
    expect(qc.builtin.length.maxPages).toBe(3);
    expect(qc.contradiction).toEqual({ flatTolerance: 0.05, directionMin: 0.02, magnitudeTolerance: 0.1 });
  });

  test("禁用词包含客单价和同比", () => {
    expect(qc.builtin.forbidden_terms.terms).toEqual(expect.arrayContaining(["客单价", "同比"]));
  });
});

describe("form.schema.json", () => {
  test("是合法的 JSON Schema（ajv 严格模式可编译）", () => {
    expect(() => newAjv().compile(form)).not.toThrow();
  });

  test("字段齐全，每个字段都有中文 title 和 description", () => {
    const props = form.properties as Record<string, any>;
    expect(Object.keys(props)).toEqual(
      expect.arrayContaining(["period", "salesFile", "minutesFiles", "remark"]),
    );
    const hasChinese = (s: unknown) => typeof s === "string" && /[一-龥]/.test(s);
    const check = (node: any) => {
      expect(hasChinese(node.title)).toBe(true);
      expect(hasChinese(node.description)).toBe(true);
      for (const child of Object.values(node.properties ?? {})) check(child);
    };
    for (const node of Object.values(props)) check(node);
  });

  test("统计周期必填；销售明细不必填（缺失由准入校验暂停，对应 A-03）", () => {
    expect(form.required).toContain("period");
    expect(form.required).not.toContain("salesFile");
  });

  test("合法表单通过，周次越界或缺统计周期被拒", () => {
    const validate = newAjv().compile(form);
    expect(validate({ period: { year: 2026, week: 44 }, salesFile: "销售明细.xlsx", minutesFiles: ["周会纪要_2026W44.docx"] })).toBe(true);
    expect(validate({ period: { year: 2026, week: 54 } })).toBe(false);
    expect(validate({ salesFile: "销售明细.xlsx" })).toBe(false);
  });

  test("form.ui.json 的提示只指向存在的字段", () => {
    const ui = readJson("form.ui.json");
    const props = Object.keys(form.properties);
    for (const key of Object.keys(ui)) {
      if (!key.startsWith("ui:")) expect(props).toContain(key);
    }
  });
});

describe("输出 Schema", () => {
  const draft: DraftReport = {
    title: "经营周报",
    sections: [
      { id: "summary", heading: "本周摘要", paragraphs: ["本周全公司实收 {{company.revenue}}，环比 {{company.wow}}。"] },
      { id: "next_week", heading: "下周关注事项", paragraphs: [], bullets: ["跟进华南活动结束后的回落。"] },
    ],
    causes: [
      {
        anomalyId: "store:HZ01",
        cause: "商场消防改造停业三天",
        level: "store",
        sourceWeek: { year: 2026, week: 44 },
        quote: "因商场消防改造停业三天",
      },
      { anomalyId: "region:华南", cause: "原因待核实", level: "unknown" },
    ],
    revisionNotes: [{ comment: "摘要太长", sections: ["summary"], change: "压缩为三句" }],
  };

  test("write_report 的结构与契约 DraftReport 一致", () => {
    const validate = newAjv().compile(readJson("schemas/write_report.json"));
    expect(validate(draft)).toBe(true);
    const { revisionNotes: _omit, ...withoutNotes } = draft;
    expect(validate(withoutNotes)).toBe(true);
    expect(validate({ ...draft, sections: undefined })).toBe(false);
    expect(validate({ ...draft, causes: [{ anomalyId: "x", cause: "y", level: "city" }] })).toBe(false);
    expect(validate({ ...draft, extra: 1 })).toBe(false);
  });

  test("match_causes 给出与 DraftReport.causes 同形的原因，以及可核验说法", () => {
    const validate = newAjv().compile(readJson("schemas/match_causes.json"));
    expect(
      validate({
        causes: draft.causes,
        claims: [
          {
            subject: { level: "region", key: "华南" },
            metric: "wow",
            direction: "flat",
            sourceWeek: { year: 2026, week: 44 },
            quote: "本周华南整体销售与上周基本持平",
          },
        ],
      }),
    ).toBe(true);
    expect(validate({ causes: draft.causes })).toBe(false);
    expect(validate({ causes: [{ anomalyId: "a", cause: "b", level: "galaxy" }], claims: [] })).toBe(false);
  });

  test("interpret_feedback 没有退回意见时可以提交空清单", () => {
    const validate = newAjv().compile(readJson("schemas/interpret_feedback.json"));
    expect(validate({ items: [] })).toBe(true);
    expect(
      validate({
        items: [
          { id: "F1", comment: "摘要太长", sections: ["summary"], kind: "narrative", action: "压缩摘要", needsHuman: false },
        ],
      }),
    ).toBe(true);
    expect(validate({ items: [{ id: "F1" }] })).toBe(false);
  });
});

describe("SKILL.md 与提示词", () => {
  const skill = read("SKILL.md");

  test("SKILL.md 写清关键规则", () => {
    for (const phrase of ["槽位", "原因待核实", "以数据为准", "纪要", "客单价", "同比", "{{", "不得推测"]) {
      expect(skill).toContain(phrase);
    }
    expect(skill).toMatch(/第\s*\d+\s*周|周次/);
  });

  test("每个判断步骤的提示词要求通过 submit_result 提交，并写明输入", () => {
    for (const step of judgeSteps) {
      const text = read(step.prompt);
      expect(text).toContain("submit_result");
      expect(text).toContain("输入");
    }
  });

  test("提示词里的槽位示例都从 WeeklyFacts 的真实字段出发", () => {
    const roots = new Set(["company", "regions", "stores", "categories", "topProducts", "quality", "trend", "period", "dataCoverage"]);
    const slots = [...[skill, ...judgeSteps.map((s) => read(s.prompt))].join("\n").matchAll(/\{\{([^}]+)\}\}/g)];
    expect(slots.length).toBeGreaterThan(0);
    for (const [, path] of slots) {
      const raw = path!.trim();
      if (raw.includes("<") || raw === "路径") continue; // 语法说明里的占位写法
      expect(roots.has(raw.split(".")[0]!)).toBe(true);
    }
  });

  test("write_report 提示词列出全部章节标题", () => {
    const text = read("prompts/write_report.md");
    for (const section of qc.builtin.structure.sections) expect(text).toContain(section.heading);
  });

  test("前台 Agent 人设：不能推进流程，只填表单草稿、回答任务问题", () => {
    const text = read("frontdesk.md");
    for (const phrase of ["前台 Agent", "表单草稿", "不能", "流程卡片", "按钮"]) expect(text).toContain(phrase);
  });
});

describe("术语", () => {
  // GLOSSARY.md 中标注 _Avoid_ 的说法；只列出不会在正常行文里另有含义的词
  const avoided = [
    "销售额", "营收", "GMV", "报告期", "工单", "助手", "客服", "用户", "申请人", "审批人",
    "管理员", "运维", "挂起", "阻塞", "驳回", "打回", "重跑", "关店", "闭店", "漏数", "空数据",
    "引文", "产物", "结果文件", "确定性步骤", "AI 步骤", "LLM 步骤", "机器人", "转人工",
  ];

  test("员工配置的中文文本不使用术语表禁用的说法", () => {
    const hits: string[] = [];
    for (const file of walk(DIR)) {
      const text = readFileSync(file, "utf8");
      for (const word of avoided) if (text.includes(word)) hits.push(`${relative(DIR, file)}: ${word}`);
    }
    expect(hits).toEqual([]);
  });
});
